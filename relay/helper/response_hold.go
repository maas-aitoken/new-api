package helper

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ResponseHold 压住这次尝试写给客户端的数据，让"上游拒答时重试"能把拒答丢掉再重试。
// 拒答信号（Claude stop_reason=refusal、OpenAI finish_reason=content_filter 等）在流的最后才到，
// 正文一旦发出去就收不回，所以在确定不是拒答之前先不往下写：
//   - 心跳（SSE 注释行）照常放行，客户端会忽略它，重试判断也不把它算作正文；
//   - 流式回答压到第一个正文帧：thinking、工具调用直接放行；可见文字的开头只要还和某条
//     「拒答开头文本」对得上就继续压，对不上立刻放行；
//   - 非流式回答在写之前适配器已经判完，不是拒答就在写的时候放行。
//
// 尝试结束时由 Finish 收尾：仍压着的拒答丢弃并改判为失败；尝试报错时也丢弃压着的部分，
// 让下一次尝试干净地重来；其余情况把压着的数据发出去。
type ResponseHold struct {
	gin.ResponseWriter
	info *relaycommon.RelayInfo

	mu        sync.Mutex
	holding   bool
	openings  []string // 小写的拒答开头文本
	status    int      // 压着期间设下的状态码，0 表示没设
	headerNow bool     // 压着期间调用过 WriteHeaderNow
	body      []byte
	line      []byte // 流式回答里还没凑成整行的部分
	text      strings.Builder
}

const (
	// 压着的数据超过这个大小就放行，免得异常的上游把内存撑满。
	maxHeldResponseBytes = 1 << 20
	// 回答已经包含某条拒答开头时，后面最多再压这么多字节等拒答信号；更长的回答不是固定拒答。
	maxRefusalTailBytes = 1024
	// 写进错误信息和消费日志的拒答原文最多这么多字符。
	maxHeldTextRunes = 1000
)

// heartbeatFrame 是 PingData 发的心跳。压着期间它照常发出去，不算正文。
var heartbeatFrame = []byte(": PING\n\n")

// InstallResponseHold 在请求开始时装上压住用的 writer。它必须装在 SetEventStreamHeaders 和
// StartStreamKeepAlive 之前，处在最里层：写超时、心跳和扫描器停心跳的逻辑都包在它外面，照旧工作。
func InstallResponseHold(c *gin.Context, info *relaycommon.RelayInfo) *ResponseHold {
	hold := &ResponseHold{ResponseWriter: c.Writer, info: info}
	c.Writer = hold
	return hold
}

// Begin 在每次尝试开始时调用：重新开始压，并按当前配置取一份拒答开头文本。
func (h *ResponseHold) Begin() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.holding = true
	h.openings = h.openings[:0]
	for _, opening := range operation_setting.UpstreamRefusalOpeningTexts {
		h.openings = append(h.openings, strings.ToLower(opening))
	}
	h.status, h.headerNow = 0, false
	h.body, h.line = nil, nil
	h.text.Reset()
	h.info.ResponseHeld, h.info.HeldResponseText = true, ""
}

func (h *ResponseHold) Write(data []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.holding || bytes.Equal(data, heartbeatFrame) {
		return h.ResponseWriter.Write(data)
	}
	h.body = append(h.body, data...)
	release := false
	if h.eventStream() {
		release = h.eventStreamCanRelease(data)
	} else if !h.info.PerformanceBusinessRejection {
		// 非流式回答写出时适配器已经判完：不是拒答就直接放行。
		release = true
	} else {
		h.setTextLocked(clientResponseText(h.body))
	}
	if release || len(h.body) > maxHeldResponseBytes {
		if err := h.releaseLocked(); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (h *ResponseHold) WriteString(s string) (int, error) {
	return h.Write([]byte(s))
}

func (h *ResponseHold) eventStream() bool {
	return strings.HasPrefix(h.ResponseWriter.Header().Get("Content-Type"), "text/event-stream")
}

// eventStreamCanRelease 按整行读压着的 SSE，遇到能结束压住的帧就返回 true。
func (h *ResponseHold) eventStreamCanRelease(data []byte) bool {
	h.line = append(h.line, data...)
	for {
		end := bytes.IndexByte(h.line, '\n')
		if end < 0 {
			return false
		}
		line := bytes.TrimRight(h.line[:end], "\r")
		h.line = h.line[end+1:]
		payload, ok := bytes.CutPrefix(line, []byte("data:"))
		if ok && h.frameEndsHold(bytes.TrimSpace(payload)) {
			return true
		}
	}
}

// frameEndsHold 判断一帧之后还要不要接着压：认不出的帧、thinking、工具调用都放行；
// 可见文字只要还可能是某条拒答的开头就接着压。
func (h *ResponseHold) frameEndsHold(payload []byte) bool {
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return false
	}
	frame := inspectClientFrame(payload)
	if !frame.known || frame.other {
		return true
	}
	if frame.text == "" {
		return false
	}
	h.text.WriteString(frame.text)
	h.setTextLocked(h.text.String())
	return !h.mayBeRefusal(h.text.String())
}

// mayBeRefusal：已经看到的文字是某条拒答开头的前缀（还没说完），或者以某条拒答开头起头、
// 多出的部分不长（拒答后面跟的固定说明），就还可能是拒答。
func (h *ResponseHold) mayBeRefusal(text string) bool {
	seen := strings.ToLower(strings.TrimLeft(text, " \t\r\n"))
	for _, opening := range h.openings {
		if strings.HasPrefix(opening, seen) {
			return true
		}
		if strings.HasPrefix(seen, opening) && len(seen)-len(opening) <= maxRefusalTailBytes {
			return true
		}
	}
	return false
}

func (h *ResponseHold) setTextLocked(text string) {
	if runes := []rune(text); len(runes) > maxHeldTextRunes {
		text = string(runes[:maxHeldTextRunes]) + "…"
	}
	h.info.HeldResponseText = text
}

// releaseLocked 把压着的状态码和数据发出去，之后的写入直接透传。
func (h *ResponseHold) releaseLocked() error {
	h.holding = false
	h.info.ResponseHeld = false
	if h.status > 0 {
		h.ResponseWriter.WriteHeader(h.status)
	}
	if h.headerNow {
		h.ResponseWriter.WriteHeaderNow()
	}
	body := h.body
	h.body, h.line = nil, nil
	if len(body) == 0 {
		return nil
	}
	_, err := h.ResponseWriter.Write(body)
	return err
}

func (h *ResponseHold) WriteHeader(status int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.holding {
		h.ResponseWriter.WriteHeader(status)
		return
	}
	if status > 0 {
		h.status = status
	}
}

func (h *ResponseHold) WriteHeaderNow() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.holding {
		h.ResponseWriter.WriteHeaderNow()
		return
	}
	h.headerNow = true
}

func (h *ResponseHold) Status() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding && h.status > 0 {
		return h.status
	}
	return h.ResponseWriter.Status()
}

// Flush 在压着期间不往下刷，免得提前发出响应头；只发过心跳时照常刷，心跳才能及时到客户端。
func (h *ResponseHold) Flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding && !h.ResponseWriter.Written() {
		return
	}
	h.ResponseWriter.Flush()
}

// FlushError 截住 FlushWriter 沿 Unwrap 链往下找刷新错误的过程：再往下的 http.response
// 在压着期间被刷新会直接发出 200 响应头。
func (h *ResponseHold) FlushError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding && !h.ResponseWriter.Written() {
		return nil
	}
	return http.NewResponseController(h.ResponseWriter).Flush()
}

// Unwrap 让 http.ResponseController 越过它设置写超时。
func (h *ResponseHold) Unwrap() http.ResponseWriter {
	return h.ResponseWriter
}

// Finish 在每次尝试结束、计费之后调用，返回这次尝试最终的错误：
//   - 尝试报错：丢弃压着的部分（客户端什么都没收到，重试可以干净地重来），原样返回错误；
//   - 上游拒答且回答还压着：丢弃，改判为可重试的上游拒答错误，错误信息带上游原文；
//   - 其余情况：把压着的数据发出去。
func (h *ResponseHold) Finish(c *gin.Context, apiErr *types.NewAPIError) *types.NewAPIError {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.holding {
		return apiErr
	}
	if apiErr != nil {
		h.discardLocked()
		return apiErr
	}
	if h.info.RefusedBeforeOutput() {
		message := i18n.T(c, i18n.MsgRelayUpstreamRefused)
		if text := strings.TrimSpace(h.info.HeldResponseText); text != "" {
			message += ": " + text
		}
		h.discardLocked()
		return types.NewOpenAIError(errors.New(message), types.ErrorCodeUpstreamRefusal, http.StatusBadRequest)
	}
	if h.eventStream() {
		// 压的时间可能比写超时还长，放行前按这次要写的量重新给一个写超时。
		var budget StreamWriteBudget
		_ = http.NewResponseController(h.ResponseWriter).SetWriteDeadline(budget.Deadline(time.Now(), len(h.body)))
	}
	// 回答已经结算；客户端在这时断开不影响结果。
	_ = h.releaseLocked()
	return nil
}

// discardLocked 丢掉压着的数据。还没写出任何东西时一并删掉 Content-Length，
// 免得和下一次尝试或最终错误的长度对不上。
func (h *ResponseHold) discardLocked() {
	h.holding = false
	h.info.ResponseHeld, h.info.HeldResponseText = false, ""
	h.status, h.headerNow = 0, false
	h.body, h.line = nil, nil
	h.text.Reset()
	if !h.ResponseWriter.Written() {
		h.ResponseWriter.Header().Del("Content-Length")
	}
}

// Abandon 用在 defer 里：尝试中途 panic 时丢弃压着的数据，之后的写入（如恢复中间件的 500）
// 直接发给客户端。正常结束时 Finish 已经不再压，这里什么也不做。
func (h *ResponseHold) Abandon() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holding {
		h.discardLocked()
	}
}

// clientFrame 是写给客户端的一帧流式数据里与拒答判断有关的部分。
type clientFrame struct {
	known bool   // 认得这种帧；认不出的帧直接放行，不让陌生格式卡住
	text  string // 可见文字
	other bool   // thinking、工具调用等其他正文
}

// inspectClientFrame 按形状认帧，不看请求格式：OpenAI Chat、Claude、Responses、Gemini 都可能出现。
func inspectClientFrame(payload []byte) clientFrame {
	if !gjson.ValidBytes(payload) {
		return clientFrame{}
	}
	root := gjson.ParseBytes(payload)
	if choices := root.Get("choices"); choices.IsArray() {
		frame := clientFrame{known: true}
		for _, choice := range choices.Array() {
			frame.text += choice.Get("text").String() + choice.Get("delta.content").String() + choice.Get("delta.refusal").String()
			for _, key := range []string{"delta.reasoning_content", "delta.reasoning", "delta.tool_calls", "delta.function_call", "delta.audio"} {
				frame.other = frame.other || hasContent(choice.Get(key))
			}
		}
		return frame
	}
	if root.Get("candidates").Exists() || root.Get("promptFeedback").Exists() || root.Get("usageMetadata").Exists() {
		frame := clientFrame{known: true}
		for _, candidate := range root.Get("candidates").Array() {
			for _, part := range candidate.Get("content.parts").Array() {
				if part.Get("thought").Bool() || !part.Get("text").Exists() {
					frame.other = frame.other || hasContent(part)
					continue
				}
				frame.text += part.Get("text").String()
			}
		}
		return frame
	}
	eventType := root.Get("type").String()
	if eventType == "" {
		return clientFrame{}
	}
	frame := clientFrame{known: true}
	switch eventType {
	case "content_block_start":
		block := root.Get("content_block")
		if block.Get("type").String() == "text" {
			frame.text = block.Get("text").String()
		} else {
			frame.other = true
		}
	case "content_block_delta":
		frame.text = root.Get("delta.text").String()
		frame.other = hasContent(root.Get("delta.thinking")) || hasContent(root.Get("delta.partial_json"))
	case "response.output_text.delta", "response.refusal.delta":
		frame.text = root.Get("delta").String()
	case "response.output_item.added":
		frame.other = root.Get("item.type").String() != "message"
	default:
		// 其余以 .delta 结尾的事件（推理摘要、函数参数、音频等）是其他正文；
		// message_start、ping、response.created 之类不带正文。
		frame.other = strings.HasSuffix(eventType, ".delta") && hasContent(root.Get("delta"))
	}
	return frame
}

func hasContent(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	raw := strings.TrimSpace(value.Raw)
	return raw != `""` && raw != "[]" && raw != "{}"
}

// clientResponseText 取非流式回答里的可见文字（OpenAI Chat、Claude、Responses、Gemini），
// 都没有时取错误信息，写进拒答改判后的错误和消费日志。
func clientResponseText(body []byte) string {
	if !gjson.ValidBytes(body) {
		return ""
	}
	root := gjson.ParseBytes(body)
	var text strings.Builder
	for _, choice := range root.Get("choices").Array() {
		text.WriteString(choice.Get("message.content").String())
		text.WriteString(choice.Get("message.refusal").String())
		text.WriteString(choice.Get("text").String())
	}
	for _, block := range root.Get("content").Array() {
		if block.Get("type").String() == "text" {
			text.WriteString(block.Get("text").String())
		}
	}
	for _, item := range root.Get("output").Array() {
		for _, part := range item.Get("content").Array() {
			text.WriteString(part.Get("text").String())
			text.WriteString(part.Get("refusal").String())
		}
	}
	for _, candidate := range root.Get("candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			if !part.Get("thought").Bool() {
				text.WriteString(part.Get("text").String())
			}
		}
	}
	if text.Len() == 0 {
		return root.Get("error.message").String()
	}
	return text.String()
}
