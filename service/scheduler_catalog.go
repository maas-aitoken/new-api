package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// SchedulerCatalog is the credential-free Catalog contract consumed by the
// standalone Scheduler. Keep this DTO local to new-api so the Scheduler
// process never needs to import new-api's model package.
type SchedulerCatalog struct {
	Version   string              `json:"version"`
	Model     string              `json:"model"`
	Endpoints []SchedulerEndpoint `json:"endpoints"`
}

// SchedulerEndpoint 是一个渠道在这个模型上的端点。渠道内用哪个 key 由 new-api
// 自己轮询，调度器只排渠道；分组、能力在 new-api 收窄 allowed_channel_ids 时已经判断过，
// 不进 Catalog。
type SchedulerEndpoint struct {
	EndpointID string `json:"endpoint_id"`
	ChannelID  int    `json:"channel_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Enabled    bool   `json:"enabled"`
	Priority   int64  `json:"priority"`
	Weight     int64  `json:"weight"`
	// Limits 是 <渠道, 上游模型> 的额度（setting.model_limits），渠道所有 key 合计。
	Limits SchedulerLimits `json:"limits"`
	// CapacityPoolID 是 <渠道, 上游模型> 计数池，与 channel_capacity.PoolID 相同；
	// 调度器按它做准入，并按它读 new-api 写的实时用量。
	CapacityPoolID string `json:"capacity_pool_id,omitempty"`
}

type SchedulerLimits struct {
	RPM            int `json:"rpm"`
	TPM            int `json:"tpm,omitempty"`
	MaxConcurrency int `json:"max_concurrency"`
}

// BuildSchedulerCatalog reads only Channel configuration and emits one
// stable Endpoint per channel. Key material itself is never copied into
// the returned DTO. The model query is explicit to keep one Scheduler
// Catalog scoped to one request model, matching the current scheduler-v2
// contract.
func BuildSchedulerCatalog(modelName string) (SchedulerCatalog, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return SchedulerCatalog{}, fmt.Errorf("model is required")
	}
	channels, err := model.GetAllChannels(0, 0, true, true)
	if err != nil {
		return SchedulerCatalog{}, err
	}
	floors, err := model.GetCapacityPoolFloors()
	if err != nil {
		return SchedulerCatalog{}, err
	}
	return BuildSchedulerCatalogFromChannels(modelName, channels, floors)
}

// BuildSchedulerCatalogFromChannels is separated from the database query so
// the sensitive-field and key-availability rules can be tested without a
// running new-api database. capacityPoolFloors 是每个容量池的保底额度，
// 绑到池上的成员用它填 Limits；不在表里的池 id（池已被删）按没绑定处理。
func BuildSchedulerCatalogFromChannels(modelName string, channels []*model.Channel, capacityPoolFloors map[int]dto.ModelLimit) (SchedulerCatalog, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return SchedulerCatalog{}, fmt.Errorf("model is required")
	}
	endpoints := make([]SchedulerEndpoint, 0)
	for _, channel := range channels {
		if channel == nil || !channelSupportsModel(channel, modelName) {
			continue
		}
		// 渠道启用且至少有一个启用、非空的 key 才算可用。一个都没有时仍发一个停用的
		// 端点，调度器能在审计里看到这个渠道，但不会把它排进候选。
		enabled := false
		if channel.Status == common.ChannelStatusEnabled {
			for keyIndex, key := range channel.GetKeys() {
				if strings.TrimSpace(key) == "" {
					continue
				}
				if status, ok := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; ok && channel.ChannelInfo.IsMultiKey && status != common.ChannelStatusEnabled {
					continue
				}
				enabled = true
				break
			}
		}
		// 额度与计数池按 <渠道, 上游模型> 算，额度是整个渠道合计。
		// 绑了容量池的成员改用容量池计数，Limits 填池的保底额度（同一池的成员都一样）；
		// 动态额度不进 Catalog，调度器从 Redis 读，免得 Catalog 版本每分钟都变。
		settingRaw := ""
		if channel.Setting != nil {
			settingRaw = *channel.Setting
		}
		capacity := model.ChannelModelCapacityOf(settingRaw, channel.GetModelMapping(), modelName)
		limit := capacity.Limit
		poolID := channelcapacity.PoolID(channel.Id, capacity.UpstreamModel)
		if floor, bound := capacityPoolFloors[capacity.CapacityPoolId]; bound && capacity.CapacityPoolId != 0 {
			limit = floor
			poolID = channelcapacity.CapacityPoolID(capacity.CapacityPoolId)
		}
		endpoints = append(endpoints, SchedulerEndpoint{
			EndpointID:     fmt.Sprintf("new-api-ch-%d", channel.Id),
			ChannelID:      channel.Id,
			Provider:       constant.GetChannelTypeName(channel.Type),
			Model:          modelName,
			Enabled:        enabled,
			Priority:       channel.GetPriority(),
			Weight:         int64(channel.GetWeight()),
			Limits:         SchedulerLimits{RPM: limit.RPM, TPM: limit.TPM, MaxConcurrency: limit.MaxConcurrency},
			CapacityPoolID: poolID,
		})
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].EndpointID < endpoints[j].EndpointID })
	if len(endpoints) == 0 {
		return SchedulerCatalog{}, fmt.Errorf("no channel endpoint supports model %s", modelName)
	}
	// Hash the complete credential-free representation. The version is stable
	// while Channel/Key metadata is unchanged and changes whenever a relevant
	// field changes, without requiring a mutable counter in new-api.
	canonical := struct {
		Model     string              `json:"model"`
		Endpoints []SchedulerEndpoint `json:"endpoints"`
	}{Model: modelName, Endpoints: endpoints}
	data, err := json.Marshal(canonical)
	if err != nil {
		return SchedulerCatalog{}, err
	}
	digest := sha256.Sum256(data)
	return SchedulerCatalog{Version: "new-api-" + hex.EncodeToString(digest[:8]), Model: modelName, Endpoints: endpoints}, nil
}

func channelSupportsModel(channel *model.Channel, requested string) bool {
	for _, candidate := range channelModelNames(channel) {
		if candidate == requested {
			return true
		}
	}
	return false
}

func channelModelNames(channel *model.Channel) []string {
	models := normalizeModels(channel.GetModels())
	if mapping := strings.TrimSpace(channel.GetModelMapping()); mapping != "" {
		var aliases map[string]string
		if json.Unmarshal([]byte(mapping), &aliases) == nil {
			for source := range aliases {
				models = append(models, source)
			}
			models = normalizeModels(models)
		}
	}
	return models
}

func normalizeModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, value := range models {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
