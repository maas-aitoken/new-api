package autorouter

import (
	"math"
	"slices"
)

// 偏好决定一次选模型愿意为价格让出多少榜单分，各偏好的权重由平台设置。
const (
	PreferenceCapability = "capability"
	PreferenceBalanced   = "balanced"
	PreferenceCost       = "cost"
	// PreferenceAdaptive 没有固定权重：每次请求按 jev 的 mode 判断，再用三档权重折算出本次的权重。
	PreferenceAdaptive = "adaptive"
)

// Preferences 按从重能力到重成本的顺序列出有固定权重的偏好，cost_weights 只接受它们。
var Preferences = []string{PreferenceCapability, PreferenceBalanced, PreferenceCost}

// SelectablePreferences 是令牌和平台默认偏好可以选的全部取值：固定权重的三档加自适应。
var SelectablePreferences = append(slices.Clone(Preferences), PreferenceAdaptive)

// TierWeights 是平台给三个固定偏好设的成本权重，榜单分尺度，0 到 1。
type TierWeights struct {
	Capability float64 `json:"capability"`
	Balanced   float64 `json:"balanced"`
	Cost       float64 `json:"cost"`
}

// Of 返回一个偏好的权重。自适应偏好或不认识的偏好没有固定权重，按均衡档处理，
// 自适应在没有可用的 mode 判断时正是这样兜底。
func (t TierWeights) Of(preference string) float64 {
	switch preference {
	case PreferenceCapability:
		return t.Capability
	case PreferenceCost:
		return t.Cost
	}
	return t.Balanced
}

// Blend 把 mode 问题给三个偏好的概率折算成本次请求的成本权重。
//
// 三个偏好按从重能力到重成本排成位置 0、1、2，先对位置求期望，再沿三档权重连成的折线取值。
// 不直接对三档权重求期望，是因为默认权重（0、0.07、0.3）的间距不均，直接平均会让拿不准的判断偏向成本。
// 这样取值有三个性质：判断完全确定时就是该档的权重；对称的不确定（各占三分之一，或一半能力一半成本）
// 落在均衡档；概率从能力移向成本时权重不会变小。
//
// 概率先按三个偏好的总和归一，缺的算 0。有负数、非有限数，或总和不为正时没有可用的答案，
// 取均衡档的权重，Reason 写明。
func (t TierWeights) Blend(probabilities map[string]float64) ModeTrace {
	unjudged := ModeTrace{Reason: ModeNoAnswer, Position: 1, Weight: t.Balanced, Tiers: t}
	total := 0.0
	for _, preference := range Preferences {
		probability := probabilities[preference]
		if probability < 0 || math.IsNaN(probability) {
			return unjudged
		}
		total += probability
	}
	// 总和溢出成无穷时份额都会变成 0，不能当成“确定的能力优先”。
	if total <= 0 || math.IsInf(total, 0) {
		return unjudged
	}

	shares := make(map[string]float64, len(Preferences))
	position := 0.0
	for rank, preference := range Preferences {
		shares[preference] = probabilities[preference] / total
		position += float64(rank) * shares[preference]
	}
	weight := t.Capability + position*(t.Balanced-t.Capability)
	if position > 1 {
		weight = t.Balanced + (position-1)*(t.Cost-t.Balanced)
	}
	return ModeTrace{Shares: shares, Position: position, Weight: weight, Tiers: t}
}

// Candidates 回答一个候选模型在当前请求下的可用性和价格，由分发中间件提供。
type Candidates struct {
	// Available 按客户端原样的请求判定；兜底路径不碰客户端的推理字段，用它。
	Available func(model string) bool
	// Overridable 撇开客户端的推理字段判定；可信的判断自己定档位，用它。
	Overridable func(model string) bool
	// Price 返回模型的混合用户价（美元/百万 token）。ok 为 false 表示没有价格或按次计价，
	// 没法按 token 比较。Price 为 nil 时不比价，按榜单顺序取第一个可用的。
	Price func(model string) (price float64, ok bool)
	// Why 说明一个模型为什么不能服务这个请求（撇开客户端的推理字段），能服务时返回空串，
	// 留给选路留痕解释"为什么没选更靠前的模型"。可以为 nil。
	Why func(model string) string
	// Costs 在候选带定下来之后对带内模型一次性给出比较成本：首次请求命中的分组下的用户价，
	// 乘上会落到的渠道的费用倍率（几个渠道时按概率取期望）。effort 是这一步的期望档位，
	// ignoreClientReasoning 与判定可用时的口径一致，主机据此找出真正服务请求的模型名和渠道。
	// 结果里没有的模型按 Price 比较；为 nil 时只比较 Price。费用倍率不参与计费，只影响这里的选择。
	// preview 是这次问调度器预览的结果（问没问、为什么没拿到回答），记进 Pick；没配调度器时为 nil。
	Costs func(models []string, effort string, ignoreClientReasoning bool) (costs map[string]Cost, preview *PreviewOutcome)
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

// Candidate 是质量/成本选择里的一个候选。
type Candidate struct {
	Model string  `json:"model"`
	Score float64 `json:"score"`
	// Price 是用户侧混合价（美元/百万 token）。
	Price float64 `json:"price"`
	// CostMultiplier 不为 0 时，比较用的是 Cost = Price × CostMultiplier（平台成本，与
	// relay_attempts.cost_actual 同一口径）；为 0 表示没有估算，按 Price 比较。
	CostMultiplier float64 `json:"cost_multiplier,omitempty"`
	Cost           float64 `json:"cost,omitempty"`
	ChannelID      int     `json:"channel_id,omitempty"`
	Channels       int     `json:"channels,omitempty"`
	CostSource     string  `json:"cost_source,omitempty"`
	// Preview 是调度器回答了预览、却没用上它给这个模型的答案时的原因，见 Cost.Preview。
	Preview string `json:"preview,omitempty"`
	// Discount 是价格折扣，Utility 是分数减去折扣，排名按 Utility。
	Discount float64 `json:"discount"`
	Utility  float64 `json:"utility"`
}

// compared 是这个候选参与比较的价格：估算过倍率时是平台成本，否则是用户价。
func (c Candidate) compared() float64 {
	if c.CostMultiplier > 0 {
		return c.Cost
	}
	return c.Price
}

// Pick 是一次质量/成本选择的过程，写进决策留痕。
type Pick struct {
	Chosen string `json:"chosen"`
	// Weight 是这次比较用的成本权重（已限制到合法范围），既是候选带的宽度，也是价格折扣的上限。
	Weight float64 `json:"weight"`
	// Band 是进入比较的候选：分数不低于最高可用分减去 Weight 的可用模型。
	Band []Candidate `json:"band"`
	// Preview 是这次比较问调度器预览的结果；没配调度器、或没有估算比较成本时为 nil。
	Preview *PreviewOutcome `json:"preview,omitempty"`

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

// pickByQualityCost 在同一个榜单的候选里一次比较质量和价格，按折后分排名取第一。
//
//   - 候选带：分数不低于"最高可用分 - weight"。再便宜也不能比最高分低出这个范围。
//   - 比较的价格：带定下来之后用 costs 一次取齐带内模型的比较成本（用户价 × 渠道费用倍率），
//     没有的按用户价；带的成员只看分数、可用和有没有价，与价格高低无关，所以一次就够。
//   - 价格折扣：带内按比较价格的对数位置打折，最便宜的折 0，最贵的折满 weight。
//     用对数是因为模型价格跨好几个数量级；只在带内归一化，所以折扣不会超过 weight。
//   - 折后分最高的中选，折后分相同取更便宜的，再相同取榜单里靠前的。
//
// weight 为 0 时候选带只剩与最高分并列的模型，等于按榜单分选。scores 里没有分数的模型不参与；
// 没有任何可用且有价格的候选时 ok 为 false。costs 标为 Excluded 的模型从带里去掉（带不重组，
// 仍以原来的最高分为准），全部去掉时 ok 为 false。
//
// 带内被跳过的模型（不可用，或可用但按次计价没法比价）连同原因记在返回的 Pick 里，
// 即使 ok 为 false 也带着，选路留痕据此解释"为什么没选更靠前的模型"。why、costs 可以为 nil。
func pickByQualityCost(models []string, scores map[string]float64, weight float64, overridable func(string) bool, price func(string) (float64, bool), costs func([]string) (map[string]Cost, *PreviewOutcome), why func(string) string) (Pick, bool) {
	scored := make([]string, 0, len(models))
	for _, model := range models {
		if _, found := scores[model]; found {
			scored = append(scored, model)
		}
	}
	slices.SortStableFunc(scored, func(a, b string) int {
		switch {
		case scores[a] > scores[b]:
			return -1
		case scores[a] < scores[b]:
			return 1
		}
		return 0
	})

	// 榜单按分数降序，第一个可用且有价格的就是最高分；之后只检查还在候选带里的模型，
	// 掉出带的模型不必再判定可用性（没有内存缓存时每次判定都是一次查询）。
	var band []Candidate
	var top float64
	var pick Pick
	for _, model := range scored {
		if len(band) > 0 && scores[model] < top-weight {
			break
		}
		if !overridable(model) {
			pick.skip(model, scores[model], skipReason(why, model))
			continue
		}
		modelPrice, priced := price(model)
		if !priced {
			// 没有价格的模型连可用都过不了（计费会失败），能走到这里的只剩按次计价。
			pick.skip(model, scores[model], ReasonPerCall)
			continue
		}
		if len(band) == 0 {
			top = scores[model]
		}
		band = append(band, Candidate{Model: model, Score: scores[model], Price: modelPrice})
	}
	if len(band) == 0 {
		return pick, false
	}
	if costs != nil {
		names := make([]string, len(band))
		for i := range band {
			names[i] = band[i].Model
		}
		estimated, preview := costs(names)
		pick.Preview = preview
		kept := band[:0]
		for _, candidate := range band {
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
		band = kept
		if len(band) == 0 {
			return pick, false
		}
	}

	floor, ceiling := priceRange(band)
	chosen := 0
	for i := range band {
		band[i].Discount = weight * logPosition(band[i].compared(), floor, ceiling)
		band[i].Utility = band[i].Score - band[i].Discount
		if band[i].Utility > band[chosen].Utility ||
			band[i].Utility == band[chosen].Utility && band[i].compared() < band[chosen].compared() {
			chosen = i
		}
	}
	pick.Chosen, pick.Weight, pick.Band = band[chosen].Model, weight, band
	return pick, true
}

// priceRange 返回带内比较价格的下限和上限。免费（价格为 0）的模型不拉低下限，
// 否则一个免费模型会把其余模型的价格差都压扁；全部免费时返回 1，折扣恒为 0。
func priceRange(band []Candidate) (floor, ceiling float64) {
	floor, ceiling = math.Inf(1), 0
	for _, candidate := range band {
		if value := candidate.compared(); value > 0 {
			floor = min(floor, value)
		}
		ceiling = max(ceiling, candidate.compared())
	}
	if math.IsInf(floor, 1) {
		return 1, 1
	}
	return floor, ceiling
}

// logPosition 返回价格在 [floor, ceiling] 上的对数位置，范围 0 到 1。
func logPosition(price, floor, ceiling float64) float64 {
	if ceiling <= floor {
		return 0
	}
	return math.Log(max(price, floor)/floor) / math.Log(ceiling/floor)
}
