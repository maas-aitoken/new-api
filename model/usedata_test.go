package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordConsumeLogWritesExtendedQuotaDataColumns(t *testing.T) {
	resetUsageAnalyticsState(t)
	previousExportEnabled := common.DataExportEnabled
	previousLogEnabled := common.LogConsumeEnabled
	common.DataExportEnabled = true
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = previousExportEnabled
		common.LogConsumeEnabled = previousLogEnabled
	})

	// Anthropic input totals include cache reads and writes.
	anthropicTotal := 100 + 300 + 200 + 50
	explicitWriteTotal := 40 + 10 + 30 + 20
	tests := []struct {
		name             string
		params           RecordConsumeLogParams
		tokenUsed        int
		inputTokens      int
		completionTokens int
		cacheReadTokens  int
		cacheWriteTokens int
		trackedCount     int
		useTimeTotal     int64
		useTimeCount     int64
		streamErrorCount int
	}{
		{
			name: "anthropic falls back to cache_creation_tokens",
			params: RecordConsumeLogParams{ModelName: "claude-semantic", PromptTokens: 100, CompletionTokens: 50,
				DashboardTokenUsed: &anthropicTotal, UseTimeSeconds: 4, OutcomeTracked: true,
				Other: &LogOther{public: map[string]any{"usage_semantic": "anthropic", "cache_tokens": 300, "cache_creation_tokens": 200}}},
			tokenUsed: 650, inputTokens: 600, completionTokens: 50, cacheReadTokens: 300, cacheWriteTokens: 200,
			trackedCount: 1, useTimeTotal: 4, useTimeCount: 1,
		},
		{
			name: "anthropic prefers normalized cache_write_tokens",
			params: RecordConsumeLogParams{ModelName: "claude-split", PromptTokens: 40, CompletionTokens: 20,
				DashboardTokenUsed: &explicitWriteTotal, UseTimeSeconds: 2, OutcomeTracked: true,
				Other: &LogOther{public: map[string]any{"cache_tokens": 10, "cache_creation_tokens": 5, "cache_write_tokens": 30}}},
			tokenUsed: 100, inputTokens: 80, completionTokens: 20, cacheReadTokens: 10, cacheWriteTokens: 30,
			trackedCount: 1, useTimeTotal: 2, useTimeCount: 1,
		},
		{
			name: "openai prompt tokens already include cache",
			params: RecordConsumeLogParams{ModelName: "gpt-openai", PromptTokens: 1000, CompletionTokens: 100, UseTimeSeconds: 3,
				OutcomeTracked: true, Other: &LogOther{public: map[string]any{"cache_tokens": 400}}},
			tokenUsed: 1100, inputTokens: 1000, completionTokens: 100, cacheReadTokens: 400,
			trackedCount: 1, useTimeTotal: 3, useTimeCount: 1,
		},
		{
			name: "interrupted stream counts as stream error without latency",
			params: RecordConsumeLogParams{ModelName: "gpt-stream-error", PromptTokens: 20, CompletionTokens: 10, UseTimeSeconds: 9, IsStream: true,
				OutcomeTracked: true, Other: &LogOther{public: map[string]any{"stream_status": map[string]interface{}{"status": "error", "end_reason": "timeout"}}}},
			tokenUsed: 30, inputTokens: 20, completionTokens: 10, trackedCount: 1, streamErrorCount: 1,
		},
		{
			name: "completed stream keeps latency",
			params: RecordConsumeLogParams{ModelName: "gpt-stream-ok", PromptTokens: 20, CompletionTokens: 10, UseTimeSeconds: 5, IsStream: true,
				OutcomeTracked: true, Other: &LogOther{public: map[string]any{"stream_status": map[string]interface{}{"status": "ok", "end_reason": "done"}}}},
			tokenUsed: 30, inputTokens: 20, completionTokens: 10, trackedCount: 1, useTimeTotal: 5, useTimeCount: 1,
		},
		{
			name: "sub-second request still counts toward latency",
			params: RecordConsumeLogParams{ModelName: "gpt-fast", PromptTokens: 5, CompletionTokens: 5, UseTimeSeconds: 0,
				OutcomeTracked: true},
			tokenUsed: 10, inputTokens: 5, completionTokens: 5, trackedCount: 1, useTimeTotal: 0, useTimeCount: 1,
		},
		{
			// Paths without final-failure tracking must not affect success rates.
			name: "untracked path keeps volume but no outcome metrics",
			params: RecordConsumeLogParams{ModelName: "mj-imagine", Quota: 30, UseTimeSeconds: 7,
				Other: &LogOther{public: map[string]any{"stream_status": map[string]interface{}{"status": "error"}}}},
			tokenUsed: 0,
		},
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "alice")
	before := common.GetTimestamp()
	for _, tt := range tests {
		RecordConsumeLog(ctx, 1, tt.params)
	}
	SaveQuotaDataCache()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var row QuotaData
			require.NoError(t, DB.Where("model_name = ?", tt.params.ModelName).First(&row).Error)
			assert.Equal(t, 1, row.UserID)
			assert.Equal(t, "alice", row.Username)
			assert.Equal(t, 1, row.Count)
			assert.Equal(t, 0, row.FailedCount)
			assert.Equal(t, tt.tokenUsed, row.TokenUsed)
			assert.Equal(t, tt.inputTokens, row.InputTokens)
			assert.Equal(t, tt.completionTokens, row.CompletionTokens)
			assert.Equal(t, int64(tt.cacheReadTokens), row.CacheReadTokens)
			assert.Equal(t, tt.cacheWriteTokens, row.CacheWriteTokens)
			assert.Equal(t, int64(tt.cacheWriteTokens), row.CacheCreationTokens)
			assert.Equal(t, tt.trackedCount, row.TrackedCount)
			assert.Equal(t, tt.useTimeTotal, row.UseTimeTotal)
			assert.Equal(t, tt.useTimeCount, row.UseTimeCount)
			assert.Equal(t, tt.streamErrorCount, row.StreamErrorCount)
			assert.GreaterOrEqual(t, row.LastCalledAt, before)
			assert.Equal(t, row.LastCalledAt-row.LastCalledAt%3600, row.CreatedAt)
		})
	}
}

func TestLogQuotaDataFinalFailureAccumulatesAcrossFlushes(t *testing.T) {
	resetUsageAnalyticsState(t)
	hour := int64(36000)
	failure := func(createdAt int64) QuotaDataLogParams {
		return QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-a", CreatedAt: createdAt,
			UseGroup: "default", TokenID: 11, ChannelID: 3, NodeName: "node-a", IsFinalFailure: true}
	}
	loadRow := func() QuotaData {
		var rows []QuotaData
		require.NoError(t, DB.Find(&rows).Error)
		require.Len(t, rows, 1)
		return rows[0]
	}

	LogQuotaData(failure(hour + 100))
	SaveQuotaDataCache()
	row := loadRow()
	assert.Equal(t, hour, row.CreatedAt)
	assert.Equal(t, 0, row.Count)
	assert.Equal(t, 1, row.FailedCount)
	assert.Equal(t, int64(hour+100), row.LastCalledAt)

	LogQuotaData(failure(hour + 500))
	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-a", CreatedAt: hour + 300,
		UseGroup: "default", TokenID: 11, ChannelID: 3, NodeName: "node-a", Quota: 70, TokenUsed: 40, UseTimeSeconds: 2,
		CacheReadTokens: 12, CacheWriteTokens: 7, CacheCreationTokens: 7, OutcomeTracked: true})
	SaveQuotaDataCache()
	row = loadRow()
	assert.Equal(t, 1, row.Count)
	assert.Equal(t, 1, row.TrackedCount)
	assert.Equal(t, 2, row.FailedCount)
	assert.Equal(t, 70, row.Quota)
	assert.Equal(t, 40, row.TokenUsed)
	assert.Equal(t, int64(12), row.CacheReadTokens)
	assert.Equal(t, 7, row.CacheWriteTokens)
	assert.Equal(t, int64(7), row.CacheCreationTokens)
	assert.Equal(t, int64(2), row.UseTimeTotal)
	assert.Equal(t, int64(1), row.UseTimeCount)
	assert.Equal(t, int64(hour+500), row.LastCalledAt)

	// Late events must not move last_called_at backwards.
	LogQuotaData(failure(hour + 200))
	SaveQuotaDataCache()
	row = loadRow()
	assert.Equal(t, 3, row.FailedCount)
	assert.Equal(t, int64(12), row.CacheReadTokens)
	assert.Equal(t, int64(7), row.CacheCreationTokens)
	assert.Equal(t, int64(hour+500), row.LastCalledAt)
}

func TestLegacyQuotaDataReadersSkipFailureOnlyRows(t *testing.T) {
	resetUsageAnalyticsState(t)
	hour := int64(36000)
	rows := []QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-a", UseGroup: "default", CreatedAt: hour, Count: 2, Quota: 100, TokenUsed: 50, InputTokens: 30, CompletionTokens: 20, CacheReadTokens: 12, CacheWriteTokens: 7, CacheCreationTokens: 7, UseTimeTotal: 9, UseTimeCount: 2, LastCalledAt: hour + 1},
		{UserID: 1, Username: "alice", ModelName: "gpt-broken", UseGroup: "default", CreatedAt: hour, FailedCount: 3, LastCalledAt: hour + 2},
		{UserID: 2, Username: "bob", ModelName: "gpt-a", UseGroup: "default", CreatedAt: hour, FailedCount: 1, LastCalledAt: hour + 3},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
	}

	all, err := GetAllQuotaDates(hour, hour+3600, "", UsageOrgFilter{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "gpt-a", all[0].ModelName)
	assert.Equal(t, 2, all[0].Count)
	assert.Equal(t, int64(12), all[0].CacheReadTokens)
	assert.Equal(t, int64(7), all[0].CacheCreationTokens)

	byUsername, err := GetAllQuotaDates(hour, hour+3600, "bob", UsageOrgFilter{})
	require.NoError(t, err)
	assert.Empty(t, byUsername)

	alice, err := GetAllQuotaDates(hour, hour+3600, "alice", UsageOrgFilter{})
	require.NoError(t, err)
	require.Len(t, alice, 1)
	assert.Equal(t, 30, alice[0].InputTokens)
	assert.Equal(t, 20, alice[0].CompletionTokens)
	assert.Equal(t, 7, alice[0].CacheWriteTokens)
	assert.Equal(t, int64(9), alice[0].UseTimeTotal)
	assert.Equal(t, int64(2), alice[0].UseTimeCount)
	assert.Equal(t, int64(hour+1), alice[0].LastCalledAt)
	assert.Zero(t, alice[0].FailedCount)

	byUserID, err := GetQuotaDataByUserId(1, hour, hour+3600)
	require.NoError(t, err)
	require.Len(t, byUserID, 1)
	assert.Equal(t, "gpt-a", byUserID[0].ModelName)
	assert.Equal(t, int64(12), byUserID[0].CacheReadTokens)
	assert.Equal(t, int64(7), byUserID[0].CacheCreationTokens)

	byUser, err := GetUserQuotaTrend(hour, hour+3600, UsageAnalyticsGroupUser, UsageOrgFilter{})
	require.NoError(t, err)
	require.Len(t, byUser, 1)
	assert.Equal(t, "alice", byUser[0].Username)
	assert.Equal(t, int64(12), byUser[0].CacheReadTokens)
	assert.Equal(t, int64(7), byUser[0].CacheCreationTokens)

	flow, err := GetFlowQuotaData(hour, hour+3600, "", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, flow, 1)
	assert.Equal(t, "gpt-a", flow[0].ModelName)
	assert.Equal(t, "alice", flow[0].Username)
}
