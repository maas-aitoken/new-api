package channel_capacity

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useClock 把包内时钟钉在 start，返回推进时钟的函数。
func useClock(t *testing.T, start time.Time) func(seconds int) {
	t.Helper()
	current := start
	previous := now
	now = func() time.Time { return current }
	t.Cleanup(func() { now = previous })
	return func(seconds int) { current = current.Add(time.Duration(seconds) * time.Second) }
}

// useBackend 清空进程内状态，shared 为 true 时接上一个 miniredis。
func useBackend(t *testing.T, shared bool) *redis.Client {
	t.Helper()
	memoryMu.Lock()
	memoryPools = make(map[string]*memoryPool)
	memoryMu.Unlock()
	leasesMu.Lock()
	leases = make(map[string]*Lease)
	leasesMu.Unlock()

	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousClient })
	if !shared {
		common.RedisEnabled, common.RDB = false, nil
		return nil
	}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	common.RedisEnabled, common.RDB = true, client
	return client
}

func usageOf(t *testing.T, poolID string) Usage {
	t.Helper()
	usages, err := Snapshot([]string{poolID})
	require.NoError(t, err)
	return usages[poolID]
}

var backends = []struct {
	name   string
	shared bool
}{{"memory", false}, {"redis", true}}

func TestCapacityCountsInputFirstThenAddsOutput(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			useBackend(t, backend.shared)
			advance := useClock(t, time.Unix(1_800_000_000, 0))
			pool := PoolID(1, "gpt-5")

			first := Begin(1, "gpt-5", 1000)
			second := Begin(1, "gpt-5", 400)
			require.NotNil(t, first)
			require.NotNil(t, second)
			assert.Equal(t, Usage{RPM: 2, TPM: 1400, InFlight: 2}, usageOf(t, pool), "在途请求只占输入估算")

			// 成功：输入按实际值校正（1000→900），输出 300 记到结束这一秒。
			advance(1)
			Finish(first, true, 900, 300)
			assert.Equal(t, Usage{RPM: 2, TPM: 1600, InFlight: 1}, usageOf(t, pool))

			// 失败且没有任何用量：撤回输入估算，RPM 不撤回。
			Finish(second, false, 0, 0)
			assert.Equal(t, Usage{RPM: 2, TPM: 1200, InFlight: 0}, usageOf(t, pool))

			// 重复结束、nil 都不影响计数。
			Finish(first, true, 5000, 5000)
			Finish(nil, true, 1, 1)
			assert.Equal(t, Usage{RPM: 2, TPM: 1200, InFlight: 0}, usageOf(t, pool))

			// 成功但上游没给用量：保留输入估算。
			advance(4)
			third := Begin(1, "gpt-5", 250)
			Finish(third, true, 0, 0)
			assert.Equal(t, Usage{RPM: 3, TPM: 1450, InFlight: 0}, usageOf(t, pool))

			// 滑动窗口是 (now-60, now]：第 60 秒时第 0 秒的记录滑出，第 1、5 秒的还在；
			// 第 65 秒时第 5 秒及以前的全部滑出。
			advance(55)
			assert.Equal(t, Usage{RPM: 1, TPM: 550}, usageOf(t, pool))
			advance(5)
			assert.Equal(t, Usage{}, usageOf(t, pool))
		})
	}
}

func TestCapacityFinishAfterStartSecondLeftWindow(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			useBackend(t, backend.shared)
			advance := useClock(t, time.Unix(1_800_000_000, 0))
			pool := PoolID(2, "claude")

			lease := Begin(2, "claude", 1000)
			// 长请求跑了 70 秒：开始那一秒的输入早已滑出窗口，不再校正，
			// 只把输出记进当前窗口。
			advance(70)
			Finish(lease, true, 800, 120)
			assert.Equal(t, Usage{RPM: 0, TPM: 120, InFlight: 0}, usageOf(t, pool))
		})
	}
}

func TestCapacityRedisLeaseExpiresUnlessRenewed(t *testing.T) {
	client := useBackend(t, true)
	advance := useClock(t, time.Unix(1_800_000_000, 0))
	pool := PoolID(3, "qwen")

	alive := Begin(3, "qwen", 10)
	crashed := Begin(3, "qwen", 10)
	require.NotNil(t, alive)
	require.NotNil(t, crashed)
	// 模拟节点挂掉：它的租约不再续期，也不会结束。
	untrackLease(crashed)

	advance(60)
	renewLeases()
	advance(60)
	assert.Equal(t, int64(1), usageOf(t, pool).InFlight, "续过期的租约还在，没续的已过期")

	Finish(alive, true, 10, 1)
	assert.Equal(t, int64(0), usageOf(t, pool).InFlight)

	// 已经结束的租约不会被续回来。
	require.NoError(t, redisRenew([]*Lease{alive}, now()))
	members, err := client.ZCard(context.Background(), poolKey(pool, "inflight")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), members, "只剩挂掉节点那条过期租约，结束过的没有被 ZADD 回来")
}

func TestCapacityPoolSharedByKeysOfOneChannelModel(t *testing.T) {
	assert.Equal(t, PoolID(9, "gpt-5"), PoolID(9, "gpt-5"))
	assert.NotEqual(t, PoolID(9, "gpt-5"), PoolID(10, "gpt-5"))
	assert.NotEqual(t, PoolID(9, "gpt-5"), PoolID(9, "gpt-5-mini"))
	assert.Regexp(t, `^c9-[0-9a-f]{12}$`, PoolID(9, "vendor/model:tag"))

	assert.Nil(t, Begin(0, "gpt-5", 1))
	assert.Nil(t, Begin(9, " ", 1))
}

// maas-scheduler 按这个键布局读计数池，改布局要两边一起改。
func TestCapacityRedisKeyLayoutMatchesSchedulerContract(t *testing.T) {
	t.Setenv("SCHEDULER_RUNTIME_PREFIX", "custom:runtime")
	client := useBackend(t, true)
	useClock(t, time.Unix(1_800_000_000, 0))
	pool := PoolID(4, "gpt-5")

	lease := Begin(4, "gpt-5", 77)
	require.NotNil(t, lease)
	ctx := context.Background()
	prefix := "custom:runtime:capacity:{" + pool + "}:"

	rpm, err := client.HGetAll(ctx, prefix+"rpm").Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1800000000": "1"}, rpm)
	tpm, err := client.HGetAll(ctx, prefix+"tpm").Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1800000000": "77"}, tpm)
	inflight, err := client.ZRangeWithScores(ctx, prefix+"inflight", 0, -1).Result()
	require.NoError(t, err)
	require.Len(t, inflight, 1)
	assert.Equal(t, float64(time.Unix(1_800_000_000, 0).Add(leaseTTL).UnixMilli()), inflight[0].Score)

	Finish(lease, true, 77, 0)
}
