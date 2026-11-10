package controller

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter/modelrank"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// RegisterScheduledSystemTasks wires the periodic channel test, upstream model
// update, auto router ranking and async task polling (Midjourney / Suno /
// video) jobs into the
// system task framework so a DB lease dedups execution across multiple master
// instances and each run is recorded as one task row. Call this before
// service.StartSystemTaskRunner.
func RegisterScheduledSystemTasks() {
	service.RegisterSystemTaskHandler(channelTestHandler{})
	service.RegisterSystemTaskHandler(channelModelRecoveryHandler{})
	service.RegisterSystemTaskHandler(capabilityGateEventCleanupHandler{})
	service.RegisterSystemTaskHandler(autoRouteLogCleanupHandler{})
	service.RegisterSystemTaskHandler(modelUpdateHandler{})
	service.RegisterSystemTaskHandler(midjourneyPollHandler{})
	service.RegisterSystemTaskHandler(asyncTaskPollHandler{})
	service.RegisterSystemTaskHandler(autoRouterRankHandler{})
	service.RegisterSystemTaskHandler(ssoUserSyncHandler{})
}

// autoRouterRankHandler regenerates the auto router's task → model lists from
// public leaderboards. One node builds them under the task lease and writes
// the options; the others pick them up through the option sync, so a new
// ranking takes effect everywhere without a restart.
type autoRouterRankHandler struct{}

func (autoRouterRankHandler) Type() string { return model.SystemTaskTypeAutoRouterRank }

func (autoRouterRankHandler) Enabled() bool {
	setting := autorouter_setting.GetAutoRouterSetting()
	return setting.Enabled && setting.RankRefreshEnabled && setting.Connections().RankKeysReady()
}

// Interval is the refresh period, or a short retry delay while the stored
// ranking predates the current task types, so a new taxonomy gets its ranking
// without waiting a whole period.
func (autoRouterRankHandler) Interval() time.Duration {
	setting := autorouter_setting.GetAutoRouterSetting()
	if !setting.RankingCurrent() {
		return autoRouterRankRetryInterval
	}
	return time.Duration(max(setting.RankRefreshIntervalHours, 1)) * time.Hour
}

const autoRouterRankRetryInterval = 10 * time.Minute

func (autoRouterRankHandler) NewPayload() any { return nil }

// Run leaves the current lists in place when any source fails, so a failed
// refresh only delays the next ranking. rank_sources is written last and
// marks a complete update.
func (autoRouterRankHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	connections := autorouter_setting.GetAutoRouterSetting().Connections()
	cfg := modelrank.DefaultConfig()
	cfg.AAKey = connections.ArtificialAnalysisKey.Value
	cfg.DAKey = connections.DesignArenaKey.Value
	cfg.OpenRouterKey = connections.OpenRouterKey.Value
	mapping, err := modelrank.Build(ctx, cfg)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	taskScores := make(map[string]map[string]float64, len(mapping.Evidence))
	for taskType, entries := range mapping.Evidence {
		taskScores[taskType] = make(map[string]float64, len(entries))
		for _, entry := range entries {
			taskScores[taskType][entry.Model] = entry.Score
		}
	}
	options := []struct {
		key   string
		value any
	}{
		{"autorouter_setting.task_models", mapping.TaskModels},
		{"autorouter_setting.task_scores", taskScores},
		{"autorouter_setting.default_models", mapping.DefaultModels},
		{"autorouter_setting.rank_sources", mapping.Sources},
	}
	for _, option := range options {
		value, err := common.Marshal(option.value)
		if err == nil {
			err = model.UpdateOption(option.key, string(value))
		}
		if err != nil {
			finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
			return
		}
	}
	summary := map[string]any{"rank_sources": mapping.Sources, "default_models": len(mapping.DefaultModels)}
	if len(mapping.StaleMetrics) > 0 {
		summary["stale_metrics"] = mapping.StaleMetrics
	}
	for profile, models := range mapping.TaskModels {
		summary[profile] = map[string]any{"models": len(models), "top": models[:min(len(models), 3)]}
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

type channelModelRecoveryHandler struct{}

func (channelModelRecoveryHandler) Type() string {
	return model.SystemTaskTypeChannelModelRecovery
}

func (channelModelRecoveryHandler) Enabled() bool {
	setting := operation_setting.GetMonitorSetting()
	now := common.GetTimestamp()
	retentionDays := setting.ChannelModelEventRetentionDays
	if retentionDays < 1 {
		retentionDays = operation_setting.DefaultChannelModelEventRetentionDays
	}
	cleanupDue := model.HasChannelModelEventsBefore(now - int64(retentionDays)*86400)
	if !setting.ChannelModelCircuitBreakerEnabled || !setting.ChannelModelRecoveryEnabled {
		return cleanupDue
	}
	return cleanupDue || model.HasDueChannelModelRecovery(
		now,
		int64(setting.ChannelModelRecoveryDelayMinutes)*60,
		int64(setting.ChannelModelRecoveryIntervalMinutes)*60,
	)
}

func (channelModelRecoveryHandler) Interval() time.Duration {
	minutes := operation_setting.GetMonitorSetting().ChannelModelRecoveryIntervalMinutes
	if minutes < 1 {
		minutes = operation_setting.DefaultChannelModelRecoveryIntervalMinutes
	}
	return time.Duration(minutes) * time.Minute
}

func (channelModelRecoveryHandler) NewPayload() any { return nil }

// capabilityGateEventCleanupHandler 清理超过保留期的能力门控事件。
// 它必须是独立任务：挂在渠道恢复任务上时，只要断路器没开、也没有过期的
// 渠道事件，那个任务就根本不会被调度，门控事件会永远留在库里。
// ClickHouse 日志库靠表 TTL 过期，HasCapabilityGateEventsBefore 对它恒为 false。
type capabilityGateEventCleanupHandler struct{}

func (capabilityGateEventCleanupHandler) Type() string {
	return model.SystemTaskTypeCapabilityGateClean
}

func (capabilityGateEventCleanupHandler) Enabled() bool {
	retention := time.Duration(operation_setting.GetCapabilityGateSetting().EventRetentionDays) * 24 * time.Hour
	return model.HasCapabilityGateEventsBefore(time.Now().Add(-retention).Unix())
}

func (capabilityGateEventCleanupHandler) Interval() time.Duration { return time.Hour }

func (capabilityGateEventCleanupHandler) NewPayload() any { return nil }

func (capabilityGateEventCleanupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	retention := time.Duration(operation_setting.GetCapabilityGateSetting().EventRetentionDays) * 24 * time.Hour
	deleted, err := model.CleanupCapabilityGateEvents(ctx, time.Now().Add(-retention).Unix())
	summary := map[string]any{"deleted": deleted}
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, summary, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// autoRouteLogCleanupHandler 清理超过保留期的选路日志。和门控事件的清理一样是独立任务，
// 不依赖 auto 是否开启：关掉 auto 之后遗留的日志仍然要按保留期过期。
// ClickHouse 日志库靠表 TTL 过期，HasAutoRouteLogsBefore 对它恒为 false。
type autoRouteLogCleanupHandler struct{}

func (autoRouteLogCleanupHandler) Type() string { return model.SystemTaskTypeAutoRouteLogClean }

func (autoRouteLogCleanupHandler) Enabled() bool {
	retention := autorouter_setting.GetAutoRouterSetting().RouteLogRetention()
	return model.HasAutoRouteLogsBefore(time.Now().Add(-retention).Unix())
}

func (autoRouteLogCleanupHandler) Interval() time.Duration { return time.Hour }

func (autoRouteLogCleanupHandler) NewPayload() any { return nil }

func (autoRouteLogCleanupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	retention := autorouter_setting.GetAutoRouterSetting().RouteLogRetention()
	deleted, err := model.CleanupAutoRouteLogs(ctx, time.Now().Add(-retention).Unix())
	summary := map[string]any{"deleted": deleted}
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, summary, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

func (channelModelRecoveryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary, err := runChannelModelRecoveryTask(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// channelTestHandler runs the scheduled "test all channels" job. Enablement and
// cadence still come from the monitor settings; only the execution path moved
// into the system task runner.
type channelTestHandler struct{}

func (channelTestHandler) Type() string { return model.SystemTaskTypeChannelTest }

func (channelTestHandler) Enabled() bool {
	return operation_setting.GetMonitorSetting().AutoTestChannelEnabled
}

func (channelTestHandler) Interval() time.Duration {
	minutes := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return time.Duration(minutes * float64(time.Minute))
}

func (channelTestHandler) NewPayload() any { return nil }

// channelTestTaskPayload controls one channel_test run. A nil/empty payload is a
// scheduled run, which uses the configured monitor ChannelTestMode and does not
// notify. A manual "test all channels" trigger sets Mode=scheduled_all and
// Notify=true to reproduce the legacy manual behavior (test every channel and
// notify root on completion).
type channelTestTaskPayload struct {
	Mode   string `json:"mode,omitempty"`
	Notify bool   `json:"notify,omitempty"`
}

func (channelTestHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := channelTestTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary, err := runChannelTestTask(ctx, payload.Mode, payload.Notify, service.NewSystemTaskProgressReporter(task, runnerID))
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// modelUpdateHandler runs the scheduled upstream model update detection job.
type modelUpdateHandler struct{}

func (modelUpdateHandler) Type() string { return model.SystemTaskTypeModelUpdate }

func (modelUpdateHandler) Enabled() bool {
	return common.GetEnvOrDefaultBool("CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_ENABLED", true)
}

func (modelUpdateHandler) Interval() time.Duration {
	intervalMinutes := common.GetEnvOrDefault(
		"CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES",
		channelUpstreamModelUpdateTaskDefaultIntervalMinutes,
	)
	if intervalMinutes < 1 {
		intervalMinutes = channelUpstreamModelUpdateTaskDefaultIntervalMinutes
	}
	return time.Duration(intervalMinutes) * time.Minute
}

func (modelUpdateHandler) NewPayload() any { return nil }

// modelUpdateTaskPayload controls one model_update run. A scheduled run
// (Manual=false) respects the per-channel minimum check interval and may
// auto-apply detected models when a channel has auto-sync enabled. A manual
// "detect all" trigger sets Manual=true to reproduce the legacy detect-all
// semantics: force a re-check regardless of the interval and never auto-apply,
// so the admin reviews and applies changes explicitly.
type modelUpdateTaskPayload struct {
	Manual bool `json:"manual,omitempty"`
}

func (modelUpdateHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := modelUpdateTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary := runChannelUpstreamModelUpdateTaskOnce(ctx, payload.Manual, !payload.Manual, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// midjourneyPollHandler runs one Midjourney polling pass per scheduled run.
// Enabled() folds the "are there unfinished tasks?" check into enablement so the
// scheduler creates no row when the system is idle; only when at least one
// Midjourney task is in progress does a row get scheduled.
type midjourneyPollHandler struct{}

func (midjourneyPollHandler) Type() string { return model.SystemTaskTypeMidjourneyPoll }

func (midjourneyPollHandler) Enabled() bool {
	return constant.UpdateTask && model.HasUnfinishedMidjourneyTasks()
}

func (midjourneyPollHandler) Interval() time.Duration { return 15 * time.Second }

func (midjourneyPollHandler) NewPayload() any { return nil }

func (midjourneyPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary := runMidjourneyTaskUpdateOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// asyncTaskPollHandler runs one async-task (Suno/video) polling pass per
// scheduled run. Like midjourneyPollHandler, Enabled() folds in the unfinished
// task existence check so an idle system schedules no rows.
type asyncTaskPollHandler struct{}

func (asyncTaskPollHandler) Type() string { return model.SystemTaskTypeAsyncTaskPoll }

func (asyncTaskPollHandler) Enabled() bool {
	return constant.UpdateTask && model.HasUnfinishedSyncTasks()
}

func (asyncTaskPollHandler) Interval() time.Duration { return 15 * time.Second }

func (asyncTaskPollHandler) NewPayload() any { return nil }

func (asyncTaskPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary := service.RunTaskPollingOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

func finishSystemTaskHandler(task *model.SystemTask, runnerID string, status model.SystemTaskStatus, result any, runErr error) {
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage); err != nil {
		common.SysLog(common.LogText("system task %s failed to persist result: %v", task.TaskID, err))
	}
}

// ssoUserSyncHandler periodically pulls the full user list from the SSO system
// and provisions local accounts via email matching.
type ssoUserSyncHandler struct{}

func (ssoUserSyncHandler) Type() string { return model.SystemTaskTypeSSOUserSync }

func (ssoUserSyncHandler) Enabled() bool {
	return common.SsoSyncEnabled &&
		common.SsoUserListApiUrl != "" &&
		common.SsoUserListPlatformId != "" &&
		common.SsoUserListSecret != ""
}

func (ssoUserSyncHandler) Interval() time.Duration { return time.Hour }

func (ssoUserSyncHandler) NewPayload() any { return nil }

func (ssoUserSyncHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := runSsoUserSync(ctx)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, result, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, result, nil)
}

type ssoSyncResult struct {
	Fetched int `json:"fetched"`
	Synced  int `json:"synced"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

func runSsoUserSync(_ context.Context) (*ssoSyncResult, error) {
	users, err := fetchAllSsoUsers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SSO users: %w", err)
	}

	result := &ssoSyncResult{Fetched: len(users)}
	for _, u := range users {
		email := strings.TrimSpace(u.Email)
		if email == "" {
			result.Skipped++
			continue
		}
		hoidcId := strconv.Itoa(u.Uid)
		if u.Uid == 0 {
			hoidcId = "sso:" + email
		}
		_, err := service.ProvisionSsoUser(
			hoidcId, email, u.Name, u.Avatar, u.DepartmentTreePathStr,
			u.StraightLineManagerEmail, u.StraightLineManagerName,
			u.StraightLineManagerUid, u.MentorUid, u.DottedLineManagerUid, u.OrganizationOid, u.DepartmentOid,
			&u.IsCharge,
			common.HoidcAllowSignup,
		)
		if err != nil {
			result.Failed++
			common.SysError(fmt.Sprintf("SSO sync failed for %s: %v", email, err))
			continue
		}
		result.Synced++
	}
	common.SysLog(fmt.Sprintf("SSO user sync completed: fetched=%d synced=%d skipped=%d failed=%d",
		result.Fetched, result.Synced, result.Skipped, result.Failed))

	edges := make(map[string]string, len(users))
	for _, u := range users {
		if u.Uid == 0 || u.StraightLineManagerUid == 0 {
			continue
		}
		// 上游把 manager uid 定义成 int64，这里要用 FormatInt 而不是 Itoa。
		// 两边都转成字符串去对 users.hoidc_id。
		edges[strconv.Itoa(u.Uid)] = strconv.FormatInt(u.StraightLineManagerUid, 10)
	}
	if rebuilt, err := model.ReplaceAllUserLeaders(edges); err != nil {
		common.SysError(fmt.Sprintf("SSO sync: failed to rebuild leader edges: %v", err))
	} else {
		common.SysLog(fmt.Sprintf("SSO sync: rebuilt %d leader edges from %d candidates", rebuilt, len(edges)))
	}

	cutoff := time.Now().Unix() - 24*3600
	disabled, err := model.DisableStaleSsoUsers(cutoff)
	if err != nil {
		common.SysError(fmt.Sprintf("SSO sync: failed to disable stale users: %v", err))
	} else if disabled > 0 {
		common.SysLog(fmt.Sprintf("SSO sync: disabled %d stale users (cutoff=%d)", disabled, cutoff))
	}

	return result, nil
}

type ssoUserListItem struct {
	Uid                      int    `json:"uid"`
	Email                    string `json:"email"`
	Name                     string `json:"name"`
	Avatar                   string `json:"avatar"`
	DepartmentTreePathStr    string `json:"department_tree_path_str"`
	StraightLineManagerEmail string `json:"straight_line_manager_email"`
	StraightLineManagerName  string `json:"straight_line_manager_name"`
	StraightLineManagerUid   int64  `json:"straight_line_manager_uid"`
	MentorUid                int64  `json:"mentor_uid"`
	DottedLineManagerUid     int64  `json:"dotted_line_manager_uid"`
	OrganizationOid          int64  `json:"organization_oid"`
	DepartmentOid            int64  `json:"department_oid"`
	IsCharge                 int    `json:"is_charge"`
}

func fetchAllSsoUsers() ([]ssoUserListItem, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	hash := md5.Sum([]byte(timestamp + "_" + common.SsoUserListSecret))
	signature := hex.EncodeToString(hash[:])

	params := url.Values{
		"signature":   {signature},
		"timestamp":   {timestamp},
		"platform_id": {common.SsoUserListPlatformId},
	}
	apiUrl := strings.TrimRight(common.SsoUserListApiUrl, "/") +
		"/open-api/user/get-all?" + params.Encode()

	req, err := http.NewRequest(http.MethodPost, apiUrl, strings.NewReader(""))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := service.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSO user list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("SSO user list failed: HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSO user list response: %w", err)
	}
	common.SysLog(common.LogText("SSO user list response: %s", raw))

	var body struct {
		Code int               `json:"code"`
		Msg  string            `json:"msg"`
		Data []ssoUserListItem `json:"data"`
	}
	if err := common.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("failed to parse SSO user list response: %w", err)
	}
	if body.Code != 0 {
		return nil, fmt.Errorf("SSO user list failed: %s", body.Msg)
	}
	return body.Data, nil
}
