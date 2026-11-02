package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/capability"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCapabilityModelTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Model{}, &Channel{}, &Ability{}, &ChannelModelStatus{}, &ChannelModelCapability{}, &ResourcePoolSource{}, &CapacityPool{}))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		InvalidateCapabilityCache()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func TestCapabilityTargetsExpandPrefixDeclarations(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "deepseek-v4-pro", NameRule: NameRulePrefix, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, db.Create(&Channel{Id: 1, Name: "ds", Group: "default", Models: "deepseek-v4-pro", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "deepseek-v4-pro", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, InitCapabilityCache())

	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.True(t, targets[0].Declared)
	assert.Equal(t, "deepseek-v4-pro", targets[0].ModelName)
	assert.Equal(t, "deepseek-v4-pro", targets[0].DeclarationName)
	assert.Equal(t, NameRulePrefix, targets[0].NameRule)
	assert.Contains(t, targets[0].Capabilities, "max_output_tokens")
	require.Len(t, targets[0].Channels, 1)
	assert.Equal(t, 1, targets[0].Channels[0].ID)
}

func TestCapabilityTargetsFilterAndPaginate(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "family-", NameRule: NameRulePrefix, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, db.Create(&Model{ModelName: "other-model", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, db.Create(&[]Channel{
		{Id: 1, Name: "one", Group: "default", Models: "family-a,family-b", Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "two", Group: "default", Models: "family-a,other-model", Status: common.ChannelStatusEnabled},
	}).Error)
	require.NoError(t, db.Create(&[]Ability{
		{Group: "default", Model: "family-a", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "family-b", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "family-a", ChannelId: 2, Enabled: true},
		{Group: "default", Model: "other-model", ChannelId: 2, Enabled: true},
	}).Error)
	require.NoError(t, InitCapabilityCache())

	targets, total, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, targets, 3)
	assert.Equal(t, []string{"family-a", "family-b", "other-model"}, []string{targets[0].ModelName, targets[1].ModelName, targets[2].ModelName})
	assert.Equal(t, "family-", targets[0].DeclarationName)
	assert.Equal(t, NameRuleExact, targets[2].NameRule)

	targets, total, err = GetCapabilityTargets(CapabilityTargetQuery{Keyword: "FAMILY-B"})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, targets, 1)
	assert.Equal(t, "family-b", targets[0].ModelName)

	// 渠道 2 只服务 family-a 与 other-model。
	targets, total, err = GetCapabilityTargets(CapabilityTargetQuery{ChannelID: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, targets, 2)
	assert.Equal(t, "family-a", targets[0].ModelName)
	assert.Equal(t, "other-model", targets[1].ModelName)
	require.Len(t, targets[0].Channels, 2)

	page, total, err := GetCapabilityTargets(CapabilityTargetQuery{Offset: 1, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, page, 1)
	assert.Equal(t, "family-b", page[0].ModelName)

	page, total, err = GetCapabilityTargets(CapabilityTargetQuery{Offset: 5, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Empty(t, page)
}

func TestCapabilityTargetsListServingChannelsOnce(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "target-model", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, db.Create(&Channel{Id: 7, Name: "chan", Group: "default", Models: "target-model", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&[]Ability{
		{Group: "default", Model: "target-model", ChannelId: 7, Enabled: true},
		{Group: "vip", Model: "target-model", ChannelId: 7, Enabled: true},
	}).Error)
	require.NoError(t, InitCapabilityCache())

	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "target-model", targets[0].ModelName)
	require.Len(t, targets[0].Channels, 1)
	assert.Equal(t, 7, targets[0].Channels[0].ID)
	assert.Equal(t, "chan", targets[0].Channels[0].Name)
}

func TestCapabilitySchemaSQLiteIdempotent(t *testing.T) {
	db := setupCapabilityModelTest(t)
	// AutoMigrate 跑两次证明幂等；SQLite 下复合主键与 capabilities 列都必须存在。
	require.NoError(t, db.AutoMigrate(&Model{}, &ChannelModelCapability{}))
	require.NoError(t, db.AutoMigrate(&Model{}, &ChannelModelCapability{}))
	require.True(t, db.Migrator().HasTable(&ChannelModelCapability{}))
	require.True(t, db.Migrator().HasColumn(&Model{}, "capabilities"))
	require.True(t, db.Migrator().HasColumn(&ChannelModelCapability{}, "channel_id"))
	require.True(t, db.Migrator().HasColumn(&ChannelModelCapability{}, "model"))
	require.True(t, db.Migrator().HasColumn(&ChannelModelCapability{}, "capabilities"))

	row := ChannelModelCapability{ChannelId: 1, Model: "m", Capabilities: `{}`}
	require.NoError(t, db.Create(&row).Error)
	err := db.Create(&ChannelModelCapability{ChannelId: 1, Model: "m", Capabilities: `{}`}).Error
	require.Error(t, err)
}

func TestCapabilityResolutionFollowsNameRulePrecedence(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "gpt-", NameRule: NameRulePrefix, Capabilities: `{"context":{"max_output_tokens":1000}}`}).Error)
	require.NoError(t, db.Create(&Model{ModelName: "gpt-4o-", NameRule: NameRulePrefix, Capabilities: `{"context":{"max_output_tokens":1500}}`}).Error)
	require.NoError(t, db.Create(&Model{ModelName: "gpt-4o", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":2000}}`}).Error)
	require.NoError(t, InitCapabilityCache())

	exact, ok := ResolveModelCapability("gpt-4o")
	require.True(t, ok)
	assert.Equal(t, int64(2000), *exact.Context.MaxOutputTokens)

	prefix, ok := ResolveModelCapability("gpt-4o-mini")
	require.True(t, ok)
	assert.Equal(t, int64(1500), *prefix.Context.MaxOutputTokens, "longer prefix must win over the family default")

	family, ok := ResolveModelCapability("gpt-5")
	require.True(t, ok)
	assert.Equal(t, int64(1000), *family.Context.MaxOutputTokens)

	_, ok = ResolveModelCapability("claude-3")
	assert.False(t, ok)
}

func TestChannelNarrowingMustStayWithinModelDeclaration(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "m", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":1000},"tools":{"supported":true}}`}).Error)
	require.NoError(t, db.Create(&Channel{Id: 1, Name: "c1", Group: "default", Models: "m", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, InitCapabilityCache())

	err := SetChannelModelCapability(1, "m", `{"context":{"max_output_tokens":2000}}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds model-level")

	require.NoError(t, SetChannelModelCapability(1, "m", `{"context":{"max_output_tokens":500},"tools":{"supported":false}}`))

	over, ok := GetChannelModelCapability(1, "m")
	require.True(t, ok)
	assert.Equal(t, int64(500), *over.Context.MaxOutputTokens)

	effective, ok := EffectiveChannelCapability(1, "m")
	require.True(t, ok)
	assert.Equal(t, int64(500), *effective.Context.MaxOutputTokens)
	assert.False(t, *effective.Tools.Supported)

	// 没有收窄记录的渠道直接继承模型层声明。
	inherited, ok := EffectiveChannelCapability(2, "m")
	require.True(t, ok)
	assert.Equal(t, int64(1000), *inherited.Context.MaxOutputTokens)
}

func TestChannelNarrowingToNoBuiltinToolsSurvivesPersistence(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "search-model", NameRule: NameRuleExact, Capabilities: `{"tools":{"supported":true,"builtin":["web_search"]}}`}).Error)
	require.NoError(t, db.Create(&Channel{Id: 1, Name: "narrowed", Group: "default", Models: "search-model", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&Channel{Id: 2, Name: "inherits", Group: "default", Models: "search-model", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, InitCapabilityCache())

	require.NoError(t, SetChannelModelCapability(1, "search-model", `{"tools":{"builtin":[]}}`))

	needs := &capability.Needs{BuiltinTools: []string{"web_search"}}
	narrowed, ok := EffectiveChannelCapability(1, "search-model")
	require.True(t, ok)
	assert.False(t, capability.Satisfied(capability.Evaluate(narrowed, needs)), "channel declared no built-in tools")

	inherited, ok := EffectiveChannelCapability(2, "search-model")
	require.True(t, ok)
	assert.True(t, capability.Satisfied(capability.Evaluate(inherited, needs)), "channel without narrowing keeps the model-level tools")
}

func TestChannelCapabilityRowsFollowChannelModelChanges(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Model{ModelName: "keep", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, db.Create(&Model{ModelName: "drop", NameRule: NameRuleExact, Capabilities: `{"context":{"max_output_tokens":100}}`}).Error)
	require.NoError(t, InitCapabilityCache())

	channel := &Channel{Id: 1, Name: "c", Group: "default", Models: "keep,drop", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, SetChannelModelCapability(1, "keep", `{"context":{"max_output_tokens":50}}`))
	require.NoError(t, SetChannelModelCapability(1, "drop", `{"context":{"max_output_tokens":50}}`))

	channel.Models = "keep"
	require.NoError(t, channel.UpdateAbilities(nil))

	var rows []ChannelModelCapability
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "keep", rows[0].Model)
	_, ok := GetChannelModelCapability(1, "drop")
	assert.False(t, ok)

	require.NoError(t, DeleteChannelModelCapability(1, "keep"))
	require.NoError(t, channel.Delete())
	require.NoError(t, db.Find(&rows).Error)
	assert.Empty(t, rows)
}

func TestValidateModelCapabilityNarrowingsRejectsStaleChannel(t *testing.T) {
	db := setupCapabilityModelTest(t)
	declaration := Model{ModelName: "strict-model", NameRule: NameRuleExact, Capabilities: `{"params":{"temperature":{"min":0,"max":2}}}`}
	require.NoError(t, db.Create(&declaration).Error)
	require.NoError(t, db.Create(&Channel{Id: 3, Name: "c3", Group: "default", Models: "strict-model", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, InitCapabilityCache())
	require.NoError(t, SetChannelModelCapability(3, "strict-model", `{"params":{"temperature":{"min":0,"max":2}}}`))

	// 渠道收窄照抄了旧的模型级区间；模型级收紧后必须拒绝保存。
	tightened := declaration
	tightened.Capabilities = `{"params":{"temperature":{"min":0,"max":1}}}`
	err := ValidateModelCapabilityNarrowings(tightened)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel #3")
	assert.Contains(t, err.Error(), "params.temperature")

	// 仍覆盖存量收窄时保存通过。
	require.NoError(t, ValidateModelCapabilityNarrowings(declaration))
}

func TestCapabilityTargetsListUndeclaredChannelModelsForCostMultiplier(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&Channel{Id: 9, Name: "raw", Group: "default", Models: "plain-model", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "plain-model", ChannelId: 9, Enabled: true}).Error)
	require.NoError(t, InitCapabilityCache())

	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.False(t, targets[0].Declared)
	assert.Empty(t, targets[0].DeclarationName)
	require.Len(t, targets[0].Channels, 1)
	assert.Equal(t, 0.0, targets[0].Channels[0].CostMultiplier)
}

func TestChannelCostMultiplierFollowsModelMapping(t *testing.T) {
	db := setupCapabilityModelTest(t)
	mapping := `{"public-model":"upstream-model"}`
	require.NoError(t, db.Create(&Channel{
		Id: 5, Name: "mapped", Group: "default", Models: "public-model",
		ModelMapping: &mapping, Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "public-model", ChannelId: 5, Enabled: true}).Error)
	require.NoError(t, InitCapabilityCache())

	assert.Equal(t, 0.0, ChannelCostMultiplier("", mapping, "public-model"))

	require.NoError(t, SetChannelModelCostMultiplier(5, "public-model", 1.25))
	var channel Channel
	require.NoError(t, db.First(&channel, "id = ?", 5).Error)
	assert.Equal(t, map[string]float64{"upstream-model": 1.25}, channel.GetSetting().CostMultiplierMap)
	assert.Equal(t, 1.25, ChannelCostMultiplier(*channel.Setting, mapping, "public-model"))

	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Len(t, targets[0].Channels, 1)
	assert.Equal(t, 1.25, targets[0].Channels[0].CostMultiplier)

	// 0 表示清除倍率，回到按 1 处理。
	require.NoError(t, SetChannelModelCostMultiplier(5, "public-model", 0))
	require.NoError(t, db.First(&channel, "id = ?", 5).Error)
	assert.Empty(t, channel.GetSetting().CostMultiplierMap)

	err = SetChannelModelCostMultiplier(5, "other-model", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not serve model")
}

func TestResolveMappedModel(t *testing.T) {
	tests := []struct {
		name       string
		mapping    string
		origin     string
		upstream   string
		mapped     bool
		wantErrMsg string
	}{
		{name: "no mapping", mapping: "", origin: "a", upstream: "a"},
		{name: "empty object", mapping: "{}", origin: "a", upstream: "a"},
		{name: "not matched", mapping: `{"x":"y"}`, origin: "a", upstream: "a"},
		{name: "single hop", mapping: `{"a":"b"}`, origin: "a", upstream: "b", mapped: true},
		{name: "chain goes to the tail", mapping: `{"a":"b","b":"c"}`, origin: "a", upstream: "c", mapped: true},
		{name: "self mapping at origin is not a redirect", mapping: `{"a":"a"}`, origin: "a", upstream: "a"},
		{name: "self mapping at the tail stops there", mapping: `{"a":"b","b":"b"}`, origin: "a", upstream: "b", mapped: true},
		{name: "empty target is ignored", mapping: `{"a":""}`, origin: "a", upstream: "a"},
		{name: "modifier suffix falls back to base name", mapping: `{"m":"up"}`, origin: "m@thinking:on", upstream: "up", mapped: true},
		{name: "cycle", mapping: `{"a":"b","b":"a"}`, origin: "a", upstream: "a", wantErrMsg: "model_mapping_contains_cycle"},
		{name: "invalid json", mapping: `not-json`, origin: "a", upstream: "a", wantErrMsg: "unmarshal_model_mapping_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream, mapped, err := ResolveMappedModel(tt.mapping, tt.origin)
			if tt.wantErrMsg != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErrMsg, err.Error())
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.upstream, upstream)
			assert.Equal(t, tt.mapped, mapped)
		})
	}
}

func TestSetChannelModelLimitKeysByMappedUpstream(t *testing.T) {
	db := setupCapabilityModelTest(t)
	// 两个公开名经映射链指向同一个上游，应当共用一份额度。
	mapping := `{"alias-a":"mid","mid":"upstream-x","alias-b":"upstream-x"}`
	setting := `{"cost_multiplier":{"upstream-x":1.5}}`
	require.NoError(t, db.Create(&Channel{
		Id: 7, Name: "pooled", Group: "default", Models: "alias-a,alias-b",
		ModelMapping: &mapping, Setting: &setting, Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "alias-a", ChannelId: 7, Enabled: true}).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "alias-b", ChannelId: 7, Enabled: true}).Error)
	require.NoError(t, InitCapabilityCache())

	limit := dto.ModelLimit{RPM: 60, TPM: 90000, MaxConcurrency: 4}
	require.NoError(t, SetChannelModelLimit(7, "alias-a", limit))

	var channel Channel
	require.NoError(t, db.First(&channel, "id = ?", 7).Error)
	stored := channel.GetSetting()
	assert.Equal(t, map[string]dto.ModelLimit{"upstream-x": limit}, stored.ModelLimits)
	assert.Equal(t, map[string]float64{"upstream-x": 1.5}, stored.CostMultiplierMap, "写额度不能冲掉同一 setting 里的其它字段")

	capacity := ChannelModelCapacityOf(*channel.Setting, mapping, "alias-b")
	assert.Equal(t, "upstream-x", capacity.UpstreamModel)
	assert.Equal(t, limit, capacity.Limit, "alias-b 指向同一上游，读到同一份额度")
	assert.Zero(t, capacity.CapacityPoolId)

	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	for _, target := range targets {
		require.Len(t, target.Channels, 1)
		assert.Equal(t, "upstream-x", target.Channels[0].UpstreamModel)
		assert.Equal(t, limit, target.Channels[0].ModelLimit)
	}

	// 三项全 0 表示清除。
	require.NoError(t, SetChannelModelLimit(7, "alias-b", dto.ModelLimit{}))
	require.NoError(t, db.First(&channel, "id = ?", 7).Error)
	assert.Empty(t, channel.GetSetting().ModelLimits)
	assert.NotContains(t, *channel.Setting, "model_limits")

	err = SetChannelModelLimit(7, "not-served", limit)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not serve model")

	err = SetChannelModelLimit(7, "alias-a", dto.ModelLimit{RPM: -1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpm must be between")

	// setting 不是合法 JSON 时按未配置处理，不报错也不改库。
	assert.Equal(t, ChannelModelCapacity{UpstreamModel: "upstream-x"}, ChannelModelCapacityOf("{broken", mapping, "alias-a"))
}

func TestCapacityPoolMembersBindByUpstreamAndClearOnDelete(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.Create(&ResourcePoolSource{Id: 1, Provider: ResourcePoolClaude, Name: "kiro-a", Endpoint: "https://a.example.com"}).Error)
	require.NoError(t, db.Create(&ResourcePoolSource{Id: 2, Provider: ResourcePoolClaude, Name: "kiro-b", Endpoint: "https://b.example.com"}).Error)
	require.NoError(t, db.Create(&ResourcePoolSource{Id: 3, Provider: ResourcePoolGPT, Name: "codex", Endpoint: "https://c.example.com"}).Error)

	// 保存时的校验：来源必须属于 provider，同 provider 的池账号范围不能重叠。
	require.ErrorContains(t, SaveCapacityPool(&CapacityPool{Name: " ", Provider: ResourcePoolClaude}, CapacityPoolConfig{SourceIds: []int64{1}}), "name is required")
	require.ErrorContains(t, SaveCapacityPool(&CapacityPool{Name: "x", Provider: ResourcePoolClaude}, CapacityPoolConfig{SourceIds: []int64{3}}), "does not belong")
	require.ErrorContains(t, SaveCapacityPool(&CapacityPool{Name: "x", Provider: ResourcePoolClaude}, CapacityPoolConfig{SourceIds: []int64{1}, ThresholdPercent: 120}), "threshold_percent")
	poolA := &CapacityPool{Name: "aws1", Provider: ResourcePoolClaude}
	require.NoError(t, SaveCapacityPool(poolA, CapacityPoolConfig{SourceIds: []int64{1}, Groups: []string{" aws1 ", "aws1"}}))
	require.ErrorContains(t, SaveCapacityPool(&CapacityPool{Name: "dup", Provider: ResourcePoolClaude}, CapacityPoolConfig{SourceIds: []int64{1}}), "overlap")
	require.NoError(t, SaveCapacityPool(&CapacityPool{Name: "aws2", Provider: ResourcePoolClaude}, CapacityPoolConfig{SourceIds: []int64{1}, Groups: []string{"aws2"}}), "同一来源的不同分组不算重叠")
	floor := dto.ModelLimit{RPM: 30}
	poolB := &CapacityPool{Name: "kiro-b", Provider: ResourcePoolClaude}
	require.NoError(t, SaveCapacityPool(poolB, CapacityPoolConfig{SourceIds: []int64{2}, Floor: floor}))
	storedA, err := GetCapacityPoolById(poolA.Id)
	require.NoError(t, err)
	configA, err := storedA.ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"aws1"}, configA.Groups)
	assert.Equal(t, float64(95), configA.ThresholdPercent, "没填的参数补默认值")
	assert.Equal(t, 600, configA.GraceSeconds)
	poolA.Provider = ResourcePoolGPT
	require.ErrorContains(t, SaveCapacityPool(poolA, CapacityPoolConfig{SourceIds: []int64{3}}), "cannot be changed")

	mapping := `{"alias-a":"mid","mid":"upstream-x","alias-b":"upstream-x"}`
	setting := `{"model_limits":{"upstream-x":{"rpm":60}}}`
	require.NoError(t, db.Create(&Channel{
		Id: 7, Name: "kiro", Group: "default", Models: "alias-a,alias-b,direct",
		ModelMapping: &mapping, Setting: &setting, Status: common.ChannelStatusEnabled,
	}).Error)
	for _, name := range []string{"alias-a", "alias-b", "direct"} {
		require.NoError(t, db.Create(&Ability{Group: "default", Model: name, ChannelId: 7, Enabled: true}).Error)
	}
	require.NoError(t, InitCapabilityCache())

	added, err := AddCapacityPoolMembers(poolA.Id, 7, []string{"alias-a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"upstream-x"}, added, "按映射链尾的上游名绑定")
	added, err = AddCapacityPoolMembers(poolA.Id, 7, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"direct"}, added, "空表示全部模型；指向已绑上游的别名跳过")
	_, err = AddCapacityPoolMembers(poolB.Id, 7, []string{"alias-b"})
	require.ErrorContains(t, err, "already in capacity pool")
	_, err = AddCapacityPoolMembers(poolA.Id, 7, []string{"not-served"})
	require.ErrorContains(t, err, "does not serve model")

	var channel Channel
	require.NoError(t, db.First(&channel, "id = ?", 7).Error)
	assert.Equal(t, map[string]int{"upstream-x": poolA.Id, "direct": poolA.Id}, channel.GetSetting().CapacityPools)
	assert.Equal(t, map[string]dto.ModelLimit{"upstream-x": {RPM: 60}}, channel.GetSetting().ModelLimits, "绑定不动成员自己的额度")
	assert.Equal(t, poolA.Id, ChannelModelCapacityOf(*channel.Setting, mapping, "alias-b").CapacityPoolId)
	require.ErrorIs(t, SetChannelModelLimit(7, "alias-b", dto.ModelLimit{RPM: 5}), ErrModelLimitManagedByCapacityPool)

	members, err := GetCapacityPoolMembers()
	require.NoError(t, err)
	assert.Equal(t, []CapacityPoolMember{
		{ChannelId: 7, ChannelName: "kiro", UpstreamModel: "direct", Models: []string{"direct"}},
		{ChannelId: 7, ChannelName: "kiro", UpstreamModel: "upstream-x", Models: []string{"alias-a", "alias-b"}},
	}, members[poolA.Id])
	targets, _, err := GetCapabilityTargets(CapabilityTargetQuery{Keyword: "alias-a"})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, poolA.Id, targets[0].Channels[0].CapacityPoolId)
	assert.Equal(t, "aws1", targets[0].Channels[0].CapacityPoolName)

	// 改了模型列表后没有公开名再指向的绑定标为失效。
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", 7).Update("models", "alias-a,alias-b").Error)
	members, err = GetCapacityPoolMembers()
	require.NoError(t, err)
	assert.True(t, members[poolA.Id][0].Stale)
	require.NoError(t, RemoveCapacityPoolMember(poolA.Id, 7, "direct"))
	require.ErrorContains(t, RemoveCapacityPoolMember(poolA.Id, 7, "direct"), "is not in capacity pool")

	floors, err := GetCapacityPoolFloors()
	require.NoError(t, err)
	assert.Equal(t, floor, floors[poolB.Id])

	// 删池时一并清掉绑定，成员回到自己的 model_limits。
	require.NoError(t, DeleteCapacityPool(poolA.Id))
	require.ErrorIs(t, DeleteCapacityPool(poolA.Id), ErrCapacityPoolNotFound)
	require.NoError(t, db.First(&channel, "id = ?", 7).Error)
	assert.Empty(t, channel.GetSetting().CapacityPools)
	assert.NotContains(t, *channel.Setting, "capacity_pools")
	assert.Equal(t, map[string]dto.ModelLimit{"upstream-x": {RPM: 60}}, channel.GetSetting().ModelLimits)
	require.NoError(t, SetChannelModelLimit(7, "alias-b", dto.ModelLimit{RPM: 5}), "解绑后又能改自己的额度")
}
