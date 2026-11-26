package autorouter

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

// 偏好决定一次选模型愿意为价格让出多少榜单分，各偏好的权重由平台设置。
const (
	PreferenceCapability = "capability"
	PreferenceBalanced   = "balanced"
	PreferenceCost       = "cost"
)

// Preferences 按从重能力到重成本的顺序列出偏好，令牌、平台默认偏好和 preference_weights 只接受它们。
var Preferences = []string{PreferenceCapability, PreferenceBalanced, PreferenceCost}

// PreferenceWeights holds each fixed preference's configured scoring strengths.
type PreferenceWeights map[string]DimensionWeights

// DimensionWeights stores the preference strengths used to derive the
// deterministic utility formula. Temperature is retained for compatibility.
type DimensionWeights struct {
	Quality  float64 `json:"quality"`
	Cost     float64 `json:"cost"`
	Capacity float64 `json:"capacity"`
	Success  float64 `json:"success"`
	// Temperature is retained for settings and old route logs. Model selection
	// is deterministic and does not use it.
	Temperature float64 `json:"temperature"`
}

// Normalized scales the four configured strengths to sum to one. Temperature is
// independent. An all-zero policy is left for the settings' built-in fallback.
func (w DimensionWeights) Normalized() DimensionWeights {
	total := w.Quality + w.Cost + w.Capacity + w.Success
	if total > 0 && !math.IsInf(total, 0) && !math.IsNaN(total) {
		w.Quality /= total
		w.Cost /= total
		w.Capacity /= total
		w.Success /= total
	}
	return w
}

// 均衡偏好的质量与成本权重由任务难度决定：need 为 0 时与成本优先相同，0.5 时与原均衡相同，
// 1 时与能力优先相同，三者的质量加成本合计都是 adaptiveQualityCostShare。
const (
	adaptiveQualityCostShare = 0.6
	adaptiveMaxCost          = 0.4
	// NeutralNeed 是没有可用难度时的 need，对应原来的均衡权重。
	NeutralNeed = 0.5
)

// DifficultyNeed 把 jev 的难度换算成 0 到 1 的能力需求：优先用概率加权的连续分数，
// 避免最可能档位在两档之间跳变；没有分数时用最可能档位。ok 为 false 表示判断里没有难度。
func DifficultyNeed(decision *Decision) (need float64, ok bool) {
	if decision == nil {
		return 0, false
	}
	top := float64(difficultyRungs - 1)
	switch {
	case decision.DifficultyScore != nil && !math.IsNaN(*decision.DifficultyScore):
		need = *decision.DifficultyScore / top
	case decision.Difficulty > 0:
		need = float64(decision.Difficulty-1) / top
	default:
		return 0, false
	}
	return min(max(need, 0), 1), true
}

// AdaptiveWeights 是均衡偏好在能力需求 need 下的权重：成本权重随 need 从 0.4 线性降到 0，
// 质量权重补足其余份额；容量和成功率沿用平台配置。
func AdaptiveWeights(need float64, base DimensionWeights) DimensionWeights {
	need = min(max(need, 0), 1)
	cost := adaptiveMaxCost * (1 - need)
	return DimensionWeights{
		Quality:     adaptiveQualityCostShare - cost,
		Cost:        cost,
		Capacity:    base.Capacity,
		Success:     base.Success,
		Temperature: base.Temperature,
	}
}

// Candidates 回答一个候选模型在当前请求下的可用性和价格，由分发中间件提供。
type Candidates struct {
	// Available 按客户端原样的请求判定；兜底路径不碰客户端的推理字段，用它。
	Available func(model string) bool
	// Overridable 撇开客户端的推理字段判定；档位由判断定下时 auto 会改写这些字段，用它。
	Overridable func(model string) bool
	// Price 返回模型的混合用户价（美元/百万 token）。ok 为 false 表示没有价格或按次计价，
	// 没法按 token 比较。Price 为 nil 时不比价，按榜单顺序取第一个可用的。
	Price func(model string) (price float64, ok bool)
	// Why 说明一个模型为什么不能服务这个请求（撇开客户端的推理字段），能服务时返回空串，
	// 留给选路留痕解释"为什么没选更靠前的模型"。可以为 nil。
	Why func(model string) string
	// Costs 对可服务且有榜单分的模型一次性给出比较成本：首次请求命中的分组下的用户价，
	// 乘上会落到的渠道的费用倍率（几个渠道时按概率取期望）。effort 是这一步的期望档位，
	// ignoreClientReasoning 与判定可用时的口径一致，主机据此找出真正服务请求的模型名和渠道。
	// 结果里没有的模型按 Price 比较；为 nil 时只比较 Price。这里算的成本只用于选择，不改变计费。
	// preview 是这次问调度器预览的结果（问没问、为什么没拿到回答），记进 Pick；没配调度器时为 nil。
	Costs func(models []string, effort string, ignoreClientReasoning bool) (costs map[string]Cost, preview *PreviewOutcome)
	// Metrics returns shared sliding-window model traffic. Nil means no live signal.
	Metrics func(models []string) map[string]MetricSample
}

// Cost 是一个候选模型的比较成本。只要有这一项，Price 就以它为准。
type Cost struct {
	// Price 是首次请求命中的分组下的用户混合价（美元/百万 token）。
	Price float64
	// Multiplier 是会落到的渠道的费用倍率，几个渠道时是按命中概率的期望，没配按 1。
	Multiplier float64
	// ChannelID 是确定会落到的那个渠道（调度器预览、会话亲和，或只有一个可能的渠道），否则为 0。
	ChannelID int
	// Channels 是参与期望的渠道个数。
	Channels int
	// Source 是倍率从哪来，见 CostSource*。
	Source string
	// Excluded 表示这个模型这次一定服务不了（调度器预览没有可用渠道），比较时跳过，Reason 是原因。
	Excluded bool
	Reason   string
	// Preview 是调度器回答了这次预览、却没用上它给这个模型的答案时的原因（还没加载、出错、
	// 给的渠道没法报价、超过一次能问的个数），这个模型按原生规则估算。整次没拿到回答时为空，
	// 原因在 PreviewOutcome 里。
	Preview string
	// Capacity 是首次请求实际落点的剩余容量评分。
	Capacity float64
	// CapacityKnown 表示 Capacity 已按渠道能力配置计算。
	CapacityKnown bool
}

// PreviewOutcome 是一次比价问调度器预览的结果，写进选路留痕：没用上预览时据此知道为什么。
type PreviewOutcome struct {
	// Status 是问的结果：answered 表示调度器回答了（各模型的情况看 Candidate.Preview），
	// 其余是没拿到回答的原因（这次不由调度器定渠道、没配置、熔断、不支持预览、超时、失败、响应不对）。
	Status string `json:"status"`
	// HTTPStatus 是失败时调度器回的状态码，连不上时为 0。
	HTTPStatus int `json:"http_status,omitempty"`
}

// 比较成本里的倍率从哪来。
const (
	CostSourceScheduler = "scheduler" // 调度器预览给出了确定的渠道
	CostSourceAffinity  = "affinity"  // 会话亲和绑定的渠道
	CostSourceExpected  = "expected"  // 按原生选渠道规则的命中概率取期望
)

// Candidate is a servable model and its four normalized routing dimensions.
type Candidate struct {
	Model string  `json:"model"`
	Score float64 `json:"score"`
	Price float64 `json:"price"`
	// CostMultiplier preserves the channel's price multiplier in the compared cost.
	CostMultiplier  float64 `json:"cost_multiplier,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	ChannelID       int     `json:"channel_id,omitempty"`
	Channels        int     `json:"channels,omitempty"`
	CostSource      string  `json:"cost_source,omitempty"`
	Preview         string  `json:"preview,omitempty"`
	CostPenalty     float64 `json:"cost_penalty"`
	CapacityPenalty float64 `json:"capacity_penalty,omitempty"`
	SuccessPenalty  float64 `json:"success_penalty,omitempty"`
	Capacity        float64 `json:"capacity"`
	Success         float64 `json:"success"`
	Samples         int64   `json:"samples"`
	Utility         float64 `json:"utility"`
	Probability     float64 `json:"probability,omitempty"`
}

// compared 是这个候选参与比较的价格：估算过倍率时是平台成本，否则是用户价。
func (c Candidate) compared() float64 {
	if c.CostMultiplier > 0 {
		return c.Cost
	}
	return c.Price
}

// Pick is the scored comparison and deterministic choice among all servable models.
type Pick struct {
	Chosen          string           `json:"chosen"`
	Band            []Candidate      `json:"band"`
	Dimensions      DimensionWeights `json:"dimensions"`
	QualityScale    float64          `json:"quality_scale"`
	CostScale       float64          `json:"cost_scale"`
	CapacityPenalty float64          `json:"capacity_penalty"`
	SuccessPenalty  float64          `json:"success_penalty"`
	// Preview 是这次比较问调度器预览的结果；没配调度器、或没有估算比较成本时为 nil。
	Preview *PreviewOutcome `json:"preview,omitempty"`
	// Need 是均衡偏好按任务难度换算的能力需求，质量和成本权重由它决定；其余偏好为 nil。
	Need *float64 `json:"need,omitempty"`
	// NeedFromDifficulty 为 false 表示没有可用难度，Need 取了 NeutralNeed。
	NeedFromDifficulty bool `json:"need_from_difficulty,omitempty"`

	// skipped 是比较时跳过的模型，只进选路留痕；消费日志里的摘要不带它，免得每条日志都变大。
	skipped     []Skipped
	skippedMore int
}

// skip 记下一个被跳过的模型，最多记 maxSkipped 个，其余只计数。
func (p *Pick) skip(model string, score float64, reason string) {
	if len(p.skipped) >= maxSkipped {
		p.skippedMore++
		return
	}
	p.skipped = append(p.skipped, Skipped{Model: model, Score: score, Reason: reason})
}

const (
	defaultCostScale       = 8.5
	capabilityCostScale    = 40
	costFirstCostScale     = 0.5
	costLogPenaltyWeight   = 0.02
	defaultCapacityPenalty = 0.04 * math.Ln2 / 0.25
	defaultSuccessPenalty  = 0.04 * math.Ln2 / 0.10
	defaultRuntimeWeight   = 0.2
)

// costScale maps the configured cost strength to a fixed price tolerance. The
// logarithmic interpolation keeps the tolerance between the built-in anchors
// of 40, 8.5, and 0.5 dollars per million tokens.
func costScale(costWeight float64) float64 {
	costWeight = min(max(costWeight, 0), 0.4)
	if costWeight <= 0.2 {
		fraction := costWeight / 0.2
		return math.Exp(math.Log(capabilityCostScale) + fraction*(math.Log(defaultCostScale)-math.Log(capabilityCostScale)))
	}
	fraction := (costWeight - 0.2) / 0.2
	return math.Exp(math.Log(defaultCostScale) + fraction*(math.Log(costFirstCostScale)-math.Log(defaultCostScale)))
}

// modelCostPenalty uses an absolute cost tolerance. The candidate set does
// not affect this penalty, so adding a cheaper model cannot rewrite existing
// models' scores.
func modelCostPenalty(cost, scale float64) float64 {
	ratio := cost / scale
	return ratio*ratio + costLogPenaltyWeight*math.Log2(1+cost)
}

// pickModels scores every servable, scored model and deterministically chooses
// the highest utility. Live traffic never excludes a model: complete
// unavailability remains the hard gate.
func pickModels(models []string, scores map[string]float64, weights DimensionWeights, overridable func(string) bool, price func(string) (float64, bool), costs func([]string) (map[string]Cost, *PreviewOutcome), metrics func([]string) map[string]MetricSample, why func(string) string) (Pick, bool) {
	var pick Pick
	weights = weights.Normalized()
	weights.Temperature = 0
	pick.Dimensions = weights
	// The built-in policies reserve 0.4 for runtime signals. Calibrate custom
	// quality strengths against the built-in quality share at this cost strength.
	pick.QualityScale = weights.Quality / (0.6 - min(weights.Cost, 0.4))
	pick.CostScale = costScale(weights.Cost)
	pick.CapacityPenalty = defaultCapacityPenalty * weights.Capacity / defaultRuntimeWeight
	pick.SuccessPenalty = defaultSuccessPenalty * weights.Success / defaultRuntimeWeight
	names := make([]string, 0, len(models))
	for _, model := range models {
		score, found := scores[model]
		if !found {
			continue
		}
		if !overridable(model) {
			pick.skip(model, score, skipReason(why, model))
			continue
		}
		modelPrice, priced := price(model)
		if !priced {
			pick.skip(model, score, ReasonPerCall)
			continue
		}
		names = append(names, model)
		pick.Band = append(pick.Band, Candidate{Model: model, Score: score, Price: modelPrice})
	}
	if len(pick.Band) == 0 {
		return pick, false
	}
	estimated := map[string]Cost{}
	if costs != nil {
		var preview *PreviewOutcome
		estimated, preview = costs(names)
		pick.Preview = preview
		kept := pick.Band[:0]
		for _, candidate := range pick.Band {
			cost, found := estimated[candidate.Model]
			if !found {
				kept = append(kept, candidate)
				continue
			}
			if cost.Excluded {
				pick.skip(candidate.Model, candidate.Score, cost.Reason)
				continue
			}
			multiplier := cost.Multiplier
			if multiplier <= 0 {
				multiplier = 1
			}
			candidate.Price = cost.Price
			candidate.CostMultiplier, candidate.Cost = multiplier, cost.Price*multiplier
			candidate.ChannelID, candidate.Channels, candidate.CostSource = cost.ChannelID, cost.Channels, cost.Source
			candidate.Preview = cost.Preview
			kept = append(kept, candidate)
		}
		pick.Band = kept
		if len(pick.Band) == 0 {
			return pick, false
		}
	}

	samples := map[string]MetricSample{}
	if metrics != nil {
		samples = metrics(names)
	}
	scored := pick.Band[:0]
	for _, candidate := range pick.Band {
		cost := candidate.compared()
		candidate.CostPenalty = modelCostPenalty(cost, pick.CostScale)
		if cost < 0 || math.IsNaN(candidate.CostPenalty) || math.IsInf(candidate.CostPenalty, 0) {
			pick.skip(candidate.Model, candidate.Score, ReasonInvalidCost)
			continue
		}
		candidate.Capacity = 1
		sample := samples[candidate.Model]
		candidate.Samples = sample.Successes + sample.Failures
		if cost, found := estimated[candidate.Model]; found && cost.CapacityKnown {
			candidate.Capacity = clampCapacity(cost.Capacity)
		}
		// Four prior successes keep a new model competitive without overwhelming
		// measured failure once enough outcomes have accumulated.
		candidate.Success = (float64(sample.Successes) + 4) / (float64(candidate.Samples) + 4)
		candidate.CapacityPenalty = pick.CapacityPenalty * (1 - candidate.Capacity)
		candidate.SuccessPenalty = pick.SuccessPenalty * (1 - candidate.Success)
		candidate.Utility = pick.QualityScale*candidate.Score - candidate.CostPenalty - candidate.CapacityPenalty - candidate.SuccessPenalty
		scored = append(scored, candidate)
	}
	pick.Band = scored
	if len(pick.Band) == 0 {
		return pick, false
	}
	slices.SortStableFunc(pick.Band, func(a, b Candidate) int {
		if order := cmp.Compare(b.Utility, a.Utility); order != 0 {
			return order
		}
		if order := cmp.Compare(a.compared(), b.compared()); order != 0 {
			return order
		}
		return strings.Compare(a.Model, b.Model)
	})
	pick.Chosen = pick.Band[0].Model
	return pick, true
}
