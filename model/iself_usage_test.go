package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetIselfUsageAnalytics(t *testing.T) {
	if common.UsingLogDatabase(common.DatabaseTypeSQLite) {
		var version string
		require.NoError(t, LOG_DB.Raw("SELECT sqlite_version()").Scan(&version).Error)
		t.Logf("SQLite %s", version)
	}
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })
	require.NoError(t, LOG_DB.Create(&[]Log{
		{UserId: 99, Username: "gateway", IselfEmail: " Alice@Example.com ", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: 1000, Quota: 100, PromptTokens: 10, CompletionTokens: 5},
		{UserId: 98, Username: "other-gateway", IselfEmail: "alice@example.com", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: 2000, Quota: 50, PromptTokens: 20, CompletionTokens: 10},
		{UserId: 99, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "model-b", Type: LogTypeConsume, CreatedAt: 1500, Quota: 20, PromptTokens: 3, CompletionTokens: 2},
		{UserId: 99, Username: "gateway", IselfEmail: "bob@example.com", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: 1500, Quota: 40, PromptTokens: 4, CompletionTokens: 2},
		{UserId: 99, Username: "gateway", IselfEmail: "", ModelName: "direct", Type: LogTypeConsume, CreatedAt: 1500, Quota: 999},
		{UserId: 99, Username: "gateway", IselfEmail: "   ", ModelName: "missing-email", Type: LogTypeConsume, CreatedAt: 1500, Quota: 999},
		{UserId: 99, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "error", Type: LogTypeError, CreatedAt: 1500, Quota: 999},
		{UserId: 99, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "refund", Type: LogTypeRefund, CreatedAt: 1500, Quota: 999},
		{UserId: 99, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "before", Type: LogTypeConsume, CreatedAt: 999, Quota: 999},
		{UserId: 99, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "after", Type: LogTypeConsume, CreatedAt: 2001, Quota: 999},
	}).Error)

	rows, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 1000, End: 2000})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, []IselfUsageRow{
		{Email: "alice@example.com", Source: "iself", ModelName: "model-a", RequestCount: 2, Quota: 150, InputTokens: 30, CompletionTokens: 15, TokenUsed: 45, LastCalledAt: 2000},
		{Email: "bob@example.com", Source: "iself", ModelName: "model-a", RequestCount: 1, Quota: 40, InputTokens: 4, CompletionTokens: 2, TokenUsed: 6, LastCalledAt: 1500},
		{Email: "alice@example.com", Source: "iself", ModelName: "model-b", RequestCount: 1, Quota: 20, InputTokens: 3, CompletionTokens: 2, TokenUsed: 5, LastCalledAt: 1500},
	}, rows)

	filtered, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 1000, End: 2000, Username: " other-gateway ", ModelName: " model-a "})
	require.NoError(t, err)
	assert.Equal(t, []IselfUsageRow{
		{Email: "alice@example.com", Source: "iself", ModelName: "model-a", RequestCount: 1, Quota: 50, InputTokens: 20, CompletionTokens: 10, TokenUsed: 30, LastCalledAt: 2000},
	}, filtered)

	empty, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 3000, End: 4000})
	require.NoError(t, err)
	assert.Equal(t, []IselfUsageRow{}, empty)

	// Match the bound email across forwarding accounts.
	require.NoError(t, DB.Create(&User{Id: 71001, Username: "iself-alice", Password: "test-password", Email: " ALICE@example.com ", AffCode: "iself-alice"}).Error)
	t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&User{}, 71001).Error) })
	self, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 1000, End: 2000, UserID: 71001, Username: "other-gateway"})
	require.NoError(t, err)
	assert.Equal(t, []IselfUsageRow{rows[0], rows[2]}, self)
	missingUser, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 1000, End: 2000, UserID: 71002})
	require.NoError(t, err)
	assert.Equal(t, []IselfUsageRow{}, missingUser)
}

func TestGetIselfUsageAnalyticsCacheTokens(t *testing.T) {
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })
	other := func(values map[string]any) string { return common.MapToJsonStr(values) }
	require.NoError(t, LOG_DB.Create(&[]Log{
		// OpenAI semantics: prompt tokens already include cache reads.
		{IselfEmail: "alice@example.com", ModelName: "gpt-cached", Type: LogTypeConsume, CreatedAt: 1000, Quota: 100, PromptTokens: 100, CompletionTokens: 10,
			Other: other(map[string]any{"cache_tokens": 40})},
		// Anthropic semantics: prompt tokens exclude cache reads and writes.
		{IselfEmail: "alice@example.com", ModelName: "claude-cached", Type: LogTypeConsume, CreatedAt: 1100, Quota: 300, PromptTokens: 10, CompletionTokens: 5,
			Other: other(map[string]any{"claude": true, "usage_semantic": "anthropic", "cache_tokens": 60, "cache_creation_tokens": 20, "cache_write_tokens": 30})},
		// Logs written before cache_write_tokens fall back to cache_creation_tokens.
		{IselfEmail: "alice@example.com", ModelName: "claude-cached", Type: LogTypeConsume, CreatedAt: 1200, Quota: 200, PromptTokens: 2, CompletionTokens: 1,
			Other: other(map[string]any{"usage_semantic": "anthropic", "cache_tokens": 40, "cache_creation_tokens": 40})},
		{IselfEmail: "alice@example.com", ModelName: "claude-cached", Type: LogTypeConsume, CreatedAt: 1300, Quota: 100, PromptTokens: 50, CompletionTokens: 5,
			Other: "not json"},
	}).Error)

	rows, err := GetIselfUsageAnalytics(IselfUsageParams{Start: 1000, End: 2000})
	require.NoError(t, err)
	assert.Equal(t, []IselfUsageRow{
		{Email: "alice@example.com", Source: "iself", ModelName: "claude-cached", RequestCount: 3, Quota: 600,
			InputTokens: 232, CompletionTokens: 11, CacheReadTokens: 100, CacheWriteTokens: 70, TokenUsed: 243, LastCalledAt: 1300},
		{Email: "alice@example.com", Source: "iself", ModelName: "gpt-cached", RequestCount: 1, Quota: 100,
			InputTokens: 100, CompletionTokens: 10, CacheReadTokens: 40, TokenUsed: 110, LastCalledAt: 1000},
	}, rows)
}
