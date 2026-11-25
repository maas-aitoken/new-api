package model

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"gorm.io/gorm/utils/tests"
)

func TestIsClickHouseDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want bool
	}{
		{"clickhouse://default:pass@localhost:9000/logs", true},
		{"tcp://localhost:9000/logs", true},
		{"http://localhost:8123/logs", true},
		{"https://localhost:8443/logs", true},
		{"postgres://root:pass@localhost:5432/db", false},
		{"postgresql://root:pass@localhost:5432/db", false},
		{"root:pass@tcp(localhost:3306)/db", false},
		{"local", false},
		{"", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, isClickHouseDSN(c.dsn), "dsn=%q", c.dsn)
	}
}

func TestNormalizeClickHouseDSN(t *testing.T) {
	// https without secure gets secure=true appended
	normalized := normalizeClickHouseDSN("https://default:pass@localhost:8443/logs")
	assert.Contains(t, normalized, "secure=true")
	assert.True(t, strings.HasPrefix(normalized, "https://"))

	// https that already specifies secure is left untouched
	assert.Equal(t,
		"https://localhost:8443/logs?secure=false",
		normalizeClickHouseDSN("https://localhost:8443/logs?secure=false"),
	)

	// non-https schemes are returned verbatim
	assert.Equal(t, "clickhouse://localhost:9000/logs", normalizeClickHouseDSN("clickhouse://localhost:9000/logs"))
	assert.Equal(t, "tcp://localhost:9000/logs", normalizeClickHouseDSN("tcp://localhost:9000/logs"))
}

func TestChooseDBRejectsClickHouseForMainDatabase(t *testing.T) {
	original, had := os.LookupEnv("SQL_DSN")
	t.Cleanup(func() {
		if had {
			require.NoError(t, os.Setenv("SQL_DSN", original))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})
	require.NoError(t, os.Setenv("SQL_DSN", "clickhouse://default:pass@localhost:9000/logs"))

	db, dbType, err := chooseDB("SQL_DSN", false)
	require.Error(t, err)
	assert.Nil(t, db)
	assert.Equal(t, common.DatabaseType(""), dbType)
	assert.Contains(t, err.Error(), "does not support ClickHouse")
}

func TestClickHouseMigrationDoesNotChangeTTL(t *testing.T) {
	previousDB := LOG_DB
	t.Cleanup(func() { LOG_DB = previousDB })

	t.Setenv("LOG_SQL_CLICKHOUSE_TTL_DAYS", "7")
	recorder := &migrationSQLRecorder{}
	db, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true, Logger: recorder})
	require.NoError(t, err)
	LOG_DB = db

	require.NoError(t, migrateClickHouseLogDB())
	statements := recorder.schemaMutations()
	require.NotEmpty(t, statements)
	for _, statement := range statements {
		assert.NotContains(t, strings.ToUpper(statement), "TTL")
	}
	allStatements := strings.Join(statements, "\n")
	assert.Contains(t, allStatements, "ADD COLUMN IF NOT EXISTS fast_mode")
	assert.Contains(t, allStatements, "ADD COLUMN IF NOT EXISTS auto_routed")
	assert.Contains(t, allStatements, "ADD COLUMN IF NOT EXISTS iself_email")
	assert.Contains(t, allStatements, "ADD COLUMN IF NOT EXISTS request_params")
}

func TestClickHouseLogCreateTableSQL(t *testing.T) {
	ddl := clickHouseLogCreateTableSQL()
	assert.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS logs")
	assert.Contains(t, ddl, "ENGINE = MergeTree()")
	assert.Contains(t, ddl, "PARTITION BY toYYYYMM(toDateTime(created_at))")
	assert.Contains(t, ddl, "ORDER BY (created_at, request_id)")
	assert.Contains(t, ddl, "fast_mode UInt8 DEFAULT 0")
	assert.Contains(t, ddl, "auto_routed UInt8 DEFAULT 0")
	assert.NotContains(t, ddl, "TTL ")
}

func TestClickHouseLogOrder(t *testing.T) {
	assert.Equal(t, "created_at desc, request_id desc", clickHouseLogOrder(""))
	assert.Equal(t, "logs.created_at desc, logs.request_id desc", clickHouseLogOrder("logs."))
}

func TestBuildLogLikeConditionUsesStandardEscape(t *testing.T) {
	originalLogDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalLogDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)

	condition, pattern, err := buildLogLikeCondition("logs.model_name", "gpt_4%")

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ? ESCAPE '!'", condition)
	assert.Equal(t, "gpt!_4%", pattern)
}

func TestBuildLogLikeConditionUsesClickHouseEscaping(t *testing.T) {
	originalLogDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalLogDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)

	condition, pattern, err := buildLogLikeCondition("logs.model_name", `gpt_4\mini%`)

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ?", condition)
	assert.Equal(t, `gpt\_4\\mini%`, pattern)
}

func TestEnsureLogRequestId(t *testing.T) {
	empty := &Log{}
	ensureLogRequestId(empty)
	assert.NotEmpty(t, empty.RequestId, "empty request id should be backfilled")

	existing := &Log{RequestId: "fixed-request-id"}
	ensureLogRequestId(existing)
	assert.Equal(t, "fixed-request-id", existing.RequestId, "existing request id must be preserved")

	assert.NotPanics(t, func() { ensureLogRequestId(nil) })
}

// ClickHouse 绕过 AutoMigrate，建表语句靠手工维护；少一列会让整批 INSERT 失败。
func TestClickHouseTelemetryCreateTableSQLCoversEveryColumn(t *testing.T) {
	tests := []struct {
		name   string
		table  string
		entity any
		ddl    func() string
	}{
		{"capability gate events", "capability_gate_events", &CapabilityGateEvent{}, clickHouseCapabilityGateEventCreateTableSQL},
		{"auto route logs", "auto_route_logs", &AutoRouteLog{}, clickHouseAutoRouteLogCreateTableSQL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := schema.Parse(tt.entity, &sync.Map{}, schema.NamingStrategy{})
			require.NoError(t, err)

			ddl := tt.ddl()
			assert.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS "+tt.table)
			assert.NotContains(t, ddl, "TTL ")
			for _, field := range parsed.Fields {
				assert.Regexp(t, "(?m)^\\s*`?"+field.DBName+"`? ", ddl, "column %s is missing from the ClickHouse DDL", field.DBName)
			}
		})
	}
}

func TestAssignDisplayLogIds(t *testing.T) {
	logs := []*Log{{}, {}, {}}

	assignDisplayLogIds(logs, 0)
	assert.Equal(t, []int{1, 2, 3}, []int{logs[0].Id, logs[1].Id, logs[2].Id})

	assignDisplayLogIds(logs, 20)
	assert.Equal(t, []int{21, 22, 23}, []int{logs[0].Id, logs[1].Id, logs[2].Id})

	assert.NotPanics(t, func() { assignDisplayLogIds(nil, 0) })
}
