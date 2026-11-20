package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRequestSubscriptionQuotaIncreaseWritesAnAuditLogOnly(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open("file:quota_increase_audit?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.AuditLog{}, &model.QuotaData{}, &model.SubscriptionPlan{},
		&model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.SubscriptionUsageAlert{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	operation_setting.SetQuotaTierEnabled(true)
	t.Cleanup(func() {
		operation_setting.SetQuotaTierEnabled(false)
		common.RedisEnabled = previousRedis
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
	})

	user := model.User{Username: "quota_user", Password: "unused-password-hash", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "aff-quota"}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{Title: "plan-a", TotalAmount: 1000, Enabled: true,
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, model.CreateSubscriptionPlan(&plan))
	model.InvalidateSubscriptionPlanCache(plan.Id)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", user.Id)
	ctx.Set("role", common.RoleCommonUser)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/subscription/self/quota-increase", nil)
	RequestSubscriptionQuotaIncrease(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"success":true`)

	var usageLogs int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&usageLogs).Error)
	assert.Zero(t, usageLogs, "a quota increase is not a usage log")

	logs, total, err := model.GetAuditLogs(model.AuditLogFilter{SelfView: true, UserId: user.Id}, 0, 10, common.RoleCommonUser)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	entry := logs[0]
	assert.Equal(t, model.AuditCategoryOperation, entry.Category)
	assert.Equal(t, model.AuditActionQuotaIncrease, entry.Action)
	assert.True(t, entry.Success)
	encoded, err := common.Marshal(entry.Other.Op)
	require.NoError(t, err)
	assert.JSONEq(t, `{"action":"subscription.quota_increase","params":{"plan":"plan-a","amount":1000,"previous_amount":0}}`, string(encoded))
}
