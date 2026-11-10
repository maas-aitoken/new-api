package modelrank

import (
	"context"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/pkg/autorouter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPool(ids ...[2]string) *pool {
	p := newPool()
	for _, id := range ids {
		p.add(&candidate{ID: id[0], Slug: id[1], Name: gatewayName(id[0])})
	}
	return p
}

func TestMatch(t *testing.T) {
	p := testPool(
		[2]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5-20260921"},
		[2]string{"openai/o4-mini", "openai/o4-mini-20250416"},
		[2]string{"openai/o4-mini-high", "openai/o4-mini-high-20250416"},
		[2]string{"qwen/qwen3.8-max-prime", "qwen/qwen3.8-max-prime-20260923"},
		[2]string{"google/gemini-3.1-pro-preview", "google/gemini-3.1-pro-preview-20260219"},
		[2]string{"qwen/qwen3-vl-235b-a22b-instruct", "qwen/qwen3-vl-235b-a22b-instruct"},
		[2]string{"deepseek/deepseek-v4-pro", "deepseek/deepseek-v4-pro-20260423"},
		[2]string{"deepseek/deepseek-v4-pro-0813", "deepseek/deepseek-v4-pro-20260813"},
		[2]string{"meta/muse-spark-1.3", "meta/muse-spark-1.3-20261001"},
	)
	tests := []struct {
		name string
		want string
	}{
		{"Claude Opus 5.5 (Max)", "anthropic/claude-opus-5.5"},
		{"claude-opus-5.5-medium", "anthropic/claude-opus-5.5"},
		{"o4-mini-high", "openai/o4-mini-high"},
		{"o4-mini (2025-04-16)", "openai/o4-mini"},
		{"gemini-3.1-pro", "google/gemini-3.1-pro-preview"},
		{"DeepSeek-V4-Pro (High, 20260813)", "deepseek/deepseek-v4-pro-0813"},
		{"DeepSeek-V4-Pro (High)", "deepseek/deepseek-v4-pro"},
		{"muse-spark-1.3-max", "meta/muse-spark-1.3"},
		{"qwen3.8-max", ""},
		{"Qwen3-VL-235B-A22B-Thinking", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ""
			if c := p.match(tt.name); c != nil {
				got = c.ID
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGatewayName(t *testing.T) {
	assert.Equal(t, "claude-opus-5-5", gatewayName("anthropic/claude-opus-5.5"))
	assert.Equal(t, "gpt-6.1-sol", gatewayName("openai/gpt-6.1-sol"))
	assert.Equal(t, "deepseek-v4.1-flash", gatewayName("deepseek/deepseek-v4.1-flash"))
}

func TestNormalize(t *testing.T) {
	p := testPool([2]string{"a/x", "a/x"}, [2]string{"a/y", "a/y"}, [2]string{"a/z", "a/z"})
	x, y, z := p.list[0], p.list[1], p.list[2]
	x.record("lb.coding", 80)
	x.record("lb.coding", 60) // a weaker effort variant keeps the best
	y.record("lb.coding", 40)
	x.record("lma.overall", 1500)
	y.record("lma.overall", 1400)
	x.record("lma.agent_bash", 0.2)
	y.record("lma.agent_bash", -0.2)
	z.record("lma.agent_bash", 0)
	p.normalize()

	assert.InDelta(t, 1, x.Norm["lb.coding"], 1e-9)
	assert.InDelta(t, 0.5, y.Norm["lb.coding"], 1e-9)
	assert.InDelta(t, 1, x.Norm["lma.overall"], 1e-9)
	assert.InDelta(t, 2/(1+math.Pow(10, 100.0/400)), y.Norm["lma.overall"], 1e-9)
	assert.InDelta(t, 0.5, z.Norm["lma.agent_bash"], 1e-9)
	assert.InDelta(t, 0, y.Norm["lma.agent_bash"], 1e-9)
	assert.NotContains(t, z.Norm, "lb.coding")
}

func TestVisionMetricNeedsImageInput(t *testing.T) {
	p := testPool([2]string{"a/text", "a/text"}, [2]string{"a/vision", "a/vision"})
	p.list[1].Vision = true
	for _, c := range p.list {
		c.record("lb.browser", 0)
		c.record("lb.coding", 50)
	}
	assert.NotContains(t, p.list[0].Raw, "lb.browser", "a text-only model's 0 is missing input, not skill")
	assert.Contains(t, p.list[1].Raw, "lb.browser")
	assert.Contains(t, p.list[0].Raw, "lb.coding")
}

func TestDropStale(t *testing.T) {
	p := testPool([2]string{"a/old", "a/old"}, [2]string{"a/new1", "a/new1"}, [2]string{"a/new2", "a/new2"}, [2]string{"a/unrated", "a/unrated"})
	for i, c := range p.list {
		c.Created = int64(i)
	}
	old, new1, new2 := p.list[0], p.list[1], p.list[2]
	// aa.strategy_ops covers the newest rated models; aa.intelligence stopped
	// at the old one. The newest candidate has no aa data and is not probed.
	old.record("aa.intelligence", 50)
	old.record("aa.strategy_ops", 40)
	new1.record("aa.strategy_ops", 45)
	new2.record("aa.strategy_ops", 48)
	// lb has too few rated models to judge.
	new2.record("lb.coding", 70)

	assert.Equal(t, []string{"aa.intelligence"}, p.dropStale(2))
	assert.NotContains(t, old.Raw, "aa.intelligence")
	assert.Contains(t, old.Raw, "aa.strategy_ops")
	assert.Contains(t, new2.Raw, "lb.coding")
}

func TestScoreCoverageAndFrontier(t *testing.T) {
	p := testPool(
		[2]string{"a/top", "a/top"},
		[2]string{"a/pricey", "a/pricey"},
		[2]string{"a/cheap", "a/cheap"},
		[2]string{"a/sparse", "a/sparse"},
		[2]string{"a/weak", "a/weak"},
	)
	price := map[string]float64{"a/top": 8, "a/pricey": 20, "a/cheap": 1, "a/sparse": 0.5, "a/weak": 0.1}
	coding := map[string]float64{"a/top": 100, "a/pricey": 95, "a/cheap": 90, "a/weak": 50}
	for _, c := range p.list {
		c.Price = price[c.ID]
		if v, ok := coding[c.ID]; ok {
			c.record("lb.coding", v)
			c.record("lb.long_agentic", v)
		}
	}
	// sparse has one of code.build's three metrics: below the two it needs.
	p.list[3].record("lb.coding", 100)
	p.normalize()

	r := ranker{pool: p, defaultFloor: 0.8}
	r.score()
	ranked := r.ranked["code.build"]
	require.Len(t, ranked, 4)
	assert.Equal(t, "a/top", ranked[0].c.ID)
	assert.Equal(t, 2, ranked[0].Covered)
	assert.InDelta(t, 1, ranked[0].Score, 1e-9, "the mean is over the covered metrics")

	frontier := r.frontier("code.build")
	assert.True(t, frontier[p.list[0]])
	assert.False(t, frontier[p.list[1]], "pricier and weaker than top")
	assert.True(t, frontier[p.list[2]])
	assert.False(t, frontier[p.list[4]], "below the score floor")

	// general.browser has one metric, so one is enough.
	assert.Equal(t, 1, minCovered(1))
	assert.Equal(t, 1, minCovered(2))
	assert.Equal(t, 2, minCovered(3))
}

func TestMapping(t *testing.T) {
	p := testPool(
		[2]string{"a/top", "a/top"},
		[2]string{"a/mid", "a/mid"},
		[2]string{"a/cheap", "a/cheap"},
		[2]string{"a/weak", "a/weak"},
	)
	strength := map[string]float64{"a/top": 100, "a/mid": 95, "a/cheap": 92, "a/weak": 40}
	price := map[string]float64{"a/top": 10, "a/mid": 12, "a/cheap": 1, "a/weak": 0.5}
	for _, c := range p.list {
		c.Price = price[c.ID]
		c.Vision = true
		for metric, scale := range metricScales {
			v := strength[c.ID]
			if scale == elo {
				v = 1000 + 5*v
			}
			c.record(metric, v)
		}
	}
	p.normalize()
	r := ranker{pool: p, defaultFloor: 0.9}
	r.score()
	m, err := r.mapping(map[string]string{})
	require.NoError(t, err)

	// Every task type and class but general has its own list; general's is
	// the default list.
	var want []string
	for _, taskType := range autorouter.TaskTypes() {
		want = append(want, taskType, autorouter.TaskClass(taskType))
	}
	slices.Sort(want)
	want = slices.DeleteFunc(slices.Compact(want), func(profile string) bool { return profile == DefaultProfile })
	assert.Equal(t, want, slices.Sorted(maps.Keys(m.TaskModels)))
	for profile, models := range m.TaskModels {
		assert.Equal(t, []string{"top", "mid", "cheap", "weak"}, models, profile)
	}
	// The cheap frontier models lead the defaults; dominated and weak ones follow in score order.
	assert.Equal(t, []string{"cheap", "top", "mid", "weak"}, m.DefaultModels)

	// A profile nobody covers fails the build.
	for _, c := range p.list {
		delete(c.Norm, "lb.browser")
	}
	r.score()
	_, err = r.mapping(map[string]string{})
	assert.ErrorContains(t, err, "general.browser")
}

func TestEveryProfileMetricHasAScale(t *testing.T) {
	for profile, metrics := range profiles {
		require.NotEmpty(t, metrics, profile)
		assert.LessOrEqual(t, len(metrics), 3, profile)
		for _, metric := range metrics {
			assert.Contains(t, metricScales, metric, profile)
		}
	}
	for _, taskType := range autorouter.TaskTypes() {
		assert.Contains(t, profiles, taskType)
		assert.Contains(t, profiles, autorouter.TaskClass(taskType))
	}
}

func TestLoadArtificialAnalysisPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/language/models/free", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "aa-key", r.Header.Get("X-Api-Key"))
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"intelligence_index_version":5,"pagination":{"has_more":true},"data":[
				{"name":"Claude Opus 5.5 (Max)","slug":"claude-opus-5-5-max","evaluations":{"artificial_analysis_intelligence_index":58.4,"artificial_analysis_strategy_and_ops_index":null,"artificial_analysis_coding_index":80}},
				{"name":"Claude Opus 5.5 (Low)","slug":"claude-opus-5-5-low","evaluations":{"artificial_analysis_intelligence_index":40,"artificial_analysis_strategy_and_ops_index":51}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"intelligence_index_version":5,"pagination":{"has_more":false},"data":[
			{"name":"Unlisted","slug":"unlisted","evaluations":{"artificial_analysis_intelligence_index":10}}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	p := testPool([2]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5-20260921"})
	src := &sources{client: server.Client(), logf: func(string, ...any) {}}
	version, err := src.loadArtificialAnalysis(context.Background(), p, server.URL, "aa-key")
	require.NoError(t, err)
	assert.Equal(t, "intelligence_index_v5", version)
	// The best variant counts; indexes outside the mapping are ignored.
	assert.Equal(t, map[string]float64{"aa.intelligence": 58.4, "aa.strategy_ops": 51}, p.list[0].Raw)
}

func TestLoadDesignArena(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/leaderboard/models/", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer da-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"success":true,"data":[
			{"displayName":"Opus","openRouterId":"anthropic/claude-opus-5.5","elo":1400},
			{"displayName":"GPT-6.1 Sol","openRouterId":null,"elo":1350},
			{"displayName":"Some Builder","openRouterId":null,"elo":1500}],
			"meta":{"lastUpdated":"2026-10-27T00:00:00Z"}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	p := testPool(
		[2]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5-20260921"},
		[2]string{"openai/gpt-6.1-sol", "openai/gpt-6.1-sol-20260929"},
	)
	src := &sources{client: server.Client(), logf: func(string, ...any) {}}
	asOf, err := src.loadDesignArena(context.Background(), p, server.URL, "da-key")
	require.NoError(t, err)
	assert.Equal(t, "slides@2026-10-27T00:00:00Z uicomponent@2026-10-27T00:00:00Z website@2026-10-27T00:00:00Z", asOf)
	assert.Equal(t, map[string]float64{"da.website": 1400, "da.uicomponent": 1400, "da.slides": 1400}, p.list[0].Raw)
	assert.Equal(t, 1350.0, p.list[1].Raw["da.website"], "a row without an OpenRouter id matches by name")
}

func TestLoadLiveBenchNewestRelease(t *testing.T) {
	// The bundle names a newer release that has no table yet; the newest one
	// with a table wins.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<script src="./static/js/main.074b2ee8.js"></script>`))
	})
	mux.HandleFunc("/static/js/main.074b2ee8.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`_e=["20261014"],x="20261101",y=["20250805"]`))
	})
	mux.HandleFunc("/data/20261014/table.csv", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("model,coding,reasoning,unknown_column\nclaude-opus-5.5-high,81.5,77,1\nunlisted-model,50,50,1\n"))
	})
	mux.HandleFunc("/data/20261101/table.csv", http.NotFound)
	server := httptest.NewServer(mux)
	defer server.Close()

	p := testPool([2]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5-20260921"})
	src := &sources{client: server.Client(), logf: func(string, ...any) {}}
	release, err := src.loadLiveBench(context.Background(), p, server.URL, "")
	require.NoError(t, err)
	assert.Equal(t, "20261014", release)
	assert.Equal(t, map[string]float64{"lb.coding": 81.5, "lb.reasoning": 77}, p.list[0].Raw)
}

func TestLoadDesignArenaViaOpenRouter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/benchmarks", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer or-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"meta":{"as_of":"2026-10-28T00:05:23Z"},"data":[
			{"source":"design-arena","arena":"models","category":"website","model_permaslug":"anthropic/claude-opus-5.5-20260921","elo":1400},
			{"source":"design-arena","arena":"models","category":"gamedev","model_permaslug":"anthropic/claude-opus-5.5-20260921","elo":1500},
			{"source":"design-arena","arena":"builders","category":"website","model_permaslug":"anthropic/claude-opus-5.5-20260921","elo":1600},
			{"source":"artificial-analysis","model_permaslug":"anthropic/claude-opus-5.5-20260921","intelligence_index":58}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	p := testPool([2]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5-20260921"})
	src := &sources{client: server.Client(), logf: func(string, ...any) {}}
	asOf, err := src.loadDesignArenaViaOpenRouter(context.Background(), p, server.URL, "or-key")
	require.NoError(t, err)
	assert.Equal(t, "openrouter@2026-10-28T00:05:23Z", asOf)
	// Only the mapped Models Arena categories count.
	assert.Equal(t, map[string]float64{"da.website": 1400}, p.list[0].Raw)
}
