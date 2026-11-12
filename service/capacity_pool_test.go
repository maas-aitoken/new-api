package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var capacityTestNow = time.Unix(1_800_000_000, 0).UTC()

func TestComputeCapacityPoolLimitBurstAndUsability(t *testing.T) {
	now := capacityTestNow
	future := now.Add(48 * time.Hour)
	config := model.CapacityPoolConfig{
		ThresholdPercent: 95,
		PlanRates:        map[string]dto.ModelLimit{"pro": {RPM: 40, MaxConcurrency: 3}},
		DefaultRate:      dto.ModelLimit{RPM: 10, MaxConcurrency: 2},
	}
	usable := capacityAccount{plan: "plus", enabled: true, used: 30, limit: 100, resetAt: future}
	accounts := []capacityAccount{
		usable,
		{plan: "pro", enabled: true, used: 10, limit: 100, resetAt: future},
		{plan: "plus", enabled: false, used: 0, limit: 100, resetAt: future},
		{plan: "plus", enabled: true, status: "banned", used: 0, limit: 100, resetAt: future},
		{plan: "plus", enabled: true, status: "cooldown", used: 0, limit: 100, resetAt: future},
		{plan: "plus", enabled: true, used: 95, limit: 100, resetAt: future},
		{plan: "plus", enabled: true, used: 10, limit: 100, resetAt: future, percent5h: common.GetPointer(97.0), reset5hAt: now.Add(time.Hour)},
		// 5h 窗口已过重置时间、7 天窗口也已过：都按新窗口算，可用。
		{plan: "plus", enabled: true, used: 99, limit: 100, resetAt: now.Add(-time.Minute), percent5h: common.GetPointer(99.0), reset5hAt: now.Add(-time.Minute)},
	}

	limit, detail := computeCapacityPoolLimit(config, accounts, nil, now)
	// 可用：两个 plus（各 10 RPM / 2 并发）+ 一个 pro（套餐行 40 RPM / 3 并发）。TPM 没人填，不限。
	assert.Equal(t, channelcapacity.PublishedLimit{RPM: 60, TPM: 0, MaxConcurrency: 7, ComputedAt: now.Unix()}, limit)
	assert.Equal(t, 8, detail.Accounts)
	assert.Equal(t, 3, detail.Usable)
	assert.Equal(t, map[string]int{"disabled": 1, "unhealthy": 1, "rate_limited": 1, "exhausted": 1, "exhausted_5h": 1}, detail.Excluded)
	assert.Equal(t, []CapacityPlanDetail{{Plan: "plus", Total: 7, Usable: 2}, {Plan: "pro", Total: 1, Usable: 1}}, detail.Plans)
	assert.True(t, detail.TPM.Unbounded)
	assert.Equal(t, CapacityDimensionDetail{Burst: 60, Computed: 60}, detail.RPM)

	// 没有可用账号：耗尽。
	limit, detail = computeCapacityPoolLimit(config, accounts[2:6], nil, now)
	assert.True(t, limit.Exhausted)
	assert.Zero(t, detail.Usable)

	// Kiro：RPM 格子都空时用账号自己报的 rpm_limit；套餐行只填了 RPM 时并发仍回落到默认行。
	kiro := model.CapacityPoolConfig{ThresholdPercent: 95, PlanRates: map[string]dto.ModelLimit{"POWER": {TPM: 5000}}, DefaultRate: dto.ModelLimit{MaxConcurrency: 1}}
	limit, _ = computeCapacityPoolLimit(kiro, []capacityAccount{
		{plan: "POWER", enabled: true, used: 100, limit: 10000, creditUnit: true, rpmLimit: 1000, resetAt: future},
		{plan: "POWER", enabled: true, used: 9600, limit: 10000, creditUnit: true, rpmLimit: 1000, resetAt: future},
	}, nil, now)
	assert.Equal(t, channelcapacity.PublishedLimit{RPM: 1000, TPM: 5000, MaxConcurrency: 1, ComputedAt: now.Unix()}, limit, "96% 的账号不计入")
}

func TestComputeCapacityPoolLimitPacing(t *testing.T) {
	now := capacityTestNow
	config := model.CapacityPoolConfig{ThresholdPercent: 95, PacingEnabled: true, BurstFactor: 2, DefaultRate: dto.ModelLimit{RPM: 100}}

	// 上游累计数：用了 20%，7 天 200 次请求 / 200 万 token → 每个百分点 10 次 / 10 万 token。
	// 剩余 75 个百分点、离重置 600 分钟、b=2 → 每分钟 75/600×2=0.25 个百分点 → 2.5 RPM / 25000 TPM。
	codex := capacityAccount{plan: "pro", enabled: true, used: 20, limit: 100, resetAt: now.Add(600 * time.Minute), requests7d: 200, tokens7d: 2_000_000}
	limit, detail := computeCapacityPoolLimit(config, []capacityAccount{codex, codex}, nil, now)
	assert.Equal(t, int64(5), limit.RPM, "每个账号取 min(100, 2.5)")
	assert.Equal(t, int64(50000), limit.TPM, "TPM 没有突发上限，配速就是上限")
	assert.Equal(t, 2, detail.UpstreamFactorAccounts)
	assert.Equal(t, CapacityDimensionDetail{Burst: 200, Pace: 5, PacedAccounts: 2, Computed: 5}, detail.RPM)

	// 用量低于 5% 时上游累计数不准，也还没学到比例：不配速，只看突发上限。
	early := codex
	early.used = 3
	limit, detail = computeCapacityPoolLimit(config, []capacityAccount{early}, nil, now)
	assert.Equal(t, int64(100), limit.RPM)
	assert.Zero(t, limit.TPM)
	assert.Zero(t, detail.UpstreamFactorAccounts)

	// Kiro 用学到的比例：剩 500 credits、离重置 1000 分钟、b=2 → 每分钟 1 credit；
	// 每 credit 0.5 次请求 / 2000 token → 每个账号 0.5 RPM / 2000 TPM，两个账号向上取整。
	kiro := capacityAccount{plan: "POWER", enabled: true, used: 9000, limit: 10000, creditUnit: true, resetAt: now.Add(1000 * time.Minute)}
	learned := &capacityFactor{requests: 0.5, tokens: 2000, creditUnit: true}
	limit, _ = computeCapacityPoolLimit(config, []capacityAccount{kiro, kiro}, learned, now)
	assert.Equal(t, int64(1), limit.RPM)
	assert.Equal(t, int64(4000), limit.TPM)

	// 单位对不上的比例不用；关了配速也不用。
	limit, _ = computeCapacityPoolLimit(config, []capacityAccount{kiro}, &capacityFactor{requests: 0.5, tokens: 2000}, now)
	assert.Equal(t, channelcapacity.PublishedLimit{RPM: 100, ComputedAt: now.Unix()}, limit)
	config.PacingEnabled = false
	limit, _ = computeCapacityPoolLimit(config, []capacityAccount{kiro}, learned, now)
	assert.Equal(t, channelcapacity.PublishedLimit{RPM: 100, ComputedAt: now.Unix()}, limit)
}

func TestCapacityAccountOfNormalizesProviders(t *testing.T) {
	grok := capacityAccountOf(&model.ResourcePoolAccount{
		Id: "68", SourceId: 3, PlanType: "SuperGrokPro", Enabled: true, Status: " Active ",
		UsagePercentage: 22, WeeklyPercent: common.GetPointer(22.0), Requests7d: 264, Tokens7d: 29794793,
		Reset7dAt: "2026-10-22T07:25:36Z", UsagePercent5h: common.GetPointer(40.0), Reset5hAt: "2026-10-21T12:00:00Z",
	}, true)
	assert.Equal(t, "3/68", grok.key)
	assert.Equal(t, "SuperGrokPro", grok.plan)
	assert.Equal(t, "active", grok.status)
	assert.Equal(t, 22.0, grok.used)
	assert.Equal(t, 100.0, grok.limit)
	assert.False(t, grok.creditUnit)
	assert.True(t, grok.resetAt.Equal(time.Date(2026, 10, 22, 7, 25, 36, 0, time.UTC)))
	assert.Equal(t, int64(264), grok.requests7d)

	// codex2api 的百分比优先级与资源池页一致：weekly_percent > usage_percent_7d > usage_percentage。
	gemini := capacityAccountOf(&model.ResourcePoolAccount{UsagePercentage: 10, UsagePercent7d: common.GetPointer(75.0)}, true)
	assert.Equal(t, 75.0, gemini.used)

	kiro := capacityAccountOf(&model.ResourcePoolAccount{
		Email: "a@example.com", SourceId: 1, SubscriptionType: "POWER", Enabled: true,
		UsageCurrent: 2500, UsageLimit: 10000, RpMLimit: 1000, NextResetDate: "2026-11-01",
	}, false)
	assert.Equal(t, "1/a@example.com", kiro.key, "没有 id 时用邮箱")
	assert.Equal(t, "POWER", kiro.plan)
	assert.Equal(t, 2500.0, kiro.used)
	assert.Equal(t, 10000.0, kiro.limit)
	assert.True(t, kiro.creditUnit)
	assert.True(t, kiro.resetAt.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)), "只有日期时按当天结束算")
	assert.Equal(t, int64(1000), kiro.rpmLimit)
}

func TestSmoothCapacityLimit(t *testing.T) {
	tests := []struct {
		name     string
		previous *channelcapacity.PublishedLimit
		next     channelcapacity.PublishedLimit
		want     int64
	}{
		{name: "first publish", previous: nil, next: channelcapacity.PublishedLimit{RPM: 400}, want: 400},
		{name: "drop applies at once", previous: &channelcapacity.PublishedLimit{RPM: 400}, next: channelcapacity.PublishedLimit{RPM: 100}, want: 100},
		{name: "rise capped at 25%", previous: &channelcapacity.PublishedLimit{RPM: 100}, next: channelcapacity.PublishedLimit{RPM: 400}, want: 125},
		{name: "rise at least 1", previous: &channelcapacity.PublishedLimit{RPM: 2}, next: channelcapacity.PublishedLimit{RPM: 9}, want: 3},
		{name: "recover from exhausted", previous: &channelcapacity.PublishedLimit{Exhausted: true}, next: channelcapacity.PublishedLimit{RPM: 400}, want: 400},
		{name: "unlimited to limited", previous: &channelcapacity.PublishedLimit{RPM: 0}, next: channelcapacity.PublishedLimit{RPM: 400}, want: 400},
		{name: "limited to unlimited", previous: &channelcapacity.PublishedLimit{RPM: 100}, next: channelcapacity.PublishedLimit{RPM: 0}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, smoothCapacityLimit(tt.next, tt.previous).RPM)
		})
	}
	exhausted := smoothCapacityLimit(channelcapacity.PublishedLimit{Exhausted: true}, &channelcapacity.PublishedLimit{RPM: 100})
	assert.True(t, exhausted.Exhausted, "耗尽立即生效")
}

// kiroLearnAccounts 是配速学习测试用的四个 Kiro 账号，used 依次取给定值。
func kiroLearnAccounts(resetAt time.Time, used ...float64) []capacityAccount {
	accounts := make([]capacityAccount, 0, len(used))
	for i, value := range used {
		accounts = append(accounts, capacityAccount{
			key: string(rune('a' + i)), enabled: true, used: value, limit: 10000, creditUnit: true, resetAt: resetAt,
		})
	}
	return accounts
}

func TestCapacityLearnerLearnsFromOwnTraffic(t *testing.T) {
	now := capacityTestNow
	resetAt := now.Add(10 * 24 * time.Hour)
	var learner capacityLearner

	assert.Nil(t, learner.observe(kiroLearnAccounts(resetAt, 0, 0, 0, 0), channelcapacity.Totals{}, true, now), "第一轮只记快照")
	// 四个账号共涨 400 credits（超过单账号额度的 2%），我们记了 200 次请求、80 万 token。
	now = now.Add(time.Minute)
	factor := learner.observe(kiroLearnAccounts(resetAt, 100, 100, 100, 100), channelcapacity.Totals{Requests: 200, Tokens: 800_000}, true, now)
	require.NotNil(t, factor)
	assert.Equal(t, capacityFactor{requests: 0.5, tokens: 2000, creditUnit: true}, *factor)

	// 一个账号刚重置（重置时间变了、用量变小），不计它的变化；另三个各涨 100，比例不变。
	now = now.Add(time.Minute)
	accounts := kiroLearnAccounts(resetAt, 200, 200, 200, 0)
	accounts[3].resetAt = resetAt.Add(30 * 24 * time.Hour)
	factor = learner.observe(accounts, channelcapacity.Totals{Requests: 350, Tokens: 1_400_000}, true, now)
	require.NotNil(t, factor)
	assert.Equal(t, capacityFactor{requests: 0.5, tokens: 2000, creditUnit: true}, *factor)

	// 对不上账的账号超过四分之一：整轮丢弃（这一轮新增的 1 万次请求不算进去）。
	now = now.Add(time.Minute)
	accounts = kiroLearnAccounts(resetAt, 300, 300, 0, 1)
	accounts[2].resetAt, accounts[3].resetAt = accounts[3].resetAt.Add(time.Hour), resetAt.Add(time.Hour)
	factor = learner.observe(accounts, channelcapacity.Totals{Requests: 10_350, Tokens: 1_500_000}, true, now)
	assert.Equal(t, capacityFactor{requests: 0.5, tokens: 2000, creditUnit: true}, *factor)

	// 读不到累计数：保留学到的，但丢掉快照，下一轮不会把两轮的差额算成一轮。
	now = now.Add(time.Minute)
	assert.NotNil(t, learner.observe(accounts, channelcapacity.Totals{}, false, now))
	assert.Nil(t, learner.snapshot)

	// 回看窗口外的样本过期，学到的比例随之失效。
	now = now.Add(7 * time.Hour)
	assert.Nil(t, learner.observe(accounts, channelcapacity.Totals{Requests: 20_000, Tokens: 2_000_000}, true, now))

	// 额度增量太小（不到单账号额度的 2%）不算学到。
	var small capacityLearner
	small.observe(kiroLearnAccounts(resetAt, 0, 0, 0, 0), channelcapacity.Totals{}, true, now)
	assert.Nil(t, small.observe(kiroLearnAccounts(resetAt, 40, 40, 40, 40), channelcapacity.Totals{Requests: 80, Tokens: 1000}, true, now.Add(time.Minute)))

	// credits 与百分比混在一个池里不学。
	mixed := append(kiroLearnAccounts(resetAt, 0), capacityAccount{key: "x", used: 1, limit: 100, resetAt: resetAt})
	assert.Nil(t, learner.observe(mixed, channelcapacity.Totals{Requests: 1}, true, now))
}

// useCapacityPoolRedis 建好容量池用到的表，接上一个 miniredis，清空主节点的计算状态。
func useCapacityPoolRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.ResourcePoolSource{}, &model.CapacityPool{}, &model.Channel{}))
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	capacityPoolStates = make(map[int]*capacityPoolState)
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousClient
		capacityPoolStates = make(map[int]*capacityPoolState)
		_ = client.Close()
	})
	return server
}

func TestRefreshCapacityPoolPublishesDynamicLimitAndKeepsItOnFailure(t *testing.T) {
	server := useCapacityPoolRedis(t)

	provider := model.ResourcePoolGemini
	require.NoError(t, model.SaveResourcePoolSources(provider, []*model.ResourcePoolSource{
		{Name: "antigravity-a", Endpoint: "https://a.example.com", Enabled: true},
		{Name: "antigravity-b", Endpoint: "https://b.example.com", Enabled: true},
	}))
	t.Cleanup(func() { require.NoError(t, model.SaveResourcePoolSources(provider, nil)) })
	sources, err := model.GetResourcePoolSources(provider)
	require.NoError(t, err)
	require.Len(t, sources, 2)
	inScope, outOfScope := sources[0].Id, sources[1].Id

	pool := &model.CapacityPool{Name: "gemini-ultra", Provider: provider}
	require.NoError(t, model.SaveCapacityPool(pool, model.CapacityPoolConfig{
		SourceIds: []int64{inScope}, Groups: []string{"ultra"}, DefaultRate: dto.ModelLimit{RPM: 100, MaxConcurrency: 4},
		Floor: dto.ModelLimit{RPM: 50}, GraceSeconds: 120,
	}))
	t.Cleanup(func() { _ = model.DeleteCapacityPool(pool.Id) })

	account := func(source int64, id, group string, enabled bool) *model.ResourcePoolAccount {
		return &model.ResourcePoolAccount{Id: id, SourceId: source, Groups: []string{group}, Enabled: enabled, Status: "active", UsagePercent7d: common.GetPointer(10.0)}
	}
	usage := &ResourcePoolResult{
		Accounts: []*model.ResourcePoolAccount{
			account(inScope, "1", "ultra", true),
			account(inScope, "2", "ultra", true),
			account(inScope, "3", "pro", true),      // 分组不在范围内
			account(outOfScope, "4", "ultra", true), // 来源不在范围内
		},
		Sources: []*model.ResourcePoolSourceStatus{{SourceId: inScope, Status: "success"}, {SourceId: outOfScope, Status: "failed", Error: "boom"}},
	}
	resolve := func() CapacityPoolLimitView {
		views, err := ResolveCapacityPoolLimits([]*model.CapacityPool{pool})
		require.NoError(t, err)
		return views[pool.Id]
	}
	now := time.Now()

	refreshCapacityPool(pool, usage, nil, now)
	view := resolve()
	assert.Equal(t, "dynamic", view.Source)
	assert.Equal(t, dto.ModelLimit{RPM: 200, MaxConcurrency: 8}, view.Limit, "只算范围内的两个账号；范围外来源失败不影响")
	require.NotNil(t, view.Detail)
	assert.Equal(t, 2, view.Detail.Usable)
	assert.Equal(t, "ok", view.Status.Status)

	// 多了两个账号（算出来 400 RPM / 16 并发）：上调每轮最多 25%。
	usage.Accounts = append(usage.Accounts, account(inScope, "5", "ultra", true), account(inScope, "6", "ultra", true))
	refreshCapacityPool(pool, usage, nil, now.Add(time.Minute))
	assert.Equal(t, dto.ModelLimit{RPM: 250, MaxConcurrency: 10}, resolve().Limit)

	// 范围内的来源失败、拉取失败、范围里一个账号也没有：都只记状态，额度沿用上次。
	failed := &ResourcePoolResult{Accounts: usage.Accounts, Sources: []*model.ResourcePoolSourceStatus{{SourceId: inScope, SourceName: "antigravity-a", Status: "failed", Error: "HTTP 502"}}}
	refreshCapacityPool(pool, failed, nil, now.Add(2*time.Minute))
	view = resolve()
	assert.Equal(t, "source_failed", view.Status.Status)
	assert.Equal(t, "antigravity-a: HTTP 502", view.Status.Error)
	assert.Equal(t, dto.ModelLimit{RPM: 250, MaxConcurrency: 10}, view.Limit)
	refreshCapacityPool(pool, nil, errors.New("timeout"), now.Add(3*time.Minute))
	assert.Equal(t, "fetch_failed", resolve().Status.Status)
	refreshCapacityPool(pool, &ResourcePoolResult{}, nil, now.Add(4*time.Minute))
	view = resolve()
	assert.Equal(t, "empty_scope", view.Status.Status)
	assert.Equal(t, "dynamic", view.Source)

	// 过了宽限期，动态额度过期，退回保底额度。
	server.FastForward(121 * time.Second)
	view = resolve()
	assert.Equal(t, "floor", view.Source)
	assert.Equal(t, dto.ModelLimit{RPM: 50}, view.Limit)

	// 过期后再拉到：调度器已回到保底额度，直接用新值，不再从过期的 250 往上爬。
	refreshCapacityPool(pool, usage, nil, now.Add(5*time.Minute))
	assert.Equal(t, dto.ModelLimit{RPM: 400, MaxConcurrency: 16}, resolve().Limit)

	// 拉取成功但账号都停用了：耗尽。恢复时直接用新值，不受 25% 限制。
	disabled := &ResourcePoolResult{Accounts: []*model.ResourcePoolAccount{account(inScope, "1", "ultra", false)}}
	refreshCapacityPool(pool, disabled, nil, now.Add(10*time.Minute))
	view = resolve()
	assert.Equal(t, "dynamic", view.Source)
	assert.True(t, view.Exhausted)
	refreshCapacityPool(pool, usage, nil, now.Add(11*time.Minute))
	view = resolve()
	assert.False(t, view.Exhausted)
	assert.Equal(t, dto.ModelLimit{RPM: 400, MaxConcurrency: 16}, view.Limit)
}

// 复现 2026-11-06 的事故：池里的账号还在被别处用，我们只记到一个请求，学到的比例把配速压到 1；
// 关掉配速后额度每轮只涨 25%，重置之后下一轮直接按突发上限发布。
func TestResetCapacityPoolDropsLearnedFactorAndSmoothing(t *testing.T) {
	useCapacityPoolRedis(t)
	provider := model.ResourcePoolGrok
	require.NoError(t, model.SaveResourcePoolSources(provider, []*model.ResourcePoolSource{{Name: "grok-a", Endpoint: "https://grok.example.com", Enabled: true}}))
	t.Cleanup(func() { require.NoError(t, model.SaveResourcePoolSources(provider, nil)) })
	sources, err := model.GetResourcePoolSources(provider)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	source := sources[0].Id

	config := model.CapacityPoolConfig{SourceIds: []int64{source}, DefaultRate: dto.ModelLimit{RPM: 100}, PacingEnabled: true}
	pool := &model.CapacityPool{Name: "grok-shared", Provider: provider}
	require.NoError(t, model.SaveCapacityPool(pool, config))
	t.Cleanup(func() { _ = model.DeleteCapacityPool(pool.Id) })

	now := time.Now().Truncate(time.Second)
	resetAt := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	// 7 天累计数为 0：上游比例用不上，配速只能靠学到的比例。
	accounts := func(percent float64) *ResourcePoolResult {
		result := &ResourcePoolResult{Sources: []*model.ResourcePoolSourceStatus{{SourceId: source, Status: "success"}}}
		for _, id := range []string{"1", "2"} {
			result.Accounts = append(result.Accounts, &model.ResourcePoolAccount{
				Id: id, SourceId: source, Enabled: true, Status: "active", UsagePercent7d: common.GetPointer(percent), Reset7dAt: resetAt,
			})
		}
		return result
	}
	resolve := func() CapacityPoolLimitView {
		views, err := ResolveCapacityPoolLimits([]*model.CapacityPool{pool})
		require.NoError(t, err)
		return views[pool.Id]
	}

	refreshCapacityPool(pool, accounts(10), nil, now)
	assert.Equal(t, dto.ModelLimit{RPM: 200}, resolve().Limit, "还没学到比例，按突发上限")

	// 两个账号各涨 20 个百分点，我们只记到一个请求：每个百分点 0.025 次请求，配速压到 1。
	channelcapacity.Finish(channelcapacity.Begin(5, "grok-4", pool.Id, 1000), true, 1000, 0)
	refreshCapacityPool(pool, accounts(30), nil, now.Add(time.Minute))
	view := resolve()
	assert.Equal(t, dto.ModelLimit{RPM: 1, TPM: 1}, view.Limit)
	require.NotNil(t, view.Detail)
	require.NotNil(t, view.Detail.Learned)

	// 关掉配速：学到的比例不用了，但额度从 1 往回涨每轮最多 25%（至少 1），比例也还留着。
	config.PacingEnabled = false
	require.NoError(t, model.SaveCapacityPool(pool, config))
	refreshCapacityPool(pool, accounts(30), nil, now.Add(2*time.Minute))
	view = resolve()
	assert.Equal(t, dto.ModelLimit{RPM: 2}, view.Limit)
	require.NotNil(t, view.Detail)
	assert.NotNil(t, view.Detail.Learned, "关掉配速只是不用比例，再打开会接着用")

	// 重置：下一轮直接按突发上限发布，学到的比例清空。
	require.NoError(t, channelcapacity.RequestReset(channelcapacity.CapacityPoolID(pool.Id)))
	refreshCapacityPool(pool, accounts(30), nil, now.Add(3*time.Minute))
	view = resolve()
	assert.Equal(t, dto.ModelLimit{RPM: 200}, view.Limit)
	require.NotNil(t, view.Detail)
	assert.Nil(t, view.Detail.Learned)
}
