package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

// SchedulerRuntimePrefix is the stable, credential-free namespace consumed by
// Scheduler. Runtime data is deliberately separate from new-api's internal
// rate-limit keys: Scheduler must never depend on an implementation detail of
// an unrelated limiter.
const SchedulerRuntimePrefix = "new-api:scheduler:runtime"

const schedulerRuntimeTTL = 2 * time.Minute

func schedulerRuntimeNamespace() string {
	prefix := strings.TrimSpace(common.GetEnvOrDefaultString("SCHEDULER_RUNTIME_PREFIX", SchedulerRuntimePrefix))
	if prefix == "" {
		return SchedulerRuntimePrefix
	}
	return prefix
}

// BeginSchedulerRuntime starts one Channel/Key attempt and returns the UTC
// minute bucket that owns its inflight reservation. The bucket must be reused
// by FinishSchedulerRuntime when a request crosses a minute boundary.
// 额度不再写进这个 hash：调度器从 Catalog 的 Limits 拿 <渠道, 上游模型> 额度，
// 按 capacity_pool_id 读 channel_capacity 的实时用量；这里只留成功率、延迟等。
func BeginSchedulerRuntime(channelID, keyIndex int) time.Time {
	window := time.Now().UTC()
	observeSchedulerRuntimeAt(channelID, keyIndex, window, true, 0, 0, 0, 0)
	return window
}

// FinishSchedulerRuntime closes an attempt in the same minute bucket created
// by BeginSchedulerRuntime. It is best-effort: Redis outages must not change
// the request result.
func FinishSchedulerRuntime(channelID, keyIndex int, window time.Time, statusCode, inputTokens, outputTokens int, latency time.Duration) {
	if window.IsZero() {
		window = time.Now().UTC()
	}
	observeSchedulerRuntimeAt(channelID, keyIndex, window, false, statusCode, inputTokens, outputTokens, latency)
}

// ObserveSchedulerRuntime is a convenience for callers that already have a
// single-window observation. Relay uses Begin/Finish so cross-minute requests
// do not leave inflight capacity stranded in the old bucket.
func ObserveSchedulerRuntime(channelID, keyIndex int, started bool, statusCode, inputTokens, outputTokens int, latency time.Duration) {
	observeSchedulerRuntimeAt(channelID, keyIndex, time.Now().UTC(), started, statusCode, inputTokens, outputTokens, latency)
}

func observeSchedulerRuntimeAt(channelID, keyIndex int, window time.Time, started bool, statusCode, inputTokens, outputTokens int, latency time.Duration) {
	if !common.RedisEnabled || common.RDB == nil || channelID < 0 || keyIndex < 0 {
		return
	}
	now := window.UTC()
	endpointID := fmt.Sprintf("new-api-ch-%d-k%d", channelID, keyIndex)
	key := fmt.Sprintf("%s:{%s}:%s", schedulerRuntimeNamespace(), endpointID, now.Format("200601021504"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	pipe := common.RDB.TxPipeline()
	// Preserve the first timestamp for this UTC-minute window so the consumer
	// can derive tokens/second rather than mistaking tokens/request for
	// throughput.
	pipe.HSetNX(ctx, key, "window_started_at", now.Unix())
	if started {
		pipe.HIncrBy(ctx, key, "requests_started", 1)
		pipe.HIncrBy(ctx, key, "inflight", 1)
	} else {
		pipe.HIncrBy(ctx, key, "requests_finished", 1)
		pipe.HIncrBy(ctx, key, "inflight", -1)
		succeeded := statusCode >= 200 && statusCode < 400
		if succeeded {
			pipe.HIncrBy(ctx, key, "success_total", 1)
		} else {
			pipe.HIncrBy(ctx, key, "error_total", 1)
		}
		if inputTokens > 0 {
			pipe.HIncrBy(ctx, key, "input_tokens", int64(inputTokens))
		}
		if outputTokens > 0 {
			pipe.HIncrBy(ctx, key, "output_tokens", int64(outputTokens))
		}
		// Success-scoped token counters power the monitor's "real passthrough"
		//水位, so a channel that only fails no longer looks busy. Attempt-scoped
		// input_tokens/output_tokens above remain the admission-rate signal.
		if succeeded {
			if inputTokens > 0 {
				pipe.HIncrBy(ctx, key, "success_input_tokens", int64(inputTokens))
			}
			if outputTokens > 0 {
				pipe.HIncrBy(ctx, key, "success_output_tokens", int64(outputTokens))
			}
		}
		if latency > 0 {
			pipe.HIncrBy(ctx, key, "latency_sum_ms", latency.Milliseconds())
			pipe.HIncrBy(ctx, key, "latency_samples", 1)
		}
	}
	pipe.HSet(ctx, key, "endpoint_id", endpointID, "channel_id", channelID, "key_index", keyIndex, "updated_at", now.Unix())
	pipe.Expire(ctx, key, schedulerRuntimeTTL)
	_, _ = pipe.Exec(ctx)
}

// SchedulerRuntimeSnapshot reads the current window for diagnostics and
// tests. Scheduler itself reads the same wire shape through its Redis store.
func SchedulerRuntimeSnapshot(ctx context.Context, channelID, keyIndex int) (map[string]string, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, redis.Nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	endpointID := fmt.Sprintf("new-api-ch-%d-k%d", channelID, keyIndex)
	key := fmt.Sprintf("%s:{%s}:%s", schedulerRuntimeNamespace(), endpointID, time.Now().UTC().Format("200601021504"))
	values, err := common.RDB.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, redis.Nil
	}
	return values, nil
}

func schedulerRuntimeInt(values map[string]string, field string) int64 {
	value, _ := strconv.ParseInt(values[field], 10, 64)
	return value
}
