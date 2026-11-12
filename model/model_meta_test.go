package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveModelMetadataPrefersLongestPrefixRegardlessOfRowOrder(t *testing.T) {
	shorter := Model{Id: 1, ModelName: "claude-opus-5", NameRule: NameRulePrefix, Status: 1}
	longer := Model{Id: 2, ModelName: "claude-opus-5-5", NameRule: NameRulePrefix, Status: 1}
	names := []string{"claude-opus-5-5", "claude-opus-5-6"}

	resolved := resolveModelMetadata([]Model{shorter, longer}, names)
	require.NotNil(t, resolved["claude-opus-5-5"])
	assert.Equal(t, 2, resolved["claude-opus-5-5"].Id, "更长前缀必须胜出")
	require.NotNil(t, resolved["claude-opus-5-6"])
	assert.Equal(t, 1, resolved["claude-opus-5-6"].Id, "不匹配长前缀时回落到家族前缀")

	// 行顺序不能影响结果：短前缀先被遍历到时也不能把它固定下来。
	resolved = resolveModelMetadata([]Model{longer, shorter}, names)
	require.NotNil(t, resolved["claude-opus-5-5"])
	assert.Equal(t, 2, resolved["claude-opus-5-5"].Id)
}

func TestResolveModelMetadataKeepsRulePrecedence(t *testing.T) {
	resolved := resolveModelMetadata([]Model{
		{Id: 1, ModelName: "-5", NameRule: NameRuleSuffix, Status: 1},
		{Id: 2, ModelName: "claude-", NameRule: NameRulePrefix, Status: 1},
		{Id: 3, ModelName: "claude-opus-5-5", NameRule: NameRuleExact, Status: 1},
		{Id: 4, ModelName: "opus", NameRule: NameRuleContains, Status: 1},
	}, []string{"claude-opus-5-5", "claude-x-5", "x-opus-y"})

	require.NotNil(t, resolved["claude-opus-5-5"])
	assert.Equal(t, 3, resolved["claude-opus-5-5"].Id, "精确行优先")
	require.NotNil(t, resolved["claude-x-5"])
	assert.Equal(t, 2, resolved["claude-x-5"].Id, "前缀优先于后缀，即使后缀更长")
	require.NotNil(t, resolved["x-opus-y"])
	assert.Equal(t, 4, resolved["x-opus-y"].Id, "只有包含规则命中时使用包含")
}
