package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// The URL is set by operators (often an internal host), not users, hence GetHttpClient.
var quotaIncreaseNotifyHTTPClient = GetHttpClient

type quotaIncreaseNotice struct {
	Email   string `json:"email"`
	Message string `json:"message"`
}

const SubscriptionUsageAlertPercent = 90

// NotifyQuotaIncrease posts the user's email and the notice text to QUOTA_INCREASE_NOTIFY_URL
// (empty sends nothing). previousTotal 0 = no tier before, newTotal 0 = unlimited.
func NotifyQuotaIncrease(user *model.User, previousTotal int64, newTotal int64) error {
	if common.QuotaIncreaseNotifyUrl == "" {
		return nil
	}
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	params := map[string]any{"Name": name, "From": quotaTierText(previousTotal), "To": quotaTierText(newTotal)}
	key := i18n.MsgNotifyQuotaIncreasedFirst
	if previousTotal > 0 {
		key = i18n.MsgNotifyQuotaIncreased
	}
	return postQuotaNotice(context.Background(), user.Email, i18n.Translate("", key, params))
}

// NotifySubscriptionUsage sends each due usage notice once; a failed one is retried next run.
func NotifySubscriptionUsage(ctx context.Context) (map[string]any, error) {
	subs, err := model.DueSubscriptionUsageAlerts(SubscriptionUsageAlertPercent)
	if err != nil {
		return nil, err
	}
	sent, failed := 0, 0
	var firstErr error
	for i := range subs {
		sub := &subs[i]
		user, err := model.GetUserById(sub.UserId, false)
		if err != nil || user.Email == "" {
			continue
		}
		message := i18n.Translate("", i18n.MsgNotifyQuotaUsageAlert, map[string]any{
			"Percent": SubscriptionUsageAlertPercent, "Used": quotaAmountText(sub.AmountUsed),
			"Remaining": quotaAmountText(max(sub.AmountTotal-sub.AmountUsed, 0)),
		})
		if err := postQuotaNotice(ctx, user.Email, message); err != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("user %d: %w", sub.UserId, err)
			}
			continue
		}
		if err := model.RecordSubscriptionUsageAlert(sub); err != nil {
			return map[string]any{"sent": sent, "failed": failed}, err
		}
		sent++
	}
	result := map[string]any{"due": len(subs), "sent": sent, "failed": failed}
	if firstErr != nil {
		return result, fmt.Errorf("%d usage notices failed, first: %w", failed, firstErr)
	}
	return result, nil
}

func postQuotaNotice(ctx context.Context, email string, message string) error {
	if email == "" {
		return errors.New("the user has no email")
	}
	body, err := common.Marshal(quotaIncreaseNotice{Email: email, Message: message})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.QuotaIncreaseNotifyUrl, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create quota notice request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if common.QuotaIncreaseNotifyPlatId != "" {
		// Assigned directly so HTTP/1.1 sends platId, not Platid.
		req.Header["platId"] = []string{common.QuotaIncreaseNotifyPlatId}
	}
	client := quotaIncreaseNotifyHTTPClient()
	if client == nil {
		return errors.New("HTTP client is not initialized")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send quota notice: %w", err)
	}
	defer CloseResponseBodyGracefully(resp)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("quota notice returned HTTP %d: %s", resp.StatusCode, snippet)
	}
	return nil
}

// quotaTierText is a tier amount; 0 is unlimited.
func quotaTierText(total int64) string {
	if total == 0 {
		return i18n.Translate("", i18n.MsgNotifyQuotaUnlimited)
	}
	return quotaAmountText(total)
}

// quotaAmountText is in the display currency, rounded to cents (¥3000, not ¥2999.999999).
func quotaAmountText(total int64) string {
	displayType := operation_setting.GetQuotaDisplayType()
	if displayType == operation_setting.QuotaDisplayTypeTokens {
		return strconv.FormatInt(total, 10)
	}
	amount := float64(total) / common.QuotaPerUnit * operation_setting.GetUsdToCurrencyRate(operation_setting.USDExchangeRate)
	symbol := "＄" // USD as logger.FormatQuota writes it
	if displayType == operation_setting.QuotaDisplayTypeCNY || displayType == operation_setting.QuotaDisplayTypeCustom {
		symbol = operation_setting.GetCurrencySymbol()
	}
	return symbol + strconv.FormatFloat(math.Round(amount*100)/100, 'f', -1, 64)
}
