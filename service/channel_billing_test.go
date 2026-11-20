package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaleChannelBillingQuotaUsesSelectedMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyChannelCostMultiplier, 0.8)

	assert.Equal(t, 80, ScaleChannelBillingQuota(c, &relaycommon.RelayInfo{}, 100))

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Equal(t, float64(1), ChannelBillingMultiplier(c2, &relaycommon.RelayInfo{}))
	assert.Equal(t, 100, ScaleChannelBillingQuota(c2, &relaycommon.RelayInfo{}, 100))
}

func TestPrepareChannelBillingKeepsFreeModelAndTrustedBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyChannelCostMultiplier, 2.0)

	free := &relaycommon.RelayInfo{}
	free.PriceData.FreeModel = true
	require.Nil(t, PrepareChannelBilling(c, free, 0))
	assert.Nil(t, free.Billing, "a free model must not open a billing session")

	trusted := &relaycommon.RelayInfo{}
	session := &BillingSession{relayInfo: trusted, funding: &WalletFunding{userId: 1}, trusted: true}
	trusted.Billing = session
	require.Nil(t, PrepareChannelBilling(c, trusted, 500))
	assert.Equal(t, 0, session.GetPreConsumedQuota(), "a trusted session reserves nothing before settlement")
	assert.True(t, session.trusted)
	assert.Equal(t, 0, session.tokenConsumed)
}

// 上游拒答被改判的尝试已经按用量结算；重试的那次要新开会话再扣一次，否则会被已结算的会话吞掉。
// 订阅预扣按请求 ID 去重，新会话必须换键。
func TestRefusedAttemptKeepsItsChargeAndRetryIsChargedAgain(t *testing.T) {
	for _, preference := range []string{"wallet_only", "subscription_only"} {
		t.Run(preference, func(t *testing.T) {
			truncate(t)
			t.Cleanup(func() {
				model.DB.Exec("DELETE FROM subscription_pre_consume_records")
				model.DB.Exec("DELETE FROM subscription_plans")
			})
			const userID, tokenID, subID = 41, 41, 41
			seedUser(t, userID, 10000)
			seedToken(t, tokenID, userID, "sk-refusal-retry", 5000)
			require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 41, Title: "plan", QuotaResetPeriod: model.SubscriptionResetNever}).Error)
			require.NoError(t, model.DB.Create(&model.UserSubscription{Id: subID, UserId: userID, PlanId: 41, AmountTotal: 100000, Status: "active",
				StartTime: time.Now().Unix(), EndTime: time.Now().Add(time.Hour).Unix()}).Error)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RequestId: "refusal-retry", UserId: userID, TokenId: tokenID, TokenKey: "sk-refusal-retry",
				UserSetting: dto.UserSetting{BillingPreference: preference}}

			require.Nil(t, PreConsumeBilling(c, 300, info))
			require.NoError(t, SettleBilling(c, info, 700))
			CloseSettledAttemptBilling(info)
			require.Nil(t, PrepareChannelBilling(c, info, 300))
			require.NotNil(t, info.Billing, "the retry must open its own billing session")
			require.NoError(t, SettleBilling(c, info, 500))

			assert.Equal(t, 5000-1200, getTokenRemainQuota(t, tokenID))
			if preference == "wallet_only" {
				assert.Equal(t, 10000-1200, getUserQuota(t, userID))
				return
			}
			assert.EqualValues(t, 1200, getSubscriptionUsed(t, subID))
			var records int64
			require.NoError(t, model.DB.Model(&model.SubscriptionPreConsumeRecord{}).Where("request_id IN ?", []string{"refusal-retry", "refusal-retry#1"}).Count(&records).Error)
			assert.EqualValues(t, 2, records, "each billing session keeps its own pre-consume record")
		})
	}
}
