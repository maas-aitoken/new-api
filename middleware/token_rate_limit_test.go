package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useTokenRateLimitSetting applies the platform-level configuration for one test
// and restores whatever the process had before.
func useTokenRateLimitSetting(t *testing.T, enabled bool, count int) {
	t.Helper()
	previousEnabled, previousCount := setting.TokenRequestRateLimitEnabled, setting.TokenRequestRateLimitCount
	t.Cleanup(func() {
		setting.TokenRequestRateLimitEnabled, setting.TokenRequestRateLimitCount = previousEnabled, previousCount
	})
	setting.TokenRequestRateLimitEnabled, setting.TokenRequestRateLimitCount = enabled, count
}

// newTokenRateLimitRouter presents a fixed token identity to TokenRateLimit,
// standing in for TokenAuth. A tokenID of 0 leaves the id unset, as it would be
// if the middleware ran before authentication.
func newTokenRateLimitRouter(tokenID, tokenRPM int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/limited", func(c *gin.Context) {
		if tokenID != 0 {
			common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
		}
		common.SetContextKey(c, constant.ContextKeyTokenRPM, tokenRPM)
	}, TokenRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return router
}

func callTokenRateLimit(router http.Handler) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/limited", nil))
	return recorder
}

// The key's own limit replaces the platform default rather than adding to it,
// and -1 opts the key out of limiting entirely.
func TestTokenRateLimitResolvesConfigurationPrecedence(t *testing.T) {
	require.NoError(t, i18n.Init())

	for _, tc := range []struct {
		name            string
		platformEnabled bool
		platformCount   int
		tokenRPM        int
		// admitted is how many requests get through before one is refused;
		// -1 means the key is not limited at all.
		admitted int
	}{
		{name: "platform default applies to a key with no limit", platformEnabled: true, platformCount: 3, tokenRPM: 0, admitted: 3},
		{name: "key limit replaces a larger platform default", platformEnabled: true, platformCount: 6, tokenRPM: 2, admitted: 2},
		{name: "key limit replaces a smaller platform default", platformEnabled: true, platformCount: 2, tokenRPM: 5, admitted: 5},
		{name: "negative key limit opts out of the platform default", platformEnabled: true, platformCount: 3, tokenRPM: -1, admitted: -1},
		{name: "nothing configured leaves the key unlimited", platformEnabled: true, platformCount: 0, tokenRPM: 0, admitted: -1},
		{name: "platform switch off drops only the platform default", platformEnabled: false, platformCount: 100, tokenRPM: 0, admitted: -1},
		// The switch governs the platform default; a key configured on purpose
		// keeps its own limit regardless of it.
		{name: "platform switch off keeps the key's own limit", platformEnabled: false, platformCount: 100, tokenRPM: 2, admitted: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redisServer, _ := useRateLimitMiniRedis(t)
			redisServer.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			useTokenRateLimitSetting(t, tc.platformEnabled, tc.platformCount)

			router := newTokenRateLimitRouter(4242, tc.tokenRPM)
			if tc.admitted < 0 {
				// A bucket would be exhausted well within this many requests.
				for i := range 20 {
					require.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code, "request %d", i+1)
				}
				assert.False(t, redisServer.Exists(tokenRPMBucketKey(4242)), "an unlimited key must not create a bucket")
				return
			}

			for i := range tc.admitted {
				assert.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code, "request %d of the burst", i+1)
			}
			assert.Equal(t, http.StatusTooManyRequests, callTokenRateLimit(router).Code, "request %d must be refused", tc.admitted+1)
		})
	}
}

// A refused request has to carry both a Retry-After the client can act on and a
// code that tells it apart from an upstream provider's own 429.
func TestTokenRateLimitRejectionCarriesRetryHintAndCode(t *testing.T) {
	require.NoError(t, i18n.Init())
	redisServer, _ := useRateLimitMiniRedis(t)
	redisServer.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	useTokenRateLimitSetting(t, true, 6)

	router := newTokenRateLimitRouter(42, 0)
	for i := range 6 {
		require.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code, "request %d of the burst", i+1)
	}

	limited := callTokenRateLimit(router)
	assert.Equal(t, http.StatusTooManyRequests, limited.Code)
	// Capacity is RPM, so an empty bucket needs a full 1/rate to earn one
	// token: 1 / (6/60000) = 10000ms, rounded up to 10s.
	assert.Equal(t, "10", limited.Header().Get("Retry-After"))
	assert.Contains(t, limited.Body.String(), "rate_limit:token_rpm")

	// The bucket must expire so a deleted token cannot leave its hash behind.
	assert.Equal(t, 120*time.Second, redisServer.TTL(tokenRPMBucketKey(42)))
}

// Refilling follows the Redis clock, and honouring the returned hint has to
// succeed rather than earn a second rejection.
func TestTokenRateLimitRefillsAfterRetryAfterElapses(t *testing.T) {
	require.NoError(t, i18n.Init())
	redisServer, _ := useRateLimitMiniRedis(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	redisServer.SetTime(base)
	useTokenRateLimitSetting(t, true, 6)

	router := newTokenRateLimitRouter(7, 0)
	for range 6 {
		require.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code)
	}
	require.Equal(t, http.StatusTooManyRequests, callTokenRateLimit(router).Code)

	// One second short of the hint the bucket is still below one token.
	redisServer.SetTime(base.Add(9 * time.Second))
	assert.Equal(t, http.StatusTooManyRequests, callTokenRateLimit(router).Code)

	redisServer.SetTime(base.Add(10 * time.Second))
	assert.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code)

	// A full minute of idling restores the burst without exceeding it.
	redisServer.SetTime(base.Add(80 * time.Second))
	for i := range 6 {
		assert.Equal(t, http.StatusNoContent, callTokenRateLimit(router).Code, "refilled request %d", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, callTokenRateLimit(router).Code)
}

func TestTokenRateLimitScopesBucketsPerToken(t *testing.T) {
	require.NoError(t, i18n.Init())
	redisServer, _ := useRateLimitMiniRedis(t)
	redisServer.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	useTokenRateLimitSetting(t, true, 1)

	first := newTokenRateLimitRouter(101, 0)
	second := newTokenRateLimitRouter(102, 0)

	require.Equal(t, http.StatusNoContent, callTokenRateLimit(first).Code)
	assert.Equal(t, http.StatusTooManyRequests, callTokenRateLimit(first).Code)
	// The second key has its own allowance.
	assert.Equal(t, http.StatusNoContent, callTokenRateLimit(second).Code)
}

// A Redis fault must only cost availability to keys that carry a limit. An
// unlimited key never reaches Redis, so it has to survive the outage.
func TestTokenRateLimitFailsClosedOnlyForLimitedKeys(t *testing.T) {
	require.NoError(t, i18n.Init())

	for _, tc := range []struct {
		name          string
		platformCount int
		tokenRPM      int
		stopRedis     bool
		redisEnabled  bool
		want          int
	}{
		{name: "limited key fails closed on redis outage", platformCount: 5, tokenRPM: 0, stopRedis: true, redisEnabled: true, want: http.StatusServiceUnavailable},
		{name: "unlimited key survives redis outage", platformCount: 0, tokenRPM: 0, stopRedis: true, redisEnabled: true, want: http.StatusNoContent},
		{name: "opted-out key survives redis outage", platformCount: 5, tokenRPM: -1, stopRedis: true, redisEnabled: true, want: http.StatusNoContent},
		{name: "limited key passes when redis is disabled", platformCount: 5, tokenRPM: 0, redisEnabled: false, want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.redisEnabled {
				redisServer, _ := useRateLimitMiniRedis(t)
				redisServer.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
				if tc.stopRedis {
					redisServer.Close()
				}
			} else {
				previous := common.RedisEnabled
				common.RedisEnabled = false
				t.Cleanup(func() { common.RedisEnabled = previous })
			}
			useTokenRateLimitSetting(t, true, tc.platformCount)

			assert.Equal(t, tc.want, callTokenRateLimit(newTokenRateLimitRouter(55, tc.tokenRPM)).Code)
		})
	}
}

// Running before TokenAuth must be loud: without a token id every caller would
// otherwise share one bucket and see rejections that look random.
func TestTokenRateLimitRejectsMissingTokenID(t *testing.T) {
	require.NoError(t, i18n.Init())
	redisServer, _ := useRateLimitMiniRedis(t)
	redisServer.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	useTokenRateLimitSetting(t, true, 5)

	assert.Equal(t, http.StatusInternalServerError, callTokenRateLimit(newTokenRateLimitRouter(0, 0)).Code)
}
