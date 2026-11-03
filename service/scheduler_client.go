package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/observability"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

var ErrSchedulerPolicyUnavailable = errors.New("scheduler effective policy is unavailable")
var ErrSchedulerTemporarilyUnavailable = errors.New("scheduler temporarily unavailable")

// SchedulerDecisionError is a structured, user-actionable rejection returned
// by Scheduler. Unlike transport failures and unstructured 5xx responses, a
// decision error is scoped to the request and must not trip Scheduler's
// infrastructure circuit breaker.
type SchedulerDecisionError struct {
	StatusCode int
	Message    string
}

func (e *SchedulerDecisionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type schedulerErrorResponse struct {
	Error string `json:"error"`
}

// schedulerDefaultMode is the platform default routing mode applied when the
// requesting token has no explicit routing preference.
const schedulerDefaultMode = "balanced"

const schedulerDefaultRuntimeHighWatermark = 0.8

type SchedulerClientConfig struct {
	Enabled              bool
	BaseURL              string
	BootstrapURLs        []string
	LocalURL             string
	Token                string
	SigningSecret        string
	Timeout              time.Duration
	Mode                 string
	CanaryPercent        int
	CanarySalt           string
	RuntimeHighWatermark float64
	EmergencyNative      bool
	EmergencyMaxDuration time.Duration
	EmergencyGroups      []string
	EmergencyModels      []string
	EmergencyLocalSwitch bool
}

func (c SchedulerClientConfig) URLs() []string {
	return schedulerResolvedURLs(c)
}

type SchedulerCandidate struct {
	EndpointID    string   `json:"endpoint_id"`
	ChannelID     int      `json:"channel_id"`
	KeyIndex      int      `json:"key_index"`
	Model         string   `json:"model"`
	UpstreamModel string   `json:"upstream_model,omitempty"`
	Reason        []string `json:"reason"`
}
type schedulerRequest struct {
	RequestID            string         `json:"request_id"`
	UserID               int64          `json:"user_id,omitempty"`
	TokenID              int64          `json:"token_id,omitempty"`
	Group                string         `json:"group,omitempty"`
	Model                string         `json:"model"`
	Workload             string         `json:"workload,omitempty"`
	Policy               map[string]any `json:"policy"`
	EstimatedInputTokens int            `json:"estimated_input_tokens"`
	// AllowedChannelIDs 是 new-api 按分组、禁用、能力、熔断收窄后的渠道范围，调度器只在
	// 这里面排序。必须发数组：调度器把 null 当成缺参数返回 400，空数组表示没有可用渠道。
	AllowedChannelIDs      []int  `json:"allowed_channel_ids"`
	PreferredChannelID     int    `json:"preferred_channel_id,omitempty"`
	AffinityKeyFingerprint string `json:"affinity_key_fingerprint,omitempty"`
}

type schedulerResponse struct {
	DecisionID        string `json:"decision_id"`
	ScoreVersion      string `json:"score_version,omitempty"`
	PreferenceVersion string `json:"preference_version,omitempty"`
	CatalogVersion    string `json:"catalog_version"`
	// 调度器不再占位，旧版调度器回的 reservation 字段直接忽略。
	Candidates []SchedulerCandidate `json:"candidates"`
}

var schedulerConfigState struct {
	sync.RWMutex
	loaded       bool
	testOverride bool
	config       SchedulerClientConfig
}

const (
	schedulerCircuitFailureThreshold = 3
	schedulerCircuitProbeInterval    = 10 * time.Second
	schedulerEmergencyMaxDuration    = 10 * time.Minute
)

var schedulerCircuitState struct {
	sync.Mutex
	failures       int
	failedAt       time.Time
	openedAt       time.Time
	halfOpen       bool
	probeSuccesses int
}

type schedulerEndpointCircuit struct {
	failures       int
	failedAt       time.Time
	openedAt       time.Time
	halfOpen       bool
	probeSuccesses int
}

var schedulerEndpointCircuitState struct {
	sync.Mutex
	states map[string]*schedulerEndpointCircuit
}

func resetSchedulerCircuit() {
	schedulerCircuitState.Lock()
	schedulerCircuitState.failures = 0
	schedulerCircuitState.failedAt = time.Time{}
	schedulerCircuitState.openedAt = time.Time{}
	schedulerCircuitState.halfOpen = false
	schedulerCircuitState.probeSuccesses = 0
	schedulerCircuitState.Unlock()
	schedulerEndpointCircuitState.Lock()
	schedulerEndpointCircuitState.states = make(map[string]*schedulerEndpointCircuit)
	schedulerEndpointCircuitState.Unlock()
}

func schedulerCircuitAllows(now time.Time) bool {
	schedulerCircuitState.Lock()
	defer schedulerCircuitState.Unlock()
	if schedulerCircuitState.openedAt.IsZero() {
		return true
	}
	if now.Sub(schedulerCircuitState.openedAt) < schedulerCircuitProbeInterval {
		return false
	}
	if schedulerCircuitState.halfOpen {
		return false
	}
	schedulerCircuitState.halfOpen = true
	return true
}

func schedulerCircuitFailure(now time.Time) {
	schedulerCircuitState.Lock()
	defer schedulerCircuitState.Unlock()
	if schedulerCircuitState.openedAt.IsZero() {
		if schedulerCircuitState.failedAt.IsZero() {
			schedulerCircuitState.failedAt = now
		}
		schedulerCircuitState.failures++
		if schedulerCircuitState.failures >= schedulerCircuitFailureThreshold {
			schedulerCircuitState.openedAt = now
			schedulerCircuitState.halfOpen = false
		}
		return
	}
	schedulerCircuitState.openedAt = now
	schedulerCircuitState.halfOpen = false
	schedulerCircuitState.probeSuccesses = 0
}

func schedulerCircuitSuccess(now time.Time) {
	schedulerCircuitState.Lock()
	defer schedulerCircuitState.Unlock()
	if !schedulerCircuitState.openedAt.IsZero() {
		if schedulerCircuitState.halfOpen {
			schedulerCircuitState.probeSuccesses++
			schedulerCircuitState.halfOpen = false
			if schedulerCircuitState.probeSuccesses >= 2 {
				schedulerCircuitState.failures = 0
				schedulerCircuitState.failedAt = time.Time{}
				schedulerCircuitState.openedAt = time.Time{}
				schedulerCircuitState.probeSuccesses = 0
			} else {
				// Keep the circuit open between half-open probes so only one
				// request probes Scheduler during each interval.
				schedulerCircuitState.openedAt = now
			}
		}
		return
	}
	schedulerCircuitState.failures = 0
	schedulerCircuitState.failedAt = time.Time{}
	schedulerCircuitState.probeSuccesses = 0
}

func schedulerCircuitEmergencyActive(now time.Time, maxDuration time.Duration) bool {
	schedulerCircuitState.Lock()
	defer schedulerCircuitState.Unlock()
	if schedulerCircuitState.failedAt.IsZero() {
		return false
	}
	if maxDuration <= 0 {
		maxDuration = schedulerEmergencyMaxDuration
	}
	return now.Sub(schedulerCircuitState.failedAt) < maxDuration
}

func schedulerEndpointCircuitForLocked(endpoint string) *schedulerEndpointCircuit {
	if schedulerEndpointCircuitState.states == nil {
		schedulerEndpointCircuitState.states = make(map[string]*schedulerEndpointCircuit)
	}
	circuit, ok := schedulerEndpointCircuitState.states[endpoint]
	if !ok {
		circuit = &schedulerEndpointCircuit{}
		schedulerEndpointCircuitState.states[endpoint] = circuit
	}
	return circuit
}

func schedulerEndpointCircuitAllows(endpoint string, now time.Time) bool {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return false
	}
	schedulerEndpointCircuitState.Lock()
	defer schedulerEndpointCircuitState.Unlock()
	circuit := schedulerEndpointCircuitForLocked(endpoint)
	if circuit.openedAt.IsZero() {
		return true
	}
	if now.Sub(circuit.openedAt) < schedulerCircuitProbeInterval {
		return false
	}
	if circuit.halfOpen {
		return false
	}
	circuit.halfOpen = true
	return true
}

func schedulerEndpointCircuitFailure(endpoint string, now time.Time) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return
	}
	schedulerEndpointCircuitState.Lock()
	defer schedulerEndpointCircuitState.Unlock()
	circuit := schedulerEndpointCircuitForLocked(endpoint)
	if circuit.openedAt.IsZero() {
		if circuit.failedAt.IsZero() {
			circuit.failedAt = now
		}
		circuit.failures++
		if circuit.failures >= schedulerCircuitFailureThreshold {
			circuit.openedAt = now
			circuit.halfOpen = false
		}
		return
	}
	circuit.openedAt = now
	circuit.halfOpen = false
	circuit.probeSuccesses = 0
}

func schedulerEndpointCircuitSuccess(endpoint string, now time.Time) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return
	}
	schedulerEndpointCircuitState.Lock()
	defer schedulerEndpointCircuitState.Unlock()
	circuit := schedulerEndpointCircuitForLocked(endpoint)
	if !circuit.openedAt.IsZero() {
		if circuit.halfOpen {
			circuit.probeSuccesses++
			circuit.halfOpen = false
			if circuit.probeSuccesses >= 2 {
				circuit.failures = 0
				circuit.failedAt = time.Time{}
				circuit.openedAt = time.Time{}
				circuit.probeSuccesses = 0
			} else {
				circuit.openedAt = now
			}
		}
		return
	}
	circuit.failures = 0
	circuit.failedAt = time.Time{}
	circuit.probeSuccesses = 0
}

func schedulerNormalizedURLs(values ...string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func schedulerResolvedURLs(config SchedulerClientConfig) []string {
	values := make([]string, 0, 2+len(config.BootstrapURLs))
	if strings.TrimSpace(config.LocalURL) != "" {
		values = append(values, config.LocalURL)
	}
	values = append(values, config.BootstrapURLs...)
	if len(values) == 0 && strings.TrimSpace(config.BaseURL) != "" {
		values = append(values, config.BaseURL)
	}
	return schedulerNormalizedURLs(values...)
}

func schedulerOptionURLs(key, envKey string) []string {
	raw := schedulerOption(key, envKey, "")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, strings.TrimRight(value, "/"))
		}
	}
	return schedulerNormalizedURLs(result...)
}

func isTransientSchedulerError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	// A Scheduler (or an intermediate proxy such as socat) may accept the TCP
	// connection and close it before returning HTTP response headers while the
	// node is stopping. net/http reports that condition as EOF. It is a
	// transport failure and should fall back just like connection refused/reset.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, ErrSchedulerTemporarilyUnavailable) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "scheduler status 502") ||
		strings.Contains(message, "scheduler status 503") ||
		strings.Contains(message, "scheduler status 504")
}

func SchedulerClient() SchedulerClientConfig {
	schedulerConfigState.RLock()
	if schedulerConfigState.testOverride {
		c := schedulerConfigState.config
		schedulerConfigState.RUnlock()
		return c
	}
	schedulerConfigState.RUnlock()
	return schedulerClientConfigFromOptions()
}

// schedulerClientConfigFromOptions builds a fresh production configuration on
// every call. OptionMap is already protected by its RWMutex and is refreshed by
// SyncOptions on every node, so mode/canary/emergency changes cannot remain
// trapped in a stale process-local SchedulerClient cache.
func schedulerClientConfigFromOptions() SchedulerClientConfig {
	timeout := time.Duration(schedulerOptionInt("SchedulerShadowTimeoutMS", "SCHEDULER_SHADOW_TIMEOUT_MS", 100)) * time.Millisecond
	canaryPercent, _ := strconv.Atoi(strings.TrimSpace(schedulerOption("SchedulerCanaryPercent", "SCHEDULER_CANARY_PERCENT", "0")))
	canarySalt := strings.TrimSpace(schedulerOption("SchedulerCanarySalt", "SCHEDULER_CANARY_SALT", "scheduler-v2"))
	if canarySalt == "" {
		canarySalt = "scheduler-v2"
	}
	mode := strings.ToLower(strings.TrimSpace(schedulerOption("SchedulerMode", "SCHEDULER_MODE", "shadow")))
	if mode == "" {
		mode = "shadow"
	}
	highWatermark := schedulerOptionFloat("SchedulerRuntimeHighWatermark", "SCHEDULER_RUNTIME_HIGH_WATERMARK", schedulerDefaultRuntimeHighWatermark)
	emergencyMaxDuration := time.Duration(schedulerOptionInt("SchedulerEmergencyMaxDurationSeconds", "SCHEDULER_EMERGENCY_MAX_DURATION_SECONDS", int(schedulerEmergencyMaxDuration/time.Second))) * time.Second
	if emergencyMaxDuration <= 0 {
		emergencyMaxDuration = schedulerEmergencyMaxDuration
	}
	bootstrapURLs := schedulerOptionList("SchedulerBootstrapURLs")
	// Local Scheduler 地址故意只从 .env 读取,不落 DB、不受 OptionMap 覆盖:
	// 这是每个节点的本机连接地址,应由部署环境决定,而非通过管理页面持久化到
	// 共享数据库(否则多节点会互相覆盖成同一个本机地址)。
	localURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SCHEDULER_LOCAL_URL")), "/")
	baseURL := ""
	switch {
	case localURL != "":
		baseURL = localURL
	case len(bootstrapURLs) > 0:
		baseURL = bootstrapURLs[0]
	}
	return SchedulerClientConfig{
		Enabled:       schedulerOptionBool("SchedulerEnabled", "SCHEDULER_ENABLED", false),
		BaseURL:       baseURL,
		BootstrapURLs: bootstrapURLs,
		LocalURL:      localURL,
		Token:         schedulerOption("SchedulerToken", "SCHEDULER_TOKEN", ""),
		SigningSecret: schedulerOption("SchedulerSigningSecret", "SCHEDULER_SIGNING_SECRET", ""),
		Timeout:       timeout, Mode: mode, CanaryPercent: canaryPercent, CanarySalt: canarySalt,
		RuntimeHighWatermark: highWatermark,
		// Central enablement is persisted in OptionMap. The local process
		// switch below is intentionally env-only and must be enabled too.
		EmergencyNative:      schedulerOptionBool("SchedulerEmergencyNativeRouting", "", false),
		EmergencyMaxDuration: emergencyMaxDuration,
		EmergencyGroups:      schedulerOptionList("SchedulerEmergencyGroups"),
		EmergencyModels:      schedulerOptionList("SchedulerEmergencyModels"),
		EmergencyLocalSwitch: strings.EqualFold(strings.TrimSpace(os.Getenv("SCHEDULER_EMERGENCY_NATIVE_ROUTING")), "true") || os.Getenv("SCHEDULER_EMERGENCY_NATIVE_ROUTING") == "1",
	}
}

func schedulerOption(key, envKey, fallback string) string {
	common.OptionMapRWMutex.RLock()
	value, ok := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	if ok {
		return value
	}
	if value = strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	return fallback
}

func schedulerOptionInt(key, envKey string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(schedulerOption(key, envKey, "")))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func schedulerOptionBool(key, envKey string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(schedulerOption(key, envKey, "")))
	if value == "true" || value == "1" {
		return true
	}
	if value == "false" || value == "0" {
		return false
	}
	return fallback
}

func schedulerOptionFloat(key, envKey string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(schedulerOption(key, envKey, "")), 64)
	if err != nil || value <= 0 || value >= 1 {
		return fallback
	}
	return value
}

func schedulerOptionList(key string) []string {
	value := schedulerOption(key, "", "")
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

type schedulerRequestOutcome struct {
	endpoint string
	status   int
	body     []byte
	err      error
}

func schedulerDecisionError(statusCode int, body []byte) *SchedulerDecisionError {
	var response schedulerErrorResponse
	if len(body) == 0 || common.Unmarshal(body, &response) != nil {
		return nil
	}
	message := strings.TrimSpace(response.Error)
	switch message {
	case "no candidate endpoint", "policy snapshot hard expired", "policy snapshot catalog mismatch":
		return &SchedulerDecisionError{StatusCode: statusCode, Message: message}
	default:
		return nil
	}
}

func schedulerDoRequestWithFallback(ctx context.Context, config SchedulerClientConfig, method, path string, payload []byte, includeAuth bool) (schedulerRequestOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoints := config.URLs()
	if len(endpoints) == 0 {
		return schedulerRequestOutcome{}, ErrSchedulerTemporarilyUnavailable
	}
	if config.Timeout <= 0 {
		config.Timeout = 100 * time.Millisecond
	}
	client := &http.Client{Timeout: config.Timeout}
	var lastErr error
	var transientFailures int
	now := time.Now()
	for endpointIndex, endpoint := range endpoints {
		attempt := endpointIndex + 1
		hasFallback := attempt < len(endpoints)
		if !schedulerEndpointCircuitAllows(endpoint, now) {
			lastErr = ErrSchedulerTemporarilyUnavailable
			logger.LogWarn(ctx, "scheduler request endpoint skipped: path=%s endpoint=%s attempt=%d/%d reason=circuit_open fallback_available=%t", path, endpoint, attempt, len(endpoints), hasFallback)
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, config.Timeout)
		req, err := http.NewRequestWithContext(attemptCtx, method, endpoint+path, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return schedulerRequestOutcome{}, err
		}
		if includeAuth && config.Token != "" {
			req.Header.Set("Authorization", "Bearer "+config.Token)
		}
		req.Header.Set("Content-Type", "application/json")
		if includeAuth {
			signSchedulerRequest(req, config.SigningSecret, payload)
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			if isTransientSchedulerError(err) {
				schedulerEndpointCircuitFailure(endpoint, time.Now())
				transientFailures++
				lastErr = fmt.Errorf("%w: %v", ErrSchedulerTemporarilyUnavailable, err)
				logger.LogWarn(ctx, "scheduler request endpoint failed: path=%s endpoint=%s attempt=%d/%d phase=request error=%v fallback_available=%t", path, endpoint, attempt, len(endpoints), err, hasFallback)
				continue
			}
			logger.LogWarn(ctx, "scheduler request failed without fallback: path=%s endpoint=%s attempt=%d/%d phase=request error=%v", path, endpoint, attempt, len(endpoints), err)
			return schedulerRequestOutcome{}, err
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			schedulerEndpointCircuitFailure(endpoint, time.Now())
			transientFailures++
			lastErr = fmt.Errorf("%w: %v", ErrSchedulerTemporarilyUnavailable, readErr)
			logger.LogWarn(ctx, "scheduler request endpoint failed: path=%s endpoint=%s attempt=%d/%d phase=response_body error=%v fallback_available=%t", path, endpoint, attempt, len(endpoints), readErr, hasFallback)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			schedulerEndpointCircuitSuccess(endpoint, time.Now())
			schedulerCircuitSuccess(time.Now())
			if attempt > 1 {
				logger.LogInfo(ctx, fmt.Sprintf("scheduler request fallback succeeded: path=%s endpoint=%s attempt=%d/%d prior_failures=%d", path, endpoint, attempt, len(endpoints), transientFailures))
			}
			return schedulerRequestOutcome{endpoint: endpoint, status: resp.StatusCode, body: body}, nil
		}
		if resp.StatusCode >= 500 {
			if decisionErr := schedulerDecisionError(resp.StatusCode, body); decisionErr != nil {
				lastErr = decisionErr
				logger.LogWarn(ctx, "scheduler request rejected: path=%s endpoint=%s attempt=%d/%d phase=response_status status=%d error=%v fallback_available=%t", path, endpoint, attempt, len(endpoints), resp.StatusCode, decisionErr, hasFallback)
				continue
			}
			schedulerEndpointCircuitFailure(endpoint, time.Now())
			transientFailures++
			lastErr = fmt.Errorf("%w: scheduler status %d", ErrSchedulerTemporarilyUnavailable, resp.StatusCode)
			logger.LogWarn(ctx, "scheduler request endpoint failed: path=%s endpoint=%s attempt=%d/%d phase=response_status status=%d fallback_available=%t", path, endpoint, attempt, len(endpoints), resp.StatusCode, hasFallback)
			continue
		}
		logger.LogWarn(ctx, "scheduler request rejected without fallback: path=%s endpoint=%s attempt=%d/%d status=%d", path, endpoint, attempt, len(endpoints), resp.StatusCode)
		return schedulerRequestOutcome{endpoint: endpoint, status: resp.StatusCode, body: body, err: fmt.Errorf("scheduler status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}, nil
	}
	if transientFailures > 0 {
		schedulerCircuitFailure(time.Now())
	}
	if lastErr != nil {
		logger.LogWarn(ctx, "scheduler request all endpoints failed: path=%s endpoints=%d transient_failures=%d error=%v", path, len(endpoints), transientFailures, lastErr)
		return schedulerRequestOutcome{}, lastErr
	}
	logger.LogWarn(ctx, "scheduler request unavailable: path=%s endpoints=%d reason=all_endpoint_circuits_open", path, len(endpoints))
	return schedulerRequestOutcome{}, ErrSchedulerTemporarilyUnavailable
}

// ReloadSchedulerClient causes the next request to read the latest persisted
// configuration. It is used by the admin configuration endpoint after a
// successful atomic options update.
func ReloadSchedulerClient() {
	schedulerConfigState.Lock()
	schedulerConfigState.loaded = false
	schedulerConfigState.testOverride = false
	schedulerConfigState.Unlock()
	resetSchedulerCircuit()
}

// ConfigureSchedulerClientForTest allows isolated middleware tests to opt in
// without mutating process environment. It is intentionally not used by boot.
func ConfigureSchedulerClientForTest(config SchedulerClientConfig) {
	schedulerConfigState.Lock()
	schedulerConfigState.config = config
	schedulerConfigState.loaded = true
	schedulerConfigState.testOverride = true
	schedulerConfigState.Unlock()
	resetSchedulerCircuit()
}

func SchedulerEnforced() bool {
	c := SchedulerClient()
	return c.Enabled && c.Mode == "enforced"
}

// SchedulerEnforcedForRequest applies the stable-hash canary gate. The hash
// input is request-scoped and contains no prompt or credential material.
func SchedulerEnforcedForRequest(c *gin.Context) bool {
	// Once an enforced request has started, keep its Scheduler lifecycle
	// enabled so an operator toggling kill switch cannot strand an existing
	// Reservation before its attempt/release is reported.
	if c != nil {
		if value, exists := c.Get(string(constant.ContextKeySchedulerEnforcedRequest)); exists {
			if enforced, ok := value.(bool); ok {
				return enforced
			}
		}
	}
	if SchedulerKillSwitchActive() {
		return false
	}
	config := SchedulerClient()
	if !config.Enabled {
		return false
	}
	result := false
	if config.Mode == "enforced" {
		result = true
	} else if config.Mode != "canary" || config.CanaryPercent <= 0 {
		result = false
	} else {
		percent := config.CanaryPercent
		if percent > 100 {
			percent = 100
		}
		key := ""
		if c != nil {
			if userID := c.GetInt("id"); userID > 0 {
				key = "user:" + strconv.Itoa(userID)
			} else if tokenID := c.GetInt("token_id"); tokenID > 0 {
				key = "token:" + strconv.Itoa(tokenID)
			} else {
				key = "request:" + c.GetString(common.RequestIdKey)
			}
		}
		if key != "" {
			h := fnv.New32a()
			_, _ = h.Write([]byte(config.CanarySalt))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(key))
			result = int(h.Sum32()%100) < percent
		}
	}
	if c != nil && result {
		c.Set(string(constant.ContextKeySchedulerEnforcedRequest), true)
	}
	return result
}

// SchedulerKillSwitchActive is intentionally read on every request. The
// management API updates OptionMap synchronously, so operators can stop new
// Scheduler decisions without restarting the process or reloading the client.
func SchedulerKillSwitchActive() bool {
	common.OptionMapRWMutex.RLock()
	value, ok := common.OptionMap["SchedulerKillSwitch"]
	common.OptionMapRWMutex.RUnlock()
	if !ok || strings.TrimSpace(value) == "" {
		value = os.Getenv("SCHEDULER_KILL_SWITCH")
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// IsSchedulerTransientUnavailable distinguishes infrastructure failures from
// policy, permission, capacity, and invalid-response errors. Only the former
// may enter emergency-native routing.
func IsSchedulerTransientUnavailable(err error) bool {
	return isTransientSchedulerError(err)
}

// SchedulerDecisionErrorMessage returns a Scheduler rejection that is safe and
// useful to expose to the caller. Transport failures and arbitrary upstream
// response bodies deliberately remain internal.
func SchedulerDecisionErrorMessage(err error) (string, bool) {
	var decisionErr *SchedulerDecisionError
	if !errors.As(err, &decisionErr) || strings.TrimSpace(decisionErr.Message) == "" {
		return "", false
	}
	return decisionErr.Message, true
}

// SchedulerEmergencyNativeAllowed checks the central option, the local
// process switch, the circuit window, and optional model/group allowlists.
// The default is deliberately false so an enforced request fails closed.
func SchedulerEmergencyNativeAllowed(modelName, group string) bool {
	config := SchedulerClient()
	if !config.EmergencyNative || !config.EmergencyLocalSwitch || !schedulerCircuitEmergencyActive(time.Now(), config.EmergencyMaxDuration) {
		return false
	}
	if len(config.EmergencyModels) > 0 && !containsString(config.EmergencyModels, modelName) {
		return false
	}
	if len(config.EmergencyGroups) > 0 && !containsString(config.EmergencyGroups, group) {
		return false
	}
	return true
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

// MarkSchedulerEmergency records the request-level degraded routing mode.
// The original error is retained for diagnostics and is never exposed as a
// native routing success signal.
func MarkSchedulerEmergency(c *gin.Context, err error) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeySchedulerEmergencyNative, true)
	common.SetContextKey(c, constant.ContextKeySchedulerDegradedReason, "scheduler_unavailable")
	if err != nil {
		common.SetContextKey(c, constant.ContextKeySchedulerFailureReason, err.Error())
	}
}

func SchedulerEmergencyNativeActive(c *gin.Context) bool {
	return c != nil && common.GetContextKeyBool(c, constant.ContextKeySchedulerEmergencyNative)
}

// RecordSchedulerUsage stores the usage reported by an upstream handler on
// the request context. Relay reads it after the handler returns: 计数池
// （channel_capacity.Finish）按它把这次尝试的实际 TPM 记账，OTel 按 stream 标记
// 判断流是否已经开始。名字是历史遗留，调度器本身已经不收尝试结果。
// Missing/negative values are normalized to zero.
func RecordSchedulerUsage(c *gin.Context, usage *dto.Usage, streamStarted bool) {
	if c == nil {
		return
	}
	if usage == nil {
		common.SetContextKey(c, constant.ContextKeySchedulerInputTokens, 0)
		common.SetContextKey(c, constant.ContextKeySchedulerOutputTokens, 0)
		if streamStarted {
			common.SetContextKey(c, constant.ContextKeySchedulerStreamStarted, true)
		}
		return
	}
	input := usage.InputTokens
	if input <= 0 {
		input = usage.PromptTokens
	}
	output := usage.OutputTokens
	if output <= 0 {
		output = usage.CompletionTokens
	}
	if input < 0 {
		input = 0
	}
	if output < 0 {
		output = 0
	}
	common.SetContextKey(c, constant.ContextKeySchedulerInputTokens, input)
	common.SetContextKey(c, constant.ContextKeySchedulerOutputTokens, output)
	if streamStarted {
		common.SetContextKey(c, constant.ContextKeySchedulerStreamStarted, true)
	}
}

// ResetSchedulerAttemptMetrics clears usage and stream timing from the
// previous upstream attempt. Relay retries reuse one Gin context; retaining
// these fields would attribute a failed/silent retry the prior attempt's
// tokens or stream state.
func ResetSchedulerAttemptMetrics(c *gin.Context) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeySchedulerInputTokens, 0)
	common.SetContextKey(c, constant.ContextKeySchedulerOutputTokens, 0)
	common.SetContextKey(c, constant.ContextKeySchedulerStreamStarted, false)
}

func applySchedulerCandidateContext(c *gin.Context, candidate SchedulerCandidate) {
	common.SetContextKey(c, constant.ContextKeySchedulerKeyIndex, candidate.KeyIndex)
	if candidate.UpstreamModel != "" {
		common.SetContextKey(c, constant.ContextKeySchedulerUpstreamModel, candidate.UpstreamModel)
	}
}

// SchedulerCandidateForInitial returns the first Scheduler candidate for the
// initial attempt. It is intentionally inert unless Enforced (including a
// selected Canary request) is active; Shadow keeps native routing authoritative.
func SchedulerCandidateForInitial(c *gin.Context) (SchedulerCandidate, bool) {
	if !SchedulerEnforcedForRequest(c) {
		return SchedulerCandidate{}, false
	}
	candidates, ok := common.GetContextKeyType[[]SchedulerCandidate](c, constant.ContextKeySchedulerCandidates)
	if !ok || len(candidates) == 0 {
		return SchedulerCandidate{}, false
	}
	candidate := candidates[0]
	applySchedulerCandidateContext(c, candidate)
	return candidate, true
}

// SchedulerCandidateForRetry is intentionally inert unless Enforced is
// explicitly selected. It only selects metadata already obtained by Shadow;
// Reservation re-acquisition per retry is a separate gate.
func SchedulerCandidateForRetry(c *gin.Context, retry int) (SchedulerCandidate, bool) {
	if !SchedulerEnforcedForRequest(c) || retry < 0 {
		return SchedulerCandidate{}, false
	}
	candidates, ok := common.GetContextKeyType[[]SchedulerCandidate](c, constant.ContextKeySchedulerCandidates)
	if !ok || retry >= len(candidates) {
		return SchedulerCandidate{}, false
	}
	candidate := candidates[retry]
	applySchedulerCandidateContext(c, candidate)
	return candidate, true
}

// schedulerEffectivePolicy builds the Scheduler policy for the current request
// from the platform defaults and the authenticated token's routing preference.
func schedulerEffectivePolicy(c *gin.Context) (map[string]any, error) {
	// Default to the balanced routing policy so a request always carries a valid
	// mode even when the token has no routing preference configured. The
	// token-level preference (one per API key, model-agnostic) may only choose
	// the sort mode. 重试次数由 new-api 自己定，不发给调度器。
	policy := map[string]any{"mode": schedulerDefaultMode}
	pref, ok := common.GetContextKeyType[*dto.RoutingPreference](c, constant.ContextKeyTokenRoutingPreference)
	if !ok || pref == nil {
		return policy, nil
	}
	if mode := strings.ToLower(strings.TrimSpace(pref.Mode)); mode != "" {
		policy["mode"] = mode
	}
	return policy, nil
}

// RunSchedulerShadow is best-effort by design: errors never alter native
// channel selection. A successful response is stored only as request metadata;
// Enforced candidate consumption is a separate, explicitly gated phase.
func RunSchedulerShadow(c *gin.Context, modelName, group string) (err error) {
	if SchedulerKillSwitchActive() {
		return nil
	}
	config := SchedulerClient()
	if !config.Enabled || len(config.URLs()) == 0 || config.Token == "" {
		return nil
	}
	enforced := SchedulerEnforcedForRequest(c)
	mode := "shadow"
	if enforced {
		mode = "enforced"
	}
	otelRuntime := observability.FromContext(c.Request.Context())
	decideCtx, decideSpan := otelRuntime.StartSchedulerDecision(c.Request.Context())
	decide := observability.Observation{Metadata: map[string]string{"mode": mode}}
	defer func() {
		if err != nil {
			// A shadow decision never affects routing, so its failure is only
			// a degradation; an enforced one fails the request.
			if enforced {
				decide.Err = err
			} else {
				decide.Warning = err.Error()
			}
		}
		otelRuntime.FinishObservation(decideSpan, decide)
	}()
	if !schedulerCircuitAllows(time.Now()) {
		return ErrSchedulerTemporarilyUnavailable
	}
	if config.Timeout <= 0 {
		config.Timeout = 100 * time.Millisecond
	}
	policy, err := schedulerEffectivePolicy(c)
	if err != nil {
		return err
	}
	policy["runtime_high_watermark"] = config.RuntimeHighWatermark
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
		c.Set(common.RequestIdKey, requestID)
	}
	// RoutingInputTokens 已限制在 int32 范围内，转 int 不会溢出。调度器拿它判断 TPM 加上这次会不会超额。
	estimatedInput := int(RoutingInputTokens(c, modelName))
	body := schedulerRequest{RequestID: requestID, UserID: int64(c.GetInt("id")), TokenID: int64(c.GetInt("token_id")), Group: group, Model: modelName, Workload: common.GetContextKeyString(c, constant.ContextKeySchedulerWorkload), Policy: policy, EstimatedInputTokens: estimatedInput, AllowedChannelIDs: []int{}, PreferredChannelID: common.GetContextKeyInt(c, constant.ContextKeySchedulerAffinityChannelID)}
	if affinity, ok := GetChannelAffinityStatsContext(c); ok {
		body.AffinityKeyFingerprint = affinity.KeyFingerprint
	}
	// 没有可用渠道时 distributor 存进来的可能是 nil 切片，追加到空数组上才能发成 []。
	// 范围没登记（理论上不会发生）时也发 []，宁可无候选也不让调度器在全部渠道里选。
	if allowed, ok := common.GetContextKeyType[[]int](c, constant.ContextKeySchedulerAllowedChannelIDs); ok {
		body.AllowedChannelIDs = append(body.AllowedChannelIDs, allowed...)
	}
	decide.Input = body
	payload, err := common.Marshal(body)
	if err != nil {
		return err
	}
	outcome, err := schedulerDoRequestWithFallback(decideCtx, config, http.MethodPost, "/v1/schedule", payload, true)
	if err != nil {
		return err
	}
	if outcome.err != nil {
		if outcome.status == http.StatusBadGateway || outcome.status == http.StatusServiceUnavailable || outcome.status == http.StatusGatewayTimeout {
			return fmt.Errorf("%w: %v", ErrSchedulerTemporarilyUnavailable, outcome.err)
		}
		return outcome.err
	}
	var scheduled schedulerResponse
	if err := common.DecodeJson(bytes.NewReader(outcome.body), &scheduled); err != nil {
		return err
	}
	decide.Output = scheduled
	decide.Metadata["decision_id"] = scheduled.DecisionID
	if scheduled.DecisionID == "" || scheduled.CatalogVersion == "" || len(scheduled.Candidates) == 0 {
		return fmt.Errorf("scheduler returned incomplete response")
	}
	seen := make(map[string]struct{}, len(scheduled.Candidates))
	for _, candidate := range scheduled.Candidates {
		if candidate.EndpointID == "" || candidate.ChannelID < 0 || candidate.KeyIndex < 0 || candidate.Model == "" || len(candidate.Reason) == 0 {
			return fmt.Errorf("scheduler returned invalid candidate")
		}
		if _, exists := seen[candidate.EndpointID]; exists {
			return fmt.Errorf("scheduler returned duplicate candidate")
		}
		seen[candidate.EndpointID] = struct{}{}
	}
	common.SetContextKey(c, constant.ContextKeySchedulerCandidates, scheduled.Candidates)
	common.SetContextKey(c, constant.ContextKeySchedulerDecisionID, scheduled.DecisionID)
	common.SetContextKey(c, constant.ContextKeySchedulerScoreVersion, scheduled.ScoreVersion)
	common.SetContextKey(c, constant.ContextKeySchedulerPreferenceVersion, scheduled.PreferenceVersion)
	match := false
	for _, candidate := range scheduled.Candidates {
		if candidate.ChannelID == common.GetContextKeyInt(c, constant.ContextKeyChannelId) {
			match = true
			break
		}
	}
	common.SetContextKey(c, constant.ContextKeySchedulerShadowMatch, match)
	if !enforced {
		decide.Metadata["shadow_match"] = strconv.FormatBool(match)
	}
	schedulerCircuitSuccess(time.Now())
	return nil
}

func signSchedulerRequest(req *http.Request, secret string, payload []byte) {
	if req == nil || strings.TrimSpace(secret) == "" {
		return
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(req.Method))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(req.URL.Path))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write(payload)
	req.Header.Set("X-Scheduler-Timestamp", timestamp)
	req.Header.Set("X-Scheduler-Signature", hex.EncodeToString(mac.Sum(nil)))
}
