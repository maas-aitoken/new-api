package model

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*relaydto.AdvancedCustomConfig
var channel2disabledModels map[int]map[string]bool
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if err := InitCapabilityCache(); err != nil {
		common.SysError(fmt.Sprintf("failed to init capability cache: %v", err))
	}
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*relaydto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	var disabledModels []ChannelModelStatus
	DB.Find(&disabledModels)
	newChannel2disabledModels := make(map[int]map[string]bool)
	for _, status := range disabledModels {
		if newChannel2disabledModels[status.ChannelId] == nil {
			newChannel2disabledModels[status.ChannelId] = make(map[string]bool)
		}
		newChannel2disabledModels[status.ChannelId][status.Model] = true
	}
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.SplitSeq(channel.Group, ",")
		for group := range groups {
			models := channel.GetModels()
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channel2disabledModels = newChannel2disabledModels
	channelSyncLock.Unlock()
	rebuildTaskAliasView()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog(common.LogText("channels synced from database"))
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog(common.LogText("syncing channels from database"))
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, filters []dto.ChannelFilter) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	targetChannels, err := retryTierLocked(group, model, retry, filters)
	if err != nil || len(targetChannels) == 0 {
		return nil, err
	}
	if len(targetChannels) == 1 {
		return targetChannels[0], nil
	}
	sumWeight := 0
	for _, channel := range targetChannels {
		sumWeight += channel.GetWeight()
	}

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, channel := range targetChannels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

// retryTierLocked 返回第 retry 次尝试要从中抽签的那一档渠道：按过滤条件筛出该分组下服务
// 这个模型的渠道（精确名没有时退到去掉 @ 修饰等后缀的路由名），再取第 retry 高的优先级，
// 超出档数时取最低一档。实际抽签（GetRandomSatisfiedChannel）和首次渠道分布估算
// （FirstAttemptChannelShares）共用它，两边的选渠道规则不会走散。调用方持有 channelSyncLock。
func retryTierLocked(group string, model string, retry int, filters []dto.ChannelFilter) ([]*Channel, error) {
	channelIDs, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)
	if len(channelIDs) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channelIDs, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}
	if len(channelIDs) == 0 {
		return nil, nil
	}
	candidates := make([]*Channel, 0, len(channelIDs))
	seen := make(map[int64]bool)
	priorities := make([]int64, 0, len(channelIDs))
	for _, channelId := range channelIDs {
		channel, ok := channelsIDM[channelId]
		if !ok {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
		candidates = append(candidates, channel)
		if priority := channel.GetPriority(); !seen[priority] {
			seen[priority] = true
			priorities = append(priorities, priority)
		}
	}
	slices.Sort(priorities)
	slices.Reverse(priorities)
	targetPriority := priorities[min(retry, len(priorities)-1)]
	tier := make([]*Channel, 0, len(candidates))
	for _, channel := range candidates {
		if channel.GetPriority() == targetPriority {
			tier = append(tier, channel)
		}
	}
	return tier, nil
}

// ChannelShare 是一次请求的首次尝试落到某个渠道的概率。
type ChannelShare struct {
	Channel     *Channel
	Probability float64
}

// FirstAttemptChannelShares 估算一次请求的首次尝试会按什么概率落到哪些渠道，只读、不抽签。
// 规则与实际选渠道一致：groups 按顺序取第一个筛选后还有渠道的分组，在它最高优先级那一档里
// 按权重分配，权重全为 0 时平均分；没有内存缓存时按数据库路径的权重（权重 + 10）分配。
// 不含会话亲和：亲和命中时首次必落亲和渠道，由调用方先判断。返回命中的分组；
// 没有任何分组有可用渠道时返回空串和 nil。
func FirstAttemptChannelShares(groups []string, model string, filters []dto.ChannelFilter) (string, []ChannelShare) {
	if !common.MemoryCacheEnabled {
		for _, group := range groups {
			abilities, err := databaseRetryTier(group, model, 0, filters)
			if err != nil {
				continue
			}
			tier := make([]*Channel, 0, len(abilities))
			weights := make([]float64, 0, len(abilities))
			for _, ability := range abilities {
				if channel, err := CacheGetChannel(ability.ChannelId); err == nil && channel != nil {
					tier = append(tier, channel)
					weights = append(weights, float64(ability.Weight)+10)
				}
			}
			if len(tier) > 0 {
				return group, sharesByWeight(tier, weights)
			}
		}
		return "", nil
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	for _, group := range groups {
		tier, err := retryTierLocked(group, model, 0, filters)
		if err != nil || len(tier) == 0 {
			continue
		}
		weights := make([]float64, len(tier))
		for i, channel := range tier {
			weights[i] = float64(channel.GetWeight())
		}
		return group, sharesByWeight(tier, weights)
	}
	return "", nil
}

// sharesByWeight 按权重把概率分给这一档的渠道，权重全为 0 时平均分。
func sharesByWeight(tier []*Channel, weights []float64) []ChannelShare {
	total := 0.0
	for _, weight := range weights {
		total += weight
	}
	shares := make([]ChannelShare, len(tier))
	for i, channel := range tier {
		probability := 1 / float64(len(tier))
		if total > 0 {
			probability = weights[i] / total
		}
		shares[i] = ChannelShare{Channel: channel, Probability: probability}
	}
	return shares
}

// filterChannelsByRequestPathAndModel restricts candidates by request path and
// model. Only Advanced Custom (type 58) channels are path-checked: they are kept
// only when one of their configured routes matches requestPath and model. All
// other channel types always pass. When requestPath is empty, filtering is skipped.
// Caller must hold channelSyncLock (read lock). The cached slice is never mutated.
func filterChannelsByRequestPathAndModel(channels []int, requestPath string, model string) []int {
	if len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelId := range channels {
		if isChannelModelDisabledLocked(channelId, model) {
			continue
		}
		channel, ok := channelsIDM[channelId]
		if !ok {
			// keep it so the downstream consistency error is raised as before
			filtered = append(filtered, channelId)
			continue
		}
		if requestPath == "" || channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, channelId)
			continue
		}
		if config := channel2advancedCustomConfig[channelId]; config != nil && config.SupportsPathForModel(requestPath, model) {
			filtered = append(filtered, channelId)
		}
	}
	return filtered
}

// Caller must hold channelSyncLock.
func isChannelModelDisabledLocked(channelID int, modelName string) bool {
	return operation_setting.IsChannelModelCircuitBreakerEnabled() &&
		!operation_setting.IsChannelModelCircuitBreakerExcluded(channelID) &&
		channel2disabledModels[channelID] != nil && channel2disabledModels[channelID][modelName]
}

func cacheSetChannelModelDisabled(channelID int, modelName string, disabled bool) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if disabled {
		if channel2disabledModels == nil {
			channel2disabledModels = make(map[int]map[string]bool)
		}
		if channel2disabledModels[channelID] == nil {
			channel2disabledModels[channelID] = make(map[string]bool)
		}
		channel2disabledModels[channelID][modelName] = true
		return
	}
	if channel2disabledModels[channelID] != nil {
		delete(channel2disabledModels[channelID], modelName)
		if len(channel2disabledModels[channelID]) == 0 {
			delete(channel2disabledModels, channelID)
		}
	}
}

func cacheClearChannelModelStatuses(channelID int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	delete(channel2disabledModels, channelID)
	channelSyncLock.Unlock()
}

func cachePruneChannelModelStatuses(channelID int, disabledModels []string, stale []string) {
	if !common.MemoryCacheEnabled {
		return
	}
	staleSet := make(map[string]bool, len(stale))
	for _, modelName := range stale {
		staleSet[modelName] = true
	}
	channelSyncLock.Lock()
	if channel2disabledModels == nil {
		channel2disabledModels = make(map[int]map[string]bool)
	}
	channel2disabledModels[channelID] = make(map[string]bool)
	for _, modelName := range disabledModels {
		if !staleSet[modelName] {
			channel2disabledModels[channelID][modelName] = true
		}
	}
	if len(channel2disabledModels[channelID]) == 0 {
		delete(channel2disabledModels, channelID)
	}
	channelSyncLock.Unlock()
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*relaydto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
