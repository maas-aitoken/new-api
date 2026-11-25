package model

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SupplierTypeModelFactory   = "model_factory"
	SupplierTypeThirdParty     = "third_party"
	SupplierTypeInternal       = "internal"
	SupplierSettlementUsage    = "usage"
	SupplierSettlementPrepaid  = "prepaid"
	SupplierSettlementMonthly  = "monthly"
	SupplierSettlementInternal = "internal"
	SupplierBillDraft          = "draft"
	SupplierBillSettled        = "settled"
	SupplierBillSuperseded     = "superseded"
	SupplierAdjustmentCharge   = "charge"
	SupplierAdjustmentCredit   = "credit"
)

const supplierMicros int64 = 1_000_000

// Plain env var so a Consul KV config path can set it at startup like the database connection.
const supplierCostDiffAccountEnv = "SUPPLIER_COST_DIFF_ACCOUNT"

// SupplierCostDiffAccount is the account historical differences are billed to, empty when unset.
func SupplierCostDiffAccount() string {
	return strings.TrimSpace(os.Getenv(supplierCostDiffAccountEnv))
}

var (
	ErrSupplierNotFound        = errors.New("Supplier not found")
	ErrSupplierBillNotFound    = errors.New("Supplier bill not found")
	ErrSupplierBillRequired    = errors.New("Import the supplier bill before adding adjustments")
	ErrSupplierBillSettled     = errors.New("Settled supplier bills are read-only")
	ErrSupplierBillVersion     = errors.New("Supplier bill version changed; reload and try again")
	ErrSupplierFeeMonth        = errors.New("Adjustment month must be earlier than the bill month")
	ErrSupplierChannelConflict = errors.New("One or more channels are already assigned to another supplier")
)

type Supplier struct {
	Id                       int            `json:"id"`
	Name                     string         `json:"name" gorm:"size:128;not null"`
	NameKey                  string         `json:"-" gorm:"size:128;not null;uniqueIndex:uk_supplier_name_key"`
	Type                     string         `json:"type" gorm:"size:32;not null"`
	Website                  string         `json:"website,omitempty" gorm:"size:255"`
	Owner                    string         `json:"owner,omitempty" gorm:"size:64"`
	Contact                  string         `json:"contact,omitempty" gorm:"size:128"`
	SettlementMethod         string         `json:"settlement_method" gorm:"size:32;not null"`
	Currency                 string         `json:"currency" gorm:"size:8;not null"`
	DefaultRateMicros        int64          `json:"-" gorm:"not null"`
	Enabled                  bool           `json:"enabled" gorm:"not null"`
	ParticipatesInSettlement bool           `json:"participates_in_settlement" gorm:"not null"`
	Description              string         `json:"description,omitempty" gorm:"type:text"`
	CreatedTime              int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime              int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt                gorm.DeletedAt `json:"-" gorm:"index"`
	ChannelCount             int64          `json:"channel_count" gorm:"-"`
	ModelCount               int64          `json:"model_count" gorm:"-"`
	ServiceStatus            string         `json:"service_status" gorm:"-"`
}

type SupplierChannel struct {
	Id         int   `json:"id"`
	SupplierId int   `json:"supplier_id" gorm:"not null;index:uk_supplier_channel,priority:1"`
	ChannelId  int   `json:"channel_id" gorm:"not null;uniqueIndex:uk_supplier_channel_id"`
	CreatedAt  int64 `json:"created_at" gorm:"bigint"`
}

type SupplierBill struct {
	Id                      int    `json:"id"`
	SupplierId              int    `json:"supplier_id" gorm:"not null;uniqueIndex:uk_supplier_bill_version,priority:1;index"`
	BillMonth               string `json:"bill_month" gorm:"size:7;not null;uniqueIndex:uk_supplier_bill_version,priority:2;index"`
	Version                 int    `json:"version" gorm:"not null;uniqueIndex:uk_supplier_bill_version,priority:3"`
	Status                  string `json:"status" gorm:"size:16;not null;index"`
	Currency                string `json:"currency" gorm:"size:8;not null"`
	AmountMicros            int64  `json:"-" gorm:"not null"`
	RateMicros              int64  `json:"-" gorm:"not null"`
	CNYAmountMicros         int64  `json:"-" gorm:"not null"`
	Remark                  string `json:"remark,omitempty" gorm:"type:text"`
	EstimatedSnapshotMicros int64  `json:"-" gorm:"not null"`
	SettledAt               int64  `json:"settled_at" gorm:"bigint"`
	SettledBy               int    `json:"settled_by"`
	SupersededBy            int    `json:"superseded_by"`
	CreatedAt               int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt               int64  `json:"updated_at" gorm:"bigint"`
}

type SupplierBillAdjustment struct {
	Id              int    `json:"id"`
	BillId          int    `json:"bill_id" gorm:"not null;index"`
	FeeMonth        string `json:"fee_month" gorm:"size:7;not null"`
	Type            string `json:"type" gorm:"size:16;not null"`
	Currency        string `json:"currency" gorm:"size:8;not null"`
	AmountMicros    int64  `json:"-" gorm:"not null"`
	RateMicros      int64  `json:"-" gorm:"not null"`
	CNYAmountMicros int64  `json:"-" gorm:"not null"`
	Reason          string `json:"reason" gorm:"type:text;not null"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint"`
	CreatedBy       int    `json:"created_by"`
}

type SupplierBillAllocation struct {
	Id              int    `json:"id"`
	BillId          int    `json:"bill_id" gorm:"not null;index"`
	ChannelId       int    `json:"channel_id" gorm:"not null;index"`
	ChannelName     string `json:"channel_name" gorm:"size:128;not null;default:''"`
	UserId          int    `json:"user_id" gorm:"not null;default:0;index"`
	Username        string `json:"username" gorm:"size:64;not null;default:''"`
	ModelName       string `json:"model_name" gorm:"size:128;not null;default:''"`
	QuotaMicros     int64  `json:"quota_micros" gorm:"not null"`
	ShareMicros     int64  `json:"share_micros" gorm:"not null"`
	EstimatedMicros int64  `json:"estimated_micros" gorm:"not null"`
	ActualMicros    int64  `json:"actual_micros" gorm:"not null"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint"`
}

type SupplierOperationLog struct {
	Id         int    `json:"id"`
	SupplierId int    `json:"supplier_id" gorm:"not null;index"`
	BillId     int    `json:"bill_id" gorm:"index"`
	Action     string `json:"action" gorm:"size:64;not null;index"`
	Params     string `json:"params" gorm:"type:text"`
	Result     string `json:"result" gorm:"type:text"`
	OperatorId int    `json:"operator_id"`
	Operator   string `json:"operator" gorm:"size:64"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint;index"`
}

type SupplierSummary struct {
	SupplierCount int64 `json:"supplier_count"`
	EnabledCount  int64 `json:"enabled_count"`
	AbnormalCount int64 `json:"abnormal_count"`
	ChannelCount  int64 `json:"channel_count"`
	ModelCount    int64 `json:"model_count"`
}

type SupplierMonitoringChannel struct {
	ChannelId    int     `json:"channel_id"`
	Status       string  `json:"status"`
	SuccessRate  float64 `json:"success_rate"`
	RequestTotal int64   `json:"request_total"`
}

type SupplierMonitoringBucket struct {
	Start        int64   `json:"start"`
	SuccessRate  float64 `json:"success_rate"`
	RequestTotal int64   `json:"request_total"`
}

type SupplierMonitoringErrorCode struct {
	Code  string  `json:"code"`
	Count int64   `json:"count"`
	Rate  float64 `json:"rate"`
}

type SupplierMonitoringResult struct {
	Start                    int64                         `json:"start"`
	End                      int64                         `json:"end"`
	Range                    string                        `json:"range"`
	SuccessRate              float64                       `json:"success_rate"`
	RequestTotal             int64                         `json:"request_total"`
	RPM                      float64                       `json:"rpm"`
	TPM                      float64                       `json:"tpm"`
	CacheHitRate             float64                       `json:"cache_hit_rate"`
	AverageFirstTokenLatency *float64                      `json:"average_first_token_latency"`
	SuccessTrend             []SupplierMonitoringBucket    `json:"success_trend"`
	RequestTrend             []SupplierMonitoringBucket    `json:"request_trend"`
	Channels                 []SupplierMonitoringChannel   `json:"channels"`
	ErrorCodes               []SupplierMonitoringErrorCode `json:"error_codes"`
}

type SupplierSettlementRow struct {
	SupplierId           int     `json:"supplier_id"`
	SupplierName         string  `json:"supplier_name"`
	BillMonth            string  `json:"bill_month"`
	ChannelCount         int64   `json:"channel_count"`
	Status               string  `json:"status"`
	EstimatedMicros      int64   `json:"estimated_micros"`
	Currency             string  `json:"currency"`
	AmountMicros         int64   `json:"amount_micros"`
	RateMicros           int64   `json:"rate_micros"`
	CNYAmountMicros      int64   `json:"cny_amount_micros"`
	AdjustmentMicros     int64   `json:"adjustment_micros"`
	TotalMicros          int64   `json:"total_micros"`
	DifferenceMicros     int64   `json:"difference_micros"`
	DifferenceRate       float64 `json:"difference_rate"`
	BillId               int     `json:"bill_id"`
	Version              int     `json:"version"`
	RevisionDraftId      int     `json:"revision_draft_id"`
	RevisionDraftVersion int     `json:"revision_draft_version"`
}

func (Supplier) TableName() string               { return "suppliers" }
func (SupplierChannel) TableName() string        { return "supplier_channels" }
func (SupplierBill) TableName() string           { return "supplier_bills" }
func (SupplierBillAdjustment) TableName() string { return "supplier_bill_adjustments" }
func (SupplierBillAllocation) TableName() string { return "supplier_bill_allocations" }
func (SupplierOperationLog) TableName() string   { return "supplier_operation_logs" }

func DefaultSupplierBillMonth() string {
	return time.Now().In(time.Local).AddDate(0, -1, 0).Format("2006-01")
}

func parseSupplierMonth(month string) (time.Time, error) {
	parsed, err := time.ParseInLocation("2006-01", month, time.Local)
	if err != nil || parsed.Format("2006-01") != month {
		return time.Time{}, errors.New("Invalid bill month")
	}
	return parsed, nil
}

func normalizeSupplier(s *Supplier) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len([]rune(s.Name)) > 40 {
		return errors.New("Supplier name must be 1-40 characters")
	}
	s.NameKey = strings.ToLower(s.Name)
	if s.Type == "" {
		s.Type = SupplierTypeThirdParty
	}
	if s.Type != SupplierTypeModelFactory && s.Type != SupplierTypeThirdParty && s.Type != SupplierTypeInternal {
		return errors.New("Invalid supplier type")
	}
	if s.SettlementMethod == "" {
		s.SettlementMethod = SupplierSettlementUsage
	}
	if s.SettlementMethod != SupplierSettlementUsage && s.SettlementMethod != SupplierSettlementPrepaid && s.SettlementMethod != SupplierSettlementMonthly && s.SettlementMethod != SupplierSettlementInternal {
		return errors.New("Invalid settlement method")
	}
	s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
	if s.Currency == "" {
		s.Currency = "CNY"
	}
	if s.Currency != "CNY" && s.Currency != "USD" {
		return errors.New("Currency must be CNY or USD")
	}
	if s.DefaultRateMicros <= 0 {
		s.DefaultRateMicros = supplierMicros * 730 / 100
	}
	s.ParticipatesInSettlement = s.Type != SupplierTypeInternal && s.SettlementMethod != SupplierSettlementInternal
	return nil
}

func amountMicros(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, errors.New("Amount must be greater than zero")
	}
	d, err := decimal.NewFromString(strings.TrimSpace(decimal.NewFromFloat(value).StringFixed(6)))
	if err != nil {
		return 0, err
	}
	return d.Mul(decimal.NewFromInt(supplierMicros)).IntPart(), nil
}

func decimalAmount(micros int64) float64 {
	return decimal.NewFromInt(micros).Div(decimal.NewFromInt(supplierMicros)).InexactFloat64()
}

func CreateSupplier(s *Supplier, operatorIDs ...int) error {
	if err := normalizeSupplier(s); err != nil {
		return err
	}
	now := common.GetTimestamp()
	s.CreatedTime, s.UpdatedTime, s.Enabled = now, now, true
	return DB.Transaction(func(tx *gorm.DB) error {
		var existing Supplier
		if err := tx.Where("name_key = ?", s.NameKey).First(&existing).Error; err == nil {
			return errors.New("Supplier name already exists")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"after": map[string]any{"name": s.Name, "type": s.Type, "currency": s.Currency, "settlement_method": s.SettlementMethod}})
		return AddSupplierOperationLog(tx, s.Id, operatorID, "create", string(params), "success")
	})
}

func GetSupplier(id int) (*Supplier, error) {
	var s Supplier
	if err := DB.First(&s, id).Error; err != nil {
		return nil, ErrSupplierNotFound
	}
	if err := enrichSupplier(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func enrichSupplier(s *Supplier) error {
	return enrichSuppliers([]*Supplier{s})
}

func enrichSuppliers(suppliers []*Supplier) error {
	if len(suppliers) == 0 {
		return nil
	}
	supplierIDs := make([]int, 0, len(suppliers))
	for _, supplier := range suppliers {
		supplierIDs = append(supplierIDs, supplier.Id)
	}
	type channelInfo struct {
		SupplierID int
		ChannelID  int
		Models     string
		Status     int
	}
	var channels []channelInfo
	if err := DB.Table("supplier_channels").Select("supplier_channels.supplier_id, channels.id AS channel_id, channels.models, channels.status").Joins("JOIN channels ON channels.id = supplier_channels.channel_id").Where("supplier_channels.supplier_id IN ?", supplierIDs).Scan(&channels).Error; err != nil {
		return err
	}
	type healthMetric struct {
		Count  int64
		Failed int64
	}
	health := make(map[int]healthMetric)
	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		channelIDs = append(channelIDs, channel.ChannelID)
	}
	if len(channelIDs) > 0 {
		var healthRows []struct {
			ChannelID int
			Count     int64
			Failed    int64
		}
		start := time.Now().In(time.Local).Add(-24 * time.Hour).Unix()
		if err := DB.Table("quota_data").Select("channel_id, COALESCE(SUM(count),0) AS count, COALESCE(SUM(failed_count),0) AS failed").Where("channel_id IN ? AND created_at >= ?", channelIDs, start).Group("channel_id").Scan(&healthRows).Error; err != nil {
			return err
		}
		for _, row := range healthRows {
			health[row.ChannelID] = healthMetric{Count: row.Count, Failed: row.Failed}
		}
	}
	bySupplier := make(map[int][]channelInfo)
	for _, channel := range channels {
		bySupplier[channel.SupplierID] = append(bySupplier[channel.SupplierID], channel)
	}
	for _, supplier := range suppliers {
		infos := bySupplier[supplier.Id]
		models := make(map[string]struct{})
		statuses := make([]string, 0, len(infos))
		for _, channel := range infos {
			for _, name := range strings.Split(strings.Trim(channel.Models, ","), ",") {
				if name = strings.TrimSpace(name); name != "" {
					models[name] = struct{}{}
				}
			}
			metric := health[channel.ChannelID]
			statuses = append(statuses, supplierChannelHealthStatus(channel.Status, metric.Count, metric.Failed))
		}
		supplier.ChannelCount = int64(len(infos))
		supplier.ModelCount = int64(len(models))
		supplier.ServiceStatus = SupplierServiceStatusFromChannels(statuses)
	}
	return nil
}

func supplierChannelHealthStatus(enabled int, count, failed int64) string {
	if enabled != common.ChannelStatusEnabled {
		return "unavailable"
	}
	total := count + failed
	if total == 0 {
		return "no_data"
	}
	rate := float64(count) / float64(total) * 100
	if rate >= 96 {
		return "normal"
	}
	if rate >= 88 {
		return "partial"
	}
	return "unavailable"
}

func SupplierServiceStatusFromChannels(statuses []string) string {
	if len(statuses) == 0 {
		return "no_data"
	}
	allNoData := true
	for _, status := range statuses {
		if status != "no_data" {
			allNoData = false
		}
		if status == "unavailable" {
			return "unavailable"
		}
	}
	if allNoData {
		return "no_data"
	}
	for _, status := range statuses {
		if status == "partial" {
			return "partial"
		}
	}
	return "normal"
}

func UpdateSupplier(s *Supplier, operatorIDs ...int) error {
	if err := normalizeSupplier(s); err != nil {
		return err
	}
	now := common.GetTimestamp()
	return DB.Transaction(func(tx *gorm.DB) error {
		var saved Supplier
		if err := lockForUpdate(tx).First(&saved, s.Id).Error; err != nil {
			return ErrSupplierNotFound
		}
		var duplicate Supplier
		if err := tx.Where("name_key = ? AND id <> ?", s.NameKey, s.Id).First(&duplicate).Error; err == nil {
			return errors.New("Supplier name already exists")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		before, _ := common.Marshal(map[string]any{"name": saved.Name, "type": saved.Type, "website": saved.Website, "owner": saved.Owner, "contact": saved.Contact, "settlement_method": saved.SettlementMethod, "currency": saved.Currency, "description": saved.Description})
		if err := tx.Model(&Supplier{}).Where("id = ?", s.Id).Updates(map[string]any{"name": s.Name, "name_key": s.NameKey, "type": s.Type, "website": s.Website, "owner": s.Owner, "contact": s.Contact, "settlement_method": s.SettlementMethod, "currency": s.Currency, "default_rate_micros": s.DefaultRateMicros, "participates_in_settlement": s.ParticipatesInSettlement, "description": s.Description, "updated_time": now}).Error; err != nil {
			return err
		}
		after, _ := common.Marshal(map[string]any{"name": s.Name, "type": s.Type, "website": s.Website, "owner": s.Owner, "contact": s.Contact, "settlement_method": s.SettlementMethod, "currency": s.Currency, "description": s.Description})
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		var beforeObject, afterObject map[string]any
		_ = common.Unmarshal(before, &beforeObject)
		_ = common.Unmarshal(after, &afterObject)
		params, _ := common.Marshal(map[string]any{"before": beforeObject, "after": afterObject})
		return AddSupplierOperationLog(tx, s.Id, operatorID, "update", string(params), "success")
	})
}

func SetSupplierEnabled(id int, enabled bool, operatorIDs ...int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Supplier{}).Where("id = ?", id).Updates(map[string]any{"enabled": enabled, "updated_time": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrSupplierNotFound
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"enabled": enabled})
		return AddSupplierOperationLog(tx, id, operatorID, "status", string(params), "success")
	})
}

func DeleteSupplier(id int, operatorIDs ...int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var supplier Supplier
		if err := tx.First(&supplier, id).Error; err != nil {
			return ErrSupplierNotFound
		}
		var n int64
		if err := tx.Table("supplier_channels").Joins("JOIN channels ON channels.id = supplier_channels.channel_id").Where("supplier_channels.supplier_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errors.New("Supplier has bound channels; unbind them before deleting")
		}
		if err := tx.Model(&SupplierBill{}).Where("supplier_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errors.New("Supplier has bills and cannot be deleted")
		}
		if err := tx.Where("supplier_id = ?", id).Delete(&SupplierChannel{}).Error; err != nil {
			return err
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"supplier_id": id, "supplier_name": supplier.Name})
		if err := AddSupplierOperationLog(tx, id, operatorID, "delete", string(params), "success"); err != nil {
			return err
		}
		return tx.Unscoped().Delete(&Supplier{}, id).Error
	})
}

func ListSuppliers(keyword, typeFilter string, enabled *bool, page, pageSize int) ([]Supplier, int64, error) {
	query := DB.Model(&Supplier{})
	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil && id > 0 {
			query = query.Where("LOWER(name_key) LIKE LOWER(?) OR id = ?", "%"+strings.ToLower(keyword)+"%", id)
		} else {
			query = query.Where("LOWER(name_key) LIKE LOWER(?)", "%"+strings.ToLower(keyword)+"%")
		}
	}
	if typeFilter != "" {
		query = query.Where("type = ?", typeFilter)
	}
	if enabled != nil {
		query = query.Where("enabled = ?", *enabled)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Supplier
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	refs := make([]*Supplier, len(rows))
	for index := range rows {
		refs[index] = &rows[index]
	}
	if err := enrichSuppliers(refs); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func GetSupplierChannelRows(supplierID int, available bool) ([]map[string]any, error) {
	q := DB.Table("channels").Select("channels.id, channels.name, channels.type, channels.status, channels.models, channels.response_time, channels.test_time").Order("channels.id DESC")
	if available {
		q = q.Where("NOT EXISTS (SELECT 1 FROM supplier_channels WHERE supplier_channels.channel_id = channels.id)")
	} else {
		q = q.Joins("JOIN supplier_channels ON supplier_channels.channel_id = channels.id AND supplier_channels.supplier_id = ?", supplierID)
	}
	var rows []struct {
		Id           int
		Name         string
		Type         int
		Status       int
		Models       string
		ResponseTime int
		TestTime     int64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Id)
	}
	type rowMetric struct {
		ChannelID int
		Count     int64
		Failed    int64
	}
	metrics := make(map[int]rowMetric)
	if !available && len(ids) > 0 {
		var data []rowMetric
		start := time.Now().In(time.Local).Add(-24 * time.Hour).Unix()
		if err := DB.Table("quota_data").Select("channel_id, COALESCE(SUM(count),0) AS count, COALESCE(SUM(failed_count),0) AS failed").Where("channel_id IN ? AND created_at >= ?", ids, start).Group("channel_id").Scan(&data).Error; err != nil {
			return nil, err
		}
		for _, metric := range data {
			metrics[metric.ChannelID] = metric
		}
	}
	for _, row := range rows {
		modelCount := 0
		if strings.Trim(row.Models, ",") != "" {
			modelCount = len(strings.Split(strings.Trim(row.Models, ","), ","))
		}
		metric := metrics[row.Id]
		result = append(result, map[string]any{"id": row.Id, "name": row.Name, "type": row.Type, "status": row.Status, "service_status": supplierChannelHealthStatus(row.Status, metric.Count, metric.Failed), "models": row.Models, "model_count": modelCount, "response_time": row.ResponseTime, "test_time": row.TestTime})
	}
	return result, nil
}

func AddSupplierChannels(supplierID int, channelIDs []int, operatorIDs ...int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var supplier Supplier
		if err := tx.First(&supplier, supplierID).Error; err != nil {
			return ErrSupplierNotFound
		}
		unique := make(map[int]struct{}, len(channelIDs))
		for _, channelID := range channelIDs {
			if channelID > 0 {
				unique[channelID] = struct{}{}
			}
		}
		ids := make([]int, 0, len(unique))
		for channelID := range unique {
			ids = append(ids, channelID)
		}
		if len(ids) > 0 {
			var count int64
			if err := tx.Model(&Channel{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(ids)) {
				return gorm.ErrRecordNotFound
			}
		}
		for _, channelID := range ids {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SupplierChannel{SupplierId: supplierID, ChannelId: channelID, CreatedAt: common.GetTimestamp()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				var owner SupplierChannel
				if err := tx.Where("channel_id = ?", channelID).First(&owner).Error; err != nil {
					return err
				}
				if owner.SupplierId != supplierID {
					return ErrSupplierChannelConflict
				}
			}
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		var channels []Channel
		if len(ids) > 0 {
			if err := tx.Select("id, name").Where("id IN ?", ids).Find(&channels).Error; err != nil {
				return err
			}
		}
		names := make(map[int]string, len(channels))
		for _, channel := range channels {
			names[channel.Id] = channel.Name
		}
		params, _ := common.Marshal(map[string]any{"channel_ids": ids, "channel_names": names})
		return AddSupplierOperationLog(tx, supplierID, operatorID, "channels.add", string(params), "success")
	})
}

func RemoveSupplierChannel(supplierID, channelID int, operatorIDs ...int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		_ = tx.Select("id, name").First(&channel, channelID).Error
		if err := tx.Where("supplier_id = ? AND channel_id = ?", supplierID, channelID).Delete(&SupplierChannel{}).Error; err != nil {
			return err
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"channel_id": channelID, "channel_name": channel.Name})
		return AddSupplierOperationLog(tx, supplierID, operatorID, "channels.remove", string(params), "success")
	})
}

type supplierUsageRow struct {
	SupplierId int
	ChannelId  int
	UserId     int
	Username   string
	ModelName  string
	Quota      int64
	Count      int64
	Failed     int64
	Input      int64
	Completion int64
	CacheRead  int64
	UseTime    int64
}

func supplierMonthWindow(month string) (int64, int64, error) {
	start, err := parseSupplierMonth(month)
	if err != nil {
		return 0, 0, err
	}
	return start.Unix(), start.AddDate(0, 1, 0).Unix(), nil
}

func supplierUsage(supplierID int, month string) ([]supplierUsageRow, error) {
	start, end, err := supplierMonthWindow(month)
	if err != nil {
		return nil, err
	}
	var rows []supplierUsageRow
	err = DB.Table("quota_data").Select("supplier_channels.supplier_id AS supplier_id, quota_data.channel_id AS channel_id, quota_data.user_id AS user_id, quota_data.username AS username, quota_data.model_name AS model_name, COALESCE(SUM(quota_data.quota),0) AS quota, COALESCE(SUM(quota_data.count),0) AS count, COALESCE(SUM(quota_data.failed_count),0) AS failed, COALESCE(SUM(quota_data.input_tokens),0) AS input, COALESCE(SUM(quota_data.completion_tokens),0) AS completion, COALESCE(SUM(quota_data.cache_read_tokens),0) AS cache_read, COALESCE(SUM(quota_data.use_time_total),0) AS use_time").Joins("JOIN supplier_channels ON supplier_channels.channel_id = quota_data.channel_id AND supplier_channels.supplier_id = ?", supplierID).Where("quota_data.created_at >= ? AND quota_data.created_at < ?", start, end).Group("supplier_channels.supplier_id, quota_data.channel_id, quota_data.user_id, quota_data.username, quota_data.model_name").Scan(&rows).Error
	return rows, err
}

func estimateMicros(rows []supplierUsageRow) int64 {
	total := int64(0)
	for _, row := range rows {
		total += row.Quota
	}
	return decimal.NewFromInt(total).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromInt(supplierMicros)).IntPart()
}

func billAdjustments(tx *gorm.DB, billID int) (int64, error) {
	var rows []SupplierBillAdjustment
	if err := tx.Where("bill_id = ?", billID).Find(&rows).Error; err != nil {
		return 0, err
	}
	total := int64(0)
	for _, row := range rows {
		if row.Type == SupplierAdjustmentCredit {
			total -= row.CNYAmountMicros
		} else {
			total += row.CNYAmountMicros
		}
	}
	return total, nil
}

// SupplierCostDiffRow is one supplier's net historical difference for FeeMonth (credits negative),
// queryable under the month it corrects even though it was entered on a later month's bill.
type SupplierCostDiffRow struct {
	SupplierId   int    `json:"supplier_id"`
	SupplierName string `json:"supplier_name"`
	FeeMonth     string `json:"fee_month"`
	Account      string `json:"account"`
	AmountMicros int64  `json:"amount_micros"`
	Remark       string `json:"remark"`
}

// GetSupplierCostDiffRows returns the net historical difference per supplier for feeMonth, billed
// to the configured account. Empty when no account is configured or nothing corrects that month.
func GetSupplierCostDiffRows(feeMonth string) ([]SupplierCostDiffRow, error) {
	if _, err := parseSupplierMonth(feeMonth); err != nil {
		return nil, err
	}
	account := SupplierCostDiffAccount()
	if account == "" {
		return nil, nil
	}
	var adjustments []SupplierBillAdjustment
	if err := DB.Where("fee_month = ?", feeMonth).Find(&adjustments).Error; err != nil {
		return nil, err
	}
	if len(adjustments) == 0 {
		return nil, nil
	}
	billIDs := make([]int, 0, len(adjustments))
	for _, adjustment := range adjustments {
		billIDs = append(billIDs, adjustment.BillId)
	}
	var bills []SupplierBill
	if err := DB.Select("id, supplier_id").Where("id IN ?", billIDs).Find(&bills).Error; err != nil {
		return nil, err
	}
	supplierOfBill := make(map[int]int, len(bills))
	for _, bill := range bills {
		supplierOfBill[bill.Id] = bill.SupplierId
	}
	totals := make(map[int]int64)
	order := make([]int, 0)
	for _, adjustment := range adjustments {
		supplierID, ok := supplierOfBill[adjustment.BillId]
		if !ok {
			continue
		}
		if _, seen := totals[supplierID]; !seen {
			order = append(order, supplierID)
		}
		if adjustment.Type == SupplierAdjustmentCredit {
			totals[supplierID] -= adjustment.CNYAmountMicros
		} else {
			totals[supplierID] += adjustment.CNYAmountMicros
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	var suppliers []Supplier
	if err := DB.Select("id, name").Where("id IN ?", order).Find(&suppliers).Error; err != nil {
		return nil, err
	}
	names := make(map[int]string, len(suppliers))
	for _, supplier := range suppliers {
		names[supplier.Id] = supplier.Name
	}
	remark := fmt.Sprintf("调整%s月差值", feeMonth)
	rows := make([]SupplierCostDiffRow, 0, len(order))
	for _, supplierID := range order {
		if totals[supplierID] == 0 {
			continue
		}
		rows = append(rows, SupplierCostDiffRow{
			SupplierId:   supplierID,
			SupplierName: names[supplierID],
			FeeMonth:     feeMonth,
			Account:      account,
			AmountMicros: totals[supplierID],
			Remark:       remark,
		})
	}
	return rows, nil
}

func latestBill(tx *gorm.DB, supplierID int, month string, lock bool) (*SupplierBill, error) {
	var bill SupplierBill
	q := tx.Where("supplier_id = ? AND bill_month = ? AND status <> ?", supplierID, month, SupplierBillSuperseded).Order("version DESC")
	if lock {
		q = lockForUpdate(q)
	}
	if err := q.First(&bill).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &bill, nil
}

func GetSupplierSettlementRows(month string) ([]SupplierSettlementRow, error) {
	if _, err := parseSupplierMonth(month); err != nil {
		return nil, err
	}
	var suppliers []Supplier
	if err := DB.Where("participates_in_settlement = ? AND type <> ?", true, SupplierTypeInternal).Order("id DESC").Find(&suppliers).Error; err != nil {
		return nil, err
	}
	if len(suppliers) == 0 {
		return []SupplierSettlementRow{}, nil
	}
	ids := make([]int, 0, len(suppliers))
	for _, supplier := range suppliers {
		ids = append(ids, supplier.Id)
	}
	channelCounts := make(map[int]int64)
	var counts []struct {
		SupplierId int
		Count      int64
	}
	if err := DB.Table("supplier_channels").Select("supplier_channels.supplier_id, COUNT(channels.id) AS count").Joins("LEFT JOIN channels ON channels.id = supplier_channels.channel_id").Where("supplier_channels.supplier_id IN ?", ids).Group("supplier_channels.supplier_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	for _, row := range counts {
		channelCounts[row.SupplierId] = row.Count
	}
	start, end, err := supplierMonthWindow(month)
	if err != nil {
		return nil, err
	}
	var usageRows []supplierUsageRow
	if err := DB.Table("quota_data").Select("supplier_channels.supplier_id AS supplier_id, quota_data.channel_id AS channel_id, quota_data.model_name AS model_name, COALESCE(SUM(quota_data.quota),0) AS quota").Joins("JOIN supplier_channels ON supplier_channels.channel_id = quota_data.channel_id").Where("supplier_channels.supplier_id IN ? AND quota_data.created_at >= ? AND quota_data.created_at < ?", ids, start, end).Group("supplier_channels.supplier_id, quota_data.channel_id, quota_data.model_name").Scan(&usageRows).Error; err != nil {
		return nil, err
	}
	usageBySupplier := make(map[int][]supplierUsageRow)
	for _, usage := range usageRows {
		usageBySupplier[usage.SupplierId] = append(usageBySupplier[usage.SupplierId], usage)
	}
	var bills []SupplierBill
	if err := DB.Where("supplier_id IN ? AND bill_month = ? AND status <> ?", ids, month, SupplierBillSuperseded).Order("supplier_id, version DESC").Find(&bills).Error; err != nil {
		return nil, err
	}
	bySupplier := make(map[int][]SupplierBill)
	billIDs := make([]int, 0, len(bills))
	for _, bill := range bills {
		bySupplier[bill.SupplierId] = append(bySupplier[bill.SupplierId], bill)
		billIDs = append(billIDs, bill.Id)
	}
	adjustments := make(map[int]int64)
	if len(billIDs) > 0 {
		var adjustmentRows []struct {
			BillId int
			Type   string
			Amount int64
		}
		if err := DB.Table("supplier_bill_adjustments").Select("bill_id, type, COALESCE(SUM(cny_amount_micros),0) AS amount").Where("bill_id IN ?", billIDs).Group("bill_id, type").Scan(&adjustmentRows).Error; err != nil {
			return nil, err
		}
		for _, row := range adjustmentRows {
			if row.Type == SupplierAdjustmentCredit {
				adjustments[row.BillId] -= row.Amount
			} else {
				adjustments[row.BillId] += row.Amount
			}
		}
	}
	rows := make([]SupplierSettlementRow, 0, len(suppliers))
	for _, supplier := range suppliers {
		usage := usageBySupplier[supplier.Id]
		item := SupplierSettlementRow{SupplierId: supplier.Id, SupplierName: supplier.Name, BillMonth: month, ChannelCount: channelCounts[supplier.Id], Status: "pending", Currency: supplier.Currency, EstimatedMicros: estimateMicros(usage)}
		var settled *SupplierBill
		var draft *SupplierBill
		for index := range bySupplier[supplier.Id] {
			bill := &bySupplier[supplier.Id][index]
			if bill.Status == SupplierBillSettled && settled == nil {
				settled = bill
			}
			if bill.Status == SupplierBillDraft && draft == nil {
				draft = bill
			}
		}
		bill := settled
		if bill == nil {
			bill = draft
		}
		if bill != nil {
			item.Status, item.BillId, item.Version = bill.Status, bill.Id, bill.Version
			item.AmountMicros, item.RateMicros, item.CNYAmountMicros = bill.AmountMicros, bill.RateMicros, bill.CNYAmountMicros
			if bill.Status == SupplierBillSettled {
				item.EstimatedMicros = bill.EstimatedSnapshotMicros
			}
			item.AdjustmentMicros = adjustments[bill.Id]
			item.TotalMicros = item.CNYAmountMicros + item.AdjustmentMicros
			item.DifferenceMicros = item.CNYAmountMicros - item.EstimatedMicros
			if item.EstimatedMicros > 0 {
				item.DifferenceRate = float64(item.DifferenceMicros) / float64(item.EstimatedMicros)
			}
		}
		if settled != nil && draft != nil {
			item.RevisionDraftId, item.RevisionDraftVersion = draft.Id, draft.Version
		}
		rows = append(rows, item)
	}
	return rows, nil
}

func SaveSupplierBillDraft(req SupplierBill, expectedVersion int, revision bool, operatorIDs ...int) (*SupplierBill, error) {
	var supplier Supplier
	if err := DB.First(&supplier, req.SupplierId).Error; err != nil {
		return nil, ErrSupplierNotFound
	}
	if !supplier.ParticipatesInSettlement {
		return nil, errors.New("Supplier does not participate in settlement")
	}
	if _, err := parseSupplierMonth(req.BillMonth); err != nil {
		return nil, err
	}
	amount, err := amountMicros(decimalAmount(req.AmountMicros))
	if err != nil {
		return nil, err
	}
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	if req.Currency != "CNY" && req.Currency != "USD" {
		return nil, errors.New("Currency must be CNY or USD")
	}
	rate := req.RateMicros
	if req.Currency == "CNY" {
		rate = supplierMicros
	} else if rate <= 0 {
		return nil, errors.New("USD exchange rate must be greater than zero")
	}
	cny := decimal.NewFromInt(amount).Mul(decimal.NewFromInt(rate)).Div(decimal.NewFromInt(supplierMicros)).IntPart()
	returnValue := &SupplierBill{}
	err = DB.Transaction(func(tx *gorm.DB) error {
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		current, err := latestBill(tx, req.SupplierId, req.BillMonth, true)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		if current == nil {
			*returnValue = req
			returnValue.Version, returnValue.Status, returnValue.CreatedAt, returnValue.UpdatedAt = 1, SupplierBillDraft, now, now
			returnValue.AmountMicros, returnValue.RateMicros, returnValue.CNYAmountMicros = amount, rate, cny
			if err := tx.Create(returnValue).Error; err != nil {
				return err
			}
			params, _ := common.Marshal(map[string]any{"bill_id": returnValue.Id, "version": returnValue.Version, "amount": returnValue.AmountMicros, "currency": returnValue.Currency, "rate": returnValue.RateMicros})
			return AddSupplierOperationLog(tx, req.SupplierId, operatorID, "bill.draft", string(params), "success", returnValue.Id)
		}
		if current.Status == SupplierBillSettled && !revision {
			return ErrSupplierBillSettled
		}
		if current.Status == SupplierBillSettled && revision {
			*returnValue = req
			returnValue.Version, returnValue.Status, returnValue.CreatedAt, returnValue.UpdatedAt = current.Version+1, SupplierBillDraft, now, now
			returnValue.AmountMicros, returnValue.RateMicros, returnValue.CNYAmountMicros = amount, rate, cny
			if err := tx.Create(returnValue).Error; err != nil {
				return err
			}
			var adjustments []SupplierBillAdjustment
			if err := tx.Where("bill_id = ?", current.Id).Find(&adjustments).Error; err != nil {
				return err
			}
			for _, adjustment := range adjustments {
				adjustment.Id = 0
				adjustment.BillId = returnValue.Id
				if err := tx.Create(&adjustment).Error; err != nil {
					return err
				}
			}
			params, _ := common.Marshal(map[string]any{"bill_id": returnValue.Id, "version": returnValue.Version, "amount": returnValue.AmountMicros, "currency": returnValue.Currency, "rate": returnValue.RateMicros, "revision": true})
			return AddSupplierOperationLog(tx, req.SupplierId, operatorID, "bill.correct", string(params), "success", returnValue.Id)
		}
		if expectedVersion > 0 && expectedVersion != current.Version {
			return ErrSupplierBillVersion
		}
		if err := tx.Model(&SupplierBill{}).Where("id = ? AND version = ?", current.Id, current.Version).Updates(map[string]any{"currency": req.Currency, "amount_micros": amount, "rate_micros": rate, "cny_amount_micros": cny, "remark": req.Remark, "updated_at": now}).Error; err != nil {
			return err
		}
		*returnValue = *current
		returnValue.AmountMicros, returnValue.RateMicros, returnValue.CNYAmountMicros, returnValue.Remark, returnValue.UpdatedAt = amount, rate, cny, req.Remark, now
		params, _ := common.Marshal(map[string]any{"bill_id": returnValue.Id, "version": returnValue.Version, "amount": returnValue.AmountMicros, "currency": returnValue.Currency, "rate": returnValue.RateMicros})
		return AddSupplierOperationLog(tx, req.SupplierId, operatorID, "bill.draft", string(params), "success", returnValue.Id)
	})
	return returnValue, err
}

func AddSupplierBillAdjustment(supplierID, billID int, adjustment SupplierBillAdjustment, operatorIDs ...int) error {
	adjustment.Reason = strings.TrimSpace(adjustment.Reason)
	if adjustment.Reason == "" {
		return errors.New("Adjustment reason is required")
	}
	if _, err := parseSupplierMonth(adjustment.FeeMonth); err != nil {
		return err
	}
	if adjustment.Type != SupplierAdjustmentCharge && adjustment.Type != SupplierAdjustmentCredit {
		return errors.New("Invalid adjustment type")
	}
	adjustment.Currency = strings.ToUpper(strings.TrimSpace(adjustment.Currency))
	if adjustment.Currency != "CNY" && adjustment.Currency != "USD" {
		return errors.New("Currency must be CNY or USD")
	}
	var bill SupplierBill
	if err := DB.Where("id = ? AND supplier_id = ?", billID, supplierID).First(&bill).Error; err != nil {
		return ErrSupplierBillNotFound
	}
	if adjustment.FeeMonth >= bill.BillMonth {
		return ErrSupplierFeeMonth
	}
	amount, err := amountMicros(decimalAmount(adjustment.AmountMicros))
	if err != nil {
		return err
	}
	rate := adjustment.RateMicros
	if adjustment.Currency == "CNY" {
		rate = supplierMicros
	} else if rate <= 0 {
		return errors.New("USD exchange rate must be greater than zero")
	}
	cny := decimal.NewFromInt(amount).Mul(decimal.NewFromInt(rate)).Div(decimal.NewFromInt(supplierMicros)).IntPart()
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND supplier_id = ?", billID, supplierID).First(&bill).Error; err != nil {
			return ErrSupplierBillNotFound
		}
		if bill.Status != SupplierBillDraft {
			return ErrSupplierBillSettled
		}
		adjustment.BillId, adjustment.AmountMicros, adjustment.RateMicros, adjustment.CNYAmountMicros, adjustment.CreatedAt = billID, amount, rate, cny, common.GetTimestamp()
		if err := tx.Create(&adjustment).Error; err != nil {
			return err
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"bill_id": billID, "adjustment_id": adjustment.Id, "fee_month": adjustment.FeeMonth, "type": adjustment.Type, "amount": adjustment.AmountMicros, "currency": adjustment.Currency, "rate": adjustment.RateMicros, "reason": adjustment.Reason})
		return AddSupplierOperationLog(tx, supplierID, operatorID, "bill.adjustment", string(params), "success", billID)
	})
}

func DeleteSupplierBillAdjustment(supplierID, adjustmentID int, operatorIDs ...int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var adjustment SupplierBillAdjustment
		if err := lockForUpdate(tx).First(&adjustment, adjustmentID).Error; err != nil {
			return ErrSupplierBillNotFound
		}
		var bill SupplierBill
		if err := lockForUpdate(tx).Where("id = ? AND supplier_id = ?", adjustment.BillId, supplierID).First(&bill).Error; err != nil {
			return ErrSupplierBillNotFound
		}
		if bill.Status != SupplierBillDraft {
			return ErrSupplierBillSettled
		}
		if err := tx.Delete(&SupplierBillAdjustment{}, adjustmentID).Error; err != nil {
			return err
		}
		operatorID := 0
		if len(operatorIDs) > 0 {
			operatorID = operatorIDs[0]
		}
		params, _ := common.Marshal(map[string]any{"bill_id": adjustment.BillId, "adjustment_id": adjustmentID})
		return AddSupplierOperationLog(tx, supplierID, operatorID, "bill.adjustment.delete", string(params), "success", adjustment.BillId)
	})
}

func ConfirmSupplierBill(supplierID, billID, expectedVersion, operatorID int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var bill SupplierBill
		if err := lockForUpdate(tx).Where("id = ? AND supplier_id = ?", billID, supplierID).First(&bill).Error; err != nil {
			return ErrSupplierBillNotFound
		}
		if bill.Version != expectedVersion {
			return ErrSupplierBillVersion
		}
		if bill.Status != SupplierBillDraft {
			return ErrSupplierBillSettled
		}
		usage, err := supplierUsage(supplierID, bill.BillMonth)
		if err != nil {
			return err
		}
		estimate := estimateMicros(usage)
		if err := tx.Model(&SupplierBill{}).Where("id = ? AND version = ?", bill.Id, bill.Version).Updates(map[string]any{"status": SupplierBillSettled, "estimated_snapshot_micros": estimate, "settled_at": common.GetTimestamp(), "settled_by": operatorID, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		if bill.Version > 1 {
			var old SupplierBill
			if err := lockForUpdate(tx).Where("supplier_id = ? AND bill_month = ? AND status = ? AND id <> ?", supplierID, bill.BillMonth, SupplierBillSettled, bill.Id).Order("version DESC").First(&old).Error; err == nil {
				if err := tx.Model(&SupplierBill{}).Where("id = ?", old.Id).Updates(map[string]any{"status": SupplierBillSuperseded, "superseded_by": bill.Id}).Error; err != nil {
					return err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := createSupplierAllocations(tx, bill.Id, usage, bill.CNYAmountMicros); err != nil {
			return err
		}
		params, _ := common.Marshal(map[string]any{"bill_id": bill.Id, "version": bill.Version, "amount": bill.CNYAmountMicros, "currency": bill.Currency, "rate": bill.RateMicros})
		return AddSupplierOperationLog(tx, supplierID, operatorID, "bill.settle", string(params), "success", bill.Id)
	})
}

func createSupplierAllocations(tx *gorm.DB, billID int, usage []supplierUsageRow, totalMicros int64) error {
	if err := tx.Where("bill_id = ?", billID).Delete(&SupplierBillAllocation{}).Error; err != nil {
		return err
	}
	if len(usage) == 0 {
		return nil
	}
	sum := int64(0)
	for _, row := range usage {
		sum += row.Quota
	}
	if sum <= 0 {
		return nil
	}
	type part struct {
		row       supplierUsageRow
		floor     int64
		remainder decimal.Decimal
	}
	parts := make([]part, len(usage))
	distributed := int64(0)
	for i, row := range usage {
		exact := decimal.NewFromInt(totalMicros).Mul(decimal.NewFromInt(row.Quota)).Div(decimal.NewFromInt(sum))
		floor := exact.IntPart()
		parts[i] = part{row: row, floor: floor, remainder: exact.Sub(decimal.NewFromInt(floor))}
		distributed += floor
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].remainder.GreaterThan(parts[j].remainder) })
	for i := int64(0); i < totalMicros-distributed && i < int64(len(parts)); i++ {
		parts[i].floor++
	}
	now := common.GetTimestamp()
	channelIDs := make([]int, 0, len(parts))
	for _, p := range parts {
		channelIDs = append(channelIDs, p.row.ChannelId)
	}
	var channels []Channel
	if err := tx.Select("id, name").Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return err
	}
	names := make(map[int]string, len(channels))
	for _, channel := range channels {
		names[channel.Id] = channel.Name
	}
	for _, p := range parts {
		channelName := names[p.row.ChannelId]
		if channelName == "" {
			channelName = "#" + strconv.Itoa(p.row.ChannelId)
		}
		estimated := decimal.NewFromInt(p.row.Quota).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(operation_setting.USDExchangeRate)).Mul(decimal.NewFromInt(supplierMicros)).IntPart()
		if err := tx.Create(&SupplierBillAllocation{BillId: billID, ChannelId: p.row.ChannelId, ChannelName: channelName, UserId: p.row.UserId, Username: p.row.Username, ModelName: p.row.ModelName, QuotaMicros: p.row.Quota, EstimatedMicros: estimated, ActualMicros: p.floor, ShareMicros: decimal.NewFromInt(p.row.Quota).Mul(decimal.NewFromInt(supplierMicros)).Div(decimal.NewFromInt(sum)).IntPart(), CreatedAt: now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func GetSupplierBillAllocations(billID int) ([]SupplierBillAllocation, error) {
	var rows []SupplierBillAllocation
	return rows, DB.Where("bill_id = ?", billID).Order("channel_id, model_name, user_id").Find(&rows).Error
}

func supplierLogStatusCodeExpr() (string, bool) {
	column := "logs.other"
	switch common.LogDatabaseType() {
	case common.DatabaseTypeSQLite:
		doc := "CASE WHEN json_valid(" + column + ") THEN " + column + " ELSE '{}' END"
		return "COALESCE(CAST(json_extract(" + doc + ", '$.status_code') AS TEXT), CAST(json_extract(" + doc + ", '$.error_code') AS TEXT), '')", true
	case common.DatabaseTypeMySQL:
		doc := "CASE WHEN JSON_VALID(" + column + ") THEN " + column + " ELSE '{}' END"
		return "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(" + doc + ", '$.status_code')), JSON_UNQUOTE(JSON_EXTRACT(" + doc + ", '$.error_code')), '')", true
	case common.DatabaseTypePostgreSQL:
		return "CASE WHEN " + column + " ~ '^\\s*\\{.*\\}\\s*$' THEN COALESCE(" + column + "::jsonb ->> 'status_code', " + column + "::jsonb ->> 'error_code', '') ELSE '' END", true
	case common.DatabaseTypeClickHouse:
		return "coalesce(nullIf(JSONExtractString(" + column + ", 'status_code'), ''), nullIf(JSONExtractString(" + column + ", 'error_code'), ''), '')", true
	default:
		return "", false
	}
}

func QuerySupplierMonitoring(channelIDs []int, rangeName string, start, end time.Time) (SupplierMonitoringResult, error) {
	if len(channelIDs) == 0 {
		return SupplierMonitoringResult{Range: rangeName, Start: start.Unix(), End: end.Unix(), Channels: []SupplierMonitoringChannel{}, SuccessTrend: []SupplierMonitoringBucket{}, RequestTrend: []SupplierMonitoringBucket{}, ErrorCodes: []SupplierMonitoringErrorCode{}}, nil
	}
	if end.Before(start) || end.Sub(start) > 31*24*time.Hour {
		return SupplierMonitoringResult{}, errors.New("Invalid monitoring time range")
	}
	result := SupplierMonitoringResult{Range: rangeName, Start: start.Unix(), End: end.Unix(), Channels: []SupplierMonitoringChannel{}, SuccessTrend: []SupplierMonitoringBucket{}, RequestTrend: []SupplierMonitoringBucket{}, ErrorCodes: []SupplierMonitoringErrorCode{}}
	var metric struct {
		Count     int64
		Failed    int64
		Token     int64
		Input     int64
		CacheRead int64
	}
	if err := DB.Table("quota_data").Where("channel_id IN ? AND created_at >= ? AND created_at < ?", channelIDs, start.Unix(), end.Unix()).Select("COALESCE(SUM(count),0) AS count, COALESCE(SUM(failed_count),0) AS failed, COALESCE(SUM(token_used),0) AS token, COALESCE(SUM(input_tokens),0) AS input, COALESCE(SUM(cache_read_tokens),0) AS cache_read").Scan(&metric).Error; err != nil {
		return result, err
	}
	result.RequestTotal = metric.Count + metric.Failed
	minutes := max(end.Sub(start).Minutes(), 1)
	result.RPM = float64(result.RequestTotal) / minutes
	result.TPM = float64(metric.Token) / minutes
	if result.RequestTotal > 0 {
		result.SuccessRate = float64(metric.Count) / float64(result.RequestTotal) * 100
	}
	if metric.Input > 0 {
		result.CacheHitRate = float64(metric.CacheRead) / float64(metric.Input) * 100
	}
	var healthRows []struct {
		ChannelID int
		Count     int64
		Failed    int64
	}
	if err := DB.Table("quota_data").Select("channel_id, COALESCE(SUM(count),0) AS count, COALESCE(SUM(failed_count),0) AS failed").Where("channel_id IN ? AND created_at >= ? AND created_at < ?", channelIDs, start.Unix(), end.Unix()).Group("channel_id").Scan(&healthRows).Error; err != nil {
		return result, err
	}
	health := make(map[int]struct{ Count, Failed int64 }, len(healthRows))
	for _, row := range healthRows {
		health[row.ChannelID] = struct{ Count, Failed int64 }{row.Count, row.Failed}
	}
	var channels []Channel
	if err := DB.Select("id, status").Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return result, err
	}
	for _, channel := range channels {
		m := health[channel.Id]
		total := m.Count + m.Failed
		rate := float64(0)
		if total > 0 {
			rate = float64(m.Count) / float64(total) * 100
		}
		result.Channels = append(result.Channels, SupplierMonitoringChannel{ChannelId: channel.Id, Status: supplierChannelHealthStatus(channel.Status, m.Count, m.Failed), SuccessRate: rate, RequestTotal: total})
	}
	bucketSeconds := int64(time.Hour / time.Second)
	if end.Sub(start) > 48*time.Hour {
		bucketSeconds = int64(24 * time.Hour / time.Second)
	}
	var trendRows []struct {
		Bucket int64
		Count  int64
		Failed int64
	}
	_, offset := start.In(time.Local).Zone()
	bucketExpr := fmt.Sprintf("(created_at + %d - ((created_at + %d) %% %d) - %d) AS bucket", offset, offset, bucketSeconds, offset)
	if err := DB.Table("quota_data").Select(bucketExpr+", COALESCE(SUM(count),0) AS count, COALESCE(SUM(failed_count),0) AS failed").Where("channel_id IN ? AND created_at >= ? AND created_at < ?", channelIDs, start.Unix(), end.Unix()).Group("bucket").Order("bucket").Scan(&trendRows).Error; err != nil {
		return result, err
	}
	for _, row := range trendRows {
		total := row.Count + row.Failed
		rate := float64(0)
		if total > 0 {
			rate = float64(row.Count) / float64(total) * 100
		}
		bucket := SupplierMonitoringBucket{Start: row.Bucket, SuccessRate: rate, RequestTotal: total}
		result.SuccessTrend = append(result.SuccessTrend, bucket)
		result.RequestTrend = append(result.RequestTrend, bucket)
	}
	if LOG_DB != nil {
		if expr, ok := supplierLogStatusCodeExpr(); ok {
			var errorRows []struct {
				Code  string
				Count int64
			}
			query := LOG_DB.Table("logs").Where("type = ? AND channel_id IN ? AND created_at >= ? AND created_at < ?", LogTypeError, channelIDs, start.Unix(), end.Unix()).Select(expr + " AS code, COUNT(*) AS count").Group("code").Order("count DESC")
			if err := query.Scan(&errorRows).Error; err != nil {
				return result, fmt.Errorf("query monitoring error codes: %w", err)
			}
			totalErrors := int64(0)
			for _, row := range errorRows {
				totalErrors += row.Count
			}
			for _, row := range errorRows {
				if strings.TrimSpace(row.Code) == "" {
					row.Code = "unknown"
				}
				rate := float64(0)
				if totalErrors > 0 {
					rate = float64(row.Count) / float64(totalErrors) * 100
				}
				result.ErrorCodes = append(result.ErrorCodes, SupplierMonitoringErrorCode{Code: row.Code, Count: row.Count, Rate: rate})
			}
		}
	}
	return result, nil
}

type SupplierBillDetail struct {
	Current     *SupplierBill            `json:"current"`
	Versions    []SupplierBill           `json:"versions"`
	Adjustments []SupplierBillAdjustment `json:"adjustments"`
	Allocations []SupplierBillAllocation `json:"allocations"`
	Operations  []SupplierOperationLog   `json:"operations"`
}

// aggregateAllocationsByChannelModel collapses the stored user+channel+model rows to one row per
// bill+channel+model, summing the amounts. UserId/Username are left zero/empty on the result.
func aggregateAllocationsByChannelModel(rows []SupplierBillAllocation) []SupplierBillAllocation {
	type key struct {
		billID    int
		channelID int
		modelName string
	}
	order := make([]key, 0, len(rows))
	merged := make(map[key]*SupplierBillAllocation, len(rows))
	for _, row := range rows {
		k := key{billID: row.BillId, channelID: row.ChannelId, modelName: row.ModelName}
		existing, ok := merged[k]
		if !ok {
			copied := row
			copied.Id = 0
			copied.UserId = 0
			copied.Username = ""
			merged[k] = &copied
			order = append(order, k)
			continue
		}
		existing.QuotaMicros += row.QuotaMicros
		existing.ShareMicros += row.ShareMicros
		existing.EstimatedMicros += row.EstimatedMicros
		existing.ActualMicros += row.ActualMicros
	}
	result := make([]SupplierBillAllocation, 0, len(order))
	for _, k := range order {
		result = append(result, *merged[k])
	}
	return result
}

func GetSupplierBillDetail(supplierID int, month string) (*SupplierBillDetail, error) {
	if _, err := parseSupplierMonth(month); err != nil {
		return nil, err
	}
	var versions []SupplierBill
	if err := DB.Where("supplier_id = ? AND bill_month = ?", supplierID, month).Order("version DESC").Find(&versions).Error; err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, ErrSupplierBillNotFound
	}
	ids := make([]int, 0, len(versions))
	for _, bill := range versions {
		ids = append(ids, bill.Id)
	}
	var adjustments []SupplierBillAdjustment
	if err := DB.Where("bill_id IN ?", ids).Order("created_at DESC").Find(&adjustments).Error; err != nil {
		return nil, err
	}
	var rawAllocations []SupplierBillAllocation
	if err := DB.Where("bill_id IN ?", ids).Order("channel_id, model_name").Find(&rawAllocations).Error; err != nil {
		return nil, err
	}
	// Allocations are stored per user + channel + model; the bill detail view is per channel + model,
	// so collapse the user dimension here. The finest-grain rows stay available via GetSupplierBillAllocations.
	allocations := aggregateAllocationsByChannelModel(rawAllocations)
	operations, err := GetSupplierOperationLogsForBills(supplierID, ids, 200)
	if err != nil {
		return nil, err
	}
	var current *SupplierBill
	for index := range versions {
		if versions[index].Status == SupplierBillSettled {
			current = &versions[index]
			break
		}
	}
	if current == nil {
		current = &versions[0]
	}
	return &SupplierBillDetail{Current: current, Versions: versions, Adjustments: adjustments, Allocations: allocations, Operations: operations}, nil
}
func GetSupplierOperationLogs(supplierID int, limit int) ([]SupplierOperationLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []SupplierOperationLog
	return rows, DB.Where("supplier_id = ?", supplierID).Order("created_at DESC").Limit(limit).Find(&rows).Error
}

func GetSupplierOperationLogsForBills(supplierID int, billIDs []int, limit int) ([]SupplierOperationLog, error) {
	if len(billIDs) == 0 {
		return []SupplierOperationLog{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []SupplierOperationLog
	err := DB.Where("supplier_id = ? AND bill_id IN ?", supplierID, billIDs).Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
func AddSupplierOperationLog(tx *gorm.DB, supplierID, operatorID int, action, params, result string, billIDs ...int) error {
	name := ""
	if operatorID > 0 {
		name, _ = GetUsernameById(operatorID, false)
	}
	billID := 0
	if len(billIDs) > 0 {
		billID = billIDs[0]
	}
	return tx.Create(&SupplierOperationLog{SupplierId: supplierID, BillId: billID, OperatorId: operatorID, Operator: name, Action: action, Params: params, Result: result, CreatedAt: common.GetTimestamp()}).Error
}
