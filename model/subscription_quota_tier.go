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

// EnsureUserQuotaTier assigns the lowest enabled plan to an eligible user.
func EnsureUserQuotaTier(userId int) (bool, error) {
	if !operation_setting.IsQuotaTierEnabled() || userId <= 0 {
		return false, nil
	}
	var eligible bool
	if err := DB.Model(&User{}).
		Where("id = ?", userId).
		Select("COALESCE(quota_tier_auto_assign, ?)", false).
		Scan(&eligible).Error; err != nil {
		return false, err
	}
	if !eligible {
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

// SubscriptionUsageAlert marks a sent usage notice; its own table so billing saves cannot overwrite it.
type SubscriptionUsageAlert struct {
	Id                 int   `json:"id"`
	UserSubscriptionId int   `json:"user_subscription_id" gorm:"uniqueIndex"`
	AmountTotal        int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	CreatedAt          int64 `json:"created_at" gorm:"bigint"`
}

// DueSubscriptionUsageAlerts lists subscriptions past percent of their quota not yet notified at that
// quota, and drops records of subscriptions back under it so the next crossing notifies again.
func DueSubscriptionUsageAlerts(percent int64) ([]UserSubscription, error) {
	now := GetDBTimestamp()
	var reached []UserSubscription
	// Usage due for a reset is last period's.
	if err := billableSubscriptions(DB, now).
		Where("amount_total > 0 AND amount_used * 100 > amount_total * ?", percent).
		Where("next_reset_time = 0 OR next_reset_time > ?", now).
		Order("id asc").
		Find(&reached).Error; err != nil {
		return nil, err
	}
	var alerts []SubscriptionUsageAlert
	if err := DB.Find(&alerts).Error; err != nil {
		return nil, err
	}
	alerted := make(map[int]int64, len(alerts))
	for _, alert := range alerts {
		alerted[alert.UserSubscriptionId] = alert.AmountTotal
	}
	due := make([]UserSubscription, 0)
	for _, sub := range reached {
		total, ok := alerted[sub.Id]
		if ok && total == sub.AmountTotal {
			delete(alerted, sub.Id)
			continue
		}
		due = append(due, sub)
	}
	if len(alerted) == 0 {
		return due, nil
	}
	stale := make([]int, 0, len(alerted))
	for id := range alerted {
		stale = append(stale, id)
	}
	return due, DB.Where("user_subscription_id IN ?", stale).Delete(&SubscriptionUsageAlert{}).Error
}

func RecordSubscriptionUsageAlert(sub *UserSubscription) error {
	return DB.Create(&SubscriptionUsageAlert{UserSubscriptionId: sub.Id, AmountTotal: sub.AmountTotal, CreatedAt: common.GetTimestamp()}).Error
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
