package controller

import (
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// prepareAutoRetryBilling reprices a new model while keeping the original
// billing session, so a retry reserves any additional quota it needs.
var prepareAutoRetryBilling = func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.TieredBillingSnapshot = nil
	info.BillingRequestInput = nil
	info.BillingModelName = ""
	info.PriceData.FreeModel = false
	if billingErr := relay.PrepareRequestBilling(c, info); billingErr != nil {
		return billingErr
	}
	if info.PriceData.FreeModel && info.Billing != nil {
		info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	}
	return nil
}
