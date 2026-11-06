package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
	require.NoError(t, db.AutoMigrate(&model.QuotaData{}, &model.Log{}, &model.UserDataScope{}, &model.UserDataScopeOrg{}))
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
	return serveUsageAnalyticsAs[T](t, handler, userID, common.RoleAdminUser, target)
}

func serveUsageAnalyticsAs[T any](t *testing.T, handler gin.HandlerFunc, userID, role int, target string) T {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if userID > 0 {
		ctx.Set("id", userID)
		ctx.Set("role", role)
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
		// Zero means an administrator.
		role int
	}{
		{name: "admin accepts 92 days", handler: GetUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 1+92*day), success: true},
		{name: "admin rejects more than 92 days", handler: GetUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 2+92*day), message: "time range cannot exceed 92 days"},
		{name: "manager accepts 30 days", handler: GetUsageAnalytics, role: common.RoleCommonUser,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 1+30*day), success: true},
		{name: "manager rejects more than 30 days", handler: GetUsageAnalytics, role: common.RoleCommonUser,
			target: fmt.Sprintf("/api/data/analytics?start_timestamp=1&end_timestamp=%d&group_by=summary", 2+30*day), message: "time range cannot exceed 30 days"},
		{name: "manager peak tpm rejects more than 30 days", handler: GetPeakTPM, role: common.RoleCommonUser,
			target: fmt.Sprintf("/api/data/peak-tpm?start_timestamp=1&end_timestamp=%d", 2+30*day), message: "time range cannot exceed 30 days"},
		{name: "admin rejects unknown group_by", handler: GetUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=channel", message: "invalid group_by"},
		{name: "admin accepts organization group_by with a filter", handler: GetUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=team&business_unit=BU-A", success: true},
		{name: "self rejects organization group_by", handler: GetSelfUsageAnalytics,
			target: "/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=department", message: "invalid group_by"},
		{name: "user trend accepts organization dimensions", handler: GetQuotaDatesByUser,
			target: "/api/data/users?start_timestamp=1&end_timestamp=7200&dimension=department&team=Web", success: true},
		{name: "user trend rejects unknown dimensions", handler: GetQuotaDatesByUser,
			target: "/api/data/users?start_timestamp=1&end_timestamp=7200&dimension=model", message: "invalid dimension"},
		{name: "org options", handler: GetUsageOrgOptions, target: "/api/data/org-options", success: true},
		{name: "admin requires group_by", handler: GetUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=1&end_timestamp=7200", message: "invalid group_by"},
		{name: "admin rejects end equal to start", handler: GetUsageAnalytics,
			target: "/api/data/analytics?start_timestamp=3600&end_timestamp=3600&group_by=summary", message: "invalid end_timestamp"},
		{name: "admin rejects missing start", handler: GetUsageAnalytics,
			target: "/api/data/analytics?end_timestamp=3600&group_by=summary", message: "invalid start_timestamp"},
		{name: "self accepts 30 days", handler: GetSelfUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics/self?start_timestamp=1&end_timestamp=%d&group_by=summary", 1+30*day), success: true},
		{name: "self rejects more than 30 days", handler: GetSelfUsageAnalytics,
			target: fmt.Sprintf("/api/data/analytics/self?start_timestamp=1&end_timestamp=%d&group_by=summary", 2+30*day), message: "time range cannot exceed 30 days"},
		{name: "self accepts the export groupings", handler: GetSelfUsageAnalytics,
			target: "/api/data/analytics/self?start_timestamp=1&end_timestamp=7200&group_by=model_channel", success: true},
		{name: "peak tpm accepts 92 days", handler: GetPeakTPM,
			target: fmt.Sprintf("/api/data/peak-tpm?start_timestamp=1&end_timestamp=%d", 1+92*day), success: true},
		{name: "peak tpm rejects more than 92 days", handler: GetPeakTPM,
			target: fmt.Sprintf("/api/data/peak-tpm?start_timestamp=1&end_timestamp=%d", 2+92*day), message: "time range cannot exceed 92 days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role := tt.role
			if role == 0 {
				role = common.RoleAdminUser
			}
			payload := serveUsageAnalyticsAs[apiStatusResponse](t, tt.handler, 1, role, tt.target)
			assert.Equal(t, tt.success, payload.Success)
			assert.Equal(t, tt.message, payload.Message)
		})
	}
}

func TestUsageAnalyticsHonoursManagedPeople(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)
	require.NoError(t, model.DB.Create(&[]model.User{
		{Id: 1, Username: "alice", Password: "password-a", AffCode: "aff-a", Status: common.UserStatusEnabled, SecondaryOrganization: "BU-A"},
		{Id: 2, Username: "bob", Password: "password-b", AffCode: "aff-b", Status: common.UserStatusEnabled},
		{Id: 3, Username: "carol", Password: "password-c", AffCode: "aff-c", Status: common.UserStatusEnabled, SecondaryOrganization: "BU-C"},
	}).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{UserID: 3, Username: "carol", ModelName: "gpt-c", CreatedAt: 3600,
		Count: 7, Quota: 700, TokenUsed: 70, LastCalledAt: 3603}).Error)
	_, _, err := model.ReplaceUserDataScope(1, []int{2}, nil, 99)
	require.NoError(t, err)
	const byUser = "/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=user"
	usernames := func(payload usageAnalyticsResponse) []string {
		require.True(t, payload.Success, payload.Message)
		names := make([]string, 0, len(payload.Data.Rows))
		for _, row := range payload.Data.Rows {
			names = append(names, row["username"].(string))
		}
		return names
	}

	assert.Equal(t, []string{"bob", "alice"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 1, common.RoleCommonUser, byUser)))
	assert.Equal(t, []string{"carol"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 3, common.RoleCommonUser, byUser)))
	assert.Equal(t, []string{"carol", "bob", "alice"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 3, common.RoleAdminUser, byUser)))
	// The user filter cannot reach outside the scope.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 2).Updates(map[string]any{"display_name": "鲍勃", "email": "bob@example.com"}).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 3).Update("email", "carol@example.com").Error)
	assert.Empty(t, usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 1, common.RoleCommonUser, byUser+"&username=carol")))
	assert.Equal(t, []string{"bob"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 1, common.RoleCommonUser, byUser+"&username=EXAMPLE.com")))
	assert.Equal(t, []string{"bob"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 1, common.RoleCommonUser, byUser+"&username=%E9%B2%8D")))
	assert.Equal(t, []string{"carol", "bob"},
		usernames(serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 3, common.RoleAdminUser, byUser+"&username=example")))

	type scopeResponse struct {
		Success bool `json:"success"`
		Data    struct {
			AllUsers  bool `json:"all_users"`
			UserCount int  `json:"user_count"`
		} `json:"data"`
	}
	for _, tt := range []struct {
		userID, role, count int
		all                 bool
	}{
		{userID: 1, role: common.RoleCommonUser, count: 2},
		{userID: 3, role: common.RoleCommonUser, count: 1},
		{userID: 3, role: common.RoleAdminUser, all: true},
	} {
		scope := serveUsageAnalyticsAs[scopeResponse](t, GetUsageDataScope, tt.userID, tt.role, "/api/data/scope")
		require.True(t, scope.Success)
		assert.Equal(t, tt.all, scope.Data.AllUsers)
		assert.Equal(t, tt.count, scope.Data.UserCount)
	}

	type orgOptionsResponse struct {
		Success bool                   `json:"success"`
		Data    []model.UsageOrgOption `json:"data"`
	}
	// Managed people's departments and alice's own; not carol's.
	options := serveUsageAnalyticsAs[orgOptionsResponse](t, GetUsageOrgOptions, 1, common.RoleCommonUser, "/api/data/org-options")
	require.True(t, options.Success)
	assert.Equal(t, []model.UsageOrgOption{{}, {BusinessUnit: "BU-A"}}, options.Data)
}

// A department head sees their department until the sync drops is_charge.
func TestUsageAnalyticsIncludesDepartmentOfHead(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)
	require.NoError(t, model.DB.Create(&[]model.User{
		{Id: 1, Username: "alice", Email: "alice@example.com", Password: "password-a", AffCode: "aff-a", Status: common.UserStatusEnabled, HoidcId: "1",
			OrgStr: "BU/Ops", SecondaryOrganization: "BU", TertiaryOrganization: "Ops", IsCharge: 1},
		{Id: 2, Username: "bob", Password: "password-b", AffCode: "aff-b", Status: common.UserStatusEnabled,
			OrgStr: "BU/Ops/Infra", SecondaryOrganization: "BU", TertiaryOrganization: "Ops"},
		{Id: 3, Username: "carol", Password: "password-c", AffCode: "aff-c", Status: common.UserStatusEnabled,
			OrgStr: "BU/Sales", SecondaryOrganization: "BU", TertiaryOrganization: "Sales"},
		{Id: 4, Username: "dave", Password: "password-d", AffCode: "aff-d", Status: common.UserStatusEnabled,
			OrgStr: "BU/Sales", SecondaryOrganization: "BU", TertiaryOrganization: "Sales"},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.QuotaData{
		{UserID: 3, Username: "carol", ModelName: "gpt-c", CreatedAt: 3600, Count: 7, Quota: 700, TokenUsed: 70, LastCalledAt: 3603},
		{UserID: 4, Username: "dave", ModelName: "gpt-d", CreatedAt: 3600, Count: 1, Quota: 50, TokenUsed: 10, LastCalledAt: 3604},
	}).Error)
	_, _, err := model.ReplaceUserDataScope(1, []int{4}, nil, 99)
	require.NoError(t, err)

	payload := serveUsageAnalyticsAs[usageAnalyticsResponse](t, GetUsageAnalytics, 1, common.RoleCommonUser,
		"/api/data/analytics?start_timestamp=1&end_timestamp=7200&group_by=user")
	require.True(t, payload.Success, payload.Message)
	usernames := make([]string, 0, len(payload.Data.Rows))
	for _, row := range payload.Data.Rows {
		usernames = append(usernames, row["username"].(string))
	}
	assert.Equal(t, []string{"bob", "alice", "dave"}, usernames)

	type scopeResponse struct {
		Success bool `json:"success"`
		Data    struct {
			UserCount           int     `json:"user_count"`
			DefaultBusinessUnit *string `json:"default_business_unit"`
			DefaultDepartment   *string `json:"default_department"`
		} `json:"data"`
	}
	scope := serveUsageAnalyticsAs[scopeResponse](t, GetUsageDataScope, 1, common.RoleCommonUser, "/api/data/scope")
	require.True(t, scope.Success)
	assert.Equal(t, 3, scope.Data.UserCount)
	require.NotNil(t, scope.Data.DefaultBusinessUnit)
	assert.Equal(t, "BU", *scope.Data.DefaultBusinessUnit)
	assert.Nil(t, scope.Data.DefaultDepartment)

	// Login keeps is_charge; the sync writing 0 revokes it.
	_, err = service.ProvisionSsoUser("1", "alice@example.com", "Alice", "", "BU/Ops", "", "", 0, 0, 0, 0, 0, nil, false)
	require.NoError(t, err)
	scope = serveUsageAnalyticsAs[scopeResponse](t, GetUsageDataScope, 1, common.RoleCommonUser, "/api/data/scope")
	assert.Equal(t, 3, scope.Data.UserCount)
	notInCharge := 0
	_, err = service.ProvisionSsoUser("1", "alice@example.com", "Alice", "", "BU/Ops", "", "", 0, 0, 0, 0, 0, &notInCharge, false)
	require.NoError(t, err)
	scope = serveUsageAnalyticsAs[scopeResponse](t, GetUsageDataScope, 1, common.RoleCommonUser, "/api/data/scope")
	assert.Equal(t, 2, scope.Data.UserCount, "alice and the configured dave remain")
	require.NotNil(t, scope.Data.DefaultDepartment, "only dave's department is left")
	assert.Equal(t, "Sales", *scope.Data.DefaultDepartment)
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

func TestGetUsageAnalyticsOmitsDimensionsOutsideGrouping(t *testing.T) {
	setupUsageAnalyticsControllerTestDB(t)
	// The user filter resolves accounts, so bob needs one.
	require.NoError(t, model.DB.Create(&model.User{Id: 2, Username: "bob", Password: "password-b", AffCode: "aff-b", Status: common.UserStatusEnabled}).Error)

	payload := serveUsageAnalytics[usageAnalyticsResponse](t, GetUsageAnalytics, 1,
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
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleAdminUser)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/peak-tpm?start_timestamp=1&end_timestamp=7200", nil)
	GetPeakTPM(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"success":true,"message":"","data":[]}`, recorder.Body.String())
}
