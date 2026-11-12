package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func parseFlowQuotaTimeRange(c *gin.Context) (int64, int64, bool) {
	startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "invalid time range")
		return 0, 0, false
	}
	return startTimestamp, endTimestamp, true
}

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		var userID int
		if username != "" {
			userID = c.GetInt("id")
		}
		dates, err := model.GetExternalQuotaDates(startTimestamp, endTimestamp, userID, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": dates})
		return
	}
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username, usageOrgFilterFromQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	dimension := c.DefaultQuery("dimension", model.UsageAnalyticsGroupUser)
	switch dimension {
	case model.UsageAnalyticsGroupUser, model.UsageAnalyticsGroupBusinessUnit,
		model.UsageAnalyticsGroupDepartment, model.UsageAnalyticsGroupTeam:
	default:
		common.ApiErrorMsg(c, "invalid dimension")
		return
	}
	dates, err := model.GetUserQuotaTrend(startTimestamp, endTimestamp, dimension, usageOrgFilterFromQuery(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	startTimestamp, err := limitLogTimeRange(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		dates, err := model.GetExternalQuotaDates(startTimestamp, endTimestamp, userId, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": dates})
		return
	}
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetAllFlowQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		userID := 0
		if username != "" {
			userID = c.GetInt("id")
		}
		dates, err := model.GetExternalFlowData(startTimestamp, endTimestamp, userID, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": dates})
		return
	}
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, username, 0, c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetUserFlowQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	if _, err := limitLogTimeRange(startTimestamp, endTimestamp); err != nil {
		common.ApiError(c, err)
		return
	}
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		dates, err := model.GetExternalFlowData(startTimestamp, endTimestamp, userId, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": dates})
		return
	}
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, "", userId, common.RoleCommonUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}
