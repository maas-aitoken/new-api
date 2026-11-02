package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/capability"

	"github.com/gin-gonic/gin"
)

// GetModelCapabilityTargets 返回"模型 → 服务渠道"清单，供渠道能力设置页使用。
func GetModelCapabilityTargets(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil {
		channelID = 0
	}
	targets, total, err := model.GetCapabilityTargets(model.CapabilityTargetQuery{
		Keyword:   c.Query("keyword"),
		ChannelID: channelID,
		Offset:    pageInfo.GetStartIdx(),
		Limit:     pageInfo.GetPageSize(),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     targets,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}

type channelModelCapabilityRequest struct {
	ChannelId    int    `json:"channel_id"`
	Model        string `json:"model"`
	Capabilities string `json:"capabilities"`
}

// GetChannelModelCapability 返回渠道收窄、模型级基线与合并后的生效能力。
func GetChannelModelCapability(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil || channelID <= 0 {
		common.ApiErrorMsg(c, "channel_id is required")
		return
	}
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		common.ApiErrorMsg(c, "model is required")
		return
	}
	base, declared := model.ResolveModelCapability(modelName)
	if !declared {
		common.ApiErrorMsg(c, "model has no model-level capability declaration")
		return
	}
	over, configured := model.GetChannelModelCapability(channelID, modelName)
	effective := capability.Merge(base, over)
	baseRaw, _ := capability.Encode(base)
	effectiveRaw, _ := capability.Encode(effective)
	overRaw := ""
	if configured {
		overRaw, _ = capability.Encode(over)
	}
	costMultiplier := 0.0
	if channel, channelErr := model.GetChannelById(channelID, false); channelErr == nil && channel != nil {
		setting := ""
		if channel.Setting != nil {
			setting = *channel.Setting
		}
		costMultiplier = model.ChannelCostMultiplier(setting, channel.GetModelMapping(), modelName)
	}
	common.ApiSuccess(c, gin.H{
		"channel_id":         channelID,
		"model":              modelName,
		"configured":         configured,
		"capabilities":       overRaw,
		"model_capabilities": baseRaw,
		"effective":          effectiveRaw,
		"cost_multiplier":    costMultiplier,
	})
}

type channelModelCostMultiplierRequest struct {
	ChannelId  int     `json:"channel_id"`
	Model      string  `json:"model"`
	Multiplier float64 `json:"multiplier"`
}

// SaveChannelModelCostMultiplier 保存某个渠道模型的费用倍率，0 表示清除。
func SaveChannelModelCostMultiplier(c *gin.Context) {
	var request channelModelCostMultiplierRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.SetChannelModelCostMultiplier(request.ChannelId, request.Model, request.Multiplier); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

type channelModelLimitRequest struct {
	ChannelId      int    `json:"channel_id"`
	Model          string `json:"model"`
	RPM            int    `json:"rpm"`
	TPM            int    `json:"tpm"`
	MaxConcurrency int    `json:"max_concurrency"`
}

// SaveChannelModelLimit 保存某个渠道模型的 RPM / TPM / 并发额度，三项全 0 表示清除。
// 额度按该模型走完 model_mapping 链后的上游名存放，只有调度器据此准入。
func SaveChannelModelLimit(c *gin.Context) {
	var request channelModelLimitRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	limit := dto.ModelLimit{RPM: request.RPM, TPM: request.TPM, MaxConcurrency: request.MaxConcurrency}
	if err := model.SetChannelModelLimit(request.ChannelId, request.Model, limit); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// channelModelCapacityMaxIds 限制一次查询的渠道数，与渠道健康接口一致。
const channelModelCapacityMaxIds = 200

// remainingCapacity 是额度减去用量后的剩余，nil 表示这一项不限。
type remainingCapacity struct {
	RPM         *int64 `json:"rpm"`
	TPM         *int64 `json:"tpm"`
	Concurrency *int64 `json:"concurrency"`
}

func remainingOf(limit dto.ModelLimit, usage channelcapacity.Usage) remainingCapacity {
	var remaining remainingCapacity
	if limit.RPM > 0 {
		value := max(int64(limit.RPM)-usage.RPM, 0)
		remaining.RPM = &value
	}
	if limit.TPM > 0 {
		value := max(int64(limit.TPM)-usage.TPM, 0)
		remaining.TPM = &value
	}
	if limit.MaxConcurrency > 0 {
		value := max(int64(limit.MaxConcurrency)-usage.InFlight, 0)
		remaining.Concurrency = &value
	}
	return remaining
}

type channelModelCapacityItem struct {
	ChannelId     int               `json:"channel_id"`
	Model         string            `json:"model"`
	UpstreamModel string            `json:"upstream_model"`
	PoolId        string            `json:"pool_id"`
	Limit         dto.ModelLimit    `json:"limit"`
	RPMUsed       int64             `json:"rpm_used"`
	TPMUsed       int64             `json:"tpm_used"`
	InFlight      int64             `json:"in_flight"`
	Remaining     remainingCapacity `json:"remaining"`
}

// GetChannelModelCapacity 返回一个模型在若干渠道上的实时用量、额度和剩余量：
// GET /api/channel-model-capabilities/capacity?model=<公开名>&channel_ids=1,2
// tpm_used 中在途请求只含输入估算。shared=false 表示没配 Redis，只反映本节点。
func GetChannelModelCapacity(c *gin.Context) {
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		common.ApiErrorMsg(c, "model is required")
		return
	}
	parts := strings.Split(strings.TrimSpace(c.Query("channel_ids")), ",")
	if len(parts) > channelModelCapacityMaxIds {
		common.ApiErrorMsg(c, "too many channel_ids")
		return
	}
	items := make([]channelModelCapacityItem, 0, len(parts))
	poolIDs := make([]string, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		channelID, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || channelID <= 0 || seen[channelID] {
			continue
		}
		seen[channelID] = true
		channel, err := model.CacheGetChannel(channelID)
		if err != nil || channel == nil {
			continue
		}
		settingRaw := ""
		if channel.Setting != nil {
			settingRaw = *channel.Setting
		}
		capacity := model.ChannelModelCapacityOf(settingRaw, channel.GetModelMapping(), modelName)
		poolID := channelcapacity.PoolID(channelID, capacity.UpstreamModel)
		items = append(items, channelModelCapacityItem{
			ChannelId: channelID, Model: modelName, UpstreamModel: capacity.UpstreamModel, PoolId: poolID, Limit: capacity.Limit,
		})
		poolIDs = append(poolIDs, poolID)
	}
	usages, err := channelcapacity.Snapshot(poolIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for i := range items {
		usage := usages[items[i].PoolId]
		items[i].RPMUsed, items[i].TPMUsed, items[i].InFlight = usage.RPM, usage.TPM, usage.InFlight
		items[i].Remaining = remainingOf(items[i].Limit, usage)
	}
	common.ApiSuccess(c, gin.H{
		"items":          items,
		"shared":         channelcapacity.Shared(),
		"window_seconds": 60,
	})
}

// SaveChannelModelCapability 保存一条渠道能力收窄。
func SaveChannelModelCapability(c *gin.Context) {
	var request channelModelCapabilityRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.SetChannelModelCapability(request.ChannelId, request.Model, request.Capabilities); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// DeleteChannelModelCapability 删除一条渠道能力收窄，回到继承模型级声明。
func DeleteChannelModelCapability(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil || channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "channel_id is required"})
		return
	}
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "model is required"})
		return
	}
	if err := model.DeleteChannelModelCapability(channelID, modelName); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
