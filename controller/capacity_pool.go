package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// capacityPoolPayload 是管理页看到的一个容量池。
type capacityPoolPayload struct {
	Id          int                        `json:"id"`
	Name        string                     `json:"name"`
	Provider    string                     `json:"provider"`
	Config      model.CapacityPoolConfig   `json:"config"`
	ConfigError string                     `json:"config_error,omitempty"`
	Members     []model.CapacityPoolMember `json:"members"`
	UpdatedTime int64                      `json:"updated_time"`
}

// GetCapacityPools 返回一个 provider 的容量池和成员：
// GET /api/channel/resource_pools/capacity_pools?provider=claude
func GetCapacityPools(c *gin.Context) {
	provider := c.Query("provider")
	if !isValidResourcePoolProvider(provider) {
		common.ApiErrorMsg(c, "unsupported provider")
		return
	}
	pools, err := model.GetCapacityPools(provider)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	members, err := model.GetCapacityPoolMembers()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload := make([]capacityPoolPayload, 0, len(pools))
	for _, pool := range pools {
		item := capacityPoolPayload{
			Id:          pool.Id,
			Name:        pool.Name,
			Provider:    pool.Provider,
			Members:     members[pool.Id],
			UpdatedTime: pool.UpdatedTime,
		}
		if item.Members == nil {
			item.Members = []model.CapacityPoolMember{}
		}
		config, err := pool.ParseConfig()
		if err != nil {
			item.ConfigError = err.Error()
		}
		item.Config = config
		payload = append(payload, item)
	}
	common.ApiSuccess(c, payload)
}

type saveCapacityPoolRequest struct {
	Id       int                      `json:"id"`
	Name     string                   `json:"name"`
	Provider string                   `json:"provider"`
	Config   model.CapacityPoolConfig `json:"config"`
}

// SaveCapacityPool 新建（id 为 0）或更新一个容量池，保存后让主节点马上重算。
func SaveCapacityPool(c *gin.Context) {
	var request saveCapacityPoolRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	pool := &model.CapacityPool{Id: request.Id, Name: request.Name, Provider: request.Provider}
	if err := model.SaveCapacityPool(pool, request.Config); err != nil {
		common.ApiError(c, err)
		return
	}
	service.WakeCapacityPoolPoller()
	common.ApiSuccess(c, gin.H{"id": pool.Id})
}

// ResetCapacityPool 丢掉容量池学到的换算比例和上调平滑，主节点下一轮按账号现状重算并直接发布：
// POST /api/channel/resource_pools/capacity_pools/:id/reset。配置和成员不变。
func ResetCapacityPool(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid capacity pool id")
		return
	}
	if _, err := model.GetCapacityPoolById(id); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := channelcapacity.RequestReset(channelcapacity.CapacityPoolID(id)); err != nil {
		common.ApiError(c, err)
		return
	}
	service.WakeCapacityPoolPoller()
	common.ApiSuccess(c, nil)
}

// DeleteCapacityPool 删除容量池，成员回到各自的 model_limits。
func DeleteCapacityPool(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid capacity pool id")
		return
	}
	if err := model.DeleteCapacityPool(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

type capacityPoolMembersRequest struct {
	ChannelId int `json:"channel_id"`
	// Models 是渠道上的公开模型名，为空表示渠道的全部模型。
	Models []string `json:"models"`
}

// AddCapacityPoolMembers 把渠道的模型加进容量池，按 model_mapping 链尾的上游名绑定。
func AddCapacityPoolMembers(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid capacity pool id")
		return
	}
	var request capacityPoolMembersRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "invalid request body")
		return
	}
	added, err := model.AddCapacityPoolMembers(id, request.ChannelId, request.Models)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"added": added})
}

// RemoveCapacityPoolMember 从容量池移除一个 <渠道, 上游模型>：
// DELETE /api/channel/resource_pools/capacity_pools/:id/members?channel_id=1&upstream_model=x
func RemoveCapacityPoolMember(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "invalid capacity pool id")
		return
	}
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	upstream := strings.TrimSpace(c.Query("upstream_model"))
	if err != nil || channelID <= 0 || upstream == "" {
		common.ApiErrorMsg(c, "channel_id and upstream_model are required")
		return
	}
	if err := model.RemoveCapacityPoolMember(id, channelID, upstream); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// capacityPoolMemberUsage 是一个成员自己的用量（计在它的 <渠道, 上游模型> 池里）。
type capacityPoolMemberUsage struct {
	ChannelId     int    `json:"channel_id"`
	UpstreamModel string `json:"upstream_model"`
	RPMUsed       int64  `json:"rpm_used"`
	TPMUsed       int64  `json:"tpm_used"`
	InFlight      int64  `json:"in_flight"`
}

type capacityPoolCapacityItem struct {
	service.CapacityPoolLimitView
	Id        int                       `json:"id"`
	RPMUsed   int64                     `json:"rpm_used"`
	TPMUsed   int64                     `json:"tpm_used"`
	InFlight  int64                     `json:"in_flight"`
	Remaining remainingCapacity         `json:"remaining"`
	Members   []capacityPoolMemberUsage `json:"members"`
}

// GetCapacityPoolCapacity 返回一个 provider 下每个容量池此刻生效的额度、整池用量、剩余量
// 和每个成员各用了多少：GET /api/channel/resource_pools/capacity_pools/capacity?provider=claude
// shared=false 表示没配 Redis，用量和动态额度只反映本节点。
func GetCapacityPoolCapacity(c *gin.Context) {
	provider := c.Query("provider")
	if !isValidResourcePoolProvider(provider) {
		common.ApiErrorMsg(c, "unsupported provider")
		return
	}
	pools, err := model.GetCapacityPools(provider)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views, err := service.ResolveCapacityPoolLimits(pools)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	members, err := model.GetCapacityPoolMembers()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	poolIDs := make([]string, 0)
	for _, pool := range pools {
		poolIDs = append(poolIDs, channelcapacity.CapacityPoolID(pool.Id))
		for _, member := range members[pool.Id] {
			poolIDs = append(poolIDs, channelcapacity.PoolID(member.ChannelId, member.UpstreamModel))
		}
	}
	usages, err := channelcapacity.Snapshot(poolIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]capacityPoolCapacityItem, 0, len(pools))
	for _, pool := range pools {
		view := views[pool.Id]
		usage := usages[view.PoolID]
		item := capacityPoolCapacityItem{
			CapacityPoolLimitView: view,
			Id:                    pool.Id,
			RPMUsed:               usage.RPM,
			TPMUsed:               usage.TPM,
			InFlight:              usage.InFlight,
			Remaining:             remainingOf(view.Limit, view.Exhausted, usage),
			Members:               make([]capacityPoolMemberUsage, 0, len(members[pool.Id])),
		}
		for _, member := range members[pool.Id] {
			memberUsage := usages[channelcapacity.PoolID(member.ChannelId, member.UpstreamModel)]
			item.Members = append(item.Members, capacityPoolMemberUsage{
				ChannelId:     member.ChannelId,
				UpstreamModel: member.UpstreamModel,
				RPMUsed:       memberUsage.RPM,
				TPMUsed:       memberUsage.TPM,
				InFlight:      memberUsage.InFlight,
			})
		}
		items = append(items, item)
	}
	common.ApiSuccess(c, gin.H{
		"items":          items,
		"shared":         channelcapacity.Shared(),
		"window_seconds": 60,
	})
}
