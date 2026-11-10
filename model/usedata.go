package model

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup  string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID   int    `json:"token_id" gorm:"index;default:0"`
	ChannelID int    `json:"channel_id" gorm:"index;default:0"`
	NodeName  string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed int    `json:"token_used" gorm:"default:0"`
	Count     int    `json:"count" gorm:"default:0"`
	Quota     int    `json:"quota" gorm:"default:0"`

	InputTokens         int   `json:"input_tokens" gorm:"default:0"`
	CompletionTokens    int   `json:"completion_tokens" gorm:"default:0"`
	CacheReadTokens     int64 `json:"cache_read_tokens" gorm:"default:0"`
	CacheWriteTokens    int   `json:"cache_write_tokens" gorm:"default:0"`
	CacheCreationTokens int64 `json:"cache_creation_tokens" gorm:"default:0"`
	TrackedCount        int   `json:"tracked_count" gorm:"default:0"`
	UseTimeTotal        int64 `json:"use_time_total" gorm:"default:0"` // seconds, successful tracked requests
	UseTimeCount        int64 `json:"use_time_count" gorm:"default:0"`
	StreamErrorCount    int   `json:"stream_error_count" gorm:"default:0"`
	FailedCount         int   `json:"failed_count" gorm:"default:0"` // excludes intermediate retries
	LastCalledAt        int64 `json:"last_called_at" gorm:"default:0"`
}

type QuotaDataLogParams struct {
	UserID              int
	Username            string
	ModelName           string
	Quota               int
	CreatedAt           int64
	TokenUsed           int
	UseGroup            string
	TokenID             int
	ChannelID           int
	NodeName            string
	InputTokens         int
	CompletionTokens    int
	CacheReadTokens     int64
	CacheCreationTokens int64
	CacheWriteTokens    int
	UseTimeSeconds      int
	IsStreamError       bool
	OutcomeTracked      bool
	IsFinalFailure      bool // after all retries
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog(common.LogText("updating dashboard data..."))
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(quotaData *QuotaData) {
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	if cached, ok := CacheQuotaData[key]; ok {
		cached.Count += quotaData.Count
		cached.Quota += quotaData.Quota
		cached.TokenUsed += quotaData.TokenUsed
		cached.InputTokens += quotaData.InputTokens
		cached.CompletionTokens += quotaData.CompletionTokens
		cached.CacheReadTokens += quotaData.CacheReadTokens
		cached.CacheWriteTokens += quotaData.CacheWriteTokens
		cached.CacheCreationTokens += quotaData.CacheCreationTokens
		cached.TrackedCount += quotaData.TrackedCount
		cached.UseTimeTotal += quotaData.UseTimeTotal
		cached.UseTimeCount += quotaData.UseTimeCount
		cached.StreamErrorCount += quotaData.StreamErrorCount
		cached.FailedCount += quotaData.FailedCount
		if quotaData.LastCalledAt > cached.LastCalledAt {
			cached.LastCalledAt = quotaData.LastCalledAt
		}
	} else {
		CacheQuotaData[key] = quotaData
	}
}

func LogQuotaData(params QuotaDataLogParams) {
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)

	count, trackedCount, failedCount, streamErrorCount := 1, 0, 0, 0
	useTimeTotal, useTimeCount := int64(0), int64(0)
	switch {
	case params.IsFinalFailure:
		count, failedCount = 0, 1
	case params.OutcomeTracked && params.IsStreamError:
		trackedCount, streamErrorCount = 1, 1
	case params.OutcomeTracked:
		// Include zero-second samples to avoid inflating average latency.
		trackedCount = 1
		useTimeTotal, useTimeCount = int64(max(params.UseTimeSeconds, 0)), 1
	}

	quotaData := &QuotaData{
		UserID:              params.UserID,
		Username:            params.Username,
		ModelName:           params.ModelName,
		CreatedAt:           createdAt,
		UseGroup:            params.UseGroup,
		TokenID:             params.TokenID,
		ChannelID:           params.ChannelID,
		NodeName:            params.NodeName,
		Count:               count,
		Quota:               params.Quota,
		TokenUsed:           params.TokenUsed,
		InputTokens:         params.InputTokens,
		CompletionTokens:    params.CompletionTokens,
		CacheReadTokens:     max(0, params.CacheReadTokens),
		CacheCreationTokens: max(0, params.CacheCreationTokens),
		CacheWriteTokens:    params.CacheWriteTokens,
		TrackedCount:        trackedCount,
		UseTimeTotal:        useTimeTotal,
		UseTimeCount:        useTimeCount,
		StreamErrorCount:    streamErrorCount,
		FailedCount:         failedCount,
		LastCalledAt:        params.CreatedAt,
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(quotaData)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range CacheQuotaData {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").
			Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
				quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName).
			First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			//quotaDataDB.Count += quotaData.Count
			//quotaDataDB.Quota += quotaData.Quota
			//DB.Table("quota_data").Save(quotaDataDB)
			increaseQuotaData(quotaData)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(common.LogText("saved dashboard data, %d records", size))
}

func lastCalledAtExpr(newTs int64) interface{} {
	return gorm.Expr("CASE WHEN last_called_at < ? THEN ? ELSE last_called_at END", newTs, newTs)
}

func increaseQuotaData(quotaData *QuotaData) {
	updates := map[string]interface{}{
		"count":                 gorm.Expr("count + ?", quotaData.Count),
		"quota":                 gorm.Expr("quota + ?", quotaData.Quota),
		"token_used":            gorm.Expr("token_used + ?", quotaData.TokenUsed),
		"input_tokens":          gorm.Expr("input_tokens + ?", quotaData.InputTokens),
		"completion_tokens":     gorm.Expr("completion_tokens + ?", quotaData.CompletionTokens),
		"cache_read_tokens":     gorm.Expr("cache_read_tokens + ?", quotaData.CacheReadTokens),
		"cache_write_tokens":    gorm.Expr("cache_write_tokens + ?", quotaData.CacheWriteTokens),
		"cache_creation_tokens": gorm.Expr("cache_creation_tokens + ?", quotaData.CacheCreationTokens),
		"tracked_count":         gorm.Expr("tracked_count + ?", quotaData.TrackedCount),
		"use_time_total":        gorm.Expr("use_time_total + ?", quotaData.UseTimeTotal),
		"use_time_count":        gorm.Expr("use_time_count + ?", quotaData.UseTimeCount),
		"stream_error_count":    gorm.Expr("stream_error_count + ?", quotaData.StreamErrorCount),
		"failed_count":          gorm.Expr("failed_count + ?", quotaData.FailedCount),
		"last_called_at":        lastCalledAtExpr(quotaData.LastCalledAt),
	}
	err := DB.Table("quota_data").
		Where("user_id = ? and username = ? and model_name = ? and created_at = ? and use_group = ? and token_id = ? and channel_id = ? and node_name = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.UseGroup, quotaData.TokenID, quotaData.ChannelID, quotaData.NodeName).
		Updates(updates).Error
	if err != nil {
		common.SysLog(common.LogText("increaseQuotaData error: %s", err))
	}
}

func (row attributedUsageRow) chartQuotaData() *QuotaData {
	return &QuotaData{
		UserID: row.UserID, Username: row.Username, ModelName: row.ModelName, CreatedAt: row.Hour,
		Count: int(row.RequestCount), Quota: int(row.Quota), TokenUsed: int(row.TokenUsed),
		CacheReadTokens: row.CacheReadTokens, CacheCreationTokens: row.CacheCreationTokens,
	}
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) ([]*QuotaData, error) {
	rows, err := loadAttributedUsage(attributedUsageQuery{
		Start: startTime, End: endTime, Scope: usageScope{UserID: userId}, ByModel: true, ByHour: true,
	})
	if err != nil {
		return nil, err
	}
	quotaDatas := make([]*QuotaData, 0, len(rows))
	for _, row := range rows {
		if row.RequestCount > 0 {
			quotaDatas = append(quotaDatas, row.chartQuotaData())
		}
	}
	return quotaDatas, nil
}

// Columns are qualified because organization filters join users.
const quotaChartSumsSelect = "SUM(quota_data.count) AS count, SUM(quota_data.quota) AS quota, " +
	"SUM(quota_data.token_used) AS token_used, SUM(quota_data.cache_read_tokens) AS cache_read_tokens, " +
	"SUM(quota_data.cache_creation_tokens) AS cache_creation_tokens"

type UserQuotaTrendRow struct {
	Username            string  `json:"username,omitempty"`
	BusinessUnit        *string `json:"business_unit,omitempty"`
	Department          *string `json:"department,omitempty"`
	Team                *string `json:"team,omitempty"`
	CreatedAt           int64   `json:"created_at"`
	Count               int64   `json:"count"`
	Quota               int64   `json:"quota"`
	TokenUsed           int64   `json:"token_used"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
}

// GetUserQuotaTrend returns hourly consumption per username or per organization path.
func GetUserQuotaTrend(startTime, endTime int64, dimension string, org UsageOrgFilter) ([]UserQuotaTrendRow, error) {
	depth := usageOrgDepth(dimension)
	if depth == 0 && dimension != UsageAnalyticsGroupUser {
		return nil, fmt.Errorf("unsupported dimension: %s", dimension)
	}
	rows, err := loadAttributedUsage(attributedUsageQuery{
		Start: startTime, End: endTime, Scope: usageScope{Org: org}, ByHour: true,
	})
	if err != nil {
		return nil, err
	}
	type trendKey struct {
		username, businessUnit, department, team string
		hour                                     int64
	}
	trend := make([]UserQuotaTrendRow, 0)
	index := make(map[trendKey]int)
	for _, row := range rows {
		if row.RequestCount == 0 {
			continue
		}
		key := trendKey{hour: row.Hour}
		if depth == 0 {
			key.username = row.Username
		}
		if depth >= 1 {
			key.businessUnit = row.BusinessUnit
		}
		if depth >= 2 {
			key.department = row.Department
		}
		if depth >= 3 {
			key.team = row.Team
		}
		i, ok := index[key]
		if !ok {
			point := UserQuotaTrendRow{Username: key.username, CreatedAt: row.Hour}
			if depth >= 1 {
				point.BusinessUnit = &row.BusinessUnit
			}
			if depth >= 2 {
				point.Department = &row.Department
			}
			if depth >= 3 {
				point.Team = &row.Team
			}
			trend = append(trend, point)
			i = len(trend) - 1
			index[key] = i
		}
		trend[i].Count += row.RequestCount
		trend[i].Quota += row.Quota
		trend[i].TokenUsed += row.TokenUsed
		trend[i].CacheReadTokens += row.CacheReadTokens
		trend[i].CacheCreationTokens += row.CacheCreationTokens
	}
	return trend, nil
}

func GetAllQuotaDates(startTime int64, endTime int64, username string, org UsageOrgFilter) ([]*QuotaData, error) {
	scope := usageScope{Username: strings.TrimSpace(username), Org: org}
	if scope.IsZero() {
		// iSelf moves do not change the totals over all accounts.
		var quotaDatas []*QuotaData
		err := DB.Table("quota_data").
			Select("quota_data.model_name AS model_name, quota_data.created_at AS created_at, "+quotaChartSumsSelect).
			Where("quota_data.created_at >= ? AND quota_data.created_at <= ? AND quota_data.count > 0", startTime, endTime).
			Group("quota_data.model_name, quota_data.created_at").
			Find(&quotaDatas).Error
		return quotaDatas, err
	}
	rows, err := loadAttributedUsage(attributedUsageQuery{
		Start: startTime, End: endTime, Scope: scope, ByModel: true, ByHour: true,
	})
	if err != nil {
		return nil, err
	}
	type chartKey struct {
		model string
		hour  int64
	}
	quotaDatas := make([]*QuotaData, 0)
	index := make(map[chartKey]*QuotaData)
	for _, row := range rows {
		if row.RequestCount == 0 {
			continue
		}
		point := row.chartQuotaData()
		if scope.Username != "" {
			quotaDatas = append(quotaDatas, point)
			continue
		}
		key := chartKey{row.ModelName, row.Hour}
		if total, ok := index[key]; ok {
			total.Count += point.Count
			total.Quota += point.Quota
			total.TokenUsed += point.TokenUsed
			total.CacheReadTokens += point.CacheReadTokens
			total.CacheCreationTokens += point.CacheCreationTokens
			continue
		}
		point.UserID, point.Username = 0, ""
		index[key] = point
		quotaDatas = append(quotaDatas, point)
	}
	return quotaDatas, nil
}

// TokenPeriodUsage 是一个令牌在一段时间里的调用次数和消耗额度。
type TokenPeriodUsage struct {
	TokenID int   `gorm:"column:token_id"`
	Calls   int64 `gorm:"column:calls"`
	Quota   int64 `gorm:"column:quota"`
}

// GetTokensUsageSince 从看板的小时汇总表 quota_data 汇总这些令牌从 since 起的调用次数和额度，
// 键是令牌 ID。count 只记成功的调用（最终失败的那行 count 为 0），所以次数不含失败。
// 汇总表按 DataExportInterval 落库、只在 DataExportEnabled 开着时写，最近几分钟的调用可能还没进来。
func GetTokensUsageSince(tokenIds []int, since int64) (map[int]TokenPeriodUsage, error) {
	usage := make(map[int]TokenPeriodUsage, len(tokenIds))
	if len(tokenIds) == 0 {
		return usage, nil
	}
	var rows []TokenPeriodUsage
	err := DB.Table("quota_data").
		Select("token_id, sum(count) as calls, sum(quota) as quota").
		Where("token_id IN ? AND created_at >= ?", tokenIds, since).
		Group("token_id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		usage[row.TokenID] = row
	}
	return usage, nil
}
