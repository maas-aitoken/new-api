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
}

// Candidate 是质量/成本选择里的一个候选。
type Candidate struct {
	Model string  `json:"model"`
	Score float64 `json:"score"`
	Price float64 `json:"price"`
	// Discount 是价格折扣，Utility 是分数减去折扣，排名按 Utility。
	Discount float64 `json:"discount"`
	Utility  float64 `json:"utility"`
}

// Pick 是一次质量/成本选择的过程，写进决策留痕。
type Pick struct {
	Chosen string `json:"chosen"`
	// Weight 是这次比较用的成本权重（已限制到合法范围），既是候选带的宽度，也是价格折扣的上限。
	Weight float64 `json:"weight"`
	// Band 是进入比较的候选：分数不低于最高可用分减去 Weight 的可用模型。
	Band []Candidate `json:"band"`

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
//   - 价格折扣：带内按价格的对数位置打折，最便宜的折 0，最贵的折满 weight。
//     用对数是因为模型价格跨好几个数量级；只在带内归一化，所以折扣不会超过 weight。
//   - 折后分最高的中选，折后分相同取更便宜的，再相同取榜单里靠前的。
//
// weight 为 0 时候选带只剩与最高分并列的模型，等于按榜单分选。scores 里没有分数的模型不参与；
// 没有任何可用且有价格的候选时 ok 为 false。
//
// 带内被跳过的模型（不可用，或可用但按次计价没法比价）连同原因记在返回的 Pick 里，
// 即使 ok 为 false 也带着，选路留痕据此解释"为什么没选更靠前的模型"。why 可以为 nil。
func pickByQualityCost(models []string, scores map[string]float64, weight float64, overridable func(string) bool, price func(string) (float64, bool), why func(string) string) (Pick, bool) {
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

	floor, ceiling := priceRange(band)
	chosen := 0
	for i := range band {
		band[i].Discount = weight * logPosition(band[i].Price, floor, ceiling)
		band[i].Utility = band[i].Score - band[i].Discount
		if band[i].Utility > band[chosen].Utility ||
			band[i].Utility == band[chosen].Utility && band[i].Price < band[chosen].Price {
			chosen = i
		}
	}
	pick.Chosen, pick.Weight, pick.Band = band[chosen].Model, weight, band
	return pick, true
}

// priceRange 返回带内的价格下限和上限。免费（价格为 0）的模型不拉低下限，
// 否则一个免费模型会把其余模型的价格差都压扁；全部免费时返回 1，折扣恒为 0。
func priceRange(band []Candidate) (floor, ceiling float64) {
	floor, ceiling = math.Inf(1), 0
	for _, candidate := range band {
		if candidate.Price > 0 {
			floor = min(floor, candidate.Price)
		}
		ceiling = max(ceiling, candidate.Price)
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
