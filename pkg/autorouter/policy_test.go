package autorouter

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

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
		MinTaskTypeProbability: 0.5,
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
		{"split task types trust their class", spread(map[string]float64{"code.build": 0.35, "code.ops": 0.3, "general.chat": 0.35}), nil, nil, Route{Profile: "code", Model: "code-class", Effort: "low"}, false, true},
		{"no trusted class inherits", spread(map[string]float64{"code.build": 0.4, "general.chat": 0.4, "unknown": 0.2}), coderHigh, nil, *coderHigh, true, true},
		{"continuation keeps model and effort", decide("general.chat", 1, 0.1, 0.1), coderHigh, nil, *coderHigh, false, true},
		{"continuation may raise effort", decide("code.ops", 4, 0.1, 0.1), coderHigh, nil, Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, false, true},
		{"new task switches model", decide("general.chat", 1, 0.9, 0.1), coderHigh, nil, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"continuation leaves an unavailable model", decide("general.chat", 1, 0.1, 0.1), coderHigh, []string{"chat"}, Route{Profile: "general.chat", Model: "chat", Effort: "low"}, false, true},
		{"unknown inherits previous", decide(TaskTypeUnknown, 1, 0.1, 0.1), coderHigh, nil, *coderHigh, true, true},
		{"low probability inherits previous", &Decision{TaskType: "general.chat", TaskTypeProbability: 0.3, Difficulty: 1}, coderHigh, nil, *coderHigh, true, true},
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

// 可信的判断自己定档位、会改写客户端的推理字段，所以用 overridable 判定候选；
// 兜底路径不碰客户端字段，仍按客户端原样的请求用 available 判定。
func TestSelectRouteJudgesTrustedAndFallbackCandidatesSeparately(t *testing.T) {
	policy := Policy{
		TaskModels:             map[string][]string{"code.build": {"coder", "coder-b"}},
		DefaultModels:          []string{"general", "general-b"},
		DifficultyEfforts:      []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:          "medium",
		UpgradeBenefit:         0.5,
		MinTaskTypeProbability: 0.5,
		ContinuationTaskChange: 0.5,
	}
	one := 1.0
	zero := 0.0
	trusted := &Decision{TaskType: "code.build", TaskTypeProbability: 0.9, Difficulty: 2, TaskChange: &one, LargerModelBenefit: &zero}
	// 客户端自己带的推理档位 coder 的渠道不支持，但 auto 会把它改写掉，所以 coder 仍可选。
	available := func(model string) bool { return model == "coder-b" || model == "general-b" }
	overridable := func(string) bool { return true }

	candidates := Candidates{Available: available, Overridable: overridable}
	route, inherited, ok, _ := SelectRoute(trusted, nil, policy, candidates, nil)
	assert.True(t, ok)
	assert.False(t, inherited)
	assert.Equal(t, "coder", route.Model, "可信判断按 overridable 选模")

	route, inherited, ok, _ = SelectRoute(nil, nil, policy, candidates, nil)
	assert.True(t, ok)
	assert.True(t, inherited)
	assert.Equal(t, "general-b", route.Model, "兜底按客户端原样的请求选模，跳过 general")
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
	pick, ok := pickByQualityCost(models, scores, weight,
		func(name string) bool { probed = append(probed, name); return !byName[name].down },
		func(name string) (float64, bool) { return byName[name].price, !byName[name].unpriced }, nil)
	return pick, ok, probed
}

func TestPickByQualityCost(t *testing.T) {
	// 榜单分和 OpenRouter 混合价取自旧分类内嵌排名里的 debugging / coding 两个任务（2026-10-22 版），数值留作固定用例。
	debugging := []pickEntry{{name: "opus", score: 0.986, price: 8}, {name: "fable", score: 0.969, price: 20},
		{name: "sonnet", score: 0.948, price: 4}, {name: "astra", score: 0.946, price: 20}}
	coding := []pickEntry{{name: "opus", score: 0.989, price: 8}, {name: "fable", score: 0.950, price: 20},
		{name: "astra", score: 0.944, price: 20}, {name: "sonnet", score: 0.928, price: 4}}
	tests := []struct {
		name    string
		entries []pickEntry
		weight  float64
		want    string
		wantOK  bool
	}{
		{"weight zero: the best score wins whatever it costs", pickLadder, 0, "top", true},
		{"a moderate weight trades a little score for a much lower price", pickLadder, 0.07, "mid", true},
		{"a large weight reaches further down the ladder", pickLadder, 0.3, "cheap", true},
		{"a cheaper model within the weight wins", debugging, 0.1, "sonnet", true},
		{"a clear leader stays", coding, 0.07, "opus", true},
		{"the same leader holds at a larger weight", coding, 0.1, "opus", true},
		{"with only the two left the leader pays the full price penalty", []pickEntry{coding[0], coding[3]}, 0.07, "sonnet", true},
		{"equal scores: the cheaper one wins even at weight zero",
			[]pickEntry{{name: "a", score: 0.95, price: 10}, {name: "b", score: 0.95, price: 2}}, 0, "b", true},
		{"a score gap beyond the weight is not traded for price",
			[]pickEntry{{name: "a", score: 0.95, price: 10}, {name: "b", score: 0.90, price: 2}}, 0.03, "a", true},
		{"an unavailable top: the best servable model becomes the reference",
			[]pickEntry{{name: "a", score: 0.99, price: 10, down: true}, {name: "b", score: 0.95, price: 8}, {name: "c", score: 0.90, price: 2}}, 0.07, "c", true},
		{"unpriced models are skipped like unavailable ones",
			[]pickEntry{{name: "a", score: 0.99, price: 10, unpriced: true}, {name: "b", score: 0.95, price: 8}, {name: "c", score: 0.90, price: 2}}, 0.07, "c", true},
		{"models without a score do not take part",
			[]pickEntry{{name: "x", price: 0.1, unscored: true}, {name: "a", score: 0.95, price: 8}, {name: "b", score: 0.94, price: 4}}, 0.07, "b", true},
		{"a free model gets no bonus beyond the cheapest priced one",
			[]pickEntry{{name: "a", score: 0.95, price: 8}, {name: "b", score: 0.94, price: 4}, {name: "c", score: 0.93, price: 0}}, 0.07, "b", true},
		{"a free model wins a tie on the cheapest price by being cheaper",
			[]pickEntry{{name: "a", score: 0.95, price: 8}, {name: "b", score: 0.94, price: 4}, {name: "c", score: 0.94, price: 0}}, 0.07, "c", true},
		{"all free: no price signal, the best score wins",
			[]pickEntry{{name: "a", score: 0.95}, {name: "b", score: 0.94}}, 0.07, "a", true},
		{"nothing servable", []pickEntry{{name: "a", score: 0.9, price: 1, down: true}}, 0.07, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pick, ok, _ := runPick(tt.entries, tt.weight)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, pick.Chosen)
		})
	}
}

func TestPickByQualityCostBandAndProbing(t *testing.T) {
	// 权重 0.3 时四个模型都在候选带里；价格按带内对数位置打折，
	// 最便宜的（0.2 美元）折 0，最贵的（20 美元）折满权重，其余按 ln(价格/0.2)/ln(100) 插值。
	pick, ok, _ := runPick(pickLadder, 0.3)
	require.True(t, ok)
	require.Len(t, pick.Band, 4)
	assert.Equal(t, 0.3, pick.Weight)
	assert.Equal(t, "cheap", pick.Chosen)
	wantDiscount := map[string]float64{"top": 0.3, "mid": 0.3 * math.Log(40) / math.Log(100), "cheap": 0.15, "weak": 0}
	for _, candidate := range pick.Band {
		assert.InDelta(t, wantDiscount[candidate.Model], candidate.Discount, 1e-9, candidate.Model)
		assert.InDelta(t, candidate.Score-candidate.Discount, candidate.Utility, 1e-9, candidate.Model)
	}

	// 权重收窄后候选带只剩分数接近最高分的模型。
	pick, ok, _ = runPick(pickLadder, 0.07)
	require.True(t, ok)
	require.Len(t, pick.Band, 2)
	assert.Equal(t, []string{"top", "mid"}, []string{pick.Band[0].Model, pick.Band[1].Model})

	// 掉出候选带的模型不再探测可用性（没有内存缓存时每次探测都是一次查询）。
	_, _, probed := runPick([]pickEntry{{name: "a", score: 0.99, price: 5}, {name: "b", score: 0.97, price: 3},
		{name: "c", score: 0.50, price: 1}, {name: "d", score: 0.40, price: 1}}, 0.07)
	assert.Equal(t, []string{"a", "b"}, probed)
}

// 可信的判断在同一任务类型的候选里一次比较质量和价格；沿用上一轮模型、
// 兜底和没有价格时都不做这个比较。
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
		MinTaskTypeProbability: 0.5,
		ContinuationTaskChange: 0.5,
		Preference:             PreferenceBalanced,
		CostWeight:             0.07,
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

	t.Run("a trusted decision picks by quality and price", func(t *testing.T) {
		route, inherited, ok, pick := SelectRoute(decide("code.build", 1), nil, policy, priced, nil)
		assert.True(t, ok)
		assert.False(t, inherited)
		assert.Equal(t, Route{Profile: "code.build", Model: "mid", Effort: "medium"}, route)
		require.NotNil(t, pick)
		assert.Equal(t, "mid", pick.Chosen)
		assert.Equal(t, 0.07, pick.Weight)
	})
	t.Run("the weight decides how far down the ladder the pick goes", func(t *testing.T) {
		for weight, want := range map[float64]string{0: "top", 0.07: "mid", 0.3: "cheap"} {
			withWeight := policy
			withWeight.CostWeight = weight
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
	t.Run("an untrusted decision with no previous route compares the default models", func(t *testing.T) {
		route, inherited, ok, pick := SelectRoute(nil, nil, policy, priced, nil)
		assert.True(t, ok)
		assert.True(t, inherited)
		assert.Equal(t, Route{Model: "general-cheap", Effort: "medium"}, route)
		require.NotNil(t, pick)

		capability := policy
		capability.CostWeight = 0
		route, _, ok, _ = SelectRoute(nil, nil, capability, priced, nil)
		assert.True(t, ok)
		assert.Equal(t, "general", route.Model, "能力优先时兜底也取分数最高的")
	})
	t.Run("a trusted decision with nothing servable in its rankings falls to the default models", func(t *testing.T) {
		candidates := priced
		candidates.Overridable = func(model string) bool { return strings.HasPrefix(model, "general") }
		route, _, ok, pick := SelectRoute(decide("code.build", 1), nil, policy, candidates, nil)
		assert.True(t, ok)
		assert.Equal(t, Route{Model: "general-cheap", Effort: "medium"}, route)
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

	adaptive := policy
	adaptive.Preference = PreferenceAdaptive
	adaptive.Tiers = TierWeights{Capability: 0, Balanced: 0.07, Cost: 0.3}
	withMode := func(d *Decision, capability, balanced, cost float64) *Decision {
		d.ModeProbabilities = map[string]float64{PreferenceCapability: capability, PreferenceBalanced: balanced, PreferenceCost: cost}
		return d
	}

	t.Run("an adaptive request picks by the weight its mode answer reads from the tiers", func(t *testing.T) {
		tests := []struct {
			name                       string
			capability, balanced, cost float64
			wantModel                  string
			wantWeight                 float64
		}{
			{"certain cost", 0, 0, 1, "cheap", 0.3},
			{"certain capability", 1, 0, 0, "top", 0},
			{"certain balanced", 0, 1, 0, "mid", 0.07},
			// 对三档权重直接求平均会得到 0.15，那个权重下选的是 cheap。
			{"an even split between capability and cost reads as balanced", 0.5, 0, 0.5, "mid", 0.07},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				trace := &Trace{}
				route, _, ok, pick := SelectRoute(withMode(decide("code.build", 1), tt.capability, tt.balanced, tt.cost), nil, adaptive, priced, trace)
				require.True(t, ok)
				assert.Equal(t, tt.wantModel, route.Model)
				require.NotNil(t, pick)
				assert.InDelta(t, tt.wantWeight, pick.Weight, 1e-9)
				require.NotNil(t, trace.Mode)
				assert.Empty(t, trace.Mode.Reason)
				assert.InDelta(t, tt.wantWeight, trace.Mode.Weight, 1e-9)
			})
		}
	})
	t.Run("the mode answer counts even when the task type is not trusted", func(t *testing.T) {
		untrusted := &Decision{TaskType: "general.chat", TaskTypeProbability: 0.3, Difficulty: 1}
		route, inherited, ok, pick := SelectRoute(withMode(untrusted, 1, 0, 0), nil, adaptive, priced, nil)
		require.True(t, ok)
		assert.True(t, inherited)
		assert.Equal(t, "general", route.Model, "能力优先时兜底取分数最高的，退到均衡档会取更便宜的 general-cheap")
		require.NotNil(t, pick)
		assert.Equal(t, 0.0, pick.Weight)
	})
	t.Run("without a usable mode answer the balanced weight applies and the trace says so", func(t *testing.T) {
		silent := decide("code.build", 1)
		silent.ModeProbabilities = map[string]float64{PreferenceCapability: 0, PreferenceBalanced: 0, PreferenceCost: 0}
		for name, decision := range map[string]*Decision{"no decision": nil, "no mode answer": decide("code.build", 1), "all zero": silent} {
			trace := &Trace{}
			_, _, ok, pick := SelectRoute(decision, nil, adaptive, priced, trace)
			require.True(t, ok, name)
			require.NotNil(t, pick, name)
			assert.Equal(t, 0.07, pick.Weight, name)
			require.NotNil(t, trace.Mode, name)
			assert.Equal(t, ModeNoAnswer, trace.Mode.Reason, name)
			assert.Equal(t, 0.07, trace.Mode.Weight, name)
			assert.Nil(t, trace.Mode.Shares, name)
		}
	})
	t.Run("a continuation keeps the previous model, so the judged weight is recorded but never used", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "medium"}
		trace := &Trace{}
		route, _, ok, pick := SelectRoute(withMode(decide("code.build", 0.1), 0, 0, 1), previous, adaptive, priced, trace)
		require.True(t, ok)
		assert.Equal(t, "top", route.Model)
		assert.Nil(t, pick)
		require.NotNil(t, trace.Mode)
		assert.InDelta(t, 0.3, trace.Mode.Weight, 1e-9)
	})
	t.Run("a fixed preference ignores the mode answer and leaves no mode in the trace", func(t *testing.T) {
		trace := &Trace{}
		route, _, ok, pick := SelectRoute(withMode(decide("code.build", 1), 0, 0, 1), nil, policy, priced, trace)
		require.True(t, ok)
		assert.Equal(t, "mid", route.Model, "均衡档的权重 0.07 不受 mode 影响")
		require.NotNil(t, pick)
		assert.Equal(t, 0.07, pick.Weight)
		assert.Nil(t, trace.Mode)
	})
}

// 三档权重的间距不均（0、0.07、0.3），所以先对位置求期望再取权重：
// 判断完全确定时就是该档的权重，对称的不确定落在均衡档，概率向成本移动权重不会变小。
func TestTierWeightsBlend(t *testing.T) {
	tiers := TierWeights{Capability: 0, Balanced: 0.07, Cost: 0.3}
	mode := func(capability, balanced, cost float64) map[string]float64 {
		return map[string]float64{PreferenceCapability: capability, PreferenceBalanced: balanced, PreferenceCost: cost}
	}
	tests := []struct {
		name         string
		tiers        TierWeights
		probability  map[string]float64
		wantPosition float64
		wantWeight   float64
	}{
		{"certain capability", tiers, mode(1, 0, 0), 0, 0},
		{"certain balanced", tiers, mode(0, 1, 0), 1, 0.07},
		{"certain cost", tiers, mode(0, 0, 1), 2, 0.3},
		{"between capability and balanced the weight follows the first segment", tiers, mode(0.2, 0.8, 0), 0.8, 0.056},
		{"between balanced and cost it follows the second segment", tiers, mode(0, 0.7, 0.3), 1.3, 0.139},
		{"leaning cost with some doubt", tiers, mode(0.1, 0.6, 0.3), 1.2, 0.116},
		{"leaning capability with some doubt", tiers, mode(0.3, 0.6, 0.1), 0.8, 0.056},
		{"an even three-way split is balanced, not the 0.123 average of the weights", tiers, mode(1.0/3, 1.0/3, 1.0/3), 1, 0.07},
		{"an even split between the ends is balanced, not the 0.15 average", tiers, mode(0.5, 0, 0.5), 1, 0.07},
		{"probabilities that do not sum to one are normalized", tiers, mode(2, 6, 2), 1, 0.07},
		{"a missing preference counts as zero", tiers, map[string]float64{PreferenceCost: 1}, 2, 0.3},
		{"evenly spaced weights make it the plain expectation", TierWeights{Capability: 0.1, Balanced: 0.2, Cost: 0.3}, mode(0.1, 0.2, 0.7), 1.6, 0.26},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tiers.Blend(tt.probability)
			assert.Empty(t, got.Reason)
			assert.InDelta(t, tt.wantPosition, got.Position, 1e-9)
			assert.InDelta(t, tt.wantWeight, got.Weight, 1e-9)
			assert.Equal(t, tt.tiers, got.Tiers)
			sum := 0.0
			for _, share := range got.Shares {
				sum += share
			}
			assert.InDelta(t, 1, sum, 1e-9, "归一后的份额合计为 1")
		})
	}
	t.Run("an answer that cannot be read falls back to the balanced weight", func(t *testing.T) {
		unusable := map[string]map[string]float64{
			"no answer":         nil,
			"empty":             {},
			"all zero":          {PreferenceCapability: 0, PreferenceBalanced: 0, PreferenceCost: 0},
			"a negative":        {PreferenceCapability: -0.2, PreferenceBalanced: 0.6, PreferenceCost: 0.6},
			"NaN":               {PreferenceCapability: math.NaN(), PreferenceBalanced: 0.5, PreferenceCost: 0.5},
			"infinity":          {PreferenceCapability: math.Inf(1), PreferenceBalanced: 0.5, PreferenceCost: 0.5},
			"only other labels": {"turbo": 1},
		}
		for name, probabilities := range unusable {
			got := tiers.Blend(probabilities)
			assert.Equal(t, ModeNoAnswer, got.Reason, name)
			assert.Equal(t, 1.0, got.Position, name)
			assert.Equal(t, 0.07, got.Weight, name)
			assert.Nil(t, got.Shares, name)
		}
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

// 选路留痕记下一次路由是怎么来的：判断可不可信、是否续用上一轮、难度怎么变成档位、
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
		MinTaskTypeProbability: 0.5,
		ContinuationTaskChange: 0.5,
		Preference:             PreferenceBalanced,
		CostWeight:             0.07,
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

	t.Run("a trusted leaf records the comparison and how difficulty became the effort", func(t *testing.T) {
		route, trace := run(decide("code.build", 2, 1, 0.1), nil, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "mid", Effort: "medium"}, route)
		assert.Equal(t, &Trust{Threshold: 0.5, Profiles: []string{"code.build", "code"}}, trace.Trust)
		assert.Equal(t, &EffortTrace{Difficulty: 2, Base: "medium", Benefit: ptr(0.1), Threshold: 0.5}, trace.Effort)
		assert.Nil(t, trace.Continuation, "没有上一轮路由就谈不上续用")
		assert.Equal(t, []LadderStep{{Profile: "code.build", Models: 3, Outcome: OutcomeCompared, Model: "mid"}}, trace.Ladder)
	})

	t.Run("a task type without its own ranking falls to its class", func(t *testing.T) {
		route, trace := run(decide("code.frontend", 1, 1, 0.1), nil, candidates)
		assert.Equal(t, Route{Profile: "code", Model: "mid", Effort: "low"}, route)
		assert.Equal(t, []string{"code.frontend", "code"}, trace.Trust.Profiles)
		assert.Equal(t, []LadderStep{
			{Profile: "code.frontend", Outcome: OutcomeNone},
			{Profile: "code", Models: 2, Outcome: OutcomeCompared, Model: "mid"},
		}, trace.Ladder)
	})

	t.Run("an untrusted decision records why and takes the defaults", func(t *testing.T) {
		tests := []struct {
			name     string
			decision *Decision
			want     Trust
		}{
			{"jev failed", nil, Trust{Reason: TrustNoDecision, Threshold: 0.5}},
			{"no difficulty", &Decision{TaskType: "code.build", TaskTypeProbability: 0.9}, Trust{Reason: TrustNoDifficulty, Threshold: 0.5}},
			{"no class reaches the threshold",
				&Decision{Difficulty: 1, TaskTypeProbabilities: map[string]float64{"code.build": 0.3, "general.chat": 0.3, TaskTypeUnknown: 0.4}},
				Trust{Reason: TrustLowConfidence, Threshold: 0.5, Classes: map[string]float64{"code": 0.3, "general": 0.3}}},
		}
		for _, tt := range tests {
			route, trace := run(tt.decision, nil, candidates)
			assert.Equal(t, &tt.want, trace.Trust, tt.name)
			assert.Equal(t, Route{Model: "general", Effort: "medium"}, route, tt.name)
			assert.Equal(t, []LadderStep{{Profile: DefaultProfile, Models: 1, Outcome: OutcomeFirstAvailable, Model: "general"}}, trace.Ladder, tt.name)
			assert.Nil(t, trace.Effort, "不可信的判断不看难度: %s", tt.name)
		}
	})

	t.Run("split task types trust their class and record the class probabilities", func(t *testing.T) {
		split := &Decision{TaskType: "code.build", TaskTypeProbability: 0.375, Difficulty: 1, TaskChange: ptr(1),
			TaskTypeProbabilities: map[string]float64{"code.build": 0.375, "code.ops": 0.25, "general.chat": 0.375}}
		route, trace := run(split, nil, candidates)
		assert.Equal(t, Route{Profile: "code", Model: "mid", Effort: "low"}, route)
		assert.Equal(t, &Trust{Threshold: 0.5, Classes: map[string]float64{"code": 0.625, "general": 0.375}, Profiles: []string{"code"}}, trace.Trust)
		assert.Equal(t, []LadderStep{{Profile: "code", Models: 2, Outcome: OutcomeCompared, Model: "mid"}}, trace.Ladder)
	})

	t.Run("a continuation keeps the previous model and never lowers its effort", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "xhigh"}
		route, trace := run(decide("general.chat", 1, 0.1, 0.1), previous, candidates)
		assert.Equal(t, Route{Profile: "code.build", Model: "top", Effort: "xhigh"}, route)
		assert.Equal(t, &Continuation{Previous: "top", TaskChange: 0.1, Threshold: 0.5, Continued: true}, trace.Continuation)
		assert.Equal(t, &EffortTrace{Difficulty: 1, Base: "low", Benefit: ptr(0.1), Threshold: 0.5, Floored: true}, trace.Effort)
		assert.Empty(t, trace.Ladder, "续用不再排榜单")
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

	t.Run("an untrusted decision keeps the previous route", func(t *testing.T) {
		previous := &Route{Profile: "code.build", Model: "top", Effort: "high"}
		route, trace := run(nil, previous, candidates)
		assert.Equal(t, *previous, route)
		assert.Equal(t, TrustNoDecision, trace.Trust.Reason)
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
			"榜单第一的 top 不能服务，mid 成了参照，cheap 的分差在候选带内且更便宜")
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
func TestPickByQualityCostCapsRecordedSkips(t *testing.T) {
	var models []string
	scores := map[string]float64{}
	for i := range maxSkipped + 2 {
		name := "m" + strconv.Itoa(i)
		models = append(models, name)
		scores[name] = 0.99 - float64(i)*0.001
	}
	models = append(models, "last")
	scores["last"] = 0.97
	pick, ok := pickByQualityCost(models, scores, 0.07,
		func(name string) bool { return name == "last" },
		func(string) (float64, bool) { return 1, true },
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
