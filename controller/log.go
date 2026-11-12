package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func parseLogSpecialFilters(c *gin.Context, allowRetry bool) (streamError bool, retry bool, ok bool) {
	for _, query := range []struct {
		name  string
		value *bool
	}{
		{name: "stream_error", value: &streamError},
		{name: "retry", value: &retry},
	} {
		raw := c.Query(query.name)
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "invalid boolean query parameter: " + query.name,
			})
			return false, false, false
		}
		*query.value = parsed
	}

	if retry && !allowRetry {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "retry filter is only available for administrator log views",
		})
		return false, false, false
	}
	return streamError, retry, true
}

func validateLogSpecialFilterTimeRange(c *gin.Context, streamError bool, retry bool, startTimestamp int64, endTimestamp int64) bool {
	if !streamError && !retry {
		return true
	}
	if startTimestamp > 0 && endTimestamp >= startTimestamp {
		return true
	}
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"message": "start_timestamp and end_timestamp are required for stream_error and retry filters",
	})
	return false
}

// autoRoutedFromQuery reads the optional auto_routed filter; a missing or unreadable
// value keeps every log, since the filter only narrows a search.
func autoRoutedFromQuery(c *gin.Context) *bool {
	autoRouted, err := strconv.ParseBool(c.Query("auto_routed"))
	if err != nil {
		return nil
	}
	return &autoRouted
}

// logFilterFromQuery reads the log search shared by the log list and the log
// export. Username and channel are read only for administrators.
func logFilterFromQuery(c *gin.Context, admin bool) model.LogFilter {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	filter := model.LogFilter{
		Type:              logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		TokenName:         c.Query("token_name"),
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
		AutoRouted:        autoRoutedFromQuery(c),
	}
	if admin {
		filter.Username = c.Query("username")
		filter.Channel, _ = strconv.Atoi(c.Query("channel"))
		filter.ISelfEmail = c.Query("iself_email")
	}
	filter.StreamError, _ = strconv.ParseBool(c.Query("stream_error"))
	if admin {
		filter.Retry, _ = strconv.ParseBool(c.Query("retry"))
	}
	return filter
}

// limitLogTimeRange returns the start of a log or dashboard search kept within
// the longest log time range. A missing start reaches back the whole range, and
// time after now does not count toward it.
func limitLogTimeRange(startTimestamp int64, endTimestamp int64) (int64, error) {
	maxRange := operation_setting.GetLogSetting().MaxRangeSeconds()
	end := common.GetTimestamp()
	if endTimestamp != 0 {
		end = min(end, endTimestamp)
	}
	if startTimestamp == 0 {
		return end - maxRange, nil
	}
	if end-startTimestamp > maxRange {
		return 0, common.NewMessage("The time range cannot exceed {{days}} days", map[string]any{"days": maxRange / (24 * 60 * 60)})
	}
	return startTimestamp, nil
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQueryWithMax(c, common.MaxAdminLogPageSize)
	filter := logFilterFromQuery(c, true)
	streamError, retry, ok := parseLogSpecialFilters(c, true)
	if !ok || !validateLogSpecialFilterTimeRange(c, streamError, retry, filter.StartTimestamp, filter.EndTimestamp) {
		return
	}
	logs, total, err := model.GetAllLogs(logFilterFromQuery(c, true), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		model.FormatAdminLogs(logs)
	} else {
		model.FormatRootLogs(logs)
	}
	model.FillLogVendors(logs)
	model.FillLogUsers(logs)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := logFilterFromQuery(c, false)
	streamError, retry, ok := parseLogSpecialFilters(c, false)
	if !ok || !validateLogSpecialFilterTimeRange(c, streamError, retry, filter.StartTimestamp, filter.EndTimestamp) {
		return
	}
	if c.GetInt("role") < common.RoleAdminUser {
		startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		filter.StartTimestamp = startTimestamp
	}
	logs, total, err := model.GetUserLogs(c.GetInt("id"), filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.FillLogVendors(logs)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// ExportAllLogs streams every user's logs matching the search.
func ExportAllLogs(c *gin.Context) {
	exportLogs(c, true)
}

// ExportUserLogs streams the caller's own logs matching the search.
func ExportUserLogs(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser && !operation_setting.GetLogSetting().UserExportEnabled {
		common.ApiErrorT(c, "Log export is disabled")
		return
	}
	exportLogs(c, false)
}

// exportLogs writes one JSON log per line, newest first, and ends with an API
// response object: success with the log count, or the failure. Each user runs
// one export at a time, and every export stays within the longest log time
// range.
func exportLogs(c *gin.Context, all bool) {
	filter := logFilterFromQuery(c, all)
	startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter.StartTimestamp = startTimestamp

	userId := c.GetInt("id")
	release, ok, err := service.AcquireLogExportLock(userId)
	if err != nil {
		common.SysError(common.LogText("failed to lock log export: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	if !ok {
		common.ApiErrorT(c, "Another log export is running. Try again after it finishes.")
		return
	}
	defer release()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	count := 0
	writeLog := func(log *model.Log) error {
		line, err := common.Marshal(log)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(line, '\n')); err != nil {
			return err
		}
		count++
		return nil
	}
	ctx := c.Request.Context()
	if all {
		err = model.StreamAllLogs(ctx, filter, c.GetInt("role"), writeLog)
	} else {
		err = model.StreamUserLogs(ctx, userId, filter, writeLog)
	}
	if ctx.Err() != nil {
		return
	}
	// The status is already sent, so the result object becomes the last line.
	if err != nil {
		common.SysError(common.LogText("failed to export logs: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	common.ApiSuccess(c, gin.H{"count": count})
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		common.ApiErrorI18n(c, i18n.MsgTokenInvalid)
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	streamError, retry, ok := parseLogSpecialFilters(c, true)
	if !ok || !validateLogSpecialFilterTimeRange(c, streamError, retry, startTimestamp, endTimestamp) {
		return
	}
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group, streamError, retry, autoRoutedFromQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota":       stat.Quota,
			"rpm":         stat.Rpm,
			"tpm":         stat.Tpm,
			"total_count": stat.Total,
			"fast_count":  stat.Fast,
			"fast_ratio":  stat.FastRatio,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if c.GetInt("role") < common.RoleAdminUser {
		var err error
		if startTimestamp, err = limitLogTimeRange(startTimestamp, endTimestamp); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	streamError, retry, ok := parseLogSpecialFilters(c, false)
	if !ok || !validateLogSpecialFilterTimeRange(c, streamError, retry, startTimestamp, endTimestamp) {
		return
	}
	quotaNum, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group, streamError, retry, autoRoutedFromQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota":       quotaNum.Quota,
			"rpm":         quotaNum.Rpm,
			"tpm":         quotaNum.Tpm,
			"total_count": quotaNum.Total,
			"fast_count":  quotaNum.Fast,
			"fast_ratio":  quotaNum.FastRatio,
			//"token": tokenNum,
		},
	})
	return
}
