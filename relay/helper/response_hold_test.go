package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const refusalOpening = "The selected model cannot continue this conversation"

type holdCase struct {
	c        *gin.Context
	recorder *httptest.ResponseRecorder
	info     *relaycommon.RelayInfo
}

func newHoldCase(t *testing.T) (holdCase, *ResponseHold) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{}
	hold := InstallResponseHold(c, info)
	hold.Begin()
	return holdCase{c: c, recorder: recorder, info: info}, hold
}

// claude 按 Claude 流式事件写给客户端，和 ClaudeChunkData 的真实写法一致。
func (h holdCase) claude(events ...string) {
	for _, data := range events {
		ClaudeChunkData(h.c, dto.ClaudeResponse{Type: gjson.Get(data, "type").String()}, data)
	}
}

// requireNothingSent：压着期间客户端什么都没收到，响应头也没发。
func (h holdCase) requireNothingSent(t *testing.T) {
	t.Helper()
	require.Empty(t, h.recorder.Body.String())
	require.False(t, h.recorder.Flushed)
	require.False(t, h.c.Writer.Written())
}

// 拒答在客户端收到任何正文之前就要能丢掉；正常回答在第一个正文到达时放行，不等到最后。
func TestResponseHoldWithholdsRefusalsUntilFirstContent(t *testing.T) {
	require.NoError(t, i18n.Init())
	previous := operation_setting.UpstreamRefusalOpeningTexts
	operation_setting.UpstreamRefusalOpeningTexts = []string{refusalOpening}
	t.Cleanup(func() { operation_setting.UpstreamRefusalOpeningTexts = previous })

	for _, tc := range []struct {
		name        string
		write       func(t *testing.T, h holdCase)
		handlerErr  *types.NewAPIError
		refused     bool
		refusalText string
		wantBody    []string
	}{
		{name: "claude refusal without text", refused: true, write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"message_start","message":{"id":"m"}}`)
			h.info.PerformanceBusinessRejection = true
			h.claude(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, `{"type":"message_stop"}`)
			h.requireNothingSent(t)
		}},
		{name: "claude refusal text split across deltas", refused: true, refusalText: refusalOpening + ". Please start a new conversation.", write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"message_start","message":{"id":"m"}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The selected model "}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"cannot continue this conversation. Please start a new conversation."}}`)
			h.requireNothingSent(t)
			h.info.PerformanceBusinessRejection = true
			h.claude(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, `{"type":"message_stop"}`)
		}},
		{name: "normal claude answer is released at its first text", wantBody: []string{"message_start", "Hello"}, write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"message_start","message":{"id":"m"}}`)
			h.requireNothingSent(t)
			h.claude(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
			require.Contains(t, h.recorder.Body.String(), "Hello")
		}},
		{name: "answer sharing the first word is released once it diverges", wantBody: []string{`"text":"The"`, `"text":" answer is 4"`}, write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The"}}`)
			h.requireNothingSent(t)
			h.claude(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" answer is 4"}}`)
			require.Contains(t, h.recorder.Body.String(), `"text":" answer is 4"`)
		}},
		{name: "thinking is released at once", wantBody: []string{"thinking"}, write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
			require.Contains(t, h.recorder.Body.String(), "thinking")
		}},
		{name: "openai content_filter before any text", refused: true, write: func(t *testing.T, h holdCase) {
			require.NoError(t, StringData(h.c, `{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`))
			h.info.PerformanceBusinessRejection = true
			require.NoError(t, StringData(h.c, `{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`))
			require.NoError(t, StringData(h.c, `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0}}`))
			Done(h.c)
			h.requireNothingSent(t)
		}},
		{name: "gemini prompt blocked", refused: true, write: func(t *testing.T, h holdCase) {
			h.info.PerformanceBusinessRejection = true
			require.NoError(t, StringData(h.c, `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":3}}`))
			h.requireNothingSent(t)
		}},
		{name: "responses created event waits for the first delta", wantBody: []string{"response.created", "Hi"}, write: func(t *testing.T, h holdCase) {
			require.NoError(t, ResponseChunkData(h.c, dto.ResponsesStreamResponse{Type: "response.created"}, `{"type":"response.created","response":{"id":"r"}}`))
			h.requireNothingSent(t)
			require.NoError(t, ResponseChunkData(h.c, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, `{"type":"response.output_text.delta","delta":"Hi"}`))
			require.Contains(t, h.recorder.Body.String(), "Hi")
		}},
		{name: "non-stream body is released when written", wantBody: []string{"chatcmpl-1"}, write: func(t *testing.T, h holdCase) {
			h.c.JSON(http.StatusOK, gin.H{"id": "chatcmpl-1", "choices": []gin.H{{"message": gin.H{"content": "Hi"}}}})
			require.Contains(t, h.recorder.Body.String(), "chatcmpl-1")
		}},
		{name: "non-stream refusal keeps its text", refused: true, refusalText: "I can't help with that.", write: func(t *testing.T, h holdCase) {
			h.info.PerformanceBusinessRejection = true
			h.c.JSON(http.StatusOK, gin.H{"choices": []gin.H{{"message": gin.H{"content": "I can't help with that."}, "finish_reason": "content_filter"}}})
			h.requireNothingSent(t)
		}},
		{name: "attempt error discards the held opening", handlerErr: types.NewOpenAIError(errors.New("overloaded"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), write: func(t *testing.T, h holdCase) {
			h.claude(`{"type":"message_start","message":{"id":"m"}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, hold := newHoldCase(t)
			tc.write(t, h)
			apiErr := hold.Finish(h.c, tc.handlerErr)
			switch {
			case tc.refused:
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeUpstreamRefusal, apiErr.GetErrorCode())
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				want := i18n.T(h.c, i18n.MsgRelayUpstreamRefused)
				if tc.refusalText != "" {
					want += ": " + tc.refusalText
				}
				assert.Equal(t, want, apiErr.Error())
				assert.Empty(t, h.recorder.Body.String(), "a refused answer never reaches the client")
			case tc.handlerErr != nil:
				assert.Same(t, tc.handlerErr, apiErr)
				assert.Empty(t, h.recorder.Body.String())
				assert.False(t, h.c.Writer.Written(), "nothing was sent, so the request can still be retried")
			default:
				require.Nil(t, apiErr)
				for _, part := range tc.wantBody {
					assert.Contains(t, h.recorder.Body.String(), part)
				}
			}
			assert.False(t, h.info.ResponseHeld)
		})
	}
}

// 压着期间心跳照常发出；拒答改判后客户端只收到过心跳，重试判断仍然放行，下一次尝试的回答照常发出。
func TestResponseHoldPassesHeartbeatsAndRetriesAfterRefusal(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousEnabled, previousOpenings := operation_setting.UpstreamRefusalRetryEnabled, operation_setting.UpstreamRefusalOpeningTexts
	operation_setting.UpstreamRefusalRetryEnabled, operation_setting.UpstreamRefusalOpeningTexts = true, []string{refusalOpening}
	t.Cleanup(func() {
		operation_setting.UpstreamRefusalRetryEnabled, operation_setting.UpstreamRefusalOpeningTexts = previousEnabled, previousOpenings
	})

	h, hold := newHoldCase(t)
	SetEventStreamHeaders(h.c)
	require.NoError(t, PingData(h.c))
	require.Equal(t, string(heartbeatFrame), h.recorder.Body.String(), "heartbeats reach the client while the answer is held")
	h.claude(`{"type":"message_start","message":{"id":"m"}}`)
	h.info.PerformanceBusinessRejection = true
	h.claude(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`)
	apiErr := hold.Finish(h.c, nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, service.PolicyDecision{Action: "retry", Reason: "upstream_refusal", Source: "global"}, service.DecideRelayRetry(h.c, apiErr, 1))

	h.info.PerformanceBusinessRejection = false
	hold.Begin()
	h.claude(`{"type":"message_start","message":{"id":"m2"}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)
	require.Nil(t, hold.Finish(h.c, nil))
	body := h.recorder.Body.String()
	assert.Contains(t, body, `"id":"m2"`)
	assert.NotContains(t, body, `"id":"m"}`, "the refused attempt's opening never reached the client")
	assert.NotContains(t, body, "refusal")
}

// 尝试中途 panic 时丢弃压着的数据，恢复中间件写的错误照常发给客户端。
func TestResponseHoldAbandonLetsTheRecoveryResponseThrough(t *testing.T) {
	h, hold := newHoldCase(t)
	h.claude(`{"type":"message_start","message":{"id":"m"}}`)
	hold.Abandon()
	h.c.AbortWithStatus(http.StatusInternalServerError)
	assert.Equal(t, http.StatusInternalServerError, h.recorder.Code)
	assert.NotContains(t, h.recorder.Body.String(), "message_start")
}
