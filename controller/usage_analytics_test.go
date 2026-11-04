package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type usageAnalyticsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Summary *model.UsageAnalyticsSummary `json:"summary"`
		Rows    []map[string]any             `json:"rows"`
	} `json:"data"`
}

func setupUsageAnalyticsControllerTestDB(t *testing.T) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.QuotaData{}, &model.Log{}))
	rows := []model.QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-a", CreatedAt: 3600, Count: 2, Quota: 100, TokenUsed: 40, LastCalledAt: 3601},
		{UserID: 2, Username: "bob", ModelName: "gpt-b", CreatedAt: 3600, Count: 5, Quota: 300, TokenUsed: 90, LastCalledAt: 3602},
	}
	for i := range rows {
		require.NoError(t, model.DB.Create(&rows[i]).Error)
	}
}

type apiStatusResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func serveUsageAnalytics[T any](t *testing.T, handler gin.HandlerFunc, userID int, target string) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if userID > 0 {
		ctx.Set("id", userID)
	}
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload T
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

func TestUsageAnalyticsEndpointsValidateParams(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)
	const day = 86400

	tests := []struct {
		name    string
		handler gin.HandlerFunc
		target  string
		success bool
		message string
	}{
		{name: "admin accepts 92 days", handler: GetAdminUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 1+92*day), success: true},
		{name: "admin rejects more than 92 days", handler: GetAdminUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 2+92*day), message: "time range cannot exceed 92 days"},
		{name: "admin rejects unknown group_by", handler: GetAdminUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=channel", message: "invalid group_by"},
		{name: "admin accepts organization group_by with a filter", handler: GetAdminUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=team&business_unit=BU-A", success: true},
		{name: "self rejects organization group_by", handler: GetSelfUsageAnalytics,
			target: "/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=department", message: "invalid group_by"},
		{name: "user trend accepts organization dimensions", handler: GetQuotaDatesByUser,
			target: "/api/data/users?start_timestamp=1&end_timestamp=7200&dimension=department&team=Web", success: true},
		{name: "user trend rejects unknown dimensions", handler: GetQuotaDatesByUser,
			target: "/api/data/users?start_timestamp=1&end_timestamp=7200&dimension=model", message: "invalid dimension"},
		{name: "org options", handler: GetUsageOrgOptions, target: "/api/data/org-options", success: true},
		{name: "admin requires group_by", handler: GetAdminUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200", message: "invalid group_by"},
		{name: "admin rejects end equal to start", handler: GetAdminUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=3600&end_timestamp=3600&group_by=summary", message: "invalid end_timestamp"},
		{name: "admin rejects missing start", handler: GetAdminUsageAnalytics,
			target: "/api/data/analytics?end_timestamp=3600&group_by=summary", message: "invalid start_timestamp"},
		{name: "self accepts 30 days", handler: GetSelfUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics/self?start_timestamp=1&end_timestamp=%d&group_by=summary", 1+30*day), success: true},
		{name: "self rejects more than 30 days", handler: GetSelfUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics/self?start_timestamp=1&end_timestamp=%d&group_by=summary", 2+30*day), message: "time range cannot exceed 30 days"},
		{name: "self rejects admin-only group_by", handler: GetSelfUsageAnalytics,
			target: "/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=user", message: "invalid group_by"},
		{name: "peak tpm accepts 92 days", handler: GetPeakTPM,
			target: fmt.Sprintf("/api/data/peak-tpm?start_timestamp=1&end_timestamp=%d", 1+92*day), success: true},
		{name: "peak tpm rejects more than 92 days", handler: GetPeakTPM,
			target: fmt.Sprintf("/api/data/peak-tpm?start_timestamp=1&end_timestamp=%d", 2+92*day), message: "time range cannot exceed 92 days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := serveUsageAnalytics[apiStatusResponse](t, tt.handler, 1, tt.target)
			assert.Equal(t, tt.success, payload.Success)
			assert.Equal(t, tt.message, payload.Message)
		})
	}
}

func TestGetSelfUsageAnalyticsForcesCaller(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)

	summary := serveUsageAnalytics[usageAnalyticsResponse](t, GetSelfUsageAnalytics, 1,
		"/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=summary&username=bob")
	require.True(t, summary.Success, summary.Message)
	require.NotNil(t, summary.Data.Summary)
	assert.Equal(t, int64(2), summary.Data.Summary.RequestCount)
	assert.Equal(t, int64(100), summary.Data.Summary.Quota)
	assert.Equal(t, int64(1), summary.Data.Summary.ActiveUsers)

	byModel := serveUsageAnalytics[usageAnalyticsResponse](t, GetSelfUsageAnalytics, 1,
		"/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=model")
	require.True(t, byModel.Success, byModel.Message)
	assert.Nil(t, byModel.Data.Summary)
	require.Len(t, byModel.Data.Rows, 1)
	assert.Equal(t, "gpt-a", byModel.Data.Rows[0]["model_name"])

	unauthenticated := serveUsageAnalytics[apiStatusResponse](t, GetSelfUsageAnalytics, 0,
		"/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=summary")
	assert.False(t, unauthenticated.Success)
}

func TestGetAdminUsageAnalyticsOmitsDimensionsOutsideGrouping(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)

	payload := serveUsageAnalytics[usageAnalyticsResponse](t, GetAdminUsageAnalytics, 1,
		"/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=user&username=bob")
	require.True(t, payload.Success, payload.Message)
	assert.Nil(t, payload.Data.Summary)
	require.Len(t, payload.Data.Rows, 1)
	row := payload.Data.Rows[0]
	assert.Equal(t, "bob", row["username"])
	assert.Equal(t, float64(2), row["user_id"])
	assert.Equal(t, "", row["display_name"])
	assert.Equal(t, float64(5), row["request_count"])
	// Unassigned users still carry the organization fields, as empty strings.
	assert.Equal(t, "", row["business_unit"])
	assert.Equal(t, "", row["department"])
	assert.Equal(t, "", row["team"])
	assert.NotContains(t, row, "model_name")
	assert.NotContains(t, row, "channel_id")
	assert.NotContains(t, row, "channel_name")
	assert.NotContains(t, row, "user_count")
}

func TestGetPeakTPMReturnsEmptyListWithoutTraffic(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/peak-tpm?start_timestamp=1&end_timestamp=7200", nil)
	GetPeakTPM(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"success":true,"message":"","data":[]}`, recorder.Body.String())
}
