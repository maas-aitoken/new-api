package channel_capacity

import (
	"context"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// Redis 里每个计数池三个键，共用 {池ID} 这个 hash tag，Lua 在 Cluster 下也落在单个槽位：
//
//	<前缀>:capacity:{池ID}:rpm       hash，字段是 Unix 秒，值是这一秒开始的尝试数
//	<前缀>:capacity:{池ID}:tpm       hash，字段是 Unix 秒，值是记在这一秒的 token 数
//	<前缀>:capacity:{池ID}:inflight  zset，成员是租约 ID，分数是租约到期的 Unix 毫秒
//
// 容量池（cp-<id>）另有四个键，同一个 hash tag：
//
//	<前缀>:capacity:{池ID}:limit   hash，动态额度（见 PublishLimit），TTL 即宽限期
//	<前缀>:capacity:{池ID}:total   hash，requests / tokens 累计数，配速学换算比例用
//	<前缀>:capacity:{池ID}:status  hash，最近一次计算的结果（ok 或失败原因）
//	<前缀>:capacity:{池ID}:reset   string，管理页请求的重置，主节点取走即删（见 RequestReset）
//
// maas-scheduler 按同样的布局读：rpm/tpm 取字段 > 当前秒-60 的值求和，
// inflight 数分数大于当前毫秒的成员，limit 的字段见 PublishLimit。改布局要两边一起改；
// total、status、reset 只有 new-api 用。
func poolKey(poolID, kind string) string {
	return runtimePrefix() + ":capacity:{" + poolID + "}:" + kind
}

// 开始：裁掉滑出窗口的字段，记 RPM/TPM，写入在途租约，刷新三个键的过期时间。
// KEYS: rpm, tpm, inflight
// ARGV: 当前秒, 窗口起点秒(含以前的都裁掉), 输入估算, 租约ID, 租约到期毫秒, 键过期毫秒
var redisBeginScript = redis.NewScript(`
local now_s = ARGV[1]
local cutoff = tonumber(ARGV[2])
for i = 1, 2 do
  local fields = redis.call('HKEYS', KEYS[i])
  for _, field in ipairs(fields) do
    local second = tonumber(field)
    if second == nil or second <= cutoff then
      redis.call('HDEL', KEYS[i], field)
    end
  end
end
redis.call('HINCRBY', KEYS[1], now_s, 1)
local estimate = tonumber(ARGV[3])
if estimate > 0 then
  redis.call('HINCRBY', KEYS[2], now_s, estimate)
end
redis.call('ZADD', KEYS[3], ARGV[5], ARGV[4])
for i = 1, 3 do
  redis.call('PEXPIRE', KEYS[i], ARGV[6])
end
return 1
`)

// 结束：删掉租约，把输入差额记回开始那一秒，输出记到结束这一秒。
// 字段被减到 0 或以下时删掉，计数不会出现负数。给了第三个键（容量池的 total）时
// 再累加累计请求数和 token 数；它与另两个键同一个 hash tag。
// KEYS: tpm, inflight[, total]
// ARGV: 租约ID, 开始秒, 输入差额, 当前秒, 输出, 键过期毫秒, 累计请求, 累计 token, total 过期毫秒
var redisFinishScript = redis.NewScript(`
redis.call('ZREM', KEYS[2], ARGV[1])
local delta = tonumber(ARGV[3])
if delta ~= 0 then
  local value = redis.call('HINCRBY', KEYS[1], ARGV[2], delta)
  if value <= 0 then
    redis.call('HDEL', KEYS[1], ARGV[2])
  end
end
local output = tonumber(ARGV[5])
if output > 0 then
  redis.call('HINCRBY', KEYS[1], ARGV[4], output)
end
redis.call('PEXPIRE', KEYS[1], ARGV[6])
if KEYS[3] then
  redis.call('HINCRBY', KEYS[3], 'requests', ARGV[7])
  redis.call('HINCRBY', KEYS[3], 'tokens', ARGV[8])
  redis.call('PEXPIRE', KEYS[3], ARGV[9])
end
return 1
`)

func redisBegin(lease *Lease, poolID string, current time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	return redisBeginScript.Run(ctx, common.RDB,
		[]string{poolKey(poolID, "rpm"), poolKey(poolID, "tpm"), poolKey(poolID, "inflight")},
		lease.startSecond,
		lease.startSecond-windowSeconds,
		lease.estimatedInput,
		lease.id,
		current.Add(leaseTTL).UnixMilli(),
		poolKeyTTL.Milliseconds(),
	).Err()
}

func redisFinish(lease *Lease, poolID string, current time.Time, inputDelta, output int64, totals Totals) error {
	ctx, cancel := context.WithTimeout(context.Background(), redisTimeout)
	defer cancel()
	keys := []string{poolKey(poolID, "tpm"), poolKey(poolID, "inflight")}
	if totals.Requests > 0 || totals.Tokens > 0 {
		keys = append(keys, poolKey(poolID, "total"))
	}
	return redisFinishScript.Run(ctx, common.RDB, keys,
		lease.id,
		lease.startSecond,
		inputDelta,
		current.Unix(),
		output,
		poolKeyTTL.Milliseconds(),
		totals.Requests,
		totals.Tokens,
		totalsKeyTTL.Milliseconds(),
	).Err()
}

// redisRenew 用一个 pipeline 给租约续期。ZADD XX 只更新还在的成员，
// 已经被 Finish 删掉的租约不会被加回来。
func redisRenew(active []*Lease, current time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	expiresAt := float64(current.Add(leaseTTL).UnixMilli())
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, lease := range active {
			for _, poolID := range lease.poolIDs {
				inflight := poolKey(poolID, "inflight")
				pipe.ZAddXX(ctx, inflight, &redis.Z{Score: expiresAt, Member: lease.id})
				pipe.PExpire(ctx, inflight, poolKeyTTL)
			}
		}
		return nil
	})
	return err
}

func redisTotals(poolIDs []string) (map[string]Totals, error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	cmds := make(map[string]*redis.StringStringMapCmd, len(poolIDs))
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, poolID := range poolIDs {
			if _, seen := cmds[poolID]; !seen {
				cmds[poolID] = pipe.HGetAll(ctx, poolKey(poolID, "total"))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make(map[string]Totals, len(cmds))
	for poolID, cmd := range cmds {
		values := cmd.Val()
		if len(values) == 0 {
			continue
		}
		requests, _ := strconv.ParseInt(values["requests"], 10, 64)
		tokens, _ := strconv.ParseInt(values["tokens"], 10, 64)
		result[poolID] = Totals{Requests: requests, Tokens: tokens}
	}
	return result, nil
}

func redisSnapshot(poolIDs []string) (map[string]Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	current := now()
	type poolCmds struct {
		rpm, tpm *redis.StringStringMapCmd
		inflight *redis.IntCmd
	}
	cmds := make(map[string]poolCmds, len(poolIDs))
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, poolID := range poolIDs {
			if _, seen := cmds[poolID]; seen {
				continue
			}
			cmds[poolID] = poolCmds{
				rpm:      pipe.HGetAll(ctx, poolKey(poolID, "rpm")),
				tpm:      pipe.HGetAll(ctx, poolKey(poolID, "tpm")),
				inflight: pipe.ZCount(ctx, poolKey(poolID, "inflight"), "("+strconv.FormatInt(current.UnixMilli(), 10), "+inf"),
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cutoff := current.Unix() - windowSeconds
	result := make(map[string]Usage, len(cmds))
	for poolID, cmd := range cmds {
		result[poolID] = Usage{
			RPM:      sumWindow(cmd.rpm.Val(), cutoff),
			TPM:      sumWindow(cmd.tpm.Val(), cutoff),
			InFlight: cmd.inflight.Val(),
		}
	}
	return result, nil
}

// sumWindow 对字段（Unix 秒）大于 cutoff 的值求和。
func sumWindow(values map[string]string, cutoff int64) int64 {
	total := int64(0)
	for field, raw := range values {
		second, err := strconv.ParseInt(field, 10, 64)
		if err != nil || second <= cutoff {
			continue
		}
		value, _ := strconv.ParseInt(raw, 10, 64)
		total += value
	}
	return total
}
