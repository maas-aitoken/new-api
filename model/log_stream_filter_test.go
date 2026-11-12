/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useSQLiteLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))

	previousDB, previousLogDB := DB, LOG_DB
	previousLogDatabaseType := common.LogDatabaseType()
	DB, LOG_DB = db, db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetLogDatabaseType(previousLogDatabaseType)
	})
	return db
}

func TestGetAllLogsStreamErrorFilter(t *testing.T) {
	db := useSQLiteLogTestDB(t)
	streamError := common.MapToJsonStr(map[string]interface{}{
		"stream_status": map[string]interface{}{"status": "error"},
	})
	streamOk := common.MapToJsonStr(map[string]interface{}{
		"stream_status": map[string]interface{}{"status": "ok"},
	})
	require.NoError(t, db.Create(&[]Log{
		{Type: LogTypeConsume, Other: streamError},
		{Type: LogTypeConsume, Other: streamOk},
		{Type: LogTypeConsume, Other: ""},
	}).Error)

	logs, total, err := GetAllLogs(
		LogFilter{StreamError: true}, 0, 20,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, streamError, logs[0].Other)
}

func TestGetAllLogsRetryFilter(t *testing.T) {
	db := useSQLiteLogTestDB(t)
	retryOther := common.MapToJsonStr(map[string]interface{}{
		"admin_info": map[string]interface{}{
			"use_channel": []string{"1", "2"},
		},
	})
	singleChannelOther := common.MapToJsonStr(map[string]interface{}{
		"admin_info": map[string]interface{}{
			"use_channel": []string{"1"},
		},
	})
	require.NoError(t, db.Create(&[]Log{
		{Type: LogTypeConsume, Other: retryOther},
		{Type: LogTypeConsume, Other: singleChannelOther},
		{Type: LogTypeConsume, Other: ""},
	}).Error)

	logs, total, err := GetAllLogs(
		LogFilter{Retry: true}, 0, 20,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, retryOther, logs[0].Other)
}

// Rows written before the auto_routed column existed hold NULL. They did not ask for "auto",
// so "not auto" has to keep them, and the stats must agree with the list.
func TestAutoRoutedFilterKeepsRowsFromBeforeTheColumn(t *testing.T) {
	db := useSQLiteLogTestDB(t)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]Log{
		{UserId: 7, CreatedAt: now, Type: LogTypeConsume, ModelName: "routed", Quota: 10, AutoRouted: true},
		{UserId: 7, CreatedAt: now, Type: LogTypeConsume, ModelName: "direct", Quota: 20},
		{UserId: 7, CreatedAt: now, Type: LogTypeConsume, ModelName: "legacy", Quota: 40},
	}).Error)
	require.NoError(t, db.Exec("UPDATE logs SET auto_routed = NULL WHERE model_name = ?", "legacy").Error)
	yes, no := true, false

	for _, tt := range []struct {
		name   string
		filter *bool
		models []string
		quota  int
	}{
		{"only auto", &yes, []string{"routed"}, 10},
		{"not auto includes rows from before the column", &no, []string{"direct", "legacy"}, 60},
		{"no filter keeps everything", nil, []string{"routed", "direct", "legacy"}, 70},
	} {
		t.Run(tt.name, func(t *testing.T) {
			filter := LogFilter{AutoRouted: tt.filter}

			logs, total, err := GetAllLogs(filter, 0, 20)
			require.NoError(t, err)
			assert.EqualValues(t, len(tt.models), total)
			var models []string
			for _, log := range logs {
				models = append(models, log.ModelName)
			}
			assert.ElementsMatch(t, tt.models, models)

			userLogs, userTotal, err := GetUserLogs(7, filter, 0, 20)
			require.NoError(t, err)
			assert.EqualValues(t, len(tt.models), userTotal)
			assert.Len(t, userLogs, len(tt.models))

			stat, err := SumUsedQuota(0, now-1, now+1, "", "", "", 0, "", false, false, tt.filter)
			require.NoError(t, err)
			assert.Equal(t, tt.quota, stat.Quota)
			assert.EqualValues(t, len(tt.models), stat.Total)
		})
	}
}

func TestSpecialLogFiltersFailClosedForUnsupportedDatabase(t *testing.T) {
	db := useSQLiteLogTestDB(t)
	common.SetLogDatabaseType(common.DatabaseType("unsupported"))
	other := common.MapToJsonStr(map[string]interface{}{
		"admin_info": map[string]interface{}{
			"use_channel": []string{"1", "2"},
		},
		"stream_status": map[string]interface{}{"status": "error"},
	})
	require.NoError(t, db.Create(&Log{Type: LogTypeConsume, Other: other}).Error)

	streamLogs, streamTotal, err := GetAllLogs(
		LogFilter{StreamError: true}, 0, 20,
	)
	require.NoError(t, err)
	assert.Zero(t, streamTotal)
	assert.Empty(t, streamLogs)

	retryLogs, retryTotal, err := GetAllLogs(
		LogFilter{Retry: true}, 0, 20,
	)
	require.NoError(t, err)
	assert.Zero(t, retryTotal)
	assert.Empty(t, retryLogs)
}
