package model

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
			for name, run := range map[string]func(*testing.T){
				"climbs tiers in place":           testQuotaIncreaseClimbsTiersInPlace,
				"keeps original group for expiry": testQuotaIncreaseKeepsOriginalGroupForExpiry,
			} {
				// Plans of one case would join the tier ladder of the next.
				for _, table := range []string{"users", "logs", "subscription_plans", "user_subscriptions"} {
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

// Enabled carries default:true, so disabling needs an update after create.
func seedQuotaIncreasePlan(t *testing.T, plan SubscriptionPlan, enabled bool) SubscriptionPlan {
	t.Helper()
	plan.DurationUnit = SubscriptionDurationMonth
	plan.DurationValue = 1
	require.NoError(t, DB.Create(&plan).Error)
	if !enabled {
		require.NoError(t, DB.Model(&plan).Update("enabled", false).Error)
	}
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

	_, err := IncreaseUserSubscriptionQuota(user.Id)
	require.Error(t, err)
	assert.Equal(t, "No subscription plan is available for a quota increase", err.Error())

	// Seeded out of order; unlimited (0) is the top tier and disabled plans are skipped.
	v3 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v3", TotalAmount: 0, QuotaResetPeriod: SubscriptionResetNever}, true)
	v2 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 3000, QuotaResetPeriod: SubscriptionResetMonthly}, true)
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "retired", TotalAmount: 2000, QuotaResetPeriod: SubscriptionResetMonthly}, false)
	v1 := seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetMonthly}, true)

	plan, err := IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v1.Id, plan.Id)
	subs := activeQuotaIncreaseSubs(t, user.Id)
	require.Len(t, subs, 1)
	first := subs[0]
	assert.Equal(t, SubscriptionSourceQuotaIncrease, first.Source)
	assert.EqualValues(t, 1000, first.AmountTotal)
	require.NoError(t, DB.Model(&first).Update("amount_used", 800).Error)

	plan, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v2.Id, plan.Id)
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

	plan, err = IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, v3.Id, plan.Id)
	third := activeQuotaIncreaseSubs(t, user.Id)[0]
	assert.Equal(t, first.Id, third.Id)
	assert.Zero(t, third.AmountTotal)
	assert.EqualValues(t, 800, third.AmountUsed)
	assert.Zero(t, third.NextResetTime)

	_, err = IncreaseUserSubscriptionQuota(user.Id)
	require.Error(t, err)
	assert.Equal(t, "Already at the highest quota tier", err.Error())
	assert.Len(t, activeQuotaIncreaseSubs(t, user.Id), 1)

	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ? AND type = ?", user.Id, LogTypeSystem).Count(&logCount).Error)
	assert.EqualValues(t, 3, logCount)
}

func testQuotaIncreaseKeepsOriginalGroupForExpiry(t *testing.T) {
	user := seedQuotaIncreaseUser(t, "quota-group-user")
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v1", TotalAmount: 1000, UpgradeGroup: "pro"}, true)
	seedQuotaIncreasePlan(t, SubscriptionPlan{Title: "v2", TotalAmount: 3000, UpgradeGroup: "max"}, true)

	_, err := IncreaseUserSubscriptionQuota(user.Id)
	require.NoError(t, err)
	_, err = IncreaseUserSubscriptionQuota(user.Id)
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
