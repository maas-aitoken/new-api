package model

import (
	"cmp"
	"fmt"
	"slices"
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

// usageOrgDepth also keys on the outer levels, so equal names under different parents stay apart.
func usageOrgDepth(groupBy string) int {
	return map[string]int{
		UsageAnalyticsGroupBusinessUnit: 1,
		UsageAnalyticsGroupDepartment:   2,
		UsageAnalyticsGroupTeam:         3,
	}[groupBy]
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

func (f UsageOrgFilter) matches(businessUnit, department, team string) bool {
	for _, level := range [][2]string{{f.BusinessUnit, businessUnit}, {f.Department, department}, {f.Team, team}} {
		switch level[0] {
		case "":
		case UsageOrgUnassigned:
			if level[1] != "" {
				return false
			}
		default:
			if level[1] != level[0] {
				return false
			}
		}
	}
	return true
}

type usageScope struct {
	UserID   int
	Username string
	Org      UsageOrgFilter
}

func (s usageScope) IsZero() bool {
	return s.UserID == 0 && s.Username == "" && s.Org.IsZero()
}

func (s usageScope) covers(owner iselfOwner) bool {
	return (s.UserID == 0 || owner.UserID == s.UserID) &&
		(s.Username == "" || owner.Username == s.Username) &&
		s.Org.matches(owner.BusinessUnit, owner.Department, owner.Team)
}

type attributedUsageQuery struct {
	Start     int64
	End       int64
	ModelName string
	Scope     usageScope
	ByModel   bool
	ByHour    bool
}

type attributedUsageRow struct {
	UsageMetrics
	UserID       int    `gorm:"column:user_id"`
	Username     string `gorm:"column:username"`
	ModelName    string `gorm:"column:model_name"`
	Hour         int64  `gorm:"column:hour_bucket"`
	DisplayName  string `gorm:"column:display_name"`
	Email        string `gorm:"column:email"`
	UserStatus   int    `gorm:"column:user_status"`
	BusinessUnit string `gorm:"column:business_unit"`
	Department   string `gorm:"column:department"`
	Team         string `gorm:"column:team"`
	OrgPath      string `gorm:"column:org_path"`
	// Charts read this column; rows older than cache_write_tokens only have it.
	CacheCreationTokens int64 `gorm:"column:cache_creation_tokens"`
}

// loadAttributedUsage aggregates quota data per account and moves iSelf
// consumption to the email's account at read time; stored rows are unchanged.
func loadAttributedUsage(q attributedUsageQuery) ([]attributedUsageRow, error) {
	keys := []string{"quota_data.user_id", "quota_data.username"}
	selects := []string{"quota_data.user_id AS user_id", "quota_data.username AS username"}
	if q.ByModel {
		keys = append(keys, "quota_data.model_name")
		selects = append(selects, "quota_data.model_name AS model_name")
	}
	if q.ByHour {
		keys = append(keys, "quota_data.created_at")
		selects = append(selects, "quota_data.created_at AS hour_bucket")
	}
	query := DB.Table("quota_data").
		Joins(usageUsersJoin).
		Where("quota_data.created_at >= ? AND quota_data.created_at <= ?", q.Start, q.End)
	if q.Scope.UserID > 0 {
		query = query.Where("quota_data.user_id = ?", q.Scope.UserID)
	}
	if q.Scope.Username != "" {
		query = query.Where("quota_data.username = ?", q.Scope.Username)
	}
	if modelName := strings.TrimSpace(q.ModelName); modelName != "" {
		query = query.Where("quota_data.model_name = ?", modelName)
	}
	rows := make([]attributedUsageRow, 0)
	err := q.Scope.Org.apply(query).
		Select(strings.Join(selects, ", ") + ", " + usageUserAttributesSelect + ", " + usageMetricsSelect +
			", COALESCE(SUM(quota_data.cache_creation_tokens), 0) AS cache_creation_tokens").
		Group(strings.Join(keys, ", ")).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	selfEmail := ""
	if q.Scope.UserID > 0 {
		email, err := GetUserEmail(q.Scope.UserID)
		if err != nil {
			return nil, err
		}
		selfEmail = NormalizeEmail(email)
	}
	moves, err := loadIselfMoves(q.Start, q.End, q.ModelName, q.ByModel, q.ByHour, q.Scope.UserID, selfEmail)
	if err != nil {
		return nil, err
	}
	type rowKey struct {
		userID          int
		username, model string
		hour            int64
	}
	index := make(map[rowKey]int, len(rows))
	for i, row := range rows {
		index[rowKey{row.UserID, row.Username, row.ModelName, row.Hour}] = i
	}
	for _, move := range moves {
		delta := move.metrics()
		if i, ok := index[rowKey{move.UserID, move.Username, move.ModelName, move.Hour}]; ok {
			rows[i].UsageMetrics.subtract(delta)
			rows[i].CacheCreationTokens = max(rows[i].CacheCreationTokens-move.CacheWriteTokens, 0)
		}
		if !q.Scope.covers(move.Owner) {
			continue
		}
		key := rowKey{move.Owner.UserID, move.Owner.Username, move.ModelName, move.Hour}
		i, ok := index[key]
		if !ok {
			owner := move.Owner
			rows = append(rows, attributedUsageRow{
				UserID: owner.UserID, Username: owner.Username, ModelName: move.ModelName, Hour: move.Hour,
				DisplayName: owner.DisplayName, Email: owner.Email, UserStatus: owner.UserStatus,
				BusinessUnit: owner.BusinessUnit, Department: owner.Department, Team: owner.Team, OrgPath: owner.OrgPath,
			})
			i = len(rows) - 1
			index[key] = i
		}
		rows[i].UsageMetrics.add(delta)
		rows[i].CacheCreationTokens += move.CacheWriteTokens
	}
	return rows, nil
}

func (m *UsageMetrics) add(other UsageMetrics) {
	m.RequestCount += other.RequestCount
	m.Quota += other.Quota
	m.TokenUsed += other.TokenUsed
	m.InputTokens += other.InputTokens
	m.CompletionTokens += other.CompletionTokens
	m.CacheReadTokens += other.CacheReadTokens
	m.CacheWriteTokens += other.CacheWriteTokens
	m.UseTimeTotal += other.UseTimeTotal
	m.UseTimeCount += other.UseTimeCount
	m.StreamErrorCount += other.StreamErrorCount
	m.FailedCount += other.FailedCount
	m.TrackedCount += other.TrackedCount
	m.LastCalledAt = max(m.LastCalledAt, other.LastCalledAt)
}

// subtract stops at zero: quota_data can hold less than the consume logs.
func (m *UsageMetrics) subtract(other UsageMetrics) {
	m.RequestCount = max(m.RequestCount-other.RequestCount, 0)
	m.Quota = max(m.Quota-other.Quota, 0)
	m.TokenUsed = max(m.TokenUsed-other.TokenUsed, 0)
	m.InputTokens = max(m.InputTokens-other.InputTokens, 0)
	m.CompletionTokens = max(m.CompletionTokens-other.CompletionTokens, 0)
	m.CacheReadTokens = max(m.CacheReadTokens-other.CacheReadTokens, 0)
	m.CacheWriteTokens = max(m.CacheWriteTokens-other.CacheWriteTokens, 0)
	m.UseTimeTotal = max(m.UseTimeTotal-other.UseTimeTotal, 0)
	m.UseTimeCount = max(m.UseTimeCount-other.UseTimeCount, 0)
	m.StreamErrorCount = max(m.StreamErrorCount-other.StreamErrorCount, 0)
	m.FailedCount = max(m.FailedCount-other.FailedCount, 0)
	m.TrackedCount = max(m.TrackedCount-other.TrackedCount, 0)
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
	scope := usageScope{UserID: params.UserID, Username: strings.TrimSpace(params.Username), Org: params.Org}
	result := &UsageAnalyticsResult{Rows: []UsageAnalyticsRow{}}
	if params.GroupBy == UsageAnalyticsGroupModelChannel {
		// iSelf moves have no channel, so this grouping keeps the recorded accounts.
		query := DB.Table("quota_data").
			Where("quota_data.created_at >= ? AND quota_data.created_at <= ?", params.Start, params.End)
		if scope.UserID > 0 {
			query = query.Where("quota_data.user_id = ?", scope.UserID)
		}
		if scope.Username != "" {
			query = query.Where("quota_data.username = ?", scope.Username)
		}
		if modelName := strings.TrimSpace(params.ModelName); modelName != "" {
			query = query.Where("quota_data.model_name = ?", modelName)
		}
		if !scope.Org.IsZero() {
			query = scope.Org.apply(query.Joins(usageUsersJoin))
		}
		err := query.
			Select("quota_data.model_name AS model_name, quota_data.channel_id AS channel_id, " +
				"COALESCE(MAX(channels.name), '') AS channel_name, " + usageMetricsSelect).
			Joins("LEFT JOIN channels ON channels.id = quota_data.channel_id").
			Group("quota_data.model_name, quota_data.channel_id").
			Order("request_count DESC, model_name ASC, channel_id ASC").
			Scan(&result.Rows).Error
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	orgDepth := usageOrgDepth(params.GroupBy)
	switch params.GroupBy {
	case UsageAnalyticsGroupSummary, UsageAnalyticsGroupModel, UsageAnalyticsGroupUser, UsageAnalyticsGroupUserModel:
	default:
		if orgDepth == 0 {
			return nil, fmt.Errorf("unsupported group_by: %s", params.GroupBy)
		}
	}
	rows, err := loadAttributedUsage(attributedUsageQuery{
		Start: params.Start, End: params.End, ModelName: params.ModelName, Scope: scope, ByModel: true,
	})
	if err != nil {
		return nil, err
	}

	if params.GroupBy == UsageAnalyticsGroupSummary {
		summary := &UsageAnalyticsSummary{}
		users := make(map[int]bool)
		models := make(map[string]bool)
		for _, row := range rows {
			summary.UsageMetrics.add(row.UsageMetrics)
			if row.RequestCount > 0 {
				users[row.UserID] = true
				models[row.ModelName] = true
			}
		}
		summary.ActiveUsers = int64(len(users))
		summary.ModelCount = int64(len(models))
		result.Summary = summary
		return result, nil
	}

	type groupKey struct {
		userID                         int
		username, model                string
		businessUnit, department, team string
	}
	type group struct {
		key   groupKey
		row   UsageAnalyticsRow
		users map[int]bool
	}
	groups := make(map[groupKey]*group)
	for _, row := range rows {
		var key groupKey
		switch params.GroupBy {
		case UsageAnalyticsGroupModel:
			key.model = row.ModelName
		case UsageAnalyticsGroupUser:
			key.userID, key.username = row.UserID, row.Username
		case UsageAnalyticsGroupUserModel:
			key.userID, key.username, key.model = row.UserID, row.Username, row.ModelName
		}
		if orgDepth >= 1 {
			key.businessUnit = row.BusinessUnit
		}
		if orgDepth >= 2 {
			key.department = row.Department
		}
		if orgDepth >= 3 {
			key.team = row.Team
		}
		g, ok := groups[key]
		if !ok {
			g = &group{key: key, users: make(map[int]bool)}
			switch params.GroupBy {
			case UsageAnalyticsGroupModel:
				g.row.ModelName = &row.ModelName
			case UsageAnalyticsGroupUser, UsageAnalyticsGroupUserModel:
				g.row.UserID, g.row.Username, g.row.DisplayName = &row.UserID, &row.Username, &row.DisplayName
				g.row.Email, g.row.UserStatus, g.row.OrgPath = &row.Email, &row.UserStatus, &row.OrgPath
				g.row.BusinessUnit, g.row.Department, g.row.Team = &row.BusinessUnit, &row.Department, &row.Team
				if params.GroupBy == UsageAnalyticsGroupUserModel {
					g.row.ModelName = &row.ModelName
				}
			}
			if orgDepth >= 1 {
				g.row.BusinessUnit = &row.BusinessUnit
			}
			if orgDepth >= 2 {
				g.row.Department = &row.Department
			}
			if orgDepth >= 3 {
				g.row.Team = &row.Team
			}
			groups[key] = g
		}
		g.row.UsageMetrics.add(row.UsageMetrics)
		if row.RequestCount > 0 {
			g.users[row.UserID] = true
		}
	}

	sorted := make([]*group, 0, len(groups))
	for _, g := range groups {
		sorted = append(sorted, g)
	}
	slices.SortFunc(sorted, func(a, b *group) int {
		if params.GroupBy == UsageAnalyticsGroupModel {
			return cmp.Or(cmp.Compare(b.row.RequestCount, a.row.RequestCount), cmp.Compare(a.key.model, b.key.model))
		}
		return cmp.Or(cmp.Compare(b.row.Quota, a.row.Quota),
			cmp.Compare(a.key.username, b.key.username), cmp.Compare(a.key.userID, b.key.userID), cmp.Compare(a.key.model, b.key.model),
			cmp.Compare(a.key.businessUnit, b.key.businessUnit), cmp.Compare(a.key.department, b.key.department), cmp.Compare(a.key.team, b.key.team))
	})
	for _, g := range sorted {
		if params.GroupBy == UsageAnalyticsGroupModel || orgDepth > 0 {
			userCount := int64(len(g.users))
			g.row.UserCount = &userCount
		}
		result.Rows = append(result.Rows, g.row)
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
	Org       UsageOrgFilter
	// UserID > 0 restricts the peaks to one user (self analytics).
	UserID int
}

// iselfAttributedLogFilter selects the logs of scoped accounts, minus iSelf
// requests for accounts outside the scope, plus iSelf requests for scoped ones.
func iselfAttributedLogFilter(scope usageScope, start, end int64, modelName string) (condition string, args []any, err error) {
	var scopeUsers []iselfOwner
	users := DB.Table("users").
		Select("users.id AS user_id, users.username AS username, LOWER(TRIM(users.email)) AS email, " +
			usageBusinessUnitExpr + " AS business_unit, " + usageDepartmentExpr + " AS department, " + usageTeamExpr + " AS team")
	if scope.UserID > 0 {
		users = users.Where("users.id = ?", scope.UserID)
	}
	if scope.Username != "" {
		users = users.Where("users.username = ?", scope.Username)
	}
	if err := scope.Org.apply(users).Scan(&scopeUsers).Error; err != nil {
		return "", nil, err
	}

	var own []string
	if scope.UserID > 0 {
		own, args = append(own, "logs.user_id = ?"), append(args, scope.UserID)
	}
	if scope.Username != "" {
		own, args = append(own, "logs.username = ?"), append(args, scope.Username)
	}
	if !scope.Org.IsZero() {
		ids := make([]int, 0, len(scopeUsers))
		for _, user := range scopeUsers {
			ids = append(ids, user.UserID)
		}
		if len(ids) == 0 {
			own = append(own, "1 = 0")
		} else {
			own, args = append(own, "logs.user_id IN ?"), append(args, ids)
		}
	}
	ownCondition := strings.Join(own, " AND ")
	if !logsHaveIselfEmail() {
		return "(" + ownCondition + ")", args, nil
	}

	scopeEmails := make([]string, 0, len(scopeUsers))
	for _, user := range scopeUsers {
		if user.Email != "" {
			scopeEmails = append(scopeEmails, user.Email)
		}
	}
	owners, err := resolveIselfOwners(scopeEmails)
	if err != nil {
		return "", nil, err
	}
	inEmails := make([]string, 0)
	for email, owner := range owners {
		if scope.covers(owner) {
			inEmails = append(inEmails, email)
		}
	}

	var recorded []string
	query := LOG_DB.Table("logs").
		Where("logs.type = ?", LogTypeConsume).
		Where("logs.created_at >= ? AND logs.created_at <= ?", start, end).
		Where(iselfEmailExpr+" <> ''").
		Where(ownCondition, args...)
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		query = query.Where("logs.model_name = ?", modelName)
	}
	if err := query.Distinct(iselfEmailExpr).Pluck(iselfEmailExpr, &recorded).Error; err != nil {
		return "", nil, err
	}
	recordedOwners, err := resolveIselfOwners(recorded)
	if err != nil {
		return "", nil, err
	}
	outEmails := make([]string, 0)
	for email, owner := range recordedOwners {
		if !scope.covers(owner) {
			outEmails = append(outEmails, email)
		}
	}

	condition = "(" + ownCondition + ")"
	if len(outEmails) > 0 {
		condition = "(" + condition + " AND " + iselfEmailExpr + " NOT IN ?)"
		args = append(args, outEmails)
	}
	if len(inEmails) > 0 {
		condition = "(" + condition + " OR " + iselfEmailExpr + " IN ?)"
		args = append(args, inEmails)
	}
	return condition, args, nil
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

	points := make([]PeakTPMPoint, 0)
	perMinute := LOG_DB.Table("logs").
		Select(minuteExpr+" AS minute_bucket, SUM(prompt_tokens + completion_tokens) AS tokens").
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at <= ?", params.Start, params.End)
	if modelName := strings.TrimSpace(params.ModelName); modelName != "" {
		perMinute = perMinute.Where("model_name = ?", modelName)
	}
	scope := usageScope{UserID: params.UserID, Username: strings.TrimSpace(params.Username), Org: params.Org}
	if !scope.IsZero() {
		condition, args, err := iselfAttributedLogFilter(scope, params.Start, params.End, params.ModelName)
		if err != nil {
			return nil, err
		}
		perMinute = perMinute.Where(condition, args...)
	}
	perMinute = perMinute.Group(minuteExpr)

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
