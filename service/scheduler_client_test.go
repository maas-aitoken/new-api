package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/observability"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSignSchedulerRequestUsesCanonicalPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://scheduler.test/v1/schedule", nil)
	body := []byte(`{"request_id":"r"}`)
	signSchedulerRequest(req, "secret", body)
	timestamp := req.Header.Get("X-Scheduler-Timestamp")
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil || timestamp == "" {
		t.Fatalf("timestamp=%q err=%v", timestamp, err)
	}
	// Recompute the protocol using the emitted timestamp; the helper uses the
	// same method/path/body canonicalization as Scheduler's verifier.
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte("POST\n/v1/schedule\n" + timestamp + "\n"))
	_, _ = mac.Write(body)
	if got, want := req.Header.Get("X-Scheduler-Signature"), hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("signature=%s want=%s", got, want)
	}
}

func TestRunSchedulerShadowStoresOnlyMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var sentEstimate atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scheduler-test" {
			t.Fatalf("missing scheduler auth")
		}
		if r.URL.Path != "/v1/schedule" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var request schedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode schedule request: %v", err)
		}
		// 策略只带排序要用的两项，重试次数留在 new-api。
		assert.Equal(t, map[string]any{"mode": "price", "runtime_high_watermark": 0.75}, request.Policy)
		sentEstimate.Store(int64(request.EstimatedInputTokens))
		assert.Equal(t, []int{4, 7}, request.AllowedChannelIDs)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision_id":"d1","catalog_version":"catalog-v1","candidates":[{"endpoint_id":"ep1","channel_id":7,"key_index":2,"model":"m","reason":["healthy"]}]}`))
	}))
	defer server.Close()
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second, RuntimeHighWatermark: 0.75})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"model":"m","max_tokens":123,"messages":[{"role":"user","content":"` +
		strings.Repeat("summarize the scheduler retry policy ", 30) + `"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Set(common.RequestIdKey, "req1")
	common.SetContextKey(c, constant.ContextKeyChannelId, 7)
	common.SetContextKey(c, constant.ContextKeySchedulerAllowedChannelIDs, []int{4, 7})
	common.SetContextKey(c, constant.ContextKeyTokenRoutingPreference, &dto.RoutingPreference{Mode: "price"})
	if err := RunSchedulerShadow(c, "m", "default"); err != nil {
		t.Fatal(err)
	}
	// 决策请求带的是选路阶段按这个模型估算的输入长度，调度器拿它判断 TPM 加上这次会不会超额。
	routingEstimate := RoutingInputTokens(c, "m")
	require.Greater(t, routingEstimate, int64(100), "消息文本要算进去")
	assert.Equal(t, routingEstimate, sentEstimate.Load())
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerDecisionID); got != "d1" {
		t.Fatalf("decision=%s", got)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeySchedulerShadowMatch) {
		t.Fatal("shadow match not recorded")
	}
	if candidates, ok := common.GetContextKeyType[[]SchedulerCandidate](c, constant.ContextKeySchedulerCandidates); !ok || len(candidates) != 1 || candidates[0].ChannelID != 7 {
		t.Fatalf("candidates=%+v ok=%v", candidates, ok)
	}
}

func TestRunSchedulerShadowFallsBackToNextSchedulerURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var firstHits int32
	var secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		if r.URL.Path != "/v1/schedule" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"decision_id":"d-fallback","catalog_version":"catalog-v1","candidates":[{"endpoint_id":"ep2","channel_id":8,"key_index":1,"model":"m","reason":["healthy"]}]}`))
	}))
	defer second.Close()

	ConfigureSchedulerClientForTest(SchedulerClientConfig{
		Enabled:       true,
		LocalURL:      first.URL,
		BootstrapURLs: []string{second.URL},
		Token:         "scheduler-test",
		Timeout:       time.Second,
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-fallback")
	if err := RunSchedulerShadow(c, "m", "default"); err != nil {
		t.Fatalf("fallback scheduler request failed: %v", err)
	}
	if got := atomic.LoadInt32(&firstHits); got == 0 {
		t.Fatal("local scheduler was not tried first")
	}
	if got := atomic.LoadInt32(&secondHits); got == 0 {
		t.Fatal("bootstrap scheduler was not used after local failure")
	}
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerDecisionID); got != "d-fallback" {
		t.Fatalf("decision=%s", got)
	}
}

func TestRunSchedulerShadowFallsBackAfterEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var firstHits int32
	var secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack first scheduler connection: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		if r.URL.Path != "/v1/schedule" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"decision_id":"d-eof-fallback","catalog_version":"catalog-v1","candidates":[{"endpoint_id":"ep2","channel_id":8,"key_index":1,"model":"m","reason":["healthy"]}]}`))
	}))
	defer second.Close()

	ConfigureSchedulerClientForTest(SchedulerClientConfig{
		Enabled:       true,
		LocalURL:      first.URL,
		BootstrapURLs: []string{second.URL},
		Token:         "scheduler-test",
		Timeout:       time.Second,
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-eof-fallback")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), common.RequestIdKey, "req-eof-fallback"))
	if err := RunSchedulerShadow(c, "m", "default"); err != nil {
		t.Fatalf("fallback after EOF failed: %v", err)
	}
	if got := atomic.LoadInt32(&firstHits); got == 0 {
		t.Fatal("local scheduler was not tried first")
	}
	if got := atomic.LoadInt32(&secondHits); got == 0 {
		t.Fatal("bootstrap scheduler was not used after EOF")
	}
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerDecisionID); got != "d-eof-fallback" {
		t.Fatalf("decision=%s", got)
	}
}

func TestRunSchedulerShadowDefaultsToBalancedPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request schedulerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode schedule request: %v", err)
		}
		if request.Policy["mode"] != "balanced" {
			t.Fatalf("default policy=%v", request.Policy)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision_id":"d1","catalog_version":"catalog-v1","candidates":[{"endpoint_id":"ep1","channel_id":7,"key_index":2,"model":"m","reason":["healthy"]}]}`))
	}))
	defer server.Close()
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req1")
	if err := RunSchedulerShadow(c, "m", "default"); err != nil {
		t.Fatalf("shadow with default policy failed: %v", err)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerDecisionID); got != "d1" {
		t.Fatalf("decision=%s", got)
	}
}

func TestSchedulerEmergencyNativeRequiresBothSwitchesAndFailureWindow(t *testing.T) {
	ConfigureSchedulerClientForTest(SchedulerClientConfig{
		Enabled:              true,
		EmergencyNative:      true,
		EmergencyLocalSwitch: false,
		EmergencyMaxDuration: time.Minute,
		EmergencyGroups:      []string{"paid"},
		EmergencyModels:      []string{"gpt-test"},
	})
	if SchedulerEmergencyNativeAllowed("gpt-test", "paid") {
		t.Fatal("emergency routing enabled without local switch")
	}
	schedulerCircuitFailure(time.Now())
	if SchedulerEmergencyNativeAllowed("gpt-test", "paid") {
		t.Fatal("emergency routing enabled without local switch after failure")
	}
	ConfigureSchedulerClientForTest(SchedulerClientConfig{
		Enabled:              true,
		EmergencyNative:      true,
		EmergencyLocalSwitch: true,
		EmergencyMaxDuration: time.Minute,
		EmergencyGroups:      []string{"paid"},
		EmergencyModels:      []string{"gpt-test"},
	})
	schedulerCircuitFailure(time.Now())
	if !SchedulerEmergencyNativeAllowed("gpt-test", "paid") {
		t.Fatal("emergency routing should be enabled after transient failure")
	}
	if SchedulerEmergencyNativeAllowed("other", "paid") || SchedulerEmergencyNativeAllowed("gpt-test", "free") {
		t.Fatal("emergency allowlist was not enforced")
	}
}

func TestSchedulerKillSwitchIsReadDynamicallyAndPreservesInFlightRequest(t *testing.T) {
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "enforced"})
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap["SchedulerKillSwitch"]
	common.OptionMap["SchedulerKillSwitch"] = "true"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap["SchedulerKillSwitch"] = previous
		} else {
			delete(common.OptionMap, "SchedulerKillSwitch")
		}
		common.OptionMapRWMutex.Unlock()
	})
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if SchedulerEnforcedForRequest(c) {
		t.Fatal("kill switch did not disable a new enforced request")
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap["SchedulerKillSwitch"] = "false"
	common.OptionMapRWMutex.Unlock()
	if !SchedulerEnforcedForRequest(c) {
		t.Fatal("dynamic kill switch disable did not take effect")
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap["SchedulerKillSwitch"] = "true"
	common.OptionMapRWMutex.Unlock()
	if !SchedulerEnforcedForRequest(c) {
		t.Fatal("kill switch interrupted an in-flight Scheduler request")
	}
}

func TestSchedulerClientReadsModeAndEmergencyConfigWithoutReload(t *testing.T) {
	ReloadSchedulerClient()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	keys := []string{"SchedulerEnabled", "SchedulerMode", "SchedulerCanaryPercent", "SchedulerEmergencyNativeRouting"}
	previous := make(map[string]string, len(keys))
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		previous[key], present[key] = common.OptionMap[key]
	}
	common.OptionMap["SchedulerEnabled"] = "true"
	common.OptionMap["SchedulerMode"] = "shadow"
	common.OptionMap["SchedulerCanaryPercent"] = "0"
	common.OptionMap["SchedulerEmergencyNativeRouting"] = "false"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		for _, key := range keys {
			if present[key] {
				common.OptionMap[key] = previous[key]
			} else {
				delete(common.OptionMap, key)
			}
		}
		common.OptionMapRWMutex.Unlock()
		ReloadSchedulerClient()
	})
	if got := SchedulerClient().Mode; got != "shadow" {
		t.Fatalf("initial mode=%q", got)
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap["SchedulerMode"] = "enforced"
	common.OptionMap["SchedulerEmergencyNativeRouting"] = "true"
	common.OptionMapRWMutex.Unlock()
	config := SchedulerClient()
	if config.Mode != "enforced" || !config.EmergencyNative {
		t.Fatalf("dynamic config not observed: %+v", config)
	}
}

func TestRunSchedulerShadowClassifiesTransientStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "transient-status")
	err := RunSchedulerShadow(c, "m", "default")
	if !IsSchedulerTransientUnavailable(err) || !errors.Is(err, ErrSchedulerTemporarilyUnavailable) {
		t.Fatalf("expected transient scheduler error, got %v", err)
	}
	_, ok := SchedulerDecisionErrorMessage(err)
	assert.False(t, ok)
}

func TestRunSchedulerShadowPreservesNoCandidateAfterFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var firstHits int32
	var secondHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"no candidate endpoint"}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"no candidate endpoint"}`))
	}))
	defer second.Close()

	ConfigureSchedulerClientForTest(SchedulerClientConfig{
		Enabled:       true,
		LocalURL:      first.URL,
		BootstrapURLs: []string{second.URL},
		Token:         "scheduler-test",
		Timeout:       time.Second,
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "no-candidate-fallback")

	err := RunSchedulerShadow(c, "m", "default")
	require.Error(t, err)
	message, ok := SchedulerDecisionErrorMessage(err)
	require.True(t, ok)
	assert.Equal(t, "no candidate endpoint", message)
	assert.False(t, IsSchedulerTransientUnavailable(err))
	assert.Equal(t, int32(1), atomic.LoadInt32(&firstHits))
	assert.Equal(t, int32(1), atomic.LoadInt32(&secondHits))
	assert.True(t, schedulerCircuitAllows(time.Now()), "request-scoped rejection must not open the infrastructure circuit")
}

// 预览一次问所有候选，每个候选带自己的渠道范围、偏好渠道和按自己家族估的输入长度；
// 请求级字段（策略、分组、workload）与正式调度相同；结果按顺序对应候选。
func TestPreviewSchedulerAsksEveryTargetWithItsOwnScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var request schedulerPreviewRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/preview", r.URL.Path)
		require.Equal(t, "Bearer scheduler-test", r.Header.Get("Authorization"))
		require.NoError(t, common.DecodeJson(r.Body, &request))
		_, _ = w.Write([]byte(`{"results":[{"model":"a","status":"ok","channel_id":7},{"model":"b","status":"no_candidate"}]}`))
	}))
	defer server.Close()
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "enforced", BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second, RuntimeHighWatermark: 0.75})
	t.Cleanup(func() { ConfigureSchedulerClientForTest(SchedulerClientConfig{}) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(common.RequestIdKey, "req-preview")
	common.SetContextKey(c, constant.ContextKeySchedulerWorkload, "code.build")
	common.SetContextKey(c, constant.ContextKeyTokenRoutingPreference, &dto.RoutingPreference{Mode: "price"})

	results := PreviewScheduler(c, "default", []SchedulerPreviewTarget{
		{Model: "a", AllowedChannelIDs: []int{7, 8}, PreferredChannelID: 8},
		{Model: "b"},
	})

	assert.Equal(t, []SchedulerPreviewResult{{Status: SchedulerPreviewOK, ChannelID: 7}, {Status: SchedulerPreviewNoCandidate}}, results)
	assert.Equal(t, "req-preview", request.RequestID)
	assert.Equal(t, "default", request.Group)
	assert.Equal(t, "code.build", request.Workload)
	assert.Equal(t, map[string]any{"mode": "price", "runtime_high_watermark": 0.75}, request.Policy)
	assert.Equal(t, []schedulerPreviewModel{
		{Model: "a", AllowedChannelIDs: []int{7, 8}, PreferredChannelID: 8, EstimatedInputTokens: int(RoutingInputTokens(c, "a"))},
		{Model: "b", AllowedChannelIDs: []int{}, EstimatedInputTokens: int(RoutingInputTokens(c, "b"))},
	}, request.Models, "an empty scope is sent as [], never null")
}

// 预览失败只是退回原生估算：不计入熔断；旧调度器回 404 后一段时间内不再问；
// 调度器不决定渠道（影子模式）时根本不问。
func TestPreviewSchedulerFailuresNeverTouchTheCircuit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	status := http.StatusInternalServerError
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
	}))
	defer server.Close()
	configure := func(mode string) {
		ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: mode, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
	}
	t.Cleanup(func() { ConfigureSchedulerClientForTest(SchedulerClientConfig{}) })
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto"}`))
		return c
	}
	targets := []SchedulerPreviewTarget{{Model: "a", AllowedChannelIDs: []int{1}}}

	configure("shadow")
	assert.Nil(t, PreviewScheduler(newContext(), "default", targets))
	assert.Zero(t, calls.Load(), "shadow mode lets new-api pick the channel, so there is nothing to preview")

	configure("enforced")
	for range 5 {
		assert.Nil(t, PreviewScheduler(newContext(), "default", targets))
	}
	assert.True(t, schedulerCircuitClosed(), "failed previews never open the scheduler circuit")
	assert.True(t, schedulerCircuitAllows(time.Now()))

	status = http.StatusNotFound
	before := calls.Load()
	assert.Nil(t, PreviewScheduler(newContext(), "default", targets))
	assert.Nil(t, PreviewScheduler(newContext(), "default", targets))
	assert.Equal(t, before+1, calls.Load(), "an old scheduler without /v1/preview is not asked again right away")
}

func TestSchedulerCircuitRequiresTwoSuccessfulProbesToClose(t *testing.T) {
	ConfigureSchedulerClientForTest(SchedulerClientConfig{})
	now := time.Now()
	schedulerCircuitFailure(now)
	schedulerCircuitFailure(now.Add(time.Millisecond))
	schedulerCircuitFailure(now.Add(2 * time.Millisecond))
	if schedulerCircuitAllows(now.Add(time.Second)) {
		t.Fatal("open circuit allowed a request before probe interval")
	}
	probeAt := now.Add(schedulerCircuitProbeInterval + 5*time.Millisecond)
	if !schedulerCircuitAllows(probeAt) {
		t.Fatal("half-open probe was not allowed")
	}
	schedulerCircuitSuccess(probeAt)
	if schedulerCircuitAllows(probeAt.Add(time.Millisecond)) {
		t.Fatal("second probe was allowed before probe interval")
	}
	secondProbeAt := probeAt.Add(schedulerCircuitProbeInterval + time.Millisecond)
	if !schedulerCircuitAllows(secondProbeAt) {
		t.Fatal("second half-open probe was not allowed")
	}
	schedulerCircuitSuccess(secondProbeAt)
	if !schedulerCircuitAllows(secondProbeAt.Add(time.Millisecond)) {
		t.Fatal("circuit did not close after two successful probes")
	}
}

func TestMarkSchedulerEmergencySetsRequestMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	MarkSchedulerEmergency(c, fmt.Errorf("connection refused"))
	if !SchedulerEmergencyNativeActive(c) {
		t.Fatal("emergency request marker missing")
	}
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerDegradedReason); got != "scheduler_unavailable" {
		t.Fatalf("degraded reason=%q", got)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeySchedulerFailureReason); got != "connection refused" {
		t.Fatalf("failure reason=%q", got)
	}
}

func TestRunSchedulerShadowIsDisabledByDefault(t *testing.T) {
	ConfigureSchedulerClientForTest(SchedulerClientConfig{})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if err := RunSchedulerShadow(c, "m", "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := common.GetContextKey(c, constant.ContextKeySchedulerCandidates); ok {
		t.Fatal("disabled shadow mutated context")
	}
}

func TestRunSchedulerShadowSendsEmptyChannelScopeAsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		scope func(c *gin.Context)
	}{
		// usableChannelIDs 在分组没有可用渠道时返回 nil 切片，以前会发成 null，被旧调度器当成"不限"。
		{name: "nil scope", scope: func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeySchedulerAllowedChannelIDs, []int(nil))
		}},
		{name: "scope not recorded", scope: func(*gin.Context) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				select {
				case bodies <- string(raw):
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"no candidate endpoint"}`))
			}))
			defer server.Close()
			ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Set(common.RequestIdKey, "empty-scope")
			tc.scope(c)

			require.Error(t, RunSchedulerShadow(c, "m", "default"))
			assert.Contains(t, <-bodies, `"allowed_channel_ids":[]`)
		})
	}
}

func TestRunSchedulerShadowRejectsIncompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"candidates":[{"endpoint_id":"ep1","channel_id":1,"key_index":0,"model":"m","reason":["healthy"]}]}`))
	}))
	defer server.Close()
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if err := RunSchedulerShadow(c, "m", "default"); err == nil {
		t.Fatal("expected incomplete response error")
	}
}

func TestRunSchedulerShadowRecordsDecisionSpan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newTracedContext := func(t *testing.T) (*gin.Context, *tracetest.InMemoryExporter) {
		exporter := tracetest.NewInMemoryExporter()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Request = c.Request.WithContext(observability.ContextWithRuntimeForTest(c.Request.Context(), provider))
		c.Set(common.RequestIdKey, "traced")
		return c, exporter
	}
	attr := func(span tracetest.SpanStub, key string) string {
		for _, kv := range span.Attributes {
			if string(kv.Key) == key {
				return kv.Value.AsString()
			}
		}
		return ""
	}

	t.Run("shadow success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"decision_id":"d9","catalog_version":"catalog-v1","candidates":[{"endpoint_id":"ep1","channel_id":7,"key_index":0,"model":"m","reason":["healthy"]}]}`))
		}))
		defer server.Close()
		ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "shadow", BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
		c, exporter := newTracedContext(t)
		common.SetContextKey(c, constant.ContextKeyChannelId, 7)
		require.NoError(t, RunSchedulerShadow(c, "m", "default"))

		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		span := spans[0]
		assert.Equal(t, "newapi.scheduler.decide", span.Name)
		assert.Equal(t, "shadow", attr(span, "langfuse.observation.metadata.mode"))
		assert.Equal(t, "d9", attr(span, "langfuse.observation.metadata.decision_id"))
		assert.Equal(t, "true", attr(span, "langfuse.observation.metadata.shadow_match"))
		assert.Contains(t, attr(span, "langfuse.observation.input"), `"request_id":"traced"`)
		assert.Contains(t, attr(span, "langfuse.observation.output"), `"endpoint_id":"ep1"`)
		assert.Empty(t, attr(span, "langfuse.observation.level"))
	})

	t.Run("failure level follows mode", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "enforced", BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
		c, exporter := newTracedContext(t)
		require.Error(t, RunSchedulerShadow(c, "m", "default"))
		spans := exporter.GetSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "enforced", attr(spans[0], "langfuse.observation.metadata.mode"))
		assert.Equal(t, codes.Error, spans[0].Status.Code)

		ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "shadow", BaseURL: server.URL, Token: "scheduler-test", Timeout: time.Second})
		c, exporter = newTracedContext(t)
		require.Error(t, RunSchedulerShadow(c, "m", "default"))
		spans = exporter.GetSpans()
		require.Len(t, spans, 1)
		assert.NotEqual(t, codes.Error, spans[0].Status.Code)
		assert.Equal(t, "WARNING", attr(spans[0], "langfuse.observation.level"))
	})
}

func TestSchedulerCandidateForInitialUsesFirstCandidateOnlyWhenEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeySchedulerCandidates, []SchedulerCandidate{
		{EndpointID: "first", ChannelID: 7, Model: "m"},
		{EndpointID: "second", ChannelID: 8, Model: "m"},
	})
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "shadow"})
	if _, ok := SchedulerCandidateForInitial(c); ok {
		t.Fatal("shadow mode selected an initial candidate")
	}
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "enforced"})
	got, ok := SchedulerCandidateForInitial(c)
	if !ok || got.EndpointID != "first" || got.ChannelID != 7 {
		t.Fatalf("candidate=%+v ok=%v", got, ok)
	}
}

func TestSchedulerCanaryUsesStableHashAndDisabledConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: true, Mode: "canary", CanaryPercent: 50, CanarySalt: "test-salt"})
	firstUserRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	firstUserRequest.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	firstUserRequest.Set("id", 42)
	firstUserRequest.Set(common.RequestIdKey, "request-a")
	secondUserRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	secondUserRequest.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	secondUserRequest.Set("id", 42)
	secondUserRequest.Set(common.RequestIdKey, "request-b")
	if SchedulerEnforcedForRequest(firstUserRequest) != SchedulerEnforcedForRequest(secondUserRequest) {
		t.Fatal("same user was assigned to different canary cohorts")
	}
	selected := map[bool]bool{}
	for i := 0; i < 200; i++ {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Set(common.RequestIdKey, fmt.Sprintf("canary-%d", i))
		first := SchedulerEnforcedForRequest(c)
		second := SchedulerEnforcedForRequest(c)
		if first != second {
			t.Fatalf("canary decision was not stable for request %d", i)
		}
		selected[first] = true
	}
	if !selected[true] || !selected[false] {
		t.Fatalf("50%% canary did not produce both cohorts: %v", selected)
	}
	ConfigureSchedulerClientForTest(SchedulerClientConfig{Enabled: false, Mode: "canary", CanaryPercent: 100, CanarySalt: "test-salt"})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "canary-kill-switch")
	if SchedulerEnforcedForRequest(c) {
		t.Fatal("disabled Scheduler config did not disable canary")
	}
}

func TestRecordSchedulerUsageNormalizesOpenAIAndClaudeFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordSchedulerUsage(c, &dto.Usage{PromptTokens: 11, CompletionTokens: 7}, true)
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerInputTokens); got != 11 {
		t.Fatalf("input=%d", got)
	}
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerOutputTokens); got != 7 {
		t.Fatalf("output=%d", got)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeySchedulerStreamStarted) {
		t.Fatal("stream start not recorded")
	}
	RecordSchedulerUsage(c, &dto.Usage{InputTokens: 13, OutputTokens: 5}, false)
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerInputTokens); got != 13 {
		t.Fatalf("claude input=%d", got)
	}
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerOutputTokens); got != 5 {
		t.Fatalf("claude output=%d", got)
	}
}

func TestResetSchedulerAttemptMetricsClearsPriorRetryState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordSchedulerUsage(c, &dto.Usage{PromptTokens: 10, CompletionTokens: 2}, true)
	ResetSchedulerAttemptMetrics(c)
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerInputTokens); got != 0 {
		t.Fatalf("input=%d", got)
	}
	if got := common.GetContextKeyInt(c, constant.ContextKeySchedulerOutputTokens); got != 0 {
		t.Fatalf("output=%d", got)
	}
	if common.GetContextKeyBool(c, constant.ContextKeySchedulerStreamStarted) {
		t.Fatal("retry state was not cleared")
	}
}
