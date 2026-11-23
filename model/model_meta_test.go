package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveModelMetadataMatchesExactNamesOnly(t *testing.T) {
	exact := Model{Id: 1, ModelName: "claude-opus-5-5", NameRule: NameRuleExact, Status: 1}
	legacyPrefix := Model{Id: 2, ModelName: "claude-opus-5", NameRule: NameRulePrefix, Status: 1}

	resolved := resolveModelMetadata([]Model{legacyPrefix, exact}, []string{"claude-opus-5-5", "claude-opus-5-6"})
	require.NotNil(t, resolved["claude-opus-5-5"])
	assert.Equal(t, 1, resolved["claude-opus-5-5"].Id)
	assert.Nil(t, resolved["claude-opus-5-6"], "non-exact rules must no longer resolve concrete names")

	assert.True(t, exact.MatchesName("claude-opus-5-5"))
	assert.False(t, exact.MatchesName("claude-opus-5-5-mini"))
	assert.False(t, legacyPrefix.MatchesName("claude-opus-5-5"))
	assert.ErrorContains(t, ValidateMetadataValues(MetadataValues{NameRule: NameRulePrefix, Status: 1}), "only exact model matching is supported")
}

func TestMigrateModelNameRulesToExactExpandsAndCleansUpRules(t *testing.T) {
	db := setupCapabilityModelTest(t)
	require.NoError(t, db.AutoMigrate(&Vendor{}))

	vendor := Vendor{Name: "Anthropic", Icon: "Claude.Color", Status: 1}
	require.NoError(t, db.Create(&vendor).Error)

	// 1. 家族前缀与更长子前缀（验证迁移保留最长前缀胜出与 status=0 隐藏语义）。
	familyRule := Model{
		ModelName:     "claude-sonnet-4",
		NameRule:      NameRulePrefix,
		VendorID:      vendor.Id,
		Description:   "Sonnet 4 family",
		Icon:          "Claude.Color",
		Tags:          "chat,reasoning",
		ContextLength: 200000,
		Capabilities:  `{"context":{"max_output_tokens":8192},"tools":{"supported":true}}`,
		Status:        1,
		SyncOfficial:  1,
	}
	longerHiddenRule := Model{
		ModelName:    "claude-sonnet-4-5",
		NameRule:     NameRulePrefix,
		VendorID:     vendor.Id,
		Description:  "Sonnet 4.5 hidden",
		Capabilities: `{"context":{"max_output_tokens":16384}}`,
		Status:       0,
		SyncOfficial: 1,
	}
	// 2. 名称本身就被渠道提供的非精确规则行：原地转为 name_rule=0，不触发唯一索引冲突。
	servedStemRule := Model{
		ModelName:    "deepseek-v4-pro",
		NameRule:     NameRulePrefix,
		Capabilities: `{"context":{"max_output_tokens":4096}}`,
		Status:       1,
	}
	// 3. 没有任何渠道或定价引用的纯规则词干行：迁移后应被清理删除。
	unusedRule := Model{
		ModelName:    "unused-family-",
		NameRule:     NameRulePrefix,
		Capabilities: `{"context":{"max_output_tokens":1024}}`,
		Status:       1,
	}
	// 4. 已有精确元数据行但 capabilities 为空（官方同步场景）：迁移后应回填原命中的规则能力。
	syncedExact := Model{
		ModelName:    "claude-sonnet-4-20250514",
		NameRule:     NameRuleExact,
		Description:  "Official synced description",
		Capabilities: "",
		Status:       1,
		SyncOfficial: 1,
	}
	for _, row := range []*Model{&familyRule, &longerHiddenRule, &servedStemRule, &unusedRule, &syncedExact} {
		desiredStatus := row.Status
		require.NoError(t, db.Create(row).Error)
		// 显式落回测试指定的 status，绕开 Create 时的 gorm default:1（Create 会原地改写指针字段）。
		require.NoError(t, db.Model(&Model{}).Where("id = ?", row.Id).Update("status", desiredStatus).Error)
	}

	require.NoError(t, db.Create(&[]Channel{
		{Id: 1, Name: "enabled-ch", Group: "default", Models: "claude-sonnet-4-0,claude-sonnet-4-20250514,deepseek-v4-pro,plain-unmatched", Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "disabled-ch", Group: "default", Models: "claude-sonnet-4-5-preview", Status: common.ChannelStatusManuallyDisabled},
	}).Error)
	require.NoError(t, db.Create(&Option{Key: "ModelPrice", Value: `{"claude-sonnet-4-priced-only":0.5}`}).Error)

	require.NoError(t, MigrateModelNameRulesToExact())

	var rows []Model
	require.NoError(t, db.Order("model_name").Find(&rows).Error)
	byName := make(map[string]Model, len(rows))
	for _, row := range rows {
		assert.Equal(t, NameRuleExact, row.NameRule, "all remaining rows must be exact: %s", row.ModelName)
		byName[row.ModelName] = row
	}

	// 纯前缀词干已被清理，未命中任何规则的渠道模型不会凭空生成空元数据行。
	assert.NotContains(t, byName, "claude-sonnet-4")
	assert.NotContains(t, byName, "claude-sonnet-4-5")
	assert.NotContains(t, byName, "unused-family-")
	assert.NotContains(t, byName, "plain-unmatched")

	// 命中家族前缀的渠道模型与仅配置定价的模型都获得了独立拷贝。
	sonnet0, ok := byName["claude-sonnet-4-0"]
	require.True(t, ok)
	assert.Equal(t, vendor.Id, sonnet0.VendorID)
	assert.Equal(t, "Sonnet 4 family", sonnet0.Description)
	assert.Equal(t, int64(200000), sonnet0.ContextLength)
	assert.Contains(t, sonnet0.Capabilities, "8192")
	assert.Equal(t, 1, sonnet0.Status)
	assert.Zero(t, sonnet0.SyncOfficial)

	pricedOnly, ok := byName["claude-sonnet-4-priced-only"]
	require.True(t, ok)
	assert.Contains(t, pricedOnly.Capabilities, "8192")

	// 禁用渠道上的模型命中了更长的隐藏子前缀，必须保留更长前缀的能力与 status=0。
	sonnet45, ok := byName["claude-sonnet-4-5-preview"]
	require.True(t, ok)
	assert.Equal(t, "Sonnet 4.5 hidden", sonnet45.Description)
	assert.Contains(t, sonnet45.Capabilities, "16384")
	assert.Zero(t, sonnet45.Status, "hidden rule status=0 must survive gorm default:1")

	// 已有精确行保留原描述，并回填原前缀规则提供的 capabilities 与 context_length。
	backfilled, ok := byName["claude-sonnet-4-20250514"]
	require.True(t, ok)
	assert.Equal(t, "Official synced description", backfilled.Description)
	assert.Contains(t, backfilled.Capabilities, "8192")
	assert.Equal(t, int64(200000), backfilled.ContextLength)
	assert.Equal(t, 1, backfilled.SyncOfficial)

	// 本身在渠道提供列表里的规则行原地转为精确行。
	converted, ok := byName["deepseek-v4-pro"]
	require.True(t, ok)
	assert.Equal(t, servedStemRule.Id, converted.Id)
	assert.Equal(t, NameRuleExact, converted.NameRule)

	// 缓存同步刷新，门控能直接按精确模型名读到能力声明。
	cap45, declared := ResolveModelCapability("claude-sonnet-4-5-preview")
	require.True(t, declared)
	assert.Equal(t, int64(16384), *cap45.Context.MaxOutputTokens)

	// 二次执行幂等，且服务端标记不可通过选项接口篡改或暴露。
	countBefore := len(rows)
	require.NoError(t, MigrateModelNameRulesToExact())
	var rowsAfter []Model
	require.NoError(t, db.Find(&rowsAfter).Error)
	assert.Len(t, rowsAfter, countBefore)

	require.ErrorIs(t, UpdateOption(modelNameRuleExactMigratedKey, "1"), errModelNameRuleExactMigratedReadOnly)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{modelNameRuleExactMigratedKey: "1"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, updateOptionMap(modelNameRuleExactMigratedKey, "1"))
	common.OptionMapRWMutex.RLock()
	_, listed := common.OptionMap[modelNameRuleExactMigratedKey]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, listed)
}
