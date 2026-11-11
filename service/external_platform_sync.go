package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const externalPlatformsConfigEnv = "EXTERNAL_PLATFORMS_CONFIG"
const externalPlatformsConsulAddressEnv = "EXTERNAL_PLATFORMS_CONSUL_HTTP_ADDR"
const externalPlatformsConsulTokenEnv = "EXTERNAL_PLATFORMS_CONSUL_HTTP_TOKEN"
const externalPlatformsConsulPathEnv = "EXTERNAL_PLATFORMS_CONSUL_KV_PATH"

type ExternalPlatformConfig struct {
	Name                  string         `json:"name"`
	Value                 string         `json:"value"`
	Label                 string         `json:"label"`
	BaseURL               string         `json:"base_url"`
	AdminToken            string         `json:"admin_token"`
	AdminUserID           int            `json:"admin_user_id"`
	Enabled               *bool          `json:"enabled"`
	TimeoutSeconds        int            `json:"timeout_seconds"`
	TimeoutSecs           int            `json:"timeout_secs"`
	PageSize              int            `json:"page_size"`
	SyncConcurrency       int            `json:"sync_concurrency"`
	SkipFailedLogs        bool           `json:"skip_failed_logs"`
	AllowedUsers          []string       `json:"allowed_users"`
	InitialStartTimestamp int64          `json:"initial_start_timestamp"`
	UserMap               map[string]int `json:"user_map"`
}

type externalQuotaWire struct {
	UserID              int64  `json:"user_id"`
	Username            string `json:"username"`
	ModelName           string `json:"model_name"`
	CreatedAt           int64  `json:"created_at"`
	TokenID             int    `json:"token_id"`
	ChannelID           int    `json:"channel_id"`
	NodeName            string `json:"node_name"`
	TokenUsed           int64  `json:"token_used"`
	Count               int64  `json:"count"`
	Quota               int64  `json:"quota"`
	InputTokens         int64  `json:"input_tokens"`
	CompletionTokens    int64  `json:"completion_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheWriteTokens    int64  `json:"cache_write_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	TrackedCount        int64  `json:"tracked_count"`
	UseTimeTotal        int64  `json:"use_time_total"`
	UseTimeCount        int64  `json:"use_time_count"`
	StreamErrorCount    int64  `json:"stream_error_count"`
	FailedCount         int64  `json:"failed_count"`
	LastCalledAt        int64  `json:"last_called_at"`
}

type externalQuotaResponse struct {
	Success bool                `json:"success"`
	Message string              `json:"message"`
	Data    []externalQuotaWire `json:"data"`
}

type externalPeakPoint struct {
	Hour    int64 `json:"hour"`
	PeakTPM int64 `json:"peak_tpm"`
}

type externalFlowWire struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	TokenID   int    `json:"token_id"`
	ChannelID int    `json:"channel_id"`
	ModelName string `json:"model_name"`
	UseGroup  string `json:"use_group"`
	TokenUsed int64  `json:"token_used"`
	Count     int64  `json:"count"`
	Quota     int64  `json:"quota"`
}

type externalCapabilityWire struct {
	ChannelID      int     `json:"id"`
	ChannelName    string  `json:"name"`
	CostMultiplier float64 `json:"cost_multiplier"`
}

type externalCapabilityTargetWire struct {
	ModelName string                   `json:"model_name"`
	Channels  []externalCapabilityWire `json:"channels"`
}

func LoadExternalPlatformConfigs() ([]ExternalPlatformConfig, error) {
	raw := strings.TrimSpace(os.Getenv(externalPlatformsConfigEnv))
	if raw == "" {
		consulRaw, err := loadExternalPlatformsFromConsul()
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(consulRaw))
	}
	if raw == "" {
		return nil, nil
	}
	var configs []ExternalPlatformConfig
	if strings.HasPrefix(raw, "[") {
		if err := common.UnmarshalJsonStr(raw, &configs); err != nil {
			return nil, fmt.Errorf("decode %s: %w", externalPlatformsConfigEnv, err)
		}
	} else {
		var wrapper struct {
			Platforms []ExternalPlatformConfig `json:"platforms"`
		}
		if err := common.UnmarshalJsonStr(raw, &wrapper); err != nil {
			return nil, fmt.Errorf("decode %s: %w", externalPlatformsConfigEnv, err)
		}
		configs = wrapper.Platforms
	}
	result := make([]ExternalPlatformConfig, 0, len(configs))
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		if config.Name == "" {
			config.Name = config.Value
		}
		config.Name = strings.ToLower(strings.TrimSpace(config.Name))
		config.Label = strings.TrimSpace(config.Label)
		config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
		config.AdminToken = strings.TrimSpace(config.AdminToken)
		if config.Enabled != nil && !*config.Enabled {
			continue
		}
		if config.Name == "" || config.BaseURL == "" || config.AdminToken == "" {
			return nil, fmt.Errorf("external platform requires name, base_url and admin_token")
		}
		if _, ok := seen[config.Name]; ok {
			return nil, fmt.Errorf("duplicate external platform: %s", config.Name)
		}
		seen[config.Name] = struct{}{}
		if config.TimeoutSeconds <= 0 {
			config.TimeoutSeconds = config.TimeoutSecs
		}
		if config.TimeoutSeconds <= 0 {
			config.TimeoutSeconds = 180
		}
		if config.AdminUserID <= 0 {
			config.AdminUserID = 1
		}
		result = append(result, config)
	}
	return result, nil
}

func loadExternalPlatformsFromConsul() ([]byte, error) {
	path := strings.TrimSpace(os.Getenv(externalPlatformsConsulPathEnv))
	if path == "" {
		return nil, nil
	}
	address := strings.TrimSpace(os.Getenv(externalPlatformsConsulAddressEnv))
	if address == "" {
		address = strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR"))
	}
	token := strings.TrimSpace(os.Getenv(externalPlatformsConsulTokenEnv))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CONSUL_HTTP_TOKEN"))
	}
	if address == "" || token == "" {
		return nil, fmt.Errorf("external platforms Consul config requires address and token")
	}
	return common.FetchConsulKVValue(context.Background(), address, token, path)
}

// RunExternalPlatformSync refreshes the current hourly window.
func RunExternalPlatformSync(ctx context.Context) (map[string]any, error) {
	configs, err := LoadExternalPlatformConfigs()
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return map[string]any{"enabled": false, "platforms": 0}, nil
	}
	result := map[string]any{"enabled": true, "platforms": len(configs)}
	for _, config := range configs {
		rows, start, end, syncErr := syncExternalPlatform(ctx, config)
		result[config.Name] = map[string]any{"rows": rows, "start": start, "end": end}
		if syncErr != nil {
			return result, fmt.Errorf("sync external platform %s: %w", config.Name, syncErr)
		}
	}
	return result, nil
}

func syncExternalPlatform(ctx context.Context, config ExternalPlatformConfig) (int, int64, int64, error) {
	now := time.Now().Unix()
	end := now - now%3600
	state, err := model.GetExternalUsageSyncState(config.Name)
	if err != nil {
		return 0, 0, 0, err
	}
	start := state.LastSuccessEnd - 3600
	if start <= 0 {
		start = config.InitialStartTimestamp
	}
	if start <= 0 || start > end {
		start = end - 3600
	}
	start -= start % 3600
	if err := updateExternalSyncState(state, start, end, "running", ""); err != nil {
		return 0, start, end, err
	}

	rows, err := fetchExternalQuotaData(ctx, config, start, end)
	if err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return 0, start, end, err
	}
	if err := model.ReplaceExternalQuotaData(config.Name, start, end, rows); err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return 0, start, end, err
	}
	peaks, err := fetchExternalPeakTPM(ctx, config, start, end)
	if err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return len(rows), start, end, err
	}
	if err := model.ReplaceExternalPeakTPM(config.Name, start, end, peaks); err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return len(rows), start, end, err
	}
	if err := syncExternalCapabilities(ctx, config); err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return len(rows), start, end, err
	}
	flow, err := fetchExternalFlow(ctx, config, start, end)
	if err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return len(rows), start, end, err
	}
	if err := model.ReplaceExternalFlowData(config.Name, start, end, flow); err != nil {
		_ = updateExternalSyncState(state, start, end, "failed", err.Error())
		return len(rows), start, end, err
	}
	if err := updateExternalSyncState(state, start, end, "succeeded", ""); err != nil {
		return len(rows), start, end, err
	}
	return len(rows), start, end, nil
}

func fetchExternalFlow(ctx context.Context, config ExternalPlatformConfig, start, end int64) ([]model.ExternalFlowData, error) {
	query := url.Values{"start_timestamp": []string{strconv.FormatInt(start, 10)}, "end_timestamp": []string{strconv.FormatInt(end, 10)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.BaseURL+"/api/data/flow?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.AdminToken)
	request.Header.Set("New-Api-User", strconv.Itoa(config.AdminUserID))
	response, err := (&http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GET /api/data/flow returned %s", response.Status)
	}
	var payload struct {
		Success bool               `json:"success"`
		Message string             `json:"message"`
		Data    []externalFlowWire `json:"data"`
	}
	if err := common.DecodeJson(response.Body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success && payload.Message != "" {
		return nil, fmt.Errorf("remote flow API failed: %s", payload.Message)
	}
	rows := make([]model.ExternalFlowData, 0, len(payload.Data))
	localIDs := make(map[string]int)
	for _, item := range payload.Data {
		remoteUser := normalizeExternalUsername(item.Username)
		if !externalUserAllowed(config.AllowedUsers, remoteUser) {
			continue
		}
		localID, ok := config.UserMap[remoteUser]
		if !ok {
			localID = localIDs[remoteUser]
			if localID == 0 {
				localID, err = model.FindLocalUserIDByExternalUsername(remoteUser)
				if err != nil {
					return nil, err
				}
				localIDs[remoteUser] = localID
			}
		}
		rows = append(rows, model.ExternalFlowData{Platform: config.Name, RemoteUserID: item.UserID, LocalUserID: localID, RemoteUsername: item.Username, RemoteTokenID: item.TokenID, RemoteChannelID: item.ChannelID, ModelName: item.ModelName, UseGroup: item.UseGroup, WindowStart: start, WindowEnd: end, TokenUsed: item.TokenUsed, Count: item.Count, Quota: item.Quota})
	}
	return rows, nil
}

func syncExternalCapabilities(ctx context.Context, config ExternalPlatformConfig) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.BaseURL+"/api/models/capability-targets?page=1&page_size=1000", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+config.AdminToken)
	request.Header.Set("New-Api-User", strconv.Itoa(config.AdminUserID))
	response, err := (&http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GET capability-targets returned %s", response.Status)
	}
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items []externalCapabilityTargetWire `json:"items"`
		} `json:"data"`
	}
	if err := common.DecodeJson(response.Body, &payload); err != nil {
		return err
	}
	if !payload.Success && payload.Message != "" {
		return fmt.Errorf("remote capability API failed: %s", payload.Message)
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("platform = ?", config.Name).Delete(&model.ExternalChannelCapability{}).Error; err != nil {
			return err
		}
		rows := make([]model.ExternalChannelCapability, 0, len(payload.Data.Items))
		for _, item := range payload.Data.Items {
			for _, channel := range item.Channels {
				if channel.ChannelID <= 0 || strings.TrimSpace(item.ModelName) == "" {
					continue
				}
				rows = append(rows, model.ExternalChannelCapability{Platform: config.Name, RemoteChannelID: channel.ChannelID, RemoteChannelName: channel.ChannelName, ModelName: item.ModelName, CostMultiplier: channel.CostMultiplier, UpdatedAt: time.Now().Unix()})
			}
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}

func fetchExternalPeakTPM(ctx context.Context, config ExternalPlatformConfig, start, end int64) ([]model.ExternalPeakTPM, error) {
	query := url.Values{}
	query.Set("start_timestamp", strconv.FormatInt(start, 10))
	query.Set("end_timestamp", strconv.FormatInt(end, 10))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.BaseURL+"/api/data/peak-tpm?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.AdminToken)
	request.Header.Set("New-Api-User", strconv.Itoa(config.AdminUserID))
	client := &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET /api/data/peak-tpm returned %s", response.Status)
	}
	var payload struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Data    []externalPeakPoint `json:"data"`
	}
	if err := common.DecodeJson(response.Body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success && payload.Message != "" {
		return nil, fmt.Errorf("remote peak TPM API failed: %s", payload.Message)
	}
	rows := make([]model.ExternalPeakTPM, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.Hour > 0 {
			rows = append(rows, model.ExternalPeakTPM{Platform: config.Name, Hour: item.Hour, PeakTPM: item.PeakTPM})
		}
	}
	return rows, nil
}

func updateExternalSyncState(state *model.ExternalUsageSyncState, start, end int64, status, lastError string) error {
	state.LastAttemptStart = start
	state.LastAttemptEnd = end
	state.LastStatus = status
	state.LastError = lastError
	state.LastSyncedAt = time.Now().Unix()
	if status == "succeeded" {
		state.LastSuccessEnd = end
	}
	return model.DB.Save(state).Error
}

func fetchExternalQuotaData(ctx context.Context, config ExternalPlatformConfig, start, end int64) ([]model.ExternalQuotaData, error) {
	query := url.Values{}
	query.Set("start_timestamp", strconv.FormatInt(start, 10))
	query.Set("end_timestamp", strconv.FormatInt(end, 10))
	endpoint := config.BaseURL + "/api/data/?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.AdminToken)
	request.Header.Set("New-Api-User", strconv.Itoa(config.AdminUserID))
	client := &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("GET /api/data/ returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var payload externalQuotaResponse
	if err := common.DecodeJson(response.Body, &payload); err != nil {
		return nil, err
	}
	if !payload.Success && payload.Message != "" {
		return nil, fmt.Errorf("remote API failed: %s", payload.Message)
	}
	rows := make([]model.ExternalQuotaData, 0, len(payload.Data))
	localUserIDs := make(map[string]int)
	for _, item := range payload.Data {
		if item.CreatedAt <= 0 {
			continue
		}
		remoteUser := normalizeExternalUsername(item.Username)
		if !externalUserAllowed(config.AllowedUsers, remoteUser) {
			continue
		}
		localUserID, ok := config.UserMap[remoteUser]
		if !ok {
			if cached, found := localUserIDs[remoteUser]; found {
				localUserID = cached
			} else {
				localUserID, err = model.FindLocalUserIDByExternalUsername(remoteUser)
				if err != nil {
					return nil, err
				}
				localUserIDs[remoteUser] = localUserID
			}
		}
		rows = append(rows, model.ExternalQuotaData{
			Platform: config.Name, RemoteUserID: item.UserID, RemoteUsername: item.Username,
			LocalUserID: localUserID, ModelName: item.ModelName, CreatedAt: item.CreatedAt,
			RemoteTokenID: item.TokenID, RemoteChannelID: item.ChannelID, NodeName: item.NodeName,
			TokenUsed: item.TokenUsed, Count: item.Count, Quota: item.Quota,
			InputTokens: item.InputTokens, CompletionTokens: item.CompletionTokens,
			CacheReadTokens: item.CacheReadTokens, CacheWriteTokens: item.CacheWriteTokens,
			CacheCreationTokens: item.CacheCreationTokens, TrackedCount: item.TrackedCount,
			UseTimeTotal: item.UseTimeTotal, UseTimeCount: item.UseTimeCount,
			StreamErrorCount: item.StreamErrorCount, FailedCount: item.FailedCount,
			LastCalledAt: item.LastCalledAt,
		})
	}
	return rows, nil
}

func normalizeExternalUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if index := strings.IndexByte(username, '@'); index >= 0 {
		username = username[:index]
	}
	return username
}

func externalUserAllowed(allowed []string, username string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if normalizeExternalUsername(item) == username {
			return true
		}
	}
	return false
}
