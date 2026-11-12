package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetCapabilityGateEvents 返回能力门控的判定留痕（管理员）。
func GetCapabilityGateEvents(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	events, total, err := model.GetCapabilityGateEvents(model.CapabilityGateEventQuery{
		Model:     c.Query("model"),
		Action:    c.Query("action"),
		RequestId: c.Query("request_id"),
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
		"items":     events,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}
