package model

import (
	"fmt"
	"strings"
)

type externalUsageAnalyticsRow struct {
	UsageMetrics
	ModelName    string `gorm:"column:model_name"`
	UserID       int    `gorm:"column:user_id"`
	Username     string `gorm:"column:username"`
	ChannelID    int    `gorm:"column:channel_id"`
	ChannelName  string `gorm:"column:channel_name"`
	UserCount    int64  `gorm:"column:user_count"`
	DisplayName  string `gorm:"column:display_name"`
	Avatar       string `gorm:"column:avatar"`
	Email        string `gorm:"column:email"`
	UserStatus   int    `gorm:"column:user_status"`
	BusinessUnit string `gorm:"column:business_unit"`
	Department   string `gorm:"column:department"`
	Team         string `gorm:"column:team"`
	OrgPath      string `gorm:"column:org_path"`
}

const externalUsageMetricsSelect = "COALESCE(SUM(count), 0) AS request_count, " +
	"COALESCE(SUM(quota), 0) AS quota, " +
	"COALESCE(SUM(token_used), 0) AS token_used, " +
	"COALESCE(SUM(input_tokens), 0) AS input_tokens, " +
	"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, " +
	"COALESCE(SUM(use_time_total), 0) AS use_time_total, " +
	"COALESCE(SUM(use_time_count), 0) AS use_time_count, " +
	"COALESCE(SUM(stream_error_count), 0) AS stream_error_count, " +
	"COALESCE(SUM(failed_count), 0) AS failed_count, " +
	"COALESCE(SUM(tracked_count), 0) AS tracked_count, " +
	"COALESCE(MAX(last_called_at), 0) AS last_called_at"

const externalUsageUserAttributesSelect = "COALESCE(MAX(users.display_name), '') AS display_name, " +
	"COALESCE(MAX(users.avatar), '') AS avatar, COALESCE(MAX(users.email), '') AS email, " +
	"COALESCE(MAX(users.status), 0) AS user_status"

func getExternalUsageAnalytics(params UsageAnalyticsParams) (*UsageAnalyticsResult, error) {
	platform := strings.ToLower(strings.TrimSpace(params.Platform))
	if platform == "" || platform == "local" || platform == "all" {
		return nil, fmt.Errorf("external usage query requires a platform")
	}
	query := DB.Table("external_quota_data").
		Where("platform = ? AND created_at >= ? AND created_at <= ?", platform, params.Start, params.End)
	query = query.Joins("LEFT JOIN users ON users.id = external_quota_data.local_user_id AND users.deleted_at IS NULL")
	if params.UserID > 0 {
		query = query.Where("local_user_id = ?", params.UserID)
	}
	if params.UserIDs != nil {
		query = query.Where("local_user_id IN ?", params.UserIDs)
	}
	if params.ModelName = strings.TrimSpace(params.ModelName); params.ModelName != "" {
		query = query.Where("model_name = ?", params.ModelName)
	}
	if !params.Org.IsZero() {
		query = params.Org.apply(query)
	}
	result := &UsageAnalyticsResult{Rows: []UsageAnalyticsRow{}}
	if params.GroupBy == UsageAnalyticsGroupSummary {
		var summary UsageAnalyticsSummary
		if err := query.Select(externalUsageMetricsSelect).Scan(&summary.UsageMetrics).Error; err != nil {
			return nil, err
		}
		result.Summary = &summary
		return result, nil
	}

	selects := externalUsageMetricsSelect
	groupBy := ""
	switch params.GroupBy {
	case UsageAnalyticsGroupModel:
		selects = "model_name, COUNT(DISTINCT local_user_id) AS user_count, " + selects
		groupBy = "model_name"
	case UsageAnalyticsGroupUser:
		selects = "local_user_id AS user_id, MAX(remote_username) AS username, " + externalUsageUserAttributesSelect + ", " + selects
		groupBy = "local_user_id"
	case UsageAnalyticsGroupUserModel:
		selects = "local_user_id AS user_id, MAX(remote_username) AS username, model_name, " + externalUsageUserAttributesSelect + ", " + selects
		groupBy = "local_user_id, model_name"
	case UsageAnalyticsGroupModelChannel:
		selects = "model_name, remote_channel_id AS channel_id, MAX(remote_channel_name) AS channel_name, " + selects
		groupBy = "model_name, remote_channel_id"
	case UsageAnalyticsGroupBusinessUnit:
		selects = "COALESCE(MAX(users.secondary_organization), '') AS business_unit, COUNT(DISTINCT local_user_id) AS user_count, " + selects
		groupBy = "users.secondary_organization"
	case UsageAnalyticsGroupDepartment:
		selects = "COALESCE(MAX(users.secondary_organization), '') AS business_unit, COALESCE(MAX(users.tertiary_organization), '') AS department, COUNT(DISTINCT local_user_id) AS user_count, " + selects
		groupBy = "users.secondary_organization, users.tertiary_organization"
	case UsageAnalyticsGroupTeam:
		selects = "COALESCE(MAX(users.secondary_organization), '') AS business_unit, COALESCE(MAX(users.tertiary_organization), '') AS department, COALESCE(MAX(users.team), '') AS team, COUNT(DISTINCT local_user_id) AS user_count, " + selects
		groupBy = "users.secondary_organization, users.tertiary_organization, users.team"
	default:
		return nil, fmt.Errorf("unsupported external group_by: %s", params.GroupBy)
	}
	rows := make([]externalUsageAnalyticsRow, 0)
	if err := query.Select(selects).Group(groupBy).Order("quota DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		item := UsageAnalyticsRow{UsageMetrics: row.UsageMetrics}
		item.Platform = &platform
		switch params.GroupBy {
		case UsageAnalyticsGroupModel:
			item.ModelName = &row.ModelName
			item.UserCount = &row.UserCount
		case UsageAnalyticsGroupUser:
			item.UserID = &row.UserID
			item.Username = &row.Username
			item.DisplayName = &row.DisplayName
			item.Avatar = &row.Avatar
			item.Email = &row.Email
			item.UserStatus = &row.UserStatus
		case UsageAnalyticsGroupUserModel:
			item.UserID = &row.UserID
			item.Username = &row.Username
			item.ModelName = &row.ModelName
			item.DisplayName = &row.DisplayName
			item.Avatar = &row.Avatar
			item.Email = &row.Email
			item.UserStatus = &row.UserStatus
		case UsageAnalyticsGroupModelChannel:
			item.ModelName = &row.ModelName
			item.ChannelID = &row.ChannelID
			item.ChannelName = &row.ChannelName
		case UsageAnalyticsGroupBusinessUnit, UsageAnalyticsGroupDepartment, UsageAnalyticsGroupTeam:
			item.BusinessUnit = &row.BusinessUnit
			item.Department = &row.Department
			item.Team = &row.Team
			item.UserCount = &row.UserCount
		}
		result.Rows = append(result.Rows, item)
	}
	return result, nil
}

func MergeUsageAnalyticsResults(results ...*UsageAnalyticsResult) *UsageAnalyticsResult {
	merged := &UsageAnalyticsResult{Rows: []UsageAnalyticsRow{}}
	groups := make(map[string]int)
	for _, result := range results {
		if result == nil {
			continue
		}
		if result.Summary != nil {
			if merged.Summary == nil {
				merged.Summary = &UsageAnalyticsSummary{}
			}
			merged.Summary.UsageMetrics.add(result.Summary.UsageMetrics)
			merged.Summary.ActiveUsers += result.Summary.ActiveUsers
			merged.Summary.ModelCount += result.Summary.ModelCount
		}
		for _, row := range result.Rows {
			key := usageAnalyticsRowKey(row)
			if index, ok := groups[key]; ok {
				merged.Rows[index].UsageMetrics.add(row.UsageMetrics)
				continue
			}
			groups[key] = len(merged.Rows)
			merged.Rows = append(merged.Rows, row)
		}
	}
	return merged
}

func usageAnalyticsRowKey(row UsageAnalyticsRow) string {
	modelName, username, userID, channelID := "", "", 0, 0
	if row.ModelName != nil {
		modelName = *row.ModelName
	}
	if row.Username != nil {
		username = *row.Username
	}
	if row.UserID != nil {
		userID = *row.UserID
	}
	if row.ChannelID != nil {
		channelID = *row.ChannelID
	}
	return fmt.Sprintf("%s|%s|%d|%d", modelName, username, userID, channelID)
}
