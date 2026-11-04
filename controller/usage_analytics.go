package controller

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	adminUsageAnalyticsMaxDays = 92
	selfUsageAnalyticsMaxDays  = 30
)

// parseUsageAnalyticsRange validates timestamps and the allowed time span.
func parseUsageAnalyticsRange(c *gin.Context, maxDays int64) (start int64, end int64, ok bool) {
	start, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || start <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	end, err = strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || end <= start {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if end-start > maxDays*86400 {
		common.ApiErrorMsg(c, fmt.Sprintf("time range cannot exceed %d days", maxDays))
		return 0, 0, false
	}
	return start, end, true
}

func usageOrgFilterFromQuery(c *gin.Context) model.UsageOrgFilter {
	return model.UsageOrgFilter{
		BusinessUnit: c.Query("business_unit"),
		Department:   c.Query("department"),
		Team:         c.Query("team"),
	}
}

func GetAdminUsageAnalytics(c *gin.Context) {
	start, end, ok := parseUsageAnalyticsRange(c, adminUsageAnalyticsMaxDays)
	if !ok {
		return
	}
	groupBy := c.Query("group_by")
	switch groupBy {
	case model.UsageAnalyticsGroupSummary, model.UsageAnalyticsGroupModel, model.UsageAnalyticsGroupUser,
		model.UsageAnalyticsGroupUserModel, model.UsageAnalyticsGroupModelChannel,
		model.UsageAnalyticsGroupBusinessUnit, model.UsageAnalyticsGroupDepartment, model.UsageAnalyticsGroupTeam:
	default:
		common.ApiErrorMsg(c, "invalid group_by")
		return
	}
	data, err := model.GetUsageAnalytics(model.UsageAnalyticsParams{
		Start:     start,
		End:       end,
		GroupBy:   groupBy,
		Username:  c.Query("username"),
		ModelName: c.Query("model"),
		Org:       usageOrgFilterFromQuery(c),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func GetUsageOrgOptions(c *gin.Context) {
	options, err := model.GetUsageOrgOptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, options)
}

// GetSelfUsageAnalytics ignores client identity filters.
func GetSelfUsageAnalytics(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorMsg(c, "invalid user")
		return
	}
	start, end, ok := parseUsageAnalyticsRange(c, selfUsageAnalyticsMaxDays)
	if !ok {
		return
	}
	groupBy := c.Query("group_by")
	if groupBy != model.UsageAnalyticsGroupSummary && groupBy != model.UsageAnalyticsGroupModel {
		common.ApiErrorMsg(c, "invalid group_by")
		return
	}
	data, err := model.GetUsageAnalytics(model.UsageAnalyticsParams{
		Start:     start,
		End:       end,
		GroupBy:   groupBy,
		ModelName: c.Query("model"),
		UserID:    userID,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func GetPeakTPM(c *gin.Context) {
	start, end, ok := parseUsageAnalyticsRange(c, adminUsageAnalyticsMaxDays)
	if !ok {
		return
	}
	points, err := model.GetPeakTPM(model.PeakTPMParams{
		Start:     start,
		End:       end,
		Username:  c.Query("username"),
		ModelName: c.Query("model"),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, points)
}
