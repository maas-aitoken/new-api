package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
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
