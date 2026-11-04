package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

// SchedulerPreviewTarget 是要问调度器的一个候选：真正服务请求的模型名、它在 new-api 这边的
// 可用渠道（与正式调度发的 allowed_channel_ids 同一口径）和会话亲和偏好的渠道。
type SchedulerPreviewTarget struct {
	Model              string
	AllowedChannelIDs  []int
	PreferredChannelID int
}

// SchedulerPreviewResult 是调度器对一个候选的回答。
type SchedulerPreviewResult struct {
	Status    string `json:"status"`
	ChannelID int    `json:"channel_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// 调度器预览里一个候选的状态，与调度器 sdk 的 PreviewStatus* 一致。
const (
	SchedulerPreviewOK          = "ok"
	SchedulerPreviewNoCandidate = "no_candidate"
	SchedulerPreviewNotCached   = "not_cached"
)

type schedulerPreviewModel struct {
	Model                string `json:"model"`
	AllowedChannelIDs    []int  `json:"allowed_channel_ids"`
	PreferredChannelID   int    `json:"preferred_channel_id,omitempty"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
}

type schedulerPreviewRequest struct {
	RequestID string                  `json:"request_id"`
	UserID    int64                   `json:"user_id,omitempty"`
	TokenID   int64                   `json:"token_id,omitempty"`
	Group     string                  `json:"group,omitempty"`
	Workload  string                  `json:"workload,omitempty"`
	Policy    map[string]any          `json:"policy"`
	Models    []schedulerPreviewModel `json:"models"`
}

type schedulerPreviewResponse struct {
	Results []SchedulerPreviewResult `json:"results"`
}

// schedulerPreviewUnsupportedUntil 是调度器回 404（旧版本没有 /v1/preview）之后不再问的截止时间
// （Unix 纳秒），免得每次比价都多一次必然失败的往返。
var schedulerPreviewUnsupportedUntil atomic.Int64

const schedulerPreviewUnsupportedTTL = time.Minute

var schedulerPreviewHTTPClient = &http.Client{}

// PreviewScheduler 问调度器：这几个候选现在各自会被调度到哪个渠道。只在这次请求由调度器决定渠道
// （强制模式或金丝雀命中）时有意义，其余情况返回 nil，调用方按原生规则估算。
//
// 结果与 targets 一一对应。调度器没开、熔断没闭合、不支持预览或请求失败时都返回 nil，不影响请求；
// 预览不占熔断的半开探测名额，失败也不计入熔断，正式调度照常走自己的判断。
// 整次预览共用一个截止时间（SchedulerShadowTimeoutMS，默认 100ms），不像正式调度那样每个地址各算一次。
func PreviewScheduler(c *gin.Context, group string, targets []SchedulerPreviewTarget) []SchedulerPreviewResult {
	if len(targets) == 0 || !SchedulerEnforcedForRequest(c) || SchedulerKillSwitchActive() {
		return nil
	}
	if time.Now().UnixNano() < schedulerPreviewUnsupportedUntil.Load() {
		return nil
	}
	config := SchedulerClient()
	if !config.Enabled || len(config.URLs()) == 0 || config.Token == "" || !schedulerCircuitClosed() {
		return nil
	}
	policy, err := schedulerEffectivePolicy(c)
	if err != nil {
		return nil
	}
	policy["runtime_high_watermark"] = config.RuntimeHighWatermark
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	body := schedulerPreviewRequest{
		RequestID: requestID, UserID: int64(c.GetInt("id")), TokenID: int64(c.GetInt("token_id")), Group: group,
		Workload: common.GetContextKeyString(c, constant.ContextKeySchedulerWorkload), Policy: policy,
		Models: make([]schedulerPreviewModel, len(targets)),
	}
	for i, target := range targets {
		body.Models[i] = schedulerPreviewModel{
			Model: target.Model, AllowedChannelIDs: append([]int{}, target.AllowedChannelIDs...),
			PreferredChannelID: target.PreferredChannelID, EstimatedInputTokens: int(RoutingInputTokens(c, target.Model)),
		}
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	for _, endpoint := range config.URLs() {
		if !schedulerEndpointCircuitClosed(endpoint) {
			continue
		}
		results, err := requestSchedulerPreview(ctx, config, endpoint, payload)
		if errors.Is(err, errSchedulerPreviewUnsupported) {
			schedulerPreviewUnsupportedUntil.Store(time.Now().Add(schedulerPreviewUnsupportedTTL).UnixNano())
			return nil
		}
		if err == nil && len(results) == len(targets) {
			return results
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	return nil
}

var errSchedulerPreviewUnsupported = errors.New("scheduler does not support preview")

func requestSchedulerPreview(ctx context.Context, config SchedulerClientConfig, endpoint string, payload []byte) ([]SchedulerPreviewResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/preview", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")
	signSchedulerRequest(req, config.SigningSecret, payload)
	resp, err := schedulerPreviewHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errSchedulerPreviewUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scheduler preview status %d", resp.StatusCode)
	}
	var decoded schedulerPreviewResponse
	if err := common.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded.Results, nil
}

// schedulerCircuitClosed 只读地看全局熔断是否闭合，不像 schedulerCircuitAllows 那样占用半开探测名额。
func schedulerCircuitClosed() bool {
	schedulerCircuitState.Lock()
	defer schedulerCircuitState.Unlock()
	return schedulerCircuitState.openedAt.IsZero()
}

// schedulerEndpointCircuitClosed 只读地看某个调度器地址的熔断是否闭合。
func schedulerEndpointCircuitClosed(endpoint string) bool {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	schedulerEndpointCircuitState.Lock()
	defer schedulerEndpointCircuitState.Unlock()
	circuit, ok := schedulerEndpointCircuitState.states[endpoint]
	return !ok || circuit.openedAt.IsZero()
}
