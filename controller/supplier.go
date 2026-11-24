package controller

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type supplierRequest struct {
	Id               int     `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	Website          string  `json:"website"`
	Owner            string  `json:"owner"`
	Contact          string  `json:"contact"`
	SettlementMethod string  `json:"settlement_method"`
	Currency         string  `json:"currency"`
	DefaultRate      float64 `json:"default_rate"`
	Description      string  `json:"description"`
	Enabled          *bool   `json:"enabled"`
}
type supplierBillRequest struct {
	BillMonth   string                      `json:"bill_month"`
	Version     int                         `json:"version"`
	Currency    string                      `json:"currency"`
	Amount      float64                     `json:"amount"`
	Rate        float64                     `json:"rate"`
	Remark      string                      `json:"remark"`
	Revision    bool                        `json:"revision"`
	Adjustments []supplierAdjustmentRequest `json:"adjustments"`
}
type supplierAdjustmentRequest struct {
	BillId   int     `json:"bill_id"`
	FeeMonth string  `json:"fee_month"`
	Type     string  `json:"type"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Rate     float64 `json:"rate"`
	Reason   string  `json:"reason"`
}

func supplierID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid supplier ID")
		return 0, false
	}
	return id, true
}
func supplierMicrosFromFloat(value float64) int64 {
	return decimal.NewFromFloat(value).Mul(decimal.NewFromInt(1_000_000)).IntPart()
}
func supplierFloat(micros int64) float64 {
	return decimal.NewFromInt(micros).Div(decimal.NewFromInt(1_000_000)).InexactFloat64()
}
func supplierToJSON(s model.Supplier) gin.H {
	return gin.H{"id": s.Id, "name": s.Name, "type": s.Type, "website": s.Website, "owner": s.Owner, "contact": s.Contact, "settlement_method": s.SettlementMethod, "currency": s.Currency, "default_rate": supplierFloat(s.DefaultRateMicros), "enabled": s.Enabled, "participates_in_settlement": s.ParticipatesInSettlement, "description": s.Description, "channel_count": s.ChannelCount, "model_count": s.ModelCount, "service_status": s.ServiceStatus, "created_time": s.CreatedTime, "updated_time": s.UpdatedTime}
}
func supplierBillToJSON(b model.SupplierBill) gin.H {
	return gin.H{"id": b.Id, "supplier_id": b.SupplierId, "bill_month": b.BillMonth, "version": b.Version, "status": b.Status, "currency": b.Currency, "amount": supplierFloat(b.AmountMicros), "rate": supplierFloat(b.RateMicros), "cny_amount": supplierFloat(b.CNYAmountMicros), "estimated_snapshot": supplierFloat(b.EstimatedSnapshotMicros), "remark": b.Remark, "settled_at": b.SettledAt, "settled_by": b.SettledBy, "superseded_by": b.SupersededBy, "created_at": b.CreatedAt, "updated_at": b.UpdatedAt}
}
func supplierAdjustmentToJSON(a model.SupplierBillAdjustment) gin.H {
	return gin.H{"id": a.Id, "bill_id": a.BillId, "fee_month": a.FeeMonth, "type": a.Type, "currency": a.Currency, "amount": supplierFloat(a.AmountMicros), "rate": supplierFloat(a.RateMicros), "cny_amount": supplierFloat(a.CNYAmountMicros), "reason": a.Reason, "created_at": a.CreatedAt, "created_by": a.CreatedBy}
}
func supplierError(c *gin.Context, err error) { common.ApiErrorT(c, err.Error()) }
func mustJSON(value any) []byte               { data, _ := common.Marshal(value); return data }

func GetSuppliers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	enabled := (*bool)(nil)
	if value := c.Query("enabled"); value != "" {
		parsed := value == "true" || value == "1"
		enabled = &parsed
	}
	items, total, err := model.ListSuppliers(c.Query("keyword"), c.Query("type"), enabled, pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		supplierError(c, err)
		return
	}
	rows := make([]gin.H, 0, len(items))
	for _, item := range items {
		rows = append(rows, supplierToJSON(item))
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rows)
	summary := model.SupplierSummary{}
	if all, _, summaryErr := model.ListSuppliers("", "", nil, 1, 10000); summaryErr == nil {
		for _, item := range all {
			summary.SupplierCount++
			if item.Enabled {
				summary.EnabledCount++
			}
			if item.ServiceStatus == "unavailable" || item.ServiceStatus == "partial" {
				summary.AbnormalCount++
			}
			summary.ChannelCount += item.ChannelCount
			summary.ModelCount += item.ModelCount
		}
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total, "page": pageInfo.GetPage(), "page_size": pageInfo.GetPageSize(), "summary": summary})
}
func CreateSupplier(c *gin.Context) {
	var req supplierRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	s := model.Supplier{Name: req.Name, Type: req.Type, Website: req.Website, Owner: req.Owner, Contact: req.Contact, SettlementMethod: req.SettlementMethod, Currency: req.Currency, DefaultRateMicros: supplierMicrosFromFloat(req.DefaultRate), Description: req.Description}
	if err := model.CreateSupplier(&s, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	recordManageAudit(c, "supplier.create", map[string]any{"supplier_id": s.Id})
	common.ApiSuccess(c, supplierToJSON(s))
}
func UpdateSupplier(c *gin.Context) {
	var req supplierRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	s := model.Supplier{Id: req.Id, Name: req.Name, Type: req.Type, Website: req.Website, Owner: req.Owner, Contact: req.Contact, SettlementMethod: req.SettlementMethod, Currency: req.Currency, DefaultRateMicros: supplierMicrosFromFloat(req.DefaultRate), Description: req.Description}
	if err := model.UpdateSupplier(&s, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	if req.Enabled != nil {
		if err := model.SetSupplierEnabled(req.Id, *req.Enabled, c.GetInt("id")); err != nil {
			supplierError(c, err)
			return
		}
	}
	recordManageAudit(c, "supplier.update", map[string]any{"supplier_id": req.Id})
	common.ApiSuccess(c, supplierToJSON(s))
}
func UpdateSupplierStatus(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	if err := model.SetSupplierEnabled(id, req.Enabled, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func DeleteSupplier(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	if err := model.DeleteSupplier(id, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	recordManageAudit(c, "supplier.delete", map[string]any{"supplier_id": id})
	common.ApiSuccess(c, nil)
}
func GetSupplier(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	s, err := model.GetSupplier(id)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, supplierToJSON(*s))
}
func GetSupplierChannels(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	rows, err := model.GetSupplierChannelRows(id, c.Query("available") == "true")
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows})
}
func AddSupplierChannels(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	var req struct {
		ChannelIds []int `json:"channel_ids"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	if err := model.AddSupplierChannels(id, req.ChannelIds, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func RemoveSupplierChannel(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	channelID, err := strconv.Atoi(c.Param("channel_id"))
	if err != nil {
		supplierError(c, err)
		return
	}
	if err := model.RemoveSupplierChannel(id, channelID, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func GetSupplierOperations(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	logs, err := model.GetSupplierOperationLogs(id, 100)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": logs})
}
func GetSupplierSettlements(c *gin.Context) {
	month := c.Query("month")
	if month == "" {
		month = model.DefaultSupplierBillMonth()
	}
	rows, err := model.GetSupplierSettlementRows(month)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"month": month, "items": rows})
}
func GetSupplierBill(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	month := c.Query("month")
	if month == "" {
		month = model.DefaultSupplierBillMonth()
	}
	rows, err := model.GetSupplierSettlementRows(month)
	if err != nil {
		supplierError(c, err)
		return
	}
	for _, row := range rows {
		if row.SupplierId == id {
			common.ApiSuccess(c, row)
			return
		}
	}
	supplierError(c, model.ErrSupplierNotFound)
}

func GetSupplierBillDetail(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	month := c.Query("month")
	if month == "" {
		month = model.DefaultSupplierBillMonth()
	}
	detail, err := model.GetSupplierBillDetail(id, month)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, supplierBillDetailToJSON(detail))
}

// supplierBillDetailToJSON renders bills and adjustments with their amounts and rates;
// the model structs keep the micro-unit fields out of JSON.
func supplierBillDetailToJSON(detail *model.SupplierBillDetail) gin.H {
	versions := make([]gin.H, 0, len(detail.Versions))
	for _, bill := range detail.Versions {
		versions = append(versions, supplierBillToJSON(bill))
	}
	adjustments := make([]gin.H, 0, len(detail.Adjustments))
	for _, adjustment := range detail.Adjustments {
		adjustments = append(adjustments, supplierAdjustmentToJSON(adjustment))
	}
	var current gin.H
	if detail.Current != nil {
		current = supplierBillToJSON(*detail.Current)
	}
	return gin.H{"current": current, "versions": versions, "adjustments": adjustments, "allocations": detail.Allocations, "operations": detail.Operations}
}
func SaveSupplierBill(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	var req supplierBillRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	bill := model.SupplierBill{SupplierId: id, BillMonth: req.BillMonth, Currency: strings.ToUpper(req.Currency), AmountMicros: supplierMicrosFromFloat(req.Amount), RateMicros: supplierMicrosFromFloat(req.Rate), Remark: req.Remark}
	saved, err := model.SaveSupplierBillDraft(bill, req.Version, req.Revision, c.GetInt("id"))
	if err != nil {
		supplierError(c, err)
		return
	}
	for _, item := range req.Adjustments {
		adjustment := model.SupplierBillAdjustment{BillId: saved.Id, FeeMonth: item.FeeMonth, Type: item.Type, Currency: strings.ToUpper(item.Currency), AmountMicros: supplierMicrosFromFloat(item.Amount), RateMicros: supplierMicrosFromFloat(item.Rate), Reason: item.Reason, CreatedBy: c.GetInt("id")}
		if err := model.AddSupplierBillAdjustment(id, saved.Id, adjustment, c.GetInt("id")); err != nil {
			supplierError(c, err)
			return
		}
	}
	common.ApiSuccess(c, supplierBillToJSON(*saved))
}
func AddSupplierAdjustment(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	var req supplierAdjustmentRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		supplierError(c, err)
		return
	}
	adjustment := model.SupplierBillAdjustment{BillId: req.BillId, FeeMonth: req.FeeMonth, Type: req.Type, Currency: strings.ToUpper(req.Currency), AmountMicros: supplierMicrosFromFloat(req.Amount), RateMicros: supplierMicrosFromFloat(req.Rate), Reason: req.Reason, CreatedBy: c.GetInt("id")}
	if err := model.AddSupplierBillAdjustment(id, req.BillId, adjustment, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, supplierAdjustmentToJSON(adjustment))
}

func DeleteSupplierAdjustment(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	adjustmentID, err := strconv.Atoi(c.Param("adjustment_id"))
	if err != nil {
		supplierError(c, err)
		return
	}
	if err := model.DeleteSupplierBillAdjustment(id, adjustmentID, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func ConfirmSupplierBill(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	billID, _ := strconv.Atoi(c.Param("bill_id"))
	version, _ := strconv.Atoi(c.Query("version"))
	if err := model.ConfirmSupplierBill(id, billID, version, c.GetInt("id")); err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
func GetSupplierAllocations(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	billID, _ := strconv.Atoi(c.Param("bill_id"))
	var bill model.SupplierBill
	if err := model.DB.Where("id = ? AND supplier_id = ?", billID, id).First(&bill).Error; err != nil {
		supplierError(c, model.ErrSupplierBillNotFound)
		return
	}
	rows, err := model.GetSupplierBillAllocations(bill.Id)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}
func GetSupplierMonitoring(c *gin.Context) {
	id, ok := supplierID(c)
	if !ok {
		return
	}
	channels, err := model.GetSupplierChannelRows(id, false)
	if err != nil {
		supplierError(c, err)
		return
	}
	rangeName := c.Query("range")
	if rangeName == "" {
		rangeName = "24h"
	}
	end := time.Now().In(time.Local)
	start := end.Add(-24 * time.Hour)
	switch rangeName {
	case "1h":
		start = end.Add(-time.Hour)
	case "7d":
		start = end.Add(-7 * 24 * time.Hour)
	case "24h":
	case "custom":
		var parseErr error
		start, parseErr = time.ParseInLocation(time.RFC3339, c.Query("start"), time.Local)
		if parseErr != nil {
			supplierError(c, errors.New("Invalid monitoring start"))
			return
		}
		end, parseErr = time.ParseInLocation(time.RFC3339, c.Query("end"), time.Local)
		if parseErr != nil {
			supplierError(c, errors.New("Invalid monitoring end"))
			return
		}
	default:
		supplierError(c, errors.New("Invalid monitoring range"))
		return
	}
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel["id"].(int))
	}
	result, err := model.QuerySupplierMonitoring(ids, rangeName, start, end)
	if err != nil {
		supplierError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"window": result.Range, "start": result.Start, "end": result.End, "success_rate": result.SuccessRate, "request_total": result.RequestTotal, "average_first_token_latency": result.AverageFirstTokenLatency, "rpm": result.RPM, "tpm": result.TPM, "cache_hit_rate": result.CacheHitRate, "success_trend": result.SuccessTrend, "request_trend": result.RequestTrend, "error_codes": result.ErrorCodes, "channels": result.Channels})
}
