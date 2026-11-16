package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
