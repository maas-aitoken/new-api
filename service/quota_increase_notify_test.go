package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setQuotaIncreaseNotifyEnv(t *testing.T, url string, platId string) {
	t.Helper()
	previousUrl, previousPlatId := common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyPlatId
	previousLanguage, previousQuotaPerUnit := common.DefaultLanguage, common.QuotaPerUnit
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousExchangeRate := operation_setting.USDExchangeRate
	previousClient := quotaIncreaseNotifyHTTPClient
	common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyPlatId = url, platId
	common.DefaultLanguage, common.QuotaPerUnit = "zh-CN", 500_000
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.USDExchangeRate = 7.3
	t.Cleanup(func() {
		common.QuotaIncreaseNotifyUrl, common.QuotaIncreaseNotifyPlatId = previousUrl, previousPlatId
		common.DefaultLanguage, common.QuotaPerUnit = previousLanguage, previousQuotaPerUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		operation_setting.USDExchangeRate = previousExchangeRate
		quotaIncreaseNotifyHTTPClient = previousClient
	})
}

func TestNotifyQuotaIncreaseSendsEmailAndChange(t *testing.T) {
	setQuotaIncreaseNotifyEnv(t, "https://cloud.example.com/api/v1/quota/notify", "plat-&-demo")
	named := &model.User{Username: "zhangsan", DisplayName: "张三", Email: "zhangsan@example.com"}
	unnamed := &model.User{Username: "lisi", Email: "lisi@example.com"}
	for _, tc := range []struct {
		name            string
		user            *model.User
		display         string
		previous, total int64
		message         string
	}{
		{"next tier", named, operation_setting.QuotaDisplayTypeUSD, 1_000_000, 1_250_000, "【Maas平台用户提额通知】张三申请提额，额度已由 ＄2 提升至 ＄2.5"},
		{"first tier without display name", unnamed, operation_setting.QuotaDisplayTypeUSD, 0, 1_000_000, "【Maas平台用户提额通知】lisi申请提额，额度已提升至 ＄2"},
		{"unlimited", named, operation_setting.QuotaDisplayTypeUSD, 10_000_000, 0, "【Maas平台用户提额通知】张三申请提额，额度已由 ＄20 提升至 不限"},
		// ¥3000 / ¥6000 / ¥1000 tiers as stored at 7.3 CNY per USD.
		{"tiers typed in CNY", named, operation_setting.QuotaDisplayTypeCNY, 205_479_452, 410_958_904, "【Maas平台用户提额通知】张三申请提额，额度已由 ¥3000 提升至 ¥6000"},
		{"first tier in CNY", unnamed, operation_setting.QuotaDisplayTypeCNY, 0, 68_493_151, "【Maas平台用户提额通知】lisi申请提额，额度已提升至 ¥1000"},
		{"token display keeps zeros", named, operation_setting.QuotaDisplayTypeTokens, 1_000_000, 2_500_000, "【Maas平台用户提额通知】张三申请提额，额度已由 1000000 提升至 2500000"},
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

func TestNotifySubscriptionUsageSendsOncePastTheThreshold(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionUsageAlert{}))
	for _, table := range []string{"user_subscriptions", "subscription_usage_alerts"} {
		require.NoError(t, model.DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() { model.DB.Exec("DELETE FROM subscription_usage_alerts") })
	setQuotaIncreaseNotifyEnv(t, "https://cloud.example.com/notify", "")
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	operation_setting.SetQuotaTierEnabled(true)
	t.Cleanup(func() { operation_setting.SetQuotaTierEnabled(false) })

	now := time.Now().Unix()
	// ¥2760 of a ¥3000 tier at 7.3 CNY per USD; the other user is at half.
	for _, seed := range []struct {
		id          int
		total, used int64
	}{{41, 205_479_452, 189_041_096}, {42, 205_479_452, 102_739_726}} {
		name := fmt.Sprintf("usage-user-%d", seed.id)
		require.NoError(t, model.DB.Create(&model.User{Id: seed.id, Username: name, Email: name + "@example.com", AffCode: name, Status: common.UserStatusEnabled}).Error)
		require.NoError(t, model.DB.Create(&model.UserSubscription{UserId: seed.id, PlanId: 1, AmountTotal: seed.total, AmountUsed: seed.used,
			Status: "active", StartTime: now - 3600, EndTime: now + 86400, Source: model.SubscriptionSourceQuotaTier}).Error)
	}
	var notices []quotaIncreaseNotice
	status := http.StatusInternalServerError
	quotaIncreaseNotifyHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			var notice quotaIncreaseNotice
			require.NoError(t, common.Unmarshal(body, &notice))
			notices = append(notices, notice)
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}
	}

	// A refused notice is sent again on the next run, and then no more.
	_, err := NotifySubscriptionUsage(context.Background())
	require.ErrorContains(t, err, "HTTP 500")
	status = http.StatusOK
	for range 2 {
		_, err = NotifySubscriptionUsage(context.Background())
		require.NoError(t, err)
	}
	want := quotaIncreaseNotice{Email: "usage-user-41@example.com",
		Message: "【Maas平台提额使用提醒】当前使用额度已超过90%，已使用 ¥2760，还剩余 ¥240。超额后只能使用智能路由模型（只用智能路由可忽略本提醒；需要继续使用其他模型，请到Maas平台申请提额）"}
	assert.Equal(t, []quotaIncreaseNotice{want, want}, notices)
}

func TestNotifyQuotaIncreaseFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		url   string
		email string
		sends int
		err   string
	}{
		{"not configured", "", "alice@example.com", 0, ""},
		{"applicant without email", "https://cloud.example.com/notify", "", 0, "the user has no email"},
		{"refused by the platform", "https://cloud.example.com/notify", "alice@example.com", 1, "HTTP 403: plat id mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setQuotaIncreaseNotifyEnv(t, tc.url, "")
			sends := 0
			quotaIncreaseNotifyHTTPClient = func() *http.Client {
				return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					sends++
					assert.NotContains(t, req.Header, "platId")
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
