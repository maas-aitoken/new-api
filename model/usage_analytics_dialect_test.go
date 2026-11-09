package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TEST_MYSQL_DSN / TEST_POSTGRES_DSN must point to disposable databases.
// TEST_*_LOG_DSN optionally selects a separate log database.

func useDialectAnalyticsDB(t *testing.T, mainDB, logDB *gorm.DB, dbType common.DatabaseType) {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, LOG_DB = mainDB, logDB
	common.SetDatabaseTypes(dbType, dbType)
	initCol()
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		initCol()
	})

	require.NoError(t, mainDB.AutoMigrate(&User{}, &Channel{}, &Token{}, &QuotaData{}, &Log{}))
	if logDB != mainDB {
		require.NoError(t, logDB.AutoMigrate(&Log{}))
	}
}

func openDialectAnalyticsDB(t *testing.T, dbType common.DatabaseType, dsn string) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	var err error
	if dbType == common.DatabaseTypePostgreSQL {
		db, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	} else {
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func runUsageAnalyticsDialectSuite(t *testing.T, dbType common.DatabaseType, mainEnv, logEnv string) {
	mainDSN := strings.TrimSpace(os.Getenv(mainEnv))
	if mainDSN == "" {
		t.Skip(mainEnv + " is not configured")
	}
	mainDB := openDialectAnalyticsDB(t, dbType, mainDSN)
	logDB := mainDB
	if logDSN := strings.TrimSpace(os.Getenv(logEnv)); logDSN != "" {
		logDB = openDialectAnalyticsDB(t, dbType, logDSN)
	}
	useDialectAnalyticsDB(t, mainDB, logDB, dbType)

	for name, run := range map[string]func(*testing.T){
		"summary closed range":       TestGetUsageAnalyticsSummaryAggregatesClosedRange,
		"summary filters":            TestGetUsageAnalyticsSummaryAppliesFilters,
		"group by model":             TestGetUsageAnalyticsGroupByModel,
		"group by user":              TestGetUsageAnalyticsGroupByUserJoinsUserAttributes,
		"group by user model":        TestGetUsageAnalyticsGroupByUserModel,
		"organization dimensions":    TestGetUsageAnalyticsOrganizationDimensions,
		"user quota trend":           TestGetUserQuotaTrendByDimension,
		"org options":                TestGetUsageOrgOptionsListsDistinctPaths,
		"user filter matching":       TestMatchUsageUserIdsByNameOrEmail,
		"group by model channel":     TestGetUsageAnalyticsGroupByModelChannelJoinsChannel,
		"peak tpm":                   TestGetPeakTPMTakesHourlyMaxOfMinuteTotals,
		"consume log columns":        TestRecordConsumeLogWritesExtendedQuotaDataColumns,
		"relay log email":            TestRecordRelayLogsPreservesIselfEmail,
		"failure flush increments":   TestLogQuotaDataFinalFailureAccumulatesAcrossFlushes,
		"legacy readers skip failed": TestLegacyQuotaDataReadersSkipFailureOnlyRows,
		"availability":               testModelAvailabilityOnLogDB,
		"model health":               TestQueryModelHealthSumsChannelsPerModel,
		"iself usage":                TestGetIselfUsageAnalytics,
		"iself cache tokens":         TestGetIselfUsageAnalyticsCacheTokens,
		"iself attribution":          TestIselfUsageIsAttributedToTheEmailAccount,
	} {
		t.Run(name, run)
	}
}

func testModelAvailabilityOnLogDB(t *testing.T) {
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })
	streamError := common.MapToJsonStr(map[string]interface{}{"stream_status": map[string]interface{}{"status": "error"}})
	streamOk := common.MapToJsonStr(map[string]interface{}{"stream_status": map[string]interface{}{"status": "ok"}})
	require.NoError(t, LOG_DB.Create(&[]Log{
		{RequestId: "retry", Type: LogTypeError, ModelName: "m-retry", CreatedAt: 1100},
		{RequestId: "retry", Type: LogTypeError, ModelName: "m-retry", CreatedAt: 1101},
		{RequestId: "retry", Type: LogTypeConsume, ModelName: "m-retry", CreatedAt: 1102, PromptTokens: 10, CompletionTokens: 5},
		{RequestId: "fail", Type: LogTypeError, ModelName: "m-fail", CreatedAt: 1200},
		{RequestId: "fail", Type: LogTypeError, ModelName: "m-fail", CreatedAt: 1201},
		{RequestId: "late", Type: LogTypeError, ModelName: "m-fail", CreatedAt: 1990},
		{RequestId: "late", Type: LogTypeConsume, ModelName: "m-fail", CreatedAt: 2040},
		{RequestId: "stream-error", Type: LogTypeConsume, ModelName: "m-stream", CreatedAt: 1300, PromptTokens: 3, CompletionTokens: 4, Other: streamError},
		{RequestId: "stream-ok", Type: LogTypeConsume, ModelName: "m-stream", CreatedAt: 1301, PromptTokens: 1, CompletionTokens: 2, Other: streamOk},
		{RequestId: "legacy", Type: LogTypeConsume, ModelName: "m-stream", CreatedAt: 1302, PromptTokens: 5},
		{RequestId: "not-json", Type: LogTypeConsume, ModelName: "m-stream", CreatedAt: 1303, Other: "not json"},
		{RequestId: "refund", Type: LogTypeRefund, ModelName: "m-stream", CreatedAt: 1500},
	}).Error)

	models, err := queryModelAvailability(1000, 2000)
	require.NoError(t, err)
	assert.Equal(t, []ModelAvailability{
		{ModelName: "m-fail", FailedCount: 1, LastCalledAt: 1201},
		{ModelName: "m-retry", SuccessCount: 1, TotalTokens: 15, LastCalledAt: 1102},
		{ModelName: "m-stream", SuccessCount: 3, StreamErrorCount: 1, TotalTokens: 15, LastCalledAt: 1303},
	}, models)
}

// ClickHouse DELETE is asynchronous; TRUNCATE keeps test cleanup synchronous.
func clearDialectTestLogs(t *testing.T) {
	t.Helper()
	statement := "DELETE FROM logs"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		statement = "TRUNCATE TABLE logs"
	}
	require.NoError(t, LOG_DB.Exec(statement).Error)
}

// ClickHouse covers the log database; the main database remains SQLite.
func TestUsageAnalyticsClickHouseLogDB(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_CLICKHOUSE_LOG_DSN"))
	if dsn == "" {
		t.Skip("TEST_CLICKHOUSE_LOG_DSN is not configured")
	}
	previousLogDB, previousLogType, previousMaster := LOG_DB, common.LogDatabaseType(), common.IsMasterNode
	common.IsMasterNode = true
	require.NoError(t, InitLogDBWithDSN(dsn))
	require.True(t, common.UsingLogDatabase(common.DatabaseTypeClickHouse))
	t.Cleanup(func() {
		if sqlDB, err := LOG_DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousLogType)
		common.IsMasterNode = previousMaster
		initCol()
	})

	for name, run := range map[string]func(*testing.T){
		"peak tpm":           TestGetPeakTPMTakesHourlyMaxOfMinuteTotals,
		"availability":       testModelAvailabilityOnLogDB,
		"model health":       TestQueryModelHealthSumsChannelsPerModel,
		"iself usage":        TestGetIselfUsageAnalytics,
		"iself cache tokens": TestGetIselfUsageAnalyticsCacheTokens,
		"relay log email":    TestRecordRelayLogsPreservesIselfEmail,
		"iself attribution":  TestIselfUsageIsAttributedToTheEmailAccount,
	} {
		t.Run(name, func(t *testing.T) {
			clearDialectTestLogs(t)
			t.Cleanup(func() { clearDialectTestLogs(t) })
			run(t)
		})
	}
}

func TestUsageAnalyticsMySQL(t *testing.T) {
	runUsageAnalyticsDialectSuite(t, common.DatabaseTypeMySQL, "TEST_MYSQL_DSN", "TEST_MYSQL_LOG_DSN")
}

func TestUsageAnalyticsPostgreSQL(t *testing.T) {
	runUsageAnalyticsDialectSuite(t, common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN", "TEST_POSTGRES_LOG_DSN")
}
