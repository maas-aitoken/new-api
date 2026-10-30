package autorouter_setting

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultsFromTaskModelsJSON(t *testing.T) {
	s := GetAutoRouterSetting()
	require.NotEmpty(t, s.DefaultModels)
	for _, taskType := range autorouter.TaskTypes() {
		assert.NotEmpty(t, s.TaskModels[taskType], taskType)
		if class := autorouter.TaskClass(taskType); class != "general" {
			assert.NotEmpty(t, s.TaskModels[class], class)
		}
	}
	assert.Equal(t, []string{"low", "medium", "high", "xhigh"}, s.DifficultyEfforts)
	// The embedded mapping says which leaderboard snapshots the lists are from
	// and which task types they are keyed by.
	for _, source := range []string{"generated_at", "livebench", "lmarena", "artificial_analysis", "design_arena"} {
		assert.NotEmpty(t, s.RankSources[source], source)
	}
	assert.True(t, s.RankingCurrent())
	assert.True(t, s.RankRefreshEnabled)
	assert.Equal(t, 24, s.RankRefreshIntervalHours)
	// 管理员能看到并调整的默认值：没设偏好的 key 走均衡，三档权重由小到大。
	assert.Equal(t, autorouter.PreferenceBalanced, s.DefaultPreference)
	assert.Equal(t, map[string]float64{"capability": 0, "balanced": 0.07, "cost": 0.3}, s.CostWeights)
}

// 质量/成本选择需要每个画像下各模型的榜单分，首次启动用内嵌文件里的 evidence 播种。
func TestTaskScoresSeededFromEmbeddedEvidence(t *testing.T) {
	s := GetAutoRouterSetting()
	require.NotEmpty(t, s.TaskModels)
	for profile, models := range s.TaskModels {
		require.NotEmpty(t, s.TaskScores[profile], profile)
		for _, model := range models {
			score, scored := s.TaskScores[profile][model]
			assert.True(t, scored, "%s/%s 在任务列表里却没有榜单分", profile, model)
			assert.GreaterOrEqual(t, score, 0.0, "%s/%s", profile, model)
			assert.LessOrEqual(t, score, 1.0, "%s/%s", profile, model)
		}
	}
	assert.Equal(t, s.TaskScores, shippedRanking.TaskScores)
}

// 排名刷新任务把 task_scores 作为 JSON 选项写进数据库，加载时整个换掉，不残留旧任务的分数。
func TestTaskScoresLoadFromOptionReplacesTheWholeMap(t *testing.T) {
	setting := GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	require.NotEmpty(t, setting.TaskScores["code.build"])

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"autorouter_setting.task_scores":        `{"code.build":{"model-a":0.9,"model-b":0.8}}`,
		"autorouter_setting.cost_weights":       `{"balanced":0.1}`,
		"autorouter_setting.default_preference": "cost",
	}))

	assert.Equal(t, map[string]map[string]float64{"code.build": {"model-a": 0.9, "model-b": 0.8}}, setting.TaskScores)
	assert.NotEmpty(t, shippedRanking.TaskScores["code.build"], "加载选项不能改到内嵌的榜单分")
	assert.NotContains(t, shippedRanking.TaskScores["code.build"], "model-a")
	assert.Equal(t, 0.1, setting.Policy(autorouter.PreferenceBalanced).CostWeight)
	assert.Equal(t, 0.3, setting.Policy(autorouter.PreferenceCost).CostWeight, "选项里没写的偏好用内置权重")
	assert.Equal(t, autorouter.PreferenceCost, setting.Policy("").Preference)
}

// 一个 key 的偏好、平台默认偏好和各偏好的权重都来自令牌或数据库选项：
// 认不出的偏好要退到默认，NaN 会让选择里的比较全部为假，必须在转成策略时挡住。
func TestPolicyResolvesPreferenceAndWeight(t *testing.T) {
	setting := GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })

	tests := []struct {
		name           string
		key, platform  string
		weights        map[string]float64
		wantPreference string
		wantWeight     float64
	}{
		{"a key's own preference wins", "capability", "cost", nil, "capability", 0},
		{"a key without one uses the platform default", "", "cost", nil, "cost", 0.3},
		{"an unknown key preference uses the platform default", "turbo", "capability", nil, "capability", 0},
		{"an unknown platform default falls to balanced", "", "turbo", nil, "balanced", 0.07},
		{"the admin sets each preference's weight", "balanced", "balanced", map[string]float64{"balanced": 0.12, "capability": 0.01}, "balanced", 0.12},
		{"capability's weight is settable too", "capability", "balanced", map[string]float64{"capability": 0.01}, "capability", 0.01},
		{"a negative weight becomes zero", "cost", "", map[string]float64{"cost": -0.3}, "cost", 0},
		{"a weight above one is capped", "cost", "", map[string]float64{"cost": 7}, "cost", 1},
		{"NaN becomes zero", "cost", "", map[string]float64{"cost": math.NaN()}, "cost", 0},
		{"infinity is capped", "cost", "", map[string]float64{"cost": math.Inf(1)}, "cost", 1},
		{"an adaptive key starts from the balanced weight", "adaptive", "cost", nil, "adaptive", 0.07},
		{"a key without one follows an adaptive platform default", "", "adaptive", nil, "adaptive", 0.07},
		{"the balanced weight the admin set is where an adaptive request starts", "adaptive", "", map[string]float64{"balanced": 0.12}, "adaptive", 0.12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting.DefaultPreference, setting.CostWeights = tt.platform, tt.weights
			policy := setting.Policy(tt.key)
			assert.Equal(t, tt.wantPreference, policy.Preference)
			assert.Equal(t, tt.wantWeight, policy.CostWeight)
		})
	}
}

// 自适应请求从三档权重里读本次的权重，三档都要按同样的规则限幅，
// 选项里写了 adaptive 这个键也不能当成一档。
func TestPolicyCarriesTheTierWeightsAnAdaptiveRequestReads(t *testing.T) {
	setting := GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })

	setting.CostWeights = map[string]float64{"capability": -1, "cost": 7, "adaptive": 0.5}
	policy := setting.Policy("adaptive")
	assert.Equal(t, autorouter.TierWeights{Capability: 0, Balanced: 0.07, Cost: 1}, policy.Tiers, "缺的取内置权重，越界的限幅")
	assert.Equal(t, 0.07, policy.CostWeight)
}

func TestRankingFallsBackWhenTaxonomyChanged(t *testing.T) {
	s := GetAutoRouterSetting()
	saved := *s
	t.Cleanup(func() { *s = saved })
	s.TaskModels = map[string][]string{"coding": {"stale"}}
	s.DefaultModels = []string{"stale"}
	s.TaskScores = map[string]map[string]float64{"coding": {"stale": 1}}

	s.RankSources = map[string]string{"taxonomy_version": "1"}
	ranking := s.Ranking()
	assert.Equal(t, shippedRanking.TaskModels, ranking.TaskModels, "lists keyed by older task types are not used")
	assert.Equal(t, shippedRanking.DefaultModels, ranking.DefaultModels)
	assert.Equal(t, shippedRanking.TaskScores, ranking.TaskScores, "榜单分和任务列表同源，一起退回内嵌的")
	assert.Equal(t, shippedRanking.TaskScores, s.Policy("").TaskScores)
	assert.Equal(t, shippedRanking.RankSources, ranking.Sources)

	s.RankSources = map[string]string{"taxonomy_version": autorouter.TaxonomyVersion}
	ranking = s.Ranking()
	assert.Equal(t, s.TaskModels, ranking.TaskModels)
	assert.Equal(t, []string{"stale"}, ranking.DefaultModels)
	assert.Equal(t, s.TaskScores, s.Policy("").TaskScores)
	assert.Equal(t, s.RankSources, ranking.Sources)
}

func TestConnectionsPreferTheOptionOverTheEnvironment(t *testing.T) {
	s := GetAutoRouterSetting()
	saved := *s
	t.Cleanup(func() { *s = saved })
	t.Setenv("JEV_ENDPOINT", "http://env.example/jev")
	t.Setenv("JEV_MODEL", "")
	t.Setenv("JEV_API_KEY", "env-key")
	t.Setenv("ARTIFICIAL_ANALYSIS_API_KEY", "aa-env")
	t.Setenv("DESIGN_ARENA_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	s.JevEndpoint, s.JevModel, s.JevAPIKey = "", "", ""
	s.ArtificialAnalysisKey, s.DesignArenaKey, s.OpenRouterKey = "", "", ""

	connections := s.Connections()
	assert.Equal(t, Connection{Value: "http://env.example/jev", Source: SourceEnv}, connections.JevEndpoint)
	assert.Equal(t, Connection{}, connections.JevModel)
	assert.Equal(t, Connection{Value: "env-key", Source: SourceEnv}, connections.JevAPIKey)
	assert.False(t, connections.RankKeysReady(), "只有 Artificial Analysis 的 key 不够，还要 Design Arena 或 OpenRouter 之一")

	s.JevEndpoint, s.OpenRouterKey = "http://option.example/jev", "or-option"
	connections = s.Connections()
	assert.Equal(t, Connection{Value: "http://option.example/jev", Source: SourceOption}, connections.JevEndpoint)
	assert.Equal(t, Connection{Value: "env-key", Source: SourceEnv}, connections.JevAPIKey, "库里没设的项仍用环境变量")
	assert.True(t, connections.RankKeysReady())
}

// 恢复内置榜单写回的四个选项必须能通过校验，并且让存的榜单重新与当前分类对得上。
func TestShippedOptionsPassValidationAndRestoreTheCurrentRanking(t *testing.T) {
	s := GetAutoRouterSetting()
	saved := *s
	t.Cleanup(func() { *s = saved })
	s.RankSources = map[string]string{"taxonomy_version": "1"}
	s.TaskModels = map[string][]string{"stale": {"x"}}
	require.False(t, s.RankingCurrent())

	options, err := ShippedOptions()
	require.NoError(t, err)
	require.Len(t, options, 4)
	for key, value := range options {
		require.NoError(t, ValidateOption(key, value), key)
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(options))

	assert.True(t, s.RankingCurrent())
	assert.Equal(t, shippedRanking.TaskModels, s.TaskModels)
	assert.Equal(t, shippedRanking.TaskScores, s.TaskScores)
	assert.Equal(t, shippedRanking.DefaultModels, s.DefaultModels)
}

// 选路日志默认只记做了决定的请求，保留 7 天；两个选项按键加载，保留期超出范围时清理任务按默认值算，
// 不会拿一个荒谬的截止时间去删数据。
func TestRouteLogOptionsLoadByKeyAndRetentionFallsBackToTheDefault(t *testing.T) {
	setting := GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	assert.Equal(t, RouteLogDecisions, setting.RouteLogMode)
	assert.Equal(t, 7*24*time.Hour, setting.RouteLogRetention())

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"autorouter_setting.route_log_mode":           "all",
		"autorouter_setting.route_log_retention_days": "30",
	}))
	assert.Equal(t, RouteLogAll, setting.RouteLogMode)
	assert.Equal(t, 30*24*time.Hour, setting.RouteLogRetention())

	for _, days := range []int{0, -3, MaxRouteLogRetentionDays + 1} {
		setting.RouteLogRetentionDays = days
		assert.Equal(t, 7*24*time.Hour, setting.RouteLogRetention(), "stored %d days", days)
	}
	setting.RouteLogRetentionDays = MaxRouteLogRetentionDays
	assert.Equal(t, time.Duration(MaxRouteLogRetentionDays)*24*time.Hour, setting.RouteLogRetention())
}

// 加载时解析失败的值会被跳过，所以写入前就要拦住，否则存进去却永远不生效。
func TestValidateOption(t *testing.T) {
	tests := []struct {
		key, value string
		valid      bool
	}{
		{"autorouter_setting.enabled", "true", true},
		{"autorouter_setting.enabled", "yes", false},
		{"autorouter_setting.jev_timeout_ms", "5000", true},
		{"autorouter_setting.jev_timeout_ms", "5000.000000", true},
		{"autorouter_setting.jev_timeout_ms", "50", false},
		{"autorouter_setting.jev_timeout_ms", "1.5", false},
		{"autorouter_setting.jev_timeout_ms", "NaN", false},
		{"autorouter_setting.max_runes", "0", true},
		{"autorouter_setting.max_runes", "-1", false},
		{"autorouter_setting.cache_ttl_seconds", "0", false},
		{"autorouter_setting.cache_ttl_seconds", "7200", true},
		{"autorouter_setting.rank_refresh_interval_hours", "0", false},
		{"autorouter_setting.rank_refresh_interval_hours", "24", true},
		{"autorouter_setting.upgrade_benefit", "0.5", true},
		{"autorouter_setting.upgrade_benefit", "1.5", false},
		{"autorouter_setting.min_task_type_probability", "NaN", false},
		{"autorouter_setting.default_effort", "medium", true},
		{"autorouter_setting.default_effort", "", false},
		{"autorouter_setting.difficulty_efforts", `["low","medium","high","xhigh"]`, true},
		{"autorouter_setting.difficulty_efforts", `[]`, false},
		{"autorouter_setting.difficulty_efforts", `["low","extreme"]`, false},
		{"autorouter_setting.difficulty_efforts", `low`, false},
		{"autorouter_setting.default_preference", "cost", true},
		{"autorouter_setting.default_preference", "adaptive", true},
		{"autorouter_setting.default_preference", "cheap", false},
		{"autorouter_setting.cost_weights", `{"balanced":0.1}`, true},
		{"autorouter_setting.cost_weights", `{"adaptive":0.1}`, false},
		{"autorouter_setting.cost_weights", `{"balanced":2}`, false},
		{"autorouter_setting.cost_weights", `{"cheap":0.1}`, false},
		{"autorouter_setting.cost_weights", `[0.1]`, false},
		{"autorouter_setting.cost_weights", `null`, false},
		{"autorouter_setting.task_models", `null`, false},
		{"autorouter_setting.task_models", `{"code.build":["a","b"]}`, true},
		{"autorouter_setting.task_models", `{"code.build":"a"}`, false},
		{"autorouter_setting.task_scores", `{"code.build":{"a":0.9}}`, true},
		{"autorouter_setting.task_scores", `{"code.build":{"a":-1}}`, false},
		{"autorouter_setting.default_models", `["a"]`, true},
		{"autorouter_setting.rank_sources", `{"taxonomy_version":"3"}`, true},
		{"autorouter_setting.rank_sources", `{"taxonomy_version":3}`, false},
		{"autorouter_setting.route_log_mode", "off", true},
		{"autorouter_setting.route_log_mode", "decisions", true},
		{"autorouter_setting.route_log_mode", "all", true},
		{"autorouter_setting.route_log_mode", "everything", false},
		{"autorouter_setting.route_log_mode", "", false},
		{"autorouter_setting.route_log_retention_days", "7", true},
		{"autorouter_setting.route_log_retention_days", "365", true},
		{"autorouter_setting.route_log_retention_days", "366", false},
		{"autorouter_setting.route_log_retention_days", "0", false},
		{"autorouter_setting.route_log_retention_days", "1.5", false},
		{"autorouter_setting.jev_endpoint", "", true},
		{"autorouter_setting.jev_endpoint", "https://jev.example/v1/decisions", true},
		{"autorouter_setting.jev_endpoint", "ftp://jev.example", false},
		{"autorouter_setting.jev_endpoint", "jev.example", false},
		{"autorouter_setting.jev_model", "typesafe/jev-1.13", true},
		{"autorouter_setting.jev_model", "two words", false},
		{"autorouter_setting.jev_api_key", "sk-abc", true},
		{"autorouter_setting.jev_api_key", "sk-abc\nX-Injected: 1", false},
		{"autorouter_setting.openrouter_api_key", strings.Repeat("k", 513), false},
		{"autorouter_setting.something_new", "anything", true},
		{"other_setting.enabled", "yes", true},
	}
	for _, tt := range tests {
		err := ValidateOption(tt.key, tt.value)
		if tt.valid {
			assert.NoError(t, err, "%s=%q", tt.key, tt.value)
		} else {
			assert.Error(t, err, "%s=%q", tt.key, tt.value)
		}
	}
}
