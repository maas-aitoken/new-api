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

// NotifyQuotaIncrease posts the user's email and the notice text to QUOTA_INCREASE_NOTIFY_URL
// (empty sends nothing). previousTotal 0 = no tier before, newTotal 0 = unlimited.
func NotifyQuotaIncrease(user *model.User, previousTotal int64, newTotal int64) error {
	if common.QuotaIncreaseNotifyUrl == "" {
		return nil
	}
	if user.Email == "" {
		return errors.New("the user has no email")
	}
	var headers map[string]string
	if common.QuotaIncreaseNotifyHeaders != "" {
		if err := common.UnmarshalJsonStr(common.QuotaIncreaseNotifyHeaders, &headers); err != nil {
			return fmt.Errorf("QUOTA_INCREASE_NOTIFY_HEADERS is not a JSON object of strings: %w", err)
		}
	}

	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	params := map[string]any{"SystemName": common.SystemName, "Name": name, "Email": user.Email,
		"From": quotaTierText(previousTotal), "To": quotaTierText(newTotal)}
	key := i18n.MsgNotifyQuotaIncreasedFirst
	if previousTotal > 0 {
		key = i18n.MsgNotifyQuotaIncreased
	}
	body, err := common.Marshal(quotaIncreaseNotice{Email: user.Email, Message: i18n.Translate("", key, params)})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.QuotaIncreaseNotifyUrl, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create quota increase notice request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		// Keeps the configured case (platId) on HTTP/1.1 instead of Platid.
		req.Header[name] = []string{value}
	}
	client := quotaIncreaseNotifyHTTPClient()
	if client == nil {
		return errors.New("HTTP client is not initialized")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send quota increase notice: %w", err)
	}
	defer CloseResponseBodyGracefully(resp)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("quota increase notice returned HTTP %d: %s", resp.StatusCode, snippet)
	}
	return nil
}

// quotaTierText is a tier amount in the display currency rounded to cents, so a
// tier typed as ¥3000 reads ¥3000, not ¥2999.999999; 0 is unlimited.
func quotaTierText(total int64) string {
	if total == 0 {
		return i18n.Translate("", i18n.MsgNotifyQuotaUnlimited)
	}
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
