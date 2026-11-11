package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ExternalQuotaData stores hourly usage from a remote new-api.
type ExternalQuotaData struct {
	ID                     int64   `json:"id" gorm:"primaryKey"`
	Platform               string  `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_quota_key,priority:1;index"`
	RemoteUserID           int64   `json:"remote_user_id" gorm:"not null;uniqueIndex:idx_external_quota_key,priority:2;index"`
	RemoteUsername         string  `json:"remote_username" gorm:"type:varchar(128);not null;default:'';index"`
	LocalUserID            int     `json:"local_user_id" gorm:"not null;default:0;index"`
	ModelName              string  `json:"model_name" gorm:"type:varchar(191);not null;default:'';uniqueIndex:idx_external_quota_key,priority:3;index"`
	CreatedAt              int64   `json:"created_at" gorm:"not null;uniqueIndex:idx_external_quota_key,priority:4;index"`
	RemoteTokenID          int     `json:"remote_token_id" gorm:"not null;default:0;uniqueIndex:idx_external_quota_key,priority:5;index"`
	RemoteChannelID        int     `json:"remote_channel_id" gorm:"not null;default:0;uniqueIndex:idx_external_quota_key,priority:6;index"`
	RemoteChannelName      string  `json:"remote_channel_name" gorm:"type:varchar(191);not null;default:''"`
	NodeName               string  `json:"node_name" gorm:"type:varchar(128);not null;default:'';uniqueIndex:idx_external_quota_key,priority:7"`
	TokenUsed              int64   `json:"token_used" gorm:"not null;default:0"`
	Count                  int64   `json:"count" gorm:"not null;default:0"`
	Quota                  int64   `json:"quota" gorm:"not null;default:0"`
	InputTokens            int64   `json:"input_tokens" gorm:"not null;default:0"`
	CompletionTokens       int64   `json:"completion_tokens" gorm:"not null;default:0"`
	CacheReadTokens        int64   `json:"cache_read_tokens" gorm:"not null;default:0"`
	CacheWriteTokens       int64   `json:"cache_write_tokens" gorm:"not null;default:0"`
	CacheCreationTokens    int64   `json:"cache_creation_tokens" gorm:"not null;default:0"`
	TrackedCount           int64   `json:"tracked_count" gorm:"not null;default:0"`
	UseTimeTotal           int64   `json:"use_time_total" gorm:"not null;default:0"`
	UseTimeCount           int64   `json:"use_time_count" gorm:"not null;default:0"`
	StreamErrorCount       int64   `json:"stream_error_count" gorm:"not null;default:0"`
	FailedCount            int64   `json:"failed_count" gorm:"not null;default:0"`
	LastCalledAt           int64   `json:"last_called_at" gorm:"not null;default:0"`
	CostMultiplierSnapshot float64 `json:"cost_multiplier_snapshot" gorm:"type:decimal(20,8);not null;default:0"`
}

func (ExternalQuotaData) TableName() string { return "external_quota_data" }

type ExternalUsageSyncState struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	Platform         string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex"`
	LastSuccessEnd   int64  `json:"last_success_end" gorm:"not null;default:0"`
	LastAttemptStart int64  `json:"last_attempt_start" gorm:"not null;default:0"`
	LastAttemptEnd   int64  `json:"last_attempt_end" gorm:"not null;default:0"`
	LastStatus       string `json:"last_status" gorm:"type:varchar(32);not null;default:''"`
	LastError        string `json:"last_error" gorm:"type:varchar(2048);not null;default:''"`
	LastSyncedAt     int64  `json:"last_synced_at" gorm:"not null;default:0"`
}

func (ExternalUsageSyncState) TableName() string { return "external_usage_sync_states" }

type ExternalPeakTPM struct {
	ID       int64  `json:"id" gorm:"primaryKey"`
	Platform string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_peak_tpm,priority:1"`
	Hour     int64  `json:"hour" gorm:"not null;uniqueIndex:idx_external_peak_tpm,priority:2;index"`
	PeakTPM  int64  `json:"peak_tpm" gorm:"not null;default:0"`
}

func (ExternalPeakTPM) TableName() string { return "external_peak_tpm" }

type ExternalChannelCapability struct {
	ID                int64   `json:"id" gorm:"primaryKey"`
	Platform          string  `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_capability,priority:1"`
	RemoteChannelID   int     `json:"remote_channel_id" gorm:"not null;uniqueIndex:idx_external_capability,priority:2"`
	RemoteChannelName string  `json:"remote_channel_name" gorm:"type:varchar(191);not null;default:''"`
	ModelName         string  `json:"model_name" gorm:"type:varchar(191);not null;uniqueIndex:idx_external_capability,priority:3"`
	CostMultiplier    float64 `json:"cost_multiplier" gorm:"type:decimal(20,8);not null;default:0"`
	UpdatedAt         int64   `json:"updated_at" gorm:"not null;default:0"`
}

func (ExternalChannelCapability) TableName() string { return "external_channel_capabilities" }

type ExternalFlowData struct {
	ID              int64  `json:"id" gorm:"primaryKey"`
	Platform        string `json:"platform" gorm:"type:varchar(32);not null;uniqueIndex:idx_external_flow,priority:1"`
	RemoteUserID    int64  `json:"remote_user_id" gorm:"not null;uniqueIndex:idx_external_flow,priority:2"`
	LocalUserID     int    `json:"local_user_id" gorm:"not null;default:0;index"`
	RemoteUsername  string `json:"remote_username" gorm:"type:varchar(128);not null;default:''"`
	RemoteTokenID   int    `json:"remote_token_id" gorm:"not null;uniqueIndex:idx_external_flow,priority:3"`
	RemoteChannelID int    `json:"remote_channel_id" gorm:"not null;uniqueIndex:idx_external_flow,priority:4"`
	ModelName       string `json:"model_name" gorm:"type:varchar(191);not null;uniqueIndex:idx_external_flow,priority:5"`
	UseGroup        string `json:"use_group" gorm:"type:varchar(128);not null;default:'';uniqueIndex:idx_external_flow,priority:6"`
	WindowStart     int64  `json:"window_start" gorm:"not null;uniqueIndex:idx_external_flow,priority:7"`
	WindowEnd       int64  `json:"window_end" gorm:"not null;uniqueIndex:idx_external_flow,priority:8"`
	TokenUsed       int64  `json:"token_used" gorm:"not null;default:0"`
	Count           int64  `json:"count" gorm:"not null;default:0"`
	Quota           int64  `json:"quota" gorm:"not null;default:0"`
}

func (ExternalFlowData) TableName() string { return "external_flow_data" }

func ReplaceExternalPeakTPM(platform string, start, end int64, rows []ExternalPeakTPM) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform = ? AND hour >= ? AND hour <= ?", platform, start, end).Delete(&ExternalPeakTPM{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].Platform = platform
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}

func GetExternalPeakTPM(start, end int64, userID int, platform string) ([]PeakTPMPoint, error) {
	query := DB.Model(&ExternalPeakTPM{}).Select("hour, MAX(peak_tpm) AS peak_tpm").Where("hour >= ? AND hour <= ?", start, end)
	if platform != "" && platform != "all" {
		query = query.Where("platform = ?", platform)
	}
	rows := make([]PeakTPMPoint, 0)
	return rows, query.Group("hour").Order("hour ASC").Scan(&rows).Error
}

func GetExternalUsageSyncState(platform string) (*ExternalUsageSyncState, error) {
	state := &ExternalUsageSyncState{}
	err := DB.Where("platform = ?", strings.TrimSpace(platform)).First(state).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			state.Platform = strings.TrimSpace(platform)
			return state, nil
		}
		return nil, err
	}
	return state, nil
}

func FindLocalUserIDByExternalUsername(username string) (int, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if index := strings.IndexByte(username, '@'); index >= 0 {
		username = username[:index]
	}
	if username == "" {
		return 0, nil
	}
	var id int
	err := DB.Unscoped().Table("users").
		Where("LOWER(username) = ? OR LOWER(email) = ? OR LOWER(email) LIKE ?", username, username, username+"@%").
		Order("id ASC").Limit(1).Pluck("id", &id).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	return id, err
}

func ReplaceExternalQuotaData(platform string, start, end int64, rows []ExternalQuotaData) error {
	platform = strings.TrimSpace(platform)
	if platform == "" || end < start {
		return fmt.Errorf("invalid external usage range")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform = ? AND created_at >= ? AND created_at <= ?", platform, start, end).
			Delete(&ExternalQuotaData{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].Platform = platform
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}

func GetExternalQuotaDates(start, end int64, userID int, platform string) ([]*QuotaData, error) {
	query := DB.Model(&ExternalQuotaData{}).
		Where("created_at >= ? AND created_at <= ?", start, end)
	if userID > 0 {
		query = query.Where("local_user_id = ?", userID)
	}
	if platform != "" && platform != "all" {
		query = query.Where("platform = ?", platform)
	}
	rows := make([]ExternalQuotaData, 0)
	if err := query.Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*QuotaData, 0, len(rows))
	for _, row := range rows {
		result = append(result, &QuotaData{
			UserID: row.LocalUserID, Username: row.RemoteUsername, ModelName: row.ModelName,
			CreatedAt: row.CreatedAt, TokenID: row.RemoteTokenID, ChannelID: row.RemoteChannelID,
			NodeName: row.NodeName, TokenUsed: int(row.TokenUsed), Count: int(row.Count), Quota: int(row.Quota),
			InputTokens: int(row.InputTokens), CompletionTokens: int(row.CompletionTokens),
			CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: int(row.CacheWriteTokens),
			CacheCreationTokens: row.CacheCreationTokens, TrackedCount: int(row.TrackedCount),
			UseTimeTotal: row.UseTimeTotal, UseTimeCount: row.UseTimeCount,
			StreamErrorCount: int(row.StreamErrorCount), FailedCount: int(row.FailedCount), LastCalledAt: row.LastCalledAt,
		})
	}
	return result, nil
}

func ReplaceExternalFlowData(platform string, start, end int64, rows []ExternalFlowData) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform = ? AND window_start = ? AND window_end = ?", platform, start, end).Delete(&ExternalFlowData{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].Platform = platform
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}

func GetExternalFlowData(start, end int64, userID int, platform string) ([]*FlowQuotaData, error) {
	query := DB.Model(&ExternalFlowData{}).Where("platform = ? AND window_end >= ? AND window_start <= ?", platform, start, end)
	if userID > 0 {
		query = query.Where("local_user_id = ?", userID)
	}
	rows := make([]ExternalFlowData, 0)
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	type flowKey struct {
		user, token, channel int
		model, group         string
	}
	grouped := make(map[flowKey]*FlowQuotaData)
	for _, row := range rows {
		key := flowKey{row.LocalUserID, row.RemoteTokenID, row.RemoteChannelID, row.ModelName, row.UseGroup}
		item := grouped[key]
		if item == nil {
			item = &FlowQuotaData{UserID: row.LocalUserID, Username: row.RemoteUsername, TokenID: row.RemoteTokenID, ChannelID: row.RemoteChannelID, ModelName: row.ModelName, UseGroup: row.UseGroup}
			grouped[key] = item
		}
		item.TokenUsed += int(row.TokenUsed)
		item.Count += int(row.Count)
		item.Quota += int(row.Quota)
	}
	result := make([]*FlowQuotaData, 0, len(grouped))
	for _, item := range grouped {
		result = append(result, item)
	}
	return result, nil
}
