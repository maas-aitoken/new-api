package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Only the master node migrates a separate log DB, so logs may lack iself_email.
func TestDashboardReadsWorkOnLogsWithoutIselfEmail(t *testing.T) {
	resetUsageAnalyticsState(t)
	legacyLogs, err := gorm.Open(sqlite.Open("file:legacy_logs?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, legacyLogs.Exec(`CREATE TABLE logs (id INTEGER PRIMARY KEY, user_id INTEGER, created_at INTEGER, type INTEGER,
		username TEXT DEFAULT '', model_name TEXT DEFAULT '', quota INTEGER DEFAULT 0, prompt_tokens INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0, other TEXT DEFAULT '')`).Error)
	previous := LOG_DB
	LOG_DB = legacyLogs
	t.Cleanup(func() {
		LOG_DB = previous
		if sqlDB, err := legacyLogs.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	const hour = int64(108000)
	require.NoError(t, DB.Create(&User{Id: 21, Username: "alice", Password: "password-a", AffCode: "aff-legacy-a",
		Email: "alice@example.com", Status: common.UserStatusEnabled, SecondaryOrganization: "BU-A"}).Error)
	require.NoError(t, DB.Create(&QuotaData{UserID: 21, Username: "alice", ModelName: "gpt-a", CreatedAt: hour,
		Count: 2, Quota: 200, TokenUsed: 150, TrackedCount: 2, LastCalledAt: hour + 30}).Error)
	require.NoError(t, legacyLogs.Exec(`INSERT INTO logs (user_id, created_at, type, username, model_name, quota, prompt_tokens, completion_tokens)
		VALUES (21, ?, ?, 'alice', 'gpt-a', 200, 100, 50)`, hour+30, LogTypeConsume).Error)
	start, end := hour, hour+3599

	self, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupModel, UserID: 21})
	require.NoError(t, err)
	require.Len(t, self.Rows, 1)
	assert.Equal(t, int64(2), self.Rows[0].RequestCount)

	units, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupBusinessUnit})
	require.NoError(t, err)
	require.Len(t, units.Rows, 1)
	assert.Equal(t, int64(200), units.Rows[0].Quota)

	chart, err := GetQuotaDataByUserId(21, start, end)
	require.NoError(t, err)
	require.Len(t, chart, 1)
	assert.Equal(t, 200, chart[0].Quota)

	for _, params := range []PeakTPMParams{{UserID: 21}, {Org: UsageOrgFilter{BusinessUnit: "BU-A"}}} {
		params.Start, params.End = start, end
		points, err := GetPeakTPM(params)
		require.NoError(t, err)
		assert.Equal(t, []PeakTPMPoint{{Hour: hour, PeakTPM: 150}}, points)
	}

	iself, err := GetIselfUsageAnalytics(IselfUsageParams{Start: start, End: end, UserID: 21})
	require.NoError(t, err)
	assert.Empty(t, iself)
}

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

// iSelf requests are recorded under the forwarding account "gateway".
func TestIselfUsageIsAttributedToTheEmailAccount(t *testing.T) {
	resetUsageAnalyticsState(t)
	const hour = int64(108000)
	users := []User{
		{Id: 10, Username: "gateway", Password: "password-g", AffCode: "aff-g", Status: common.UserStatusEnabled,
			SecondaryOrganization: "BU-G", OrgStr: "BU-G"},
		{Id: 11, Username: "alice", Password: "password-a", AffCode: "aff-a", Email: " Alice@Example.com ", Status: common.UserStatusEnabled,
			SecondaryOrganization: "BU-A", OrgStr: "BU-A"},
		{Id: 12, Username: "bob", Password: "password-b", AffCode: "aff-b", Email: "bob@example.com", Status: common.UserStatusEnabled,
			SecondaryOrganization: "BU-B", OrgStr: "BU-B"},
		// Two accounts share this email, so its usage is not moved.
		{Id: 13, Username: "twin1", Password: "password-t1", AffCode: "aff-t1", Email: "shared@example.com", Status: common.UserStatusEnabled},
		{Id: 14, Username: "twin2", Password: "password-t2", AffCode: "aff-t2", Email: "shared@example.com", Status: common.UserStatusEnabled},
	}
	for i := range users {
		require.NoError(t, DB.Create(&users[i]).Error)
	}
	quotaRows := []QuotaData{
		{UserID: 10, Username: "gateway", ModelName: "gpt-a", CreatedAt: hour, Count: 5, Quota: 600, TokenUsed: 550,
			InputTokens: 440, CompletionTokens: 110, CacheWriteTokens: 30, CacheCreationTokens: 30, TrackedCount: 5, LastCalledAt: hour + 120},
		{UserID: 11, Username: "alice", ModelName: "gpt-a", CreatedAt: hour, Count: 2, Quota: 200, TokenUsed: 200,
			InputTokens: 160, CompletionTokens: 40, TrackedCount: 2, LastCalledAt: hour + 30},
	}
	for i := range quotaRows {
		require.NoError(t, DB.Create(&quotaRows[i]).Error)
	}
	consume := func(userID int, username, email string, createdAt int64, quota, prompt, completion int) Log {
		return Log{UserId: userID, Username: username, IselfEmail: email, ModelName: "gpt-a", Type: LogTypeConsume,
			CreatedAt: createdAt, Quota: quota, PromptTokens: prompt, CompletionTokens: completion}
	}
	logs := []Log{
		consume(10, "gateway", "alice@example.com", hour+60, 100, 80, 20),
		consume(10, "gateway", "ALICE@example.com", hour+120, 200, 160, 40),
		consume(10, "gateway", "bob@example.com", hour+60, 150, 80, 20),
		consume(10, "gateway", "bob@example.com", hour+90, 50, 40, 10),
		consume(10, "gateway", "shared@example.com", hour+60, 100, 80, 20),
		// alice's own request, plus one she sent with her own iSelf header.
		consume(11, "alice", "", hour, 150, 120, 30),
		consume(11, "alice", "alice@example.com", hour+30, 50, 40, 10),
		{UserId: 10, Username: "gateway", IselfEmail: "alice@example.com", ModelName: "gpt-a", Type: LogTypeError, CreatedAt: hour + 60, Quota: 999},
	}
	for i := range logs {
		require.NoError(t, LOG_DB.Create(&logs[i]).Error)
	}
	start, end := hour, hour+3599

	t.Run("users", func(t *testing.T) {
		result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupUser})
		require.NoError(t, err)
		type userTotals struct {
			username                         string
			requests, quota, tokens, tracked int64
		}
		got := make([]userTotals, 0, len(result.Rows))
		for _, row := range result.Rows {
			got = append(got, userTotals{*row.Username, row.RequestCount, row.Quota, row.TokenUsed, row.TrackedCount})
		}
		// gateway keeps the shared-email request and its own outcome counters.
		assert.Equal(t, []userTotals{
			{"alice", 4, 500, 500, 2},
			{"bob", 2, 200, 150, 0},
			{"gateway", 1, 100, 100, 5},
		}, got)
		assert.Equal(t, "BU-B", *result.Rows[1].BusinessUnit)
		assert.Equal(t, "bob@example.com", *result.Rows[1].Email)
	})

	t.Run("summary is unchanged by the move", func(t *testing.T) {
		result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupSummary})
		require.NoError(t, err)
		assert.Equal(t, int64(7), result.Summary.RequestCount)
		assert.Equal(t, int64(800), result.Summary.Quota)
		assert.Equal(t, int64(3), result.Summary.ActiveUsers)
		assert.Equal(t, int64(1), result.Summary.ModelCount)
	})

	t.Run("self", func(t *testing.T) {
		alice, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupModel, UserID: 11})
		require.NoError(t, err)
		require.Len(t, alice.Rows, 1)
		assert.Equal(t, int64(4), alice.Rows[0].RequestCount)
		assert.Equal(t, int64(500), alice.Rows[0].Quota)
		assert.Equal(t, int64(hour+120), alice.Rows[0].LastCalledAt)

		gateway, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupSummary, UserID: 10})
		require.NoError(t, err)
		assert.Equal(t, int64(1), gateway.Summary.RequestCount)
		assert.Equal(t, int64(100), gateway.Summary.Quota)
	})

	t.Run("organization", func(t *testing.T) {
		result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupBusinessUnit})
		require.NoError(t, err)
		quotaByUnit := make(map[string]int64)
		for _, row := range result.Rows {
			quotaByUnit[*row.BusinessUnit] = row.Quota
		}
		assert.Equal(t, map[string]int64{"BU-A": 500, "BU-B": 200, "BU-G": 100}, quotaByUnit)

		filtered, err := GetUsageAnalytics(UsageAnalyticsParams{Start: start, End: end, GroupBy: UsageAnalyticsGroupSummary,
			Org: UsageOrgFilter{BusinessUnit: "BU-B"}})
		require.NoError(t, err)
		assert.Equal(t, int64(2), filtered.Summary.RequestCount)
		assert.Equal(t, int64(1), filtered.Summary.ActiveUsers)
	})

	t.Run("charts", func(t *testing.T) {
		aliceRows, err := GetQuotaDataByUserId(11, start, end)
		require.NoError(t, err)
		require.Len(t, aliceRows, 1)
		assert.Equal(t, 4, aliceRows[0].Count)
		assert.Equal(t, 500, aliceRows[0].Quota)
		assert.Equal(t, hour, aliceRows[0].CreatedAt)

		gatewayRows, err := GetQuotaDataByUserId(10, start, end)
		require.NoError(t, err)
		require.Len(t, gatewayRows, 1)
		assert.Equal(t, 1, gatewayRows[0].Count)
		assert.Equal(t, int64(30), gatewayRows[0].CacheCreationTokens)

		bobRows, err := GetAllQuotaDates(start, end, "bob", UsageOrgFilter{})
		require.NoError(t, err)
		require.Len(t, bobRows, 1)
		assert.Equal(t, 12, bobRows[0].UserID)
		assert.Equal(t, 200, bobRows[0].Quota)

		trend, err := GetUserQuotaTrend(start, end, UsageAnalyticsGroupUser, UsageOrgFilter{})
		require.NoError(t, err)
		countByUser := make(map[string]int64)
		for _, point := range trend {
			countByUser[point.Username] = point.Count
		}
		assert.Equal(t, map[string]int64{"alice": 4, "bob": 2, "gateway": 1}, countByUser)
	})

	t.Run("peak tpm", func(t *testing.T) {
		for _, tt := range []struct {
			name   string
			params PeakTPMParams
			peak   int64
		}{
			// Minute 1 (hour+60..119) holds alice 100, bob 100+50 and shared 100.
			{name: "all traffic", peak: 350},
			{name: "self", params: PeakTPMParams{UserID: 11}, peak: 200},
			{name: "forwarding account", params: PeakTPMParams{UserID: 10}, peak: 100},
			{name: "username without own logs", params: PeakTPMParams{Username: "bob"}, peak: 150},
			{name: "organization", params: PeakTPMParams{Org: UsageOrgFilter{BusinessUnit: "BU-A"}}, peak: 200},
		} {
			t.Run(tt.name, func(t *testing.T) {
				params := tt.params
				params.Start, params.End = start, end
				points, err := GetPeakTPM(params)
				require.NoError(t, err)
				assert.Equal(t, []PeakTPMPoint{{Hour: hour, PeakTPM: tt.peak}}, points)
			})
		}
	})
}
