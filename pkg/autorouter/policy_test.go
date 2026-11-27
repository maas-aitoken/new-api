package autorouter

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectRoute(t *testing.T) {
	policy := Policy{
		TaskModels: map[string][]string{
			"code.build":   {"coder", "coder-b"},
			"code":         {"code-class"},
			"general.chat": {"chat"},
			"code.ops":     {"gone"},
		},
		DefaultModels:          []string{"general"},
		DifficultyEfforts:      []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:          "medium",
		UpgradeBenefit:         0.5,
		ContinuationTaskChange: 0.5,
	}
	p := func(v float64) *float64 { return &v }
	decide := func(taskType string, difficulty int, taskChange, benefit float64) *Decision {
		return &Decision{TaskType: taskType, TaskTypeProbability: 0.9, Difficulty: difficulty, TaskChange: p(taskChange), LargerModelBenefit: p(benefit)}
	}
	// spread splits the probability over task types; the chosen one is the
	// most likely.
	spread := func(probabilities map[string]float64) *Decision {
		d := &Decision{Difficulty: 1, TaskChange: p(1), TaskTypeProbabilities: probabilities}
		for taskType, probability := range probabilities {
			if probability > d.TaskTypeProbability {
				d.TaskType, d.TaskTypeProbability = taskType, probability
			}
		}
		return d
	}
	coderHigh := &Route{Profile: "code.build", Model: "coder", Effort: "high"}
	tests := []struct {
		name          string
		decision      *Decision
		previous      *Route
		available     []string
		want          Route
		wantInherited bool
		wantOK        bool
	}{
		{"task picks model, difficulty picks effort", decide("code.build", 2, 1, 0.1), nil, nil, Route{Profile: "code.build", Model: "coder", Effort: "medium"}, false, true},
		{"simple question", decide("general.chat", 1, 1, 0.1), nil, nil, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"larger model benefit raises effort", decide("code.build", 3, 1, 0.7), nil, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"rung past the table uses the last effort", decide("code.build", 6, 1, 0.1), nil, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"skips unavailable task model", decide("code.build", 1, 1, 0.1), nil, []string{"coder-b-low", "general"}, Route{Profile: "code.build", Model: "coder-b", Effort: "low"}, false, true},
		{"unavailable task models use the class", decide("code.ops", 1, 1, 0.1), nil, []string{"code-class", "general"}, Route{Profile: "code", Model: "code-class", Effort: "low"}, false, true},
		{"task type without a list uses the class", decide("code.frontend", 1, 1, 0.1), nil, nil, Route{Profile: "code", Model: "code-class", Effort: "low"}, false, true},
		{"class without a list uses defaults", decide("general.research", 4, 1, 0.1), nil, nil, Route{Model: "general", Effort: "xhigh"}, false, true},
		{"the most probable task type routes however low its probability", spread(map[string]float64{"general.chat": 0.3, "code.build": 0.25, "code.ops": 0.25, "unknown": 0.2}), nil, nil, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"the most probable unknown inherits even when a class sums higher", spread(map[string]float64{"unknown": 0.4, "code.build": 0.3, "code.ops": 0.3}), coderHigh, nil, *coderHigh, true, true},
		{"unknown without previous uses defaults", spread(map[string]float64{"unknown": 0.9, "code.build": 0.1}), nil, nil, Route{Model: "general", Effort: "medium"}, true, true},
		{"continuation lowers model effort when the current task is easier and a larger model has little benefit", decide("general.chat", 1, 0.1, 0.1), coderHigh, nil, Route{Profile: "code.build", Model: "coder", Effort: "low"}, false, true},
		{"continuation may raise effort", decide("code.ops", 4, 0.1, 0.1), coderHigh, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"larger model benefit raises a continuation above the previous effort", decide("general.chat", 1, 0.1, 0.7), coderHigh, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"repeated dissatisfaction keeps raising above the previous effort", decide("general.chat", 1, 0.1, 0.7), &Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, nil, Route{Profile: "code.build", Model: "coder", Effort: "max"}, false, true},
		{"a difficulty already above the previous effort is not raised again", decide("code.build", 4, 0.1, 0.7), &Route{Profile: "code.build", Model: "coder", Effort: "medium"}, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"larger model benefit raises an unknown task above the previous effort", decide(TaskTypeUnknown, 1, 0.1, 0.7), coderHigh, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"larger model benefit raises an unknown task without previous on the defaults", decide(TaskTypeUnknown, 2, 1, 0.7), nil, nil, Route{Model: "general", Effort: "high"}, false, true},
		{"new task switches model", decide("general.chat", 1, 0.9, 0.1), coderHigh, nil, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"continuation leaves an unavailable model", decide("general.chat", 1, 0.1, 0.1), coderHigh, []string{"chat"}, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"unknown inherits previous", decide(TaskTypeUnknown, 1, 0.1, 0.1), coderHigh, nil, *coderHigh, true, true},
		{"low probability still leaves the previous route", &Decision{TaskType: "general.chat", TaskTypeProbability: 0.3, Difficulty: 1}, coderHigh, nil, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"jev failure without previous uses defaults", nil, nil, nil, Route{Model: "general", Effort: "medium"}, true, true},
		{"unavailable previous uses defaults", nil, coderHigh, []string{"general"}, Route{Model: "general", Effort: "medium"}, true, true},
		{"nothing available", decide("code.build", 1, 1, 0.1), nil, []string{}, Route{}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			available := func(model string) bool { return tt.available == nil || slices.Contains(tt.available, model) }
			route, inherited, ok, pick := SelectRoute(tt.decision, tt.previous, policy, Candidates{Available: available, Overridable: available}, nil)
			assert.Nil(t, pick, "没有价格和榜单分时不做质量/成本选择")
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.want, route)
			}
			assert.Equal(t, tt.wantInherited, inherited)
		})
	}
}

// 档位由判断定下时会改写客户端的推理字段，所以用 overridable 判定候选；
// 沿用上一轮或默认模型时不碰客户端字段，仍按客户端原样的请求用 available 判定。
func TestSelectRouteJudgesOverridingAndFallbackCandidatesSeparately(t *testing.T) {
	policy := Policy{
		TaskModels:             map[string][]string{"code.build": {"coder", "coder-b"}},
		DefaultModels:          []string{"general", "general-b"},
		DifficultyEfforts:      []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:          "medium",
		UpgradeBenefit:         0.5,
		ContinuationTaskChange: 0.5,
	}
	one := 1.0
	zero := 0.0
	leaf := &Decision{TaskType: "code.build", TaskTypeProbability: 0.9, Difficulty: 2, TaskChange: &one, LargerModelBenefit: &zero}
	// 客户端自己带的推理档位 coder 的渠道不支持，但 auto 会把它改写掉，所以 coder 仍可选。
	available := func(model string) bool { return model == "coder-b" || model == "general-b" }
	overridable := func(string) bool { return true }

	candidates := Candidates{Available: available, Overridable: overridable}
	route, inherited, ok, _ := SelectRoute(leaf, nil, policy, candidates, nil)
	assert.True(t, ok)
	assert.False(t, inherited)
	assert.Equal(t, "coder", route.Model, "任务类型是二级类时按 overridable 选模")

	route, inherited, ok, _ = SelectRoute(nil, nil, policy, candidates, nil)
	assert.True(t, ok)
	assert.True(t, inherited)
	assert.Equal(t, "general-b", route.Model, "没有判断时按客户端原样的请求选模，跳过 general")

	// 任务类型是 unknown 但大模型收益够高时档位由 auto 定，同样撇开客户端的推理字段判定候选。
	strong := 0.7
	unknown := &Decision{TaskType: TaskTypeUnknown, TaskTypeProbability: 0.9, Difficulty: 2, TaskChange: &one, LargerModelBenefit: &strong}
	route, inherited, ok, _ = SelectRoute(unknown, &Route{Model: "coder", Effort: "medium"}, policy, candidates, nil)
	assert.True(t, ok)
	assert.False(t, inherited)
	assert.Equal(t, Route{Model: "coder", Effort: "high"}, route, "上一轮的 coder 只在 overridable 下可用，仍沿用它")
	route, _, ok, _ = SelectRoute(unknown, nil, policy, candidates, nil)
	assert.True(t, ok)
	assert.Equal(t, Route{Model: "general", Effort: "high"}, route, "没有上一轮时按 overridable 取默认模型")
}

func pickForTest(models []string, scores map[string]float64, weight float64, available func(string) bool, price func(string) (float64, bool), costs func([]string) (map[string]Cost, *PreviewOutcome), why ...func(string) string) (Pick, bool) {
	var reason func(string) string
	if len(why) > 0 {
		reason = why[0]
	}
	return pickModels(models, scores, DimensionWeights{Quality: 1 - weight, Cost: weight, Temperature: 1}, available, price, costs, nil, reason)
}

// pickEntry 描述质量/成本选择里的一个候选。
type pickEntry struct {
	name     string
	score    float64
	price    float64
	down     bool // 不可用
	unpriced bool // 没有可比较的价格
	unscored bool // 榜单里没有分数
}

// pickLadder 的分数随价格一起降低，三个权重下中选的模型各不相同。
var pickLadder = []pickEntry{
	{name: "top", score: 0.97, price: 20}, {name: "mid", score: 0.95, price: 8},
	{name: "cheap", score: 0.89, price: 2}, {name: "weak", score: 0.70, price: 0.2},
}

func runPick(entries []pickEntry, weight float64) (Pick, bool, []string) {
	byName := map[string]pickEntry{}
	scores := map[string]float64{}
	var models, probed []string
	for _, entry := range entries {
		byName[entry.name] = entry
		models = append(models, entry.name)
		if !entry.unscored {
			scores[entry.name] = entry.score
		}
	}
	pick, ok := pickForTest(models, scores, weight,
		func(name string) bool { probed = append(probed, name); return !byName[name].down },
		func(name string) (float64, bool) { return byName[name].price, !byName[name].unpriced }, nil, nil)
	return pick, ok, probed
}

func TestPickModelsScoresAllDimensionsAndSamples(t *testing.T) {
	models := []string{"busy", "idle"}
	scores := map[string]float64{"busy": 0.9, "idle": 0.9}
	metrics := func([]string) map[string]MetricSample {
		return map[string]MetricSample{"busy": {Failures: 100}, "idle": {Successes: 100}}
	}
	costs := func([]string) (map[string]Cost, *PreviewOutcome) {
		return map[string]Cost{
			"busy": {Price: 1, Capacity: 0.2, CapacityKnown: true},
			"idle": {Price: 1, Capacity: 0.8, CapacityKnown: true},
		}, nil
	}
	weights := DimensionWeights{Quality: 0.4, Cost: 0.2, Capacity: 0.2, Success: 0.2, Temperature: 0.1}
	pick, ok := pickModels(models, scores, weights, func(string) bool { return true },
		func(string) (float64, bool) { return 1, true }, costs, metrics, nil)
	require.True(t, ok)
	require.Len(t, pick.Band, 2)
	assert.Equal(t, "idle", pick.Chosen)
	idle, busy := pick.Band[0], pick.Band[1]
	assert.InDelta(t, 0.8, idle.Capacity, 1e-9)
	assert.InDelta(t, 0.2, busy.Capacity, 1e-9)
	assert.Equal(t, 1.0, idle.Success)
	assert.InDelta(t, 4.0/104, busy.Success, 1e-9)
	assert.Equal(t, int64(100), busy.Samples)
	assert.InDelta(t, 0.8439784598, idle.Utility, 1e-9)
	assert.InDelta(t, 0.5108412610, busy.Utility, 1e-9)
}

func TestAggregateCapacityUsesWeightedLimitsAndBottleneck(t *testing.T) {
	capacity := AggregateCapacity([]CapacitySample{
		{Share: 0.75, RPMLimit: 100, RPMUsed: 25, TPMLimit: 1000, TPMUsed: 100, ConcurrencyLimit: 10, ConcurrencyUsed: 2},
		{Share: 0.25, RPMLimit: 20, RPMUsed: 10, TPMLimit: 200, TPMUsed: 100, ConcurrencyLimit: 4, ConcurrencyUsed: 4},
	})
	assert.InDelta(t, 6.0/8.5, capacity, 1e-9)

	assert.Equal(t, 1.0, AggregateCapacity([]CapacitySample{{Share: 1, RPMUsed: 100}}))
	assert.Equal(t, 0.0, AggregateCapacity([]CapacitySample{{Share: 1, Exhausted: true}}))
}

func TestPickModelsNormalizesFourDimensionWeights(t *testing.T) {
	models := []string{"same-b", "same-a"}
	scores := map[string]float64{"same-a": 0.8, "same-b": 0.8}
	available := func(string) bool { return true }
	price := func(string) (float64, bool) { return 1, true }
	pick, ok := pickModels(models, scores, DimensionWeights{Quality: 0.8, Cost: 0.2, Temperature: 0.25}, available, price, nil, nil, nil)
	require.True(t, ok)
	assert.Equal(t, DimensionWeights{Quality: 0.8, Cost: 0.2}, pick.Dimensions)
	assert.Equal(t, "same-a", pick.Chosen)

	slices.Reverse(models)
	scaled, ok := pickModels(models, scores, DimensionWeights{Quality: 8, Cost: 2, Temperature: 1}, available, price, nil, nil, nil)
	require.True(t, ok)
	assert.Equal(t, pick, scaled)
}

func TestPickModelsUsesCandidateIndependentCostPenalty(t *testing.T) {
	models := []string{"premium", "mid"}
	scores := map[string]float64{"premium": 1, "mid": 0.9828, "budget": 0.70}
	prices := map[string]float64{"premium": 8, "mid": 4, "budget": 0.1}
	available := func(string) bool { return true }
	price := func(model string) (float64, bool) { return prices[model], true }

	for _, tc := range []struct {
		name    string
		weights DimensionWeights
		want    string
	}{
		{"capability", DimensionWeights{Quality: 0.6, Capacity: 0.2, Success: 0.2}, "mid"},
		{"balanced", DimensionWeights{Quality: 0.4, Cost: 0.2, Capacity: 0.2, Success: 0.2}, "mid"},
		{"cost", DimensionWeights{Quality: 0.2, Cost: 0.4, Capacity: 0.2, Success: 0.2}, "budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pick, ok := pickModels(models, scores, tc.weights, available, price, nil, nil, nil)
			require.True(t, ok)
			assert.Equal(t, "mid", pick.Chosen)

			expanded, ok := pickModels(append(slices.Clone(models), "budget"), scores, tc.weights, available, price, nil, nil, nil)
			require.True(t, ok)
			require.Len(t, expanded.Band, 3)
			assert.Equal(t, tc.want, expanded.Chosen)
			for _, scored := range pick.Band {
				assert.Contains(t, expanded.Band, scored)
			}
		})
	}
}

func TestPickModelsNormalizesCost(t *testing.T) {
	models := []string{"free", "cheap", "mid", "dear"}
	scores := map[string]float64{"free": 0.9, "cheap": 0.9, "mid": 0.9, "dear": 0.9}
	prices := map[string]float64{"free": 0, "cheap": 1, "mid": 10, "dear": 100}
	pick, ok := pickModels(models, scores, DimensionWeights{Cost: 1}, func(string) bool { return true }, func(model string) (float64, bool) { return prices[model], true }, nil, nil, nil)
	require.True(t, ok)
	require.Len(t, pick.Band, 4)
	assert.Equal(t, "free", pick.Chosen)
	assert.Zero(t, pick.Band[0].CostPenalty)
	for i, candidate := range pick.Band {
		assert.Equal(t, 1.0, candidate.Capacity)
		assert.Equal(t, 1.0, candidate.Success)
		if i > 0 {
			assert.Greater(t, candidate.CostPenalty, pick.Band[i-1].CostPenalty)
		}
	}
}

func TestPickModelsSkipsOverflowingComparisonCosts(t *testing.T) {
	models := []string{"normal", "overflowed-cost", "overflowed-penalty"}
	scores := map[string]float64{"normal": 0.8, "overflowed-cost": 1, "overflowed-penalty": 1}
	costs := func([]string) (map[string]Cost, *PreviewOutcome) {
		return map[string]Cost{
			"overflowed-cost":    {Price: 2, Multiplier: math.MaxFloat64},
			"overflowed-penalty": {Price: 2, Multiplier: 1e200},
		}, nil
	}
	pick, ok := pickForTest(models, scores, 0.2, func(string) bool { return true },
		func(string) (float64, bool) { return 1, true }, costs)
	require.True(t, ok)
	assert.Equal(t, "normal", pick.Chosen)
	require.Len(t, pick.Band, 1)
	assert.Equal(t, "normal", pick.Band[0].Model)
}

func TestPickModelsKeepsQualityRelevantAboveDefaultCostWeight(t *testing.T) {
	models := []string{"cheap", "strong"}
	scores := map[string]float64{"cheap": 0.7, "strong": 0.95}
	prices := map[string]float64{"cheap": 0.2, "strong": 0.21}
	weights := DimensionWeights{Quality: 0.2, Cost: 0.6, Capacity: 0.1, Success: 0.1}
	pick, ok := pickModels(models, scores, weights, func(string) bool { return true },
		func(model string) (float64, bool) { return prices[model], true }, nil, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "strong", pick.Chosen)
}

func TestPickModelsRejectsUnavailableAndUnscoredModels(t *testing.T) {
	pick, ok, _ := runPick([]pickEntry{{name: "top", score: 0.99, price: 10, down: true},
		{name: "per-call", score: 0.98, price: 1, unpriced: true}, {name: "unscored", unscored: true},
		{name: "usable", score: 0.9, price: 2}}, 0.1)
	require.True(t, ok)
	assert.Equal(t, "usable", pick.Chosen)
	assert.Equal(t, []Skipped{{Model: "top", Score: 0.99, Reason: ReasonUnavailable}, {Model: "per-call", Score: 0.98, Reason: ReasonPerCall}}, pick.skipped)
	_, ok, _ = runPick([]pickEntry{{name: "top", score: 0.99, price: 10, down: true}}, 0.1)
	assert.False(t, ok)
}

func TestPickModelsComparesAllScoredCandidates(t *testing.T) {
	pick, ok, probed := runPick(pickLadder, 0.3)
	require.True(t, ok)
	assert.Len(t, probed, 4)
	assert.Equal(t, pick.Band[0].Model, pick.Chosen)
	for i := 1; i < len(pick.Band); i++ {
		assert.GreaterOrEqual(t, pick.Band[i-1].Utility, pick.Band[i].Utility)
	}
}

// 比较成本等于用户价乘以首次落点的渠道费用倍率。一次估算所有可服务且有榜单分的候选。
func TestPickByQualityCostComparesThePlatformCost(t *testing.T) {
	scores := map[string]float64{"a": 0.95, "b": 0.94, "far": 0.50}
	models := []string{"a", "b", "far"}
	prices := map[string]float64{"a": 10, "b": 3, "far": 0.1}
	all := func(string) bool { return true }
	price := func(model string) (float64, bool) { return prices[model], true }
	var asked [][]string
	var outcome *PreviewOutcome
	pickWith := func(estimates map[string]Cost) Pick {
		pick, ok := pickForTest(models, scores, 0.07, all, price, func(names []string) (map[string]Cost, *PreviewOutcome) {
			asked = append(asked, slices.Clone(names))
			return estimates, outcome
		}, nil)
		require.True(t, ok)
		return pick
	}

	pick, ok := pickForTest(models, scores, 0.07, all, price, nil, nil)
	require.True(t, ok)
	assert.Equal(t, "b", pick.Chosen, "统一效用会让成本惩罚参与能力档选择")

	pick = pickWith(map[string]Cost{
		"a": {Price: 10, Multiplier: 0.2, ChannelID: 7, Channels: 1, Source: CostSourceScheduler},
		"b": {Price: 3, Multiplier: 1, Channels: 2, Source: CostSourceExpected},
	})
	assert.Equal(t, "a", pick.Chosen, "a costs the platform 2, b costs 3")
	assert.Equal(t, [][]string{{"a", "b", "far"}}, asked, "one call for every scored candidate")
	assert.Less(t, pick.Band[0].CostPenalty, pick.Band[1].CostPenalty)
	assert.Nil(t, pick.Preview, "no scheduler, nothing to record")

	// 调度器的预览结果原样记进 Pick；某个模型没用上它的答案时，原因跟着那个候选。
	outcome = &PreviewOutcome{Status: "answered"}
	pick = pickWith(map[string]Cost{
		"a": {Price: 10, Multiplier: 0.2, ChannelID: 7, Channels: 1, Source: CostSourceScheduler},
		"b": {Price: 3, Multiplier: 1, Channels: 2, Source: CostSourceExpected, Preview: "not_cached"},
	})
	assert.Equal(t, &PreviewOutcome{Status: "answered"}, pick.Preview)
	assert.Empty(t, pick.Band[0].Preview)
	assert.Equal(t, "not_cached", pick.Band[1].Preview)
	outcome = nil

	tied := map[string]float64{"a": 0.95, "b": 0.95}
	pick, ok = pickForTest([]string{"a", "b"}, tied, 0, all, price, func([]string) (map[string]Cost, *PreviewOutcome) {
		return map[string]Cost{"a": {Price: 10, Multiplier: 0.2}, "b": {Price: 3, Multiplier: 1}}, nil
	}, nil)
	require.True(t, ok)
	assert.Equal(t, "a", pick.Chosen, "成本强度为零时，相同质量仍优先选成本更低的模型")

	pick = pickWith(map[string]Cost{"a": {Price: 10, Multiplier: 0.5}, "b": {Price: 3, Multiplier: 0.5}})
	assert.Equal(t, "b", pick.Chosen, "相同倍率时质量差距不足以覆盖成本差异")

	pick = pickWith(map[string]Cost{"a": {Price: 10, Multiplier: 0.2}})
	assert.Equal(t, "a", pick.Chosen, "a model without an estimate is compared by its user price")
	assert.Zero(t, pick.Band[1].CostMultiplier)

	pick = pickWith(map[string]Cost{"b": {Excluded: true, Reason: ReasonSchedulerNoCandidate}})
	assert.Equal(t, "a", pick.Chosen, "an excluded model leaves the band")
	assert.Equal(t, []Skipped{{Model: "b", Score: 0.94, Reason: ReasonSchedulerNoCandidate}}, pick.skipped)

	_, ok = pickForTest(models, scores, 0.07, all, price, func([]string) (map[string]Cost, *PreviewOutcome) {
		return map[string]Cost{"a": {Excluded: true}, "b": {Excluded: true}, "far": {Excluded: true}}, nil
	}, nil)
	assert.False(t, ok, "nothing left to compare")
}

// 均衡偏好的质量和成本权重由难度决定：简单任务按成本优先选，难任务按能力优先选，
// 没有可用难度时取中间值；手动配置的质量和成本不起作用，其余偏好不受难度影响。
func TestSelectRouteBalancedWeighsByDifficulty(t *testing.T) {
	policy := Policy{
		TaskModels:        map[string][]string{"code.build": {"top", "mid", "budget"}},
		TaskScores:        map[string]map[string]float64{"code.build": {"top": 1, "mid": 0.85, "budget": 0.7}, "general": {"top": 1, "mid": 0.85, "budget": 0.7}},
		DefaultModels:     []string{"top", "mid", "budget"},
		DifficultyEfforts: []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:     "medium",
		UpgradeBenefit:    0.5,
		Preference:        PreferenceBalanced,
		// 质量和成本故意配成能力优先，验证均衡不再使用它们。
		DimensionWeights: DimensionWeights{Quality: 1, Capacity: 0.2, Success: 0.2},
		Adaptive:         true,
	}
	prices := map[string]float64{"top": 8, "mid": 2, "budget": 0.1}
	all := func(string) bool { return true }
	priced := Candidates{Available: all, Overridable: all, Price: func(model string) (float64, bool) { return prices[model], true }}
	one := 1.0
	decide := func(score float64) *Decision {
		return &Decision{TaskType: "code.build", TaskTypeProbability: 0.9, Difficulty: int(score) + 1, DifficultyScore: &score, TaskChange: &one}
	}

	for _, tc := range []struct {
		name     string
		decision *Decision
		want     string
		need     float64
		fromJev  bool
		cost     float64
	}{
		{"the easiest task picks like cost first", decide(0), "budget", 0, true, 0.4},
		{"a middling task picks like the former balanced", decide(1.5), "mid", 0.5, true, 0.2},
		{"the hardest task picks like capability first", decide(3), "top", 1, true, 0},
		{"the rung is used when jev gave no score", &Decision{TaskType: "code.build", Difficulty: 4, TaskChange: &one}, "top", 1, true, 0},
		{"no decision takes the neutral need", nil, "mid", NeutralNeed, false, 0.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route, _, ok, pick := SelectRoute(tc.decision, nil, policy, priced, nil)
			require.True(t, ok)
			assert.Equal(t, tc.want, route.Model)
			require.NotNil(t, pick)
			require.NotNil(t, pick.Need)
			assert.InDelta(t, tc.need, *pick.Need, 1e-9)
			assert.Equal(t, tc.fromJev, pick.NeedFromDifficulty)
			assert.InDelta(t, tc.cost, pick.Dimensions.Cost, 1e-9)
		})
	}

	t.Run("fixed preferences ignore difficulty", func(t *testing.T) {
		cost := policy
		cost.Preference, cost.Adaptive = PreferenceCost, false
		cost.DimensionWeights = DimensionWeights{Quality: 0.2, Cost: 0.4, Capacity: 0.2, Success: 0.2}
		route, _, ok, pick := SelectRoute(decide(3), nil, cost, priced, nil)
		require.True(t, ok)
		assert.Equal(t, "budget", route.Model)
		require.NotNil(t, pick)
		assert.Nil(t, pick.Need)
	})
}

// 按榜单选模型时在同一榜单的候选里一次比较质量、成本、容量和成功率；沿用上一轮模型、
// 默认模型以外的退路和没有价格时都不做这个比较。
func TestSelectRoutePicksByQualityAndPrice(t *testing.T) {
	policy := Policy{
		TaskModels: map[string][]string{"code.build": {"top", "mid", "cheap", "weak"}, "code.ops": {"top", "legacy"}, "code": {"top", "mid"}},
		TaskScores: map[string]map[string]float64{
			"code.build": {"top": 0.97, "mid": 0.95, "cheap": 0.89, "weak": 0.70},
			"code.ops":   {"top": 0.9},
			"code":       {"top": 0.97, "mid": 0.95},
			"general":    {"general": 0.9, "general-cheap": 0.85},
		},
		DefaultModels:          []string{"general", "general-cheap"},
		DifficultyEfforts:      []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:          "medium",
		UpgradeBenefit:         0.5,
		ContinuationTaskChange: 0.5,
		Preference:             PreferenceBalanced,
		DimensionWeights:       DimensionWeights{Quality: 0.93, Cost: 0.07},
	}
	prices := map[string]float64{"top": 20, "mid": 8, "cheap": 2, "weak": 0.2, "general": 1, "general-cheap": 0.2, "legacy": 3}
	all := func(string) bool { return true }
	priced := Candidates{Available: all, Overridable: all, Price: func(model string) (float64, bool) {
		price, ok := prices[model]
		return price, ok
	}}
	zero := 0.0
	decide := func(taskType string, taskChange float64) *Decision {
		return &Decision{TaskType: taskType, TaskTypeProbability: 0.9, Difficulty: 2, TaskChange: &taskChange, LargerModelBenefit: &zero}
	}

	t.Run("a decision with a leaf task type picks by quality and price", func(t *testing.T) {
		route, inherited, ok, pick := SelectRoute(decide("code.build", 1), nil, policy, priced, nil)
		assert.True(t, ok)
		assert.False(t, inherited)
		assert.Equal(t, Route{Profile: "code.build", Model: "cheap", Effort: "medium"}, route)
		require.NotNil(t, pick)
		assert.Equal(t, "cheap", pick.Chosen)
		assert.Equal(t, 0.07, pick.Dimensions.Cost)
	})
	t.Run("the weight decides how far down the ladder the pick goes", func(t *testing.T) {
		for weight, want := range map[float64]string{0: "mid", 0.07: "cheap", 0.3: "weak"} {
			withWeight := policy
			withWeight.DimensionWeights = DimensionWeights{Quality: 1 - weight, Cost: weight}
			route, _, ok, _ := SelectRoute(decide("code.build", 1), nil, withWeight, priced, nil)
			assert.True(t, ok)
			assert.Equal(t, want, route.Model, "weight %v", weight)
		}
	})
	t.Run("a task type without a list is picked from its class the same way", func(t *testing.T) {
		route, inherited, ok, pick := SelectRoute(decide("code.frontend", 1), nil, policy, priced, nil)
		assert.True(t, ok)
		assert.False(t, inherited)
		assert.Equal(t, Route{Profile: "code", Model: "mid", Effort: "medium"}, route)
		require.NotNil(t, pick)
		assert.Equal(t, "mid", pick.Chosen)
	})
	t.Run("costs are asked with the step's effort and whether the client's reasoning is set aside", func(t *testing.T) {
		type ask struct {
			effort                string
			ignoreClientReasoning bool
		}
		var asked []ask
		withCosts := priced
		withCosts.Costs = func(_ []string, effort string, ignoreClientReasoning bool) (map[string]Cost, *PreviewOutcome) {
			asked = append(asked, ask{effort, ignoreClientReasoning})
			return nil, nil
		}
		hard := decide("code.build", 1)
		hard.Difficulty = 4
		_, _, ok, _ := SelectRoute(hard, nil, policy, withCosts, nil)
		require.True(t, ok)
		_, _, ok, _ = SelectRoute(nil, nil, policy, withCosts, nil)
		require.True(t, ok)
		assert.Equal(t, []ask{{"xhigh", true}, {"medium", false}}, asked, "a ranking chosen by a valid decision, then the default models without a decision")
	})
	t.Run("without prices the first available model in list order wins", func(t *testing.T) {
		route, _, ok, pick := SelectRoute(decide("code.build", 1), nil, policy, Candidates{Available: all, Overridable: all}, nil)
		assert.True(t, ok)
		assert.Equal(t, "top", route.Model)
		assert.Nil(t, pick)
	})
	t.Run("a continuation keeps the previous model whatever it costs", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "medium"}
		route, _, ok, pick := SelectRoute(decide("code.build", 0.1), previous, policy, priced, nil)
		assert.True(t, ok)
		assert.Equal(t, "top", route.Model)
		assert.Nil(t, pick)
	})
	t.Run("no decision and no previous route compares the default models", func(t *testing.T) {
		route, inherited, ok, pick := SelectRoute(nil, nil, policy, priced, nil)
		assert.True(t, ok)
		assert.True(t, inherited)
		assert.Equal(t, Route{Model: "general", Effort: "medium"}, route)
		require.NotNil(t, pick)

		capability := policy
		capability.DimensionWeights = DimensionWeights{Quality: 1}
		route, _, ok, _ = SelectRoute(nil, nil, capability, priced, nil)
		assert.True(t, ok)
		assert.Equal(t, "general", route.Model, "能力优先时兜底也取分数最高的")
	})
	t.Run("a leaf task type with nothing servable in its rankings falls to the default models", func(t *testing.T) {
		candidates := priced
		candidates.Overridable = func(model string) bool { return strings.HasPrefix(model, "general") }
		route, _, ok, pick := SelectRoute(decide("code.build", 1), nil, policy, candidates, nil)
		assert.True(t, ok)
		assert.Equal(t, Route{Model: "general", Effort: "medium"}, route)
		require.NotNil(t, pick)
	})
	t.Run("unscored models are a fallback in list order", func(t *testing.T) {
		candidates := priced
		candidates.Overridable = func(model string) bool { return model == "legacy" }
		route, _, ok, pick := SelectRoute(decide("code.ops", 1), nil, policy, candidates, nil)
		assert.True(t, ok)
		assert.Equal(t, "legacy", route.Model)
		assert.Nil(t, pick)
	})
}

func TestPolicyAnyModelAvailable(t *testing.T) {
	policy := Policy{
		TaskModels:    map[string][]string{"coding": {"coder"}, "review": {"gone"}},
		DefaultModels: []string{"general"},
	}
	tests := []struct {
		name      string
		available []string
		want      bool
	}{
		{"a default model is enough", []string{"general"}, true},
		{"a task model is enough", []string{"coder-high"}, true},
		{"only models outside both lists", []string{"other"}, false},
		{"nothing available", []string{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, policy.AnyModelAvailable(func(model string) bool { return slices.Contains(tt.available, model) }))
		})
	}
}

func TestRouteFallback(t *testing.T) {
	assert.Equal(t, "leaf", Route{Profile: "code.build"}.Fallback())
	assert.Equal(t, "class", Route{Profile: "code"}.Fallback())
	assert.Equal(t, "default", Route{}.Fallback())
}

// 选路留痕记下一次路由是怎么来的：判断合不合法、按哪些榜单、是否续用上一轮、难度怎么变成档位、
// 每个榜单试了什么，以及为什么没选更靠前的模型。
func TestSelectRouteTrace(t *testing.T) {
	policy := Policy{
		TaskModels: map[string][]string{"code.build": {"top", "mid", "cheap"}, "code": {"top", "mid"}, "general.chat": {"chat"}},
		TaskScores: map[string]map[string]float64{
			"code.build": {"top": 0.97, "mid": 0.95, "cheap": 0.89},
			"code":       {"top": 0.97, "mid": 0.95},
		},
		DefaultModels:          []string{"general"},
		DifficultyEfforts:      []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:          "medium",
		UpgradeBenefit:         0.5,
		ContinuationTaskChange: 0.5,
		Preference:             PreferenceBalanced,
		DimensionWeights:       DimensionWeights{Quality: 0.93, Cost: 0.07},
	}
	prices := map[string]float64{"top": 20, "mid": 8, "cheap": 2, "general": 1}
	all := func(string) bool { return true }
	candidates := Candidates{Available: all, Overridable: all, Price: func(model string) (float64, bool) {
		price, ok := prices[model]
		return price, ok
	}}
	ptr := func(v float64) *float64 { return &v }
	decide := func(taskType string, difficulty int, taskChange, benefit float64) *Decision {
		return &Decision{TaskType: taskType, TaskTypeProbability: 0.9, Difficulty: difficulty, TaskChange: ptr(taskChange), LargerModelBenefit: ptr(benefit)}
	}
	run := func(decision *Decision, previous *Route, with Candidates) (Route, *Trace) {
		trace := &Trace{}
		route, _, _, _ := SelectRoute(decision, previous, policy, with, trace)
		return route, trace
	}

	t.Run("a leaf task type records the comparison and how difficulty became the effort", func(t *testing.T) {
		route, trace := run(decide("code.build", 2, 1, 0.1), nil, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "cheap", Effort: "medium"}, route)
		assert.Equal(t, &Validity{Profiles: []string{"code.build", "code"}}, trace.Validity)
		assert.Equal(t, &EffortTrace{Difficulty: 2, Base: "medium", Benefit: ptr(0.1), Threshold: 0.5}, trace.Effort)
		assert.Nil(t, trace.Continuation, "没有上一轮路由就谈不上续用")
		assert.Equal(t, []LadderStep{{Profile: "code.build", Models: 3, Outcome: OutcomeCompared, Model: "cheap"}}, trace.Ladder)
	})

	t.Run("a task type without its own ranking falls to its class", func(t *testing.T) {
		route, trace := run(decide("code.frontend", 1, 1, 0.1), nil, candidates)
		assert.Equal(t, Route{Profile: "code", Model: "mid", Effort: "low"}, route)
		assert.Equal(t, []string{"code.frontend", "code"}, trace.Validity.Profiles)
		assert.Equal(t, []LadderStep{
			{Profile: "code.frontend", Outcome: OutcomeNone},
			{Profile: "code", Models: 2, Outcome: OutcomeCompared, Model: "mid"},
		}, trace.Ladder)
	})

	t.Run("an unknown task type and a request without a valid decision both take the defaults", func(t *testing.T) {
		tests := []struct {
			name     string
			decision *Decision
			want     Validity
		}{
			{"unknown is a valid answer with no ranking",
				&Decision{TaskType: TaskTypeUnknown, TaskTypeProbability: 0.4, Difficulty: 1, TaskTypeProbabilities: map[string]float64{"code.build": 0.3, "general.chat": 0.3, TaskTypeUnknown: 0.4}},
				Validity{}},
			{"jev failed", nil, Validity{Error: DecisionMissing}},
			{"no difficulty", &Decision{TaskType: "code.build", TaskTypeProbability: 0.9}, Validity{Error: DecisionNoDifficulty}},
			{"no task type", &Decision{Difficulty: 2}, Validity{Error: DecisionInvalidTaskType}},
			{"a task type jev was not offered", &Decision{TaskType: "code.mobile", TaskTypeProbability: 0.9, Difficulty: 2}, Validity{Error: DecisionInvalidTaskType}},
		}
		for _, tt := range tests {
			route, trace := run(tt.decision, nil, candidates)
			assert.Equal(t, &tt.want, trace.Validity, tt.name)
			assert.Equal(t, Route{Model: "general", Effort: "medium"}, route, tt.name)
			assert.Equal(t, []LadderStep{{Profile: DefaultProfile, Models: 1, Outcome: OutcomeFirstAvailable, Model: "general"}}, trace.Ladder, tt.name)
			assert.Nil(t, trace.Effort, "这些请求的档位不由难度定: %s", tt.name)
		}
	})

	t.Run("a continuation lowers effort when the current task is easier and a larger model has little benefit", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "xhigh"}
		route, trace := run(decide("general.chat", 1, 0.1, 0.1), previous, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "top", Effort: "low"}, route)
		assert.Equal(t, &Continuation{Previous: "top", TaskChange: 0.1, Threshold: 0.5, Continued: true}, trace.Continuation)
		assert.False(t, trace.Effort.Floored)
		assert.Empty(t, trace.Ladder, "续用不再排榜单")
	})

	t.Run("a continuation without a benefit judgment keeps the previous effort", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "xhigh"}
		decision := &Decision{TaskType: "general.chat", Difficulty: 1, TaskChange: ptr(0.1)}
		route, trace := run(decision, previous, candidates)
		assert.Equal(t, *previous, route)
		assert.Equal(t, &EffortTrace{Difficulty: 1, Base: "low", Threshold: 0.5, Floored: true}, trace.Effort)
	})

	t.Run("a new task leaves the previous model and ranks again", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "xhigh"}
		route, trace := run(decide("general.chat", 1, 0.9, 0.1), previous, candidates)
		assert.Equal(t, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, route)
		assert.Equal(t, &Continuation{Previous: "top", TaskChange: 0.9, Threshold: 0.5}, trace.Continuation)
		assert.False(t, trace.Effort.Floored)
		assert.Equal(t, []LadderStep{{Profile: "general.chat", Models: 1, Outcome: OutcomeFirstAvailable, Model: "chat"}}, trace.Ladder)
	})

	t.Run("a large benefit from a bigger model raises the effort one rung", func(t *testing.T) {
		route, trace := run(decide("code.build", 2, 1, 0.7), nil, candidates)
		assert.Equal(t, "high", route.Effort)
		assert.Equal(t, &EffortTrace{Difficulty: 2, Base: "medium", Benefit: ptr(0.7), Threshold: 0.5, Upgraded: true}, trace.Effort)
	})

	t.Run("a difficulty one rung above the previous effort counts as the raise", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "medium"}
		route, trace := run(decide("code.build", 3, 0.1, 0.7), previous, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "top", Effort: "high"}, route, "比上一轮高一档的 high 由难度给出，收益不再加到 xhigh")
		assert.Equal(t, &EffortTrace{Difficulty: 3, Base: "high", Benefit: ptr(0.7), Threshold: 0.5}, trace.Effort)
	})

	t.Run("an unknown task with a large benefit keeps the previous model one rung above its effort", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "high"}
		route, trace := run(decide(TaskTypeUnknown, 1, 0.1, 0.7), previous, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "top", Effort: "xhigh"}, route)
		assert.Equal(t, &Validity{}, trace.Validity, "unknown 是合法的判断，只是没有榜单")
		assert.Equal(t, &EffortTrace{Difficulty: 1, Base: "low", Benefit: ptr(0.7), Threshold: 0.5, Floored: true, Upgraded: true}, trace.Effort)
		assert.Nil(t, trace.Continuation, "任务类型是 unknown 时不看任务变化")
		assert.Equal(t, []LadderStep{{Profile: "code.build", Outcome: OutcomeInherited, Model: "top"}}, trace.Ladder)
	})

	t.Run("without a decision the previous route is kept", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "high"}
		route, trace := run(nil, previous, candidates)
		assert.Equal(t, *previous, route)
		assert.Equal(t, DecisionMissing, trace.Validity.Error)
		assert.Equal(t, []LadderStep{{Profile: "code.build", Outcome: OutcomeInherited, Model: "top"}}, trace.Ladder)
	})

	t.Run("a previous model that can no longer serve is not continued, and the skip says why", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "medium"}
		withoutTop := candidates
		withoutTop.Overridable = func(model string) bool { return !strings.HasPrefix(model, "top") }
		withoutTop.Why = func(model string) string {
			if strings.HasPrefix(model, "top") {
				return ReasonCapability
			}
			return ""
		}
		route, trace := run(decide("code.build", 2, 0.1, 0.1), previous, withoutTop)
		assert.Equal(t, Route{Profile: "code.build", Model: "cheap", Effort: "medium"}, route,
			"榜单第一的 top 不能服务，跳过它后在全部可服务模型中比较")
		assert.Equal(t, &Continuation{Previous: "top", TaskChange: 0.1, Threshold: 0.5}, trace.Continuation)
		require.Len(t, trace.Ladder, 1)
		assert.Equal(t, []Skipped{{Model: "top", Score: 0.97, Reason: ReasonCapability}}, trace.Ladder[0].Skipped)
	})

	t.Run("a model billed per call is skipped because it cannot be compared", func(t *testing.T) {
		perCall := candidates
		perCall.Price = func(model string) (float64, bool) { return prices[model], model != "top" }
		route, trace := run(decide("code.build", 2, 1, 0.1), nil, perCall)
		assert.Equal(t, "cheap", route.Model)
		require.Len(t, trace.Ladder, 1)
		assert.Equal(t, []Skipped{{Model: "top", Score: 0.97, Reason: ReasonPerCall}}, trace.Ladder[0].Skipped)
	})

	t.Run("a model only the client's own reasoning fields rule out says so", func(t *testing.T) {
		defaults := policy
		defaults.DefaultModels = []string{"g-top", "g-cheap"}
		defaults.TaskScores = map[string]map[string]float64{DefaultProfile: {"g-top": 0.9, "g-cheap": 0.88}}
		clientPrices := map[string]float64{"g-top": 10, "g-cheap": 1}
		clientView := Candidates{
			Available:   func(model string) bool { return !strings.HasPrefix(model, "g-top") },
			Overridable: all,
			Price:       func(model string) (float64, bool) { return clientPrices[model], true },
			Why:         func(string) string { return "" },
		}
		trace := &Trace{}
		route, _, ok, _ := SelectRoute(nil, nil, defaults, clientView, trace)
		require.True(t, ok)
		assert.Equal(t, "g-cheap", route.Model)
		require.Len(t, trace.Ladder, 1)
		assert.Equal(t, []Skipped{{Model: "g-top", Score: 0.9, Reason: ReasonClientReasoning}}, trace.Ladder[0].Skipped)
	})

	t.Run("a failed route still records what every ranking tried", func(t *testing.T) {
		none := Candidates{Available: func(string) bool { return false }, Overridable: func(string) bool { return false }, Price: candidates.Price}
		trace := &Trace{}
		_, _, ok, _ := SelectRoute(decide("code.build", 1, 1, 0.1), nil, policy, none, trace)
		assert.False(t, ok)
		require.Len(t, trace.Ladder, 3)
		assert.Equal(t, []string{"code.build", "code", DefaultProfile}, []string{trace.Ladder[0].Profile, trace.Ladder[1].Profile, trace.Ladder[2].Profile})
		for _, step := range trace.Ladder {
			assert.Equal(t, OutcomeNone, step.Outcome, step.Profile)
		}
		assert.Equal(t, []Skipped{
			{Model: "top", Score: 0.97, Reason: ReasonUnavailable},
			{Model: "mid", Score: 0.95, Reason: ReasonUnavailable},
			{Model: "cheap", Score: 0.89, Reason: ReasonUnavailable},
		}, trace.Ladder[0].Skipped, "没有 Why 时原因未知，榜单里最高分的几个都记下")
	})
}

// 一个榜单里不能服务的模型可能很多，留痕只记最靠前的几个，其余只计数。
func TestPickModelsCapsRecordedSkips(t *testing.T) {
	var models []string
	scores := map[string]float64{}
	for i := range maxSkipped + 2 {
		name := "m" + strconv.Itoa(i)
		models = append(models, name)
		scores[name] = 0.99 - float64(i)*0.001
	}
	models = append(models, "last")
	scores["last"] = 0.97
	pick, ok := pickForTest(models, scores, 0.07,
		func(name string) bool { return name == "last" },
		func(string) (float64, bool) { return 1, true }, nil,
		func(string) string { return ReasonNoChannel })
	require.True(t, ok)
	assert.Equal(t, "last", pick.Chosen)
	require.Len(t, pick.skipped, maxSkipped)
	assert.Equal(t, Skipped{Model: "m0", Score: 0.99, Reason: ReasonNoChannel}, pick.skipped[0])
	assert.Equal(t, 2, pick.skippedMore)
}

// 没有一个模型能服务时，按原因汇总所有榜单里的模型：先默认模型，再按榜单名排序的各任务榜单，
// 同一个模型只算一次；原因按个数从多到少排，每个原因最多列五个例子。
func TestExplainUnroutable(t *testing.T) {
	policy := Policy{
		DefaultModels: []string{"d1", "d2"},
		TaskModels: map[string][]string{
			"general.chat": {"g1", "g2", "g3", "d1"},
			"code.build":   {"c1", "c2", "c3", "d2"},
		},
	}
	reasons := map[string]string{
		"d1": ReasonGroupDisabled, "d2": ReasonGroupDisabled, "c1": ReasonGroupDisabled,
		"c2": ReasonGroupDisabled, "c3": ReasonGroupDisabled, "g1": ReasonGroupDisabled,
		"g2": ReasonNoChannel, "g3": ReasonCapability,
	}

	got := policy.ExplainUnroutable(func(model string) string { return reasons[model] })
	assert.Equal(t, &Unroutable{Reasons: []ReasonCount{
		{Reason: ReasonGroupDisabled, Count: 6, Examples: []string{"d1", "d2", "c1", "c2", "c3"}},
		{Reason: ReasonCapability, Count: 1, Examples: []string{"g3"}},
		{Reason: ReasonNoChannel, Count: 1, Examples: []string{"g2"}},
	}}, got)

	got = policy.ExplainUnroutable(nil)
	assert.Equal(t, &Unroutable{Reasons: []ReasonCount{
		{Reason: ReasonUnavailable, Count: 8, Examples: []string{"d1", "d2", "c1", "c2", "c3"}},
	}}, got, "没有办法问原因时全归为未知")
}

func TestResolveVariant(t *testing.T) {
	tests := []struct {
		name      string
		effort    string
		available []string
		want      string
		wantOK    bool
	}{
		{"exact variant", "high", []string{"m", "m-high"}, "m-high", true},
		{"bare model is the provider default effort", "high", []string{"m", "m-low"}, "m", true},
		{"nearest variant", "xhigh", []string{"m-high", "m-low"}, "m-high", true},
		{"tie prefers stronger", "medium", []string{"m-low", "m-high"}, "m-high", true},
		{"walks several rungs", "none", []string{"m-max"}, "m-max", true},
		{"unknown effort searches from medium", "", []string{"m-low", "m-xhigh"}, "m-low", true},
		{"unknown effort finds a model listed only as medium", "", []string{"m-medium"}, "m-medium", true},
		{"unknown effort prefers medium over its neighbours", "", []string{"m-low", "m-medium", "m-high"}, "m-medium", true},
		{"other models do not count", "high", []string{"other-high"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, ok := ResolveVariant("m", tt.effort, func(name string) bool { return slices.Contains(tt.available, name) })
			assert.Equal(t, tt.want, model)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestMemoryMetricsWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	metrics := NewMemoryMetricsClock(5*time.Minute, func() time.Time { return now })
	metrics.Finish("a", true)
	now = now.Add(2 * time.Minute)
	metrics.Finish("a", false)
	assert.Equal(t, map[string]MetricSample{"a": {Successes: 1, Failures: 1}}, metrics.Snapshot([]string{"a", "b"}))
	now = now.Add(5 * time.Minute)
	assert.Empty(t, metrics.Snapshot([]string{"a"}))
}
