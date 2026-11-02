package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestObserveSchedulerRuntimePublishesChannelKeyWindow(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		_ = client.Close()
	})

	ObserveSchedulerRuntime(7, 2, true, 0, 0, 0, 0)
	ObserveSchedulerRuntime(7, 2, false, 200, 12, 8, 250000000)
	values, err := SchedulerRuntimeSnapshot(t.Context(), 7, 2)
	require.NoError(t, err)
	require.Equal(t, "new-api-ch-7-k2", values["endpoint_id"])
	require.Equal(t, "1", values["requests_started"])
	require.Equal(t, "1", values["requests_finished"])
	require.Equal(t, "0", values["inflight"])
	require.Equal(t, "12", values["input_tokens"])
	require.Equal(t, "8", values["output_tokens"])
	require.Equal(t, "1", values["success_total"])
	require.Equal(t, "250", values["latency_sum_ms"])
}

// 额度改由 Catalog 的 <渠道, 上游模型> Limits 提供，按 key 的运行时 hash 不再
// 带 capacity_* 字段，免得调度器拿到过时的渠道级额度当回退值。
func TestBeginSchedulerRuntimeDoesNotPublishCapacity(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		_ = client.Close()
	})

	BeginSchedulerRuntime(8, 1)
	values, err := SchedulerRuntimeSnapshot(t.Context(), 8, 1)
	require.NoError(t, err)
	require.Equal(t, "1", values["requests_started"])
	require.NotContains(t, values, "capacity_rpm")
	require.NotContains(t, values, "capacity_tpm")
	require.NotContains(t, values, "max_concurrency")
}
