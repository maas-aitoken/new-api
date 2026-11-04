package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useTokenBucketMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

// RetryAfterMs must always round up. Redis truncates Lua numbers towards zero
// when it converts them to an integer reply, so a fractional millisecond that
// was not rounded up would return a hint one millisecond too early and the
// retry it invites would be rejected again.
func TestAllowBucketsRoundsRetryHintUp(t *testing.T) {
	server, client := useTokenBucketMiniRedis(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	server.SetTime(base)
	ctx := context.Background()

	// 7 per minute leaves a fractional per-millisecond rate, and refilling for
	// one second leaves a fractional balance, so the deficit does not divide
	// evenly: 1 - 7/60 tokens needs 7571.43ms, which has to become 7572.
	buckets := []Bucket{{Key: "bucket", RatePerMin: 7, Capacity: 7}}
	for i := range 7 {
		decision, err := AllowBuckets(ctx, client, 1, buckets)
		require.NoError(t, err)
		require.True(t, decision.Allowed, "request %d of the burst", i+1)
	}

	server.SetTime(base.Add(time.Second))
	decision, err := AllowBuckets(ctx, client, 1, buckets)
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	assert.Equal(t, int64(7572), decision.RetryAfterMs)

	// Honouring the hint has to succeed: one millisecond earlier must not.
	server.SetTime(base.Add(time.Second + 7571*time.Millisecond))
	decision, err = AllowBuckets(ctx, client, 1, buckets)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "the hint must not invite a retry that is still refused")

	server.SetTime(base.Add(time.Second + 7572*time.Millisecond))
	decision, err = AllowBuckets(ctx, client, 1, buckets)
	require.NoError(t, err)
	assert.True(t, decision.Allowed, "a retry at the hint must be admitted")
}

// Every tier is checked before any is debited, so a request an inner tier
// refuses must not spend the outer tiers' allowance.
func TestAllowBucketsDoesNotDebitWhenAnyTierRefuses(t *testing.T) {
	server, client := useTokenBucketMiniRedis(t)
	server.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	outer := Bucket{Key: "outer", RatePerMin: 100, Capacity: 100}
	inner := Bucket{Key: "inner", RatePerMin: 1, Capacity: 1}

	decision, err := AllowBuckets(ctx, client, 1, []Bucket{outer, inner})
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	// The inner tier is now empty. Ten more attempts must all fail and must
	// leave the outer tier exactly where the one admitted request left it.
	for range 10 {
		decision, err = AllowBuckets(ctx, client, 1, []Bucket{outer, inner})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		assert.Equal(t, 1, decision.BlockedIndex, "the inner tier is the one that refused")
	}

	assert.Equal(t, "99", server.HGet("outer", "tokens"), "refused requests must not spend the outer tier")
}

// A tier configured with a non-positive rate or capacity is unlimited, which is
// how the product's "0 means unlimited" setting reaches this layer.
func TestAllowBucketsTreatsNonPositiveTierAsUnlimited(t *testing.T) {
	server, client := useTokenBucketMiniRedis(t)
	server.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	for range 50 {
		decision, err := AllowBuckets(ctx, client, 1, []Bucket{{Key: "unlimited", RatePerMin: 0, Capacity: 0}})
		require.NoError(t, err)
		require.True(t, decision.Allowed)
	}
	assert.False(t, server.Exists("unlimited"), "an unlimited tier must not create a bucket")
}

// Shrinking a limit has to take effect on the next call rather than leaving the
// old configuration's larger burst sitting in the bucket.
func TestAllowBucketsClampsToReducedCapacity(t *testing.T) {
	server, client := useTokenBucketMiniRedis(t)
	server.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	decision, err := AllowBuckets(ctx, client, 1, []Bucket{{Key: "shrink", RatePerMin: 600, Capacity: 600}})
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	// Reconfigured to 2 per minute: the bucket may hold at most 2 tokens now,
	// so only two requests get through before it is empty.
	small := []Bucket{{Key: "shrink", RatePerMin: 2, Capacity: 2}}
	for i := range 2 {
		decision, err = AllowBuckets(ctx, client, 1, small)
		require.NoError(t, err)
		require.True(t, decision.Allowed, "request %d after shrinking", i+1)
	}
	decision, err = AllowBuckets(ctx, client, 1, small)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "the previous configuration's burst must not survive")
}

// The cached SHA is only an optimisation: losing the server's script cache, as
// a Redis restart does, must not wedge the limiter.
func TestAllowBucketsRecoversFromFlushedScriptCache(t *testing.T) {
	server, client := useTokenBucketMiniRedis(t)
	server.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()

	buckets := []Bucket{{Key: "restart", RatePerMin: 60, Capacity: 60}}
	decision, err := AllowBuckets(ctx, client, 1, buckets)
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	require.NoError(t, client.ScriptFlush(ctx).Err())

	decision, err = AllowBuckets(ctx, client, 1, buckets)
	require.NoError(t, err, "a flushed script cache must be reloaded rather than surfaced as NOSCRIPT")
	assert.True(t, decision.Allowed)
}

func TestAllowBucketsRejectsInvalidInput(t *testing.T) {
	_, client := useTokenBucketMiniRedis(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		client    *redis.Client
		requested int
		buckets   []Bucket
	}{
		{name: "nil client", client: nil, requested: 1, buckets: []Bucket{{Key: "k", RatePerMin: 1, Capacity: 1}}},
		{name: "zero requested", client: client, requested: 0, buckets: []Bucket{{Key: "k", RatePerMin: 1, Capacity: 1}}},
		{name: "no buckets", client: client, requested: 1, buckets: nil},
		{name: "empty key", client: client, requested: 1, buckets: []Bucket{{Key: "", RatePerMin: 1, Capacity: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := AllowBuckets(ctx, tc.client, tc.requested, tc.buckets)
			require.Error(t, err)
			// Callers fail closed on error, so a misconfiguration must never
			// come back as an allowed request.
			assert.False(t, decision.Allowed)
		})
	}
}
