package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setQuotaIncreaseNotifyEnv(t *testing.T, url string, headers string) {
	t.Helper()
	previousUrl, previousHeaders := common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyHeaders
	previousLanguage, previousQuotaPerUnit := common.DefaultLanguage, common.QuotaPerUnit
	previousSystemName := common.SystemName
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousExchangeRate := operation_setting.USDExchangeRate
	previousClient := quotaIncreaseNotifyHTTPClient
	common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyHeaders = url, headers
	common.DefaultLanguage, common.QuotaPerUnit = "zh-CN", 500_000
	common.SystemName = "MaaS"
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.USDExchangeRate = 7.3
	t.Cleanup(func() {
		common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyHeaders = previousUrl, previousHeaders
		common.DefaultLanguage, common.QuotaPerUnit = previousLanguage, previousQuotaPerUnit
		common.SystemName = previousSystemName
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		operation_setting.USDExchangeRate = previousExchangeRate
		quotaIncreaseNotifyHTTPClient = previousClient
	})
}

func TestNotifyQuotaIncreaseSendsEmailAndChange(t *testing.T) {
	setQuotaIncreaseNotifyEnv(t, "https://cloud.example.com/api/v1/quota/notify", `{"platId":"plat-&-demo"}`)
	named := &model.User{Username: "zhangsan", DisplayName: "张三", Email: "zhangsan@example.com"}
	unnamed := &model.User{Username: "lisi", Email: "lisi@example.com"}
	for _, tc := range []struct {
		name            string
		user            *model.User
		display         string
		previous, total int64
		message         string
	}{
		{"next tier", named, operation_setting.QuotaDisplayTypeUSD, 1_000_000, 1_250_000, "【MaaS】张三（zhangsan@example.com）申请提额，额度已由 ＄2 提升至 ＄2.5"},
		{"first tier without display name", unnamed, operation_setting.QuotaDisplayTypeUSD, 0, 1_000_000, "【MaaS】lisi（lisi@example.com）申请提额，额度已提升至 ＄2"},
		{"unlimited", named, operation_setting.QuotaDisplayTypeUSD, 10_000_000, 0, "【MaaS】张三（zhangsan@example.com）申请提额，额度已由 ＄20 提升至 不限"},
		// ¥3000 / ¥6000 / ¥1000 tiers as stored at 7.3 CNY per USD.
		{"tiers typed in CNY", named, operation_setting.QuotaDisplayTypeCNY, 205_479_452, 410_958_904, "【MaaS】张三（zhangsan@example.com）申请提额，额度已由 ¥3000 提升至 ¥6000"},
		{"first tier in CNY", unnamed, operation_setting.QuotaDisplayTypeCNY, 0, 68_493_151, "【MaaS】lisi（lisi@example.com）申请提额，额度已提升至 ¥1000"},
		{"token display keeps zeros", named, operation_setting.QuotaDisplayTypeTokens, 1_000_000, 2_500_000, "【MaaS】张三（zhangsan@example.com）申请提额，额度已由 1000000 提升至 2500000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = tc.display
			var sent *http.Request
			var body []byte
			quotaIncreaseNotifyHTTPClient = func() *http.Client {
				return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					sent = req
					var err error
					body, err = io.ReadAll(req.Body)
					require.NoError(t, err)
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
				})}
			}

			require.NoError(t, NotifyQuotaIncrease(tc.user, tc.previous, tc.total))
			require.NotNil(t, sent)
			assert.Equal(t, http.MethodPost, sent.Method)
			assert.Equal(t, "https://cloud.example.com/api/v1/quota/notify", sent.URL.String())
			assert.Equal(t, []string{"plat-&-demo"}, sent.Header["platId"])
			assert.Equal(t, "application/json", sent.Header.Get("Content-Type"))
			var notice quotaIncreaseNotice
			require.NoError(t, common.Unmarshal(body, &notice))
			assert.Equal(t, quotaIncreaseNotice{Email: tc.user.Email, Message: tc.message}, notice)
		})
	}
}

func TestNotifyQuotaIncreaseFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		url     string
		headers string
		email   string
		sends   int
		err     string
	}{
		{"not configured", "", "", "alice@example.com", 0, ""},
		{"applicant without email", "https://cloud.example.com/notify", "", "", 0, "the user has no email"},
		{"headers not a JSON object", "https://cloud.example.com/notify", "platId: plat-demo", "alice@example.com", 0, "QUOTA_INCREASE_NOTIFY_HEADERS"},
		{"refused by the platform", "https://cloud.example.com/notify", "", "alice@example.com", 1, "HTTP 403: plat id mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setQuotaIncreaseNotifyEnv(t, tc.url, tc.headers)
			sends := 0
			quotaIncreaseNotifyHTTPClient = func() *http.Client {
				return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					sends++
					return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("plat id mismatch")), Header: make(http.Header)}, nil
				})}
			}

			err := NotifyQuotaIncrease(&model.User{Username: "alice", Email: tc.email}, 1_000_000, 2_500_000)
			if tc.err == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.err)
			}
			assert.Equal(t, tc.sends, sends)
		})
	}
}
