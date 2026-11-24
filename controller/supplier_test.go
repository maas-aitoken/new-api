package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bill detail drives the correction dialog and the adjustment tab, so its bills and
// adjustments must carry amount, rate and CNY amount even though the model hides the micro fields.
func TestSupplierBillDetailJSONCarriesAmounts(t *testing.T) {
	bill := model.SupplierBill{Id: 7, SupplierId: 3, BillMonth: "2026-10", Version: 2, Status: model.SupplierBillSettled, Currency: "USD", AmountMicros: 110_000_000, RateMicros: 7_100_000, CNYAmountMicros: 781_000_000, Remark: "fixture"}
	adjustment := model.SupplierBillAdjustment{Id: 9, BillId: 7, FeeMonth: "2026-09", Type: model.SupplierAdjustmentCharge, Currency: "CNY", AmountMicros: 5_000_000, RateMicros: 1_000_000, CNYAmountMicros: 5_000_000, Reason: "late invoice"}
	detail := &model.SupplierBillDetail{Current: &bill, Versions: []model.SupplierBill{bill}, Adjustments: []model.SupplierBillAdjustment{adjustment}}

	raw, err := common.Marshal(supplierBillDetailToJSON(detail))
	require.NoError(t, err)
	var decoded struct {
		Current     map[string]any   `json:"current"`
		Versions    []map[string]any `json:"versions"`
		Adjustments []map[string]any `json:"adjustments"`
	}
	require.NoError(t, common.Unmarshal(raw, &decoded))
	require.Len(t, decoded.Versions, 1)
	require.Len(t, decoded.Adjustments, 1)
	for _, item := range []map[string]any{decoded.Current, decoded.Versions[0]} {
		assert.Equal(t, 110.0, item["amount"])
		assert.Equal(t, 7.1, item["rate"])
		assert.Equal(t, 781.0, item["cny_amount"])
		assert.Equal(t, "fixture", item["remark"])
	}
	assert.Equal(t, 5.0, decoded.Adjustments[0]["amount"])
	assert.Equal(t, 1.0, decoded.Adjustments[0]["rate"])
	assert.Equal(t, 5.0, decoded.Adjustments[0]["cny_amount"])

	pending, err := common.Marshal(supplierBillDetailToJSON(&model.SupplierBillDetail{}))
	require.NoError(t, err)
	assert.JSONEq(t, `{"current":null,"versions":[],"adjustments":[],"allocations":null,"operations":null}`, string(pending))
}
