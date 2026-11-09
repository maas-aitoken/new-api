package channel_capacity

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// PublishedLimit 是主节点每分钟为一个容量池算出的动态额度。
//
// Redis 里存成 <前缀>:capacity:{cp-<id>}:limit 这个 hash，字段：
//
//	rpm / tpm / max_concurrency  整数，0 表示这一项不限
//	exhausted                    "1" 表示没有可用账号，调度器不再选这个池的成员
//	computed_at                  计算时间，Unix 秒
//	detail                       计算明细 JSON，只给管理页展示，调度器不读
//
// 键的 TTL 就是宽限期：拉取失败时不去动它，过期后调度器退回 Catalog 里的保底额度。
type PublishedLimit struct {
	RPM            int64
	TPM            int64
	MaxConcurrency int64
	Exhausted      bool
	ComputedAt     int64
	Detail         string
}

// PollStatus 是容量池最近一次计算的结果，给管理页说明额度为什么没在更新。
type PollStatus struct {
	// Status 是 ok，或 fetch_failed / source_failed / empty_scope / config_error / publish_failed。
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	AttemptAt int64  `json:"attempt_at"`
}

// pollStatusTTL 远长于轮询间隔：主节点停了以后管理页还能看到最后一次结果。
const pollStatusTTL = 24 * time.Hour

// resetRequestTTL 是重置请求等主节点来取的最长时间，远长于轮询间隔；
// 主节点停了很久才回来时，那时的状态早已不是要重置的那份，请求作废也无妨。
const resetRequestTTL = 24 * time.Hour

type memoryLimitEntry struct {
	limit     PublishedLimit
	expiresAt time.Time
}

var (
	memoryLimitMu       sync.Mutex
	memoryLimits        = make(map[string]memoryLimitEntry)
	memoryStatuses      = make(map[string]PollStatus)
	memoryResetRequests = make(map[string]bool)
)

// PublishLimit 写入容量池的动态额度，ttl 后自动失效。
func PublishLimit(poolID string, limit PublishedLimit, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("limit ttl must be positive")
	}
	if !Shared() {
		memoryLimitMu.Lock()
		memoryLimits[poolID] = memoryLimitEntry{limit: limit, expiresAt: now().Add(ttl)}
		memoryLimitMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	key := poolKey(poolID, "limit")
	exhausted := "0"
	if limit.Exhausted {
		exhausted = "1"
	}
	// 先删再写，旧版本留下的字段不会混进新额度；事务保证键不会没有 TTL。
	_, err := common.RDB.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		pipe.HSet(ctx, key,
			"rpm", limit.RPM,
			"tpm", limit.TPM,
			"max_concurrency", limit.MaxConcurrency,
			"exhausted", exhausted,
			"computed_at", limit.ComputedAt,
			"detail", limit.Detail,
		)
		pipe.PExpire(ctx, key, ttl)
		return nil
	})
	return err
}

// ReadLimits 批量读取容量池的动态额度；没发布过或已过期的池不出现在结果里。
func ReadLimits(poolIDs []string) (map[string]PublishedLimit, error) {
	result := make(map[string]PublishedLimit, len(poolIDs))
	if len(poolIDs) == 0 {
		return result, nil
	}
	if !Shared() {
		current := now()
		memoryLimitMu.Lock()
		defer memoryLimitMu.Unlock()
		for _, poolID := range poolIDs {
			if entry, ok := memoryLimits[poolID]; ok && current.Before(entry.expiresAt) {
				result[poolID] = entry.limit
			}
		}
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	cmds := make(map[string]*redis.StringStringMapCmd, len(poolIDs))
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, poolID := range poolIDs {
			if _, seen := cmds[poolID]; !seen {
				cmds[poolID] = pipe.HGetAll(ctx, poolKey(poolID, "limit"))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for poolID, cmd := range cmds {
		values := cmd.Val()
		if len(values) == 0 {
			continue
		}
		limit := PublishedLimit{Exhausted: values["exhausted"] == "1", Detail: values["detail"]}
		limit.RPM, _ = strconv.ParseInt(values["rpm"], 10, 64)
		limit.TPM, _ = strconv.ParseInt(values["tpm"], 10, 64)
		limit.MaxConcurrency, _ = strconv.ParseInt(values["max_concurrency"], 10, 64)
		limit.ComputedAt, _ = strconv.ParseInt(values["computed_at"], 10, 64)
		result[poolID] = limit
	}
	return result, nil
}

// PublishStatus 记下容量池最近一次计算的结果。
func PublishStatus(poolID string, status PollStatus) error {
	if !Shared() {
		memoryLimitMu.Lock()
		memoryStatuses[poolID] = status
		memoryLimitMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	key := poolKey(poolID, "status")
	_, err := common.RDB.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key, "status", status.Status, "error", status.Error, "attempt_at", status.AttemptAt)
		pipe.PExpire(ctx, key, pollStatusTTL)
		return nil
	})
	return err
}

// ReadStatuses 批量读取容量池最近一次计算的结果。
func ReadStatuses(poolIDs []string) (map[string]PollStatus, error) {
	result := make(map[string]PollStatus, len(poolIDs))
	if len(poolIDs) == 0 {
		return result, nil
	}
	if !Shared() {
		memoryLimitMu.Lock()
		defer memoryLimitMu.Unlock()
		for _, poolID := range poolIDs {
			if status, ok := memoryStatuses[poolID]; ok {
				result[poolID] = status
			}
		}
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	cmds := make(map[string]*redis.StringStringMapCmd, len(poolIDs))
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, poolID := range poolIDs {
			if _, seen := cmds[poolID]; !seen {
				cmds[poolID] = pipe.HGetAll(ctx, poolKey(poolID, "status"))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for poolID, cmd := range cmds {
		values := cmd.Val()
		if len(values) == 0 {
			continue
		}
		status := PollStatus{Status: values["status"], Error: values["error"]}
		status.AttemptAt, _ = strconv.ParseInt(values["attempt_at"], 10, 64)
		result[poolID] = status
	}
	return result, nil
}

// RequestReset 请主节点丢掉容量池的计算状态（学到的换算比例和上调平滑），下一轮按账号
// 现状重算、直接发布。状态只在主节点内存里，管理请求可能落在任何节点，所以经 Redis 转交；
// 没有 Redis 时只有落在主节点上的请求有效，与动态额度本身只在主节点可见一致。
func RequestReset(poolID string) error {
	if !Shared() {
		memoryLimitMu.Lock()
		memoryResetRequests[poolID] = true
		memoryLimitMu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	return common.RDB.Set(ctx, poolKey(poolID, "reset"), "1", resetRequestTTL).Err()
}

// TakeResetRequest 取走容量池的重置请求，有请求时返回 true。取走即删除，同一次请求只生效一次；
// 连点几次在主节点下一轮之前只算一次。
func TakeResetRequest(poolID string) (bool, error) {
	if !Shared() {
		memoryLimitMu.Lock()
		defer memoryLimitMu.Unlock()
		requested := memoryResetRequests[poolID]
		delete(memoryResetRequests, poolID)
		return requested, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	deleted, err := common.RDB.Del(ctx, poolKey(poolID, "reset")).Result()
	return deleted > 0, err
}
