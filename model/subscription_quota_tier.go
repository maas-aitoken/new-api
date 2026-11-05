package model

import (
	"errors"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// SubscriptionSourceQuotaTier marks the lowest tier assigned automatically.
const SubscriptionSourceQuotaTier = "quota_tier"

// quotaTierSources: subscriptions that do not bill while quota tiers are off.
var quotaTierSources = []string{SubscriptionSourceQuotaTier, SubscriptionSourceQuotaIncrease}

// ErrSubscriptionTierExhausted: the tier is used up and the request is not "auto".
var ErrSubscriptionTierExhausted = errors.New("subscription quota tier exhausted")

func IsQuotaTierSource(source string) bool {
	return slices.Contains(quotaTierSources, source)
}

// billableSubscriptions scopes active subscriptions; tier ones only while tiers are on.
func billableSubscriptions(tx *gorm.DB, now int64) *gorm.DB {
	tx = tx.Where("status = ? AND end_time > ?", "active", now)
	if !operation_setting.IsQuotaTierEnabled() {
		tx = tx.Where("(source IS NULL OR source NOT IN ?)", quotaTierSources)
	}
	return tx
}

func activeUserSubscriptions(tx *gorm.DB, userId int, now int64) *gorm.DB {
	return billableSubscriptions(tx.Where("user_id = ?", userId), now)
}

// EnsureUserQuotaTier gives a user without a subscription the lowest enabled plan, with this
// period's spend as used. Reports whether the user has one afterwards.
func EnsureUserQuotaTier(userId int) (bool, error) {
	if !operation_setting.IsQuotaTierEnabled() || userId <= 0 {
		return false, nil
	}
	if has, err := HasActiveUserSubscription(userId); err != nil || has {
		return has, err
	}
	created := false
	groupChanged := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
			return err
		}
		var count int64
		if err := activeUserSubscriptions(tx.Model(&UserSubscription{}), userId, GetDBTimestamp()).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			created = true
			return nil
		}
		var plans []SubscriptionPlan
		if err := tx.Where("enabled = ?", true).Find(&plans).Error; err != nil {
			return err
		}
		var lowest *SubscriptionPlan
		for i := range plans {
			if lowest == nil || quotaTierAbove(lowest.TotalAmount, plans[i].TotalAmount) ||
				(lowest.TotalAmount == plans[i].TotalAmount && plans[i].Id < lowest.Id) {
				lowest = &plans[i]
			}
		}
		if lowest == nil {
			return nil
		}
		lowest.NormalizeDefaults()
		sub, err := CreateUserSubscriptionFromPlanTx(tx, userId, lowest, SubscriptionSourceQuotaTier)
		if err != nil {
			return err
		}
		created = true
		groupChanged = sub.PrevUserGroup != ""
		return carryOverPeriodUsageTx(tx, sub, lowest)
	})
	if err != nil {
		return false, err
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(userId, common.LogText("subscription quota tier"))
	}
	return created, nil
}

// UserQuotaTierExhausted reports whether every active tier is used up. Quota due for a reset
// counts as available: the reset happens when the request is billed.
func UserQuotaTierExhausted(userId int) (bool, error) {
	if !operation_setting.IsQuotaTierEnabled() || userId <= 0 {
		return false, nil
	}
	if _, err := EnsureUserQuotaTier(userId); err != nil {
		return false, err
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := activeUserSubscriptions(DB, userId, now).Find(&subs).Error; err != nil {
		return false, err
	}
	for _, sub := range subs {
		if sub.AmountTotal <= 0 || sub.AmountUsed < sub.AmountTotal || (sub.NextResetTime > 0 && sub.NextResetTime <= now) {
			return false, nil
		}
	}
	return len(subs) > 0, nil
}

// carryOverPeriodUsageTx counts this period's spend as used on a new subscription; it may pass the quota.
func carryOverPeriodUsageTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan) error {
	start := time.Unix(sub.StartTime, 0)
	periodStart := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
	switch NormalizeResetPeriod(plan.QuotaResetPeriod) {
	case SubscriptionResetDaily:
		periodStart = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	case SubscriptionResetWeekly:
		periodStart = time.Date(start.Year(), start.Month(), start.Day()-(int(start.Weekday())+6)%7, 0, 0, 0, 0, start.Location())
	}
	used, err := userQuotaUsedSince(tx, sub.UserId, periodStart.Unix())
	if err != nil || used <= 0 {
		return err
	}
	sub.AmountUsed = used
	return tx.Model(sub).Update("amount_used", used).Error
}
