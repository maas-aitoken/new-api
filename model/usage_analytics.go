package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	UsageAnalyticsGroupSummary      = "summary"
	UsageAnalyticsGroupModel        = "model"
	UsageAnalyticsGroupUser         = "user"
	UsageAnalyticsGroupUserModel    = "user_model"
	UsageAnalyticsGroupModelChannel = "model_channel"
	UsageAnalyticsGroupBusinessUnit = "business_unit"
	UsageAnalyticsGroupDepartment   = "department"
	UsageAnalyticsGroupTeam         = "team"
)

// UsageOrgUnassigned filters users without a value at that organization level.
const UsageOrgUnassigned = "__unassigned__"

// The user's current organization; quota_data keeps no snapshot.
const (
	usageBusinessUnitExpr = "COALESCE(users.secondary_organization, '')"
	usageDepartmentExpr   = "COALESCE(users.tertiary_organization, '')"
	usageTeamExpr         = "COALESCE(users.team, '')"
)

var usageOrgColumns = []struct{ expr, alias string }{
	{usageBusinessUnitExpr, "business_unit"},
	{usageDepartmentExpr, "department"},
	{usageTeamExpr, "team"},
}

// usageOrgGrouping also keys on the outer levels, so equal names under different parents stay apart.
func usageOrgGrouping(groupBy string) (selectList, groupList string, ok bool) {
	depth := map[string]int{
		UsageAnalyticsGroupBusinessUnit: 1,
		UsageAnalyticsGroupDepartment:   2,
		UsageAnalyticsGroupTeam:         3,
	}[groupBy]
	if depth == 0 {
		return "", "", false
	}
	selects := make([]string, depth)
	groups := make([]string, depth)
	for i, column := range usageOrgColumns[:depth] {
		selects[i] = column.expr + " AS " + column.alias
		groups[i] = column.expr
	}
	return strings.Join(selects, ", "), strings.Join(groups, ", "), true
}

type UsageOrgFilter struct {
	BusinessUnit string
	Department   string
	Team         string
}

func (f UsageOrgFilter) IsZero() bool {
	return f.BusinessUnit == "" && f.Department == "" && f.Team == ""
}

// apply expects quota_data LEFT JOIN users; rows of a missing user are unassigned.
func (f UsageOrgFilter) apply(query *gorm.DB) *gorm.DB {
	for _, level := range []struct{ expr, value string }{
		{usageBusinessUnitExpr, f.BusinessUnit},
		{usageDepartmentExpr, f.Department},
		{usageTeamExpr, f.Team},
	} {
		switch level.value {
		case "":
		case UsageOrgUnassigned:
			query = query.Where(level.expr + " = ''")
		default:
			query = query.Where(level.expr+" = ?", level.value)
		}
	}
	return query
}

const usageUsersJoin = "LEFT JOIN users ON users.id = quota_data.user_id"

const usageUserAttributesSelect = "COALESCE(MAX(users.display_name), '') AS display_name, " +
	"COALESCE(MAX(users.email), '') AS email, " +
	"COALESCE(MAX(users.status), 0) AS user_status, " +
	"COALESCE(MAX(users.secondary_organization), '') AS business_unit, " +
	"COALESCE(MAX(users.tertiary_organization), '') AS department, " +
	"COALESCE(MAX(users.team), '') AS team, " +
	"COALESCE(MAX(users.org_str), '') AS org_path"

// UsageMetrics counts consume requests separately from final failures.
type UsageMetrics struct {
	RequestCount     int64 `json:"request_count"`
	Quota            int64 `json:"quota"`
	TokenUsed        int64 `json:"token_used"`
	InputTokens      int64 `json:"input_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	UseTimeTotal     int64 `json:"use_time_total"`
	UseTimeCount     int64 `json:"use_time_count"`
	StreamErrorCount int64 `json:"stream_error_count"`
	FailedCount      int64 `json:"failed_count"`
	TrackedCount     int64 `json:"tracked_count"`
	LastCalledAt     int64 `json:"last_called_at"`
}

const usageMetricsSelect = "COALESCE(SUM(quota_data.count), 0) AS request_count, " +
	"COALESCE(SUM(quota_data.quota), 0) AS quota, " +
	"COALESCE(SUM(quota_data.token_used), 0) AS token_used, " +
	"COALESCE(SUM(quota_data.input_tokens), 0) AS input_tokens, " +
	"COALESCE(SUM(quota_data.completion_tokens), 0) AS completion_tokens, " +
	"COALESCE(SUM(quota_data.cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(quota_data.cache_write_tokens), 0) AS cache_write_tokens, " +
	"COALESCE(SUM(quota_data.use_time_total), 0) AS use_time_total, " +
	"COALESCE(SUM(quota_data.use_time_count), 0) AS use_time_count, " +
	"COALESCE(SUM(quota_data.stream_error_count), 0) AS stream_error_count, " +
	"COALESCE(SUM(quota_data.failed_count), 0) AS failed_count, " +
	"COALESCE(SUM(quota_data.tracked_count), 0) AS tracked_count, " +
	"COALESCE(MAX(quota_data.last_called_at), 0) AS last_called_at"

// Failure-only users are not active users.
const usageActiveUsersSelect = "COUNT(DISTINCT CASE WHEN quota_data.count > 0 THEN quota_data.user_id END)"

type UsageAnalyticsSummary struct {
	UsageMetrics
	ActiveUsers int64 `json:"active_users"`
	ModelCount  int64 `json:"model_count"`
}

// UsageAnalyticsRow uses pointers to omit unused dimensions while retaining zero values.
type UsageAnalyticsRow struct {
	UsageMetrics
	ModelName   *string `json:"model_name,omitempty"`
	UserID      *int    `json:"user_id,omitempty"`
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	ChannelID   *int    `json:"channel_id,omitempty"`
	ChannelName *string `json:"channel_name,omitempty"`
	UserCount   *int64  `json:"user_count,omitempty"`
	// UserStatus is 0 when the user no longer exists.
	Email        *string `json:"email,omitempty"`
	UserStatus   *int    `json:"user_status,omitempty"`
	BusinessUnit *string `json:"business_unit,omitempty"`
	Department   *string `json:"department,omitempty"`
	Team         *string `json:"team,omitempty"`
	OrgPath      *string `json:"org_path,omitempty"`
}

type UsageAnalyticsResult struct {
	Summary *UsageAnalyticsSummary `json:"summary"`
	Rows    []UsageAnalyticsRow    `json:"rows"`
}

type UsageAnalyticsParams struct {
	Start     int64
	End       int64
	GroupBy   string
	Username  string
	ModelName string
	Org       UsageOrgFilter
	// UserID > 0 restricts the query to one user (self analytics).
	UserID int
}

// GetUsageAnalytics aggregates hourly quota data in [Start, End].
func GetUsageAnalytics(params UsageAnalyticsParams) (*UsageAnalyticsResult, error) {
	query := DB.Table("quota_data").
		Where("quota_data.created_at >= ? AND quota_data.created_at <= ?", params.Start, params.End)
	if params.UserID > 0 {
		query = query.Where("quota_data.user_id = ?", params.UserID)
	}
	if username := strings.TrimSpace(params.Username); username != "" {
		query = query.Where("quota_data.username = ?", username)
	}
	if modelName := strings.TrimSpace(params.ModelName); modelName != "" {
		query = query.Where("quota_data.model_name = ?", modelName)
	}
	orgSelect, orgGroup, orgGrouping := usageOrgGrouping(params.GroupBy)
	// users.id is the primary key, so the join never multiplies quota rows.
	if !params.Org.IsZero() || orgGrouping ||
		params.GroupBy == UsageAnalyticsGroupUser || params.GroupBy == UsageAnalyticsGroupUserModel {
		query = params.Org.apply(query.Joins(usageUsersJoin))
	}

	result := &UsageAnalyticsResult{Rows: []UsageAnalyticsRow{}}
	if orgGrouping {
		err := query.
			Select(orgSelect + ", " + usageActiveUsersSelect + " AS user_count, " + usageMetricsSelect).
			Group(orgGroup).
			Order("quota DESC, " + orgGroup).
			Scan(&result.Rows).Error
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	switch params.GroupBy {
	case UsageAnalyticsGroupSummary:
		summary := &UsageAnalyticsSummary{}
		err := query.
			Select(usageMetricsSelect + ", " + usageActiveUsersSelect + " AS active_users, " +
				"COUNT(DISTINCT CASE WHEN quota_data.count > 0 THEN quota_data.model_name END) AS model_count").
			Scan(summary).Error
		if err != nil {
			return nil, err
		}
		result.Summary = summary
		return result, nil
	case UsageAnalyticsGroupModel:
		query = query.
			Select("quota_data.model_name AS model_name, " + usageActiveUsersSelect + " AS user_count, " + usageMetricsSelect).
			Group("quota_data.model_name").
			Order("request_count DESC, model_name ASC")
	case UsageAnalyticsGroupUser:
		query = query.
			Select("quota_data.user_id AS user_id, quota_data.username AS username, " +
				usageUserAttributesSelect + ", " + usageMetricsSelect).
			Group("quota_data.user_id, quota_data.username").
			Order("quota DESC, username ASC")
	case UsageAnalyticsGroupUserModel:
		query = query.
			Select("quota_data.user_id AS user_id, quota_data.username AS username, " +
				usageUserAttributesSelect + ", quota_data.model_name AS model_name, " + usageMetricsSelect).
			Group("quota_data.user_id, quota_data.username, quota_data.model_name").
			Order("quota DESC, username ASC, model_name ASC")
	case UsageAnalyticsGroupModelChannel:
		query = query.
			Select("quota_data.model_name AS model_name, quota_data.channel_id AS channel_id, " +
				"COALESCE(MAX(channels.name), '') AS channel_name, " + usageMetricsSelect).
			Joins("LEFT JOIN channels ON channels.id = quota_data.channel_id").
			Group("quota_data.model_name, quota_data.channel_id").
			Order("request_count DESC, model_name ASC, channel_id ASC")
	default:
		return nil, fmt.Errorf("unsupported group_by: %s", params.GroupBy)
	}
	if err := query.Scan(&result.Rows).Error; err != nil {
		return nil, err
	}
	return result, nil
}

type UsageOrgOption struct {
	BusinessUnit string `json:"business_unit"`
	Department   string `json:"department"`
	Team         string `json:"team"`
}

// GetUsageOrgOptions keeps soft-deleted users, whose past usage still counts for their path.
func GetUsageOrgOptions() ([]UsageOrgOption, error) {
	options := make([]UsageOrgOption, 0)
	err := DB.Table("users").
		Select(usageBusinessUnitExpr + " AS business_unit, " + usageDepartmentExpr + " AS department, " + usageTeamExpr + " AS team").
		Group(usageBusinessUnitExpr + ", " + usageDepartmentExpr + ", " + usageTeamExpr).
		Order(usageBusinessUnitExpr + ", " + usageDepartmentExpr + ", " + usageTeamExpr).
		Scan(&options).Error
	return options, err
}

type PeakTPMPoint struct {
	Hour    int64 `json:"hour" gorm:"column:hour"`
	PeakTPM int64 `json:"peak_tpm" gorm:"column:peak_tpm"`
}

type PeakTPMParams struct {
	Start     int64
	End       int64
	Username  string
	ModelName string
}

// GetPeakTPM returns hourly maxima of minute-level prompt + completion totals.
func GetPeakTPM(params PeakTPMParams) ([]PeakTPMPoint, error) {
	var minuteExpr, hourExpr string
	switch common.LogDatabaseType() {
	case common.DatabaseTypeMySQL:
		minuteExpr = "created_at DIV 60"
		hourExpr = "(m.minute_bucket DIV 60) * 3600"
	case common.DatabaseTypeClickHouse:
		minuteExpr = "intDiv(created_at, 60)"
		hourExpr = "intDiv(m.minute_bucket, 60) * 3600"
	case common.DatabaseTypePostgreSQL, common.DatabaseTypeSQLite:
		minuteExpr = "created_at / 60"
		hourExpr = "(m.minute_bucket / 60) * 3600"
	default:
		return nil, fmt.Errorf("unsupported log database type: %s", common.LogDatabaseType())
	}

	perMinute := LOG_DB.Table("logs").
		Select(minuteExpr+" AS minute_bucket, SUM(prompt_tokens + completion_tokens) AS tokens").
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at <= ?", params.Start, params.End)
	if username := strings.TrimSpace(params.Username); username != "" {
		perMinute = perMinute.Where("username = ?", username)
	}
	if modelName := strings.TrimSpace(params.ModelName); modelName != "" {
		perMinute = perMinute.Where("model_name = ?", modelName)
	}
	perMinute = perMinute.Group(minuteExpr)

	points := make([]PeakTPMPoint, 0)
	err := LOG_DB.Table("(?) AS m", perMinute).
		Select(hourExpr + " AS hour, MAX(m.tokens) AS peak_tpm").
		Group(hourExpr).
		Order("hour ASC").
		Scan(&points).Error
	if err != nil {
		return nil, err
	}
	return points, nil
}
