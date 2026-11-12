package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func countRequestTokenFor(t *testing.T, model string, format types.RelayFormat, meta *types.TokenCountMeta) int {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	tokens, err := CountRequestToken(c, meta, &relaycommon.RelayInfo{RelayFormat: format})
	require.NoError(t, err)
	return tokens
}

func TestCountRequestTokenAddsFramingAndMediaOverhead(t *testing.T) {
	previous := constant.GetMediaToken
	constant.GetMediaToken = false // 不联网量图片尺寸，图片按默认尺寸计
	t.Cleanup(func() { constant.GetMediaToken = previous })

	const model = "claude-sonnet-4-5"
	const text = "implement the cache and write tests for it"
	source := types.NewFileSourceFromData("data:image/png;base64,iVBORw0KGgo=", "")
	newMeta := func() *types.TokenCountMeta {
		return &types.TokenCountMeta{
			CombineText: text, ToolsCount: 2, MessagesCount: 3, NameCount: 1,
			Files: []*types.FileMeta{
				{FileType: types.FileTypeImage, Source: source},
				{FileType: types.FileTypeAudio, Source: source},
				{FileType: types.FileTypeVideo, Source: source},
				{FileType: types.FileTypeFile, Source: source},
				{}, // 类型未识别的附件按文件计
			},
		}
	}

	textTokens := tokenkit.Count(model, text)
	imageTokens := tokenkit.ImageTokens(model, tokenkit.Image{})
	framing := 2*8 + 3*3 + 1*3 + 3      // 每个工具 8、每条消息和每个 name 各 3，外加 3
	media := 256 + 4096*2 + 4096 + 4096 // 音频、视频、文件、未识别类型

	assert.Equal(t, textTokens+framing+imageTokens+media, countRequestTokenFor(t, model, types.RelayFormatOpenAI, newMeta()))
	assert.Equal(t, textTokens+imageTokens+media, countRequestTokenFor(t, model, types.RelayFormatClaude, newMeta()),
		"只有 OpenAI 格式有框架开销")
}

const (
	estimateProse = "The scheduler picks the cheapest channel that still satisfies the request, 调度器会在满足请求的渠道里选最便宜的那个，并且在重试时保持同一个模型不变。"
	estimateCode  = "func pickChannel(candidates []Channel) *Channel { for _, c := range candidates { if c.Healthy { return &c } }; return nil }"
	estimateImage = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
)

func estimateBody(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, err := common.Marshal(payload)
	require.NoError(t, err)
	return string(body)
}

func estimateContext(path, body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	return c
}

// estimateCases 是四种协议各一个带 system、tools、tool_calls/tool 结果和图片的 agent 形态请求。
func estimateCases(t *testing.T) []struct {
	name, path, body string
	format           types.RelayFormat
	request          dto.Request
} {
	longProse := strings.Repeat(estimateProse+" ", 40)
	longCode := strings.Repeat(estimateCode+"\n", 40)
	tool := map[string]any{"name": "read_file", "description": longProse, "parameters": map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": longProse}}}}
	return []struct {
		name, path, body string
		format           types.RelayFormat
		request          dto.Request
	}{
		{"chat", "/v1/chat/completions", estimateBody(t, map[string]any{
			"model": "x", "tools": []any{map[string]any{"type": "function", "function": tool}},
			"messages": []any{
				map[string]any{"role": "system", "content": longProse},
				map[string]any{"role": "user", "name": "alice", "content": []any{
					map[string]any{"type": "text", "text": longCode},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + estimateImage}}}},
				map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
					"id": "c1", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"` + longCode + `"}`}}}},
				map[string]any{"role": "tool", "tool_call_id": "c1", "content": longProse},
			}}), types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{}},
		{"anthropic", "/v1/messages", estimateBody(t, map[string]any{
			"model": "x", "max_tokens": 64, "system": longProse,
			"tools": []any{map[string]any{"name": "read_file", "description": longProse, "input_schema": tool["parameters"]}},
			"messages": []any{
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": longCode},
					map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": estimateImage}}}},
				map[string]any{"role": "assistant", "content": []any{map[string]any{
					"type": "tool_use", "id": "c1", "name": "read_file", "input": map[string]any{"path": longCode}}}},
				map[string]any{"role": "user", "content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": "c1", "content": longProse}}},
			}}), types.RelayFormatClaude, &dto.ClaudeRequest{}},
		{"responses", "/v1/responses", estimateBody(t, map[string]any{
			"model": "x", "instructions": longProse,
			"tools": []any{map[string]any{"type": "function", "name": "read_file", "description": longProse, "parameters": tool["parameters"]}},
			"input": []any{
				map[string]any{"type": "message", "role": "user", "content": []any{
					map[string]any{"type": "input_text", "text": longCode},
					map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + estimateImage}}},
				map[string]any{"type": "function_call", "call_id": "c1", "name": "read_file", "arguments": `{"path":"` + longCode + `"}`},
				map[string]any{"type": "function_call_output", "call_id": "c1", "output": longProse},
			}}), types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{}},
		{"gemini", "/v1beta/models/x:generateContent", estimateBody(t, map[string]any{
			"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": longProse}}},
			"tools":             []any{map[string]any{"functionDeclarations": []any{tool}}},
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{
					map[string]any{"text": longCode},
					map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": estimateImage}}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "read_file", "args": map[string]any{"path": longCode}}}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "read_file", "response": map[string]any{"content": longProse}}}}},
			}}), types.RelayFormatGemini, &dto.GeminiChatRequest{}},
	}
}

func officialInputTokens(t *testing.T, model, body string, format types.RelayFormat, request dto.Request) int {
	t.Helper()
	require.NoError(t, common.Unmarshal([]byte(body), request))
	return countRequestTokenFor(t, model, format, request.GetTokenCountMeta())
}

// 非 OpenAI 家族的官方计数本来就是 tokenkit 的权重估算，所以选路估算必须和它逐个相等：
// 数什么、框架开销、附件开销、按家族折算，四协议都是同一条规则。
func TestRoutingInputTokensMatchOfficialCountForEstimatedFamilies(t *testing.T) {
	previous := constant.GetMediaToken
	constant.GetMediaToken = false // 官方计数不联网量图片，和选路一样按默认尺寸
	t.Cleanup(func() { constant.GetMediaToken = previous })

	for _, tc := range estimateCases(t) {
		for _, model := range []string{"claude-sonnet-4-5", "claude-opus-4-7", "gemini-2.5-pro", "qwen3-coder"} {
			t.Run(tc.name+"/"+model, func(t *testing.T) {
				official := officialInputTokens(t, model, tc.body, tc.format, tc.request)
				require.Greater(t, official, 1000, "夹具要足够大，相等才有意义")
				assert.Equal(t, int64(official), RoutingInputTokens(estimateContext(tc.path, tc.body), model))
			})
		}
	}
}

// OpenAI 系官方用 tiktoken 精确计数，选路用权重估算换速度；实测偏差在 11% 以内，这里放宽到 20%。
func TestRoutingInputTokensStayCloseToTiktokenForOpenAI(t *testing.T) {
	previous := constant.GetMediaToken
	constant.GetMediaToken = false
	t.Cleanup(func() { constant.GetMediaToken = previous })

	for _, tc := range estimateCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			official := officialInputTokens(t, "gpt-5", tc.body, tc.format, tc.request)
			routing := RoutingInputTokens(estimateContext(tc.path, tc.body), "gpt-5")
			assert.InEpsilon(t, official, routing, 0.2)
		})
	}
}

// 选路估算只在请求体可用、体积在上限内、是文本类协议时有值，其余一律是 0（未知）。
func TestRoutingInputTokensIsZeroWhenTheRequestCannotBeEstimated(t *testing.T) {
	chat := estimateCases(t)[0]
	setting := operation_setting.GetCapabilityGateSetting()
	previous := setting.MaxBodyBytes
	t.Cleanup(func() { setting.MaxBodyBytes = previous })
	setting.MaxBodyBytes = operation_setting.MinCapabilityGateMaxBodyBytes

	oversized := estimateBody(t, map[string]any{"model": "x", "messages": []any{map[string]any{
		"role": "user", "content": strings.Repeat("x", int(operation_setting.MinCapabilityGateMaxBodyBytes))}}})
	for name, tc := range map[string]struct{ path, body string }{
		"body over the gate size cap": {chat.path, oversized},
		"malformed json":              {chat.path, `{"model":`},
		"empty body":                  {chat.path, ""},
		"endpoint not gated":          {"/v1/embeddings", chat.body},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Zero(t, RoutingInputTokens(estimateContext(tc.path, tc.body), "claude-sonnet-4-5"))
		})
	}
	assert.Positive(t, RoutingInputTokens(estimateContext(chat.path, chat.body), "claude-sonnet-4-5"), "对照：同一个模型在正常请求上有值")
}

// 门控原先的估算只数消息文本，agent 流量里占一半以上的 tools 定义和 tool_calls 一个都不数。
func TestRoutingInputTokensCountToolDefinitionsAndFamilies(t *testing.T) {
	chat := estimateCases(t)[0]
	var payload map[string]any
	require.NoError(t, common.Unmarshal([]byte(chat.body), &payload))
	tools, _ := common.Marshal(payload["tools"])
	delete(payload, "tools")
	withoutTools := estimateBody(t, payload)

	const model = "claude-sonnet-4-5"
	with := RoutingInputTokens(estimateContext(chat.path, chat.body), model)
	without := RoutingInputTokens(estimateContext(chat.path, withoutTools), model)
	assert.GreaterOrEqual(t, with-without, int64(tokenkit.Estimate(model, string(tools))/2),
		"带 tools 的请求要比不带的多出至少一半的工具定义文本")

	c := estimateContext(chat.path, chat.body)
	assert.Equal(t, RoutingInputTokens(c, model), RoutingInputTokens(c, "claude-3-5-haiku"), "同一家族共用折算")
	assert.Greater(t, RoutingInputTokens(c, "claude-opus-4-7"), RoutingInputTokens(c, model), "claude 4.7 的 tokenizer 切得更碎")
}
