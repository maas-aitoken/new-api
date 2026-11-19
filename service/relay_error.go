package service

import (
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	// 客户端已经收到响应正文时不能再重试：下一次尝试会接着写进同一个响应，
	// Claude 客户端会收到第二个 message_start，OpenAI 客户端会把两次回答拼成一段。
	// 心跳是 SSE 注释行，客户端会忽略，所以写出的字节数要扣掉心跳。
	// 先读总字节数再读心跳数：并发的心跳先记数再写，这样只会多扣，不会把心跳当成正文。
	if c != nil {
		bodyBytes := int64(c.Writer.Size())
		if heartbeatBytes, ok := common.GetContextKeyType[*atomic.Int64](c, constant.ContextKeyStreamHeartbeatBytes); ok {
			bodyBytes -= heartbeatBytes.Load()
		}
		if bodyBytes > 0 {
			return PolicyDecision{Action: "stop", Reason: "response_started", Source: "system"}
		}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	// 上游拒答时重试：网关压着没发的拒答（upstream_refusal），以及上游直接以错误形式返回的拒答
	// （Azure 的 content_filter、Gemini 的 prompt_blocked）。它们是 400，不能靠重试状态码命中。
	if operation_setting.UpstreamRefusalRetryEnabled {
		switch err.GetErrorCode() {
		case types.ErrorCodeUpstreamRefusal, types.ErrorCodePromptBlocked, "content_filter":
			return PolicyDecision{Action: "retry", Reason: "upstream_refusal", Source: "global"}
		}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	upstreamURLs := common.UpstreamAddresses(c)
	logger.LogError(c, common.LogText("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode(upstreamURLs...))))
	modelName := c.GetString("original_model")
	if modelName == "" {
		modelName = c.GetString("model")
	}
	modelCircuitMatched := operation_setting.IsChannelModelCircuitBreakerEnabled() &&
		channelError.AutoBan && modelName != "" && IsChannelModelFailureMatch(err)
	if modelCircuitMatched {
		if RecordChannelModelFailure(channelError.ChannelId, modelName) {
			reason := err.MaskSensitiveErrorWithStatusCode(upstreamURLs...)
			gopool.Go(func() {
				DisableChannelModel(channelError.ChannelId, modelName, channelError.ChannelName, reason, err.UpstreamStatusCode(), channelError.AutoBan)
			})
		}
	} else if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode(upstreamURLs...)
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}
	if modelName != "" && !modelCircuitMatched {
		ResetChannelModelFailureConsecutive(channelError.ChannelId, modelName)
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(upstreamURLs...), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
