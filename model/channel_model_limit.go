package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"

	"gorm.io/gorm"
)

var (
	// ErrModelMappingInvalid 是 model_mapping 不是合法 JSON 对象时的错误，
	// 文案沿用中继里原来的 "unmarshal_model_mapping_failed"。
	ErrModelMappingInvalid = errors.New("unmarshal_model_mapping_failed")
	// ErrModelMappingCycle 是 model_mapping 出现 a→b→a 这类环时的错误。
	ErrModelMappingCycle = errors.New("model_mapping_contains_cycle")
)

// ResolveMappedModel 沿渠道的 model_mapping 链一路走到链尾，返回真正发给上游的
// 模型名。某一步查不到原名时，会去掉推理后缀（如 -thinking）再查一次。
// mapped 为 false 表示没有发生重定向（没配映射、没命中，或第一步就映射回自己）。
// 出错时 upstream 仍返回 originModel，调用方忽略错误也能拿到可用的名字。
//
// 中继（relay/helper.ModelMappedHelper）、<渠道, 模型> 额度的读写和容量计数
// 都用这一个函数，保证配置额度用的键与请求实际打到的上游模型一致。
func ResolveMappedModel(modelMappingRaw string, originModel string) (upstream string, mapped bool, err error) {
	if modelMappingRaw == "" || modelMappingRaw == "{}" {
		return originModel, false, nil
	}
	modelMap := make(map[string]string)
	if err := common.Unmarshal([]byte(modelMappingRaw), &modelMap); err != nil {
		return originModel, false, ErrModelMappingInvalid
	}
	current := originModel
	visited := map[string]bool{current: true}
	for {
		next, exists := modelMap[current]
		if base := hostreasoning.BaseModelName(current); (!exists || next == "") && base != current {
			next, exists = modelMap[base]
		}
		if !exists || next == "" {
			break
		}
		if visited[next] {
			// 映射回自己（a→a，或 a→b→b）不算环，停在当前位置。
			if next == current {
				break
			}
			return originModel, false, ErrModelMappingCycle
		}
		visited[next] = true
		current = next
		mapped = true
	}
	return current, mapped, nil
}

// SetChannelModelLimit 写入渠道对一个公开模型的 RPM / TPM / 并发额度。
// 额度按 model_mapping 链尾的上游模型名存放，三项全为 0 表示清除。
// 只有调度器会据此准入，网关这边只统计用量。
func SetChannelModelLimit(channelID int, publicModel string, limit dto.ModelLimit) error {
	publicModel = strings.TrimSpace(publicModel)
	if channelID <= 0 || publicModel == "" {
		return errors.New("channel_id and model are required")
	}
	if err := limit.Validate(); err != nil {
		return err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var row struct {
			Models       string
			ModelMapping *string
			Setting      *string
		}
		if err := lockForUpdate(tx).Model(&Channel{}).
			Select("models", "model_mapping", "setting").
			Where("id = ?", channelID).
			Take(&row).Error; err != nil {
			return fmt.Errorf("channel #%d not found", channelID)
		}
		served := slices.ContainsFunc(strings.Split(row.Models, ","), func(model string) bool {
			return strings.TrimSpace(model) == publicModel
		})
		if !served {
			return fmt.Errorf("channel #%d does not serve model %q", channelID, publicModel)
		}
		setting := dto.ChannelSettings{}
		if row.Setting != nil && strings.TrimSpace(*row.Setting) != "" {
			if err := common.UnmarshalJsonStr(*row.Setting, &setting); err != nil {
				return fmt.Errorf("invalid channel setting: %w", err)
			}
		}
		modelMapping := ""
		if row.ModelMapping != nil {
			modelMapping = *row.ModelMapping
		}
		upstream, _, err := ResolveMappedModel(modelMapping, publicModel)
		if err != nil {
			return fmt.Errorf("channel #%d model_mapping: %w", channelID, err)
		}
		if setting.CapacityPoolFor(upstream) != 0 {
			return ErrModelLimitManagedByCapacityPool
		}
		if limit.IsZero() {
			delete(setting.ModelLimits, upstream)
			if len(setting.ModelLimits) == 0 {
				setting.ModelLimits = nil
			}
		} else {
			if setting.ModelLimits == nil {
				setting.ModelLimits = make(map[string]dto.ModelLimit)
			}
			setting.ModelLimits[upstream] = limit
		}
		encoded, err := common.Marshal(setting)
		if err != nil {
			return err
		}
		return tx.Model(&Channel{}).Where("id = ?", channelID).Update("setting", string(encoded)).Error
	})
	if err != nil {
		return err
	}
	InitChannelCache()
	return nil
}

// ChannelModelCapacity 是渠道对一个公开模型的容量配置。
type ChannelModelCapacity struct {
	// UpstreamModel 是 model_mapping 链尾的上游模型名，额度和计数都按它。
	UpstreamModel string
	// Limit 是渠道自己配的额度（setting.model_limits）；绑了容量池时不生效。
	Limit dto.ModelLimit
	// CapacityPoolId 是绑定的容量池，0 表示没绑。
	CapacityPoolId int
}

// ChannelModelCapacityOf 返回渠道对一个公开模型的容量配置。
// 入参是渠道的 setting 与 model_mapping 原文：不走 Channel.GetSetting，因为它在
// JSON 解析失败时会把渠道写回数据库。setting 解析失败时按未配置（全不限、没绑池）处理；
// model_mapping 有环时按公开名查，与中继报错前的状态一致，额度不会命中。
func ChannelModelCapacityOf(settingRaw, modelMappingRaw, publicModel string) ChannelModelCapacity {
	upstream, _, _ := ResolveMappedModel(modelMappingRaw, publicModel)
	capacity := ChannelModelCapacity{UpstreamModel: upstream}
	setting := dto.ChannelSettings{}
	if strings.TrimSpace(settingRaw) != "" {
		if err := common.UnmarshalJsonStr(settingRaw, &setting); err != nil {
			return capacity
		}
	}
	capacity.Limit = setting.ModelLimitFor(upstream)
	capacity.CapacityPoolId = setting.CapacityPoolFor(upstream)
	return capacity
}

// ChannelCapacityModel 返回请求在这个渠道上按哪个公开名算额度和计数：渠道列了请求名、
// 或 model_mapping 认这个名字时就是请求名；否则是原生选路退到的路由名（去掉 @ 修饰和
// 推理强度后缀）。所以渠道只列了 gpt-5 时，gpt-5-high 与 gpt-5 计进同一个池、受同一份
// model_limits 约束，不会多出一个能力页上看不到、也没配额度的池。结果再交给
// ChannelModelCapacityOf / ResolveMappedModel 走映射链。
func ChannelCapacityModel(channelModels []string, modelMappingRaw, requestModel string) string {
	if slices.Contains(channelModels, requestModel) {
		return requestModel
	}
	if _, mapped, _ := ResolveMappedModel(modelMappingRaw, requestModel); mapped {
		return requestModel
	}
	if routing := ratio_setting.RoutingMatchModelName(requestModel); slices.Contains(channelModels, routing) {
		return routing
	}
	return requestModel
}
