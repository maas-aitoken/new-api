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

	memoryLimitMu.Lock()
	memoryLimits = make(map[string]memoryLimitEntry)
	memoryStatuses = make(map[string]PollStatus)
	memoryResetRequests = make(map[string]bool)
	memoryLimitMu.Unlock()

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
	miniredisServers[client] = server
	t.Cleanup(func() { delete(miniredisServers, client) })
	return client
}

// miniredisServers 让测试能快进 miniredis 的时钟（键过期不看包内时钟）。
var miniredisServers = map[*redis.Client]*miniredis.Miniredis{}

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

			first := Begin(1, "gpt-5", 0, 1000)
			second := Begin(1, "gpt-5", 0, 400)
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
			third := Begin(1, "gpt-5", 0, 250)
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

			lease := Begin(2, "claude", 0, 1000)
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

	alive := Begin(3, "qwen", 0, 10)
	crashed := Begin(3, "qwen", 0, 10)
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

	assert.Nil(t, Begin(0, "gpt-5", 0, 1))
	assert.Nil(t, Begin(9, " ", 0, 1))
}

// maas-scheduler 按这个键布局读计数池，改布局要两边一起改。
func TestCapacityRedisKeyLayoutMatchesSchedulerContract(t *testing.T) {
	t.Setenv("SCHEDULER_RUNTIME_PREFIX", "custom:runtime")
	client := useBackend(t, true)
	useClock(t, time.Unix(1_800_000_000, 0))
	pool := PoolID(4, "gpt-5")

	lease := Begin(4, "gpt-5", 0, 77)
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

	// 容量池的动态额度、累计数、状态也在同一前缀、同一 hash tag 下。
	capacityPool := CapacityPoolID(5)
	assert.Equal(t, "cp-5", capacityPool)
	require.NoError(t, PublishLimit(capacityPool, PublishedLimit{RPM: 120, TPM: 90000, MaxConcurrency: 6, ComputedAt: 1_800_000_000, Detail: `{"usable":3}`}, 10*time.Minute))
	limitKey := "custom:runtime:capacity:{cp-5}:limit"
	limit, err := client.HGetAll(ctx, limitKey).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"rpm": "120", "tpm": "90000", "max_concurrency": "6", "exhausted": "0",
		"computed_at": "1800000000", "detail": `{"usable":3}`,
	}, limit)
	ttl, err := client.PTTL(ctx, limitKey).Result()
	require.NoError(t, err)
	assert.Equal(t, 10*time.Minute, ttl, "TTL 就是宽限期")
	require.NoError(t, PublishLimit(capacityPool, PublishedLimit{Exhausted: true, ComputedAt: 1_800_000_060}, time.Minute))
	limit, err = client.HGetAll(ctx, limitKey).Result()
	require.NoError(t, err)
	assert.Equal(t, "1", limit["exhausted"])
	assert.Equal(t, "", limit["detail"], "重新发布时旧字段不会留下")

	bound := Begin(4, "gpt-5", 5, 10)
	Finish(bound, true, 30, 20)
	total, err := client.HGetAll(ctx, "custom:runtime:capacity:{cp-5}:total").Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"requests": "1", "tokens": "50"}, total)
}

func TestCapacityPoolMembersShareOneBucket(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			useBackend(t, backend.shared)
			useClock(t, time.Unix(1_800_000_000, 0))
			capacityPool := CapacityPoolID(7)

			// 两个渠道的上游模型绑在同一个容量池：容量池合计，各自的池照记。
			a := Begin(1, "claude-sonnet", 7, 100)
			b := Begin(2, "claude-opus", 7, 200)
			unbound := Begin(3, "claude-sonnet", 0, 50)
			require.NotNil(t, a)
			require.NotNil(t, b)
			assert.Equal(t, Usage{RPM: 2, TPM: 300, InFlight: 2}, usageOf(t, capacityPool))
			assert.Equal(t, Usage{RPM: 1, TPM: 100, InFlight: 1}, usageOf(t, PoolID(1, "claude-sonnet")))
			assert.Equal(t, Usage{RPM: 1, TPM: 200, InFlight: 1}, usageOf(t, PoolID(2, "claude-opus")))

			// 报了用量的尝试才进累计数；没绑的不进。
			Finish(a, true, 120, 80)
			Finish(b, false, 0, 0)
			Finish(unbound, true, 50, 10)
			assert.Equal(t, Usage{RPM: 2, TPM: 200, InFlight: 0}, usageOf(t, capacityPool))
			totals, err := ReadTotals([]string{capacityPool, PoolID(1, "claude-sonnet")})
			require.NoError(t, err)
			assert.Equal(t, map[string]Totals{capacityPool: {Requests: 1, Tokens: 200}}, totals)
		})
	}
}

func TestCapacityPoolLimitExpiresAfterGrace(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			client := useBackend(t, backend.shared)
			advance := useClock(t, time.Unix(1_800_000_000, 0))
			capacityPool := CapacityPoolID(8)
			published := PublishedLimit{RPM: 30, TPM: 0, MaxConcurrency: 2, ComputedAt: 1_800_000_000}
			require.NoError(t, PublishLimit(capacityPool, published, 2*time.Minute))
			require.Error(t, PublishLimit(capacityPool, published, 0))

			limits, err := ReadLimits([]string{capacityPool, CapacityPoolID(9)})
			require.NoError(t, err)
			assert.Equal(t, map[string]PublishedLimit{capacityPool: published}, limits, "没发布过的池不出现")

			advance(121)
			if client != nil {
				miniredisServers[client].FastForward(121 * time.Second)
			}
			limits, err = ReadLimits([]string{capacityPool})
			require.NoError(t, err)
			assert.Empty(t, limits, "过了宽限期就读不到，调度器回到保底额度")

			require.NoError(t, PublishStatus(capacityPool, PollStatus{Status: "source_failed", Error: "kiro: HTTP 502", AttemptAt: 1_800_000_121}))
			statuses, err := ReadStatuses([]string{capacityPool})
			require.NoError(t, err)
			assert.Equal(t, PollStatus{Status: "source_failed", Error: "kiro: HTTP 502", AttemptAt: 1_800_000_121}, statuses[capacityPool])
		})
	}
}

func TestCapacityPoolResetRequestIsTakenOnce(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			client := useBackend(t, backend.shared)
			capacityPool, other := CapacityPoolID(10), CapacityPoolID(11)

			requested, err := TakeResetRequest(capacityPool)
			require.NoError(t, err)
			assert.False(t, requested, "没人请求过")

			// 主节点取走之前连点两次，只算一次。
			require.NoError(t, RequestReset(capacityPool))
			require.NoError(t, RequestReset(capacityPool))
			if client != nil {
				ttl, err := client.PTTL(context.Background(), poolKey(capacityPool, "reset")).Result()
				require.NoError(t, err)
				assert.Equal(t, resetRequestTTL, ttl, "主节点一直不来取，请求也会过期")
			}
			requested, err = TakeResetRequest(capacityPool)
			require.NoError(t, err)
			assert.True(t, requested)
			requested, err = TakeResetRequest(capacityPool)
			require.NoError(t, err)
			assert.False(t, requested, "取走即删除")

			requested, err = TakeResetRequest(other)
			require.NoError(t, err)
			assert.False(t, requested, "只重置被请求的池")
		})
	}
}
