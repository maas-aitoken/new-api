package limiter

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/go-redis/redis/v8"
)

//go:embed lua/token_bucket.lua
var tokenBucketScript string

// SHA 缓存
var tokenBucketSHA atomic.Value

type Bucket struct {
	Key        string
	RatePerMin int // 稳态速率
	Capacity   int // 最大并发
}

type BucketDecision struct {
	Allowed      bool
	BlockedIndex int // 多级令牌桶标注哪一个层级
	RetryAfterMs int64
}

func AllowBuckets(ctx context.Context, rdb *redis.Client, requested int, buckets []Bucket) (BucketDecision, error) {
	decision := BucketDecision{BlockedIndex: -1}
	if rdb == nil {
		return decision, errors.New("Redis client is not initialized")
	}
	if requested <= 0 {
		return decision, fmt.Errorf("requested tokens must be positive, got %d", requested)
	}
	if len(buckets) == 0 {
		return decision, errors.New("at least one bucket is required")
	}

	keys := make([]string, 0, len(buckets))
	args := make([]any, 0, 2+len(buckets)*2)
	// 参数打包成 [requested, n, rate1, cap1, rate2, cap2, ...]
	args = append(args, requested, len(buckets))
	for _, bucket := range buckets {
		if bucket.Key == "" {
			return decision, errors.New("bucket key is empty")
		}
		keys = append(keys, bucket.Key)
		args = append(args, bucket.RatePerMin, bucket.Capacity)
	}

	values, err := evalTokenBucket(ctx, rdb, keys, args)
	if err != nil {
		return decision, err
	}
	if len(values) != 3 {
		return decision, fmt.Errorf("unexpected token bucket reply length %d", len(values))
	}

	allowed, err := replyInt(values[0])
	if err != nil {
		return decision, err
	}
	blockedIndex, err := replyInt(values[1])
	if err != nil {
		return decision, err
	}
	retryAfterMs, err := replyInt(values[2])
	if err != nil {
		return decision, err
	}

	decision.Allowed = allowed == 1
	decision.RetryAfterMs = retryAfterMs
	if decision.Allowed {
		decision.BlockedIndex = -1
	} else {
		// Lua indexes from 1.
		decision.BlockedIndex = int(blockedIndex) - 1
	}
	return decision, nil
}

func evalTokenBucket(ctx context.Context, rdb *redis.Client, keys []string, args []any) ([]any, error) {
	if sha, ok := tokenBucketSHA.Load().(string); ok && sha != "" {
		values, err := rdb.EvalSha(ctx, sha, keys, args...).Slice()
		if err == nil {
			return values, nil
		}
		if !isNoScriptErr(err) {
			return nil, err
		}
	}

	values, err := rdb.Eval(ctx, tokenBucketScript, keys, args...).Slice()
	if err != nil {
		return nil, err
	}
	if sha, loadErr := rdb.ScriptLoad(ctx, tokenBucketScript).Result(); loadErr == nil {
		tokenBucketSHA.Store(sha)
	}
	return values, nil
}

func isNoScriptErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOSCRIPT")
}

func replyInt(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("unexpected token bucket integer reply %q: %w", typed, err)
		}
		return parsed, nil
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("unexpected token bucket integer reply %q: %w", typed, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected token bucket integer reply type %T", value)
	}
}
