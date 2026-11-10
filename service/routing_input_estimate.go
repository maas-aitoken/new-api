package service

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/capability"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/gin-gonic/gin"
)

// routingInputEstimator 在选路阶段估算请求的输入 token 数，给能力门控的上下文窗口判定、
// auto 的分档价格探测和调度器的容量预留用。
//
// 选路发生在请求体解析成 DTO 和确定最终模型之前，所以拿不到计费用的那份精确计数。
// 它和计费共用同一套规则：数什么由各协议 DTO 的 GetTokenCountMeta 决定（含 tools 定义、
// tool_calls、role），框架开销和固定附件开销用 promptFramingTokens 与 fixedMediaTokens，
// 文本按候选模型所属的 tokenizer 家族折算。与计费不同的两点：文本一律用 tokenkit 的
// 权重估算而不跑 tiktoken（约快 7 到 10 倍，对 OpenAI 系偏差在 11% 以内），图片按默认尺寸
// 计而不下载、不解码。计费那条路径不受影响，仍然是结算的依据。
//
// 同一个请求里同一个估算器被门控、价格探测和调度器共用，请求体只解析一次。
type routingInputEstimator struct {
	c            *gin.Context
	loaded       bool
	meta         *types.TokenCountMeta
	openAIFormat bool
	textByFamily map[tokenkit.Family]int
}

func routingInputEstimatorOf(c *gin.Context) *routingInputEstimator {
	if estimator, ok := common.GetContextKeyType[*routingInputEstimator](c, constant.ContextKeyRoutingInputEstimator); ok {
		return estimator
	}
	estimator := &routingInputEstimator{c: c, textByFamily: make(map[tokenkit.Family]int)}
	common.SetContextKey(c, constant.ContextKeyRoutingInputEstimator, estimator)
	return estimator
}

// RoutingInputTokens 返回这个请求发给 modelName 时估算的输入 token 数。
// 请求体不可用、超过能力门控的体积上限、不是文本类请求或解析失败时返回 0，表示未知。
func RoutingInputTokens(c *gin.Context, modelName string) int64 {
	return routingInputEstimatorOf(c).tokens(modelName)
}

func (e *routingInputEstimator) tokens(modelName string) int64 {
	if e == nil || !e.load() {
		return 0
	}
	family := tokenkit.ModelFamily(modelName)
	text, seen := e.textByFamily[family]
	if !seen {
		text = tokenkit.EstimateFamily(family, e.meta.CombineText)
		e.textByFamily[family] = text
	}
	total := int64(text)
	if e.openAIFormat {
		total += int64(promptFramingTokens(e.meta))
	}
	for _, file := range e.meta.Files {
		if file.FileType == types.FileTypeImage {
			total += int64(tokenkit.ImageTokens(modelName, tokenkit.Image{Detail: file.Detail}))
			continue
		}
		total += int64(fixedMediaTokens(file.FileType))
	}
	return min(total, math.MaxInt32)
}

// load 只解析一次。体积上限沿用能力门控的 max_body_bytes：它的作用就是不在选路的热路径上
// 把超大请求体再解析一遍，门控关闭时同样适用，超限就当作未知。
func (e *routingInputEstimator) load() bool {
	if e.loaded {
		return e.meta != nil
	}
	e.loaded = true
	if e.c == nil || e.c.Request == nil {
		return false
	}
	format, ok := capabilityGateFormat(e.c.Request.URL.Path)
	if !ok {
		return false
	}
	storage, err := common.GetBodyStorage(e.c)
	if err != nil {
		return false
	}
	if maxBytes := capabilityGateMaxBodyBytes(); maxBytes > 0 && storage.Size() > maxBytes {
		return false
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return false
	}
	var request dto.Request
	switch format {
	case capability.FormatAnthropic:
		request = &dto.ClaudeRequest{}
	case capability.FormatOpenAIResponses:
		request = &dto.OpenAIResponsesRequest{}
	case capability.FormatGemini:
		request = &dto.GeminiChatRequest{}
	default:
		request = &dto.GeneralOpenAIRequest{}
		e.openAIFormat = true
	}
	if err := common.Unmarshal(body, request); err != nil {
		return false
	}
	e.meta = request.GetTokenCountMeta()
	return e.meta != nil
}
