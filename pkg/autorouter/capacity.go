package autorouter

import "math"

// CapacityPricing 是容量溢价的配置：把一个候选模型会落到的计数池的繁忙程度折算成
// 比较成本的倍数，让 auto 在容量紧张时主动把边际请求让给别的模型。
//
// 压力 q = max(各维度 当前用量 / 当前额度)，只统计有上限的维度。额度是容量池发布的
// 动态额度（配速已经把长周期预算折算成每分钟速率），没有动态额度时用渠道自己配的
// 静态额度；三项都不限的池子是弹性资源，不参与溢价。
//
// 溢价只影响 auto 的比较成本，不参与计费，也不写 relay_attempts.cost_actual。
type CapacityPricing struct {
	Enabled bool `json:"enabled"`
	// Knee 是膝点：压力不超过它时溢价为 1，容量约束不紧就不为它付钱。
	Knee float64 `json:"knee"`
	// Gamma 是膝点以上的曲线指数，越大越早变贵。
	Gamma float64 `json:"gamma"`
	// MaxMultiplier 是连续段的溢价上限。
	MaxMultiplier float64 `json:"max_multiplier"`
	// SaturatedMultiplier 是压力达到上限（或容量池已耗尽）时的溢价。它要明显大于
	// MaxMultiplier：饱和候选因此成为带内最高比价、吃满价格折扣，带内其它候选必然赢它，
	// 不需要额外的排除规则。带内只有它一个候选、或带内全部饱和时仍然会选中它。
	SaturatedMultiplier float64 `json:"saturated_multiplier"`
	// CacheSeconds 是节点本地池用量快照的有效期，避免每个 auto 请求都批量读 Redis。
	CacheSeconds int `json:"cache_seconds"`
	// SmoothingSeconds 是压力指数平滑的时间常数，0 表示不平滑。饱和不平滑，立即生效。
	SmoothingSeconds int `json:"smoothing_seconds"`
}

// DefaultCapacityPricing 是没配选项时的取值，默认直接生效（不是只观察）。
func DefaultCapacityPricing() CapacityPricing {
	return CapacityPricing{
		Enabled:             true,
		Knee:                0.5,
		Gamma:               1,
		MaxMultiplier:       20,
		SaturatedMultiplier: 100,
		CacheSeconds:        5,
		SmoothingSeconds:    30,
	}
}

// Sanitized 把选项里的非法值换成默认值：曲线参数错了会让比较全部失真，
// 这里宁可回退也不照着算。Enabled 原样保留。
func (c CapacityPricing) Sanitized() CapacityPricing {
	defaults := DefaultCapacityPricing()
	if c.Knee < 0 || c.Knee >= 1 || math.IsNaN(c.Knee) {
		c.Knee = defaults.Knee
	}
	if c.Gamma <= 0 || c.Gamma > 8 || math.IsNaN(c.Gamma) {
		c.Gamma = defaults.Gamma
	}
	if c.MaxMultiplier < 1 || math.IsNaN(c.MaxMultiplier) || math.IsInf(c.MaxMultiplier, 0) {
		c.MaxMultiplier = defaults.MaxMultiplier
	}
	if c.SaturatedMultiplier < c.MaxMultiplier || math.IsNaN(c.SaturatedMultiplier) || math.IsInf(c.SaturatedMultiplier, 0) {
		c.SaturatedMultiplier = max(defaults.SaturatedMultiplier, c.MaxMultiplier)
	}
	if c.CacheSeconds < 0 || c.CacheSeconds > 300 {
		c.CacheSeconds = defaults.CacheSeconds
	}
	if c.SmoothingSeconds < 0 || c.SmoothingSeconds > 3600 {
		c.SmoothingSeconds = defaults.SmoothingSeconds
	}
	return c
}

// Premium 按压力算溢价，始终不小于 1。pressure 为负表示这个池子没有额度，
// 是弹性资源，溢价为 1。
func (c CapacityPricing) Premium(pressure float64) float64 {
	c = c.Sanitized()
	if !c.Enabled || pressure < 0 || math.IsNaN(pressure) {
		return 1
	}
	if pressure >= 1 {
		return c.SaturatedMultiplier
	}
	if pressure <= c.Knee {
		return 1
	}
	premium := math.Pow((1-c.Knee)/(1-pressure), c.Gamma)
	return min(max(premium, 1), c.MaxMultiplier)
}
