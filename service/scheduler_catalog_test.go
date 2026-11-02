package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSchedulerCatalogFromChannelsIsCredentialFreeAndExpandsKeys(t *testing.T) {
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

	catalog, err := BuildSchedulerCatalogFromChannels("deepseek-v4-flash", []*model.Channel{channel})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Endpoints) != 2 || catalog.Version == "" {
		t.Fatalf("catalog=%+v", catalog)
	}
	if catalog.Endpoints[0].EndpointID != "new-api-ch-42-k0" || catalog.Endpoints[1].EndpointID != "new-api-ch-42-k1" {
		t.Fatalf("unexpected endpoint ids: %+v", catalog.Endpoints)
	}
	if !catalog.Endpoints[0].Enabled || catalog.Endpoints[1].Enabled {
		t.Fatalf("key status was not projected: %+v", catalog.Endpoints)
	}
	data := string(mustJSON(t, catalog))
	if strings.Contains(data, "secret-one") || strings.Contains(data, "secret-two") || strings.Contains(data, "base_url") {
		t.Fatalf("catalog leaked credential/configuration: %s", data)
	}
	if catalog.Endpoints[0].Provider != constant.GetChannelTypeName(constant.ChannelTypeOpenAI) || catalog.Endpoints[0].Priority != priority || catalog.Endpoints[0].Weight != int64(weight) {
		t.Fatalf("channel metadata mismatch: %+v", catalog.Endpoints[0])
	}
	// 两个 key 端点带同一份 <渠道, 模型> 额度和同一个计数池：额度是整个渠道合计。
	for _, endpoint := range catalog.Endpoints {
		assert.Equal(t, SchedulerLimits{RPM: 120, TPM: 90000, MaxConcurrency: 20}, endpoint.Limits)
		assert.Equal(t, channelcapacity.PoolID(42, "deepseek-v4-flash"), endpoint.CapacityPoolID)
	}
}

func TestBuildSchedulerCatalogPoolsByMappedUpstreamModel(t *testing.T) {
	mapping := `{"alias":"upstream-x"}`
	setting := `{"model_limits":{"upstream-x":{"rpm":30}}}`
	channel := &model.Channel{
		Id: 5, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled,
		Models: "alias,upstream-x,other", ModelMapping: &mapping, Setting: &setting,
	}

	viaAlias, err := BuildSchedulerCatalogFromChannels("alias", []*model.Channel{channel})
	require.NoError(t, err)
	direct, err := BuildSchedulerCatalogFromChannels("upstream-x", []*model.Channel{channel})
	require.NoError(t, err)
	other, err := BuildSchedulerCatalogFromChannels("other", []*model.Channel{channel})
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
	reloaded, err := BuildSchedulerCatalogFromChannels("alias", []*model.Channel{channel})
	require.NoError(t, err)
	assert.NotEqual(t, viaAlias.Version, reloaded.Version)
}

func TestBuildSchedulerCatalogIncludesModelMappingSourceAlias(t *testing.T) {
	mapping := `{"customer-model":"deepseek-v4-flash"}`
	channel := &model.Channel{Id: 9, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled, Models: "deepseek-v4-flash", ModelMapping: &mapping}
	catalog, err := BuildSchedulerCatalogFromChannels("customer-model", []*model.Channel{channel})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Endpoints) != 1 || catalog.Endpoints[0].Model != "customer-model" {
		t.Fatalf("mapping alias was not exposed: %+v", catalog)
	}
}

func TestBuildSchedulerCatalogRejectsUnknownModel(t *testing.T) {
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled, Models: "other-model"}
	if _, err := BuildSchedulerCatalogFromChannels("missing-model", []*model.Channel{channel}); err == nil {
		t.Fatal("expected unknown model error")
	}
}

func TestBuildSchedulerCatalogAdvertisesRelayRequestCapabilities(t *testing.T) {
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "secret", Status: common.ChannelStatusEnabled, Models: "m"}

	catalog, err := BuildSchedulerCatalogFromChannels("m", []*model.Channel{channel})
	require.NoError(t, err)
	require.Len(t, catalog.Endpoints, 1)

	capabilities := catalog.Endpoints[0].Capabilities
	assert.True(t, capabilities.Stream)
	assert.True(t, capabilities.Tools)
	assert.True(t, capabilities.JSONMode)
	assert.False(t, capabilities.Vision)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
