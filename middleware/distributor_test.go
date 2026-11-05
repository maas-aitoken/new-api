package middleware

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelMatchesExpectedTaskPluginUsesGenericChannelSetting(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "generic-alpha"})

	assert.True(t, channelMatchesExpectedTaskPlugin(nil, channel, "generic-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, channel, "generic-beta"))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, channel, ""))
}

func TestChannelMatchesExpectedTaskPluginUsesPinnedLegacyIndex(t *testing.T) {
	registry := jsplugin.NewRegistry()
	alpha, err := registry.Register(distributorTaskPluginSource("legacy-alpha", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)
	pinnedGeneration := registry.Generation()

	require.NoError(t, registry.Unregister("legacy-alpha"))
	_, err = registry.Register(distributorTaskPluginSource("legacy-beta", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{
		Generation: pinnedGeneration,
		Plugin:     alpha,
	})
	channel := &model.Channel{Type: constant.ChannelTypeKling}

	assert.True(t, channelMatchesExpectedTaskPlugin(c, channel, "legacy-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, channel, "legacy-beta"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "legacy-alpha"))
}

func TestChannelMatchesExpectedTaskPluginRejectsUnindexedLegacyChannel(t *testing.T) {
	registry := jsplugin.NewRegistry()
	plugin, err := registry.Register(distributorTaskPluginSource("legacy-alpha", constant.ChannelTypeKling), jsplugin.Options{})
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{
		Generation: registry.Generation(),
		Plugin:     plugin,
	})

	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "legacy-alpha"))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: 0}, "legacy-alpha"))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeJimeng}, ""))
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, &model.Channel{Type: constant.ChannelTypeKling}, "legacy-alpha"))

	c.Set("expected_task_plugin_key", "legacy-alpha")
	setupErr := SetupContextForSelectedChannel(c, &model.Channel{Type: constant.ChannelTypeJimeng}, "task-model")
	require.NotNil(t, setupErr)
	assert.Contains(t, setupErr.Error(), "does not match")
}

func TestSharedEndpointRebindsToSelectedLegacyProvider(t *testing.T) {
	registry := jsplugin.NewRegistry()
	_, err := registry.Register(distributorEndpointPluginSource("gemini-shared", constant.ChannelTypeGemini), jsplugin.Options{})
	require.NoError(t, err)
	_, err = registry.Register(distributorEndpointPluginSource("vertex-shared", constant.ChannelTypeVertexAi), jsplugin.Options{})
	require.NoError(t, err)
	candidates := registry.Generation().LookupEndpointCandidates("POST", "/v1/responses", "task-model")
	require.Len(t, candidates, 2)

	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: registry.Generation(), Plugin: candidates[0].Plugin})
	c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{
		Generation: registry.Generation(),
		Plugin:     candidates[0].Plugin,
		Protocol:   candidates[0].Protocol,
		Operation:  candidates[0].Operation,
		Model:      "task-model",
		Candidates: candidates,
	})
	c.Set("expected_task_plugin_key", candidates[0].Plugin.Meta.Key)

	geminiChannel := &model.Channel{Id: 1, Type: constant.ChannelTypeGemini}
	vertexChannel := &model.Channel{Id: 2, Type: constant.ChannelTypeVertexAi}
	assert.True(t, channelMatchesExpectedTaskPlugin(c, geminiChannel, candidates[0].Plugin.Meta.Key))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, vertexChannel, candidates[0].Plugin.Meta.Key))
	assert.False(t, channelMatchesExpectedTaskPlugin(c, &model.Channel{Type: constant.ChannelTypeKling}, candidates[0].Plugin.Meta.Key))

	require.Nil(t, SetupContextForSelectedChannel(c, vertexChannel, "task-model"))
	pinnedValue, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	require.True(t, exists)
	pinned, ok := pinnedValue.(jsplugin.PinnedEndpoint)
	require.True(t, ok)
	assert.Equal(t, "vertex-shared", pinned.Plugin.Meta.Key)
	assert.Equal(t, "vertex-shared", c.GetString("expected_task_plugin_key"))
	assert.Equal(t, "vertex-shared", c.GetString("task_plugin_key"))
	assert.True(t, channelMatchesExpectedTaskPlugin(c, geminiChannel, "vertex-shared"), "a retry may select another declared provider")
}

func distributorTaskPluginSource(key string, channelType int) string {
	return fmt.Sprintf(`
export const meta = {
  apiVersion: 1,
  key: %q,
  name: %q,
  version: "1.0.0",
  author: {name: "Test"},
  channelTypes: [%d],
  models: ["task-model"],
  fetchMode: "per_task",
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {taskId: "task"}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
`, key, key, channelType)
}

func distributorEndpointPluginSource(key string, channelType int) string {
	return fmt.Sprintf(`
export const meta = {
  apiVersion: 1,
  key: %q,
  name: %q,
  version: "1.0.0",
  author: {name: "Test"},
  channelTypes: [%d],
  models: ["task-model"],
  fetchMode: "per_task",
  protocols: [{name: "openai_responses", supports: ["stream", "sync", "background"]}],
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {taskId: "task"}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export const protocols = {openai_responses: {
  decodeRequest: function(ctx) { return {kind: "submit", model: "task-model", requestBody: ctx.body.value}; },
  renderEvents: function() { return {events: [], state: null, done: false}; },
  renderFinal: function() { return {output: []}; },
}};
`, key, key, channelType)
}

func TestTokenModelLimitAllowsLegacyAliasAndModifierVariant(t *testing.T) {
	aliasOnly := map[string]bool{"claude-3-7-sonnet-thinking": true}
	assert.True(t, TokenModelLimitAllows(aliasOnly, "claude-3-7-sonnet-thinking"))
	assert.False(t, TokenModelLimitAllows(aliasOnly, "claude-3-7-sonnet"))

	baseOnly := map[string]bool{"claude-3-7-sonnet": true}
	assert.True(t, TokenModelLimitAllows(baseOnly, "claude-3-7-sonnet@thinking:on"))
	assert.True(t, TokenModelLimitAllows(baseOnly, "claude-3-7-sonnet-thinking"))

	wildcard := map[string]bool{"gemini-2.5-flash-thinking-*": true}
	assert.True(t, TokenModelLimitAllows(wildcard, "gemini-2.5-flash-thinking-8192"))
}

// /v1/tools/search and /v1/tools/fetch always route and bill by the DeepSeek
// routing names. A model named in the body is ignored: a chat model there
// would price the call at zero tokens.
func TestGetModelRequestAssignsDeepSeekToolRoutingNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ path, body, want string }{
		{"/v1/tools/search", `{"query":"deepseek","count":3}`, constant.DeepSeekSearchModel},
		{"/v1/tools/fetch", `{"url":"https://example.com/"}`, constant.DeepSeekFetchModel},
		{"/v1/tools/search", `{"query":"deepseek","model":"deepseek-flash"}`, constant.DeepSeekSearchModel},
		{"/v1/tools/fetch", `{"url":"https://example.com/","model":"deepseek-v4-pro"}`, constant.DeepSeekFetchModel},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		require.NoError(t, err, tc.body)
		assert.True(t, shouldSelectChannel, tc.body)
		assert.Equal(t, tc.want, modelRequest.Model, tc.body)
	}
}

func TestTokenModelLimitAllowsExemptAtNameByFullName(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := append([]string(nil), settings.ThinkingModelBlacklist...)
	t.Cleanup(func() { settings.ThinkingModelBlacklist = original })
	settings.ThinkingModelBlacklist = append(original, "re:.*@sha256:.*")

	fullOnly := map[string]bool{"opaque@sha256:deadbeef": true}
	assert.True(t, TokenModelLimitAllows(fullOnly, "opaque@sha256:deadbeef"))

	baseOnly := map[string]bool{"opaque": true}
	assert.False(t, TokenModelLimitAllows(baseOnly, "opaque@sha256:deadbeef"))
}

func TestDistributeHidesTaskPluginDetailsButLogsDiagnostics(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCacheEnabled })

	const group = "private-plugin-error-test-group"
	for _, locale := range []struct{ language, message string }{
		{"en", "No available channel for model task-model under group " + group + ": the model is claimed by a task plugin, which has no enabled channel serving it (distributor)"},
		{"zh-CN", "分组 " + group + " 下模型 task-model 无可用渠道：该模型由任务插件认领，但当前没有启用的渠道可服务此模型（distributor）"},
		{"zh-TW", "分組 " + group + " 下模型 task-model 無可用管道：該模型由任務插件認領，但目前沒有啟用的管道可服務此模型（distributor）"},
	} {
		for _, providerCount := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d_providers", locale.language, providerCount), func(t *testing.T) {
				registry := jsplugin.NewRegistry()
				keys := []string{"private-provider-alpha", "private-provider-beta"}[:providerCount]
				for index, key := range keys {
					_, err := registry.Register(distributorEndpointPluginSource(key, constant.ChannelTypeKling+index), jsplugin.Options{})
					require.NoError(t, err)
				}
				generation := registry.Generation()
				candidates := generation.LookupEndpointCandidates("POST", "/v1/responses", "task-model")
				require.Len(t, candidates, providerCount)

				var logs bytes.Buffer
				common.LogWriterMu.Lock()
				previousWriter := gin.DefaultErrorWriter
				gin.DefaultErrorWriter = &logs
				common.LogWriterMu.Unlock()
				t.Cleanup(func() {
					common.LogWriterMu.Lock()
					gin.DefaultErrorWriter = previousWriter
					common.LogWriterMu.Unlock()
				})

				router := gin.New()
				router.POST("/v1/responses", RequestId(), func(c *gin.Context) {
					common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
					c.Set("resolved_task_model", "task-model")
					c.Set("expected_task_plugin_key", keys[0])
					c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: candidates[0].Plugin})
					if providerCount > 1 {
						c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{
							Generation: generation, Plugin: candidates[0].Plugin,
							Protocol: candidates[0].Protocol, Operation: candidates[0].Operation,
							Model: "task-model", Candidates: candidates,
						})
					}
				}, Distribute(), func(c *gin.Context) {
					t.Error("unavailable requests must stop before the relay handler")
					c.Status(http.StatusNoContent)
				})
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				request.Header.Set("Accept-Language", locale.language)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)

				require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				requestID := recorder.Header().Get(common.RequestIdKey)
				require.NotEmpty(t, requestID)
				assert.JSONEq(t, fmt.Sprintf(`{"error":{"message":%q,"type":"new_api_error","code":"model_not_found"}}`,
					locale.message+" (request id: "+requestID+")"), recorder.Body.String())
				assert.NotContains(t, recorder.Body.String(), "disable or override")
				for _, key := range keys {
					assert.NotContains(t, recorder.Body.String(), key)
					assert.Contains(t, logs.String(), key)
				}
				assert.Contains(t, logs.String(), requestID)
				assert.Contains(t, logs.String(), `group="`+group+`"`)
				assert.Contains(t, logs.String(), `model="task-model"`)
				assert.Contains(t, logs.String(), "reason=no_eligible_channel")
			})
		}
	}
}

func TestNoAvailableChannelMessageWithoutPlugin(t *testing.T) {
	require.NoError(t, i18n.Init())
	plain, _ := gin.CreateTestContext(nil)
	plain.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	plain.Request.Header.Set("Accept-Language", "en")
	generic := noAvailableChannelMessage(plain, "default", "gpt-4o")
	assert.NotContains(t, generic, "task plugin")
	assert.Contains(t, generic, "gpt-4o")
}

func TestSharedEndpointRebindsToSelectedType61Plugin(t *testing.T) {
	registry := jsplugin.NewRegistry()
	for _, key := range []string{"alpha", "beta"} {
		source := strings.Replace(distributorEndpointPluginSource(key, 0), "channelTypes: [0],", "", 1)
		_, err := registry.Register(source, jsplugin.Options{})
		require.NoError(t, err)
	}
	generation := registry.Generation()
	candidates := generation.LookupEndpointCandidates("POST", "/v1/responses", "task-model")
	require.Len(t, candidates, 2)
	c, _ := gin.CreateTestContext(nil)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: candidates[0].Plugin})
	c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Generation: generation, Plugin: candidates[0].Plugin, Protocol: candidates[0].Protocol, Operation: candidates[0].Operation, Model: "task-model", Candidates: candidates})
	c.Set("expected_task_plugin_key", "alpha")
	channel := &model.Channel{Id: 2, Type: constant.ChannelTypeTaskPlugin}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "unrelated"})
	assert.False(t, channelMatchesExpectedTaskPlugin(c, channel, "alpha"))
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "beta"})
	require.Nil(t, SetupContextForSelectedChannel(c, channel, "task-model"))
	assert.Equal(t, "beta", c.GetString("task_plugin_key"))
	assert.Equal(t, "beta", c.GetString("expected_task_plugin_key"))
	assert.Equal(t, "beta", c.MustGet(jsplugin.ContextKeyPinnedEndpoint).(jsplugin.PinnedEndpoint).Plugin.Meta.Key)
}

func TestChannelMatchesExpectedTaskPluginUsesNewAPIExtensions(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeNewAPI}
	channel.SetSetting(dto.ChannelSettings{TaskPluginKey: "single", TaskExtendPluginKeys: []string{"ext-alpha", "ext-beta"}})

	assert.True(t, channelMatchesExpectedTaskPlugin(nil, channel, "ext-alpha"))
	assert.True(t, channelMatchesExpectedTaskPlugin(nil, channel, "single"), "the single key stays valid on a New API channel")
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, channel, "ext-gamma"))
	assert.True(t, channelMatchesExpectedTaskPlugin(nil, channel, ""), "requests without a pinned plugin still use the gateway")
	assert.False(t, channelMatchesExpectedTaskPlugin(nil, &model.Channel{Type: constant.ChannelTypeNewAPI}, "ext-alpha"))
}

func TestSharedEndpointRebindsToBoundNewAPIExtension(t *testing.T) {
	registry := jsplugin.NewRegistry()
	for _, key := range []string{"alpha", "beta"} {
		source := strings.Replace(distributorEndpointPluginSource(key, 0), "channelTypes: [0],", "", 1)
		_, err := registry.Register(source, jsplugin.Options{})
		require.NoError(t, err)
	}
	generation := registry.Generation()
	candidates := generation.LookupEndpointCandidates("POST", "/v1/responses", "task-model")
	require.Len(t, candidates, 2)
	pin := func(expected string) *gin.Context {
		pinned := candidates[0]
		if candidates[1].Plugin.Meta.Key == expected {
			pinned = candidates[1]
		}
		c, _ := gin.CreateTestContext(nil)
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: pinned.Plugin})
		c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Generation: generation, Plugin: pinned.Plugin, Protocol: pinned.Protocol, Operation: pinned.Operation, Model: "task-model", Candidates: candidates})
		c.Set("expected_task_plugin_key", expected)
		return c
	}

	channel := &model.Channel{Id: 3, Type: constant.ChannelTypeNewAPI}
	channel.SetSetting(dto.ChannelSettings{TaskExtendPluginKeys: []string{"unrelated"}})
	assert.False(t, channelMatchesExpectedTaskPlugin(pin("alpha"), channel, "alpha"))

	channel.SetSetting(dto.ChannelSettings{TaskExtendPluginKeys: []string{"beta"}})
	c := pin("alpha")
	require.Nil(t, SetupContextForSelectedChannel(c, channel, "task-model"))
	assert.Equal(t, "beta", c.GetString("task_plugin_key"), "the only bound candidate executes")
	assert.Equal(t, "beta", c.GetString("expected_task_plugin_key"))
	assert.Equal(t, "beta", c.MustGet(jsplugin.ContextKeyPinnedEndpoint).(jsplugin.PinnedEndpoint).Plugin.Meta.Key)

	channel.SetSetting(dto.ChannelSettings{TaskExtendPluginKeys: []string{"alpha", "beta"}})
	c = pin("beta")
	require.Nil(t, SetupContextForSelectedChannel(c, channel, "task-model"))
	assert.Equal(t, "alpha", c.GetString("task_plugin_key"), "the first bound candidate executes regardless of the earlier pin")
	assert.Equal(t, "alpha", c.MustGet(jsplugin.ContextKeyPinnedEndpoint).(jsplugin.PinnedEndpoint).Plugin.Meta.Key)
}

// setupAutoRoutingDistribute 建一个内存库，每个渠道服务 models 里的全部模型（分组 default），
// 并打开内存渠道缓存。auto 配置里不设 JEV_ENDPOINT，分类立即失败，
// 路由固定走 default_models 的第一个可用模型，不需要起 jev 桩。
func setupAutoRoutingDistribute(t *testing.T, defaultModels []string, channels map[int][]string) *gorm.DB {
	t.Helper()
	require.NoError(t, i18n.Init())

	previousDB := model.DB
	previousCache := common.MemoryCacheEnabled
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Channel{}, &model.Ability{}, &model.ChannelModelStatus{}, &model.ChannelModelCapability{}))
	model.DB = db
	common.MemoryCacheEnabled = true

	priority := int64(0)
	weight := uint(100)
	for channelID, models := range channels {
		require.NoError(t, db.Create(&model.Channel{
			Id: channelID, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", channelID),
			Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("channel-%d", channelID),
			Weight: &weight, Priority: &priority, Group: "default", Models: strings.Join(models, ","),
		}).Error)
		for _, modelName := range models {
			require.NoError(t, db.Create(&model.Ability{
				Group: "default", Model: modelName, ChannelId: channelID, Enabled: true, Priority: &priority, Weight: weight,
			}).Error)
		}
	}
	model.InitChannelCache()

	setting := autorouter_setting.GetAutoRouterSetting()
	saved := *setting
	t.Setenv("JEV_ENDPOINT", "")
	setting.Enabled = true
	setting.TaskModels = map[string][]string{}
	setting.DefaultModels = defaultModels
	setting.DefaultEffort = "medium"

	t.Cleanup(func() {
		*setting = saved
		model.DB = previousDB
		common.MemoryCacheEnabled = previousCache
		model.InvalidateCapabilityCache()
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

// serveAutoRoutingRequest 让请求经过真实的 Distribute，返回处理器看到的存储请求体。
func serveAutoRoutingRequest(t *testing.T, path string, tokenID int, body string) (stored string, originalModel string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	return serveAutoRoutingRequestIn(t, path, "default", tokenID, body)
}

// serveAutoRoutingRequestIn 同 serveAutoRoutingRequest，但令牌分组可指定（例如 "auto"）。
func serveAutoRoutingRequestIn(t *testing.T, path, usingGroup string, tokenID int, body string, setup ...func(*gin.Context)) (stored string, originalModel string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	router := gin.New()
	router.POST(path, RequestId(), func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, usingGroup)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
		for _, apply := range setup {
			apply(c)
		}
	}, Distribute(), func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		raw, err := storage.Bytes()
		require.NoError(t, err)
		stored = string(raw)
		originalModel = c.GetString("original_model")
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return stored, originalModel, recorder
}

// serveAutoRoutingResult 同 serveAutoRoutingRequestIn（聊天补全入口、default 分组），
// 另外返回 Distribute 存下的选路结果。
func serveAutoRoutingResult(t *testing.T, tokenID int, body string, setup ...func(*gin.Context)) (*autorouter.Result, string, *httptest.ResponseRecorder) {
	t.Helper()
	var result *autorouter.Result
	setup = append(setup, func(c *gin.Context) {
		// 结果是 Distribute 存的，要等整条链跑完才能读。
		c.Next()
		result = service.GetAutoRouterResult(c)
	})
	_, originalModel, recorder := serveAutoRoutingRequestIn(t, "/v1/chat/completions", "default", tokenID, body, setup...)
	return result, originalModel, recorder
}

func TestDistributeAutoRewritesStoredBodyModel(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"general"}, map[int][]string{1: {"general"}})
	useModelPrices(t, map[string]float64{"general": 1})

	stored, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9601,
		`{"model":"auto","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "general", originalModel)
	assert.JSONEq(t, `{"model":"general","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`, stored,
		"透传渠道原样转发存储的请求体，里面的 model 必须是解析后的模型而不是 auto")
}

func TestDistributeNonAutoRequestKeepsStoredBody(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"general"}, map[int][]string{1: {"general"}})
	body := `{"model":"general","messages":[{"role":"user","content":"hello"}]}`

	stored, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9602, body)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "general", originalModel)
	assert.Equal(t, body, stored)
}

// 调度器决定渠道时，客户端发带推理后缀的名字、渠道只列了基础名，allowed_channel_ids
// 也要有这个渠道（原生选路能选到它）；以前这里是空的，调度器只能回"无候选"，请求 503。
func TestDistributeEnforcedSchedulerScopesSuffixedModelsLikeNativeRouting(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"gpt-5"}, map[int][]string{1: {"gpt-5"}, 2: {"other"}})
	var allowed []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model             string `json:"model"`
			AllowedChannelIDs []int  `json:"allowed_channel_ids"`
		}
		assert.NoError(t, common.DecodeJson(r.Body, &request))
		allowed = request.AllowedChannelIDs
		if len(allowed) == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"no candidate endpoint"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"decision_id":"d","catalog_version":"v","candidates":[{"endpoint_id":"new-api-ch-%d","channel_id":%d,"model":%q,"reason":["available"]}]}`,
			allowed[0], allowed[0], request.Model)
	}))
	defer server.Close()
	service.ConfigureSchedulerClientForTest(service.SchedulerClientConfig{Enabled: true, Mode: "enforced", BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
	t.Cleanup(func() { service.ConfigureSchedulerClientForTest(service.SchedulerClientConfig{}) })

	for index, requested := range []string{"gpt-5-low", "gpt-5@effort:high", "gpt-5"} {
		allowed = nil
		capacityModel := ""
		_, originalModel, recorder := serveAutoRoutingRequestIn(t, "/v1/chat/completions", "default", 9650+index,
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, requested), func(c *gin.Context) {
				c.Next()
				capacityModel = common.GetContextKeyString(c, constant.ContextKeyChannelCapacityModel)
			})

		require.Equal(t, http.StatusNoContent, recorder.Code, "%s: %s", requested, recorder.Body.String())
		assert.Equal(t, requested, originalModel)
		assert.Equal(t, []int{1}, allowed, requested)
		// 中继按它算 <渠道, 上游模型> 计数池，与 Catalog 里这个渠道的池一致。
		assert.Equal(t, "gpt-5", capacityModel, requested)
	}
}

// auto 判断候选时强度变体只有渠道真的列了才算可用：渠道只列了 gpt-5 时选 gpt-5，
// 不会选 gpt-5-medium 再借后缀往上游加推理强度（模型不一定支持）。
func TestDistributeAutoOnlyResolvesListedEffortVariants(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"gpt-5"}, map[int][]string{1: {"gpt-5"}})
	useModelPrices(t, map[string]float64{"gpt-5": 1, "gpt-5-medium": 1})

	_, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9660,
		`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "gpt-5", originalModel)
}

// useEnforcedToolCapabilities 给 models 里每个模型声明"支持 tools"，并把门控切到 enforce。
// narrowed 里的 (渠道, 模型) 再被渠道收窄成不支持 tools。
func useEnforcedToolCapabilities(t *testing.T, db *gorm.DB, models []string, narrowed map[int]string) {
	t.Helper()
	for _, modelName := range models {
		require.NoError(t, db.Create(&model.Model{
			ModelName: modelName, NameRule: model.NameRuleExact, Status: 1,
			Capabilities: `{"tools":{"supported":true},"streaming":{"supported":true}}`,
		}).Error)
	}
	require.NoError(t, model.InitCapabilityCache())
	for channelID, modelName := range narrowed {
		require.NoError(t, model.SetChannelModelCapability(channelID, modelName, `{"tools":{"supported":false}}`))
	}

	gate := operation_setting.GetCapabilityGateSetting()
	previous := gate.Mode
	gate.Mode = operation_setting.CapabilityGateModeEnforce
	t.Cleanup(func() { gate.Mode = previous })
}

const autoRoutingToolsBody = `{"model":"auto","messages":[{"role":"user","content":"hello"}],` +
	`"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`

func TestDistributeAutoSkipsModelWhoseChannelsAreAllNarrowed(t *testing.T) {
	db := setupAutoRoutingDistribute(t, []string{"m-a", "m-b"}, map[int][]string{1: {"m-a"}, 2: {"m-b"}})
	useModelPrices(t, map[string]float64{"m-a": 1, "m-b": 1})
	useEnforcedToolCapabilities(t, db, []string{"m-a", "m-b"}, map[int]string{1: "m-a"})

	_, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9603, autoRoutingToolsBody)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "m-b", originalModel, "m-a 唯一的渠道把 tools 收窄掉了，auto 应该改选 m-b 而不是选中 m-a 再 503")
}

func TestDistributeAutoRejectsWhenEveryCandidateIsNarrowed(t *testing.T) {
	db := setupAutoRoutingDistribute(t, []string{"m-a", "m-b"}, map[int][]string{1: {"m-a"}, 2: {"m-b"}})
	useModelPrices(t, map[string]float64{"m-a": 1, "m-b": 1})
	useEnforcedToolCapabilities(t, db, []string{"m-a", "m-b"}, map[int]string{1: "m-a", 2: "m-b"})

	_, _, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9604, autoRoutingToolsBody)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "no auto candidate model satisfies the request")
	assert.Contains(t, recorder.Body.String(), "tools.supported")
}

// useReasoningModels 声明两个模型并把门控切到 enforce：
//   - reasoner：支持 tools 和推理，档位 none..xhigh。渠道 1 收窄到 low/medium，渠道 2 收窄到
//     low/medium/high，渠道 3 收窄成不支持 tools 且只留 xhigh。
//   - no-think：明确不支持推理，由渠道 4 服务。
func useReasoningModels(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "reasoner", NameRule: model.NameRuleExact, Status: 1,
			Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":true,"efforts":["none","low","medium","high","xhigh"]}}`},
		{ModelName: "no-think", NameRule: model.NameRuleExact, Status: 1, Capabilities: `{"reasoning":{"supported":false}}`},
	}).Error)
	require.NoError(t, model.InitCapabilityCache())
	for channelID, narrowing := range map[int]string{
		1: `{"reasoning":{"efforts":["low","medium"]}}`,
		2: `{"reasoning":{"efforts":["low","medium","high"]}}`,
		3: `{"tools":{"supported":false},"reasoning":{"efforts":["xhigh"]}}`,
	} {
		require.NoError(t, model.SetChannelModelCapability(channelID, "reasoner", narrowing))
	}
	gate := operation_setting.GetCapabilityGateSetting()
	previous := gate.Mode
	gate.Mode = operation_setting.CapabilityGateModeEnforce
	t.Cleanup(func() { gate.Mode = previous })
}

// fakeJevFor 返回一个把任务判成 taskType、难度 3（对应第 4 档 xhigh）的 jev 桩地址。
func fakeJevFor(t *testing.T, taskType string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{` +
			`"task_type":{"type":"choice","choice":"` + taskType + `","probabilities":{"` + taskType + `":0.9}},` +
			`"difficulty":{"type":"score","probabilities":{"3":0.8}},` +
			`"task_change":{"type":"noul","noul":0.1},` +
			`"larger_model_benefit":{"type":"noul","noul":0.1}}}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// 选路留痕里榜单靠前却没被选中的模型带着不能服务的原因，原因就是分发中间件判定候选的顺序：
// 令牌白名单、分组启用、渠道、价格、能力。这里开着内存缓存，一个分组里没登记的模型表现为没有渠道；
// 分组启用只在没开内存缓存时才单独判定，那条路径要真实的 model.InitDB 才有列名，这个内存库夹具走不了。
func TestDistributeAutoTraceExplainsWhySkippedModelsCouldNotServe(t *testing.T) {
	db := setupAutoRoutingDistribute(t, []string{"m-token", "m-nochannel", "m-unpriced", "m-capability", "m-ok"},
		map[int][]string{1: {"m-token", "m-unpriced", "m-capability", "m-ok"}})
	useModelPrices(t, map[string]float64{"m-token": 1, "m-nochannel": 1, "m-capability": 1, "m-ok": 1})
	useEnforcedToolCapabilities(t, db, []string{"m-token", "m-unpriced", "m-capability", "m-ok"}, map[int]string{1: "m-capability"})
	autorouter_setting.GetAutoRouterSetting().TaskScores = map[string]map[string]float64{autorouter.DefaultProfile: {
		"m-token": 0.95, "m-nochannel": 0.94, "m-unpriced": 0.93, "m-capability": 0.92, "m-ok": 0.91,
	}}

	result, originalModel, recorder := serveAutoRoutingResult(t, 9605, autoRoutingToolsBody, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"m-nochannel": true, "m-unpriced": true, "m-capability": true, "m-ok": true})
	})

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "m-ok", originalModel)
	require.NotNil(t, result)
	require.Len(t, result.Trace.Ladder, 1)
	assert.Equal(t, autorouter.OutcomeCompared, result.Trace.Ladder[0].Outcome)
	assert.Equal(t, 5, result.Trace.Ladder[0].Models)
	assert.Equal(t, []autorouter.Skipped{
		{Model: "m-token", Score: 0.95, Reason: autorouter.ReasonTokenLimit},
		{Model: "m-nochannel", Score: 0.94, Reason: autorouter.ReasonNoChannel},
		{Model: "m-unpriced", Score: 0.93, Reason: autorouter.ReasonUnpriced},
		{Model: "m-capability", Score: 0.92, Reason: autorouter.ReasonCapability},
	}, result.Trace.Ladder[0].Skipped)
}

// 客户端自己写的推理档位会让一个模型在"按原样的请求"下不可用，但这个档位 auto 能改写。
// 兜底路径不碰客户端字段，所以会跳过这个模型，留痕要把原因记成客户端的推理字段，
// 而不是能力不足：撇开推理字段后它是能服务的。
func TestDistributeAutoTraceBlamesTheClientReasoningFields(t *testing.T) {
	db := setupAutoRoutingDistribute(t, []string{"reasoner", "plain"}, map[int][]string{1: {"reasoner"}, 2: {"reasoner"}, 3: {"reasoner"}, 5: {"plain"}})
	// 没有声明的模型在 enforce 下一律不可用，所以 plain 也要声明；它的唯一渠道满足客户端的 tools + xhigh。
	require.NoError(t, db.Create(&model.Model{
		ModelName: "plain", NameRule: model.NameRuleExact, Status: 1,
		Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":true,"efforts":["none","low","medium","high","xhigh"]}}`,
	}).Error)
	useReasoningModels(t, db)
	useModelPrices(t, map[string]float64{"reasoner": 1, "plain": 1})
	autorouter_setting.GetAutoRouterSetting().TaskScores = map[string]map[string]float64{autorouter.DefaultProfile: {"reasoner": 0.95, "plain": 0.94}}
	// 渠道 1、2 最高声明到 high，渠道 3 不支持 tools：请求带 tools 又要 xhigh 时 reasoner 没有渠道能服务。
	body := `{"model":"auto","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"xhigh",` +
		`"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`

	result, originalModel, recorder := serveAutoRoutingResult(t, 9606, body)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "plain", originalModel)
	require.NotNil(t, result)
	require.Len(t, result.Trace.Ladder, 1)
	assert.Equal(t, []autorouter.Skipped{{Model: "reasoner", Score: 0.95, Reason: autorouter.ReasonClientReasoning}}, result.Trace.Ladder[0].Skipped)
}

func TestDistributeAutoWritesConvergedEffortIntoStoredBody(t *testing.T) {
	const (
		chatTool      = `{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}`
		anthropicTool = `{"name":"lookup","description":"d","input_schema":{"type":"object"}}`
		responsesTool = `{"type":"function","name":"lookup","parameters":{"type":"object"}}`
	)
	chatMessages := `[{"role":"user","content":"fix the bug"}]`
	tests := []struct {
		name      string
		path      string
		taskType  string // jev 判成的任务类型；空表示 jev 不可用，走兜底
		body      string
		wantModel string
		wantBody  string
	}{
		{
			// 请求带 tools，渠道 3 不支持 tools，剩下的渠道最高声明到 high；难度要 xhigh，收敛到 high。
			name: "chat: a trusted decision overrides the client's effort with the converged one",
			path: "/v1/chat/completions", taskType: "code.build", wantModel: "reasoner",
			body:     `{"model":"auto","messages":` + chatMessages + `,"reasoning_effort":"low","tools":[` + chatTool + `]}`,
			wantBody: `{"model":"reasoner","messages":` + chatMessages + `,"reasoning_effort":"high","tools":[` + chatTool + `]}`,
		},
		{
			name: "chat: a model that does not support reasoning has the client's fields stripped",
			path: "/v1/chat/completions", taskType: "general.chat", wantModel: "no-think",
			body:     `{"model":"auto","messages":` + chatMessages + `,"reasoning_effort":"high"}`,
			wantBody: `{"model":"no-think","messages":` + chatMessages + `}`,
		},
		{
			name: "chat: without a trusted decision the client's effort is kept",
			path: "/v1/chat/completions", wantModel: "reasoner",
			body:     `{"model":"auto","messages":` + chatMessages + `,"reasoning_effort":"low"}`,
			wantBody: `{"model":"reasoner","messages":` + chatMessages + `,"reasoning_effort":"low"}`,
		},
		{
			name: "anthropic: the thinking budget is replaced by output_config.effort",
			path: "/v1/messages", taskType: "code.build", wantModel: "reasoner",
			body: `{"model":"auto","max_tokens":4096,"messages":` + chatMessages +
				`,"thinking":{"type":"enabled","budget_tokens":2048},"tools":[` + anthropicTool + `]}`,
			wantBody: `{"model":"reasoner","max_tokens":4096,"messages":` + chatMessages +
				`,"output_config":{"effort":"high"},"tools":[` + anthropicTool + `]}`,
		},
		{
			name: "responses: reasoning.effort is converged and the summary is kept",
			path: "/v1/responses", taskType: "code.build", wantModel: "reasoner",
			body: `{"model":"auto","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]}],` +
				`"reasoning":{"effort":"low","summary":"auto"},"tools":[` + responsesTool + `]}`,
			wantBody: `{"model":"reasoner","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]}],` +
				`"reasoning":{"effort":"high","summary":"auto"},"tools":[` + responsesTool + `]}`,
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupAutoRoutingDistribute(t, []string{"reasoner"},
				map[int][]string{1: {"reasoner"}, 2: {"reasoner"}, 3: {"reasoner"}, 4: {"no-think"}})
			useReasoningModels(t, db)
			useModelPrices(t, map[string]float64{"reasoner": 1, "no-think": 1})
			autorouter_setting.GetAutoRouterSetting().TaskModels = map[string][]string{"code.build": {"reasoner"}, "general.chat": {"no-think"}}
			if tt.taskType != "" {
				t.Setenv("JEV_ENDPOINT", fakeJevFor(t, tt.taskType))
			}

			stored, originalModel, recorder := serveAutoRoutingRequest(t, tt.path, 9700+index, tt.body)

			require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
			assert.Equal(t, tt.wantModel, originalModel)
			assert.JSONEq(t, tt.wantBody, stored)
		})
	}
}

// useModelPrices 把模型倍率换成测试值：补全倍率统一为 1，混合价就等于输入价
// （倍率 1 是每百万 token 2 美元）。同时关掉自用模式，没配倍率的模型才是真的没价格。
func useModelPrices(t *testing.T, ratios map[string]float64) {
	t.Helper()
	savedRatios, savedCompletions := ratio_setting.ModelRatio2JSONString(), ratio_setting.CompletionRatio2JSONString()
	savedSelfUse := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() {
		operation_setting.SelfUseModeEnabled = savedSelfUse
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(savedCompletions))
	})
	currentRatios, currentCompletions := map[string]float64{}, map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(savedRatios, &currentRatios))
	require.NoError(t, common.UnmarshalJsonStr(savedCompletions, &currentCompletions))
	for modelName, ratio := range ratios {
		currentRatios[modelName], currentCompletions[modelName] = ratio, 1
	}
	encodedRatios, err := common.Marshal(currentRatios)
	require.NoError(t, err)
	encodedCompletions, err := common.Marshal(currentCompletions)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encodedRatios)))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(encodedCompletions)))
}

func TestDistributeAutoPicksByQualityAndUserPrice(t *testing.T) {
	prepare := func(t *testing.T, channels map[int][]string, defaults []string) {
		setupAutoRoutingDistribute(t, defaults, channels)
		// opus-x 每百万 token 8 美元，sonnet-x 4 美元；ghost-x 榜单分最高但没有配价格。
		useModelPrices(t, map[string]float64{"opus-x": 4, "sonnet-x": 2})
		s := autorouter_setting.GetAutoRouterSetting()
		s.TaskModels = map[string][]string{"code.build": {"ghost-x", "opus-x", "sonnet-x"}}
		s.TaskScores = map[string]map[string]float64{"code.build": {"ghost-x": 0.99, "opus-x": 0.97, "sonnet-x": 0.96}}
	}
	pool := map[int][]string{1: {"opus-x"}, 2: {"sonnet-x"}, 3: {"ghost-x"}}
	body := `{"model":"auto","messages":[{"role":"user","content":"fix the bug"}]}`

	t.Run("a trusted decision takes the cheaper of the close models and skips the unpriced one", func(t *testing.T) {
		prepare(t, pool, []string{"opus-x"})
		t.Setenv("JEV_ENDPOINT", fakeJevFor(t, "code.build"))
		_, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9801, body)
		require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
		assert.Equal(t, "sonnet-x", originalModel)
	})

	t.Run("a key that prefers capability takes the best-scored priced model", func(t *testing.T) {
		prepare(t, pool, []string{"opus-x"})
		t.Setenv("JEV_ENDPOINT", fakeJevFor(t, "code.build"))
		_, originalModel, recorder := serveAutoRoutingRequestIn(t, "/v1/chat/completions", "default", 9806, body,
			func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyTokenAutoPreference, autorouter.PreferenceCapability)
			})
		require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
		assert.Equal(t, "opus-x", originalModel, "同样的候选和价格，能力优先的 key 不拿便宜的 sonnet-x 换分数")
	})

	t.Run("the fallback never picks an unpriced model either", func(t *testing.T) {
		prepare(t, pool, []string{"ghost-x", "opus-x"})
		_, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9802, body)
		require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
		assert.Equal(t, "opus-x", originalModel)
	})

	t.Run("a pool of unpriced models is rejected up front", func(t *testing.T) {
		prepare(t, map[int][]string{3: {"ghost-x"}}, []string{"ghost-x"})
		_, _, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9803, body)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "no available model")
	})
}

// 令牌分组是 auto 时，计费分组是第一个对该模型有可用渠道的自动分组，倍率因模型而异。
// 两个模型基础价格相同、榜单分接近：服务在高倍率分组里的那个实际更贵，应该选另一个。
func TestDistributeAutoPricesCandidatesInTheirOwnBillingGroup(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"exp-x"}, nil)
	useModelPrices(t, map[string]float64{"exp-x": 2, "cheap-y": 2})

	savedAutoGroups, savedUsable, savedGroupRatios := setting.AutoGroups2JsonString(), setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	savedMaxAutoGroups, savedRetry := setting.GetMaxTokenAutoGroups(), common.RetryTimes
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(savedAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(savedUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", savedMaxAutoGroups)))
		common.RetryTimes = savedRetry
	})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["cheap","pricey"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","cheap":"Cheap","pricey":"Pricey"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"cheap":1,"pricey":4}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	common.RetryTimes = 0

	// exp-x 只有 pricey 分组有渠道（实际价格 4 倍），cheap-y 在 cheap 分组。
	priority, weight := int64(0), uint(100)
	for id, group := range map[int]string{1: "pricey", 2: "cheap"} {
		modelName := map[int]string{1: "exp-x", 2: "cheap-y"}[id]
		require.NoError(t, model.DB.Create(&model.Channel{
			Id: id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", id), Status: common.ChannelStatusEnabled,
			Name: fmt.Sprintf("channel-%d", id), Weight: &weight, Priority: &priority, Group: group, Models: modelName,
		}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{
			Group: group, Model: modelName, ChannelId: id, Enabled: true, Priority: &priority, Weight: weight,
		}).Error)
	}
	model.InitChannelCache()

	s := autorouter_setting.GetAutoRouterSetting()
	s.TaskModels = map[string][]string{"code.build": {"exp-x", "cheap-y"}}
	s.TaskScores = map[string]map[string]float64{"code.build": {"exp-x": 0.97, "cheap-y": 0.96}}
	t.Setenv("JEV_ENDPOINT", fakeJevFor(t, "code.build"))

	_, originalModel, recorder := serveAutoRoutingRequestIn(t, "/v1/chat/completions", "auto", 9804,
		`{"model":"auto","messages":[{"role":"user","content":"fix the bug"}]}`)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "cheap-y", originalModel, "exp-x 的计费分组倍率是 4，实际价格是 cheap-y 的 4 倍")
}

// useTieredExpressions 给 expressions 里的模型配上分档计费表达式。
func useTieredExpressions(t *testing.T, expressions map[string]string) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	modes := map[string]string{}
	for name := range expressions {
		modes[name] = billing_setting.BillingModeTieredExpr
	}
	encodedModes, err := common.Marshal(modes)
	require.NoError(t, err)
	encodedExpressions, err := common.Marshal(expressions)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(encodedModes),
		"billing_setting.billing_expr": string(encodedExpressions),
	}))
}

// 门控关闭（没有任何能力声明）时价格探测也要按候选自己的家族估算输入长度：
// 同一段文本在 claude 4.7 的 tokenizer 下更长，会落进另一个价格档。
// 原先价格探测的估算来自门控，门控为 nil 时恒为 0，所有候选都按最小档估价。
func TestDistributeAutoPricesTieredModelsAtTheirOwnEstimatedLength(t *testing.T) {
	setupAutoRoutingDistribute(t, []string{"claude-opus-4-7-tier"}, map[int][]string{
		1: {"claude-opus-4-7-tier"}, 2: {"claude-sonnet-4-5-tier"}})
	content := strings.Repeat("Please review the scheduler design and the retry policy. 请审查调度器的设计和重试策略。", 60)
	encoded, err := common.Marshal(map[string]any{"model": "auto", "messages": []any{map[string]any{"role": "user", "content": content}}})
	require.NoError(t, err)
	body := string(encoded)

	probe, _ := gin.CreateTestContext(httptest.NewRecorder())
	probe.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	sonnetLength := service.RoutingInputTokens(probe, "claude-sonnet-4-5-tier")
	opusLength := service.RoutingInputTokens(probe, "claude-opus-4-7-tier")
	require.Less(t, sonnetLength, opusLength)

	// 长度阈值落在两个估算之间：sonnet 在短档（混合价 1），opus 在长档（混合价 10）。
	expression := fmt.Sprintf(`len <= %d ? tier("short", p * 1 + c * 1) : tier("long", p * 10 + c * 10)`, (sonnetLength+opusLength)/2)
	useTieredExpressions(t, map[string]string{"claude-opus-4-7-tier": expression, "claude-sonnet-4-5-tier": expression})

	s := autorouter_setting.GetAutoRouterSetting()
	s.TaskModels = map[string][]string{"code.build": {"claude-opus-4-7-tier", "claude-sonnet-4-5-tier"}}
	s.TaskScores = map[string]map[string]float64{"code.build": {"claude-opus-4-7-tier": 0.97, "claude-sonnet-4-5-tier": 0.96}}
	t.Setenv("JEV_ENDPOINT", fakeJevFor(t, "code.build"))

	_, originalModel, recorder := serveAutoRoutingRequest(t, "/v1/chat/completions", 9805, body)

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, "claude-sonnet-4-5-tier", originalModel,
		"opus 的 tokenizer 把同一段文本切得更长，落进 10 倍价格的长档；两者分数只差 0.01，所以选便宜的 sonnet")
}
