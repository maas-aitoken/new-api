package model

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// TEST_CLICKHOUSE_LOG_DSN must point to a disposable database.
func TestAuditLogClickHouseMigrationAndReadback(t *testing.T) {
	dsn := os.Getenv("TEST_CLICKHOUSE_LOG_DSN")
	if dsn == "" {
		t.Skip("TEST_CLICKHOUSE_LOG_DSN is not configured")
	}
	db, err := gorm.Open(clickhouse.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	previousDB, previousType := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	t.Cleanup(func() {
		LOG_DB = previousDB
		common.SetLogDatabaseType(previousType)
		assert.NoError(t, pool.Close())
	})
	var version string
	require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
	t.Logf("ClickHouse %s", version)

	for _, columnType := range []string{"fresh", "String", "JSON"} {
		t.Run(columnType, func(t *testing.T) {
			require.NoError(t, db.Exec("DROP TABLE IF EXISTS audit_logs").Error)
			t.Cleanup(func() { assert.NoError(t, db.Exec("DROP TABLE IF EXISTS audit_logs").Error) })
			if columnType != "fresh" {
				setupDB := db.WithContext(ch.Context(context.Background(), ch.WithSettings(ch.Settings{
					"allow_experimental_json_type": 1,
				})))
				require.NoError(t, setupDB.Exec(`CREATE TABLE audit_logs (
					id Int64 DEFAULT 0, event_id String, user_id Int64, username String, actor_role Int32,
					created_at Int64, category String, action String, token_ref String,
					auth_method String, ip String, user_agent String, method String, route String,
					status Int32, success UInt8, request_id String, content String, other `+columnType+`
				) ENGINE = MergeTree() PARTITION BY toYYYYMM(toDateTime(created_at)) ORDER BY (created_at, event_id)`).Error)
			} else {
				LOG_DB = db.WithContext(ch.Context(context.Background(), ch.WithSettings(ch.Settings{
					"allow_experimental_json_type": 0,
				})))
				t.Cleanup(func() { LOG_DB = db })
				require.NoError(t, migrateLOGDB())
			}
			entry := AuditLog{
				EventId: "audit-roundtrip", UserId: 1, Username: "audit-user", ActorRole: common.RoleCommonUser,
				CreatedAt: 1770000000, Category: AuditCategoryOperation, Action: "update", Success: true,
				RequestId: "audit-request", Other: AuditOther{
					Op:        &AuditOperation{Action: "update", Params: AuditFields{"large_id": int64(9007199254740993)}},
					AdminInfo: &AuditAdminInfo{AdminID: 2, AdminUsername: "admin", AdminRole: common.RoleAdminUser},
					RootInfo:  AuditFields{"internal": "private"},
				},
			}
			if columnType == "JSON" {
				encoded, err := common.Marshal(entry.Other)
				require.NoError(t, err)
				seedDB := db.WithContext(ch.Context(context.Background(), ch.WithSettings(ch.Settings{"allow_experimental_json_type": 1})))
				require.NoError(t, seedDB.Exec(`INSERT INTO audit_logs (event_id, user_id, username, actor_role, created_at, success, request_id, other)
					SELECT ?, ?, ?, ?, ?, ?, ?, CAST(? AS JSON)`, entry.EventId, entry.UserId, entry.Username, entry.ActorRole, entry.CreatedAt, entry.Success, entry.RequestId, string(encoded)).Error)
			} else {
				entry.Other.AuditInfo = &AuditRequestInfo{Method: "POST", Route: "/test", Path: "/test", Status: 200, Success: true}
				RecordAuditLog(nil, entry)
			}
			for range 2 {
				require.NoError(t, migrateLOGDB())
			}
			var storedType string
			require.NoError(t, db.Raw("SELECT type FROM system.columns WHERE database = currentDatabase() AND table = 'audit_logs' AND name = 'other'").Scan(&storedType).Error)
			expectedType := columnType
			if expectedType == "fresh" {
				expectedType = "String"
			}
			assert.Equal(t, expectedType, storedType)
			logs, total, err := GetAuditLogs(AuditLogFilter{RequestId: entry.RequestId}, 0, 10, common.RoleRootUser)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, logs, 1)
			assert.Equal(t, entry.EventId, logs[0].EventId)
			assert.True(t, logs[0].Success)
			expected, err := common.Marshal(entry.Other)
			require.NoError(t, err)
			actual, err := common.Marshal(logs[0].Other)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(actual))
			assert.Contains(t, string(actual), "9007199254740993")
			logs, _, err = GetAuditLogs(AuditLogFilter{SelfView: true, UserId: 1}, 0, 10, common.RoleCommonUser)
			require.NoError(t, err)
			require.Len(t, logs, 1)
			assert.Nil(t, logs[0].Other.AdminInfo)
			assert.Nil(t, logs[0].Other.RootInfo)
		})
	}
}

// TestAuditLogSortIndexDatabaseMatrix verifies audit ordering and index migrations across supported databases.
func TestAuditLogSortIndexDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "audit_sort_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousType := common.LogDatabaseType()
			common.SetLogDatabaseType(common.DatabaseType(dialect))
			t.Cleanup(func() {
				common.SetLogDatabaseType(previousType)
				assert.NoError(t, sqlDB.Close())
			})
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%v", upgrade), func(t *testing.T) {
					db := db
					if dialect == "postgres" {
						db = db.Begin()
						require.NoError(t, db.Error)
						t.Cleanup(func() { assert.NoError(t, db.Rollback().Error) })
						schemaName := fmt.Sprintf("audit_sort_test_%d", time.Now().UnixNano())
						require.NoError(t, db.Exec("CREATE SCHEMA ?", clause.Table{Name: schemaName}).Error)
						require.NoError(t, db.Exec("SET LOCAL search_path TO ?", clause.Table{Name: schemaName}).Error)
					} else {
						t.Cleanup(func() { assert.NoError(t, db.Migrator().DropTable(&AuditLog{})) })
					}
					previousLogDB := LOG_DB
					LOG_DB = db
					t.Cleanup(func() { LOG_DB = previousLogDB })
					require.NoError(t, MigrateAuditLogs())
					if upgrade {
						require.NoError(t, db.Migrator().DropIndex(&AuditLog{}, "idx_audit_time_event"))
					}
					entries := []AuditLog{
						{EventId: "event-older", CreatedAt: 100, UserId: 1, ActorRole: common.RoleCommonUser},
						{EventId: "event-a", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
						{EventId: "event-z", CreatedAt: 200, UserId: 1, ActorRole: common.RoleCommonUser},
					}
					require.NoError(t, db.Create(&entries).Error)
					originalIndexes, err := db.Migrator().GetIndexes(&AuditLog{})
					require.NoError(t, err)
					for range 2 {
						require.NoError(t, MigrateAuditLogs())
					}
					indexes, err := db.Migrator().GetIndexes(&AuditLog{})
					require.NoError(t, err)
					byName := make(map[string]gorm.Index, len(indexes))
					for _, index := range indexes {
						byName[index.Name()] = index
					}
					index, ok := byName["idx_audit_time_event"]
					require.True(t, ok, "migration must create the audit ordering index")
					var columns []string
					if dialect == "postgres" {
						// GORM's PostgreSQL GetIndexes does not preserve index key order.
						require.NoError(t, db.Raw(`
SELECT attribute.attname
FROM pg_catalog.pg_index AS index_meta
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS index_column(attnum, ordinal_position)
JOIN pg_catalog.pg_attribute AS attribute
  ON attribute.attrelid = index_meta.indrelid AND attribute.attnum = index_column.attnum
WHERE index_meta.indexrelid = to_regclass(?)
ORDER BY index_column.ordinal_position
`, index.Name()).Scan(&columns).Error)
					} else {
						columns = index.Columns()
					}
					assert.Equal(t, []string{"created_at", "event_id"}, columns)
					for _, original := range originalIndexes {
						preserved, ok := byName[original.Name()]
						require.True(t, ok, "existing index %s must remain", original.Name())
						assert.Equal(t, original.Columns(), preserved.Columns(), original.Name())
						originalUnique, originalKnown := original.Unique()
						preservedUnique, preservedKnown := preserved.Unique()
						assert.Equal(t, originalKnown, preservedKnown, original.Name())
						assert.Equal(t, originalUnique, preservedUnique, original.Name())
					}
					var stored []AuditLog
					require.NoError(t, db.Order("id").Find(&stored).Error)
					assert.Equal(t, entries, stored, "migration must preserve existing audit records")
					logs, total, err := GetAuditLogs(AuditLogFilter{}, 0, 2, common.RoleRootUser)
					require.NoError(t, err)
					assert.EqualValues(t, 3, total)
					require.Len(t, logs, 2)
					assert.Equal(t, []string{"event-z", "event-a"}, []string{logs[0].EventId, logs[1].EventId})
					logs, total, err = GetAuditLogs(AuditLogFilter{}, 2, 2, common.RoleRootUser)
					require.NoError(t, err)
					assert.EqualValues(t, 3, total)
					require.Len(t, logs, 1)
					assert.Equal(t, "event-older", logs[0].EventId)
					assert.Error(t, db.Create(&AuditLog{EventId: "event-a"}).Error, "event IDs must remain unique")
				})
			}
		})
	}
}
