package controller

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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

func usagePlatformFromQuery(c *gin.Context) string {
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	if platform == "" {
		return "local"
	}
	return platform
}

func GetUsagePlatforms(c *gin.Context) {
	platforms := []gin.H{{"value": "local", "label": "Local"}}
	configs, err := service.LoadExternalPlatformConfigs()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, config := range configs {
		label := config.Label
		if label == "" {
			label = strings.ToUpper(config.Name)
		}
		platforms = append(platforms, gin.H{"value": config.Name, "label": label})
	}
	common.ApiSuccess(c, platforms)
}

// usageViewerScope: everyone (nil UserIDs) for administrators and SSO accounts above every department;
// otherwise the caller plus granted people and departments and the department they head.
func usageViewerScope(c *gin.Context) (scope *model.UsageViewerScope, maxDays int64, err error) {
	if c.GetInt("role") >= common.RoleAdminUser {
		return &model.UsageViewerScope{}, adminUsageAnalyticsMaxDays, nil
	}
	userID := c.GetInt("id")
	if userID <= 0 {
		return nil, 0, errors.New("invalid user")
	}
	scope, err = model.GetUsageViewerScope(userID)
	if err != nil {
		return nil, 0, err
	}
	if scope.UserIDs == nil {
		return scope, adminUsageAnalyticsMaxDays, nil
	}
	return scope, selfUsageAnalyticsMaxDays, nil
}

// usageUserFilter narrows the scope by the "username" query; nil means everyone.
func usageUserFilter(c *gin.Context, scope *model.UsageViewerScope) ([]int, error) {
	keyword := strings.TrimSpace(c.Query("username"))
	if keyword == "" {
		return scope.UserIDs, nil
	}
	matched, err := model.MatchUsageUserIds(keyword)
	if err != nil || scope.UserIDs == nil {
		return matched, err
	}
	return slices.DeleteFunc(matched, func(id int) bool { return !slices.Contains(scope.UserIDs, id) }), nil
}

func GetUsageAnalytics(c *gin.Context) {
	scope, maxDays, err := usageViewerScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	start, end, ok := parseUsageAnalyticsRange(c, maxDays)
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
	userIDs, err := usageUserFilter(c, scope)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	data, err := model.GetUsageAnalytics(model.UsageAnalyticsParams{
		Start:     start,
		End:       end,
		GroupBy:   groupBy,
		ModelName: c.Query("model"),
		Org:       usageOrgFilterFromQuery(c),
		UserIDs:   userIDs,
		Platform: func() string {
			if usagePlatformFromQuery(c) == "all" {
				return "local"
			}
			return usagePlatformFromQuery(c)
		}(),
	})
	if usagePlatformFromQuery(c) == "all" {
		localParams := model.UsageAnalyticsParams{Start: start, End: end, GroupBy: groupBy, ModelName: c.Query("model"), Org: usageOrgFilterFromQuery(c), UserIDs: userIDs, Platform: "local"}
		local, localErr := model.GetUsageAnalytics(localParams)
		if localErr != nil {
			common.ApiError(c, localErr)
			return
		}
		results := []*model.UsageAnalyticsResult{local}
		configs, configErr := service.LoadExternalPlatformConfigs()
		if configErr != nil {
			common.ApiError(c, configErr)
			return
		}
		for _, config := range configs {
			params := localParams
			params.Platform = config.Name
			external, externalErr := model.GetUsageAnalytics(params)
			if externalErr != nil {
				common.ApiError(c, externalErr)
				return
			}
			results = append(results, external)
		}
		data = model.MergeUsageAnalyticsResults(results...)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// GetUsageDataScope tells the console what the caller sees and which filters to start on.
func GetUsageDataScope(c *gin.Context) {
	scope, _, err := usageViewerScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"all_users":             scope.UserIDs == nil,
		"user_count":            len(scope.UserIDs),
		"default_business_unit": scope.DefaultBusinessUnit,
		"default_department":    scope.DefaultDepartment,
	})
}

func GetUsageOrgOptions(c *gin.Context) {
	scope, _, err := usageViewerScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	options, err := model.GetUsageOrgOptions(scope.UserIDs)
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
	// User groupings are for the console export.
	groupBy := c.Query("group_by")
	switch groupBy {
	case model.UsageAnalyticsGroupSummary, model.UsageAnalyticsGroupModel, model.UsageAnalyticsGroupUser,
		model.UsageAnalyticsGroupUserModel, model.UsageAnalyticsGroupModelChannel:
	default:
		common.ApiErrorMsg(c, "invalid group_by")
		return
	}
	data, err := model.GetUsageAnalytics(model.UsageAnalyticsParams{
		Start:     start,
		End:       end,
		GroupBy:   groupBy,
		ModelName: c.Query("model"),
		UserID:    userID,
		Platform:  usagePlatformFromQuery(c),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

func GetPeakTPM(c *gin.Context) {
	scope, maxDays, err := usageViewerScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	start, end, ok := parseUsageAnalyticsRange(c, maxDays)
	if !ok {
		return
	}
	userIDs, err := usageUserFilter(c, scope)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		points, err := model.GetExternalPeakTPM(start, end, 0, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, points)
		return
	}
	points, err := model.GetPeakTPM(model.PeakTPMParams{
		Start:     start,
		End:       end,
		ModelName: c.Query("model"),
		Org:       usageOrgFilterFromQuery(c),
		UserIDs:   userIDs,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, points)
}

// GetSelfPeakTPM ignores client identity filters.
func GetSelfPeakTPM(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		common.ApiErrorMsg(c, "invalid user")
		return
	}
	start, end, ok := parseUsageAnalyticsRange(c, selfUsageAnalyticsMaxDays)
	if !ok {
		return
	}
	platform := usagePlatformFromQuery(c)
	if platform != "local" {
		points, err := model.GetExternalPeakTPM(start, end, userID, platform)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, points)
		return
	}
	points, err := model.GetPeakTPM(model.PeakTPMParams{
		Start:     start,
		End:       end,
		ModelName: c.Query("model"),
		UserID:    userID,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, points)
}
