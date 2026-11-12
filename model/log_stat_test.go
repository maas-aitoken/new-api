package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSumUsedQuotaKeepsAggregateQueriesIndependent(t *testing.T) {
	previousLogDB := LOG_DB
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		_ = sqlDB.Close()
	})

	now := time.Now().Unix()
	logs := []Log{
		{CreatedAt: now, Type: LogTypeConsume, Username: "alice", TokenName: "token", ModelName: "gpt", ChannelId: 3, Group: "default", Quota: 100, PromptTokens: 10, CompletionTokens: 5, FastMode: true},
		{CreatedAt: now, Type: LogTypeConsume, Username: "alice", TokenName: "token", ModelName: "gpt", ChannelId: 3, Group: "default", Quota: 200, PromptTokens: 20, CompletionTokens: 10},
		{CreatedAt: now, Type: LogTypeRefund, Username: "alice", TokenName: "token", ModelName: "gpt", ChannelId: 3, Group: "default", Quota: 999, FastMode: true},
		{CreatedAt: now, Type: LogTypeConsume, Username: "bob", TokenName: "token", ModelName: "gpt", ChannelId: 3, Group: "default", Quota: 999, FastMode: true},
	}
	require.NoError(t, db.Create(&logs).Error)

	stat, err := SumUsedQuota(0, now-1, now+1, "gpt", "alice", "token", 3, "default", false, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 300, stat.Quota)
	assert.Equal(t, 2, stat.Rpm)
	assert.Equal(t, 45, stat.Tpm)
	assert.EqualValues(t, 2, stat.Total)
	assert.EqualValues(t, 1, stat.Fast)
	assert.Equal(t, 0.5, stat.FastRatio)
}
