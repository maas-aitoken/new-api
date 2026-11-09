package controller

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"

	"github.com/gin-gonic/gin"
)

// GetAutoRouterStatus reports what the settings page cannot read from the
// options: whether the router can run, where each connection value comes from
// (never the secret values themselves), and how the last leaderboard refresh ended.
func GetAutoRouterStatus(c *gin.Context) {
	setting := autorouter_setting.GetAutoRouterSetting()
	connections := setting.Connections()
	status := gin.H{
		"enabled":          setting.Enabled,
		"jev_ready":        connections.JevEndpoint.Value != "",
		"rank_keys_ready":  connections.RankKeysReady(),
		"ranking_current":  setting.RankingCurrent(),
		"taxonomy_version": autorouter.TaxonomyVersion,
		"rank_sources":     setting.Ranking().Sources,
		"sources": map[string]string{
			"jev_endpoint":                connections.JevEndpoint.Source,
			"jev_model":                   connections.JevModel.Source,
			"jev_api_key":                 connections.JevAPIKey.Source,
			"artificial_analysis_api_key": connections.ArtificialAnalysisKey.Source,
			"design_arena_api_key":        connections.DesignArenaKey.Source,
			"openrouter_api_key":          connections.OpenRouterKey.Source,
		},
	}
	latest, err := model.GetLatestSystemTasks([]string{model.SystemTaskTypeAutoRouterRank})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task := latest[model.SystemTaskTypeAutoRouterRank]; task != nil {
		status["rank_refresh"] = task.ToResponse()
	}
	common.ApiSuccess(c, status)
}

// GetAutoRouterRanking returns the ranking the router routes by right now,
// which is the embedded one while the stored ranking predates the task types.
// 每个画像都列出来（库里缺的画像给空列表），这正是手工编辑保存时要交回的画像集合；
// 编辑保存时用 sources 里的 generated_at、edited_at 做冲突检查。
func GetAutoRouterRanking(c *gin.Context) {
	setting := autorouter_setting.GetAutoRouterSetting()
	ranking := setting.Ranking()
	common.ApiSuccess(c, gin.H{
		"current":          setting.RankingCurrent(),
		"taxonomy_version": autorouter.TaxonomyVersion,
		"sources":          ranking.Sources,
		"profiles":         ranking.Profiles(),
	})
}

// RefreshAutoRouterRanking runs a leaderboard refresh now, as a system task so it
// takes the same lease as the scheduled one. It works while the periodic refresh
// is switched off.
func RefreshAutoRouterRanking(c *gin.Context) {
	if !autorouter_setting.GetAutoRouterSetting().Connections().RankKeysReady() {
		common.ApiErrorT(c, "The leaderboard keys are not configured. Set the Artificial Analysis key and either the Design Arena or the OpenRouter key first.")
		return
	}
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeAutoRouterRank, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if created {
		recordManageAudit(c, "autorouter.rank_refresh", nil)
	}
	common.ApiSuccess(c, gin.H{"task": task.ToResponse(), "created": created})
}

// RestoreAutoRouterRanking writes the embedded ranking back into the options.
// A refresh in flight would overwrite it a moment later, so it must finish first.
func RestoreAutoRouterRanking(c *gin.Context) {
	active, err := model.GetActiveSystemTask(model.SystemTaskTypeAutoRouterRank)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if active != nil {
		common.ApiErrorT(c, "A leaderboard refresh is running. Restore the built-in ranking after it finishes.")
		return
	}
	options, err := autorouter_setting.ShippedOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(options); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "autorouter.rank_restore", nil)
	common.ApiSuccess(c, nil)
}

// autoRouterRankingEditRequest 是手工编辑后的整份榜单，base 是加载时看到的 rank_sources 时间。
type autoRouterRankingEditRequest struct {
	Base     autorouter_setting.RankingBase      `json:"base"`
	Profiles []autorouter_setting.RankingProfile `json:"profiles"`
}

// UpdateAutoRouterRanking 用管理员手工编辑的整份榜单替换存的榜单，并关掉定时刷新，
// 免得下一次刷新把它整个覆盖。刷新正在跑时拒绝：刷新跑完会立刻盖掉这次保存。
func UpdateAutoRouterRanking(c *gin.Context) {
	var request autoRouterRankingEditRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	active, err := model.GetActiveSystemTask(model.SystemTaskTypeAutoRouterRank)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if active != nil {
		common.ApiErrorT(c, "A leaderboard refresh is running. Save your edits after it finishes.")
		return
	}
	edit, err := autorouter_setting.GetAutoRouterSetting().EditedRanking(request.Base, request.Profiles, time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(edit.Options); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "autorouter.rank_edit", map[string]any{
		"profiles": strings.Join(edit.Profiles, ", "),
		"added":    edit.Added,
		"removed":  edit.Removed,
		"rescored": edit.Rescored,
	})
	common.ApiSuccess(c, nil)
}
