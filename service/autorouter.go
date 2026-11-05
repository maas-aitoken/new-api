package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/observability"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/pkg/cachex"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

const (
	autoRouterCacheNamespace = "new-api:autorouter:v3"
	ginKeyAutoRouterResult   = "autorouter_result"
	// autoRouterFailureTTL 是 jev 调用失败后不再重试的时长。agent 循环里同一条指令会
	// 连发很多请求，jev 故障时不记一下，每个请求都要白等一次超时；
	// 时间取得短，是为了 jev 恢复后能很快重新分类，而不是把兜底结果钉住整个缓存周期。
	autoRouterFailureTTL = 60 * time.Second
)

// autoRouterEntry 按键的用途存不同的字段：
//   - d: 键只存 jev 的判断（Decision），或"刚失败过"的标记（Failed）。
//     同一条指令的后续请求不再调 jev，但路由每次按各自的可用集合重新算，
//     所以一次带图请求因为能力不满足换了模型，不会影响之后的纯文本请求。
//   - r: 和 s: 键存最近一次算出的路由（Route），供下一条指令"沿用上一轮模型"，
//     以及没有可读文字的子 agent 继承会话路由。s: 键还记着这条路由是否来自可信的
//     jev 判断（Trusted），子 agent 继承时据此决定能不能覆盖客户端的推理字段。
type autoRouterEntry struct {
	Route    autorouter.Route     `json:"route"`
	Decision *autorouter.Decision `json:"decision,omitempty"`
	Failed   bool                 `json:"failed,omitempty"`
	Trusted  bool                 `json:"trusted,omitempty"`
}

var (
	autoRouterCacheOnce sync.Once
	autoRouterCache     *cachex.HybridCache[autoRouterEntry]
	autoRouterClient    = &http.Client{}
)

func getAutoRouterCache() *cachex.HybridCache[autoRouterEntry] {
	autoRouterCacheOnce.Do(func() {
		autoRouterCache = cachex.NewHybridCache[autoRouterEntry](cachex.HybridCacheConfig[autoRouterEntry]{
			Namespace: cachex.Namespace(autoRouterCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[autoRouterEntry]{},
			Memory: func() *hot.HotCache[string, autoRouterEntry] {
				return hot.NewHotCache[string, autoRouterEntry](hot.LRU, 100_000).
					WithTTL(2 * time.Hour).
					WithJanitor().
					Build()
			},
		})
	})
	return autoRouterCache
}

// IsAutoRouterRequest reports whether the request asks the gateway to pick
// the model. Only conversational endpoints carry a human instruction to
// classify; elsewhere "auto" stays an ordinary, unknown model name.
func IsAutoRouterRequest(c *gin.Context, modelName string) bool {
	if modelName != autorouter.AutoModel || !autorouter_setting.GetAutoRouterSetting().Enabled {
		return false
	}
	_, supported := AutoRouterProtocol(c.Request.URL.Path)
	return supported
}

// AutoRouterProtocol 返回 auto 支持的入站协议，决定推理档位写进请求体的哪个字段；
// 其他路径 ok 为 false。
func AutoRouterProtocol(path string) (protocol autorouter.Protocol, ok bool) {
	switch mode := relayconstant.Path2RelayMode(path); {
	case strings.HasPrefix(path, "/v1/messages"):
		return autorouter.ProtocolAnthropic, true
	case mode == relayconstant.RelayModeResponses:
		return autorouter.ProtocolResponses, true
	case mode == relayconstant.RelayModeChatCompletions:
		return autorouter.ProtocolChat, true
	}
	return "", false
}

// AutoRouterListed reports whether "auto" belongs in a model list containing
// visible: only when it can resolve to one of them.
func AutoRouterListed(visible []string) bool {
	setting := autorouter_setting.GetAutoRouterSetting()
	if !setting.Enabled || slices.Contains(visible, autorouter.AutoModel) {
		return false
	}
	isVisible := func(model string) bool { return slices.Contains(visible, model) }
	ranking := setting.Ranking()
	if slices.ContainsFunc(ranking.DefaultModels, func(model string) bool { return autorouter.ModelAvailable(model, isVisible) }) {
		return true
	}
	for _, models := range ranking.TaskModels {
		if slices.ContainsFunc(models, func(model string) bool { return autorouter.ModelAvailable(model, isVisible) }) {
			return true
		}
	}
	return false
}

// autoRouterCacheKey scopes keys by token so short instructions such as "ok"
// from different users never share a decision, and instruction text never
// crosses tenants.
func autoRouterCacheKey(kind string, tokenID int, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{strconv.Itoa(tokenID)}, parts...), "\x00")))
	return kind + ":" + hex.EncodeToString(sum[:])
}

// AutoCandidates 由分发中间件提供，回答一个候选模型能不能服务这个请求。
type AutoCandidates struct {
	// Serves 判定一个候选：令牌白名单、分组启用、至少一个可用渠道的生效能力满足请求。
	// ignoreClientReasoning 为 true 时撇开客户端自己写的推理字段：可信的 auto 判断
	// 会自己定档位并改写它们；false 时按客户端原样的请求判定，兜底路径用它。
	Serves func(name string, ignoreClientReasoning bool) bool
	// Why 说明一个候选为什么不能服务这个请求（撇开客户端的推理字段，与 Serves 的第二个
	// 参数为 true 时一致），能服务时返回空串，只用于选路留痕。可以为 nil。
	Why func(name string) string
	// Channels 返回候选模型的可用渠道，用来把期望档位收敛到渠道声明过的档位。
	Channels func(name string) []int
	// Price 返回候选模型的混合用户价（美元/百万 token）；ok 为 false 表示没有价格
	// 或按次计价，没法按 token 比较。为 nil 时不比价，按榜单顺序取第一个可用的。
	Price func(name string) (price float64, ok bool)
	// Costs 给候选带里的模型估算比较成本（用户价 × 首次会落到的渠道的费用倍率），
	// 见 autorouter.Candidates.Costs。为 nil 时只比较用户价。
	Costs func(names []string, effort string, ignoreClientReasoning bool) map[string]autorouter.Cost
}

// ResolveAutoModel picks the model and reasoning effort for an "auto" request.
// A jev failure never fails the request: the route falls back to the previous
// instruction's route or the defaults.
//
// 模型声明了推理维度且裸模型名可用时，档位按声明收敛后写进请求体（result 里记着
// 写什么，由分发中间件落到请求体）；否则沿用旧办法，把档位拼在模型名后缀上。
func ResolveAutoModel(c *gin.Context, candidates AutoCandidates) (*autorouter.Result, error) {
	setting := autorouter_setting.GetAutoRouterSetting()
	policy := setting.Policy(common.GetContextKeyString(c, constant.ContextKeyTokenAutoPreference))
	result := &autorouter.Result{Source: autorouter.SourceDefault, Preference: policy.Preference}
	available := func(name string) bool { return candidates.Serves(name, false) }
	overridable := func(name string) bool { return candidates.Serves(name, true) }
	routing := autorouter.Candidates{Available: available, Overridable: overridable, Price: candidates.Price, Why: candidates.Why, Costs: candidates.Costs}
	trace := &autorouter.Trace{Judgment: autorouter.JudgmentNoInstruction}
	result.Trace = trace

	otelRuntime := observability.FromContext(c.Request.Context())
	routeCtx, routeSpan := otelRuntime.StartAutoRouter(c.Request.Context())
	routeInput := map[string]any{}
	route := observability.Observation{Input: routeInput}

	var instruction autorouter.Instruction
	if storage, err := common.GetBodyStorage(c); err == nil {
		if body, err := storage.Bytes(); err == nil {
			instruction = autorouter.ExtractInstruction(body)
		}
	}
	if instruction.Auxiliary {
		routeInput["auxiliary"] = true
	}
	protocol, _ := AutoRouterProtocol(c.Request.URL.Path)
	trace.Request = autorouter.RequestTrace{
		Protocol:      protocol,
		Auxiliary:     instruction.Auxiliary,
		Agent:         instruction.Agent,
		HumanTurns:    instruction.HumanTurns,
		Session:       instruction.Session != "",
		TextRunes:     utf8.RuneCountInString(instruction.Text),
		PreviousRunes: utf8.RuneCountInString(instruction.Previous),
	}

	ok := false
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	cache := getAutoRouterCache()
	ttl := time.Duration(setting.CacheTTLSeconds) * time.Second
	switch {
	case !policy.AnyModelAvailable(overridable):
		// 一个可用模型都没有时，分类得出什么都选不出路由，不必先花一次 jev 调用。
		// 用 overridable 判是因为它是 available 的超集，只有它也找不到才是真的没有。
		trace.Judgment = autorouter.JudgmentUnroutable
		trace.Unroutable = policy.ExplainUnroutable(routing.Why)
	case instruction.Auxiliary:
		trace.Judgment = autorouter.JudgmentAuxiliary
	case instruction.Text == "" && instruction.Session != "":
		trace.Judgment = autorouter.JudgmentSession
		if entry, found, _ := cache.Get(autoRouterCacheKey("s", tokenID, instruction.Session)); found {
			serves := available
			if entry.Trusted {
				serves = overridable
			}
			if autorouter.ModelAvailable(entry.Route.Model, serves) {
				result.Route, result.Source, result.EffortOverride = entry.Route, autorouter.SourceInherit, entry.Trusted
				trace.Ladder = append(trace.Ladder, autorouter.LadderStep{Profile: entry.Route.Profile, Outcome: autorouter.OutcomeInherited, Model: entry.Route.Model})
				ok = true
			}
		}
	case instruction.Text != "":
		state := autorouter.BuildState(instruction.Text, instruction.Previous, setting.MaxRunes, setting.PreviousMaxRunes)
		trace.Request.Truncated = state["current_task"] != instruction.Text || (instruction.Previous != "" && state["previous_task"] != instruction.Previous)
		if otelRuntime.RecordsContent(nil) {
			for key, value := range state {
				routeInput[key] = value
			}
		}
		decisionKey := autoRouterCacheKey("d", tokenID, autorouter.QuestionsVersion, state["current_task"], state["previous_task"])
		routeKey := autoRouterCacheKey("r", tokenID, instruction.Text)

		var previous *autorouter.Route
		if instruction.Previous != "" {
			if entry, found, _ := cache.Get(autoRouterCacheKey("r", tokenID, instruction.Previous)); found {
				previous = &entry.Route
			}
		}
		routeInput["previous_route"] = previous

		var decision *autorouter.Decision
		fromCache := false
		if entry, found, _ := cache.Get(decisionKey); found && (entry.Decision != nil || entry.Failed) {
			decision, fromCache = entry.Decision, entry.Decision != nil
			trace.Judgment = autorouter.JudgmentCache
			if entry.Failed {
				trace.Judgment = autorouter.JudgmentRecentFailure
			}
		} else {
			connections := setting.Connections()
			client := autorouter.JevClient{
				Endpoint:   connections.JevEndpoint.Value,
				Model:      connections.JevModel.Value,
				APIKey:     connections.JevAPIKey.Value,
				HTTPClient: autoRouterClient,
			}
			jevCtx, jevSpan := otelRuntime.StartJevClassify(routeCtx)
			ctx, cancel := context.WithTimeout(jevCtx, time.Duration(setting.JevTimeoutMs)*time.Millisecond)
			started := time.Now()
			response, err := client.Classify(ctx, state)
			cancel()
			result.Jev = &autorouter.JevCall{Endpoint: client.Endpoint, LatencyMs: time.Since(started).Milliseconds()}
			jev := observability.Observation{Err: err}
			jevInput := map[string]any{"questions_version": autorouter.QuestionsVersion}
			if otelRuntime.RecordsContent(err) {
				jevInput["state"] = state
			}
			jev.Input = jevInput

			if err != nil {
				trace.Judgment = autorouter.JudgmentLiveFailed
				result.Jev.Error = err.Error()
				_ = cache.SetWithTTL(decisionKey, autoRouterEntry{Failed: true}, autoRouterFailureTTL)
			} else {
				trace.Judgment = autorouter.JudgmentLive
				result.Jev.Response = response.ResponseBody
				decided := response.Decision(instruction.Previous != "")
				decision = &decided
				jev.Model = response.Model
				jevOutput := map[string]any{"answers": response.Answers}
				if response.LatencyMs != nil {
					jevOutput["server_latency_ms"] = *response.LatencyMs
				}
				jev.Output = jevOutput
				_ = cache.SetWithTTL(decisionKey, autoRouterEntry{Decision: decision}, ttl)
			}
			otelRuntime.FinishObservation(jevSpan, jev)
		}

		// 路由每次按本次请求自己的可用集合重新算，缓存里只有 jev 的判断。
		var inherited bool
		result.Decision = decision
		// 调度器的 workload 在选路前就定下来：比价时的调度器预览和选定后的正式调度用同一个值，
		// 两边按同一份评分排序。客户端自己带了 workload 时以客户端为准。
		if decision != nil && decision.TaskType != autorouter.TaskTypeUnknown &&
			common.GetContextKeyString(c, constant.ContextKeySchedulerWorkload) == "" {
			common.SetContextKey(c, constant.ContextKeySchedulerWorkload, decision.TaskType)
		}
		result.Route, inherited, ok, result.Pick = autorouter.SelectRoute(decision, previous, policy, routing, trace)
		result.EffortOverride = !inherited
		switch {
		case !inherited && fromCache:
			result.Source = autorouter.SourceCache
		case !inherited:
			result.Source = autorouter.SourceJev
		case previous != nil && result.Route == *previous:
			result.Source = autorouter.SourceInherit
		}
		// 兜底路由也记下来，下一条指令才能沿用同一个模型，agent 循环不会来回换模型。
		if ok {
			_ = cache.SetWithTTL(routeKey, autoRouterEntry{Route: result.Route}, ttl)
		}
		// The session remembers its latest human instruction's route for the
		// sub-agents whose task arrives without readable text.
		if ok && instruction.Session != "" && !instruction.Agent {
			_ = cache.SetWithTTL(autoRouterCacheKey("s", tokenID, instruction.Session), autoRouterEntry{Route: result.Route, Trusted: !inherited}, ttl)
		}
	}
	if !ok {
		// 前面已经选过路（Trust 有值），或根本没有可用模型时，兜底这一次不再写留痕，
		// 免得盖掉真正的失败过程。
		var fallbackTrace *autorouter.Trace
		if trace.Trust == nil && trace.Judgment != autorouter.JudgmentUnroutable {
			fallbackTrace = trace
		}
		result.Route, _, ok, result.Pick = autorouter.SelectRoute(nil, nil, policy, routing, fallbackTrace)
		result.EffortOverride = false
	}

	if ok {
		serves := available
		if result.EffortOverride {
			serves = overridable
		}
		result.ResolvedModel, result.EffortInBody, ok = AutoServingModel(result.Model, result.Effort, serves)
		if result.EffortInBody {
			// 只有可信的判断才改客户端的推理字段；兜底路径保持原样。
			result.EffortAction = autorouter.EffortKeep
			if result.EffortOverride {
				gate := CapabilityGateFromContext(c)
				support := gate.EffortSupport(result.ResolvedModel, candidates.Channels(result.ResolvedModel))
				result.AppliedEffort, result.EffortAction = support.Clamp(result.Effort)
				gate.OverrideReasoning(result.EffortAction, result.AppliedEffort)
			}
		}
	}
	if !ok {
		route.Err = errors.New("auto router found no available model for this token and group")
	}
	route.Output = result
	route.Metadata = map[string]string{
		"route_model":    result.Model,
		"route_effort":   result.Effort,
		"route_profile":  result.Profile,
		"route_fallback": result.Fallback(),
		"source":         result.Source,
		"resolved_model": result.ResolvedModel,
		"preference":     result.Preference,
	}
	route.TraceMetadata = map[string]string{
		"auto_router_model":      result.Model,
		"auto_router_effort":     result.Effort,
		"auto_router_profile":    result.Profile,
		"auto_router_fallback":   result.Fallback(),
		"auto_router_source":     result.Source,
		"auto_router_preference": result.Preference,
	}
	otelRuntime.FinishObservation(routeSpan, route)
	recordAutoRoute(c, result, route.Err)
	if !ok {
		return result, route.Err
	}
	c.Set(ginKeyAutoRouterResult, result)
	common.SetContextKey(c, constant.ContextKeyAutoRouted, true)
	return result, nil
}

// AutoServingModel 返回 auto 选中一个模型后真正用来服务请求的模型名：模型声明了推理维度
// 且裸名可用时用裸名，档位写进请求体（effortInBody 为 true）；否则取最接近期望档位的可用
// 变体，档位拼在名字后缀上。选路的最终解析和比价时估算候选落到哪个渠道共用这一条规则，
// 两边按同一个名字找渠道。serves 判定一个名字能不能服务这个请求。
func AutoServingModel(name, effort string, serves func(string) bool) (resolved string, effortInBody bool, ok bool) {
	if spec, declared := model.ResolveModelCapability(capabilityBaseModel(name)); declared && spec.Reasoning.Declared() && serves(name) {
		return name, true, true
	}
	resolved, ok = autorouter.ResolveVariant(name, effort, serves)
	return resolved, false, ok
}

// GetAutoRouterResult returns the routing outcome stored by ResolveAutoModel.
func GetAutoRouterResult(c *gin.Context) *autorouter.Result {
	value, ok := c.Get(ginKeyAutoRouterResult)
	if !ok {
		return nil
	}
	result, _ := value.(*autorouter.Result)
	return result
}
