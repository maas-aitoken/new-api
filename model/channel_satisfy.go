package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}
	if isChannelModelDisabledLocked(channelID, modelName) {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	if IsChannelModelDisabled(channelID, modelName) {
		return false
	}
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func isChannelIDInList(list []int, channelID int) bool {
	return slices.Contains(list, channelID)
}

// GetEnabledChannelIDsForGroupModel 返回分组里列了这个模型名的启用渠道（没有时退到
// FormatMatchingModelName 的通配名），扣掉熔断中的 <渠道, 模型>。它不像原生选路那样
// 去掉 @ 修饰和推理强度后缀，auto 判断候选"能不能服务"就靠这一点：gpt-5-high 这类
// 强度变体只有渠道真的列了才算可用，否则 auto 就用不带后缀的名字。一个请求实际能走
// 哪些渠道看 GetRoutableChannelIDsForGroupModel。返回的是快照副本，调用方可以再按
// 请求路径等条件过滤。
func GetEnabledChannelIDsForGroupModel(group, modelName string) []int {
	if group == "" || modelName == "" {
		return []int{}
	}
	var ids []int
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		if group2model2channels != nil {
			raw := group2model2channels[group][modelName]
			if len(raw) == 0 {
				normalized := ratio_setting.FormatMatchingModelName(modelName)
				if normalized != "" && normalized != modelName {
					raw = group2model2channels[group][normalized]
				}
			}
			for _, id := range raw {
				if !isChannelModelDisabledLocked(id, modelName) {
					ids = append(ids, id)
				}
			}
		}
		channelSyncLock.RUnlock()
	} else {
		DB.Model(&Ability{}).
			Where(commonGroupCol+" = ? and enabled = ? and model = ?", group, commonTrueVal, modelName).
			Pluck("channel_id", &ids)
		if len(ids) == 0 {
			normalized := ratio_setting.FormatMatchingModelName(modelName)
			if normalized != "" && normalized != modelName {
				DB.Model(&Ability{}).
					Where(commonGroupCol+" = ? and enabled = ? and model = ?", group, commonTrueVal, normalized).
					Pluck("channel_id", &ids)
			}
		}
		filtered := ids[:0]
		for _, id := range ids {
			if !IsChannelModelDisabled(id, modelName) {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	if ids == nil {
		return []int{}
	}
	return ids
}

// GetRoutableChannelIDsForGroupModel 返回原生选路在这个分组里会考虑的渠道，口径与
// GetRandomSatisfiedChannel 相同：先找列了请求原名的渠道，扣掉熔断后一个都不剩时，
// 再找列了路由名（RoutingMatchModelName，去掉 @ 修饰和推理强度后缀）的渠道；熔断
// 始终按请求原名判断。所以客户端发 gpt-5-high、渠道只列了 gpt-5 时也有渠道可走，
// 发给调度器的 allowed_channel_ids 与原生选路能选到的渠道一致。
// 关内存缓存时原生选路按路由名回退的那次查询不扣熔断，这里两次都扣：调度器选回来的
// 渠道还要过 IsChannelEnabledForGroupModel 的熔断复核，不扣会选中后再 503。
func GetRoutableChannelIDsForGroupModel(group, modelName string) []int {
	if group == "" || modelName == "" {
		return []int{}
	}
	names := []string{modelName}
	if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != "" && normalized != modelName {
		names = append(names, normalized)
	}
	for _, name := range names {
		var ids []int
		if common.MemoryCacheEnabled {
			channelSyncLock.RLock()
			for _, id := range group2model2channels[group][name] {
				if !isChannelModelDisabledLocked(id, modelName) {
					ids = append(ids, id)
				}
			}
			channelSyncLock.RUnlock()
		} else {
			DB.Model(&Ability{}).
				Where(commonGroupCol+" = ? and enabled = ? and model = ?", group, commonTrueVal, name).
				Pluck("channel_id", &ids)
			ids = slices.DeleteFunc(ids, func(id int) bool { return IsChannelModelDisabled(id, modelName) })
		}
		if len(ids) > 0 {
			return ids
		}
	}
	return []int{}
}
