package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSchedulerCatalogFromChannelsEmitsOneCredentialFreeEndpointPerChannel(t *testing.T) {
	priority := int64(7)
	weight := uint(3)
	setting := `{"model_limits":{"deepseek-v4-flash":{"rpm":120,"tpm":90000,"max_concurrency":20}}}`
	channel := &model.Channel{
		Id:       42,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "secret-one\nsecret-two",
		Status:   common.ChannelStatusEnabled,
		Models:   "deepseek-v4-flash,alias-model",
		Group:    "default,paid",
		Priority: &priority,
		Weight:   &weight,
		Setting:  &setting,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled},
		},
	}

	catalog, err := BuildSchedulerCatalogFromChannels("deepseek-v4-flash", []*model.Channel{channel}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Version)
	// 两个 key 只发一个端点，额度是整个渠道合计；渠道内用哪个 key 由 new-api 自己轮询。
	assert.Equal(t, []SchedulerEndpoint{{
		EndpointID:     "new-api-ch-42",
		ChannelID:      42,
		Provider:       constant.GetChannelTypeName(constant.ChannelTypeOpenAI),
		Model:          "deepseek-v4-flash",
		Enabled:        true,
		Priority:       priority,
		Weight:         int64(weight),
		Limits:         SchedulerLimits{RPM: 120, TPM: 90000, MaxConcurrency: 20},
		CapacityPoolID: channelcapacity.PoolID(42, "deepseek-v4-flash"),
	}}, catalog.Endpoints)
	data := string(mustJSON(t, catalog))
	if strings.Contains(data, "secret-one") || strings.Contains(data, "secret-two") || strings.Contains(data, "base_url") {
		t.Fatalf("catalog leaked credential/configuration: %s", data)
	}
}

func TestBuildSchedulerCatalogEnabledNeedsAnUsableKey(t *testing.T) {
	cases := []struct {
		name    string
		channel model.Channel
		want    bool
	}{
		{name: "single key", channel: model.Channel{Key: "k", Status: common.ChannelStatusEnabled}, want: true},
		{name: "channel disabled", channel: model.Channel{Key: "k", Status: common.ChannelStatusAutoDisabled}, want: false},
		{name: "no key", channel: model.Channel{Status: common.ChannelStatusEnabled}, want: false},
		{name: "every key disabled", channel: model.Channel{Key: "k1\nk2", Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusManuallyDisabled},
		}}, want: false},
		// 状态表里没登记的 key 按启用算，与 GetNextEnabledKey 一致；空白 key 不算。
		{name: "only the enabled key is blank", channel: model.Channel{Key: "k1\n  ", Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
		}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := tc.channel
			channel.Id = 3
			channel.Type = constant.ChannelTypeOpenAI
			channel.Models = "m"
			catalog, err := BuildSchedulerCatalogFromChannels("m", []*model.Channel{&channel}, nil)
			require.NoError(t, err)
			require.Len(t, catalog.Endpoints, 1)
			assert.Equal(t, tc.want, catalog.Endpoints[0].Enabled)
		})
	}
}

func TestBuildSchedulerCatalogPoolsByMappedUpstreamModel(t *testing.T) {
	mapping := `{"alias":"upstream-x"}`
	setting := `{"model_limits":{"upstream-x":{"rpm":30}}}`
	channel := &model.Channel{
		Id: 5, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled,
		Models: "alias,upstream-x,other", ModelMapping: &mapping, Setting: &setting,
	}

	viaAlias, err := BuildSchedulerCatalogFromChannels("alias", []*model.Channel{channel}, nil)
	require.NoError(t, err)
	direct, err := BuildSchedulerCatalogFromChannels("upstream-x", []*model.Channel{channel}, nil)
	require.NoError(t, err)
	other, err := BuildSchedulerCatalogFromChannels("other", []*model.Channel{channel}, nil)
	require.NoError(t, err)

	// 别名与上游名打到同一个上游模型，共用一个池和一份额度；别的模型各算各的、不限。
	assert.Equal(t, viaAlias.Endpoints[0].CapacityPoolID, direct.Endpoints[0].CapacityPoolID)
	assert.Equal(t, SchedulerLimits{RPM: 30}, viaAlias.Endpoints[0].Limits)
	assert.Equal(t, SchedulerLimits{RPM: 30}, direct.Endpoints[0].Limits)
	assert.NotEqual(t, viaAlias.Endpoints[0].CapacityPoolID, other.Endpoints[0].CapacityPoolID)
	assert.Equal(t, SchedulerLimits{}, other.Endpoints[0].Limits)

	// 额度变化要换 Catalog 版本，调度器才会重新加载。
	changed := `{"model_limits":{"upstream-x":{"rpm":31}}}`
	channel.Setting = &changed
	reloaded, err := BuildSchedulerCatalogFromChannels("alias", []*model.Channel{channel}, nil)
	require.NoError(t, err)
	assert.NotEqual(t, viaAlias.Version, reloaded.Version)
}

func TestBuildSchedulerCatalogCountsBoundMembersInCapacityPool(t *testing.T) {
	mapping := `{"alias":"upstream-x"}`
	bound := `{"model_limits":{"upstream-x":{"rpm":30}},"capacity_pools":{"upstream-x":3}}`
	dangling := `{"model_limits":{"upstream-x":{"rpm":7}},"capacity_pools":{"upstream-x":99}}`
	channels := []*model.Channel{
		{Id: 5, Type: constant.ChannelTypeOpenAI, Key: "k1\nk2", Status: common.ChannelStatusEnabled, Models: "alias", ModelMapping: &mapping, Setting: &bound,
			ChannelInfo: model.ChannelInfo{IsMultiKey: true}},
		{Id: 6, Type: constant.ChannelTypeOpenAI, Key: "k", Status: common.ChannelStatusEnabled, Models: "alias", ModelMapping: &mapping, Setting: &bound},
		{Id: 8, Type: constant.ChannelTypeOpenAI, Key: "k", Status: common.ChannelStatusEnabled, Models: "alias", ModelMapping: &mapping, Setting: &dangling},
	}
	floors := map[int]dto.ModelLimit{3: {RPM: 40, MaxConcurrency: 4}}

	catalog, err := BuildSchedulerCatalogFromChannels("alias", channels, floors)
	require.NoError(t, err)
	require.Len(t, catalog.Endpoints, 3)
	for _, endpoint := range catalog.Endpoints {
		if endpoint.ChannelID == 8 {
			// 指向已删除的池：按没绑定处理，回到自己的 <渠道, 上游模型> 池和额度。
			assert.Equal(t, channelcapacity.PoolID(8, "upstream-x"), endpoint.CapacityPoolID)
			assert.Equal(t, SchedulerLimits{RPM: 7}, endpoint.Limits)
			continue
		}
		// 两个渠道都计进容量池，Limits 是池的保底额度，自己的 model_limits 不生效。
		assert.Equal(t, "cp-3", endpoint.CapacityPoolID)
		assert.Equal(t, SchedulerLimits{RPM: 40, MaxConcurrency: 4}, endpoint.Limits)
	}

	// 动态额度不进 Catalog：只有保底额度变了才换版本。
	again, err := BuildSchedulerCatalogFromChannels("alias", channels, map[int]dto.ModelLimit{3: {RPM: 40, MaxConcurrency: 4}})
	require.NoError(t, err)
	assert.Equal(t, catalog.Version, again.Version)
	changed, err := BuildSchedulerCatalogFromChannels("alias", channels, map[int]dto.ModelLimit{3: {RPM: 41, MaxConcurrency: 4}})
	require.NoError(t, err)
	assert.NotEqual(t, catalog.Version, changed.Version)
}

func TestBuildSchedulerCatalogIncludesModelMappingSourceAlias(t *testing.T) {
	mapping := `{"customer-model":"deepseek-v4-flash"}`
	channel := &model.Channel{Id: 9, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled, Models: "deepseek-v4-flash", ModelMapping: &mapping}
	catalog, err := BuildSchedulerCatalogFromChannels("customer-model", []*model.Channel{channel}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Endpoints) != 1 || catalog.Endpoints[0].Model != "customer-model" {
		t.Fatalf("mapping alias was not exposed: %+v", catalog)
	}
}

func TestBuildSchedulerCatalogRejectsUnknownModel(t *testing.T) {
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled, Models: "other-model"}
	if _, err := BuildSchedulerCatalogFromChannels("missing-model", []*model.Channel{channel}, nil); err == nil {
		t.Fatal("expected unknown model error")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
