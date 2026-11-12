package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertHealthLog writes one log row landing in the block that starts
// blockOffset blocks after startTs.
func insertHealthLog(t *testing.T, channelId int, logType int, startTs int64, blockOffset int64, secondsIntoBlock int64) {
	t.Helper()
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    1,
		ChannelId: channelId,
		Type:      logType,
		CreatedAt: startTs + blockOffset*ChannelHealthBlockSeconds + secondsIntoBlock,
	}).Error)
}

func TestGetChannelHealthBucketsSplitsSuccessAndFailure(t *testing.T) {
	t.Cleanup(func() { LOG_DB.Exec("DELETE FROM logs") })

	const startTs int64 = 1_700_000_000

	// Channel 1: two successes in the first block, one failure in the last.
	insertHealthLog(t, 1, LogTypeConsume, startTs, 0, 0)
	insertHealthLog(t, 1, LogTypeConsume, startTs, 0, ChannelHealthBlockSeconds-1)
	insertHealthLog(t, 1, LogTypeError, startTs, ChannelHealthBlockCount-1, 5)
	// Channel 2: a single success in the middle block.
	insertHealthLog(t, 2, LogTypeConsume, startTs, 10, 30)
	// Channel 3 has traffic but is not requested, so it must not appear.
	insertHealthLog(t, 3, LogTypeConsume, startTs, 3, 0)

	buckets, err := GetChannelHealthBuckets([]int{1, 2}, startTs)
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	require.NotContains(t, buckets, 3)

	require.Len(t, buckets[1], ChannelHealthBlockCount)
	assert.Equal(t, ChannelHealthBucket{Success: 2, Failed: 0}, buckets[1][0])
	assert.Equal(t, ChannelHealthBucket{Success: 0, Failed: 1}, buckets[1][ChannelHealthBlockCount-1])
	// Every untouched block stays idle.
	for i := 1; i < ChannelHealthBlockCount-1; i++ {
		assert.Equalf(t, ChannelHealthBucket{}, buckets[1][i], "block %d", i)
	}

	assert.Equal(t, ChannelHealthBucket{Success: 1, Failed: 0}, buckets[2][10])
}

func TestGetChannelHealthBucketsExcludesOutOfWindowAndOtherTypes(t *testing.T) {
	t.Cleanup(func() { LOG_DB.Exec("DELETE FROM logs") })

	const startTs int64 = 1_700_000_000

	// One second before the window opens: excluded.
	insertHealthLog(t, 1, LogTypeConsume, startTs, 0, -1)
	// Non-relay log types never affect channel availability.
	insertHealthLog(t, 1, LogTypeTopup, startTs, 1, 0)
	insertHealthLog(t, 1, LogTypeManage, startTs, 1, 0)
	insertHealthLog(t, 1, LogTypeSystem, startTs, 1, 0)
	insertHealthLog(t, 1, LogTypeRefund, startTs, 1, 0)
	// A row past the last block (clock skew) folds into the newest block.
	insertHealthLog(t, 1, LogTypeError, startTs, ChannelHealthBlockCount, 7)

	buckets, err := GetChannelHealthBuckets([]int{1}, startTs)
	require.NoError(t, err)
	require.Contains(t, buckets, 1)

	var totalSuccess, totalFailed int64
	for _, bucket := range buckets[1] {
		totalSuccess += bucket.Success
		totalFailed += bucket.Failed
	}
	assert.Zero(t, totalSuccess)
	assert.EqualValues(t, 1, totalFailed)
	assert.EqualValues(t, 1, buckets[1][ChannelHealthBlockCount-1].Failed)
}

func TestGetChannelHealthBucketsRejectsOversizedIdList(t *testing.T) {
	ids := make([]int, channelHealthMaxIds+1)
	for i := range ids {
		ids[i] = i + 1
	}
	_, err := GetChannelHealthBuckets(ids, 1_700_000_000)
	require.Error(t, err)

	buckets, err := GetChannelHealthBuckets(nil, 1_700_000_000)
	require.NoError(t, err)
	assert.Empty(t, buckets)
}

// Runs on the global LOG_DB, so the dialect suites reuse it.
func TestQueryModelHealthSumsChannelsPerModel(t *testing.T) {
	clearDialectTestLogs(t)
	t.Cleanup(func() { clearDialectTestLogs(t) })

	const startTs int64 = 1_700_000_000
	endTs := startTs + ChannelHealthBlockCount*ChannelHealthBlockSeconds
	at := func(block int64) int64 { return startTs + block*ChannelHealthBlockSeconds + 30 }
	require.NoError(t, LOG_DB.Create(&[]Log{
		// The first block of m-a adds up two channels.
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: at(0)},
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: at(0)},
		{UserId: 1, ChannelId: 2, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: at(0)},
		{UserId: 1, ChannelId: 2, ModelName: "m-a", Type: LogTypeError, CreatedAt: at(0)},
		{UserId: 1, ChannelId: 2, ModelName: "m-a", Type: LogTypeError, CreatedAt: endTs - 1},
		{UserId: 1, ChannelId: 1, ModelName: "m-b", Type: LogTypeConsume, CreatedAt: at(5)},
		// Left out: no channel, no model, other types, outside [startTs, endTs).
		{UserId: 1, ChannelId: 0, ModelName: "m-a", Type: LogTypeError, CreatedAt: at(1)},
		{UserId: 1, ChannelId: 1, ModelName: "", Type: LogTypeConsume, CreatedAt: at(1)},
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeRefund, CreatedAt: at(2)},
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: startTs - 1},
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: endTs},
	}).Error)

	models, err := queryModelHealth(startTs, endTs)
	require.NoError(t, err)

	modelA := make([]ChannelHealthBucket, ChannelHealthBlockCount)
	modelA[0] = ChannelHealthBucket{Success: 3, Failed: 1}
	modelA[ChannelHealthBlockCount-1] = ChannelHealthBucket{Failed: 1}
	modelB := make([]ChannelHealthBucket, ChannelHealthBlockCount)
	modelB[5] = ChannelHealthBucket{Success: 1}
	assert.Equal(t, []ModelHealth{
		{ModelName: "m-a", Buckets: modelA},
		{ModelName: "m-b", Buckets: modelB},
	}, models)
}

func TestGetRecentModelHealthFiltersAfterCache(t *testing.T) {
	db := useSQLiteLogTestDB(t)
	resetCache := func() {
		modelHealthCacheMu.Lock()
		modelHealthCache, modelHealthCacheExpires = ModelHealthWindow{}, time.Time{}
		modelHealthCacheMu.Unlock()
	}
	resetCache()
	t.Cleanup(resetCache)

	createdAt := time.Now().Unix() - 300
	require.NoError(t, db.Create(&[]Log{
		{UserId: 1, ChannelId: 1, ModelName: "m-a", Type: LogTypeConsume, CreatedAt: createdAt},
		{UserId: 1, ChannelId: 1, ModelName: "m-b", Type: LogTypeConsume, CreatedAt: createdAt},
	}).Error)

	subset, err := GetRecentModelHealth(map[string]struct{}{"m-a": {}})
	require.NoError(t, err)
	assert.Equal(t, ChannelHealthBlockCount, subset.BlockCount)
	assert.Equal(t, ChannelHealthBlockSeconds, subset.BlockSeconds)
	assert.Zero(t, subset.StartTs%60)
	want := make([]ChannelHealthBucket, ChannelHealthBlockCount)
	want[(createdAt-subset.StartTs)/ChannelHealthBlockSeconds] = ChannelHealthBucket{Success: 1}
	assert.Equal(t, []ModelHealth{{ModelName: "m-a", Buckets: want}}, subset.Models)

	empty, err := GetRecentModelHealth(map[string]struct{}{})
	require.NoError(t, err)
	assert.NotNil(t, empty.Models)
	assert.Empty(t, empty.Models)

	// Caller filters must not alter the shared cache.
	all, err := GetRecentModelHealth(nil)
	require.NoError(t, err)
	require.Len(t, all.Models, 2)
	assert.Equal(t, "m-a", all.Models[0].ModelName)
	assert.Equal(t, "m-b", all.Models[1].ModelName)
}

func TestChannelHealthBucketExprPerDialect(t *testing.T) {
	original := common.LogDatabaseType()
	t.Cleanup(func() { common.SetLogDatabaseType(original) })

	cases := []struct {
		dbType common.DatabaseType
		want   string
	}{
		{common.DatabaseTypeMySQL, "(created_at - 100) DIV 600 AS bucket_index"},
		{common.DatabaseTypeClickHouse, "intDiv(created_at - 100, 600) AS bucket_index"},
		{common.DatabaseTypePostgreSQL, "(created_at - 100) / 600 AS bucket_index"},
		{common.DatabaseTypeSQLite, "(created_at - 100) / 600 AS bucket_index"},
	}
	for _, c := range cases {
		common.SetLogDatabaseType(c.dbType)
		expr, err := channelHealthBucketExpr(100, 600)
		require.NoErrorf(t, err, "dbType=%s", c.dbType)
		assert.Equalf(t, c.want, expr, "dbType=%s", c.dbType)
	}

	common.SetLogDatabaseType(common.DatabaseType("unknown"))
	_, err := channelHealthBucketExpr(100, 600)
	require.Error(t, err)
}
