package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
)

func useAutoModelMetricsRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousClient
		_ = client.Close()
	})
	return server
}

func TestAutoModelMetricsSharedStore(t *testing.T) {
	server := useAutoModelMetricsRedis(t)
	store := newAutoModelMetrics()
	autorouter.SetMetrics(store)
	t.Cleanup(func() { autorouter.SetMetrics(autoModelMetricsStore) })

	autorouter.BeginModelRequest("shared")
	autorouter.FinishModelRequest("shared", true)

	assert.Equal(t, map[string]autorouter.MetricSample{
		"shared": {Requests: 1, Successes: 1},
	}, store.Snapshot([]string{"shared"}))
	assert.Equal(t, "1", server.HGet(autoModelMetricsKey("shared", "outcomes"), strconv.FormatInt(time.Now().Unix(), 10)+":s"))
}

func TestAutoModelMetricsRedisWindow(t *testing.T) {
	server := useAutoModelMetricsRedis(t)
	store := newAutoModelMetrics()
	now := time.Now().Unix()
	expired := strconv.FormatInt(now-301, 10)
	server.HSet(autoModelMetricsKey("model", "requests"), expired, "8")
	server.HSet(autoModelMetricsKey("model", "outcomes"), expired+":s", "8")

	store.Begin("model")
	store.Finish("model", false)

	assert.Equal(t, map[string]autorouter.MetricSample{
		"model": {Requests: 1, Failures: 1},
	}, store.Snapshot([]string{"model"}))
	assert.Empty(t, server.HGet(autoModelMetricsKey("model", "requests"), expired))
	assert.Empty(t, server.HGet(autoModelMetricsKey("model", "outcomes"), expired+":s"))
}

func TestAutoModelMetricsReadFailureUsesMemory(t *testing.T) {
	server := useAutoModelMetricsRedis(t)
	store := newAutoModelMetrics()
	store.Begin("model")
	store.Finish("model", true)
	server.Close()

	assert.Equal(t, map[string]autorouter.MetricSample{
		"model": {Requests: 1, Successes: 1},
	}, store.Snapshot([]string{"model"}))
}
