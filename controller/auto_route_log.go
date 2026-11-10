package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// autoRouteLogView 是单条选路日志的响应：detail 是解码后的对象，而不是一段 JSON 文本。
type autoRouteLogView struct {
	model.AutoRouteLog
	Detail any `json:"detail"`
}

// GetAutoRouteLogs 返回 auto 请求的选路日志列表（管理员），列表不带 detail。
func GetAutoRouteLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	userID, _ := strconv.Atoi(c.Query("user_id"))
	logs, total, err := model.GetAutoRouteLogs(model.AutoRouteLogQuery{
		RequestId: c.Query("request_id"),
		UserId:    userID,
		Model:     c.Query("model"),
		Outcome:   c.Query("outcome"),
		Judgment:  c.Query("judgment"),
		TaskType:  c.Query("task_type"),
		StartTime: startTime,
		EndTime:   endTime,
		Offset:    pageInfo.GetStartIdx(),
		Limit:     pageInfo.GetPageSize(),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     logs,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}

// GetAutoRouteLog 返回一次请求的选路过程（管理员）。没有这条日志是正常情况（没开记录、
// 记录模式没覆盖这类请求、已过保留期），此时 data 为 null，由前端退回消费日志里的摘要。
func GetAutoRouteLog(c *gin.Context) {
	log, found, err := model.GetAutoRouteLogByRequestId(c.Param("request_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !found {
		common.ApiSuccess(c, nil)
		return
	}
	view := autoRouteLogView{AutoRouteLog: log}
	// 写入时已经保证 detail 是不超过上限的合法 JSON；万一被人直接改库改坏了，
	// 这条日志的其余列照样返回，只是没有过程。
	if err := common.UnmarshalJsonStr(log.Detail, &view.Detail); err != nil {
		view.Detail = nil
	}
	common.ApiSuccess(c, view)
}
