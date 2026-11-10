package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRelayLogsPreservesIselfEmail(t *testing.T) {
	if common.UsingLogDatabase(common.DatabaseTypeSQLite) {
		var version string
		require.NoError(t, LOG_DB.Raw("SELECT sqlite_version()").Scan(&version).Error)
		t.Logf("SQLite %s", version)
	}
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })
	previousExport, previousConsume := common.DataExportEnabled, common.LogConsumeEnabled
	common.DataExportEnabled, common.LogConsumeEnabled = false, true
	t.Cleanup(func() { common.DataExportEnabled, common.LogConsumeEnabled = previousExport, previousConsume })

	for _, tt := range []struct {
		name    string
		logType int
		email   string
	}{
		{"consume with email and no affinity", LogTypeConsume, "alice@example.com"},
		{"error with email and no affinity", LogTypeError, "alice@example.com"},
		{"consume without email", LogTypeConsume, ""},
		{"error without email", LogTypeError, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("username", "iself")
			c.Set(common.RequestIdKey, tt.name)
			common.SetContextKey(c, constant.ContextKeyIselfEmail, tt.email)
			if tt.logType == LogTypeConsume {
				RecordConsumeLog(c, 0, RecordConsumeLogParams{ModelName: "test-model", Quota: 5, PromptTokens: 2, CompletionTokens: 1})
			} else {
				RecordErrorLog(c, 0, 0, "test-model", "test-token", "upstream failed", 0, 0, false, "default", nil)
			}

			var logs []Log
			require.NoError(t, LOG_DB.Where("request_id = ?", tt.name).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, tt.email, logs[0].IselfEmail)
			assert.Equal(t, tt.logType, logs[0].Type)
		})
	}
}

// A request that asked for "auto" is marked on both the success and the error log,
// since a failed auto request is the one an administrator most wants to find.
func TestRecordRelayLogsMarksAutoRoutedRequests(t *testing.T) {
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })
	previousExport, previousConsume := common.DataExportEnabled, common.LogConsumeEnabled
	common.DataExportEnabled, common.LogConsumeEnabled = false, true
	t.Cleanup(func() { common.DataExportEnabled, common.LogConsumeEnabled = previousExport, previousConsume })

	for _, tt := range []struct {
		name       string
		logType    int
		autoRouted bool
	}{
		{"consume asked for auto", LogTypeConsume, true},
		{"consume asked for the model", LogTypeConsume, false},
		{"error asked for auto", LogTypeError, true},
		{"error asked for the model", LogTypeError, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("username", "auto-user")
			c.Set(common.RequestIdKey, tt.name)
			if tt.autoRouted {
				common.SetContextKey(c, constant.ContextKeyAutoRouted, true)
			}
			if tt.logType == LogTypeConsume {
				RecordConsumeLog(c, 0, RecordConsumeLogParams{ModelName: "resolved-model", Quota: 5, PromptTokens: 2, CompletionTokens: 1})
			} else {
				RecordErrorLog(c, 0, 0, "resolved-model", "test-token", "upstream failed", 0, 0, false, "default", nil)
			}

			var logs []Log
			require.NoError(t, LOG_DB.Where("request_id = ?", tt.name).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, tt.autoRouted, logs[0].AutoRouted)
			assert.Equal(t, "resolved-model", logs[0].ModelName, "the log keeps the model the request resolved to")
		})
	}
}
