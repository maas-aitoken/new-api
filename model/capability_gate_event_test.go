package model

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// legacyCapabilityGateEvent 是上一个版本落在主库里的表结构，用来构造待升级的库。
type legacyCapabilityGateEvent struct {
	Id                int64  `gorm:"primaryKey"`
	Mode              string `gorm:"type:varchar(16);index"`
	Model             string `gorm:"type:varchar(255);index"`
	Action            string `gorm:"type:varchar(16);index"`
	ChannelsEvaluated int
	RejectedChannels  string `gorm:"type:varchar(255)"`
	Dimensions        string `gorm:"type:text"`
	CreatedAt         int64  `gorm:"bigint;index"`
}

func (legacyCapabilityGateEvent) TableName() string {
	return "capability_gate_events"
}

func TestCapabilityGateEventsCanBeFilteredAndOrdered(t *testing.T) {
	require.NoError(t, LOG_DB.Exec("DELETE FROM capability_gate_events").Error)
	t.Cleanup(func() { LOG_DB.Exec("DELETE FROM capability_gate_events") })
	require.NoError(t, BatchInsertCapabilityGateEvents([]*CapabilityGateEvent{
		{Mode: "shadow", Model: "model-a", Action: CapabilityGateEventWouldReject, RejectedChannels: "1,2", RejectedCount: 2, RequestId: "req-1", Dimensions: `[{"dimension":"tools.supported","severity":"hard"}]`, CreatedAt: 100},
		{Mode: "enforce", Model: "model-a", Action: CapabilityGateEventRejected, RejectedChannels: "3", RejectedCount: 1, RequestId: "req-2", Dimensions: `[{"dimension":"params.top_p","severity":"soft"}]`, CreatedAt: 200},
		{Mode: "enforce", Model: "model-b", Action: CapabilityGateEventSoft, RequestId: "req-3", Dimensions: `[{"dimension":"params.stop","severity":"soft"}]`, CreatedAt: 300},
	}))

	filtered, total, err := GetCapabilityGateEvents(CapabilityGateEventQuery{
		Model: "model-a", Action: CapabilityGateEventRejected, StartTime: 150, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, filtered, 1)
	assert.Equal(t, "req-2", filtered[0].RequestId)
	assert.Equal(t, 1, filtered[0].RejectedCount)

	byRequest, total, err := GetCapabilityGateEvents(CapabilityGateEventQuery{RequestId: "req-3", Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, byRequest, 1)
	assert.Equal(t, "model-b", byRequest[0].Model)

	all, total, err := GetCapabilityGateEvents(CapabilityGateEventQuery{Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, all, 3)
	assert.Equal(t, []string{"req-3", "req-2", "req-1"}, []string{all[0].RequestId, all[1].RequestId, all[2].RequestId})
}

func TestCleanupCapabilityGateEventsDeletesInChunks(t *testing.T) {
	require.NoError(t, LOG_DB.Exec("DELETE FROM capability_gate_events").Error)
	t.Cleanup(func() { LOG_DB.Exec("DELETE FROM capability_gate_events") })
	// 2500 条过期事件要跨过三个清理块（1000 + 1000 + 500），3 条新事件必须保留。
	events := make([]*CapabilityGateEvent, 0, 2503)
	for i := range 2500 {
		events = append(events, &CapabilityGateEvent{Mode: "shadow", Model: "model-a", Action: CapabilityGateEventSoft, CreatedAt: int64(1 + i)})
	}
	for i := range 3 {
		events = append(events, &CapabilityGateEvent{Mode: "shadow", Model: "model-a", Action: CapabilityGateEventSoft, CreatedAt: int64(10000 + i)})
	}
	require.NoError(t, BatchInsertCapabilityGateEvents(events))
	assert.True(t, HasCapabilityGateEventsBefore(5000))

	deleted, err := CleanupCapabilityGateEvents(context.Background(), 5000)
	require.NoError(t, err)
	assert.Equal(t, int64(2500), deleted)
	assert.False(t, HasCapabilityGateEventsBefore(5000))
	var remaining int64
	require.NoError(t, LOG_DB.Model(&CapabilityGateEvent{}).Count(&remaining).Error)
	assert.Equal(t, int64(3), remaining)

	// ctx 已取消时不动数据，并把取消原因交给调用方。
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deleted, err = CleanupCapabilityGateEvents(canceled, 20000)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, deleted)
	require.NoError(t, LOG_DB.Model(&CapabilityGateEvent{}).Count(&remaining).Error)
	assert.Equal(t, int64(3), remaining)
}

func TestCleanupCapabilityGateEventsChunkSizeComesFromEnvironment(t *testing.T) {
	cases := []struct {
		name        string
		chunk       string
		wantDeletes int64
	}{
		{name: "default chunk clears 25 rows in one statement", chunk: "", wantDeletes: 1},
		{name: "chunk of 10 needs three statements", chunk: "10", wantDeletes: 3},
		{name: "zero is raised to one row per statement", chunk: "0", wantDeletes: 25},
		{name: "unparsable value falls back to the default", chunk: "abc", wantDeletes: 1},
	}
	var deletes atomic.Int64
	const callbackName = "count_capability_gate_event_deletes"
	require.NoError(t, LOG_DB.Callback().Delete().After("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "capability_gate_events" && tx.Error == nil {
			deletes.Add(1)
		}
	}))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Delete().Remove(callbackName)) })
	t.Cleanup(func() { LOG_DB.Exec("DELETE FROM capability_gate_events") })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, LOG_DB.Exec("DELETE FROM capability_gate_events").Error)
			events := make([]*CapabilityGateEvent, 0, 25)
			for i := range 25 {
				events = append(events, &CapabilityGateEvent{Mode: "shadow", Model: "model-a", Action: CapabilityGateEventSoft, CreatedAt: int64(1 + i)})
			}
			require.NoError(t, BatchInsertCapabilityGateEvents(events))
			t.Setenv("CAPABILITY_GATE_EVENT_CLEANUP_CHUNK", tc.chunk)
			deletes.Store(0)

			deleted, err := CleanupCapabilityGateEvents(context.Background(), 5000)

			require.NoError(t, err)
			assert.Equal(t, int64(25), deleted)
			assert.Equal(t, tc.wantDeletes, deletes.Load())
		})
	}
}

func TestCapabilityGateEventTableUpgradesFromLegacySchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.AutoMigrate(&legacyCapabilityGateEvent{}))
	require.NoError(t, db.Create(&legacyCapabilityGateEvent{
		Mode: "shadow", Model: "old-model", Action: CapabilityGateEventWouldFilter,
		ChannelsEvaluated: 3, RejectedChannels: "1,2", Dimensions: "[]", CreatedAt: 42,
	}).Error)

	// 连跑两次，第二次证明迁移幂等。
	for range 2 {
		require.NoError(t, db.AutoMigrate(&CapabilityGateEvent{}))
	}

	var upgraded CapabilityGateEvent
	require.NoError(t, db.First(&upgraded).Error)
	assert.Equal(t, "old-model", upgraded.Model)
	assert.Equal(t, "1,2", upgraded.RejectedChannels)
	assert.Equal(t, int64(42), upgraded.CreatedAt)
	assert.Zero(t, upgraded.RejectedCount)
	assert.Empty(t, upgraded.RequestId)
	assert.True(t, db.Migrator().HasIndex(&CapabilityGateEvent{}, "idx_capability_gate_events_request_id"))

	require.NoError(t, db.Create(&CapabilityGateEvent{Mode: "enforce", Model: "new-model", Action: CapabilityGateEventRejected, RequestId: "req-new", CreatedAt: 43}).Error)
	var total int64
	require.NoError(t, db.Model(&CapabilityGateEvent{}).Count(&total).Error)
	assert.Equal(t, int64(2), total)
}
