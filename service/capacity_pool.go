package service

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	capacityPoolPollSecondsEnv     = "CAPACITY_POOL_POLL_SECONDS"
	defaultCapacityPoolPollSeconds = 60
	minCapacityPoolPollSeconds     = 15
	// capacityPoolUpstreamMinPercent：7 天用量低于这个百分比时，用上游累计数换算误差太大，
	// 改用学到的比例。
	capacityPoolUpstreamMinPercent = 5.0
	// capacityPoolLearnWindow 是学换算比例回看的时长。
	capacityPoolLearnWindow = 6 * time.Hour
	// capacityPoolLearnMinPercent：回看窗口里额度用量至少涨了这么多（按单账号额度的百分点折算）
	// 学到的比例才生效，太少时整数百分比的量化误差会盖过信号。
	capacityPoolLearnMinPercent = 2.0
	// capacityPoolLearnMaxSkipped：一轮里对不上账（刚重置、数据过期、新出现）的账号超过
	// 这个比例时丢弃这一轮，它们收到的请求算不进额度变化，会让比例偏松。
	capacityPoolLearnMaxSkipped = 0.25
	// capacityPoolRiseLimit 是动态额度每轮最多上调的比例，下调不受限。
	capacityPoolRiseLimit = 0.25
)

// capacityAccount 是算额度用到的一个账号的状态，由 model.ResourcePoolAccount 归一而来。
type capacityAccount struct {
	key     string
	plan    string
	enabled bool
	status  string
	// used / limit 是长窗口的用量与额度，单位相同：Kiro 是本月 credits（creditUnit），
	// 其它是 7 天用量百分比（limit 恒为 100）。
	used, limit float64
	creditUnit  bool
	// resetAt 是长窗口的重置时间，零值表示未知。
	resetAt   time.Time
	percent5h *float64
	reset5hAt time.Time
	// rpmLimit 是 Kiro 账号自己报的 rpm_limit。
	rpmLimit int64
	// requests7d / tokens7d 是 codex2api 报的 7 天累计数，配速优先用它换算。
	requests7d, tokens7d int64
}

// capacityAccountOf 把资源池账号归一成计算用的状态。windowed 表示 codex2api（GPT/Gemini/Grok），
// 否则是 Kiro（tokenmanagement）。用量百分比的取法与资源池页一致。
func capacityAccountOf(account *model.ResourcePoolAccount, windowed bool) capacityAccount {
	view := capacityAccount{
		key:        fmt.Sprintf("%d/%s", account.SourceId, cmp.Or(account.Id, account.Email)),
		plan:       capacityAccountPlan(account),
		enabled:    account.Enabled,
		status:     strings.ToLower(strings.TrimSpace(account.Status)),
		percent5h:  account.UsagePercent5h,
		rpmLimit:   account.RpMLimit,
		requests7d: account.Requests7d,
		tokens7d:   account.Tokens7d,
	}
	if windowed {
		percent := account.UsagePercentage
		if account.WeeklyPercent != nil {
			percent = *account.WeeklyPercent
		} else if account.UsagePercent7d != nil {
			percent = *account.UsagePercent7d
		}
		view.used, view.limit = min(max(percent, 0), 100), 100
		view.resetAt = parseCapacityResetTime(account.Reset7dAt)
		view.reset5hAt = parseCapacityResetTime(account.Reset5hAt)
		return view
	}
	if account.UsageLimit > 0 {
		view.used, view.limit, view.creditUnit = max(account.UsageCurrent, 0), account.UsageLimit, true
	} else {
		view.used, view.limit = min(max(account.UsagePercentage, 0), 100), 100
	}
	view.resetAt = parseCapacityResetTime(account.NextResetDate)
	return view
}

// capacityAccountPlan 是账号的套餐键：codex2api 的 plan_type，没有时用 Kiro 的 subscription_type。
// 前端列套餐时用同样的取法。
func capacityAccountPlan(account *model.ResourcePoolAccount) string {
	return cmp.Or(strings.TrimSpace(account.PlanType), strings.TrimSpace(account.SubscriptionType))
}

// parseCapacityResetTime 解析上游给的重置时间：RFC3339，或只有日期（Kiro 的 next_reset_date）。
// 只有日期时按当天结束（UTC）算：剩余时间算长、配速偏紧，时区猜错也不会提前用光。
func parseCapacityResetTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at
	}
	if day, err := time.Parse(time.DateOnly, value); err == nil {
		return day.Add(24 * time.Hour)
	}
	return time.Time{}
}

// windowExpired 表示长窗口已经过了重置时间但上游还没刷新数据，按新窗口处理。
func (a capacityAccount) windowExpired(now time.Time) bool {
	return !a.resetAt.IsZero() && !a.resetAt.After(now)
}

// unusableReason 返回账号不计入可用的原因，空表示可用：停用、报错/封禁/未授权、
// 限流冷却中、长窗口或 5h 窗口用量到了阈值。窗口已过重置时间的用量不算数。
func (a capacityAccount) unusableReason(thresholdPercent float64, now time.Time) string {
	if !a.enabled {
		return "disabled"
	}
	switch a.status {
	case "error", "banned", "unauthorized":
		return "unhealthy"
	case "rate_limited", "cooldown":
		return "rate_limited"
	}
	if !a.windowExpired(now) && a.limit > 0 && a.used/a.limit*100 >= thresholdPercent {
		return "exhausted"
	}
	fiveHourLive := a.reset5hAt.IsZero() || a.reset5hAt.After(now)
	if a.percent5h != nil && fiveHourLive && *a.percent5h >= thresholdPercent {
		return "exhausted_5h"
	}
	return ""
}

// capacityFactor 是"每单位额度（1 个百分点，或 Kiro 的 1 个 credit）约等于多少请求、多少 token"。
type capacityFactor struct {
	requests, tokens float64
	creditUnit       bool
}

// capacityAccountRate 返回账号的单账号速率：套餐行的非零项优先，其余用默认行；
// RPM 仍为 0 时用 Kiro 账号自己报的 rpm_limit。为 0 的项表示池子不限这一项。
func capacityAccountRate(config model.CapacityPoolConfig, account capacityAccount) dto.ModelLimit {
	rate := config.DefaultRate
	if planRate, ok := config.PlanRates[account.plan]; ok {
		if planRate.RPM > 0 {
			rate.RPM = planRate.RPM
		}
		if planRate.TPM > 0 {
			rate.TPM = planRate.TPM
		}
		if planRate.MaxConcurrency > 0 {
			rate.MaxConcurrency = planRate.MaxConcurrency
		}
	}
	if rate.RPM == 0 && account.rpmLimit > 0 {
		rate.RPM = int(min(account.rpmLimit, dto.MaxModelLimitValue))
	}
	return rate
}

// capacityAccountPace 返回账号按配速可用的 RPM / TPM：
// 阈值内剩余额度 × 换算比例 ÷ 距重置分钟数 × 放宽系数。
// 每一项的换算比例优先用上游累计数（codex2api 的 7 天请求/token 数 ÷ 7 天用量百分比），
// 其次用池子学到的比例。没开配速、重置时间未知或已过、两种比例都没有时这一项算不出。
// 7 天累计数与用量百分比的窗口不一定对齐（滚动 7 天 vs 固定周期），周期刚开始时偏松。
func capacityAccountPace(config model.CapacityPoolConfig, account capacityAccount, learned *capacityFactor, now time.Time) (rpm, tpm float64, rpmKnown, tpmKnown, upstream bool) {
	if !config.PacingEnabled || account.limit <= 0 || account.resetAt.IsZero() || !account.resetAt.After(now) {
		return 0, 0, false, false, false
	}
	budget := config.ThresholdPercent/100*account.limit - account.used
	if budget <= 0 {
		return 0, 0, false, false, false
	}
	upstreamUsable := !account.creditUnit && account.used >= capacityPoolUpstreamMinPercent
	learnedUsable := learned != nil && learned.creditUnit == account.creditUnit
	requestsPerUnit, tokensPerUnit := 0.0, 0.0
	switch {
	case upstreamUsable && account.requests7d > 0:
		requestsPerUnit, upstream = float64(account.requests7d)/account.used, true
	case learnedUsable:
		requestsPerUnit = learned.requests
	}
	switch {
	case upstreamUsable && account.tokens7d > 0:
		tokensPerUnit, upstream = float64(account.tokens7d)/account.used, true
	case learnedUsable:
		tokensPerUnit = learned.tokens
	}
	scale := budget / max(account.resetAt.Sub(now).Minutes(), 1) * config.BurstFactor
	if requestsPerUnit > 0 {
		rpm, rpmKnown = requestsPerUnit*scale, true
	}
	if tokensPerUnit > 0 {
		tpm, tpmKnown = tokensPerUnit*scale, true
	}
	return rpm, tpm, rpmKnown, tpmKnown, upstream
}

// capacityDimension 累加一项（RPM / TPM / 并发）在可用账号上的速率。
// 每个账号取 min(突发, 配速)，只有一个已知时用那个；两个都不知道的账号让这一项不限。
type capacityDimension struct {
	burst, pace, final float64
	paced              int
	unbounded          bool
}

func (d *capacityDimension) add(burst, pace float64, burstKnown, paceKnown bool) {
	if burstKnown {
		d.burst += burst
	}
	if paceKnown {
		d.pace += pace
		d.paced++
	}
	switch {
	case burstKnown && paceKnown:
		d.final += min(burst, pace)
	case burstKnown:
		d.final += burst
	case paceKnown:
		d.final += pace
	default:
		d.unbounded = true
	}
}

// limit 把累加结果取成额度：不限返回 0，否则向上取整、至少 1、不超过 MaxModelLimitValue。
func (d capacityDimension) limit() int64 {
	if d.unbounded {
		return 0
	}
	return min(max(int64(math.Ceil(d.final)), 1), dto.MaxModelLimitValue)
}

func (d capacityDimension) detail(computed int64) CapacityDimensionDetail {
	return CapacityDimensionDetail{
		Burst:         math.Round(d.burst*100) / 100,
		Pace:          math.Round(d.pace*100) / 100,
		PacedAccounts: d.paced,
		Computed:      computed,
		Unbounded:     d.unbounded,
	}
}

// CapacityPoolDetail 是动态额度的计算明细，存在额度的 detail 字段里给管理页展示。
type CapacityPoolDetail struct {
	Accounts    int                     `json:"accounts"`
	Usable      int                     `json:"usable"`
	Excluded    map[string]int          `json:"excluded,omitempty"`
	Plans       []CapacityPlanDetail    `json:"plans"`
	RPM         CapacityDimensionDetail `json:"rpm"`
	TPM         CapacityDimensionDetail `json:"tpm"`
	Concurrency CapacityDimensionDetail `json:"concurrency"`
	Pacing      bool                    `json:"pacing"`
	// UpstreamFactorAccounts 是用上游累计数换算配速的账号数。
	UpstreamFactorAccounts int `json:"upstream_factor_accounts"`
	// Learned 是池子学到的换算比例，还没学到时为空。
	Learned *CapacityFactorDetail `json:"learned,omitempty"`
}

// CapacityPlanDetail 是一个套餐的账号数与可用数，Plan 为空表示上游没报套餐。
type CapacityPlanDetail struct {
	Plan   string `json:"plan"`
	Total  int    `json:"total"`
	Usable int    `json:"usable"`
}

// CapacityDimensionDetail 是一项额度的明细：可用账号的突发上限之和、配速上限之和
// （只含算得出配速的账号）、平滑前的结果（0 表示不限）。
type CapacityDimensionDetail struct {
	Burst         float64 `json:"burst"`
	Pace          float64 `json:"pace"`
	PacedAccounts int     `json:"paced_accounts"`
	Computed      int64   `json:"computed"`
	Unbounded     bool    `json:"unbounded"`
}

// CapacityFactorDetail 是学到的换算比例，Unit 是 credit 或 percent。
type CapacityFactorDetail struct {
	RequestsPerUnit float64 `json:"requests_per_unit"`
	TokensPerUnit   float64 `json:"tokens_per_unit"`
	Unit            string  `json:"unit"`
}

// computeCapacityPoolLimit 按账号状态算一个容量池的动态额度（未平滑），accounts 不为空。
// 每个可用账号取 min(突发上限, 配速上限)，再在可用账号上求和；并发只看突发上限。
// 没有可用账号时额度为"已耗尽"。
func computeCapacityPoolLimit(config model.CapacityPoolConfig, accounts []capacityAccount, learned *capacityFactor, now time.Time) (channelcapacity.PublishedLimit, CapacityPoolDetail) {
	detail := CapacityPoolDetail{Accounts: len(accounts), Pacing: config.PacingEnabled, Excluded: make(map[string]int)}
	plans := make(map[string]*CapacityPlanDetail)
	var rpm, tpm, concurrency capacityDimension
	for _, account := range accounts {
		plan := plans[account.plan]
		if plan == nil {
			plan = &CapacityPlanDetail{Plan: account.plan}
			plans[account.plan] = plan
		}
		plan.Total++
		if reason := account.unusableReason(config.ThresholdPercent, now); reason != "" {
			detail.Excluded[reason]++
			continue
		}
		plan.Usable++
		detail.Usable++
		rate := capacityAccountRate(config, account)
		paceRPM, paceTPM, paceRPMKnown, paceTPMKnown, upstream := capacityAccountPace(config, account, learned, now)
		if upstream {
			detail.UpstreamFactorAccounts++
		}
		rpm.add(float64(rate.RPM), paceRPM, rate.RPM > 0, paceRPMKnown)
		tpm.add(float64(rate.TPM), paceTPM, rate.TPM > 0, paceTPMKnown)
		concurrency.add(float64(rate.MaxConcurrency), 0, rate.MaxConcurrency > 0, false)
	}
	for _, plan := range plans {
		detail.Plans = append(detail.Plans, *plan)
	}
	slices.SortFunc(detail.Plans, func(a, b CapacityPlanDetail) int { return cmp.Compare(a.Plan, b.Plan) })
	if len(detail.Excluded) == 0 {
		detail.Excluded = nil
	}
	if learned != nil {
		unit := "percent"
		if learned.creditUnit {
			unit = "credit"
		}
		detail.Learned = &CapacityFactorDetail{RequestsPerUnit: learned.requests, TokensPerUnit: learned.tokens, Unit: unit}
	}
	limit := channelcapacity.PublishedLimit{ComputedAt: now.Unix()}
	if detail.Usable == 0 {
		limit.Exhausted = true
	} else {
		limit.RPM, limit.TPM, limit.MaxConcurrency = rpm.limit(), tpm.limit(), concurrency.limit()
	}
	detail.RPM, detail.TPM, detail.Concurrency = rpm.detail(limit.RPM), tpm.detail(limit.TPM), concurrency.detail(limit.MaxConcurrency)
	return limit, detail
}

// smoothCapacityLimit 让动态额度降得快、升得慢：每一项比上次低时直接用新值，比上次高时
// 每轮最多上调 25%（至少 1）。没有上次的值、上次或这次是耗尽、某一项在不限与有限之间
// 切换时直接用新值。
func smoothCapacityLimit(next channelcapacity.PublishedLimit, previous *channelcapacity.PublishedLimit) channelcapacity.PublishedLimit {
	if previous == nil || previous.Exhausted || next.Exhausted {
		return next
	}
	next.RPM = smoothCapacityDimension(next.RPM, previous.RPM)
	next.TPM = smoothCapacityDimension(next.TPM, previous.TPM)
	next.MaxConcurrency = smoothCapacityDimension(next.MaxConcurrency, previous.MaxConcurrency)
	return next
}

func smoothCapacityDimension(next, previous int64) int64 {
	if next == 0 || previous == 0 || next <= previous {
		return next
	}
	return min(next, previous+max(int64(float64(previous)*capacityPoolRiseLimit), 1))
}

// capacityLearner 用池子自己的流量学"每单位额度约等于多少请求/token"：相邻两轮之间
// 我们记的累计请求/token 增量 ÷ 范围内账号的额度用量增量之和，回看 6 小时。
// 只放在主节点内存里，重启后重新学。
type capacityLearner struct {
	snapshot   map[string]capacityLearnPoint
	totals     channelcapacity.Totals
	creditUnit bool
	samples    []capacityLearnSample
}

type capacityLearnPoint struct {
	used    float64
	resetAt time.Time
}

type capacityLearnSample struct {
	at                      time.Time
	units, requests, tokens float64
}

// observe 记下这一轮的账号用量与累计数，返回当前学到的比例，还没学到时为 nil。
// totalsKnown 为 false 表示这一轮读不到累计数，丢掉快照，下一轮重新起算。
// 一轮里对不上账的账号（新出现、刚重置、用量变小、窗口已过）太多时丢弃这一轮。
func (l *capacityLearner) observe(accounts []capacityAccount, totals channelcapacity.Totals, totalsKnown bool, now time.Time) *capacityFactor {
	if len(accounts) == 0 {
		return l.factor(accounts)
	}
	creditUnit := accounts[0].creditUnit
	if slices.ContainsFunc(accounts, func(a capacityAccount) bool { return a.creditUnit != creditUnit }) {
		// credits 和百分比混在一个池里没法用一个比例，不学。
		*l = capacityLearner{}
		return nil
	}
	if l.creditUnit != creditUnit {
		l.samples, l.snapshot = nil, nil
	}
	if !totalsKnown {
		l.snapshot = nil
		return l.factor(accounts)
	}
	current := make(map[string]capacityLearnPoint, len(accounts))
	for _, a := range accounts {
		current[a.key] = capacityLearnPoint{used: a.used, resetAt: a.resetAt}
	}
	if l.snapshot != nil && totals.Requests >= l.totals.Requests && totals.Tokens >= l.totals.Tokens {
		units, skipped := 0.0, 0
		for key, point := range current {
			previous, ok := l.snapshot[key]
			if !ok || !previous.resetAt.Equal(point.resetAt) || point.used < previous.used || !point.resetAt.After(now) {
				skipped++
				continue
			}
			units += point.used - previous.used
		}
		if float64(skipped) <= float64(len(current))*capacityPoolLearnMaxSkipped {
			l.samples = append(l.samples, capacityLearnSample{
				at:       now,
				units:    units,
				requests: float64(totals.Requests - l.totals.Requests),
				tokens:   float64(totals.Tokens - l.totals.Tokens),
			})
		}
	}
	l.snapshot, l.totals, l.creditUnit = current, totals, creditUnit
	cutoff := now.Add(-capacityPoolLearnWindow)
	l.samples = slices.DeleteFunc(l.samples, func(s capacityLearnSample) bool { return s.at.Before(cutoff) })
	return l.factor(accounts)
}

// factor 汇总回看窗口里的样本。额度增量要达到"单账号额度的 2 个百分点"才算学到：
// 百分比单位就是 2，credits 按平均单账号额度折算。
func (l *capacityLearner) factor(accounts []capacityAccount) *capacityFactor {
	units, requests, tokens := 0.0, 0.0, 0.0
	for _, sample := range l.samples {
		units += sample.units
		requests += sample.requests
		tokens += sample.tokens
	}
	minUnits := capacityPoolLearnMinPercent
	if l.creditUnit {
		totalLimit := 0.0
		for _, a := range accounts {
			totalLimit += a.limit
		}
		if len(accounts) == 0 || totalLimit <= 0 {
			return nil
		}
		minUnits = totalLimit / float64(len(accounts)) * capacityPoolLearnMinPercent / 100
	}
	if units <= 0 || units < minUnits {
		return nil
	}
	return &capacityFactor{requests: requests / units, tokens: tokens / units, creditUnit: l.creditUnit}
}

// capacityPoolState 是主节点上一个容量池的计算状态，只由轮询协程读写。
type capacityPoolState struct {
	learner        capacityLearner
	published      *channelcapacity.PublishedLimit
	publishedUntil time.Time
}

var (
	capacityPoolStates     = make(map[int]*capacityPoolState)
	capacityPoolPollerOnce sync.Once
	capacityPoolWakeup     = make(chan struct{}, 1)
)

// StartCapacityPoolPoller 在主节点上按 CAPACITY_POOL_POLL_SECONDS（默认 60 秒）重算
// 所有容量池的动态额度。每个节点都能接请求，但上游管理接口只由主节点拉。
func StartCapacityPoolPoller() {
	capacityPoolPollerOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		seconds := max(common.GetEnvOrDefault(capacityPoolPollSecondsEnv, defaultCapacityPoolPollSeconds), minCapacityPoolPollSeconds)
		interval := time.Duration(seconds) * time.Second
		gopool.Go(func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				refreshCapacityPools(time.Now())
				select {
				case <-ticker.C:
				case <-capacityPoolWakeup:
				}
			}
		})
	})
}

// WakeCapacityPoolPoller 让主节点马上重算一次，保存容量池后调用，不阻塞。
// 非主节点上没有轮询协程，什么也不发生，等主节点下一轮。
func WakeCapacityPoolPoller() {
	select {
	case capacityPoolWakeup <- struct{}{}:
	default:
	}
}

// refreshCapacityPools 重算所有容量池一次，每个 provider 只拉一次上游。
func refreshCapacityPools(now time.Time) {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("capacity pool refresh panic: %v", recovered))
		}
	}()
	pools, err := model.GetCapacityPools("")
	if err != nil {
		common.SysError(common.LogText("failed to load capacity pools: %s", err.Error()))
		return
	}
	active := make(map[int]bool, len(pools))
	byProvider := make(map[string][]*model.CapacityPool)
	for _, pool := range pools {
		active[pool.Id] = true
		byProvider[pool.Provider] = append(byProvider[pool.Provider], pool)
	}
	for id := range capacityPoolStates {
		if !active[id] {
			delete(capacityPoolStates, id)
		}
	}
	for provider, providerPools := range byProvider {
		usage, fetchErr := refreshResourcePoolUsage(provider)
		for _, pool := range providerPools {
			refreshCapacityPool(pool, usage, fetchErr, now)
		}
	}
}

// refreshCapacityPool 重算一个容量池并发布。拉取失败、范围内的来源失败、范围里一个账号
// 也没有时只记状态，不动已发布的额度：它在宽限期后过期，调度器回到保底额度。
// 管理页请求过重置时先丢掉这个池的计算状态：学到的换算比例从头再学，这一轮算出的额度
// 不受上次发布值的上调限制，直接发布。
func refreshCapacityPool(pool *model.CapacityPool, usage *ResourcePoolResult, fetchErr error, now time.Time) {
	poolID := channelcapacity.CapacityPoolID(pool.Id)
	status := channelcapacity.PollStatus{Status: "ok", AttemptAt: now.Unix()}
	defer func() {
		if err := channelcapacity.PublishStatus(poolID, status); err != nil {
			common.SysError(common.LogText("failed to publish capacity pool status: %s", err.Error()))
		}
	}()
	if reset, err := channelcapacity.TakeResetRequest(poolID); err != nil {
		// 没取到就留着请求，下一轮再取。
		common.SysError(common.LogText("failed to read the reset request of capacity pool %s: %s", poolID, err.Error()))
	} else if reset {
		delete(capacityPoolStates, pool.Id)
	}
	config, err := pool.ParseConfig()
	if err != nil {
		status.Status, status.Error = "config_error", err.Error()
		return
	}
	if fetchErr != nil {
		status.Status, status.Error = "fetch_failed", fetchErr.Error()
		return
	}
	for _, source := range usage.Sources {
		if slices.Contains(config.SourceIds, source.SourceId) && source.Status == "failed" {
			status.Status = "source_failed"
			status.Error = cmp.Or(source.SourceName, source.SourceEndpoint) + ": " + source.Error
			return
		}
	}
	windowed := pool.Provider != model.ResourcePoolClaude
	accounts := make([]capacityAccount, 0)
	for _, account := range usage.Accounts {
		if !slices.Contains(config.SourceIds, account.SourceId) {
			continue
		}
		if len(config.Groups) > 0 && !slices.Contains(config.Groups, resourcePoolAccountGroup(account, account.SourceGroup)) {
			continue
		}
		accounts = append(accounts, capacityAccountOf(account, windowed))
	}
	if len(accounts) == 0 {
		status.Status = "empty_scope"
		return
	}
	state := capacityPoolStates[pool.Id]
	if state == nil {
		state = &capacityPoolState{}
		capacityPoolStates[pool.Id] = state
	}
	totals, totalsErr := channelcapacity.ReadTotals([]string{poolID})
	learned := state.learner.observe(accounts, totals[poolID], totalsErr == nil, now)
	limit, detail := computeCapacityPoolLimit(config, accounts, learned, now)
	if state.published != nil && !now.Before(state.publishedUntil) {
		// 上次发布的已经过期，调度器已回到保底额度，这次直接用新值。
		state.published = nil
	}
	published := smoothCapacityLimit(limit, state.published)
	if encoded, err := common.Marshal(detail); err == nil {
		published.Detail = string(encoded)
	}
	grace := time.Duration(config.GraceSeconds) * time.Second
	if err := channelcapacity.PublishLimit(poolID, published, grace); err != nil {
		status.Status, status.Error = "publish_failed", err.Error()
		return
	}
	state.published, state.publishedUntil = &published, now.Add(grace)
}

// CapacityPoolLimitView 是一个容量池此刻生效的额度。
type CapacityPoolLimitView struct {
	PoolID string `json:"pool_id"`
	// Limit 为 0 的项不限；Exhausted 为 true 时没有可用账号，Limit 没有意义。
	Limit dto.ModelLimit `json:"limit"`
	// Source 是 dynamic（主节点发布的动态额度）或 floor（还没发布或已过期，用保底额度）。
	Source     string                      `json:"source"`
	Exhausted  bool                        `json:"exhausted"`
	ComputedAt int64                       `json:"computed_at"`
	Detail     *CapacityPoolDetail         `json:"detail,omitempty"`
	Status     *channelcapacity.PollStatus `json:"status,omitempty"`
}

// ResolveCapacityPoolLimits 返回这些容量池此刻生效的额度：没过期的动态额度，否则是保底额度。
func ResolveCapacityPoolLimits(pools []*model.CapacityPool) (map[int]CapacityPoolLimitView, error) {
	poolIDs := make([]string, 0, len(pools))
	for _, pool := range pools {
		poolIDs = append(poolIDs, channelcapacity.CapacityPoolID(pool.Id))
	}
	limits, err := channelcapacity.ReadLimits(poolIDs)
	if err != nil {
		return nil, err
	}
	statuses, err := channelcapacity.ReadStatuses(poolIDs)
	if err != nil {
		return nil, err
	}
	views := make(map[int]CapacityPoolLimitView, len(pools))
	for _, pool := range pools {
		poolID := channelcapacity.CapacityPoolID(pool.Id)
		view := CapacityPoolLimitView{PoolID: poolID, Source: "floor"}
		if config, err := pool.ParseConfig(); err == nil {
			view.Limit = config.Floor
		}
		if published, ok := limits[poolID]; ok {
			view.Source, view.Exhausted, view.ComputedAt = "dynamic", published.Exhausted, published.ComputedAt
			view.Limit = dto.ModelLimit{RPM: int(published.RPM), TPM: int(published.TPM), MaxConcurrency: int(published.MaxConcurrency)}
			var detail CapacityPoolDetail
			if published.Detail != "" && common.UnmarshalJsonStr(published.Detail, &detail) == nil {
				view.Detail = &detail
			}
		}
		if status, ok := statuses[poolID]; ok {
			view.Status = &status
		}
		views[pool.Id] = view
	}
	return views, nil
}
