package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"

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

// 调度器预览里一个候选的状态。前四个与调度器 sdk 的 PreviewStatus* 一致；
// SchedulerPreviewOverLimit 是 new-api 自己填的：一次最多问 schedulerPreviewMaxModels 个。
const (
	SchedulerPreviewOK          = "ok"
	SchedulerPreviewNoCandidate = "no_candidate"
	SchedulerPreviewNotCached   = "not_cached"
	SchedulerPreviewError       = "error"
	SchedulerPreviewOverLimit   = "over_limit"
)

// schedulerPreviewMaxModels 与调度器 sdk 的 MaxPreviewModels 一致，超过时调度器整批回 400。
const schedulerPreviewMaxModels = 32

// SchedulerPreview 是一次预览的结果。Status 说明问没问、为什么没拿到回答：为空表示没配调度器，
// 不留痕；SchedulerPreviewAnswered 时 Results 与 targets 一一对应，其余情况 Results 为空，
// 调用方全部按原生规则估算。
type SchedulerPreview struct {
	Status string
	// HTTPStatus 是 SchedulerPreviewFailed 时调度器回的状态码，连不上时为 0。
	HTTPStatus int
	Results    []SchedulerPreviewResult
}

// 一次预览的状态。
const (
	SchedulerPreviewAnswered      = "answered"              // 调度器回答了，每个候选的状态看 Results
	SchedulerPreviewNotDecided    = "not_scheduler_decided" // 这次请求不由调度器定渠道：影子模式、金丝雀没命中或紧急停用
	SchedulerPreviewNotConfigured = "not_configured"        // 由调度器定渠道，但没配地址或令牌
	SchedulerPreviewCircuitOpen   = "circuit_open"          // 调度器熔断开着，没有能问的地址
	SchedulerPreviewUnsupported   = "unsupported"           // 能问的地址都回 404（没有 /v1/preview），一分钟内不再问那个地址
	SchedulerPreviewTimeout       = "timeout"               // 整次的截止时间到了
	SchedulerPreviewFailed        = "failed"                // 连不上，或回了 404 以外的非 200
	SchedulerPreviewBadResponse   = "bad_response"          // 回了 200，但解不开或条数不对
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

// schedulerPreviewUnsupported 记哪些调度器地址回过 404（旧版本没有 /v1/preview，或前面的
// 网关只放行了 /v1/schedule），到期前不再问那个地址，免得每次比价都多一次必然失败的往返。
// 按地址记：一个地址不支持不影响问别的地址。
var schedulerPreviewUnsupported struct {
	sync.Mutex
	until map[string]time.Time
}

const schedulerPreviewUnsupportedTTL = time.Minute

var schedulerPreviewHTTPClient = &http.Client{}

// PreviewScheduler 问调度器：这几个候选现在各自会被调度到哪个渠道。只在这次请求由调度器决定渠道
// （强制模式或金丝雀命中）时才问；返回值的 Status 说明问没问、为什么没拿到回答，写进选路留痕，
// 没拿到回答时调用方全部按原生规则估算，不影响请求。
//
// 预览不占熔断的半开探测名额，失败也不计入熔断，正式调度照常走自己的判断。地址按顺序问，
// 回 404 的地址一分钟内跳过、接着问下一个；整次预览共用一个截止时间（SchedulerShadowTimeoutMS，
// 默认 100ms），不像正式调度那样每个地址各算一次。真正问过却没拿到回答时记一条 WARN。
// 一次最多问 schedulerPreviewMaxModels 个候选，后面的候选标 SchedulerPreviewOverLimit。
func PreviewScheduler(c *gin.Context, group string, targets []SchedulerPreviewTarget) SchedulerPreview {
	config := SchedulerClient()
	if len(targets) == 0 || !config.Enabled {
		return SchedulerPreview{}
	}
	if !SchedulerEnforcedForRequest(c) || SchedulerKillSwitchActive() {
		return SchedulerPreview{Status: SchedulerPreviewNotDecided}
	}
	if len(config.URLs()) == 0 || config.Token == "" {
		return SchedulerPreview{Status: SchedulerPreviewNotConfigured}
	}
	if !schedulerCircuitClosed() {
		return SchedulerPreview{Status: SchedulerPreviewCircuitOpen}
	}
	policy, err := schedulerEffectivePolicy(c)
	if err != nil {
		return SchedulerPreview{Status: SchedulerPreviewFailed}
	}
	policy["runtime_high_watermark"] = config.RuntimeHighWatermark
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.NewRequestId()
	}
	asked := targets[:min(len(targets), schedulerPreviewMaxModels)]
	body := schedulerPreviewRequest{
		RequestID: requestID, UserID: int64(c.GetInt("id")), TokenID: int64(c.GetInt("token_id")), Group: group,
		Workload: common.GetContextKeyString(c, constant.ContextKeySchedulerWorkload), Policy: policy,
		Models: make([]schedulerPreviewModel, len(asked)),
	}
	for i, target := range asked {
		body.Models[i] = schedulerPreviewModel{
			Model: target.Model, AllowedChannelIDs: append([]int{}, target.AllowedChannelIDs...),
			PreferredChannelID: target.PreferredChannelID, EstimatedInputTokens: int(RoutingInputTokens(c, target.Model)),
		}
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return SchedulerPreview{Status: SchedulerPreviewFailed}
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 100 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	// 一个地址都没问时，状态是"熔断开着"，有地址还在 404 的冷却期里时是"不支持"；
	// 问过的地址里，最后一次的失败原因为准。
	preview := SchedulerPreview{Status: SchedulerPreviewCircuitOpen}
	attempted := false
	for _, endpoint := range config.URLs() {
		if !schedulerEndpointCircuitClosed(endpoint) {
			continue
		}
		schedulerPreviewUnsupported.Lock()
		cooling := time.Now().Before(schedulerPreviewUnsupported.until[endpoint])
		schedulerPreviewUnsupported.Unlock()
		if cooling {
			if !attempted {
				preview.Status = SchedulerPreviewUnsupported
			}
			continue
		}
		attempted = true
		results, status, err := requestSchedulerPreview(ctx, config, endpoint, requestID, payload)
		if err == nil && status == http.StatusOK && len(results) == len(asked) {
			preview = SchedulerPreview{Status: SchedulerPreviewAnswered, Results: results}
			break
		}
		if ctx.Err() != nil {
			// 客户端已经断开时不必记 WARN；截止时间到了就不再问别的地址，时间已经用完。
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				logger.LogWarn(c, common.LogText("scheduler preview at %s timed out after %s; auto prices its candidates by the native estimate", endpoint, timeout))
				return SchedulerPreview{Status: SchedulerPreviewTimeout}
			}
			return SchedulerPreview{Status: SchedulerPreviewFailed}
		}
		switch {
		case status == http.StatusNotFound:
			schedulerPreviewUnsupported.Lock()
			if schedulerPreviewUnsupported.until == nil {
				schedulerPreviewUnsupported.until = map[string]time.Time{}
			}
			schedulerPreviewUnsupported.until[endpoint] = time.Now().Add(schedulerPreviewUnsupportedTTL)
			schedulerPreviewUnsupported.Unlock()
			logger.LogWarn(c, common.LogText("scheduler preview is not served at %s (HTTP 404: the scheduler is too old, or a gateway in front of it only passes /v1/schedule); not asking that address again for %s", endpoint, schedulerPreviewUnsupportedTTL))
			preview = SchedulerPreview{Status: SchedulerPreviewUnsupported}
		case status == http.StatusOK:
			if err == nil {
				err = fmt.Errorf("%d results for %d models", len(results), len(asked))
			}
			logger.LogWarn(c, common.LogText("scheduler preview at %s returned an unusable response: %v", endpoint, err))
			preview = SchedulerPreview{Status: SchedulerPreviewBadResponse}
		case err != nil:
			logger.LogWarn(c, common.LogText("scheduler preview at %s failed: %v", endpoint, err))
			preview = SchedulerPreview{Status: SchedulerPreviewFailed, HTTPStatus: status}
		default:
			logger.LogWarn(c, common.LogText("scheduler preview at %s failed: HTTP %d", endpoint, status))
			preview = SchedulerPreview{Status: SchedulerPreviewFailed, HTTPStatus: status}
		}
	}
	if preview.Status != SchedulerPreviewAnswered {
		return preview
	}
	for i, result := range preview.Results {
		if result.Status == SchedulerPreviewError {
			// 单个模型取目录或评分出错，调度器照样回答了其它模型。
			logger.LogWarn(c, common.LogText("scheduler preview could not place %s: %s", asked[i].Model, result.Error))
		}
	}
	for range targets[len(asked):] {
		preview.Results = append(preview.Results, SchedulerPreviewResult{Status: SchedulerPreviewOverLimit})
	}
	return preview
}

// requestSchedulerPreview 向一个地址发一次预览。status 是调度器回的状态码，连不上时为 0；
// 回 200 但解不开时 err 不为空；其余非 200 只返回状态码，由调用方判断。
// 请求头带上 new-api 的请求号，调度器的访问日志能对上是哪个请求问的。
func requestSchedulerPreview(ctx context.Context, config SchedulerClientConfig, endpoint, requestID string, payload []byte) ([]SchedulerPreviewResult, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/preview", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(common.RequestIdKey, requestID)
	signSchedulerRequest(req, config.SigningSecret, payload)
	resp, err := schedulerPreviewHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, err
	}
	var decoded schedulerPreviewResponse
	if err := common.Unmarshal(data, &decoded); err != nil {
		return nil, resp.StatusCode, err
	}
	return decoded.Results, resp.StatusCode, nil
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
