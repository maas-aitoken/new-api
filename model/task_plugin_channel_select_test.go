package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPluginChannelSelectionFiltersBothCachePaths(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://example.com"
	alphaSetting := `{"task_plugin_key":"alpha"}`
	betaSetting := `{"task_plugin_key":"beta"}`
	channels := []Channel{
		{Id: 900001, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "alpha", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &alphaSetting},
		{Id: 900002, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: "beta", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &betaSetting},
		{Id: 900003, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "ordinary", Models: "ordinary", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900004, Type: constant.ChannelTypeKling, Status: common.ChannelStatusEnabled, Name: "legacy-alpha", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
		{Id: 900005, Type: constant.ChannelTypeJimeng, Status: common.ChannelStatusEnabled, Name: "legacy-beta", Models: "legacy", Group: "default", Priority: &priority, Weight: &weight},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	selected, err := GetChannel("default", "shared", 0, identityFilters("alpha", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "alpha", selected.Name)
	selected, err = GetChannel("default", "shared", 0, identityFilters("", nil))
	require.NoError(t, err)
	assert.Nil(t, selected)
	selected, err = GetChannel("default", "ordinary", 0, identityFilters("", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "ordinary", selected.Name)
	selected, err = GetChannel("default", "legacy", 0, identityFilters("legacy-alpha", []int{constant.ChannelTypeKling}))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "legacy-alpha", selected.Name)
	selected, err = GetChannel("default", "legacy", 0, identityFilters("legacy-alpha", []int{constant.ChannelTypeKling, constant.ChannelTypeJimeng}))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Contains(t, []string{"legacy-alpha", "legacy-beta"}, selected.Name)
}

func identityFilters(key string, channelTypes []int) []dto.ChannelFilter {
	return []dto.ChannelFilter{{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          key,
		TaskPluginChannelTypes: channelTypes,
	}}
}

func TestSharedPluginKeysFilterBothChannelSources(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	filters := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "alpha", TaskPluginKeys: []string{"alpha", "beta"}}}
	var abilities []Ability
	for index, key := range []string{"alpha", "beta", "unrelated"} {
		setting := `{"task_plugin_key":"` + key + `"}`
		channel := Channel{Id: 910001 + index, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Name: key, Models: "shared", Group: "default", Setting: &setting}
		require.NoError(t, channel.Insert())
		abilities = append(abilities, Ability{ChannelId: channel.Id, Model: "shared", Group: "default", Enabled: true})
		matches, _ := ChannelSatisfiesFilters(&channel, "shared", filters)
		assert.Equal(t, index < 2, matches)
	}
	assert.Equal(t, abilities[:2], filterAbilitiesByConstraints(abilities, "shared", filters))
	InitChannelCache()
	channelSyncLock.RLock()
	kept, emptied := filterCandidateIDs([]int{910001, 910002, 910003}, "shared", filters)
	channelSyncLock.RUnlock()
	assert.Equal(t, []int{910001, 910002}, kept)
	assert.Empty(t, emptied)
}

func TestNewAPIChannelServesExtendedTaskPlugins(t *testing.T) {
	truncateTables(t)
	priority := int64(0)
	weight := uint(1)
	baseURL := "https://gateway.example"
	extended := `{"task_extend_plugin_keys":["alpha","gamma"]}`
	single := `{"task_plugin_key":"delta"}`
	channels := []Channel{
		{Id: 920001, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway", Models: "shared,chat", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &extended},
		{Id: 920002, Type: constant.ChannelTypeNewAPI, Status: common.ChannelStatusEnabled, Name: "gateway-single", Models: "shared", Group: "default", Priority: &priority, Weight: &weight, BaseURL: &baseURL, Setting: &single},
	}
	for i := range channels {
		require.NoError(t, channels[i].Insert())
	}

	selected, err := GetChannel("default", "shared", 0, identityFilters("alpha", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name)
	selected, err = GetChannel("default", "shared", 0, identityFilters("delta", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway-single", selected.Name, "a single task_plugin_key still binds a New API channel")
	selected, err = GetChannel("default", "shared", 0, identityFilters("beta", nil))
	require.NoError(t, err)
	assert.Nil(t, selected, "unbound plugins never reach the gateway channel")
	shared := []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "beta", TaskPluginKeys: []string{"beta", "gamma"}}}
	selected, err = GetChannel("default", "shared", 0, shared)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name, "a bound shared-model candidate admits the channel")
	selected, err = GetChannel("default", "chat", 0, identityFilters("", nil))
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, "gateway", selected.Name, "requests without a pinned plugin keep using the gateway")
}

// 首次尝试的渠道分布与实际抽签同一套规则：分组按顺序取第一个筛选后还有渠道的，只看最高
// 优先级那一档，按权重分（全 0 时平均分）；没有内存缓存时按数据库路径的权重 + 10。
func TestFirstAttemptChannelSharesFollowsSelectionRules(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	high, low := int64(10), int64(0)
	weight30, weight10, weight0 := uint(30), uint(10), uint(0)
	for _, channel := range []Channel{
		{Id: 940001, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "top-a", Models: "m", Group: "vip", Priority: &high, Weight: &weight30},
		{Id: 940002, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "top-b", Models: "m", Group: "vip", Priority: &high, Weight: &weight10},
		{Id: 940003, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "lower", Models: "m", Group: "vip", Priority: &low, Weight: &weight30},
		{Id: 940004, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "zero-a", Models: "z", Group: "default", Priority: &low, Weight: &weight0},
		{Id: 940005, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "zero-b", Models: "z", Group: "default", Priority: &low, Weight: &weight0},
	} {
		require.NoError(t, channel.Insert())
	}
	sharesOf := func(shares []ChannelShare) map[int]float64 {
		out := map[int]float64{}
		for _, share := range shares {
			out[share.Channel.Id] = share.Probability
		}
		return out
	}
	onlyTopB := []dto.ChannelFilter{{Kind: dto.FilterCapability, AllowedChannelIDs: []int{940002, 940003}}}

	common.MemoryCacheEnabled = true
	InitChannelCache()
	group, shares := FirstAttemptChannelShares([]string{"default", "vip"}, "m", nil)
	assert.Equal(t, "vip", group, "the first group that serves the model wins")
	assert.InDeltaMapValues(t, map[int]float64{940001: 0.75, 940002: 0.25}, sharesOf(shares), 1e-9, "only the top priority tier, by weight")
	_, shares = FirstAttemptChannelShares([]string{"default"}, "z", nil)
	assert.InDeltaMapValues(t, map[int]float64{940004: 0.5, 940005: 0.5}, sharesOf(shares), 1e-9, "all-zero weights split evenly")
	_, shares = FirstAttemptChannelShares([]string{"vip"}, "m", onlyTopB)
	assert.InDeltaMapValues(t, map[int]float64{940002: 1}, sharesOf(shares), 1e-9, "filters apply before the tier is chosen")
	group, shares = FirstAttemptChannelShares([]string{"default"}, "m", nil)
	assert.Empty(t, group)
	assert.Empty(t, shares)
	selected, err := GetRandomSatisfiedChannel("vip", "m", 0, onlyTopB)
	require.NoError(t, err)
	assert.Equal(t, 940002, selected.Id, "the real draw uses the same tier")
	for _, retry := range []int{1, 5} {
		selected, err = GetRandomSatisfiedChannel("vip", "m", retry, nil)
		require.NoError(t, err)
		assert.Equal(t, 940003, selected.Id, "retry %d moves to the lower tier and stays on the lowest", retry)
	}

	common.MemoryCacheEnabled = false
	_, shares = FirstAttemptChannelShares([]string{"vip"}, "m", nil)
	assert.InDeltaMapValues(t, map[int]float64{940001: 40.0 / 60, 940002: 20.0 / 60}, sharesOf(shares), 1e-9, "database path weighs weight+10")
}

func TestModelModifierNamesRouteWithAndWithoutMemoryCache(t *testing.T) {
	truncateTables(t)
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = originalMemoryCache; InitChannelCache() })
	channel := Channel{Id: 930001, Type: constant.ChannelTypeDeepSeek, Status: common.ChannelStatusEnabled, Name: "deepseek", Models: "deepseek-flash", Group: "default"}
	require.NoError(t, channel.Insert())

	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		InitChannelCache()
		selected, err := GetRandomSatisfiedChannel("default", "deepseek-flash@effort:max", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, selected, "memory cache %v", memoryCache)
		assert.Equal(t, "deepseek", selected.Name)
		selected, err = GetRandomSatisfiedChannel("default", "deepseek-pro@effort:max", 0, nil)
		require.NoError(t, err)
		assert.Nil(t, selected, "memory cache %v", memoryCache)
	}
}
