package model

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// IselfUsageRow groups consumption by recorded email, not forwarding account.
type IselfUsageRow struct {
	Email        string `json:"email"`
	Source       string `json:"source"`
	ModelName    string `json:"model_name"`
	RequestCount int64  `json:"request_count"`
	Quota        int64  `json:"quota"`
	// InputTokens includes cache reads and writes, matching quota_data.
	InputTokens      int64 `json:"input_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	TokenUsed        int64 `json:"token_used"`
	LastCalledAt     int64 `json:"last_called_at"`
}

type IselfUsageParams struct {
	Start     int64
	End       int64
	Username  string
	ModelName string
	UserID    int
}

func logOtherCacheTokenExprs(column string) (cacheRead, cacheWrite, anthropic string, ok bool) {
	var number, text string
	switch common.LogDatabaseType() {
	case common.DatabaseTypeSQLite:
		doc := "CASE WHEN json_valid(" + column + ") THEN " + column + " ELSE '{}' END"
		number = "COALESCE(CAST(json_extract(" + doc + ", '$.%[1]s') AS INTEGER), 0)"
		text = "json_extract(" + doc + ", '$.%[1]s')"
	case common.DatabaseTypeMySQL:
		doc := "CASE WHEN JSON_VALID(" + column + ") THEN " + column + " ELSE '{}' END"
		number = "COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(" + doc + ", '$.%[1]s')) AS SIGNED), 0)"
		text = "JSON_UNQUOTE(JSON_EXTRACT(" + doc + ", '$.%[1]s'))"
	case common.DatabaseTypePostgreSQL:
		isObject := column + " ~ '^\\s*\\{.*\\}\\s*$'"
		number = "COALESCE(CASE WHEN " + isObject + " THEN CASE WHEN jsonb_typeof(" + column + "::jsonb -> '%[1]s') = 'number' " +
			"THEN (" + column + "::jsonb ->> '%[1]s')::numeric::bigint END END, 0)"
		text = "CASE WHEN " + isObject + " THEN " + column + "::jsonb ->> '%[1]s' END"
	case common.DatabaseTypeClickHouse:
		number = "JSONExtractInt(" + column + ", '%[1]s')"
		text = "JSONExtractString(" + column + ", '%[1]s')"
	default:
		return "", "", "", false
	}
	cacheRead = fmt.Sprintf(number, "cache_tokens")
	written := fmt.Sprintf(number, "cache_write_tokens")
	cacheWrite = "CASE WHEN " + written + " > 0 THEN " + written + " ELSE " + fmt.Sprintf(number, "cache_creation_tokens") + " END"
	anthropic = "(" + fmt.Sprintf(text, "usage_semantic") + " = 'anthropic')"
	return cacheRead, cacheWrite, anthropic, true
}

func GetIselfUsageAnalytics(params IselfUsageParams) ([]IselfUsageRow, error) {
	rows := make([]IselfUsageRow, 0)
	if !logsHaveIselfEmail() {
		return rows, nil
	}
	var email string
	if params.UserID > 0 {
		boundEmail, err := GetUserEmail(params.UserID)
		if err != nil {
			return nil, err
		}
		email = NormalizeEmail(boundEmail)
		// An unbound email must never turn into an unfiltered log query.
		if email == "" {
			return rows, nil
		}
	}

	cacheRead, cacheWrite, anthropic, ok := logOtherCacheTokenExprs("logs.other")
	if !ok {
		return nil, fmt.Errorf("iself usage unsupported on log database type: %s", common.LogDatabaseType())
	}
	inputExpr := "logs.prompt_tokens + CASE WHEN " + anthropic + " THEN " + cacheRead + " + " + cacheWrite + " ELSE 0 END"

	const emailExpr = "LOWER(TRIM(COALESCE(iself_email, '')))"
	query := LOG_DB.Model(&Log{}).
		Where("type = ?", LogTypeConsume).
		Where("created_at >= ? AND created_at <= ?", params.Start, params.End).
		Where(emailExpr + " <> ''")
	if params.UserID > 0 {
		query = query.Where(emailExpr+" = ?", email)
	} else if username := strings.TrimSpace(params.Username); username != "" {
		query = query.Where("username = ?", username)
	}
	if modelName := strings.TrimSpace(params.ModelName); modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}

	// Qualify columns to prevent ClickHouse aggregate-alias substitution.
	err := query.Select(emailExpr + " AS email, 'iself' AS source, model_name, " +
		"COUNT(*) AS request_count, COALESCE(SUM(logs.quota), 0) AS quota, " +
		"COALESCE(SUM(" + inputExpr + "), 0) AS input_tokens, " +
		"COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens, " +
		"COALESCE(SUM(" + cacheRead + "), 0) AS cache_read_tokens, " +
		"COALESCE(SUM(" + cacheWrite + "), 0) AS cache_write_tokens, " +
		"MAX(logs.created_at) AS last_called_at").
		Group(emailExpr + ", model_name").
		Order("quota DESC, email ASC, model_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].TokenUsed = rows[i].InputTokens + rows[i].CompletionTokens
	}
	return rows, nil
}

const iselfEmailExpr = "LOWER(TRIM(COALESCE(logs.iself_email, '')))"

var iselfColumnCache struct {
	sync.Mutex
	db        *gorm.DB
	present   bool
	checkedAt time.Time
}

func logsHaveIselfEmail() bool {
	iselfColumnCache.Lock()
	defer iselfColumnCache.Unlock()
	if iselfColumnCache.db != LOG_DB || time.Since(iselfColumnCache.checkedAt) > time.Minute {
		iselfColumnCache.db = LOG_DB
		iselfColumnCache.present = LOG_DB.Migrator().HasColumn(&Log{}, "iself_email")
		iselfColumnCache.checkedAt = time.Now()
	}
	return iselfColumnCache.present
}

type iselfOwner struct {
	UserID       int    `gorm:"column:user_id"`
	Username     string `gorm:"column:username"`
	DisplayName  string `gorm:"column:display_name"`
	Email        string `gorm:"column:email"`
	UserStatus   int    `gorm:"column:user_status"`
	BusinessUnit string `gorm:"column:business_unit"`
	Department   string `gorm:"column:department"`
	Team         string `gorm:"column:team"`
	OrgPath      string `gorm:"column:org_path"`
}

func resolveIselfOwners(emails []string) (map[string]iselfOwner, error) {
	owners := make(map[string]iselfOwner, len(emails))
	shared := make(map[string]bool)
	for chunk := range slices.Chunk(emails, 500) {
		var users []iselfOwner
		err := DB.Table("users").
			Select("users.id AS user_id, users.username AS username, COALESCE(users.display_name, '') AS display_name, "+
				"LOWER(TRIM(users.email)) AS email, users.status AS user_status, "+
				usageBusinessUnitExpr+" AS business_unit, "+usageDepartmentExpr+" AS department, "+
				usageTeamExpr+" AS team, COALESCE(users.org_str, '') AS org_path").
			Where("users.deleted_at IS NULL").
			Where("LOWER(TRIM(users.email)) IN ?", chunk).
			Scan(&users).Error
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if _, seen := owners[user.Email]; seen || shared[user.Email] {
				shared[user.Email] = true
				delete(owners, user.Email)
				continue
			}
			owners[user.Email] = user
		}
	}
	return owners, nil
}

// iselfMove is iSelf consumption recorded under UserID that belongs to Owner.
type iselfMove struct {
	UserID           int        `gorm:"column:user_id"`
	Username         string     `gorm:"column:username"`
	Email            string     `gorm:"column:email"`
	ModelName        string     `gorm:"column:model_name"`
	Hour             int64      `gorm:"column:hour_bucket"`
	RequestCount     int64      `gorm:"column:request_count"`
	Quota            int64      `gorm:"column:quota"`
	InputTokens      int64      `gorm:"column:input_tokens"`
	CompletionTokens int64      `gorm:"column:completion_tokens"`
	CacheReadTokens  int64      `gorm:"column:cache_read_tokens"`
	CacheWriteTokens int64      `gorm:"column:cache_write_tokens"`
	LastCalledAt     int64      `gorm:"column:last_called_at"`
	Owner            iselfOwner `gorm:"-"`
}

// metrics leaves latency and outcome counters with the forwarding account.
func (m iselfMove) metrics() UsageMetrics {
	return UsageMetrics{
		RequestCount:     m.RequestCount,
		Quota:            m.Quota,
		TokenUsed:        m.InputTokens + m.CompletionTokens,
		InputTokens:      m.InputTokens,
		CompletionTokens: m.CompletionTokens,
		CacheReadTokens:  m.CacheReadTokens,
		CacheWriteTokens: m.CacheWriteTokens,
		LastCalledAt:     m.LastCalledAt,
	}
}

// loadIselfMoves returns iSelf consumption recorded under another account than
// the email's; selfID > 0 keeps only moves into or out of that account.
func loadIselfMoves(start, end int64, modelName string, byModel, byHour bool, selfID int, selfEmail string) ([]iselfMove, error) {
	if !logsHaveIselfEmail() {
		return nil, nil
	}
	cacheRead, cacheWrite, anthropic, ok := logOtherCacheTokenExprs("logs.other")
	if !ok {
		return nil, fmt.Errorf("iself usage unsupported on log database type: %s", common.LogDatabaseType())
	}
	inputExpr := "logs.prompt_tokens + CASE WHEN " + anthropic + " THEN " + cacheRead + " + " + cacheWrite + " ELSE 0 END"

	// ClickHouse substitutes aliases into WHERE, so the hour alias is not created_at.
	keys := []string{"logs.user_id", "logs.username", iselfEmailExpr}
	selects := []string{"logs.user_id AS user_id", "logs.username AS username", iselfEmailExpr + " AS email"}
	if byModel {
		keys = append(keys, "logs.model_name")
		selects = append(selects, "logs.model_name AS model_name")
	}
	if byHour {
		var hourExpr string
		switch common.LogDatabaseType() {
		case common.DatabaseTypeMySQL:
			hourExpr = "(logs.created_at DIV 3600) * 3600"
		case common.DatabaseTypeClickHouse:
			hourExpr = "intDiv(logs.created_at, 3600) * 3600"
		default:
			hourExpr = "(logs.created_at / 3600) * 3600"
		}
		keys = append(keys, hourExpr)
		selects = append(selects, hourExpr+" AS hour_bucket")
	}
	query := LOG_DB.Table("logs").
		Where("logs.type = ?", LogTypeConsume).
		Where("logs.created_at >= ? AND logs.created_at <= ?", start, end).
		Where(iselfEmailExpr + " <> ''")
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		query = query.Where("logs.model_name = ?", modelName)
	}
	if selfID > 0 {
		if selfEmail == "" {
			query = query.Where("logs.user_id = ?", selfID)
		} else {
			query = query.Where("(logs.user_id = ? OR "+iselfEmailExpr+" = ?)", selfID, selfEmail)
		}
	}
	moves := make([]iselfMove, 0)
	err := query.Select(strings.Join(selects, ", ") + ", COUNT(*) AS request_count, " +
		"COALESCE(SUM(logs.quota), 0) AS quota, " +
		"COALESCE(SUM(" + inputExpr + "), 0) AS input_tokens, " +
		"COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens, " +
		"COALESCE(SUM(" + cacheRead + "), 0) AS cache_read_tokens, " +
		"COALESCE(SUM(" + cacheWrite + "), 0) AS cache_write_tokens, " +
		"MAX(logs.created_at) AS last_called_at").
		Group(strings.Join(keys, ", ")).
		Scan(&moves).Error
	if err != nil {
		return nil, err
	}

	emails := make([]string, 0)
	seen := make(map[string]bool)
	for _, move := range moves {
		if !seen[move.Email] {
			seen[move.Email] = true
			emails = append(emails, move.Email)
		}
	}
	owners, err := resolveIselfOwners(emails)
	if err != nil {
		return nil, err
	}
	result := moves[:0]
	for _, move := range moves {
		owner, ok := owners[move.Email]
		if !ok || owner.UserID == move.UserID {
			continue
		}
		move.Owner = owner
		result = append(result, move)
	}
	return result, nil
}
