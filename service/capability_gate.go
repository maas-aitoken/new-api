package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/service/capability"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"

	"github.com/gin-gonic/gin"
)

// CapabilityGateMode 决定能力门控如何参与路由。
type CapabilityGateMode string

const (
	CapabilityGateOff     CapabilityGateMode = "off"
	CapabilityGateShadow  CapabilityGateMode = "shadow"
	CapabilityGateEnforce CapabilityGateMode = "enforce"
)

// CurrentCapabilityGateMode 读取系统设置里的门控模式（默认 shadow）。
// 模式只存在数据库里，没有环境变量开关。
func CurrentCapabilityGateMode() CapabilityGateMode {
	switch operation_setting.GetCapabilityGateSetting().Mode {
	case operation_setting.CapabilityGateModeOff:
		return CapabilityGateOff
	case operation_setting.CapabilityGateModeEnforce:
		return CapabilityGateEnforce
	default:
		return CapabilityGateShadow
	}
}

// CapabilityGate 是请求级能力闸门。
//
// auto 选模时按"模型+渠道"判定：候选模型至少有一个渠道的生效能力满足请求才可选
// （AllowsAnyChannel，只判定不留痕）；选定模型后对它的候选渠道再合取一次并留痕
// （FilterChannels），结果同时注册为 FilterCapability 约束，重试沿用同一条过滤链。
// 两处共用同一个 judgeChannels，所以通过选模的模型不会在渠道收窄后变成零渠道。
// 解析失败、请求体不可用或未声明任何能力时返回 nil，调用方按"不拦截"处理：
// 宁可漏掉一次约束，也不能让门控抖动把流量打挂。
type CapabilityGate struct {
	mode  CapabilityGateMode
	needs *capability.Needs
	// estimator 给声明了上下文窗口的候选估算输入长度，同一个请求里与价格探测、调度器共用。
	// 为 nil 时不做上下文窗口判定。
	estimator *routingInputEstimator

	modelVerdicts   map[string]capabilityVerdict
	modelRejections map[string]capabilityRejection
	evaluated       int
	wouldReject     []string
	rejected        []string

	channelModel      string
	channelsEvaluated int
	channelsRejected  []int
	channelRejections map[int]capabilityRejection
}

type capabilityRejection struct {
	undeclared bool
	violations []capability.Violation
}

type capabilityVerdict struct {
	satisfied bool
	// kept 是通过判定的渠道，auto 估算首次落到哪个渠道时用。
	kept []int
}

// CapabilityGateSummary 是写进日志的门控摘要。
type CapabilityGateSummary struct {
	Mode              CapabilityGateMode `json:"mode"`
	Evaluated         int                `json:"evaluated"`
	WouldReject       []string           `json:"would_reject,omitempty"`
	Rejected          []string           `json:"rejected,omitempty"`
	ChannelModel      string             `json:"channel_model,omitempty"`
	ChannelsEvaluated int                `json:"channels_evaluated,omitempty"`
	ChannelsRejected  []int              `json:"channels_rejected,omitempty"`
}

// BuildCapabilityGate 解析一次请求体并构造闸门。非四协议入口返回 nil。
func BuildCapabilityGate(c *gin.Context) *CapabilityGate {
	mode := CurrentCapabilityGateMode()
	if mode == CapabilityGateOff {
		return nil
	}
	if !model.HasCapabilityDeclarations() {
		return nil
	}
	format, ok := capabilityGateFormat(c.Request.URL.Path)
	if !ok {
		return nil
	}
	body, ok := capabilityGateBody(c, capabilityGateMaxBodyBytes())
	if !ok {
		return nil
	}
	needs, err := capability.Extract(format, body)
	if err != nil {
		logger.LogWarn(c.Request.Context(), common.LogText("capability gate skipped: request body could not be parsed: %v", err))
		return nil
	}
	gate := &CapabilityGate{
		mode:          mode,
		needs:         needs,
		estimator:     routingInputEstimatorOf(c),
		modelVerdicts: make(map[string]capabilityVerdict),
	}
	common.SetContextKey(c, constant.ContextKeyCapabilityGate, gate)
	return gate
}

// CapabilityGateFromContext 返回本请求构建的闸门。
func CapabilityGateFromContext(c *gin.Context) *CapabilityGate {
	gate, ok := common.GetContextKeyType[*CapabilityGate](c, constant.ContextKeyCapabilityGate)
	if !ok {
		return nil
	}
	return gate
}

// capabilityBaseModel 是能力声明的查找名：去掉推理档位后缀，声明和渠道收窄都按它记。
func capabilityBaseModel(modelName string) string {
	if baseModel := reasoning.BaseModelName(modelName); baseModel != "" {
		return baseModel
	}
	return modelName
}

// AllowsAnyChannel 返回 auto 候选模型是否至少有一个渠道满足请求。
//
// 判定口径与选定模型后的 FilterChannels 完全相同（共用 judgeChannels），
// 所以通过这里的模型不会在后面被渠道级收窄淘汰到零渠道。
// 这里只判定、不写门控事件：事件只给最终选中的那个模型写一条。
// 没有模型级声明视为不合格；shadow 模式只记录，不拦截。
//
// ignoreClientReasoning 为 true 时撇开客户端自己写的推理字段来判定：
// 可信的 auto 判断会自己定档位并改写这些字段，用客户端原来的档位去淘汰候选没有意义。
// 结论按（候选名，是否撇开推理）缓存，同一个请求里同一个候选的渠道集合不会变。
func (g *CapabilityGate) AllowsAnyChannel(modelName string, channelIDs []int, ignoreClientReasoning bool) bool {
	if g == nil {
		return true
	}
	if g.modelVerdicts == nil {
		g.modelVerdicts = make(map[string]capabilityVerdict)
	}
	if g.modelRejections == nil {
		g.modelRejections = make(map[string]capabilityRejection)
	}
	needs, memoKey := g.needs, modelName
	if ignoreClientReasoning {
		needs, memoKey = g.withoutClientReasoning(), modelName+"|auto-reasoning"
	}
	verdict, seen := g.modelVerdicts[memoKey]
	if !seen {
		judgement := g.judgeChannels(modelName, channelIDs, needs)
		verdict = capabilityVerdict{satisfied: len(judgement.kept) > 0, kept: judgement.kept}
		g.modelVerdicts[memoKey] = verdict
		g.evaluated++
		if !verdict.satisfied {
			// 每个渠道都因为同一个原因没声明时才算"未声明"，否则把各渠道的违规维度并起来。
			rejection := capabilityRejection{undeclared: len(judgement.rejections) > 0}
			for _, each := range judgement.rejections {
				if !each.undeclared {
					rejection.undeclared = false
					rejection.violations = append(rejection.violations, each.violations...)
				}
			}
			g.modelRejections[memoKey] = rejection
			g.wouldReject = append(g.wouldReject, judgement.baseModel)
		}
	}
	if verdict.satisfied || g.mode != CapabilityGateEnforce {
		return true
	}
	g.rejected = append(g.rejected, capabilityBaseModel(modelName))
	return false
}

// KeptChannels 返回这个模型的候选渠道里，能力门控在选定模型后会留下的那些：enforce 模式下
// 是通过判定的渠道（与 AllowsAnyChannel 同一个口径、同一个备忘），其余模式不收窄，原样返回。
// 只读：不写门控事件、不计入评估次数。auto 估算候选模型首次落到哪个渠道时用它。
func (g *CapabilityGate) KeptChannels(modelName string, channelIDs []int, ignoreClientReasoning bool) []int {
	if g == nil || g.mode != CapabilityGateEnforce {
		return channelIDs
	}
	needs, memoKey := g.needs, modelName
	if ignoreClientReasoning {
		needs, memoKey = g.withoutClientReasoning(), modelName+"|auto-reasoning"
	}
	if verdict, seen := g.modelVerdicts[memoKey]; seen {
		return verdict.kept
	}
	return g.judgeChannels(modelName, channelIDs, needs).kept
}

// withoutClientReasoning 返回撇开客户端推理字段的诉求副本，推理维度不参与判定。
func (g *CapabilityGate) withoutClientReasoning() *capability.Needs {
	if g.needs == nil {
		return nil
	}
	needs := *g.needs
	needs.ReasoningEnabled, needs.ReasoningEffort, needs.ReasoningBudget = nil, "", nil
	return &needs
}

// OverrideReasoning 让后面的 FilterChannels 按 auto 写进请求体的推理结果判定，
// 而不是客户端原来写的字段。none 要显式记成档位 none（并关闭思考），
// 这样 mandatory 和 efforts 集合会真正拿它校验；Anthropic 的 disabled 重新解析不出档位。
func (g *CapabilityGate) OverrideReasoning(action autorouter.EffortAction, effort string) {
	if g == nil || g.needs == nil || action == autorouter.EffortKeep {
		return
	}
	needs := g.withoutClientReasoning()
	if action == autorouter.EffortSet {
		enabled := effort != "none"
		needs.ReasoningEnabled, needs.ReasoningEffort = &enabled, effort
	}
	g.needs = needs
}

// EffortSupport 汇总选中模型在候选渠道上的推理声明，用于把期望档位收敛到合法档位。
// 渠道集合先按"撇开推理后其余诉求都满足"的渠道收窄，避免挑出一个只有被淘汰的渠道才支持的档位。
// 只读声明，不依赖门控模式；g 为 nil（门控关闭）时按全部候选渠道汇总。
func (g *CapabilityGate) EffortSupport(modelName string, channelIDs []int) autorouter.EffortSupport {
	baseModel := capabilityBaseModel(modelName)
	if g != nil {
		if kept := g.judgeChannels(modelName, channelIDs, g.withoutClientReasoning()).kept; len(kept) > 0 {
			channelIDs = kept
		}
	}
	var support autorouter.EffortSupport
	for _, channelID := range channelIDs {
		effective, ok := model.EffectiveChannelCapability(channelID, baseModel)
		if !ok {
			continue
		}
		reasoning := effective.Reasoning
		support.Add(autorouter.ChannelReasoning{
			Declared:    reasoning.Declared(),
			Unsupported: reasoning.Supported != nil && !*reasoning.Supported,
			Mandatory:   reasoning.Mandatory != nil && *reasoning.Mandatory,
			Efforts:     reasoning.Efforts,
		})
	}
	return support
}

// FilterChannels 在选定模型后对候选渠道做一次能力合取，并写一条门控事件。
// shadow 模式只记录应被剔除的渠道，enforce 模式返回过滤后的集合。
// 只有 soft 违规（采样参数越界）的渠道仍然保留，但会写进门控事件供排查。
func (g *CapabilityGate) FilterChannels(c *gin.Context, modelName string, channelIDs []int) []int {
	if g == nil || len(channelIDs) == 0 {
		return channelIDs
	}
	judgement := g.judgeChannels(modelName, channelIDs, g.needs)
	g.channelModel = judgement.baseModel
	g.channelsEvaluated = len(channelIDs)
	g.channelsRejected = judgement.rejected
	g.channelRejections = judgement.rejections
	recordCapabilityGateEvent(c, g, judgement.baseModel, len(channelIDs), judgement.rejected, judgement.rejections, judgement.soft)
	if g.mode != CapabilityGateEnforce {
		return channelIDs
	}
	return judgement.kept
}

// channelJudgement 是对一个模型的候选渠道逐个判定的结果。
type channelJudgement struct {
	baseModel  string
	kept       []int
	rejected   []int
	rejections map[int]capabilityRejection
	// soft 是通过判定但仍越界的"仅记录"维度。
	soft map[string]capability.Violation
}

// judgeChannels 逐个渠道判定"模型级声明被该渠道收窄后"的生效能力能否满足请求。
// 它不看门控模式、不写事件，模式如何处置由调用方决定。
func (g *CapabilityGate) judgeChannels(modelName string, channelIDs []int, needs *capability.Needs) channelJudgement {
	baseModel := capabilityBaseModel(modelName)
	_, declared := model.ResolveModelCapability(baseModel)
	judgement := channelJudgement{
		baseModel:  baseModel,
		kept:       make([]int, 0, len(channelIDs)),
		rejected:   make([]int, 0),
		rejections: make(map[int]capabilityRejection, len(channelIDs)),
		soft:       make(map[string]capability.Violation),
	}
	for _, channelID := range channelIDs {
		if !declared {
			judgement.rejected = append(judgement.rejected, channelID)
			judgement.rejections[channelID] = capabilityRejection{undeclared: true}
			continue
		}
		effective, ok := model.EffectiveChannelCapability(channelID, baseModel)
		if !ok {
			judgement.rejected = append(judgement.rejected, channelID)
			judgement.rejections[channelID] = capabilityRejection{undeclared: true}
			continue
		}
		// 上下文窗口是按这个模型自己的 tokenizer 声明的，所以输入长度也按它的家族估算；
		// 只有声明了窗口才估算，没声明的模型不必为此解析请求体。
		judged := needs
		if needs != nil && g.estimator != nil && effective.Context.MaxInputTokens != nil {
			withEstimate := *needs
			withEstimate.EstimatedInputTokens = g.estimator.tokens(modelName)
			judged = &withEstimate
		}
		violations := normalizeGateViolations(capability.Evaluate(effective, judged))
		if !capability.Satisfied(violations) {
			judgement.rejected = append(judgement.rejected, channelID)
			judgement.rejections[channelID] = capabilityRejection{violations: violations}
			continue
		}
		for _, violation := range violations {
			judgement.soft[violation.Dimension] = violation
		}
		judgement.kept = append(judgement.kept, channelID)
	}
	return judgement
}

// capabilityGateEventAction 把一次判定归类：
// 只有"剩余可用渠道为 0"才是真正的拒绝；淘汰了部分渠道但仍有可用渠道时，
// 请求是被过滤后成功的，不能标成 rejected。
func capabilityGateEventAction(mode CapabilityGateMode, evaluated, rejected, soft int) string {
	if rejected == 0 {
		if soft > 0 {
			return model.CapabilityGateEventSoft
		}
		return model.CapabilityGateEventPassed
	}
	kept := evaluated - rejected
	if mode == CapabilityGateEnforce {
		if kept == 0 {
			return model.CapabilityGateEventRejected
		}
		return model.CapabilityGateEventFiltered
	}
	if kept == 0 {
		return model.CapabilityGateEventWouldReject
	}
	return model.CapabilityGateEventWouldFilter
}

// normalizeGateViolations 按系统设置里的"仅记录维度"重算严重度。
// 命中清单的维度（可以是组，也可以是单个维度）一律 soft，其余 hard。
func normalizeGateViolations(violations []capability.Violation) []capability.Violation {
	if len(violations) == 0 {
		return nil
	}
	recordOnly := operation_setting.CapabilityGateRecordOnlyDimensions()
	out := make([]capability.Violation, 0, len(violations))
	for _, violation := range violations {
		normalized := violation
		normalized.Severity = capability.SeverityHard
		for _, prefix := range recordOnly {
			if violation.Dimension == prefix || strings.HasPrefix(violation.Dimension, prefix+".") {
				normalized.Severity = capability.SeveritySoft
				break
			}
		}
		out = append(out, normalized)
	}
	return out
}

// recordCapabilityGateEvent 把一次渠道级判定交给异步 writer 落库。
// 设置里允许记录通过时，每个请求都会留痕（含多渠道命中里部分渠道不满足的情况）；
// 这里只做内存里的组装和非阻塞投递，落库失败或队列满都不影响本次请求的选路。
func recordCapabilityGateEvent(c *gin.Context, g *CapabilityGate, modelName string, evaluated int, rejected []int, rejections map[int]capabilityRejection, soft map[string]capability.Violation) {
	writer := capabilityGateEvents.Load()
	if writer == nil {
		return
	}
	action := capabilityGateEventAction(g.mode, evaluated, len(rejected), len(soft))
	if action == model.CapabilityGateEventPassed && !operation_setting.GetCapabilityGateSetting().RecordPassed {
		return
	}

	dimensions := make(map[string]capability.Violation, len(soft)+len(rejections))
	for dimension, violation := range soft {
		dimensions[dimension] = violation
	}
	undeclared := false
	for _, rejection := range rejections {
		if rejection.undeclared {
			undeclared = true
			continue
		}
		for _, violation := range rejection.violations {
			if _, exists := dimensions[violation.Dimension]; !exists {
				dimensions[violation.Dimension] = violation
			}
		}
	}
	if undeclared {
		dimensions["model.declaration"] = capability.Violation{
			Dimension: "model.declaration",
			Required:  true,
			Supported: false,
			Severity:  capability.SeverityHard,
		}
	}
	names := make([]string, 0, len(dimensions))
	for dimension := range dimensions {
		names = append(names, dimension)
	}
	sort.Strings(names)
	list := make([]capability.Violation, 0, len(names))
	for _, name := range names {
		list = append(list, dimensions[name])
	}
	encoded, err := common.Marshal(list)
	if err != nil {
		common.SysError("failed to encode capability gate event: " + err.Error())
		return
	}
	channelText := ""
	if len(rejected) > 0 {
		ids := make([]string, 0, len(rejected))
		for _, channelID := range rejected {
			ids = append(ids, strconv.Itoa(channelID))
		}
		channelText = strings.Join(ids, ",")
		// 列宽有限，超出时在逗号处截断；准确数量由 RejectedCount 记录。
		if len(channelText) > model.CapabilityGateEventRejectedChannelsMax {
			channelText = channelText[:strings.LastIndex(channelText[:model.CapabilityGateEventRejectedChannelsMax+1], ",")]
		}
	}
	writer.enqueue(&model.CapabilityGateEvent{
		Mode:              string(g.mode),
		Model:             modelName,
		Action:            action,
		ChannelsEvaluated: evaluated,
		RejectedChannels:  channelText,
		Dimensions:        string(encoded),
		CreatedAt:         common.GetTimestamp(),
		RejectedCount:     len(rejected),
		RequestId:         c.GetString(common.RequestIdKey),
		UserId:            common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenId:           common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		UsingGroup:        common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
	})
}

// ExplainChannelRejection 把本次请求的渠道级淘汰原因整理成面向调用方的错误。
// 只描述 hard 违规维度；未声明模型会明确提示缺少模型级声明。
func (g *CapabilityGate) ExplainChannelRejection(modelName string) string {
	if g == nil || len(g.channelRejections) == 0 {
		return fmt.Sprintf("no available channel for model %q satisfies the request capabilities", modelName)
	}
	undeclared := 0
	dimensions := make(map[string]string)
	for _, rejection := range g.channelRejections {
		if rejection.undeclared {
			undeclared++
			continue
		}
		for _, violation := range rejection.violations {
			if violation.Severity != capability.SeverityHard {
				continue
			}
			if _, exists := dimensions[violation.Dimension]; exists {
				continue
			}
			dimensions[violation.Dimension] = describeCapabilityViolation(violation)
		}
	}
	if len(dimensions) == 0 {
		if undeclared > 0 {
			return fmt.Sprintf("model %q has no model-level capability declaration, so no available channel can be validated", modelName)
		}
		return fmt.Sprintf("no available channel for model %q satisfies the request capabilities", modelName)
	}
	names := make([]string, 0, len(dimensions))
	for dimension := range dimensions {
		names = append(names, dimension)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = append(names[:8], fmt.Sprintf("and %d more", len(names)-8))
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, dimensions[name])
	}
	return fmt.Sprintf("no available channel for model %q satisfies the request: %s", modelName, strings.Join(parts, "; "))
}

func describeCapabilityViolation(violation capability.Violation) string {
	if violation.Required == nil {
		return violation.Dimension
	}
	if violation.Supported == nil {
		return fmt.Sprintf("%s (requested %s)", violation.Dimension, formatCapabilityValue(violation.Required))
	}
	return fmt.Sprintf("%s (requested %s, supported %s)",
		violation.Dimension, formatCapabilityValue(violation.Required), formatCapabilityValue(violation.Supported))
}

func formatCapabilityValue(value any) string {
	switch typed := value.(type) {
	case []string:
		if len(typed) == 0 {
			return "not declared"
		}
		return strings.Join(typed, ", ")
	case *capability.IntRange:
		if typed == nil {
			return "not declared"
		}
		return fmt.Sprintf("[%d,%d]", typed.Min, typed.Max)
	case *capability.FloatRange:
		if typed == nil {
			return "not declared"
		}
		return fmt.Sprintf("[%g,%g]", typed.Min, typed.Max)
	case *capability.Limit:
		if typed == nil {
			return "not declared"
		}
		return fmt.Sprintf("max %d", typed.Max)
	default:
		return fmt.Sprint(value)
	}
}

// AllEvaluatedRejected 报告 auto 候选里所有已评估模型都被模型级能力淘汰。
func (g *CapabilityGate) AllEvaluatedRejected() bool {
	return g != nil && g.mode == CapabilityGateEnforce && g.evaluated > 0 && len(g.wouldReject) >= g.evaluated
}

// ExplainModelRejection 把 auto 模型级淘汰原因整理成面向调用方的错误。
func (g *CapabilityGate) ExplainModelRejection() string {
	undeclared := 0
	dimensions := make(map[string]string)
	for _, rejection := range g.modelRejections {
		if rejection.undeclared {
			undeclared++
			continue
		}
		for _, violation := range rejection.violations {
			if violation.Severity != capability.SeverityHard {
				continue
			}
			if _, exists := dimensions[violation.Dimension]; exists {
				continue
			}
			dimensions[violation.Dimension] = describeCapabilityViolation(violation)
		}
	}
	if len(dimensions) == 0 {
		if undeclared > 0 {
			return "no auto candidate model has a model-level capability declaration, so none can be validated"
		}
		return "no auto candidate model satisfies the request capabilities"
	}
	names := make([]string, 0, len(dimensions))
	for dimension := range dimensions {
		names = append(names, dimension)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = append(names[:8], fmt.Sprintf("and %d more", len(names)-8))
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, dimensions[name])
	}
	return fmt.Sprintf("no auto candidate model satisfies the request: %s", strings.Join(parts, "; "))
}

// Summary 返回可写进日志的摘要；没有结论时返回 nil。
func (g *CapabilityGate) Summary() *CapabilityGateSummary {
	if g == nil || (g.evaluated == 0 && g.channelsEvaluated == 0) {
		return nil
	}
	return &CapabilityGateSummary{
		Mode:              g.mode,
		Evaluated:         g.evaluated,
		WouldReject:       dedupeSorted(g.wouldReject),
		Rejected:          dedupeSorted(g.rejected),
		ChannelModel:      g.channelModel,
		ChannelsEvaluated: g.channelsEvaluated,
		ChannelsRejected:  append([]int{}, g.channelsRejected...),
	}
}

// LogSummary 把本次门控结论写进服务端日志，供 shadow 评估。
func (g *CapabilityGate) LogSummary(c *gin.Context) {
	summary := g.Summary()
	if summary == nil {
		return
	}
	if len(summary.WouldReject) == 0 && len(summary.ChannelsRejected) == 0 && len(summary.Rejected) == 0 {
		return
	}
	logger.LogWarn(c.Request.Context(), common.LogText(
		"capability gate: mode=%s evaluated=%d would_reject=%v rejected=%v channel_model=%s channels_evaluated=%d channels_rejected=%v",
		summary.Mode, summary.Evaluated, summary.WouldReject, summary.Rejected, summary.ChannelModel,
		summary.ChannelsEvaluated, summary.ChannelsRejected))
}

func capabilityGateFormat(path string) (capability.Format, bool) {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return capability.FormatAnthropic, true
	case strings.HasPrefix(path, "/v1/responses"):
		return capability.FormatOpenAIResponses, true
	case strings.HasPrefix(path, "/v1beta/models/"), strings.HasPrefix(path, "/v1/models/"):
		return capability.FormatGemini, true
	case strings.HasPrefix(path, "/v1/chat/completions"),
		strings.HasPrefix(path, "/pg/chat/completions"),
		strings.HasPrefix(path, "/v1/completions"):
		return capability.FormatOpenAIChat, true
	default:
		return "", false
	}
}

func capabilityGateBody(c *gin.Context, maxBytes int64) ([]byte, bool) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, false
	}
	body, err := storage.Bytes()
	if err != nil || len(body) == 0 {
		return nil, false
	}
	if maxBytes > 0 && int64(len(body)) > maxBytes {
		logger.LogWarn(c.Request.Context(), common.LogText(
			"capability gate skipped: body %d bytes exceeds capability_gate_setting.max_body_bytes %d",
			len(body), maxBytes))
		return nil, false
	}
	return body, true
}

func capabilityGateMaxBodyBytes() int64 {
	return operation_setting.GetCapabilityGateSetting().MaxBodyBytes
}

func dedupeSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
