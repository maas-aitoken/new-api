package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetCapabilityGateEvents 返回能力门控的判定留痕（管理员）。
// /self 路由只返回当前用户自己的留痕：user_id 一律取会话用户，不信任查询参数。
func GetCapabilityGateEvents(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	query := model.CapabilityGateEventQuery{
		Model:     c.Query("model"),
		Action:    c.Query("action"),
		RequestId: c.Query("request_id"),
		StartTime: startTime,
		EndTime:   endTime,
		Offset:    pageInfo.GetStartIdx(),
		Limit:     pageInfo.GetPageSize(),
	}
	if c.FullPath() == "/api/capability-gate/events/self" {
		query.UserId = c.GetInt("id")
	}
	events, total, err := model.GetCapabilityGateEvents(query)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     events,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}
