package service

import (
	"errors"
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ChannelBillingMultiplier is the <channel, request model> multiplier of this attempt, 1 if unset.
func ChannelBillingMultiplier(c *gin.Context, info *relaycommon.RelayInfo) float64 {
	if c != nil {
		multiplier, ok := common.GetContextKeyType[float64](c, constant.ContextKeyChannelCostMultiplier)
		if ok && validChannelBillingMultiplier(multiplier) {
			return multiplier
		}
	}
	if info != nil && info.ChannelMeta != nil {
		setting := info.ChannelMeta.ChannelSetting
		return setting.CostMultiplierForModel(info.OriginModelName)
	}
	return 1
}

func validChannelBillingMultiplier(multiplier float64) bool {
	return multiplier > 0 && !math.IsNaN(multiplier) && !math.IsInf(multiplier, 0)
}

// TaskBillingMultiplier is the multiplier stored at submit; older tasks were charged list price.
func TaskBillingMultiplier(bc *model.TaskBillingContext) float64 {
	if bc == nil || !validChannelBillingMultiplier(bc.CostMultiplier) {
		return 1
	}
	return bc.CostMultiplier
}

// recordChannelBillingMultiplier writes the multiplier into the log; absent means 1.
func recordChannelBillingMultiplier(other *model.LogOther, multiplier float64) {
	if other != nil && multiplier != 1 {
		other.SetPublic("cost_multiplier", multiplier)
	}
}

func RecordChannelBillingMultiplier(c *gin.Context, info *relaycommon.RelayInfo, other *model.LogOther) {
	recordChannelBillingMultiplier(other, ChannelBillingMultiplier(c, info))
}

func ScaleChannelBillingQuota(c *gin.Context, info *relaycommon.RelayInfo, quota int) int {
	if quota <= 0 {
		return quota
	}
	scaled, clamp := common.QuotaFromFloatChecked(float64(quota) * ChannelBillingMultiplier(c, info))
	noteQuotaClamp(info, clamp)
	return scaled
}

// PrepareChannelBilling reserves an attempt at the channel's multiplier. Tasks
// must be covered by the balance; other retries use Reserve like a tiered retry,
// which keeps the trusted bypass.
func PrepareChannelBilling(c *gin.Context, info *relaycommon.RelayInfo, baseQuota int) *types.NewAPIError {
	if info.Billing == nil && info.PriceData.FreeModel {
		return nil
	}
	targetQuota := ScaleChannelBillingQuota(c, info, baseQuota)
	if info.Billing == nil || info.ForcePreConsume {
		return PreConsumeBilling(c, targetQuota, info)
	}
	if err := info.Billing.Reserve(targetQuota); err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}
