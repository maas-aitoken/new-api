package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetUsageAnalyticsState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	for _, table := range []string{"quota_data", "users", "channels"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	// logs live in LOG_DB, which may be a separate database.
	clearDialectTestLogs(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		CacheQuotaDataLock.Lock()
		CacheQuotaData = make(map[string]*QuotaData)
		CacheQuotaDataLock.Unlock()
	})
}

const (
	analyticsH1 = int64(7200)
	analyticsH2 = analyticsH1 + 3600
	analyticsH3 = analyticsH2 + 3600
)

func seedUsageAnalyticsFixture(t *testing.T) {
	t.Helper()
	// Both users sit in a "Core" department, under different business units.
	require.NoError(t, DB.Create(&User{Id: 1, Username: "alice", Password: "password-1", DisplayName: "Alice A", AffCode: "aff-alice",
		Email: "alice@example.com", Status: common.UserStatusEnabled, Avatar: "https://avatar.example.com/alice.png",
		SecondaryOrganization: "BU-A", TertiaryOrganization: "Core", Team: "Core", OrgStr: "BU-A/Core"}).Error)
	require.NoError(t, DB.Create(&User{Id: 2, Username: "bob", Password: "password-2", AffCode: "aff-bob",
		Email: "bob@example.com", Status: common.UserStatusResigned,
		SecondaryOrganization: "BU-B", TertiaryOrganization: "Core", Team: "Web", OrgStr: "BU-B/Core/Web"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "east", Type: constant.ChannelTypeAnthropic, Key: "k1"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "west", Type: constant.ChannelTypeOpenAI, Key: "k2"}).Error)

	rows := []QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-a", ChannelID: 1, CreatedAt: analyticsH1, Count: 5, Quota: 500, TokenUsed: 1000,
			InputTokens: 800, CompletionTokens: 200, CacheReadTokens: 300, CacheWriteTokens: 50, UseTimeTotal: 20, UseTimeCount: 4,
			StreamErrorCount: 1, TrackedCount: 5, LastCalledAt: analyticsH1 + 100},
		{UserID: 1, Username: "alice", ModelName: "gpt-b", ChannelID: 2, CreatedAt: analyticsH2, Count: 3, Quota: 200, TokenUsed: 600,
			InputTokens: 500, CompletionTokens: 100, CacheReadTokens: 100, UseTimeTotal: 9, UseTimeCount: 3, TrackedCount: 3, LastCalledAt: analyticsH2 + 200},
		// Consumption and final failure share an hourly bucket.
		{UserID: 2, Username: "bob", ModelName: "gpt-a", ChannelID: 1, CreatedAt: analyticsH2, Count: 3, Quota: 150, TokenUsed: 300,
			InputTokens: 250, CompletionTokens: 50, CacheWriteTokens: 10, UseTimeTotal: 6, UseTimeCount: 3, FailedCount: 1,
			TrackedCount: 3, LastCalledAt: analyticsH2 + 300},
		// Legacy row without detailed counters.
		{UserID: 2, Username: "bob", ModelName: "gpt-a", ChannelID: 2, CreatedAt: analyticsH1, Count: 4, Quota: 200, TokenUsed: 300},
		// Failure-only row with no matching user or channel.
		{UserID: 3, Username: "carol", ModelName: "gpt-c", ChannelID: 9, CreatedAt: analyticsH2, FailedCount: 2, LastCalledAt: analyticsH2 + 400},
		{UserID: 2, Username: "bob", ModelName: "gpt-b", ChannelID: 1, CreatedAt: analyticsH3, Count: 2, Quota: 80, TokenUsed: 100,
			InputTokens: 120, CompletionTokens: 80, UseTimeTotal: 4, UseTimeCount: 2, TrackedCount: 2, LastCalledAt: analyticsH3 + 50},
		{UserID: 1, Username: "alice", ModelName: "gpt-a", ChannelID: 1, CreatedAt: analyticsH1 - 3600, Count: 100, Quota: 9999, TokenUsed: 9999, LastCalledAt: analyticsH1 - 3500},
		{UserID: 1, Username: "alice", ModelName: "gpt-a", ChannelID: 1, CreatedAt: analyticsH3 + 3600, Count: 100, Quota: 9999, TokenUsed: 9999, LastCalledAt: analyticsH3 + 3700},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
	}
}

func TestGetUsageAnalyticsSummaryAggregatesClosedRange(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupSummary})
	require.NoError(t, err)
	require.NotNil(t, result.Summary)
	assert.Empty(t, result.Rows)
	assert.Equal(t, UsageAnalyticsSummary{
		UsageMetrics: UsageMetrics{
			RequestCount: 17, Quota: 1130, TokenUsed: 2300,
			InputTokens: 1670, CompletionTokens: 430, CacheReadTokens: 400, CacheWriteTokens: 60,
			UseTimeTotal: 39, UseTimeCount: 12, StreamErrorCount: 1, FailedCount: 3,
			// Legacy requests are excluded from tracked_count.
			TrackedCount: 13, LastCalledAt: analyticsH3 + 50,
		},
		// Failure-only rows do not count as active users or used models.
		ActiveUsers: 2,
		ModelCount:  2,
	}, *result.Summary)

	inner, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1 + 1, End: analyticsH3 - 1, GroupBy: UsageAnalyticsGroupSummary})
	require.NoError(t, err)
	assert.Equal(t, int64(6), inner.Summary.RequestCount)
	assert.Equal(t, int64(350), inner.Summary.Quota)
	assert.Equal(t, int64(3), inner.Summary.FailedCount)
	assert.Equal(t, int64(2), inner.Summary.ActiveUsers)
}

func TestGetUsageAnalyticsSummaryAppliesFilters(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	tests := []struct {
		name         string
		params       UsageAnalyticsParams
		requestCount int64
		quota        int64
		failedCount  int64
		trackedCount int64
		activeUsers  int64
		modelCount   int64
	}{
		{name: "username", params: UsageAnalyticsParams{Username: "bob"}, requestCount: 9, quota: 430, failedCount: 1, trackedCount: 5, activeUsers: 1, modelCount: 2},
		{name: "model", params: UsageAnalyticsParams{ModelName: "gpt-a"}, requestCount: 12, quota: 850, failedCount: 1, trackedCount: 8, activeUsers: 2, modelCount: 1},
		{name: "username and model", params: UsageAnalyticsParams{Username: "bob", ModelName: "gpt-b"}, requestCount: 2, quota: 80, trackedCount: 2, activeUsers: 1, modelCount: 1},
		{name: "user id", params: UsageAnalyticsParams{UserID: 1}, requestCount: 8, quota: 700, trackedCount: 8, activeUsers: 1, modelCount: 2},
		{name: "model prefix is not a match", params: UsageAnalyticsParams{ModelName: "gpt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.params
			params.Start, params.End, params.GroupBy = analyticsH1, analyticsH3, UsageAnalyticsGroupSummary
			result, err := GetUsageAnalytics(params)
			require.NoError(t, err)
			require.NotNil(t, result.Summary)
			assert.Equal(t, tt.requestCount, result.Summary.RequestCount)
			assert.Equal(t, tt.quota, result.Summary.Quota)
			assert.Equal(t, tt.failedCount, result.Summary.FailedCount)
			assert.Equal(t, tt.trackedCount, result.Summary.TrackedCount)
			assert.Equal(t, tt.activeUsers, result.Summary.ActiveUsers)
			assert.Equal(t, tt.modelCount, result.Summary.ModelCount)
		})
	}
}

func TestGetUsageAnalyticsGroupByModel(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupModel})
	require.NoError(t, err)
	assert.Nil(t, result.Summary)
	assert.Equal(t, []UsageAnalyticsRow{
		{
			UsageMetrics: UsageMetrics{RequestCount: 12, Quota: 850, TokenUsed: 1600, InputTokens: 1050, CompletionTokens: 250,
				CacheReadTokens: 300, CacheWriteTokens: 60, UseTimeTotal: 26, UseTimeCount: 7, StreamErrorCount: 1, FailedCount: 1,
				TrackedCount: 8, LastCalledAt: analyticsH2 + 300},
			ModelName: common.GetPointer("gpt-a"),
			UserCount: common.GetPointer(int64(2)),
		},
		{
			UsageMetrics: UsageMetrics{RequestCount: 5, Quota: 280, TokenUsed: 700, InputTokens: 620, CompletionTokens: 180,
				CacheReadTokens: 100, UseTimeTotal: 13, UseTimeCount: 5, TrackedCount: 5, LastCalledAt: analyticsH3 + 50},
			ModelName: common.GetPointer("gpt-b"),
			UserCount: common.GetPointer(int64(2)),
		},
		{
			UsageMetrics: UsageMetrics{FailedCount: 2, LastCalledAt: analyticsH2 + 400},
			ModelName:    common.GetPointer("gpt-c"),
			UserCount:    common.GetPointer(int64(0)),
		},
	}, result.Rows)
}

func TestGetUsageAnalyticsForExternalPlatform(t *testing.T) {
	resetUsageAnalyticsState(t)
	require.NoError(t, DB.AutoMigrate(&ExternalQuotaData{}))
	require.NoError(t, DB.Exec("DELETE FROM external_quota_data").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM external_quota_data") })
	// users also has quota and created_at columns.
	require.NoError(t, DB.Create(&User{Id: 11, Username: "alice", Password: "password-1", AffCode: "aff-alice", Quota: 777,
		SecondaryOrganization: "BU-A", TertiaryOrganization: "Core", Team: "Core"}).Error)
	require.NoError(t, DB.Create(&User{Id: 12, Username: "bob", Password: "password-2", AffCode: "aff-bob", Quota: 888,
		SecondaryOrganization: "BU-B", TertiaryOrganization: "Core", Team: "Web"}).Error)
	rows := []ExternalQuotaData{
		{Platform: "us", LocalUserID: 11, RemoteUsername: "alice", ModelName: "model-a", RemoteChannelID: 1, CreatedAt: analyticsH1, Count: 2, Quota: 100},
		{Platform: "us", LocalUserID: 11, RemoteUsername: "alice", ModelName: "model-b", RemoteChannelID: 2, CreatedAt: analyticsH2, Count: 1, Quota: 50},
		{Platform: "us", LocalUserID: 12, RemoteUsername: "bob", ModelName: "model-a", RemoteChannelID: 1, CreatedAt: analyticsH2, Count: 3, Quota: 200},
		{Platform: "jp", LocalUserID: 11, RemoteUsername: "alice", ModelName: "model-a", CreatedAt: analyticsH1, Count: 9, Quota: 999},
		{Platform: "us", LocalUserID: 11, RemoteUsername: "alice", ModelName: "model-a", CreatedAt: analyticsH3 + 3600, Count: 9, Quota: 999},
	}
	require.NoError(t, DB.Create(&rows).Error)

	for _, tt := range []struct {
		groupBy string
		rows    int
	}{
		{UsageAnalyticsGroupModel, 2}, {UsageAnalyticsGroupUser, 2}, {UsageAnalyticsGroupUserModel, 3},
		{UsageAnalyticsGroupModelChannel, 2}, {UsageAnalyticsGroupBusinessUnit, 2},
		{UsageAnalyticsGroupDepartment, 2}, {UsageAnalyticsGroupTeam, 2},
	} {
		result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: tt.groupBy, Platform: "us"})
		require.NoError(t, err, tt.groupBy)
		require.Len(t, result.Rows, tt.rows, tt.groupBy)
		total := int64(0)
		for _, row := range result.Rows {
			total += row.Quota
		}
		assert.Equal(t, int64(350), total, tt.groupBy)
		assert.GreaterOrEqual(t, result.Rows[0].Quota, result.Rows[len(result.Rows)-1].Quota, tt.groupBy)
	}

	for _, tt := range []struct {
		name            string
		params          UsageAnalyticsParams
		requests, quota int64
	}{
		{"all", UsageAnalyticsParams{}, 6, 350},
		{"self", UsageAnalyticsParams{UserID: 11}, 3, 150},
		{"managed people", UsageAnalyticsParams{UserIDs: []int{12}}, 3, 200},
		{"model", UsageAnalyticsParams{ModelName: "model-b"}, 1, 50},
		{"business unit", UsageAnalyticsParams{Org: UsageOrgFilter{BusinessUnit: "BU-B"}}, 3, 200},
	} {
		params := tt.params
		params.Start, params.End, params.GroupBy, params.Platform = analyticsH1, analyticsH3, UsageAnalyticsGroupSummary, "us"
		result, err := GetUsageAnalytics(params)
		require.NoError(t, err, tt.name)
		require.NotNil(t, result.Summary, tt.name)
		assert.Equal(t, tt.requests, result.Summary.RequestCount, tt.name)
		assert.Equal(t, tt.quota, result.Summary.Quota, tt.name)
	}
}

func TestGetUsageAnalyticsGroupByUserJoinsUserAttributes(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupUser})
	require.NoError(t, err)
	require.Len(t, result.Rows, 3)

	type userRow struct {
		userID       int
		username     string
		displayName  string
		email        string
		status       int
		org          [4]string
		requestCount int64
		quota        int64
		failedCount  int64
		trackedCount int64
	}
	got := make([]userRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		require.NotNil(t, row.UserID)
		require.NotNil(t, row.Username)
		require.NotNil(t, row.DisplayName)
		require.NotNil(t, row.Email)
		require.NotNil(t, row.UserStatus)
		require.NotNil(t, row.BusinessUnit)
		require.NotNil(t, row.Department)
		require.NotNil(t, row.Team)
		require.NotNil(t, row.OrgPath)
		assert.Nil(t, row.ModelName)
		assert.Nil(t, row.ChannelID)
		assert.Nil(t, row.UserCount)
		got = append(got, userRow{*row.UserID, *row.Username, *row.DisplayName, *row.Email, *row.UserStatus,
			[4]string{*row.BusinessUnit, *row.Department, *row.Team, *row.OrgPath},
			row.RequestCount, row.Quota, row.FailedCount, row.TrackedCount})
	}
	assert.Equal(t, []userRow{
		{userID: 1, username: "alice", displayName: "Alice A", email: "alice@example.com", status: common.UserStatusEnabled,
			org: [4]string{"BU-A", "Core", "Core", "BU-A/Core"}, requestCount: 8, quota: 700, trackedCount: 8},
		{userID: 2, username: "bob", email: "bob@example.com", status: common.UserStatusResigned,
			org: [4]string{"BU-B", "Core", "Web", "BU-B/Core/Web"}, requestCount: 9, quota: 430, failedCount: 1, trackedCount: 5},
		// Missing users keep empty attributes and status 0.
		{userID: 3, username: "carol", failedCount: 2},
	}, got)
}

func TestGetUsageAnalyticsOrganizationDimensions(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	type orgRow struct {
		org          string
		userCount    int64
		requestCount int64
		quota        int64
		failedCount  int64
	}
	orgKey := func(row UsageAnalyticsRow) string {
		var parts []string
		for _, level := range []*string{row.BusinessUnit, row.Department, row.Team} {
			if level != nil {
				parts = append(parts, *level)
			}
		}
		return strings.Join(parts, "|")
	}
	tests := []struct {
		name     string
		groupBy  string
		org      UsageOrgFilter
		expected []orgRow
	}{
		{name: "business unit", groupBy: UsageAnalyticsGroupBusinessUnit, expected: []orgRow{
			{org: "BU-A", userCount: 1, requestCount: 8, quota: 700},
			{org: "BU-B", userCount: 1, requestCount: 9, quota: 430, failedCount: 1},
			// Quota rows of a missing user are unassigned.
			{org: "", failedCount: 2},
		}},
		{name: "department keeps equal names under different business units", groupBy: UsageAnalyticsGroupDepartment, expected: []orgRow{
			{org: "BU-A|Core", userCount: 1, requestCount: 8, quota: 700},
			{org: "BU-B|Core", userCount: 1, requestCount: 9, quota: 430, failedCount: 1},
			{org: "|", failedCount: 2},
		}},
		{name: "team", groupBy: UsageAnalyticsGroupTeam, expected: []orgRow{
			{org: "BU-A|Core|Core", userCount: 1, requestCount: 8, quota: 700},
			{org: "BU-B|Core|Web", userCount: 1, requestCount: 9, quota: 430, failedCount: 1},
			{org: "||", failedCount: 2},
		}},
		{name: "department filter spans business units", groupBy: UsageAnalyticsGroupBusinessUnit, org: UsageOrgFilter{Department: "Core"}, expected: []orgRow{
			{org: "BU-A", userCount: 1, requestCount: 8, quota: 700},
			{org: "BU-B", userCount: 1, requestCount: 9, quota: 430, failedCount: 1},
		}},
		{name: "unassigned filter", groupBy: UsageAnalyticsGroupBusinessUnit, org: UsageOrgFilter{BusinessUnit: UsageOrgUnassigned}, expected: []orgRow{
			{org: "", failedCount: 2},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: tt.groupBy, Org: tt.org})
			require.NoError(t, err)
			got := make([]orgRow, 0, len(result.Rows))
			for _, row := range result.Rows {
				require.NotNil(t, row.UserCount)
				assert.Nil(t, row.Username)
				got = append(got, orgRow{orgKey(row), *row.UserCount, row.RequestCount, row.Quota, row.FailedCount})
			}
			assert.Equal(t, tt.expected, got)
		})
	}

	filters := []struct {
		name         string
		org          UsageOrgFilter
		requestCount int64
		quota        int64
		failedCount  int64
		activeUsers  int64
	}{
		{name: "business unit", org: UsageOrgFilter{BusinessUnit: "BU-B"}, requestCount: 9, quota: 430, failedCount: 1, activeUsers: 1},
		{name: "business unit and department", org: UsageOrgFilter{BusinessUnit: "BU-A", Department: "Core"}, requestCount: 8, quota: 700, activeUsers: 1},
		{name: "team", org: UsageOrgFilter{Team: "Web"}, requestCount: 9, quota: 430, failedCount: 1, activeUsers: 1},
		{name: "unassigned team", org: UsageOrgFilter{Team: UsageOrgUnassigned}, failedCount: 2},
		{name: "unknown business unit", org: UsageOrgFilter{BusinessUnit: "BU-X"}},
	}
	for _, tt := range filters {
		t.Run("summary filter "+tt.name, func(t *testing.T) {
			result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupSummary, Org: tt.org})
			require.NoError(t, err)
			require.NotNil(t, result.Summary)
			assert.Equal(t, tt.requestCount, result.Summary.RequestCount)
			assert.Equal(t, tt.quota, result.Summary.Quota)
			assert.Equal(t, tt.failedCount, result.Summary.FailedCount)
			assert.Equal(t, tt.activeUsers, result.Summary.ActiveUsers)
		})
	}

	t.Run("filter combines with model and user groupings", func(t *testing.T) {
		org := UsageOrgFilter{BusinessUnit: "BU-B"}
		byModel, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupModel, Org: org})
		require.NoError(t, err)
		require.Len(t, byModel.Rows, 2)
		assert.Equal(t, "gpt-a", *byModel.Rows[0].ModelName)
		assert.Equal(t, int64(7), byModel.Rows[0].RequestCount)
		assert.Equal(t, "gpt-b", *byModel.Rows[1].ModelName)

		byUser, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupUser, Org: org})
		require.NoError(t, err)
		require.Len(t, byUser.Rows, 1)
		assert.Equal(t, "bob", *byUser.Rows[0].Username)
	})
}

func TestGetUserQuotaTrendByDimension(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	type point struct {
		key       string
		createdAt int64
		quota     int64
	}
	collect := func(t *testing.T, dimension string, org UsageOrgFilter) []point {
		t.Helper()
		rows, err := GetUserQuotaTrend(analyticsH1, analyticsH3, dimension, org)
		require.NoError(t, err)
		got := make([]point, 0, len(rows))
		for _, row := range rows {
			key := row.Username
			if dimension != UsageAnalyticsGroupUser {
				assert.Empty(t, row.Username)
				require.NotNil(t, row.BusinessUnit)
				key = *row.BusinessUnit
				if row.Department != nil {
					key += "|" + *row.Department
				}
			}
			got = append(got, point{key, row.CreatedAt, row.Quota})
		}
		return got
	}

	tests := []struct {
		name      string
		dimension string
		org       UsageOrgFilter
		expected  []point
	}{
		// Failure-only rows never reach the consumption charts.
		{name: "user", dimension: UsageAnalyticsGroupUser, expected: []point{
			{"alice", analyticsH1, 500}, {"alice", analyticsH2, 200}, {"bob", analyticsH1, 200}, {"bob", analyticsH2, 150}, {"bob", analyticsH3, 80},
		}},
		{name: "business unit", dimension: UsageAnalyticsGroupBusinessUnit, expected: []point{
			{"BU-A", analyticsH1, 500}, {"BU-A", analyticsH2, 200}, {"BU-B", analyticsH1, 200}, {"BU-B", analyticsH2, 150}, {"BU-B", analyticsH3, 80},
		}},
		{name: "department under a business unit filter", dimension: UsageAnalyticsGroupDepartment, org: UsageOrgFilter{BusinessUnit: "BU-B"}, expected: []point{
			{"BU-B|Core", analyticsH1, 200}, {"BU-B|Core", analyticsH2, 150}, {"BU-B|Core", analyticsH3, 80},
		}},
		{name: "user under a team filter", dimension: UsageAnalyticsGroupUser, org: UsageOrgFilter{Team: "Core"}, expected: []point{
			{"alice", analyticsH1, 500}, {"alice", analyticsH2, 200},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expected, collect(t, tt.dimension, tt.org))
		})
	}

	_, err := GetUserQuotaTrend(analyticsH1, analyticsH3, UsageAnalyticsGroupModel, UsageOrgFilter{})
	assert.Error(t, err)

	t.Run("model chart totals follow the organization filter", func(t *testing.T) {
		rows, err := GetAllQuotaDates(analyticsH1, analyticsH3, "", UsageOrgFilter{BusinessUnit: "BU-A"})
		require.NoError(t, err)
		var count, quota int
		for _, row := range rows {
			count += row.Count
			quota += row.Quota
		}
		assert.Equal(t, 8, count)
		assert.Equal(t, 700, quota)

		mismatched, err := GetAllQuotaDates(analyticsH1, analyticsH3, "bob", UsageOrgFilter{BusinessUnit: "BU-A"})
		require.NoError(t, err)
		assert.Empty(t, mismatched)
	})
}

// "_" is literal and soft-deleted accounts match.
func TestMatchUsageUserIdsByNameOrEmail(t *testing.T) {
	resetUsageAnalyticsState(t)
	users := []User{
		{Id: 1, Username: "san_zhang", Email: "san_zhang@corp.example", DisplayName: "张三丰", Password: "password-1", AffCode: "aff-1"},
		{Id: 2, Username: "si_li", Email: "Si_Li@Example.com", DisplayName: "李四", Password: "password-2", AffCode: "aff-2"},
		{Id: 3, Username: "sanxzhang", DisplayName: "三丰", Password: "password-3", AffCode: "aff-3"},
		{Id: 4, Username: "gone", Email: "gone@corp.example", DisplayName: "离职", Password: "password-4", AffCode: "aff-4"},
	}
	for i := range users {
		require.NoError(t, DB.Create(&users[i]).Error)
	}
	require.NoError(t, DB.Delete(&User{}, 4).Error)

	for keyword, want := range map[string][]int{
		"SAN_":         {1},
		"si_li@":       {2},
		"example.COM":  {2},
		"三丰":           {1, 3},
		" 李四 ":         {2},
		"corp.example": {1, 4},
		"nobody-there": {},
	} {
		ids, err := MatchUsageUserIds(keyword)
		require.NoError(t, err, keyword)
		assert.Equal(t, want, ids, keyword)
	}
}

func TestGetUsageOrgOptionsListsDistinctPaths(t *testing.T) {
	resetUsageAnalyticsState(t)
	users := []User{
		{Id: 1, Username: "u1", Password: "password-1", AffCode: "aff-1", SecondaryOrganization: "BU-B", TertiaryOrganization: "Core", Team: "Web"},
		{Id: 2, Username: "u2", Password: "password-2", AffCode: "aff-2", SecondaryOrganization: "BU-B", TertiaryOrganization: "Core", Team: "Web"},
		{Id: 3, Username: "u3", Password: "password-3", AffCode: "aff-3", SecondaryOrganization: "BU-A", TertiaryOrganization: "Core", Team: "Core"},
		{Id: 4, Username: "u4", Password: "password-4", AffCode: "aff-4"},
		// Soft-deleted users keep their historical usage, so their path stays listed.
		{Id: 5, Username: "u5", Password: "password-5", AffCode: "aff-5", SecondaryOrganization: "BU-C", TertiaryOrganization: "Ops", Team: "Ops"},
	}
	for i := range users {
		require.NoError(t, DB.Create(&users[i]).Error)
	}
	require.NoError(t, DB.Delete(&User{}, 5).Error)

	options, err := GetUsageOrgOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, []UsageOrgOption{
		{},
		{BusinessUnit: "BU-A", Department: "Core", Team: "Core"},
		{BusinessUnit: "BU-B", Department: "Core", Team: "Web"},
		{BusinessUnit: "BU-C", Department: "Ops", Team: "Ops"},
	}, options)

	scoped, err := GetUsageOrgOptions([]int{2, 3})
	require.NoError(t, err)
	assert.Equal(t, []UsageOrgOption{
		{BusinessUnit: "BU-A", Department: "Core", Team: "Core"},
		{BusinessUnit: "BU-B", Department: "Core", Team: "Web"},
	}, scoped)
	none, err := GetUsageOrgOptions([]int{})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestGetUsageAnalyticsGroupByUserModel(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupUserModel})
	require.NoError(t, err)

	type userModelRow struct {
		username     string
		displayName  string
		avatar       string
		modelName    string
		requestCount int64
		quota        int64
	}
	got := make([]userModelRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		require.NotNil(t, row.UserID)
		require.NotNil(t, row.Username)
		require.NotNil(t, row.DisplayName)
		require.NotNil(t, row.Avatar)
		require.NotNil(t, row.ModelName)
		got = append(got, userModelRow{*row.Username, *row.DisplayName, *row.Avatar, *row.ModelName, row.RequestCount, row.Quota})
	}
	const aliceAvatar = "https://avatar.example.com/alice.png"
	assert.Equal(t, []userModelRow{
		{username: "alice", displayName: "Alice A", avatar: aliceAvatar, modelName: "gpt-a", requestCount: 5, quota: 500},
		{username: "bob", modelName: "gpt-a", requestCount: 7, quota: 350},
		{username: "alice", displayName: "Alice A", avatar: aliceAvatar, modelName: "gpt-b", requestCount: 3, quota: 200},
		{username: "bob", modelName: "gpt-b", requestCount: 2, quota: 80},
		{username: "carol", modelName: "gpt-c"},
	}, got)
}

func TestGetUsageAnalyticsGroupByModelChannelJoinsChannel(t *testing.T) {
	resetUsageAnalyticsState(t)
	seedUsageAnalyticsFixture(t)

	result, err := GetUsageAnalytics(UsageAnalyticsParams{Start: analyticsH1, End: analyticsH3, GroupBy: UsageAnalyticsGroupModelChannel})
	require.NoError(t, err)

	type modelChannelRow struct {
		modelName    string
		channelID    int
		channelName  string
		channelType  int
		requestCount int64
		failedCount  int64
	}
	got := make([]modelChannelRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		require.NotNil(t, row.ModelName)
		require.NotNil(t, row.ChannelID)
		require.NotNil(t, row.ChannelName)
		require.NotNil(t, row.ChannelType)
		assert.Nil(t, row.UserID)
		assert.Nil(t, row.Username)
		got = append(got, modelChannelRow{*row.ModelName, *row.ChannelID, *row.ChannelName, *row.ChannelType, row.RequestCount, row.FailedCount})
	}
	anthropic, openAI := constant.ChannelTypeAnthropic, constant.ChannelTypeOpenAI
	assert.Equal(t, []modelChannelRow{
		{modelName: "gpt-a", channelID: 1, channelName: "east", channelType: anthropic, requestCount: 8, failedCount: 1},
		{modelName: "gpt-a", channelID: 2, channelName: "west", channelType: openAI, requestCount: 4},
		{modelName: "gpt-b", channelID: 2, channelName: "west", channelType: openAI, requestCount: 3},
		{modelName: "gpt-b", channelID: 1, channelName: "east", channelType: anthropic, requestCount: 2},
		// Missing channels retain an empty name and type 0.
		{modelName: "gpt-c", channelID: 9, channelName: "", failedCount: 2},
	}, got)
}

func TestGetPeakTPMTakesHourlyMaxOfMinuteTotals(t *testing.T) {
	resetUsageAnalyticsState(t)
	hour := int64(72000)
	logs := []Log{
		// Sum different users in the same minute before taking the hourly peak.
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour + 10, PromptTokens: 100, CompletionTokens: 50},
		{Username: "bob", ModelName: "gpt-b", Type: LogTypeConsume, CreatedAt: hour + 59, PromptTokens: 900, CompletionTokens: 100},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour + 300, PromptTokens: 200, CompletionTokens: 100},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour + 330, PromptTokens: 150, CompletionTokens: 50},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeError, CreatedAt: hour + 300, PromptTokens: 99999},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour + 3720, PromptTokens: 80, CompletionTokens: 20},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour - 1, PromptTokens: 99999},
		{Username: "alice", ModelName: "gpt-a", Type: LogTypeConsume, CreatedAt: hour + 3721, PromptTokens: 99999},
	}
	for i := range logs {
		require.NoError(t, LOG_DB.Create(&logs[i]).Error)
	}

	tests := []struct {
		name     string
		params   PeakTPMParams
		expected []PeakTPMPoint
	}{
		{name: "all traffic", expected: []PeakTPMPoint{{Hour: hour, PeakTPM: 1150}, {Hour: hour + 3600, PeakTPM: 100}}},
		{name: "username", params: PeakTPMParams{Username: "alice"}, expected: []PeakTPMPoint{{Hour: hour, PeakTPM: 500}, {Hour: hour + 3600, PeakTPM: 100}}},
		{name: "model", params: PeakTPMParams{ModelName: "gpt-b"}, expected: []PeakTPMPoint{{Hour: hour, PeakTPM: 1000}}},
		{name: "no traffic", params: PeakTPMParams{Username: "alice", ModelName: "gpt-b"}, expected: []PeakTPMPoint{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.params
			params.Start, params.End = hour, hour+3720
			points, err := GetPeakTPM(params)
			require.NoError(t, err)
			require.NotNil(t, points)
			assert.Equal(t, tt.expected, points)
		})
	}
}
