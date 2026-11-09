package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetModelAvailability reports outcomes for the last 10, 30, or 60 minutes.
func GetModelAvailability(c *gin.Context) {
	minutes := 10
	if raw := c.Query("minutes"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || (parsed != 10 && parsed != 30 && parsed != 60) {
			common.ApiErrorMsg(c, "minutes must be 10, 30, or 60")
			return
		}
		minutes = parsed
	}

	allowedModels, err := visibleHealthModels(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	window, err := model.GetRecentModelAvailability(minutes, allowedModels)
	if err != nil {
		common.SysError("failed to query model availability: " + err.Error())
		common.ApiErrorMsg(c, "failed to query model availability")
		return
	}
	window.ErrorLogEnabled = constant.ErrorLogEnabled
	common.ApiSuccess(c, window)
}

// GetModelHealth returns each model's health bar summed over its channels.
func GetModelHealth(c *gin.Context) {
	allowedModels, err := visibleHealthModels(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	window, err := model.GetRecentModelHealth(allowedModels)
	if err != nil {
		common.SysError("failed to query model health: " + err.Error())
		common.ApiErrorMsg(c, "failed to query model health")
		return
	}
	common.ApiSuccess(c, window)
}

// visibleHealthModels limits non-admins to the models of their usable groups.
// nil allows all models; an empty set allows none.
func visibleHealthModels(c *gin.Context) (map[string]struct{}, error) {
	if c.GetInt("role") >= common.RoleAdminUser {
		return nil, nil
	}
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		return nil, err
	}
	groupNames := make([]string, 0)
	for group := range service.GetUserUsableGroups(user.Group) {
		groupNames = append(groupNames, group)
	}
	allowedModels := make(map[string]struct{})
	for _, modelName := range service.GetGroupsEnabledModels(groupNames) {
		allowedModels[modelName] = struct{}{}
	}
	return allowedModels, nil
}
