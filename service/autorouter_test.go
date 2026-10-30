package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/observability"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/service/capability"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	testJevAPIKey = "test-jev-key"
	testJevModel  = "test/jev"
)

// fakeJev answers by the current task: "hard" is a very difficult coding task,
// "careful" and "frugal" the same task from a user who stresses capability or
// cost, "ok" is unclassifiable, "split" spreads the task type over several leaves
// without any single one reaching the trust threshold, anything else is a
// simple question. Every answer continues the previous task and leans balanced
// unless the task says otherwise. It expects the key and model that
// useAutoRouterSetting configures, and that every request asks the mode question.
func fakeJev(t *testing.T, calls *atomic.Int32, delay time.Duration) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(delay)
		var payload struct {
			Model     string            `json:"model"`
			State     map[string]string `json:"state"`
			Questions map[string]struct {
				Type     string `json:"type"`
				Criteria any    `json:"criteria"`
			} `json:"questions"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		assert.Equal(t, "Bearer "+testJevAPIKey, r.Header.Get("Authorization"))
		assert.Equal(t, testJevModel, payload.Model)
		asked := payload.Questions["mode"]
		assert.Equal(t, "choice", asked.Type, "不论令牌用哪种偏好，每次都问 mode")
		criteria, _ := asked.Criteria.(map[string]any)
		assert.Len(t, criteria, len(autorouter.Preferences))
		for _, preference := range autorouter.Preferences {
			assert.Contains(t, criteria, preference)
		}
		taskType, difficulty, mode := "general.chat", "0", autorouter.PreferenceBalanced
		probabilities := ""
		switch payload.State["current_task"] {
		case "hard":
			taskType, difficulty = "code.build", "3"
		case "careful":
			taskType, difficulty, mode = "code.build", "3", autorouter.PreferenceCapability
		case "frugal":
			taskType, difficulty, mode = "code.build", "3", autorouter.PreferenceCost
		case "ok":
			taskType = autorouter.TaskTypeUnknown
		case "split":
			// 概率最高的叶子是 general.chat，但只有 0.4；code 下的两个叶子加起来 0.6。
			difficulty = "3"
			probabilities = `"general.chat":0.4,"code.build":0.35,"code.ops":0.25`
		}
		if probabilities == "" {
			probabilities = `"` + taskType + `":0.9`
		}
		var modeShares []string
		for _, preference := range autorouter.Preferences {
			share := "0.05"
			if preference == mode {
				share = "0.9"
			}
			modeShares = append(modeShares, `"`+preference+`":`+share)
		}
		_, _ = w.Write([]byte(`{"answers":{
			"task_type":{"type":"choice","choice":"` + taskType + `","probabilities":{` + probabilities + `}},
			"mode":{"type":"choice","choice":"` + mode + `","probabilities":{` + strings.Join(modeShares, ",") + `}},
			"difficulty":{"type":"score","probabilities":{"` + difficulty + `":0.8}},
			"task_change":{"type":"noul","noul":0.1},
			"larger_model_benefit":{"type":"noul","noul":0.1}}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func useAutoRouterSetting(t *testing.T, endpoint string, timeoutMs int) {
	setting := autorouter_setting.GetAutoRouterSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	t.Setenv("JEV_ENDPOINT", endpoint)
	t.Setenv("JEV_API_KEY", testJevAPIKey)
	t.Setenv("JEV_MODEL", testJevModel)
	setting.Enabled = true
	setting.JevTimeoutMs = timeoutMs
	setting.TaskModels = map[string][]string{
		"code.build":   {"coder"},
		"general.chat": {"chat"},
	}
	setting.DefaultModels = []string{"general"}
	setting.RankSources = map[string]string{"taxonomy_version": autorouter.TaxonomyVersion}
	setting.DifficultyEfforts = []string{"low", "medium", "high", "xhigh"}
	setting.DefaultEffort = "medium"
}

func newAutoRouterContext(tokenID int, body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
	return c
}

// candidatesFrom 把"这个模型名能不能用"的判定包成 AutoCandidates。
// 这些用例不区分客户端的推理字段，也不关心渠道。
func candidatesFrom(serves func(name string) bool) AutoCandidates {
	return AutoCandidates{
		Serves:   func(name string, _ bool) bool { return serves(name) },
		Channels: func(string) []int { return nil },
	}
}

var allModelsAvailable = candidatesFrom(func(string) bool { return true })

// encryptedSubAgentBody 是子 agent 的任务：文字被加密读不出来，只有会话 id 可用。
func encryptedSubAgentBody(session string) string {
	return `{"client_metadata":{"session_id":"` + session + `"},"input":[{"type":"agent_message","content":[
		{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/scan\nSender: /root\nPayload:"},
		{"type":"encrypted_content","encrypted_content":"gAAA"}]}]}`
}

func TestResolveAutoModelCachesDecisionPerInstruction(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	body := `{"model":"auto","messages":[{"role":"user","content":"hard"}]}`

	first, err := ResolveAutoModel(newAutoRouterContext(9101, body), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", first.ResolvedModel)
	assert.Equal(t, autorouter.Route{Profile: "code.build", Model: "coder", Effort: "xhigh"}, first.Route)
	assert.Equal(t, autorouter.SourceJev, first.Source)
	require.NotNil(t, first.Jev)
	assert.NotEmpty(t, first.Jev.Response)

	c := newAutoRouterContext(9101, body)
	second, err := ResolveAutoModel(c, allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", second.ResolvedModel)
	assert.Equal(t, autorouter.SourceCache, second.Source)
	assert.Nil(t, second.Jev, "a cache hit must not carry jev output")
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, "code.build", common.GetContextKeyString(c, constant.ContextKeySchedulerWorkload))
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyAutoRouted), "every resolved request is marked, a cache hit included")

	_, err = ResolveAutoModel(newAutoRouterContext(9102, body), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "another token must not share the decision")
}

func TestResolveAutoModelFollowsPreviousRoute(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

	_, err := ResolveAutoModel(newAutoRouterContext(9201, `{"messages":[{"role":"user","content":"hard"}]}`), allModelsAvailable)
	require.NoError(t, err)

	unknown, err := ResolveAutoModel(newAutoRouterContext(9201, `{"messages":[
		{"role":"user","content":"hard"},{"role":"assistant","content":"done"},{"role":"user","content":"ok"}]}`), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", unknown.ResolvedModel)
	assert.Equal(t, autorouter.SourceInherit, unknown.Source)
	assert.False(t, unknown.EffortOverride, "不可信的判断沿用上一轮路由，不碰客户端的推理字段")
	require.NotNil(t, unknown.Decision)
	assert.Equal(t, autorouter.TaskTypeUnknown, unknown.Decision.TaskType)

	continued, err := ResolveAutoModel(newAutoRouterContext(9201, `{"messages":[
		{"role":"user","content":"hard"},{"role":"assistant","content":"done"},{"role":"user","content":"rename it"}]}`), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", continued.ResolvedModel, "a continuation keeps the model and never lowers the effort")
	assert.Equal(t, autorouter.SourceJev, continued.Source)
	assert.Equal(t, "general.chat", continued.Decision.TaskType)
	assert.True(t, continued.EffortOverride)
}

// 没有任何一个叶子的概率过半，但同一大类下各叶子的概率之和过半：按大类的榜单选模型，
// 这仍然是一次可信的判断，档位由 auto 定，会覆盖客户端自己写的推理字段。
func TestResolveAutoModelTrustsTheClassWhenNoSingleLeafIsTrusted(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	autorouter_setting.GetAutoRouterSetting().TaskModels["code"] = []string{"class-coder"}

	result, err := ResolveAutoModel(newAutoRouterContext(9451, `{"messages":[{"role":"user","content":"split"}]}`), allModelsAvailable)
	require.NoError(t, err)
	require.NotNil(t, result.Decision)
	assert.Less(t, result.Decision.TaskTypeProbability, 0.5, "概率最高的叶子没有过半")
	assert.Equal(t, autorouter.Route{Profile: "code", Model: "class-coder", Effort: "xhigh"}, result.Route)
	assert.Equal(t, "class", result.Fallback())
	assert.Equal(t, autorouter.SourceJev, result.Source)
	assert.True(t, result.EffortOverride)
	assert.Equal(t, "class-coder-xhigh", result.ResolvedModel)
}

func TestResolveAutoModelEncryptedSubAgentInheritsSessionRoute(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	subAgent := encryptedSubAgentBody

	_, err := ResolveAutoModel(newAutoRouterContext(9251, `{"client_metadata":{"session_id":"sess-a"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hard"}]}]}`), allModelsAvailable)
	require.NoError(t, err)

	result, err := ResolveAutoModel(newAutoRouterContext(9251, subAgent("sess-a")), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", result.ResolvedModel)
	assert.Equal(t, autorouter.SourceInherit, result.Source)
	assert.True(t, result.EffortOverride, "子 agent 继承的是一次可信判断的路由，也继承覆盖推理字段的资格")
	assert.Nil(t, result.Jev)
	assert.Equal(t, int32(1), calls.Load())

	result, err = ResolveAutoModel(newAutoRouterContext(9251, subAgent("sess-b")), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "general-medium", result.ResolvedModel, "an unknown session falls back to the default route")
	assert.Equal(t, autorouter.SourceDefault, result.Source)
	assert.False(t, result.EffortOverride)
}

func TestResolveAutoModelJevTimeoutFallsBackToDefault(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 200*time.Millisecond).URL, 50)

	result, err := ResolveAutoModel(newAutoRouterContext(9301, `{"messages":[{"role":"user","content":"hard"}]}`), allModelsAvailable)
	require.NoError(t, err, "a jev failure must not fail the request")
	assert.Equal(t, "general-medium", result.ResolvedModel)
	assert.Equal(t, autorouter.SourceDefault, result.Source)
	require.NotNil(t, result.Jev)
	assert.NotEmpty(t, result.Jev.Error)
	assert.Nil(t, result.Decision)
}

func TestResolveAutoModelOnlyPicksAvailableModels(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	result, err := ResolveAutoModel(newAutoRouterContext(9401, body), candidatesFrom(func(model string) bool { return model == "coder-high" }))
	require.NoError(t, err)
	assert.Equal(t, "coder-high", result.ResolvedModel, "a missing effort variant falls to the nearest one")

	result, err = ResolveAutoModel(newAutoRouterContext(9401, body), candidatesFrom(func(model string) bool { return model == "general" }))
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load(), "the cached decision is reused, only the route is recomputed")
	assert.Equal(t, "general", result.ResolvedModel, "the bare model runs at the provider default effort")
	assert.Equal(t, "xhigh", result.Effort)

	_, err = ResolveAutoModel(newAutoRouterContext(9401, body), candidatesFrom(func(string) bool { return false }))
	require.Error(t, err)
}

func TestResolveAutoModelRecomputesRouteFromCachedDecision(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	// 第一次只有兜底模型可用（比如这次请求带图，别的模型能力不满足）。
	first, err := ResolveAutoModel(newAutoRouterContext(9411, body), candidatesFrom(func(model string) bool { return model == "general" }))
	require.NoError(t, err)
	assert.Equal(t, "general", first.ResolvedModel)

	// 同一条指令的下一次请求所有模型都可用：不能被上一次的选择钉住，也不必再调 jev。
	second, err := ResolveAutoModel(newAutoRouterContext(9411, body), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "coder-xhigh", second.ResolvedModel)
	assert.Equal(t, autorouter.SourceCache, second.Source)
	assert.Equal(t, int32(1), calls.Load())
}

func TestResolveAutoModelSkipsJevWhenNothingIsRoutable(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

	c := newAutoRouterContext(9412, `{"messages":[{"role":"user","content":"hard"}]}`)
	_, err := ResolveAutoModel(c, candidatesFrom(func(string) bool { return false }))
	require.Error(t, err)
	assert.Equal(t, int32(0), calls.Load(), "没有任何可用模型时不该花一次 jev 调用")
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAutoRouted), "a request that resolved to no model is not marked")
}

// 候选的用户价格从 AutoCandidates.Price 进来，可信的判断在榜单分接近的模型里选更便宜的。
func TestResolveAutoModelPicksByQualityAndPrice(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	setting := autorouter_setting.GetAutoRouterSetting()
	setting.TaskModels = map[string][]string{"code.build": {"opus-m", "sonnet-m"}}
	setting.TaskScores = map[string]map[string]float64{"code.build": {"opus-m": 0.97, "sonnet-m": 0.96}}
	prices := map[string]float64{"opus-m": 8, "sonnet-m": 4}
	candidates := func(withPrice func(string) (float64, bool)) AutoCandidates {
		candidates := candidatesFrom(func(string) bool { return true })
		candidates.Price = withPrice
		return candidates
	}
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	t.Run("the cheaper model wins when the scores are close", func(t *testing.T) {
		result, err := ResolveAutoModel(newAutoRouterContext(9801, body), candidates(func(model string) (float64, bool) {
			price, ok := prices[model]
			return price, ok
		}))
		require.NoError(t, err)
		assert.Equal(t, "sonnet-m-xhigh", result.ResolvedModel)
		require.NotNil(t, result.Pick)
		assert.Equal(t, "sonnet-m", result.Pick.Chosen)
		assert.Len(t, result.Pick.Band, 2)
		assert.Equal(t, autorouter.PreferenceBalanced, result.Preference, "没设偏好的 key 走平台默认，出厂是均衡")
	})

	t.Run("a key that prefers capability keeps the best score whatever it costs", func(t *testing.T) {
		c := newAutoRouterContext(9804, body)
		common.SetContextKey(c, constant.ContextKeyTokenAutoPreference, autorouter.PreferenceCapability)
		result, err := ResolveAutoModel(c, candidates(func(model string) (float64, bool) { return prices[model], true }))
		require.NoError(t, err)
		assert.Equal(t, "opus-m-xhigh", result.ResolvedModel)
		assert.Equal(t, autorouter.PreferenceCapability, result.Preference)
		require.NotNil(t, result.Pick)
		assert.Len(t, result.Pick.Band, 1)
	})

	t.Run("a key without a preference follows a changed platform default", func(t *testing.T) {
		previous := setting.DefaultPreference
		setting.DefaultPreference = autorouter.PreferenceCapability
		t.Cleanup(func() { setting.DefaultPreference = previous })
		result, err := ResolveAutoModel(newAutoRouterContext(9805, body), candidates(func(model string) (float64, bool) { return prices[model], true }))
		require.NoError(t, err)
		assert.Equal(t, "opus-m-xhigh", result.ResolvedModel)
		assert.Equal(t, autorouter.PreferenceCapability, result.Preference)
	})

	t.Run("a model without a comparable price does not take part", func(t *testing.T) {
		result, err := ResolveAutoModel(newAutoRouterContext(9802, body), candidates(func(model string) (float64, bool) {
			return prices[model], model == "opus-m"
		}))
		require.NoError(t, err)
		assert.Equal(t, "opus-m-xhigh", result.ResolvedModel)
	})

	t.Run("without a price source the first available model in list order wins", func(t *testing.T) {
		result, err := ResolveAutoModel(newAutoRouterContext(9803, body), candidates(nil))
		require.NoError(t, err)
		assert.Equal(t, "opus-m-xhigh", result.ResolvedModel)
		assert.Nil(t, result.Pick)
	})
}

// 自适应偏好：令牌不定档位，每次请求按 jev 的 mode 判断从三档权重里读出本次的成本权重。
func TestResolveAutoModelAdaptivePreferenceReadsTheWeightFromTheModeAnswer(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	setting := autorouter_setting.GetAutoRouterSetting()
	setting.TaskModels = map[string][]string{"code.build": {"opus-m", "sonnet-m", "haiku-m"}}
	setting.TaskScores = map[string]map[string]float64{"code.build": {"opus-m": 0.97, "sonnet-m": 0.95, "haiku-m": 0.89}}
	prices := map[string]float64{"opus-m": 20, "sonnet-m": 8, "haiku-m": 2}
	candidates := candidatesFrom(func(string) bool { return true })
	candidates.Price = func(model string) (float64, bool) {
		price, ok := prices[model]
		return price, ok
	}
	resolve := func(t *testing.T, tokenID int, preference, instruction string) *autorouter.Result {
		c := newAutoRouterContext(tokenID, `{"messages":[{"role":"user","content":"`+instruction+`"}]}`)
		if preference != "" {
			common.SetContextKey(c, constant.ContextKeyTokenAutoPreference, preference)
		}
		result, err := ResolveAutoModel(c, candidates)
		require.NoError(t, err)
		return result
	}

	t.Run("the stronger the user stresses capability, the higher the model it picks", func(t *testing.T) {
		for i, tt := range []struct {
			instruction string
			wantModel   string
			wantWeight  float64
		}{
			{"careful", "opus-m-xhigh", 0.0105},
			{"hard", "sonnet-m-xhigh", 0.07},
			{"frugal", "haiku-m-xhigh", 0.2655},
		} {
			result := resolve(t, 9631+i, autorouter.PreferenceAdaptive, tt.instruction)
			assert.Equal(t, tt.wantModel, result.ResolvedModel, tt.instruction)
			assert.Equal(t, autorouter.PreferenceAdaptive, result.Preference)
			require.NotNil(t, result.Pick, tt.instruction)
			assert.InDelta(t, tt.wantWeight, result.Pick.Weight, 1e-9, tt.instruction)
			require.NotNil(t, result.Trace.Mode, tt.instruction)
			assert.InDelta(t, tt.wantWeight, result.Trace.Mode.Weight, 1e-9, tt.instruction)
			assert.Empty(t, result.Trace.Mode.Reason)
		}
	})
	t.Run("a fixed preference still asks the mode question but ignores the answer", func(t *testing.T) {
		result := resolve(t, 9634, autorouter.PreferenceCapability, "frugal")
		assert.Equal(t, "opus-m-xhigh", result.ResolvedModel)
		assert.Nil(t, result.Trace.Mode)
	})
	t.Run("the weight is read again from the tiers when the judgment comes from the cache", func(t *testing.T) {
		before := calls.Load()
		first := resolve(t, 9635, autorouter.PreferenceAdaptive, "frugal")
		assert.Equal(t, "haiku-m-xhigh", first.ResolvedModel)
		assert.Equal(t, before+1, calls.Load())

		saved := setting.CostWeights
		setting.CostWeights = map[string]float64{"capability": 0, "balanced": 0.03, "cost": 0.05}
		t.Cleanup(func() { setting.CostWeights = saved })
		second := resolve(t, 9635, autorouter.PreferenceAdaptive, "frugal")
		assert.Equal(t, before+1, calls.Load(), "命中判断缓存，不再调 jev")
		assert.Equal(t, autorouter.SourceCache, second.Source)
		assert.Equal(t, "sonnet-m-xhigh", second.ResolvedModel, "平台改了三档权重，同一个判断读出的权重跟着变")
		assert.Equal(t, autorouter.TierWeights{Capability: 0, Balanced: 0.03, Cost: 0.05}, second.Trace.Mode.Tiers)
	})
	t.Run("a request that is never judged takes the balanced weight", func(t *testing.T) {
		result := resolve(t, 9636, autorouter.PreferenceAdaptive, "[SUGGESTION MODE: next prompt]")
		assert.Equal(t, autorouter.JudgmentAuxiliary, result.Trace.Judgment)
		require.NotNil(t, result.Trace.Mode)
		assert.Equal(t, autorouter.ModeNoAnswer, result.Trace.Mode.Reason)
		assert.Equal(t, 0.07, result.Trace.Mode.Weight)
	})
}

func TestResolveAutoModelRemembersJevFailureBriefly(t *testing.T) {
	var calls atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	useAutoRouterSetting(t, failing.URL, 1000)
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	first, err := ResolveAutoModel(newAutoRouterContext(9413, body), allModelsAvailable)
	require.NoError(t, err, "a jev failure must not fail the request")
	assert.Equal(t, "general-medium", first.ResolvedModel)
	require.NotNil(t, first.Jev)
	assert.NotEmpty(t, first.Jev.Error)

	second, err := ResolveAutoModel(newAutoRouterContext(9413, body), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "general-medium", second.ResolvedModel)
	assert.Nil(t, second.Jev, "刚失败过的指令不再调 jev")
	assert.Equal(t, int32(1), calls.Load())
}

func TestResolveAutoModelConvergesEffortForDeclaredReasoningModels(t *testing.T) {
	setupReasoningCapabilities(t)
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	setting := autorouter_setting.GetAutoRouterSetting()
	setting.TaskModels = map[string][]string{"code.build": {"reasoner"}, "general.chat": {"no-think"}}
	setting.DefaultModels = []string{"reasoner"}
	withChannels := func(channels ...int) AutoCandidates {
		return AutoCandidates{Serves: func(string, bool) bool { return true }, Channels: func(string) []int { return channels }}
	}

	t.Run("a trusted decision converges the effort and the final filter judges it", func(t *testing.T) {
		// 客户端自己要 xhigh；请求带 tools，所以只有渠道 11、12 能服务（13 不支持 tools）。
		gate := &CapabilityGate{mode: CapabilityGateEnforce, needs: &capability.Needs{ToolsCount: 1, ReasoningEffort: "xhigh"}, modelVerdicts: map[string]capabilityVerdict{}}
		c := newAutoRouterContext(9701, `{"messages":[{"role":"user","content":"hard"}]}`)
		common.SetContextKey(c, constant.ContextKeyCapabilityGate, gate)

		result, err := ResolveAutoModel(c, withChannels(11, 12, 13))
		require.NoError(t, err)
		assert.Equal(t, "reasoner", result.ResolvedModel, "声明了推理的模型用裸模型名，档位不拼后缀")
		assert.True(t, result.EffortInBody)
		assert.True(t, result.EffortOverride)
		assert.Equal(t, "xhigh", result.Effort, "难度给出的期望档位")
		assert.Equal(t, "high", result.AppliedEffort, "渠道 11、12 声明过的档位最高到 high")
		assert.Equal(t, autorouter.EffortSet, result.EffortAction)
		assert.Equal(t, []int{12}, gate.FilterChannels(&gin.Context{}, "reasoner", []int{11, 12, 13}),
			"最终的渠道过滤按写进请求体的 high 判定：渠道 11 没声明 high")
	})

	t.Run("a model that does not support reasoning has the client's fields stripped", func(t *testing.T) {
		result, err := ResolveAutoModel(newAutoRouterContext(9702, `{"messages":[{"role":"user","content":"what is 2+2"}]}`), withChannels(15))
		require.NoError(t, err)
		assert.Equal(t, "no-think", result.ResolvedModel)
		assert.Equal(t, autorouter.EffortStrip, result.EffortAction)
		assert.Empty(t, result.AppliedEffort)
	})

	t.Run("an untrusted fallback leaves the client's reasoning fields alone", func(t *testing.T) {
		result, err := ResolveAutoModel(newAutoRouterContext(9703, `{"messages":[{"role":"user","content":"ok"}]}`), withChannels(11, 12))
		require.NoError(t, err)
		assert.Equal(t, "reasoner", result.ResolvedModel)
		assert.True(t, result.EffortInBody)
		assert.False(t, result.EffortOverride)
		assert.Equal(t, autorouter.EffortKeep, result.EffortAction)
		assert.Empty(t, result.AppliedEffort)
	})

	t.Run("a model without a reasoning declaration keeps the model-name suffix", func(t *testing.T) {
		setting.TaskModels = map[string][]string{"code.build": {"gated-model"}}
		result, err := ResolveAutoModel(newAutoRouterContext(9704, `{"messages":[{"role":"user","content":"hard"}]}`), withChannels(1))
		require.NoError(t, err)
		assert.Equal(t, "gated-model-xhigh", result.ResolvedModel)
		assert.False(t, result.EffortInBody)
		assert.Empty(t, result.EffortAction)
	})
}

func TestResolveAutoModelAuxiliarySkipsJev(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

	result, err := ResolveAutoModel(newAutoRouterContext(9501, `{"messages":[{"role":"user","content":"[SUGGESTION MODE: next prompt]"}]}`), allModelsAvailable)
	require.NoError(t, err)
	assert.Equal(t, "general-medium", result.ResolvedModel)
	assert.Equal(t, autorouter.SourceDefault, result.Source)
	assert.Equal(t, int32(0), calls.Load())
}

func TestResolveAutoModelTraceSpans(t *testing.T) {
	newTracedContext := func(t *testing.T, tokenID int, body string) (*gin.Context, *tracetest.InMemoryExporter) {
		exporter := tracetest.NewInMemoryExporter()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		c := newAutoRouterContext(tokenID, body)
		c.Request = c.Request.WithContext(observability.ContextWithRuntimeForTest(c.Request.Context(), provider))
		return c, exporter
	}
	spansByName := func(exporter *tracetest.InMemoryExporter) map[string]tracetest.SpanStub {
		spans := map[string]tracetest.SpanStub{}
		for _, span := range exporter.GetSpans() {
			spans[span.Name] = span
		}
		return spans
	}
	attr := func(span tracetest.SpanStub, key string) string {
		for _, kv := range span.Attributes {
			if string(kv.Key) == key {
				return kv.Value.AsString()
			}
		}
		return ""
	}
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	t.Run("jev called then cache hit", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

		c, exporter := newTracedContext(t, 9601, body)
		_, err := ResolveAutoModel(c, allModelsAvailable)
		require.NoError(t, err)
		spans := spansByName(exporter)
		require.Len(t, spans, 2)
		route, jev := spans["newapi.auto_router"], spans["newapi.jev.classify"]
		assert.Equal(t, route.SpanContext.SpanID(), jev.Parent.SpanID())
		assert.Equal(t, "generation", attr(jev, "langfuse.observation.type"))
		assert.Contains(t, attr(jev, "langfuse.observation.input"), `"current_task":"hard"`)
		assert.Contains(t, attr(jev, "langfuse.observation.output"), `"task_type"`)
		assert.Equal(t, autorouter.SourceJev, attr(route, "langfuse.observation.metadata.source"))
		assert.Equal(t, "coder-xhigh", attr(route, "langfuse.observation.metadata.resolved_model"))
		assert.Equal(t, "coder", attr(route, "langfuse.trace.metadata.auto_router_model"))
		assert.Equal(t, "xhigh", attr(route, "langfuse.trace.metadata.auto_router_effort"))
		assert.Equal(t, "code.build", attr(route, "langfuse.observation.metadata.route_profile"))
		assert.Equal(t, "leaf", attr(route, "langfuse.trace.metadata.auto_router_fallback"))
		assert.Empty(t, attr(route, "langfuse.observation.level"))

		c, exporter = newTracedContext(t, 9601, body)
		_, err = ResolveAutoModel(c, allModelsAvailable)
		require.NoError(t, err)
		spans = spansByName(exporter)
		require.Len(t, spans, 1, "a cache hit must not record a jev call")
		assert.Equal(t, autorouter.SourceCache, attr(spans["newapi.auto_router"], "langfuse.observation.metadata.source"))
	})

	t.Run("jev timeout", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 200*time.Millisecond).URL, 50)

		c, exporter := newTracedContext(t, 9602, body)
		_, err := ResolveAutoModel(c, allModelsAvailable)
		require.NoError(t, err)
		spans := spansByName(exporter)
		require.Len(t, spans, 2)
		assert.Equal(t, codes.Error, spans["newapi.jev.classify"].Status.Code)
		route := spans["newapi.auto_router"]
		assert.Equal(t, "WARNING", attr(route, "langfuse.observation.level"))
		assert.Contains(t, attr(route, "langfuse.observation.status_message"), "jev failed")
	})

	t.Run("no available model", func(t *testing.T) {
		useAutoRouterSetting(t, "", 1000)

		c, exporter := newTracedContext(t, 9603, `{"messages":[{"role":"user","content":"[SUGGESTION MODE: next prompt]"}]}`)
		_, err := ResolveAutoModel(c, candidatesFrom(func(string) bool { return false }))
		require.Error(t, err)
		spans := spansByName(exporter)
		require.Len(t, spans, 1)
		route := spans["newapi.auto_router"]
		assert.Equal(t, codes.Error, route.Status.Code)
		assert.JSONEq(t, `{"auxiliary":true}`, attr(route, "langfuse.observation.input"))
	})
}

// ResolveAutoModel 把选路是怎么走到最后那个模型的记在 result.Trace 里：指令长什么样、
// 判断从哪来、各榜单试了什么。留痕只给选路日志用，不进消费日志。
func TestResolveAutoModelTrace(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hard"}]}`

	t.Run("a live judgment, then the cached one", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

		first, err := ResolveAutoModel(newAutoRouterContext(9901, body), allModelsAvailable)
		require.NoError(t, err)
		trace := first.Trace
		require.NotNil(t, trace)
		assert.Equal(t, autorouter.JudgmentLive, trace.Judgment)
		assert.Equal(t, autorouter.RequestTrace{Protocol: autorouter.ProtocolChat, HumanTurns: 1, TextRunes: 4}, trace.Request)
		assert.Equal(t, []string{"code.build", "code"}, trace.Trust.Profiles)
		assert.Empty(t, trace.Trust.Reason)
		assert.Equal(t, "xhigh", trace.Effort.Base)
		assert.Equal(t, []autorouter.LadderStep{{Profile: "code.build", Models: 1, Outcome: autorouter.OutcomeFirstAvailable, Model: "coder"}}, trace.Ladder)

		second, err := ResolveAutoModel(newAutoRouterContext(9901, body), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentCache, second.Trace.Judgment)
		assert.Equal(t, trace.Ladder, second.Trace.Ladder, "同一个判断、同样的可用集合，走出同样的榜单")
	})

	t.Run("a jev failure, then the failure marker", func(t *testing.T) {
		failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(failing.Close)
		useAutoRouterSetting(t, failing.URL, 1000)

		first, err := ResolveAutoModel(newAutoRouterContext(9902, body), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentLiveFailed, first.Trace.Judgment)
		assert.Equal(t, autorouter.TrustNoDecision, first.Trace.Trust.Reason)
		assert.Equal(t, []autorouter.LadderStep{{Profile: autorouter.DefaultProfile, Models: 1, Outcome: autorouter.OutcomeFirstAvailable, Model: "general"}}, first.Trace.Ladder)

		second, err := ResolveAutoModel(newAutoRouterContext(9902, body), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentRecentFailure, second.Trace.Judgment)
	})

	t.Run("requests that are never classified say so", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)

		auxiliary, err := ResolveAutoModel(newAutoRouterContext(9903, `{"messages":[{"role":"user","content":"[SUGGESTION MODE: next prompt]"}]}`), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentAuxiliary, auxiliary.Trace.Judgment)
		assert.True(t, auxiliary.Trace.Request.Auxiliary)
		assert.Equal(t, autorouter.TrustNoDecision, auxiliary.Trace.Trust.Reason)

		empty, err := ResolveAutoModel(newAutoRouterContext(9904, `{"messages":[{"role":"assistant","content":"hi"}]}`), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentNoInstruction, empty.Trace.Judgment)
		assert.Zero(t, empty.Trace.Request.TextRunes)
		assert.Equal(t, int32(0), calls.Load(), "没有可分类的指令就不调 jev")
	})

	t.Run("a sub-agent task without text follows its session", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		_, err := ResolveAutoModel(newAutoRouterContext(9905, `{"client_metadata":{"session_id":"sess-t"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hard"}]}]}`), allModelsAvailable)
		require.NoError(t, err)

		known, err := ResolveAutoModel(newAutoRouterContext(9905, encryptedSubAgentBody("sess-t")), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentSession, known.Trace.Judgment)
		assert.True(t, known.Trace.Request.Session)
		assert.Equal(t, []autorouter.LadderStep{{Profile: "code.build", Outcome: autorouter.OutcomeInherited, Model: "coder"}}, known.Trace.Ladder)

		unknown, err := ResolveAutoModel(newAutoRouterContext(9905, encryptedSubAgentBody("sess-none")), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, autorouter.JudgmentSession, unknown.Trace.Judgment)
		assert.Equal(t, []autorouter.LadderStep{{Profile: autorouter.DefaultProfile, Models: 1, Outcome: autorouter.OutcomeFirstAvailable, Model: "general"}}, unknown.Trace.Ladder,
			"会话里没有记录就走兜底")
	})

	t.Run("nothing routable counts the models by reason and does not call jev", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		reasons := map[string]string{"general": autorouter.ReasonGroupDisabled, "chat": autorouter.ReasonNoChannel, "coder": autorouter.ReasonNoChannel}
		candidates := candidatesFrom(func(string) bool { return false })
		candidates.Why = func(name string) string { return reasons[name] }

		result, err := ResolveAutoModel(newAutoRouterContext(9906, body), candidates)
		require.Error(t, err)
		require.NotNil(t, result.Trace, "失败的请求也带着留痕，选路日志要记它")
		assert.Equal(t, autorouter.JudgmentUnroutable, result.Trace.Judgment)
		assert.Nil(t, result.Trace.Trust)
		assert.Empty(t, result.Trace.Ladder, "兜底那一次不再写进留痕")
		assert.Equal(t, &autorouter.Unroutable{Reasons: []autorouter.ReasonCount{
			{Reason: autorouter.ReasonNoChannel, Count: 2, Examples: []string{"coder", "chat"}},
			{Reason: autorouter.ReasonGroupDisabled, Count: 1, Examples: []string{"general"}},
		}}, result.Trace.Unroutable)
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("a failed route keeps the first attempt instead of the fallback's", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		// "ok" 分不出任务，判断不可信；客户端原样的请求没有任何模型能服务，但撇开推理字段后有。
		candidates := AutoCandidates{
			Serves:   func(_ string, ignoreClientReasoning bool) bool { return ignoreClientReasoning },
			Channels: func(string) []int { return nil },
		}

		result, err := ResolveAutoModel(newAutoRouterContext(9907, `{"messages":[{"role":"user","content":"ok"}]}`), candidates)
		require.Error(t, err)
		assert.Equal(t, autorouter.JudgmentLive, result.Trace.Judgment)
		require.Len(t, result.Trace.Ladder, 1, "兜底重试一次不再追加第二步")
		assert.Equal(t, autorouter.OutcomeNone, result.Trace.Ladder[0].Outcome)
	})

	t.Run("truncation is flagged for the current and the previous instruction separately", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		setting := autorouter_setting.GetAutoRouterSetting()
		setting.MaxRunes, setting.PreviousMaxRunes = 10, 5

		long, err := ResolveAutoModel(newAutoRouterContext(9908, `{"messages":[{"role":"user","content":"这是一段超过十个字的很长的指令内容"}]}`), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, 17, long.Trace.Request.TextRunes, "按字符数而不是字节数")
		assert.True(t, long.Trace.Request.Truncated)

		short, err := ResolveAutoModel(newAutoRouterContext(9909, body), allModelsAvailable)
		require.NoError(t, err)
		assert.False(t, short.Trace.Request.Truncated)

		withPrevious, err := ResolveAutoModel(newAutoRouterContext(9910, `{"messages":[
			{"role":"user","content":"previous task text"},{"role":"assistant","content":"done"},{"role":"user","content":"ok"}]}`), allModelsAvailable)
		require.NoError(t, err)
		assert.Equal(t, 18, withPrevious.Trace.Request.PreviousRunes)
		assert.Equal(t, 2, withPrevious.Trace.Request.HumanTurns)
		assert.True(t, withPrevious.Trace.Request.Truncated, "上一句被截断也算")
	})

	t.Run("the protocol follows the endpoint the request came in on", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		for path, want := range map[string]autorouter.Protocol{
			"/v1/chat/completions": autorouter.ProtocolChat,
			"/v1/messages":         autorouter.ProtocolAnthropic,
			"/v1/responses":        autorouter.ProtocolResponses,
		} {
			c := newAutoRouterContext(9911, body)
			c.Request.URL.Path = path
			result, err := ResolveAutoModel(c, allModelsAvailable)
			require.NoError(t, err)
			assert.Equal(t, want, result.Trace.Request.Protocol, path)
		}
	})

	t.Run("the consume log's summary leaves the trace out", func(t *testing.T) {
		var calls atomic.Int32
		useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
		setting := autorouter_setting.GetAutoRouterSetting()
		setting.TaskModels = map[string][]string{"code.build": {"opus-m", "sonnet-m"}}
		setting.TaskScores = map[string]map[string]float64{"code.build": {"opus-m": 0.97, "sonnet-m": 0.96}}
		// 模型名带不带档位后缀都要判不可用，否则 opus-m-medium 会让 opus-m 仍然算可用。
		candidates := candidatesFrom(func(model string) bool { return !strings.HasPrefix(model, "opus-m") })
		candidates.Why = func(string) string { return autorouter.ReasonCapability }
		candidates.Price = func(string) (float64, bool) { return 4, true }

		result, err := ResolveAutoModel(newAutoRouterContext(9912, body), candidates)
		require.NoError(t, err)
		require.NotNil(t, result.Pick)
		require.Len(t, result.Trace.Ladder, 1)
		assert.Equal(t, []autorouter.Skipped{{Model: "opus-m", Score: 0.97, Reason: autorouter.ReasonCapability}}, result.Trace.Ladder[0].Skipped)

		summary, err := common.Marshal(result)
		require.NoError(t, err)
		assert.Contains(t, string(summary), `"pick"`)
		for _, leaked := range []string{`"trace"`, `"ladder"`, `"skipped"`, `"unroutable"`} {
			assert.NotContains(t, string(summary), leaked)
		}
	})
}

// captureAutoRouteLogs 把进程级选路日志 writer 换成录制用的（批大小 1，每条单独交付）。
// 返回的函数停掉 writer，按写入顺序给出收到的全部日志，只能调用一次。
func captureAutoRouteLogs(t *testing.T) func() []model.AutoRouteLog {
	t.Helper()
	batches := make(chan []model.AutoRouteLog, 64)
	writer := newEventWriter("auto route log", 64, 1, time.Hour, func(batch []*model.AutoRouteLog) error {
		delivered := make([]model.AutoRouteLog, 0, len(batch))
		for _, entry := range batch {
			delivered = append(delivered, *entry)
		}
		batches <- delivered
		return nil
	})
	autoRouteLogs.Store(writer)
	t.Cleanup(func() { autoRouteLogs.Store(nil) })
	return func() []model.AutoRouteLog {
		autoRouteLogs.Store(nil)
		writer.shutdown(time.Second)
		close(batches)
		var logs []model.AutoRouteLog
		for batch := range batches {
			logs = append(logs, batch...)
		}
		return logs
	}
}

// 选路日志按 route_log_mode 决定记哪些请求：decisions 只记做了决定的（现调 jev，或者找不到任何模型），
// 命中判断缓存、辅助请求这类重复的只有 all 才记。
func TestResolveAutoModelRouteLogModes(t *testing.T) {
	tests := []struct {
		mode string
		want []string // 每条日志的 outcome/judgment
	}{
		{autorouter_setting.RouteLogOff, nil},
		{autorouter_setting.RouteLogDecisions, []string{"routed/live", "failed/unroutable"}},
		{autorouter_setting.RouteLogAll, []string{"routed/live", "routed/cache", "routed/auxiliary", "failed/unroutable"}},
		{"not-a-mode", nil},
	}
	for index, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			var calls atomic.Int32
			useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
			autorouter_setting.GetAutoRouterSetting().RouteLogMode = tt.mode
			collect := captureAutoRouteLogs(t)
			tokenID := 9950 + index
			hard := `{"messages":[{"role":"user","content":"hard"}]}`

			for _, body := range []string{hard, hard, `{"messages":[{"role":"user","content":"[SUGGESTION MODE: next prompt]"}]}`} {
				_, err := ResolveAutoModel(newAutoRouterContext(tokenID, body), allModelsAvailable)
				require.NoError(t, err)
			}
			_, err := ResolveAutoModel(newAutoRouterContext(tokenID, hard), candidatesFrom(func(string) bool { return false }))
			require.Error(t, err)

			var got []string
			for _, entry := range collect() {
				got = append(got, entry.Outcome+"/"+entry.Judgment)
			}
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("a failed jev call is a decision, the failure marker after it is not", func(t *testing.T) {
		failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(failing.Close)
		useAutoRouterSetting(t, failing.URL, 1000)
		autorouter_setting.GetAutoRouterSetting().RouteLogMode = autorouter_setting.RouteLogDecisions
		collect := captureAutoRouteLogs(t)

		for range 2 {
			_, err := ResolveAutoModel(newAutoRouterContext(9959, `{"messages":[{"role":"user","content":"hard"}]}`), allModelsAvailable)
			require.NoError(t, err)
		}

		var got []string
		for _, entry := range collect() {
			got = append(got, entry.Outcome+"/"+entry.Judgment)
		}
		assert.Equal(t, []string{"routed/live_failed"}, got)
	})
}

func TestAutoRouteLogRowCarriesTheRequestAndTheOutcome(t *testing.T) {
	var calls atomic.Int32
	useAutoRouterSetting(t, fakeJev(t, &calls, 0).URL, 1000)
	collect := captureAutoRouteLogs(t)
	newContext := func(tokenID int, requestID, body string) *gin.Context {
		c := newAutoRouterContext(tokenID, body)
		c.Set(common.RequestIdKey, requestID)
		common.SetContextKey(c, constant.ContextKeyUserId, 42)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "vip")
		return c
	}
	_, err := ResolveAutoModel(newContext(9960, "req-routed", `{"messages":[{"role":"user","content":"hard"}]}`), allModelsAvailable)
	require.NoError(t, err)
	_, err = ResolveAutoModel(newContext(9960, "req-failed", `{"messages":[{"role":"user","content":"hard"}]}`), candidatesFrom(func(string) bool { return false }))
	require.Error(t, err)

	logs := collect()
	require.Len(t, logs, 2)
	routed, failed := logs[0], logs[1]
	assert.Equal(t, model.AutoRouteLog{
		CreatedAt: routed.CreatedAt, RequestId: "req-routed", UserId: 42, TokenId: 9960, UsingGroup: "vip",
		Outcome: model.AutoRouteOutcomeRouted, Judgment: autorouter.JudgmentLive, Source: autorouter.SourceJev,
		TaskType: "code.build", Model: "coder", Effort: "xhigh", Preference: autorouter.PreferenceBalanced,
		JevLatencyMs: routed.JevLatencyMs, Detail: routed.Detail,
	}, routed)
	assert.NotZero(t, routed.CreatedAt)

	var detail map[string]any
	require.NoError(t, common.UnmarshalJsonStr(routed.Detail, &detail))
	assert.Equal(t, "coder-xhigh", detail["result"].(map[string]any)["resolved_model"])
	assert.Equal(t, autorouter.JudgmentLive, detail["trace"].(map[string]any)["judgment"])
	assert.NotContains(t, detail, "error")

	assert.Equal(t, model.AutoRouteOutcomeFailed, failed.Outcome)
	assert.Equal(t, "req-failed", failed.RequestId)
	assert.Equal(t, autorouter.JudgmentUnroutable, failed.Judgment)
	assert.Empty(t, failed.Model, "没选出模型就没有模型和档位")
	assert.Empty(t, failed.Effort)
	assert.Contains(t, failed.Detail, "no available model")
}

func TestNewAutoRouteLogDerivesEffortLatencyAndColumnWidths(t *testing.T) {
	route := func(model, resolved string) autorouter.Result {
		return autorouter.Result{Route: autorouter.Route{Model: model}, ResolvedModel: resolved, Trace: &autorouter.Trace{}}
	}
	inBody := route("reasoner", "reasoner")
	inBody.EffortInBody, inBody.AppliedEffort = true, "high"
	clientOwn := route("reasoner", "reasoner")
	clientOwn.EffortInBody = true
	tests := []struct {
		name   string
		result autorouter.Result
		want   string
	}{
		{"effort written into the body", inBody, "high"},
		{"the client's own effort is kept", clientOwn, ""},
		{"effort in the model name", route("coder", "coder-high"), "high"},
		{"bare model runs at the provider's default", route("coder", "coder"), ""},
	}
	c := newAutoRouterContext(9961, "{}")
	for _, tt := range tests {
		assert.Equal(t, tt.want, newAutoRouteLog(c, &tt.result, nil).Effort, tt.name)
	}

	calledJev := route("coder", "coder")
	calledJev.Jev = &autorouter.JevCall{LatencyMs: 37}
	assert.Equal(t, int64(37), newAutoRouteLog(c, &calledJev, nil).JevLatencyMs)
	assert.Zero(t, newAutoRouteLog(c, &inBody, nil).JevLatencyMs, "没调 jev 的请求没有延迟")

	long := route(strings.Repeat("长", 300), "x")
	long.Decision = &autorouter.Decision{TaskType: strings.Repeat("t", 100)}
	entry := newAutoRouteLog(c, &long, nil)
	assert.Len(t, []rune(entry.Model), 255, "超出列宽的值要截断，否则整批 INSERT 会失败")
	assert.Len(t, entry.TaskType, 64)
}

// 详情超过上限时依次去掉最占地方的部分：比较候选带、整个路由结果，最后只剩一个空对象。
func TestEncodeAutoRouteDetailShrinksToFit(t *testing.T) {
	band := make([]autorouter.Candidate, 2000)
	probabilities := make(map[string]float64, 4000)
	ladder := make([]autorouter.LadderStep, 1500)
	for i := range band {
		band[i] = autorouter.Candidate{Model: "model-" + strconv.Itoa(i), Score: 0.9, Price: 1}
	}
	for i := range 4000 {
		probabilities["task-"+strconv.Itoa(i)] = 0.5
	}
	for i := range ladder {
		ladder[i] = autorouter.LadderStep{Profile: "general", Outcome: autorouter.OutcomeNone}
	}
	build := func(pick *autorouter.Pick, decision *autorouter.Decision, steps []autorouter.LadderStep) *autorouter.Result {
		return &autorouter.Result{
			Route: autorouter.Route{Model: "coder"}, Pick: pick, Decision: decision,
			Trace: &autorouter.Trace{Judgment: autorouter.JudgmentLive, Ladder: steps},
		}
	}
	tests := []struct {
		name       string
		result     *autorouter.Result
		wantResult bool
		wantPick   bool
		wantTrace  bool
	}{
		{"fits as is", build(&autorouter.Pick{Chosen: "coder", Band: band[:3]}, nil, nil), true, true, true},
		{"the comparison band goes first", build(&autorouter.Pick{Chosen: "coder", Band: band}, nil, nil), true, false, true},
		{"then the route result", build(nil, &autorouter.Decision{TaskTypeProbabilities: probabilities}, nil), false, false, true},
		{"nothing fits", build(nil, nil, ladder), false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeAutoRouteDetail(tt.result, errors.New("no model"))

			assert.LessOrEqual(t, len(encoded), model.AutoRouteLogDetailMax)
			var detail map[string]any
			require.NoError(t, common.UnmarshalJsonStr(encoded, &detail))
			assert.Equal(t, tt.wantResult, detail["result"] != nil, "result")
			assert.Equal(t, tt.wantTrace, detail["trace"] != nil, "trace")
			if tt.wantResult {
				assert.Equal(t, tt.wantPick, detail["result"].(map[string]any)["pick"] != nil, "pick")
			}
			if tt.wantTrace {
				assert.Equal(t, "no model", detail["error"])
			}
		})
	}
}

func TestAutoRouterListedOnlyWhenResolvable(t *testing.T) {
	useAutoRouterSetting(t, "", 1000)

	assert.True(t, AutoRouterListed([]string{"other", "coder-high"}), "an effort variant makes its model resolvable")
	assert.True(t, AutoRouterListed([]string{"general"}))
	assert.False(t, AutoRouterListed([]string{"other", "coder-turbo"}))
	assert.False(t, AutoRouterListed([]string{"auto", "chat"}), "a real auto model is listed already")

	autorouter_setting.GetAutoRouterSetting().Enabled = false
	assert.False(t, AutoRouterListed([]string{"chat"}))
}
