package model

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newSupplierSQLite(t *testing.T, name string) func() {
	previousDB, previousLog := DB, LOG_DB
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}))
	return func() { DB, LOG_DB = previousDB, previousLog }
}

func TestSupplierBillVersionAndAdjustmentRules(t *testing.T) {
	previousDB := DB
	t.Cleanup(func() { DB = previousDB })
	db, err := gorm.Open(sqlite.Open("file:supplier-billing-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}))
	require.NoError(t, DB.AutoMigrate(&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}))
	require.True(t, DB.Migrator().HasTable(&QuotaData{}))

	supplier := &Supplier{Name: "Azure Procurement", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(supplier))
	require.NoError(t, DB.Create(&Channel{Id: 10, Name: "fixture-channel", Models: "gpt", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, DB.Create(&SupplierChannel{SupplierId: supplier.Id, ChannelId: 10}).Error)
	require.NoError(t, DB.Create(&QuotaData{ChannelID: 10, ModelName: "gpt", Quota: 500000, Count: 1, CreatedAt: mustSupplierMonthTimestamp("2026-11")}).Error)
	settlementRows, err := GetSupplierSettlementRows("2026-11")
	require.NoError(t, err)
	require.Len(t, settlementRows, 1)
	require.Equal(t, int64(7_300_000), settlementRows[0].EstimatedMicros)

	draft := &SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 100_000_000, RateMicros: supplierMicros}
	saved, err := SaveSupplierBillDraft(*draft, 0, false)
	require.NoError(t, err)
	require.Equal(t, SupplierBillDraft, saved.Status)
	require.NoError(t, DB.Model(&QuotaData{}).Where("channel_id = ?", 10).Updates(map[string]any{"quota": 1_000_000}).Error)
	draftRows, err := GetSupplierSettlementRows("2026-11")
	require.NoError(t, err)
	require.Equal(t, int64(14_600_000), draftRows[0].EstimatedMicros)
	require.NoError(t, AddSupplierBillAdjustment(supplier.Id, saved.Id, SupplierBillAdjustment{FeeMonth: "2026-10", Type: SupplierAdjustmentCharge, Currency: "CNY", AmountMicros: 1_000_000, RateMicros: supplierMicros, Reason: "late invoice"}))
	require.ErrorIs(t, AddSupplierBillAdjustment(supplier.Id, saved.Id, SupplierBillAdjustment{FeeMonth: "2026-11", Type: SupplierAdjustmentCharge, Currency: "CNY", AmountMicros: 1_000_000, RateMicros: supplierMicros, Reason: "invalid month"}), ErrSupplierFeeMonth)
	require.NoError(t, ConfirmSupplierBill(supplier.Id, saved.Id, 1, 1))
	require.NoError(t, DB.Model(&QuotaData{}).Where("channel_id = ?", 10).Updates(map[string]any{"quota": 2_000_000}).Error)
	settledRows, err := GetSupplierSettlementRows("2026-11")
	require.NoError(t, err)
	require.Equal(t, saved.Id, settledRows[0].BillId)
	require.Equal(t, int64(14_600_000), settledRows[0].EstimatedMicros)
	var firstAllocation SupplierBillAllocation
	require.NoError(t, DB.Where("bill_id = ?", saved.Id).First(&firstAllocation).Error)
	require.NotEqual(t, firstAllocation.EstimatedMicros, firstAllocation.ActualMicros)
	require.ErrorIs(t, AddSupplierBillAdjustment(supplier.Id, saved.Id, SupplierBillAdjustment{FeeMonth: "2026-10", Type: SupplierAdjustmentCharge, Currency: "CNY", AmountMicros: 1_000_000, RateMicros: supplierMicros, Reason: "locked"}), ErrSupplierBillSettled)
	_, err = SaveSupplierBillDraft(SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 120_000_000, RateMicros: supplierMicros}, 1, false)
	require.ErrorIs(t, err, ErrSupplierBillSettled)
	revised, err := SaveSupplierBillDraft(SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 120_000_000, RateMicros: supplierMicros}, 1, true)
	require.NoError(t, err)
	require.Equal(t, 2, revised.Version)
	revisionRows, err := GetSupplierSettlementRows("2026-11")
	require.NoError(t, err)
	require.Equal(t, saved.Id, revisionRows[0].BillId)
	require.Equal(t, int64(100_000_000), revisionRows[0].AmountMicros)
	require.Equal(t, revised.Id, revisionRows[0].RevisionDraftId)
	require.NoError(t, ConfirmSupplierBill(supplier.Id, revised.Id, revised.Version, 1))
	var old SupplierBill
	require.NoError(t, DB.First(&old, saved.Id).Error)
	require.Equal(t, SupplierBillSuperseded, old.Status)
	var allocationTotal int64
	require.NoError(t, DB.Model(&SupplierBillAllocation{}).Where("bill_id = ?", revised.Id).Select("COALESCE(SUM(actual_micros), 0)").Scan(&allocationTotal).Error)
	require.Equal(t, revised.CNYAmountMicros, allocationTotal)
}

func mustSupplierMonthTimestamp(month string) int64 {
	parsed, _ := parseSupplierMonth(month)
	return parsed.AddDate(0, 0, 1).Unix()
}

// A difference for an earlier month is entered on a later month's bill, but is queryable under the
// month it corrects, billed to the configured account with a "调整{month}月差值" remark. Charges and
// credits for the same supplier net out.
func TestSupplierCostDiffRowsByFeeMonth(t *testing.T) {
	restore := newSupplierSQLite(t, "supplier-cost-diff")
	defer restore()
	t.Setenv("SUPPLIER_COST_DIFF_ACCOUNT", "finance-adjustments")

	supplier := &Supplier{Name: "Diff Supplier", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(supplier))
	require.NoError(t, DB.Create(&Channel{Id: 30, Name: "diff-channel", Models: "gpt", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, DB.Create(&SupplierChannel{SupplierId: supplier.Id, ChannelId: 30}).Error)

	// November bill carries two corrections to October: +12 charge and -2 credit -> net +10.
	bill, err := SaveSupplierBillDraft(SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 50_000_000, RateMicros: supplierMicros}, 0, false)
	require.NoError(t, err)
	require.NoError(t, AddSupplierBillAdjustment(supplier.Id, bill.Id, SupplierBillAdjustment{FeeMonth: "2026-10", Type: SupplierAdjustmentCharge, Currency: "CNY", AmountMicros: 12_000_000, RateMicros: supplierMicros, Reason: "underbilled"}))
	require.NoError(t, AddSupplierBillAdjustment(supplier.Id, bill.Id, SupplierBillAdjustment{FeeMonth: "2026-10", Type: SupplierAdjustmentCredit, Currency: "CNY", AmountMicros: 2_000_000, RateMicros: supplierMicros, Reason: "refund"}))

	rows, err := GetSupplierCostDiffRows("2026-10")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, supplier.Id, rows[0].SupplierId)
	require.Equal(t, "Diff Supplier", rows[0].SupplierName)
	require.Equal(t, "finance-adjustments", rows[0].Account)
	require.Equal(t, "2026-10", rows[0].FeeMonth)
	require.Equal(t, int64(10_000_000), rows[0].AmountMicros)
	require.Equal(t, "调整2026-10月差值", rows[0].Remark)

	// A month with no corrections has no rows.
	empty, err := GetSupplierCostDiffRows("2026-09")
	require.NoError(t, err)
	require.Empty(t, empty)

	// With no account configured, differences are not broken out at all.
	t.Setenv("SUPPLIER_COST_DIFF_ACCOUNT", "")
	none, err := GetSupplierCostDiffRows("2026-10")
	require.NoError(t, err)
	require.Empty(t, none)
}

// Settlement allocates the entered total down to user + channel + model: two users on the same
// channel and model each get their own row, split by quota share, and the rows still sum to the bill.
func TestSupplierAllocationSplitsByUser(t *testing.T) {
	restore := newSupplierSQLite(t, "supplier-allocation-by-user")
	defer restore()

	supplier := &Supplier{Name: "User Split Supplier", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(supplier))
	require.NoError(t, DB.Create(&Channel{Id: 20, Name: "shared-channel", Models: "gpt", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, DB.Create(&SupplierChannel{SupplierId: supplier.Id, ChannelId: 20}).Error)
	// Same channel + model, two users, quota 3:1.
	ts := mustSupplierMonthTimestamp("2026-11")
	require.NoError(t, DB.Create(&QuotaData{UserID: 101, Username: "alice", ChannelID: 20, ModelName: "gpt", Quota: 3_000_000, Count: 1, CreatedAt: ts}).Error)
	require.NoError(t, DB.Create(&QuotaData{UserID: 102, Username: "bob", ChannelID: 20, ModelName: "gpt", Quota: 1_000_000, Count: 1, CreatedAt: ts}).Error)

	bill, err := SaveSupplierBillDraft(SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 100_000_000, RateMicros: supplierMicros}, 0, false)
	require.NoError(t, err)
	require.NoError(t, ConfirmSupplierBill(supplier.Id, bill.Id, bill.Version, 1))

	allocations, err := GetSupplierBillAllocations(bill.Id)
	require.NoError(t, err)
	require.Len(t, allocations, 2)

	byUser := map[int]SupplierBillAllocation{}
	var total int64
	for _, a := range allocations {
		require.Equal(t, 20, a.ChannelId)
		require.Equal(t, "gpt", a.ModelName)
		byUser[a.UserId] = a
		total += a.ActualMicros
	}
	require.Equal(t, bill.CNYAmountMicros, total)
	require.Equal(t, "alice", byUser[101].Username)
	require.Equal(t, "bob", byUser[102].Username)
	// 3:1 quota share of 100 CNY -> 75 / 25.
	require.Equal(t, int64(75_000_000), byUser[101].ActualMicros)
	require.Equal(t, int64(25_000_000), byUser[102].ActualMicros)

	// The bill detail view collapses the user dimension back to one channel + model row.
	detail, err := GetSupplierBillDetail(supplier.Id, "2026-11")
	require.NoError(t, err)
	require.Len(t, detail.Allocations, 1)
	require.Equal(t, 20, detail.Allocations[0].ChannelId)
	require.Equal(t, "gpt", detail.Allocations[0].ModelName)
	require.Equal(t, 0, detail.Allocations[0].UserId)
	require.Equal(t, int64(100_000_000), detail.Allocations[0].ActualMicros)
	require.Equal(t, int64(4_000_000), detail.Allocations[0].QuotaMicros)
}

func TestSupplierDatabaseMatrix(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		open func(string) gorm.Dialector
	}{
		{name: "mysql", dsn: os.Getenv("TEST_MYSQL_DSN"), open: mysql.Open},
		{name: "postgresql", dsn: os.Getenv("TEST_POSTGRES_DSN"), open: func(dsn string) gorm.Dialector { return postgres.Open(dsn) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dsn == "" {
				t.Skipf("%s DSN not configured", tc.name)
			}
			db, err := gorm.Open(tc.open(tc.dsn), &gorm.Config{})
			require.NoError(t, err)
			previousDB, previousLog, previousLogType := DB, LOG_DB, common.LogDatabaseType()
			DB, LOG_DB = db, db
			if tc.name == "mysql" {
				common.SetLogDatabaseType(common.DatabaseTypeMySQL)
			} else {
				common.SetLogDatabaseType(common.DatabaseTypePostgreSQL)
			}
			t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog; common.SetLogDatabaseType(previousLogType) })
			models := []any{&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}, &Log{}}
			require.NoError(t, DB.AutoMigrate(models...))
			require.NoError(t, DB.AutoMigrate(models...))
			runSuffix := strconv.FormatInt(time.Now().UnixNano(), 10)
			supplierName := "Matrix Supplier " + runSuffix
			supplier := Supplier{Name: supplierName, NameKey: strings.ToLower(supplierName), Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY", DefaultRateMicros: supplierMicros, Enabled: true, ParticipatesInSettlement: true}
			require.NoError(t, DB.Create(&supplier).Error)
			textRows, _, err := ListSuppliers("Matrix Supplier "+runSuffix, "", nil, 1, 20)
			require.NoError(t, err)
			textIDs := make([]int, len(textRows))
			for index := range textRows {
				textIDs[index] = textRows[index].Id
			}
			require.Contains(t, textIDs, supplier.Id)
			numericRows, _, err := ListSuppliers(strconv.Itoa(supplier.Id), "", nil, 1, 20)
			require.NoError(t, err)
			numericIDs := make([]int, len(numericRows))
			for index := range numericRows {
				numericIDs[index] = numericRows[index].Id
			}
			require.Contains(t, numericIDs, supplier.Id)
			channelID := int(700000000 + time.Now().UnixNano()%100000000)
			conflictChannelID := channelID + 1
			require.NoError(t, DB.Create(&SupplierChannel{SupplierId: supplier.Id, ChannelId: channelID}).Error)
			require.NoError(t, DB.Create(&Channel{Id: channelID, Name: "matrix-channel-" + runSuffix, Models: "matrix-model", Status: common.ChannelStatusEnabled}).Error)
			otherName := "Matrix Other " + runSuffix
			other := Supplier{Name: otherName, NameKey: strings.ToLower(otherName), Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY", DefaultRateMicros: supplierMicros, Enabled: true, ParticipatesInSettlement: true}
			require.NoError(t, DB.Create(&other).Error)
			require.NoError(t, DB.Create(&Channel{Id: conflictChannelID, Name: "matrix-conflict-" + runSuffix, Models: "matrix-model", Status: common.ChannelStatusEnabled}).Error)
			require.NoError(t, DB.Create(&SupplierChannel{SupplierId: other.Id, ChannelId: conflictChannelID}).Error)
			require.ErrorIs(t, AddSupplierChannels(supplier.Id, []int{conflictChannelID}), ErrSupplierChannelConflict)
			require.NoError(t, AddSupplierChannels(supplier.Id, []int{channelID}))
			var operationCount int64
			require.NoError(t, DB.Model(&SupplierOperationLog{}).Where("supplier_id = ? AND action = ?", supplier.Id, "channels.add").Count(&operationCount).Error)
			require.GreaterOrEqual(t, operationCount, int64(1))
			require.NoError(t, DB.Create(&QuotaData{UserID: 201, Username: "matrix-alice", ChannelID: channelID, ModelName: "matrix-model", Quota: 500000, Count: 1, CreatedAt: mustSupplierMonthTimestamp("2026-11")}).Error)
			require.NoError(t, DB.Create(&QuotaData{UserID: 202, Username: "matrix-bob", ChannelID: channelID, ModelName: "matrix-model", Quota: 1500000, Count: 1, CreatedAt: mustSupplierMonthTimestamp("2026-11")}).Error)
			now := time.Now().Unix()
			require.NoError(t, DB.Create(&Log{CreatedAt: now, Type: LogTypeError, ChannelId: channelID, Other: `{"status_code":429}`}).Error)
			require.NoError(t, DB.Create(&Log{CreatedAt: now, Type: LogTypeError, ChannelId: channelID, Other: `{"error_code":"E_UPSTREAM"}`}).Error)
			monitoring, err := QuerySupplierMonitoring([]int{channelID}, "1h", time.Unix(now-3600, 0), time.Unix(now+1, 0))
			require.NoError(t, err)
			codes := map[string]int64{}
			for _, item := range monitoring.ErrorCodes {
				codes[item.Code] = item.Count
			}
			require.Equal(t, int64(1), codes["429"])
			require.Equal(t, int64(1), codes["E_UPSTREAM"])
			bill, err := SaveSupplierBillDraft(SupplierBill{SupplierId: supplier.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 5_000_000, RateMicros: supplierMicros}, 0, false)
			require.NoError(t, err)
			require.NoError(t, ConfirmSupplierBill(supplier.Id, bill.Id, bill.Version, 1))
			// Two users on one channel + model: both rows stored, summing to the bill, collapsed to one in detail.
			var allocationCount int64
			require.NoError(t, DB.Model(&SupplierBillAllocation{}).Where("bill_id = ?", bill.Id).Count(&allocationCount).Error)
			require.Equal(t, int64(2), allocationCount)
			var allocationTotal int64
			require.NoError(t, DB.Model(&SupplierBillAllocation{}).Where("bill_id = ?", bill.Id).Select("COALESCE(SUM(actual_micros), 0)").Scan(&allocationTotal).Error)
			require.Equal(t, bill.CNYAmountMicros, allocationTotal)
			detail, err := GetSupplierBillDetail(supplier.Id, "2026-11")
			require.NoError(t, err)
			require.Len(t, detail.Allocations, 1)
			require.Equal(t, bill.CNYAmountMicros, detail.Allocations[0].ActualMicros)
		})
	}
}

func TestSupplierReviewRegressionScenarios(t *testing.T) {
	restore := newSupplierSQLite(t, "supplier-review-regressions")
	defer restore()

	first := &Supplier{Name: "Rebuild Me", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(first))
	require.NoError(t, DeleteSupplier(first.Id))
	rebuilt := &Supplier{Name: "rebuild me", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(rebuilt))

	channel := &Channel{Id: 501, Name: "review-channel", Models: "same,same,other", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, AddSupplierChannels(rebuilt.Id, []int{channel.Id}))
	rows, _, err := ListSuppliers("rebuild", "", nil, 1, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(1), rows[0].ChannelCount)
	require.Equal(t, int64(2), rows[0].ModelCount)
	require.Equal(t, "no_data", rows[0].ServiceStatus)
	numericName := &Supplier{Name: "360 Cloud", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(numericName))
	numericNameRows, _, err := ListSuppliers("360", "", nil, 1, 10)
	require.NoError(t, err)
	numericNameIDs := make([]int, len(numericNameRows))
	for index := range numericNameRows {
		numericNameIDs[index] = numericNameRows[index].Id
	}
	require.Contains(t, numericNameIDs, numericName.Id)

	draft, err := SaveSupplierBillDraft(SupplierBill{SupplierId: rebuilt.Id, BillMonth: "2026-11", Currency: "USD", AmountMicros: 1_000_000}, 0, false)
	require.Error(t, err)
	require.Nil(t, draft)
	_, err = SaveSupplierBillDraft(SupplierBill{SupplierId: rebuilt.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 1_000_000, RateMicros: supplierMicros}, 0, false)
	require.NoError(t, err)

	orphan := &Supplier{Name: "Orphan Supplier", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(orphan))
	require.NoError(t, DB.Create(&SupplierChannel{SupplierId: orphan.Id, ChannelId: 9999}).Error)
	require.NoError(t, DeleteSupplier(orphan.Id))

	other := &Supplier{Name: "Other Supplier", Type: SupplierTypeThirdParty, SettlementMethod: SupplierSettlementMonthly, Currency: "CNY"}
	require.NoError(t, CreateSupplier(other))
	require.ErrorIs(t, AddSupplierChannels(other.Id, []int{channel.Id}), ErrSupplierChannelConflict)

	deleted := &Channel{Id: 502, Name: "deleted-before-settle", Models: "gone", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(deleted).Error)
	require.NoError(t, AddSupplierChannels(rebuilt.Id, []int{deleted.Id}))
	require.NoError(t, DB.Unscoped().Delete(deleted).Error)
	require.NoError(t, DB.Create(&QuotaData{ChannelID: deleted.Id, ModelName: "gone", Quota: 500000, CreatedAt: mustSupplierMonthTimestamp("2026-11")}).Error)
	bill, err := SaveSupplierBillDraft(SupplierBill{SupplierId: rebuilt.Id, BillMonth: "2026-11", Currency: "CNY", AmountMicros: 10_000_000, RateMicros: supplierMicros}, 1, false)
	require.NoError(t, err)
	require.NoError(t, ConfirmSupplierBill(rebuilt.Id, bill.Id, bill.Version, 1))
	var allocation SupplierBillAllocation
	require.NoError(t, DB.Where("bill_id = ?", bill.Id).First(&allocation).Error)
	require.Equal(t, "#502", allocation.ChannelName)
	require.Equal(t, "partial", SupplierServiceStatusFromChannels([]string{"normal", "partial"}))
	require.Equal(t, "unavailable", SupplierServiceStatusFromChannels([]string{"normal", "unavailable"}))
	require.Equal(t, "no_data", SupplierServiceStatusFromChannels([]string{"no_data"}))
	require.Equal(t, time.Now().In(time.Local).AddDate(0, -1, 0).Format("2006-01"), DefaultSupplierBillMonth())
}

func TestSupplierMonitoringClickHouseErrorCodes(t *testing.T) {
	dsn := os.Getenv("TEST_CLICKHOUSE_LOG_DSN")
	if dsn == "" {
		t.Skip("TEST_CLICKHOUSE_LOG_DSN not configured")
	}
	previousDB, previousLog, previousType := DB, LOG_DB, common.LogDatabaseType()
	defer func() { DB, LOG_DB = previousDB, previousLog; common.SetLogDatabaseType(previousType) }()
	db, err := gorm.Open(sqlite.Open("file:supplier-ch-monitor?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}))
	logDB, err := gorm.Open(clickhouse.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	now := time.Now().Unix()
	channelID := int(now%1000000000 + 700000000)
	defer func() { _ = LOG_DB.Exec("ALTER TABLE logs DELETE WHERE channel_id = ?", channelID).Error }()
	require.NoError(t, LOG_DB.Exec("INSERT INTO logs (created_at,type,channel_id,other) VALUES (?,?,?,?)", now, LogTypeError, channelID, `{"status_code":429}`).Error)
	require.NoError(t, LOG_DB.Exec("INSERT INTO logs (created_at,type,channel_id,other) VALUES (?,?,?,?)", now, LogTypeError, channelID, `{"error_code":"E_UPSTREAM"}`).Error)
	require.NoError(t, DB.Create(&Channel{Id: channelID, Name: "clickhouse-monitor", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, DB.Create(&QuotaData{ChannelID: channelID, Count: 2, CreatedAt: now}).Error)
	result, err := QuerySupplierMonitoring([]int{channelID}, "1h", time.Unix(now-3600, 0), time.Unix(now+1, 0))
	require.NoError(t, err)
	require.Equal(t, int64(2), result.RequestTotal)
	require.Len(t, result.ErrorCodes, 2)
	codes := map[string]int64{}
	for _, item := range result.ErrorCodes {
		codes[item.Code] = item.Count
	}
	require.Equal(t, int64(1), codes["429"])
	require.Equal(t, int64(1), codes["E_UPSTREAM"])
}

func TestSupplierMonitoringSQLiteErrorCodes(t *testing.T) {
	previousDB, previousLog, previousType := DB, LOG_DB, common.LogDatabaseType()
	defer func() { DB, LOG_DB = previousDB, previousLog; common.SetLogDatabaseType(previousType) }()
	db, err := gorm.Open(sqlite.Open("file:supplier-sqlite-monitor?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, DB.AutoMigrate(&Supplier{}, &SupplierChannel{}, &SupplierBill{}, &SupplierBillAdjustment{}, &SupplierBillAllocation{}, &SupplierOperationLog{}, &QuotaData{}, &Channel{}))
	logDB, err := gorm.Open(sqlite.Open("file:supplier-sqlite-monitor-log?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	now := time.Now().Unix()
	require.NoError(t, LOG_DB.Create(&Log{CreatedAt: now, Type: LogTypeError, ChannelId: 800, Other: `{"status_code":429}`}).Error)
	require.NoError(t, LOG_DB.Create(&Log{CreatedAt: now, Type: LogTypeError, ChannelId: 800, Other: `{"error_code":"E_UPSTREAM"}`}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 800, Name: "sqlite-monitor", Status: common.ChannelStatusEnabled}).Error)
	result, err := QuerySupplierMonitoring([]int{800}, "1h", time.Unix(now-3600, 0), time.Unix(now+1, 0))
	require.NoError(t, err)
	codes := map[string]int64{}
	for _, item := range result.ErrorCodes {
		codes[item.Code] = item.Count
	}
	require.Equal(t, int64(1), codes["429"])
	require.Equal(t, int64(1), codes["E_UPSTREAM"])
}
