package autorouter_setting

import (
	"maps"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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
	// 手工编辑按 RankingProfiles 读写整份榜单：内置 task_models 的键加上 general（只在 default_models 里）
	// 必须正好是这些画像，否则编辑页看到的画像和保存时要求的对不上。
	lists := append(slices.Collect(maps.Keys(shippedRanking.TaskModels)), autorouter.DefaultProfile)
	slices.Sort(lists)
	assert.Equal(t, autorouter.RankingProfiles(), lists)
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

// rankingEditFixture 把设置复位成内置榜单，返回编辑页加载时读到的 base 和画像列表。
func rankingEditFixture(t *testing.T) (*AutoRouterSetting, RankingBase, []RankingProfile) {
	t.Helper()
	setting := GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	shipped, err := ShippedOptions()
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(shipped))
	setting.RankRefreshEnabled = true
	ranking := setting.Ranking()
	base := RankingBase{GeneratedAt: ranking.Sources["generated_at"], EditedAt: ranking.Sources["edited_at"]}
	return setting, base, ranking.Profiles()
}

// 手工编辑可以加榜外模型、移除模型、改分，以及在列表末尾放只当兜底的无分模型；
// 保存的列表就是编辑的样子，同时关掉定时刷新，rank_sources 保留来源日期并盖上编辑时间。
func TestEditedRankingWritesTheEditedListsAndTurnsRefreshOff(t *testing.T) {
	setting, base, profiles := rankingEditFixture(t)
	build := slices.Index(autorouter.RankingProfiles(), "code.build")
	original := profiles[build].Models
	require.Greater(t, len(original), 3)
	removed := original[len(original)-1].Model
	rescored := original[0].Model
	privateScore, newScore := 0.95, 0.5

	edited := []RankingEntry{{Model: "  private-coder ", Score: &privateScore}, {Model: rescored, Score: &newScore}}
	edited = append(edited, original[1:len(original)-1]...)
	edited = append(edited, RankingEntry{Model: "fallback-only"})
	profiles[build].Models = edited

	now := time.Date(2026, 11, 9, 8, 30, 0, 0, time.FixedZone("CST", 8*3600))
	edit, err := setting.EditedRanking(base, profiles, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"code.build"}, edit.Profiles, "只有改过的画像算变化")
	assert.Equal(t, 2, edit.Added, "private-coder 和 fallback-only")
	assert.Equal(t, 1, edit.Removed)
	assert.Equal(t, 1, edit.Rescored)
	for key, value := range edit.Options {
		require.NoError(t, ValidateOption(key, value), key)
	}

	require.NoError(t, config.GlobalConfig.LoadFromDB(edit.Options))
	assert.False(t, setting.RankRefreshEnabled, "保存时关掉定时刷新，免得下一次刷新把手工改动冲掉")
	require.True(t, setting.RankingCurrent())
	models := setting.TaskModels["code.build"]
	assert.Equal(t, "private-coder", models[0], "模型名去掉首尾空白，顺序照编辑的写")
	assert.Equal(t, "fallback-only", models[len(models)-1])
	assert.NotContains(t, models, removed)
	assert.Equal(t, 0.95, setting.TaskScores["code.build"]["private-coder"])
	assert.Equal(t, 0.5, setting.TaskScores["code.build"][rescored])
	assert.NotContains(t, setting.TaskScores["code.build"], "fallback-only", "没给分的模型只当兜底")
	assert.NotContains(t, setting.TaskModels, autorouter.DefaultProfile, "general 只写进 default_models")
	assert.Equal(t, shippedRanking.DefaultModels, setting.DefaultModels, "没改的画像原样写回，default_models 的帕累托顺序不变")
	assert.Equal(t, shippedRanking.TaskModels["code.frontend"], setting.TaskModels["code.frontend"])
	assert.Equal(t, "2026-11-09T00:30:00Z", setting.RankSources["edited_at"])
	assert.Equal(t, shippedRanking.RankSources["livebench"], setting.RankSources["livebench"], "来源的快照日期保留")
	assert.Equal(t, 0.95, setting.Policy("").TaskScores["code.build"]["private-coder"], "下一个请求就按改过的分选")
}

// 库里存的是旧分类下的榜单时运行时用内置的那份，编辑页看到和改的也是它；保存后改动直接生效，
// 不会因为 rank_sources 的版本旧而被忽略。
func TestEditedRankingTakesEffectOverAStoredRankingOfOlderTaskTypes(t *testing.T) {
	setting, _, _ := rankingEditFixture(t)
	setting.RankSources = map[string]string{"taxonomy_version": "1", "generated_at": "2026-01-01T00:00:00Z"}
	setting.TaskModels = map[string][]string{"coding": {"stale"}}
	require.False(t, setting.RankingCurrent())

	ranking := setting.Ranking()
	profiles := ranking.Profiles()
	general := slices.Index(autorouter.RankingProfiles(), autorouter.DefaultProfile)
	profiles[general].Models = append(profiles[general].Models, RankingEntry{Model: "private-default"})
	edit, err := setting.EditedRanking(RankingBase{GeneratedAt: ranking.Sources["generated_at"]}, profiles, time.Now())
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(edit.Options))

	assert.True(t, setting.RankingCurrent())
	assert.NotContains(t, setting.TaskModels, "coding", "旧分类下的列表不再留着")
	assert.Equal(t, append(slices.Clone(shippedRanking.DefaultModels), "private-default"), setting.Ranking().DefaultModels)
}

// 不合法或过期的编辑一律拒绝，错误是前端能翻译的固定英文句子；内容和当前榜单一样时也拒绝，
// 免得一次空保存就盖上编辑时间、关掉刷新。
func TestEditedRankingRejects(t *testing.T) {
	names := autorouter.RankingProfiles()
	build := slices.Index(names, "code.build")
	general := slices.Index(names, autorouter.DefaultProfile)
	score := func(value float64) *float64 { return &value }
	addToBuild := func(entry RankingEntry) func(*RankingBase, []RankingProfile) []RankingProfile {
		return func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			profiles[build].Models = append(profiles[build].Models, entry)
			return profiles
		}
	}
	tests := []struct {
		name string
		edit func(base *RankingBase, profiles []RankingProfile) []RankingProfile
		want string
	}{
		{"nothing changed", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile { return profiles }, "No changes to save"},
		{"refreshed since loading", func(base *RankingBase, profiles []RankingProfile) []RankingProfile {
			base.GeneratedAt = "2026-01-01T00:00:00Z"
			return addToBuild(RankingEntry{Model: "x"})(base, profiles)
		}, "The ranking was changed after you loaded it. Reload it and make your edits again."},
		{"edited by someone else since loading", func(base *RankingBase, profiles []RankingProfile) []RankingProfile {
			base.EditedAt = "2026-11-09T00:00:00Z"
			return addToBuild(RankingEntry{Model: "x"})(base, profiles)
		}, "The ranking was changed after you loaded it. Reload it and make your edits again."},
		{"a profile left out", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			return slices.Delete(profiles, build, build+1)
		}, "The ranking is missing profile code.build. Reload it and make your edits again."},
		{"an unknown profile", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			return append(profiles, RankingProfile{Profile: "coding"})
		}, "Unknown ranking profile coding"},
		{"a profile twice", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			return append(profiles, profiles[build])
		}, "Profile code.build is listed more than once"},
		{"general emptied", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			profiles[general].Models = nil
			return profiles
		}, "The general ranking cannot be empty, because it is the last fallback"},
		{"an empty model name", addToBuild(RankingEntry{Model: "  "}), "A model name in code.build is empty"},
		{"auto itself", addToBuild(RankingEntry{Model: "auto"}), "auto cannot be listed in a ranking"},
		{"a line break in a name", addToBuild(RankingEntry{Model: "a\nb"}), "A model name in code.build contains a line break or control character"},
		{"the same model twice after trimming", func(_ *RankingBase, profiles []RankingProfile) []RankingProfile {
			profiles[build].Models = append(profiles[build].Models, RankingEntry{Model: "x"}, RankingEntry{Model: " x "})
			return profiles
		}, "Model x is listed more than once in code.build"},
		{"a score above one", addToBuild(RankingEntry{Model: "x", Score: score(1.2)}), "The score of x in code.build must be between 0 and 1"},
		{"a negative score", addToBuild(RankingEntry{Model: "x", Score: score(-0.1)}), "The score of x in code.build must be between 0 and 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting, base, profiles := rankingEditFixture(t)
			profiles = tt.edit(&base, profiles)
			_, err := setting.EditedRanking(base, profiles, time.Now())
			require.Error(t, err)
			var message *common.Message
			require.ErrorAs(t, err, &message, "web console messages are translated by key")
			assert.Equal(t, tt.want, err.Error())
		})
	}
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
		{"autorouter_setting.continuation_task_change", "NaN", false},
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
