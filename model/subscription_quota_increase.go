package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const SubscriptionSourceQuotaIncrease = "quota_increase"

// userQuotaUsedSince sums quota billed to the account, without iSelf attribution or
// business-scene keys. quota_data lags by the dashboard flush interval.
func userQuotaUsedSince(db *gorm.DB, userId int, since int64) (int64, error) {
	businessTokens := db.Unscoped().Model(&Token{}).Select("id").
		Where("user_id = ? AND scene = ?", userId, string(common.TokenSceneBusiness))
	var used int64
	err := db.Model(&QuotaData{}).
		Where("user_id = ? AND created_at >= ? AND token_id NOT IN (?)", userId, since, businessTokens).
		Select("COALESCE(SUM(quota), 0)").
		Scan(&used).Error
	return used, err
}

// GetUserMonthQuotaUsed is the quota billed since the 1st of this month.
func GetUserMonthQuotaUsed(userId int) (int64, error) {
	now := time.Unix(GetDBTimestamp(), 0)
	return userQuotaUsedSince(DB, userId, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix())
}

// quotaTierAbove treats 0 as unlimited, the highest tier.
func quotaTierAbove(amount int64, than int64) bool {
	if than == 0 {
		return false
	}
	return amount == 0 || amount > than
}

// IncreaseUserSubscriptionQuota moves the user to the next larger enabled plan.
// The largest active subscription switches plan in place so in-flight
// settlements still find it; without one, the smallest plan is created.
func IncreaseUserSubscriptionQuota(userId int) (*SubscriptionPlan, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var next SubscriptionPlan
	var upgradedId int
	groupChanged := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
			return err
		}
		var plans []SubscriptionPlan
		if err := tx.Find(&plans).Error; err != nil {
			return err
		}
		var subs []UserSubscription
		if err := activeUserSubscriptions(lockForUpdate(tx), userId, GetDBTimestamp()).
			Order("id asc").
			Find(&subs).Error; err != nil {
			return err
		}
		var current *UserSubscription
		for i := range subs {
			if current == nil || quotaTierAbove(subs[i].AmountTotal, current.AmountTotal) {
				current = &subs[i]
			}
		}

		found := false
		for _, plan := range plans {
			if !plan.Enabled || (current != nil && !quotaTierAbove(plan.TotalAmount, current.AmountTotal)) {
				continue
			}
			if !found || quotaTierAbove(next.TotalAmount, plan.TotalAmount) || (next.TotalAmount == plan.TotalAmount && plan.Id < next.Id) {
				next = plan
				found = true
			}
		}
		if !found {
			if current != nil {
				return common.NewMessage("Already at the highest quota tier")
			}
			return common.NewMessage("No subscription plan is available for a quota increase")
		}
		next.NormalizeDefaults()

		if current == nil {
			sub, err := CreateUserSubscriptionFromPlanTx(tx, userId, &next, SubscriptionSourceQuotaIncrease)
			if err != nil {
				return err
			}
			groupChanged = sub.PrevUserGroup != ""
			// The first increase only raises the limit; this period's spend carries over.
			return carryOverPeriodUsageTx(tx, sub, &next)
		}

		upgradedId = current.Id
		updates := map[string]any{
			"plan_id":               next.Id,
			"amount_total":          next.TotalAmount,
			"downgrade_group":       strings.TrimSpace(next.DowngradeGroup),
			"allow_wallet_overflow": *next.AllowWalletOverflow,
		}
		// A different reset period restarts now; the used amount carries over.
		var previous *SubscriptionPlan
		for i := range plans {
			if plans[i].Id == current.PlanId {
				previous = &plans[i]
			}
		}
		if previous == nil || NormalizeResetPeriod(previous.QuotaResetPeriod) != NormalizeResetPeriod(next.QuotaResetPeriod) ||
			(NormalizeResetPeriod(next.QuotaResetPeriod) == SubscriptionResetCustom && previous.QuotaResetCustomSeconds != next.QuotaResetCustomSeconds) {
			now := GetDBTimestamp()
			nextReset := calcNextResetTime(time.Unix(now, 0), &next, current.EndTime)
			lastReset := int64(0)
			if nextReset > 0 {
				lastReset = now
			}
			updates["next_reset_time"] = nextReset
			updates["last_reset_time"] = lastReset
		}
		// Keep the group from before the first tier change for restoring on expiry.
		if upgradeGroup := strings.TrimSpace(next.UpgradeGroup); upgradeGroup != "" {
			userGroup, err := getUserGroupByIdTx(tx, userId)
			if err != nil {
				return err
			}
			updates["upgrade_group"] = upgradeGroup
			if userGroup != upgradeGroup {
				if strings.TrimSpace(current.PrevUserGroup) == "" {
					updates["prev_user_group"] = userGroup
				}
				if err := tx.Model(&User{}).Where("id = ?", userId).Update("group", upgradeGroup).Error; err != nil {
					return err
				}
				groupChanged = true
			}
		}
		return tx.Model(current).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}
	if upgradedId > 0 {
		_, _ = getSubscriptionPlanInfoCache().DeleteMany([]string{subscriptionPlanInfoCacheKey(upgradedId)})
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(userId, common.LogText("subscription quota increase"))
	}
	// amount (0 = unlimited) is for the console notice.
	RecordLog(userId, LogTypeSystem, common.NewMessage("Subscription quota increased, plan: {{plan}}", map[string]any{"plan": next.Title, "amount": next.TotalAmount}))
	return &next, nil
}
