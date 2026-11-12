package middleware

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 限流 Key 构造
func tokenRPMBucketKey(tokenID int) string {
	return "rateLimit:v3:token:rpm:" + strconv.Itoa(tokenID)
}

func TokenRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		rpm := 0

		// 平台默认的限流度
		if setting.TokenRequestRateLimitEnabled {
			rpm = setting.TokenRequestRateLimitCount
		}

		switch tokenRPM := common.GetContextKeyInt(c, constant.ContextKeyTokenRPM); {
		case tokenRPM < 0:
			// -1 表示 Key 显式不限流
			c.Next()
			return
		case tokenRPM > 0:
			// 该 key 有自己的上限
			rpm = tokenRPM
		}

		// 没有设置限流的直接放行
		if rpm <= 0 {
			c.Next()
			return
		}

		tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
		if tokenID <= 0 {
			logger.LogError(c.Request.Context(), common.LogText("token rate limit ran without a token id"))
			abortWithOpenAiMessage(c, http.StatusInternalServerError, i18n.T(c, i18n.MsgDatabaseError))
			return
		}

		// Redis 未启用
		// TODO: 目前没有降级处理
		if !common.RedisEnabled {
			logger.LogWarn(c.Request.Context(),
				"token %d has rpm=%d but Redis is disabled, request not rate limited", tokenID, rpm)
			c.Next()
			return
		}

		decision, err := limiter.AllowBuckets(c.Request.Context(), common.RDB, 1, []limiter.Bucket{
			{Key: tokenRPMBucketKey(tokenID), RatePerMin: rpm, Capacity: rpm},
		})
		if err != nil {
			logger.LogError(c.Request.Context(), common.LogText(
				"token rate limit check failed for token %d: %v", tokenID, err))
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDatabaseError))
			return
		}

		if !decision.Allowed {
			retryAfterSeconds := (decision.RetryAfterMs + 999) / 1000
			if retryAfterSeconds < 1 {
				retryAfterSeconds = 1
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
			abortWithOpenAiMessage(c, http.StatusTooManyRequests,
				i18n.T(c, i18n.MsgRateLimitTokenRPMReached, map[string]any{"Max": rpm}),
				types.ErrorCodeRateLimitTokenRPM)
			return
		}

		c.Next()
	}
}
