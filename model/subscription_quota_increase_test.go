package model

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TEST_MYSQL_DSN / TEST_POSTGRES_DSN must point to disposable databases.
func TestIncreaseUserSubscriptionQuota(t *testing.T) {
	for _, dialect := range []struct {
		name   string
		dbType common.DatabaseType
		env    string
	}{
		{"sqlite", common.DatabaseTypeSQLite, ""},
		{"mysql", common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"},
		{"postgres", common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			var db *gorm.DB
			if dialect.env == "" {
				// GetDBTimestamp inside the transaction deadlocks a single-connection DB.
				var err error
				db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(4)
				t.Cleanup(func() { _ = sqlDB.Close() })
			} else {
				dsn := strings.TrimSpace(os.Getenv(dialect.env))
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				db = openDialectAnalyticsDB(t, dialect.dbType, dsn)
			}
			useDialectAnalyticsDB(t, db, db, dialect.dbType)
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
			operation_setting.SetQuotaTierEnabled(true)
			t.Cleanup(func() { operation_setting.SetQuotaTierEnabled(false) })
			for name, run := range map[string]func(*testing.T){
				"climbs tiers in place":           testQuotaIncreaseClimbsTiersInPlace,
				"keeps original group for expiry": testQuotaIncreaseKeepsOriginalGroupForExpiry,
				"assigns the lowest tier":         testQuotaTierAssignsLowestTier,
				"serves only auto past the quota": testQuotaTierServesOnlyAutoPastQuota,
				"admin changes the plan in place": testAdminChangesSubscriptionPlanInPlace,
			} {
				operation_setting.SetQuotaTierEnabled(true)
				// Plans of one case would join the tier ladder of the next.
				for _, table := range []string{"users", "tokens", "logs", "quota_data", "subscription_plans", "user_subscriptions", "subscription_pre_consume_records"} {
					require.NoError(t, db.Exec("DELETE FROM "+table).Error)
				}
				t.Run(name, run)
			}
		})
	}
}

func seedQuotaIncreaseUser(t *testing.T, username string) User {
	t.Helper()
	user := User{Username: username, Password: "unused-password-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

// Ids repeat across cases, so the plan cache is dropped.
func seedQuotaIncreasePlan(t *testing.T, plan SubscriptionPlan, enabled bool) SubscriptionPlan {
	t.Helper()
	plan.DurationUnit = SubscriptionDurationMonth
	plan.DurationValue = 1
	plan.Enabled = enabled
	require.NoError(t, CreateSubscriptionPlan(&plan))
	InvalidateSubscriptionPlanCache(plan.Id)
	var stored SubscriptionPlan
	require.NoError(t, DB.First(&stored, plan.Id).Error)
	require.Equal(t, enabled, stored.Enabled)
	return plan
}

func activeQuotaIncreaseSubs(t *testing.T, userId int) []UserSubscription {
	t.Helper()
	var subs []UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND status = ?", userId, "active").Order("id asc").Find(&subs).Error)
	return subs
}

func testQuotaIncreaseClimbsTiersInPlace(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "quota-tier-user")

	_, _, err := IncreaseUserSubscriptionQuota(user.Id)
	require.Error(t, err)
	assert.Equal(t, "No subscription plan is available for a quota increase", err.Error())

	// Seeded out of order; unlimited (0) is the top tier and disabled plans are skipped.
	v3 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v3", TotalAmount: 0, QuotaResetPeriod: SubscriptionResetNever}, true)
	v2 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 3000, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "retired", TotalAmount: 2000, QuotaResetPeriod: SubscriptionResetMonthly}, false)
	v1 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetMonthly}, true)

	// Only this month's spend of this account counts.
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	for _, row := range []QuotaData{
		{UserID: user.Id, Username: user.Username, ModelName: "m", CreatedAt: monthStart - 3600, Count: 1, Quota: 5000},
		{UserID: user.Id, Username: user.Username, ModelName: "m", CreatedAt: monthStart, Count: 1, Quota: 300},
		{UserID: user.Id, Username: user.Username, ModelName: "n", CreatedAt: monthStart, Count: 1, Quota: 40},
		{UserID: user.Id + 1, Username: "someone-else", ModelName: "m", CreatedAt: monthStart, Count: 1, Quota: 777},
	} {
		require.NoError(t, DB.Create(&row).Error)
	}
	monthUsed, err := GetUserMonthQuotaUsed(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 340, monthUsed)

	plan, previousTotal, err := IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v1.Id, plan.Id)
	assert.Zero(t, previousTotal)
	subs := activeQuotaIncreaseSubs(t, user.Id)
	require.Len(t, subs, 1)
	first := subs[0]
	assert.Equal(t, SubscriptionSourceQuotaIncrease, first.Source)
	assert.EqualValues(t, 1000, first.AmountTotal)
	assert.EqualValues(t, 340, first.AmountUsed)
	require.NoError(t, DB.Model(&first).Update("amount_used", 800).Error)

	plan, previousTotal, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v2.Id, plan.Id)
	assert.EqualValues(t, 1000, previousTotal)
	subs = activeQuotaIncreaseSubs(t, user.Id)
	require.Len(t, subs, 1)
	second := subs[0]
	assert.Equal(t, first.Id, second.Id)
	assert.Equal(t, v2.Id, second.PlanId)
	assert.EqualValues(t, 3000, second.AmountTotal)
	assert.EqualValues(t, 800, second.AmountUsed)
	assert.Equal(t, first.EndTime, second.EndTime)
	// Same monthly period: the current period keeps running.
	assert.Equal(t, first.NextResetTime, second.NextResetTime)
	assert.Equal(t, first.LastResetTime, second.LastResetTime)

	plan, previousTotal, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v3.Id, plan.Id)
	assert.EqualValues(t, 3000, previousTotal)
	third := activeQuotaIncreaseSubs(t, user.Id)[0]
	assert.Equal(t, first.Id, third.Id)
	assert.Zero(t, third.AmountTotal)
	assert.EqualValues(t, 800, third.AmountUsed)
	assert.Zero(t, third.NextResetTime)

	_, _, err = IncreaseUserSubscriptionQuota(user.Id)
	require.Error(t, err)
	assert.Equal(t, "Already at the highest quota tier", err.Error())
	assert.Len(t, activeQuotaIncreaseSubs(t, user.Id), 1)

	expected := []struct {
		plan   string
		amount float64
	}{{"v1", 1000}, {"v2", 3000}, {"v3", 0}}
	var logs []Log
	require.NoError(t, LOG_DB.Where("user_id = ? AND type = ?", user.Id, LogTypeSystem).Order("id asc").Find(&logs).Error)
	require.Len(t, logs, len(expected))
	for i, log := range logs {
		var other struct {
			ContentParts []common.Message `json:"content_parts"`
		}
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
		require.Len(t, other.ContentParts, 1)
		assert.Equal(t, "Subscription quota increased, plan: {{plan}}", other.ContentParts[0].Key)
		assert.Equal(t, map[string]any{"plan": expected[i].plan, "amount": expected[i].amount}, other.ContentParts[0].Params)
		assert.Equal(t, "Subscription quota increased, plan: "+expected[i].plan, log.Content)
	}
}

func testQuotaIncreaseKeepsOriginalGroupForExpiry(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "quota-group-user")
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, UpgradeGroup: "pro"}, true)
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 3000, UpgradeGroup: "max"}, true)

	_, _, err := IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	_, _, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)

	var updated User
	require.NoError(t, DB.First(&updated, user.Id).Error)
	assert.Equal(t, "max", updated.Group)
	sub := activeQuotaIncreaseSubs(t, user.Id)[0]
	assert.Equal(t, "max", sub.UpgradeGroup)
	assert.Equal(t, "default", sub.PrevUserGroup)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, GetDBTimestamp())
		assert.Equal(t, "default", target)
		return err
	}))
	require.NoError(t, DB.First(&updated, user.Id).Error)
	assert.Equal(t, "default", updated.Group)
}

func testQuotaTierAssignsLowestTier(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "quota-tier-new-user")

	has, err := EnsureUserQuotaTier(user.Id)
	require.NoError(t, err)
	assert.False(t, has)

	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "draft", TotalAmount: 500, QuotaResetPeriod: SubscriptionResetMonthly}, false)
	v2 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 3000, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	v1 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	// Business-scene keys, deleted ones too, do not count.
	business := Token{UserId: user.Id, Key: "quota-tier-business-key", Name: "business", Status: common.TokenStatusEnabled, Scene: common.TokenSceneBusiness}
	require.NoError(t, DB.Create(&business).Error)
	require.NoError(t, DB.Delete(&business).Error)
	for _, row := range []QuotaData{
		{UserID: user.Id, Username: user.Username, ModelName: "m", CreatedAt: monthStart, Count: 1, Quota: 1500},
		{UserID: user.Id, Username: user.Username, ModelName: "m", TokenID: business.Id, CreatedAt: monthStart, Count: 1, Quota: 9000},
	} {
		require.NoError(t, DB.Create(&row).Error)
	}

	// Lowest plan, used = this month's spend even past the quota.
	for range 2 {
		has, err = EnsureUserQuotaTier(user.Id)
		require.NoError(t, err)
		assert.True(t, has)
	}
	subs := activeQuotaIncreaseSubs(t, user.Id)
	require.Len(t, subs, 1)
	assert.Equal(t, v1.Id, subs[0].PlanId)
	assert.Equal(t, SubscriptionSourceQuotaTier, subs[0].Source)
	assert.EqualValues(t, 1500, subs[0].AmountUsed)

	plan, previousTotal, err := IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v2.Id, plan.Id)
	assert.EqualValues(t, 1000, previousTotal)

	// Off: tier subscriptions neither bill nor show.
	operation_setting.SetQuotaTierEnabled(false)
	has, err = HasActiveUserSubscription(user.Id)
	require.NoError(t, err)
	assert.False(t, has)
	has, err = EnsureUserQuotaTier(user.Id)
	require.NoError(t, err)
	assert.False(t, has)
	_, err = PreConsumeUserSubscription("tier-off", user.Id, "m", 0, 10, false)
	require.EqualError(t, err, "no active subscription")
	operation_setting.SetQuotaTierEnabled(true)
	active, err := GetAllActiveUserSubscriptions(user.Id)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.EqualValues(t, 3000, active[0].Subscription.AmountTotal)
}

func testQuotaTierServesOnlyAutoPastQuota(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "quota-tier-heavy-user")
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 100, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 300, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	_, err := EnsureUserQuotaTier(user.Id)
	require.NoError(t, err)
	sub := activeQuotaIncreaseSubs(t, user.Id)[0]
	require.NoError(t, DB.Model(&sub).Update("amount_used", 95).Error)
	used := func() int64 {
		var row UserSubscription
		require.NoError(t, DB.First(&row, sub.Id).Error)
		return row.AmountUsed
	}

	res, err := PreConsumeUserSubscription("cross", user.Id, "m", 0, 50, false)
	require.NoError(t, err)
	assert.Equal(t, sub.Id, res.UserSubscriptionId)
	require.NoError(t, PostConsumeUserSubscriptionDelta(sub.Id, 30))
	assert.EqualValues(t, 175, used())

	// Used up: only "auto" requests run, and refunds stay exact.
	exhausted, err := UserQuotaTierExhausted(user.Id)
	require.NoError(t, err)
	assert.True(t, exhausted)
	_, err = PreConsumeUserSubscription("fixed-model", user.Id, "m", 0, 10, false)
	require.ErrorIs(t, err, ErrSubscriptionTierExhausted)
	res, err = PreConsumeUserSubscription("auto", user.Id, "m", 0, 20, true)
	require.NoError(t, err)
	assert.Equal(t, sub.Id, res.UserSubscriptionId)
	assert.EqualValues(t, 195, used())
	require.NoError(t, RefundSubscriptionPreConsume("auto"))
	assert.EqualValues(t, 175, used())

	// 175 of 100 becomes 175 of 300.
	_, _, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 175, used())
	exhausted, err = UserQuotaTierExhausted(user.Id)
	require.NoError(t, err)
	assert.False(t, exhausted)
	_, err = PreConsumeUserSubscription("after-increase", user.Id, "m", 0, 50, false)
	require.NoError(t, err)
	assert.EqualValues(t, 225, used())

	// Off: the hard limit applies again.
	operation_setting.SetQuotaTierEnabled(false)
	exhausted, err = UserQuotaTierExhausted(user.Id)
	require.NoError(t, err)
	assert.False(t, exhausted)
	legacy := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "bought", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetMonthly}, false)
	_, err = AdminBindSubscription(user.Id, legacy.Id, "")
	require.NoError(t, err)
	bought := activeQuotaIncreaseSubs(t, user.Id)[1]
	require.Equal(t, legacy.Id, bought.PlanId)
	require.NoError(t, DB.Model(&bought).Update("amount_used", 995).Error)
	_, err = PreConsumeUserSubscription("legacy", user.Id, "m", 0, 50, true)
	require.EqualError(t, err, "subscription quota insufficient, need=50")
	require.Error(t, PostConsumeUserSubscriptionDelta(bought.Id, 10))
}

func testAdminChangesSubscriptionPlanInPlace(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "admin-change-user")
	other := User{Username: "admin-change-expired", Password: "unused-password-hash", AffCode: "aff-admin-change-expired",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(&other).Error)
	v1 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	v3 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v3", TotalAmount: 6000, QuotaResetPeriod: SubscriptionResetMonthly}, true)

	_, err := AdminChangeUserSubscriptionPlan(user.Id, v3.Id)
	require.Error(t, err)
	assert.Equal(t, "This user has no active subscription", err.Error())

	_, err = AdminBindSubscription(user.Id, v1.Id, "")
	require.NoError(t, err)
	first := activeQuotaIncreaseSubs(t, user.Id)[0]
	require.NoError(t, DB.Model(&first).Update("amount_used", 800).Error)
	_, err = AdminBindSubscription(other.Id, v3.Id, "")
	require.NoError(t, err)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", other.Id).Update("end_time", GetDBTimestamp()-1).Error)

	current, err := GetUsersCurrentSubscriptions([]int{user.Id, other.Id})
	require.NoError(t, err)
	assert.Equal(t, []UserCurrentSubscription{{UserId: user.Id, SubscriptionId: first.Id, PlanId: v1.Id, PlanTitle: "v1",
		AmountTotal: 1000, AmountUsed: 800, EndTime: first.EndTime}}, current)

	// Up and back down: same row, used amount and end time kept.
	for _, plan := range []SubscriptionPlan{v3, v1} {
		_, err = AdminChangeUserSubscriptionPlan(user.Id, plan.Id)
		require.NoError(t, err)
		subs := activeQuotaIncreaseSubs(t, user.Id)
		require.Len(t, subs, 1)
		assert.Equal(t, first.Id, subs[0].Id)
		assert.Equal(t, plan.Id, subs[0].PlanId)
		assert.Equal(t, plan.TotalAmount, subs[0].AmountTotal)
		assert.EqualValues(t, 800, subs[0].AmountUsed)
		assert.Equal(t, first.EndTime, subs[0].EndTime)
	}
}
