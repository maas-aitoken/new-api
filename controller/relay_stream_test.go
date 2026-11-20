package controller

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func disableRelayTestPerfMetrics(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if key == "perf_metrics_setting.enabled" {
			saved[key] = value
		}
		return nil
	}))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": "false"}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
}

func TestRelayAutoRetriesNextModelBeyondChannelRetryBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableRelayTestPerfMetrics(t)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousRetryTimes := common.RetryTimes
	previousRatios := ratio_setting.ModelRatio2JSONString()
	previousCompletions := ratio_setting.CompletionRatio2JSONString()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	autoSetting := autorouter_setting.GetAutoRouterSetting()
	previousAutoSetting := *autoSetting
	autoSetting.Enabled = true
	autoSetting.DefaultModels = []string{"failed-model", "available-model", "last-model"}
	autoSetting.TaskModels = map[string][]string{}
	autoSetting.TaskScores = map[string]map[string]float64{}
	t.Setenv("JEV_ENDPOINT", "")
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/relay.db"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Model{}, &model.Channel{}, &model.Ability{}, &model.ChannelModelStatus{}, &model.ChannelModelCapability{}, &model.User{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.QuotaData{}, &model.Log{}, &model.Token{}))
	model.DB, model.LOG_DB = database, database
	common.MemoryCacheEnabled, common.RedisEnabled = true, false
	common.RetryTimes = 0
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		model.InitChannelCache()
		common.RetryTimes = previousRetryTimes
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletions))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		*autoSetting = previousAutoSetting
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"failed-model":1,"available-model":1,"last-model":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"failed-model":1,"available-model":1,"last-model":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		requests = append(requests, string(body))
		var requestModel struct{ Model string }
		assert.NoError(t, common.Unmarshal(body, &requestModel))
		if requestModel.Model != "last-model" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"last-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	priority, weight := int64(0), uint(1)
	for id, name := range []string{"failed-model", "available-model", "last-model"} {
		channel := model.Channel{Id: id + 1, Name: name, Type: constant.ChannelTypeOpenAI, BaseURL: common.GetPointer(upstream.URL), Key: "test", Status: common.ChannelStatusEnabled, Models: name, Group: "default", Priority: &priority, Weight: &weight}
		require.NoError(t, database.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	model.InitChannelCache()
	require.NoError(t, database.Create(&model.User{Id: 1, Username: "auto-retry-user", Quota: 1_000_000}).Error)
	require.NoError(t, i18n.Init())

	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserId, 1)
		common.SetContextKey(c, constant.ContextKeyTokenId, 7301)
		c.Set("id", 1)
	}, middleware.Distribute(), func(c *gin.Context) {
		Relay(c, types.RelayFormatOpenAI)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, requests, 3, "each model must be tried once even when the channel retry budget is zero")
	assert.Contains(t, requests[0], `"model":"failed-model"`)
	assert.Contains(t, requests[1], `"model":"available-model"`)
	assert.Contains(t, requests[2], `"model":"last-model"`)

	requests = nil
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, requests, 1, "a continuation must start with the model that actually answered the previous request")
	assert.Contains(t, requests[0], `"model":"last-model"`)

	requests = nil
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"exhaust all models"}]}`))
	request.Header.Set("Content-Type", "application/json")
	autoSetting.DefaultModels = []string{"failed-model", "available-model"}
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Len(t, requests, 2, "the request ends after every eligible model has failed once")
}

func TestRelayAutoRetryRefreshesModelScopedRequestState(t *testing.T) {
	t.Run("reasoning and billing", testRelayAutoRetryRefreshesModelScopedRequestState)
	t.Run("capability channel filter", testRelayAutoRetryReplacesCapabilityFilter)
}

func testRelayAutoRetryRefreshesModelScopedRequestState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableRelayTestPerfMetrics(t)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousRetryTimes := common.RetryTimes
	previousRatios := ratio_setting.ModelRatio2JSONString()
	previousCompletions := ratio_setting.CompletionRatio2JSONString()
	previousPrices := ratio_setting.ModelPrice2JSONString()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	autoSetting := autorouter_setting.GetAutoRouterSetting()
	previousAutoSetting := *autoSetting
	gateSetting := operation_setting.GetCapabilityGateSetting()
	previousGateMode := gateSetting.Mode
	autoSetting.Enabled = true
	autoSetting.DefaultModels = []string{"auto-state-first", "auto-state-second", "auto-state-third"}
	autoSetting.TaskModels = map[string][]string{"general.chat": {"auto-state-first", "auto-state-second", "auto-state-third"}}
	autoSetting.TaskScores = map[string]map[string]float64{}
	autoSetting.RankSources = map[string]string{"taxonomy_version": "3"}
	autoSetting.JevEndpoint = ""
	t.Setenv("JEV_ENDPOINT", "")
	t.Setenv("JEV_MODEL", "")
	t.Setenv("JEV_API_KEY", "")
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev","answers":{"task_type":{"choice":"general.chat","probabilities":{"general.chat":1}},"difficulty":{"probabilities":{"1":1}}}}`)
	}))
	defer jev.Close()
	autoSetting.JevEndpoint = jev.URL
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/relay.db"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Model{}, &model.Channel{}, &model.Ability{}, &model.ChannelModelStatus{}, &model.ChannelModelCapability{}, &model.User{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.QuotaData{}, &model.Log{}, &model.Token{}))
	model.DB, model.LOG_DB = database, database
	common.MemoryCacheEnabled, common.RedisEnabled = true, false
	common.RetryTimes = 0
	gateSetting.Mode = operation_setting.CapabilityGateModeOff
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		model.InitChannelCache()
		common.RetryTimes = previousRetryTimes
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletions))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		*autoSetting = previousAutoSetting
		gateSetting.Mode = previousGateMode
		model.InvalidateCapabilityCache()
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"auto-state-first":1,"auto-state-first-medium":1,"auto-state-second":2,"auto-state-second@effort:medium":2,"auto-state-third":4}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"auto-state-first":1,"auto-state-second":1,"auto-state-third":1}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		requests = append(requests, string(body))
		var request relaydto.GeneralOpenAIRequest
		assert.NoError(t, common.Unmarshal(body, &request))
		if request.Model == "auto-state-first" || request.Model == "auto-state-first-medium" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if request.Model == "auto-state-second" && len(request.Tools) == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"`+request.Model+`","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	priority, weight := int64(0), uint(1)
	for id, name := range []string{"auto-state-first,auto-state-first-medium", "auto-state-second", "auto-state-third"} {
		channel := model.Channel{Id: id + 1, Name: name, Type: constant.ChannelTypeOpenAI, BaseURL: common.GetPointer(upstream.URL), Key: "test", Status: common.ChannelStatusEnabled, Models: name, Group: "default", Priority: &priority, Weight: &weight}
		require.NoError(t, database.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	for id := 4; id <= 5; id++ {
		channel := model.Channel{Id: id, Name: "auto-state-first-extra", Type: constant.ChannelTypeOpenAI, BaseURL: common.GetPointer(upstream.URL), Key: "test", Status: common.ChannelStatusEnabled, Models: "auto-state-first,auto-state-first-medium", Group: "default", Priority: &priority, Weight: &weight}
		require.NoError(t, database.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	require.NoError(t, database.Create(&[]model.Model{
		{ModelName: "auto-state-first", Capabilities: `{"context":{"max_input_tokens":1000},"tools":{"supported":true},"reasoning":{"supported":true,"efforts":["none","minimal","low","medium","high","xhigh"]}}`},
		{ModelName: "auto-state-second", Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":true}}`},
		{ModelName: "auto-state-third", Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":false}}`},
	}).Error)
	model.InitChannelCache()
	require.NoError(t, model.InitCapabilityCache())
	require.NoError(t, model.SetChannelModelCapability(4, "auto-state-first", `{"tools":{"supported":false}}`))
	require.NoError(t, model.SetChannelModelCapability(5, "auto-state-first", `{"reasoning":{"efforts":["low","medium"]}}`))
	require.NoError(t, database.Create(&model.User{Id: 1, Username: "auto-retry-state-user", Quota: 1_000_000}).Error)
	require.NoError(t, i18n.Init())

	router := gin.New()
	var billingModel, billingEffort string
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserId, 1)
		c.Set("id", 1)
	}, middleware.Distribute(), func(c *gin.Context) {
		previousPrepare := prepareAutoRetryBilling
		prepareAutoRetryBilling = func(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
			apiErr := previousPrepare(c, info)
			billingModel = info.GetBillingModelName()
			billingEffort = info.Request.(*relaydto.GeneralOpenAIRequest).ReasoningEffort
			return apiErr
		}
		defer func() { prepareAutoRetryBilling = previousPrepare }()
		Relay(c, types.RelayFormatOpenAI)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"auto retry request state"}],"max_tokens":16,"reasoning_effort":"high","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, requests, 2)
	assert.Contains(t, requests[0], `"model":"auto-state-first"`)
	assert.Contains(t, requests[1], `"model":"auto-state-second"`)
	assert.Contains(t, requests[1], `"reasoning_effort":"medium"`)
	assert.Equal(t, "auto-state-second", billingModel)
	assert.Equal(t, "medium", billingEffort, "billing must use the same reasoning fields as the rewritten body")
}

func testRelayAutoRetryReplacesCapabilityFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disableRelayTestPerfMetrics(t)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousRetryTimes := common.RetryTimes
	previousRatios := ratio_setting.ModelRatio2JSONString()
	previousCompletions := ratio_setting.CompletionRatio2JSONString()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	autoSetting := autorouter_setting.GetAutoRouterSetting()
	previousAutoSetting := *autoSetting
	gateSetting := operation_setting.GetCapabilityGateSetting()
	previousGateMode := gateSetting.Mode
	autoSetting.Enabled = true
	autoSetting.DefaultModels = []string{"first", "second", "third"}
	autoSetting.TaskModels = map[string][]string{}
	autoSetting.TaskScores = map[string]map[string]float64{}
	autoSetting.RankSources = map[string]string{"taxonomy_version": "3"}
	autoSetting.JevEndpoint = ""
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/relay.db"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Model{}, &model.Channel{}, &model.Ability{}, &model.ChannelModelStatus{}, &model.ChannelModelCapability{}, &model.User{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.QuotaData{}, &model.Log{}, &model.Token{}))
	model.DB, model.LOG_DB = database, database
	common.MemoryCacheEnabled, common.RedisEnabled = true, false
	common.RetryTimes = 0
	gateSetting.Mode = operation_setting.CapabilityGateModeEnforce
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		model.InitChannelCache()
		common.RetryTimes = previousRetryTimes
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletions))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		*autoSetting = previousAutoSetting
		gateSetting.Mode = previousGateMode
		model.InvalidateCapabilityCache()
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"first":1,"second":1,"third":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"first":1,"second":1,"third":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		requests = append(requests, string(body))
		var request struct{ Model string }
		assert.NoError(t, common.Unmarshal(body, &request))
		if request.Model != "third" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"third","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	priority, weight := int64(0), uint(1)
	for id, name := range []string{"first", "second", "third", "first"} {
		channel := model.Channel{Id: id + 1, Name: name, Type: constant.ChannelTypeOpenAI, BaseURL: common.GetPointer(upstream.URL), Key: "test", Status: common.ChannelStatusEnabled, Models: name, Group: "default", Priority: &priority, Weight: &weight}
		require.NoError(t, database.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	require.NoError(t, database.Create(&[]model.Model{
		{ModelName: "first", Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":true}}`},
		{ModelName: "second", Capabilities: `{"tools":{"supported":true}}`},
		{ModelName: "third", Capabilities: `{"tools":{"supported":true},"reasoning":{"supported":false}}`},
	}).Error)
	model.InitChannelCache()
	require.NoError(t, model.InitCapabilityCache())
	require.NoError(t, model.SetChannelModelCapability(4, "first", `{"tools":{"supported":false}}`))
	require.NoError(t, database.Create(&model.User{Id: 1, Username: "auto-retry-capability-user", Quota: 1_000_000}).Error)
	require.NoError(t, i18n.Init())
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserId, 1)
		c.Set("id", 1)
	}, middleware.Distribute(), func(c *gin.Context) {
		Relay(c, types.RelayFormatOpenAI)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"auto retry capability filter"}],"max_tokens":16,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, requests, 3, "the next model must not inherit the first model's capability channel filter")
	assert.Contains(t, requests[0], `"model":"first"`)
	assert.Contains(t, requests[1], `"model":"second"`)
	assert.Contains(t, requests[2], `"model":"third"`)
	assert.NotContains(t, requests[2], "reasoning_effort")
}

func TestRelayKeepAliveDuringImagePreparation(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	previousCount, previousMedia := constant.CountToken, constant.GetMediaToken
	fetchSettings := system_setting.GetFetchSetting()
	previousFetchSettings := *fetchSettings
	fetchSettings.AllowPrivateIp = true
	fetchSettings.AllowedPorts = []string{"1-65535"}
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	constant.CountToken, constant.GetMediaToken = true, true
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
		constant.CountToken, constant.GetMediaToken = previousCount, previousMedia
		*fetchSettings = previousFetchSettings
	})
	service.InitHttpClient()

	imageRequested := make(chan struct{})
	releaseImage := make(chan struct{})
	var releaseOnce sync.Once
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(imageRequested)
		<-releaseImage
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer imageServer.Close()
	engine := gin.New()
	engine.POST("/v1/messages", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyOriginalModel, "claude-sonnet-4-20250514")
		Relay(c, types.RelayFormatClaude)
	})
	gateway := httptest.NewServer(engine)
	defer gateway.Close()
	defer releaseOnce.Do(func() { close(releaseImage) })

	body := `{"model":"claude-sonnet-4-20250514","max_tokens":16,"stream":true,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"` + imageServer.URL + `"}}]}]}`
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Post(gateway.URL+"/v1/messages", "application/json", strings.NewReader(body))
	require.NoError(t, err, "the gateway must send keepalive while its image download is blocked")
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	reader := bufio.NewReader(response.Body)
	for range 2 {
		line, readErr := reader.ReadString('\n')
		require.NoError(t, readErr)
		require.Equal(t, ": PING\n", line)
		line, readErr = reader.ReadString('\n')
		require.NoError(t, readErr)
		require.Equal(t, "\n", line)
	}
	select {
	case <-imageRequested:
	default:
		t.Fatal("the request never reached image preparation")
	}
	releaseOnce.Do(func() { close(releaseImage) })
	remaining, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(remaining), "event: error\n")
	require.Contains(t, string(remaining), `"type":"error"`)
	require.Contains(t, string(remaining), "data: ")
}

func TestRelayKeepAliveAcrossUpstreamRetries(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
	})
	service.InitHttpClient()
	var attempts atomic.Int32
	retryStarted, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		close(retryStarted)
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: ok\n\n")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	engine := gin.New()
	engine.POST("/v1/messages", func(c *gin.Context) {
		stop := helper.StartStreamKeepAlive(c, 10*time.Millisecond)
		defer stop()
		if !assert.NoError(t, http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(5*time.Second))) {
			return
		}
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
		for range 2 {
			request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream.URL, strings.NewReader("{}"))
			if !assert.NoError(t, err) {
				return
			}
			response, err := channel.DoRequest(c, request, info)
			if !assert.NoError(t, err) {
				return
			}
			if response.StatusCode == http.StatusTooManyRequests {
				response.Body.Close()
				continue
			}
			_, _ = io.Copy(c.Writer, response.Body)
			response.Body.Close()
		}
	})
	gateway := httptest.NewServer(engine)
	defer gateway.Close()
	defer releaseOnce.Do(func() { close(release) })
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Post(gateway.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer response.Body.Close()
	select {
	case <-retryStarted:
	case <-time.After(time.Second):
		t.Fatal("the upstream retry did not start")
	}
	reader := bufio.NewReader(response.Body)
	for range 2 {
		line, readErr := reader.ReadString('\n')
		require.NoError(t, readErr)
		require.Equal(t, ": PING\n", line)
		line, readErr = reader.ReadString('\n')
		require.NoError(t, readErr)
		require.Equal(t, "\n", line)
	}
	releaseOnce.Do(func() { close(release) })
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(body), "data: ok\n\n")
	require.Equal(t, int32(2), attempts.Load())
}

func TestRelayKeepAlivePreservesValidationErrors(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled := settings.PingIntervalEnabled
	settings.PingIntervalEnabled = true
	t.Cleanup(func() { settings.PingIntervalEnabled = previousEnabled })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-20250514","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	Relay(c, types.RelayFormatClaude)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
	require.NotContains(t, recorder.Body.String(), ": PING")
}

func TestStreamKeepAlivePreservesFastHTTPFailures(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 60
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
	})
	service.InitHttpClient()
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":"request rejected"}`)
			}))
			defer upstream.Close()
			engine := gin.New()
			engine.POST("/v1/messages", func(c *gin.Context) {
				stop := helper.StartStreamKeepAlive(c, time.Minute)
				defer stop()
				request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream.URL, strings.NewReader("{}"))
				if !assert.NoError(t, err) {
					return
				}
				info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
				response, err := channel.DoRequest(c, request, info)
				if !assert.NoError(t, err) {
					return
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if assert.NoError(t, err) {
					c.Header("Content-Type", "application/json")
					c.Data(response.StatusCode, "application/json", body)
				}
			})
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			client := &http.Client{Timeout: 4 * time.Second}
			response, err := client.Post(gateway.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, status, response.StatusCode)
			require.Equal(t, "application/json", response.Header.Get("Content-Type"))
			require.JSONEq(t, `{"error":"request rejected"}`, string(body))
		})
	}
}

func TestRelayKeepAliveHonorsStreamSettings(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled := settings.PingIntervalEnabled
	previousCount, previousMedia, previousNonStreamMedia := constant.CountToken, constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.CountToken, constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true, true
	t.Cleanup(func() {
		settings.PingIntervalEnabled = previousEnabled
		constant.CountToken, constant.GetMediaToken, constant.GetMediaTokenNotStream = previousCount, previousMedia, previousNonStreamMedia
	})
	for _, test := range []struct {
		name    string
		enabled bool
		stream  bool
	}{
		{name: "ping disabled", enabled: false, stream: true},
		{name: "non-streaming", enabled: true, stream: false},
		{name: "fast streaming failure", enabled: true, stream: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings.PingIntervalEnabled = test.enabled
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := `{"model":"claude-sonnet-4-20250514","max_tokens":16,"stream":` + strconv.FormatBool(test.stream) + `,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"!"}}]}]}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "claude-sonnet-4-20250514")
			Relay(c, types.RelayFormatClaude)
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
			require.NotContains(t, recorder.Body.String(), ": PING")
			var response map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, "error", response["type"])
		})
	}
}

func TestRelayStreamErrorAfterKeepAlive(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		t.Run(string(format), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil)
			helper.SetEventStreamHeaders(c)
			require.NoError(t, helper.PingData(c))
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://provider.example")
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestOrigin, "https://provider.example")
			apiError := types.NewErrorWithStatusCode(errors.New("upstream rejected input at https://provider.example/v1/messages"), types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest)
			writeRelayStreamError(c, format, apiError)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
			require.Contains(t, recorder.Body.String(), "upstream rejected input")
			require.NotContains(t, recorder.Body.String(), "provider.example")
			status, ok := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
			require.True(t, ok)
			require.True(t, status.ResponseFailed(), "stream errors must not consume the successful-request rate limit")
			require.Equal(t, http.StatusBadRequest, status.OutcomeSnapshot().ErrorStatus)
			if format == types.RelayFormatClaude {
				require.Contains(t, recorder.Body.String(), "event: error\n")
			} else {
				require.Contains(t, recorder.Body.String(), "data: [DONE]")
			}
		})
	}
}

func TestStreamKeepAliveCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/stream", nil).WithContext(requestContext)
	stop := helper.StartStreamKeepAlive(c, time.Hour)
	cancel()
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not stop after client cancellation")
	}
	_, err := c.Writer.WriteString("data: done\n\n")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(recorder.Body.String(), "data: done\n\n"))
}
