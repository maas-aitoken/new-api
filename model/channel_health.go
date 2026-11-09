package model

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// ChannelHealthBlockCount is the number of blocks rendered in the channel
	// status health bar.
	ChannelHealthBlockCount = 20
	// ChannelHealthBlockSeconds is the time span each block covers, so the bar
	// as a whole covers ChannelHealthBlockCount * ChannelHealthBlockSeconds
	// (200 minutes) of history.
	ChannelHealthBlockSeconds = 600
	// channelHealthMaxIds bounds the page-scoped query so a crafted request
	// cannot turn the health endpoint into a pool-wide aggregation.
	channelHealthMaxIds = 200
)

// ChannelHealthBucket holds the success/failure counts of a single block.
type ChannelHealthBucket struct {
	Success int64 `json:"success"`
	Failed  int64 `json:"failed"`
}

// channelHealthRow is the raw aggregation row returned by the log database.
type channelHealthRow struct {
	ChannelId   int   `gorm:"column:channel_id"`
	Type        int   `gorm:"column:type"`
	BucketIndex int   `gorm:"column:bucket_index"`
	Total       int64 `gorm:"column:total"`
}

// channelHealthBucketExpr returns the SQL expression that maps created_at onto
// a block index. Integer division differs per dialect: MySQL needs DIV,
// ClickHouse needs intDiv, and PostgreSQL/SQLite already truncate when both
// operands are integers.
func channelHealthBucketExpr(startTs int64, blockSeconds int64) (string, error) {
	switch common.LogDatabaseType() {
	case common.DatabaseTypeMySQL:
		return fmt.Sprintf("(created_at - %d) DIV %d AS bucket_index", startTs, blockSeconds), nil
	case common.DatabaseTypeClickHouse:
		return fmt.Sprintf("intDiv(created_at - %d, %d) AS bucket_index", startTs, blockSeconds), nil
	case common.DatabaseTypePostgreSQL, common.DatabaseTypeSQLite:
		return fmt.Sprintf("(created_at - %d) / %d AS bucket_index", startTs, blockSeconds), nil
	default:
		return "", fmt.Errorf("unsupported log database type: %s", common.LogDatabaseType())
	}
}

// GetChannelHealthBuckets aggregates consume (success) and error (failure) log
// rows of the given channels into fixed-width time blocks, oldest block first.
// Channels without any traffic in the window are absent from the result, and a
// block with no traffic stays at zero, which the frontend renders as idle.
func GetChannelHealthBuckets(channelIds []int, startTs int64) (map[int][]ChannelHealthBucket, error) {
	if len(channelIds) == 0 {
		return map[int][]ChannelHealthBucket{}, nil
	}
	if len(channelIds) > channelHealthMaxIds {
		return nil, errors.New("请求的渠道数量过多")
	}

	bucketExpr, err := channelHealthBucketExpr(startTs, ChannelHealthBlockSeconds)
	if err != nil {
		common.SysError("failed to build channel health bucket expression: " + err.Error())
		return nil, errors.New("查询渠道健康数据失败")
	}

	var rows []channelHealthRow
	err = LOG_DB.Table("logs").
		Select("channel_id, type, "+bucketExpr+", count(*) AS total").
		Where("created_at >= ?", startTs).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("channel_id IN ?", channelIds).
		Group("channel_id, type, bucket_index").
		Find(&rows).Error
	if err != nil {
		common.SysError("failed to query channel health buckets: " + err.Error())
		return nil, errors.New("查询渠道健康数据失败")
	}

	result := make(map[int][]ChannelHealthBucket, len(rows))
	for _, row := range rows {
		if row.ChannelId <= 0 || row.Total <= 0 {
			continue
		}
		index := row.BucketIndex
		if index < 0 {
			continue
		}
		if index > ChannelHealthBlockCount-1 {
			// Clock skew between nodes can place a row slightly past the last
			// block; fold it into the most recent one instead of dropping it.
			index = ChannelHealthBlockCount - 1
		}
		buckets, ok := result[row.ChannelId]
		if !ok {
			buckets = make([]ChannelHealthBucket, ChannelHealthBlockCount)
			result[row.ChannelId] = buckets
		}
		switch row.Type {
		case LogTypeConsume:
			buckets[index].Success += row.Total
		case LogTypeError:
			buckets[index].Failed += row.Total
		}
	}
	return result, nil
}

// ModelHealth is one model's channel health bar summed over its channels.
type ModelHealth struct {
	ModelName string                `json:"model_name"`
	Buckets   []ChannelHealthBucket `json:"buckets"`
}

// ModelHealthWindow lists the models with traffic in the window, sorted by name.
type ModelHealthWindow struct {
	Models       []ModelHealth `json:"models"`
	BlockCount   int           `json:"block_count"`
	BlockSeconds int           `json:"block_seconds"`
	StartTs      int64         `json:"start_ts"`
}

// The query covers every model, so all callers share one result.
const modelHealthCacheTTL = 30 * time.Second

var (
	modelHealthCacheMu      sync.Mutex
	modelHealthCache        ModelHealthWindow
	modelHealthCacheExpires time.Time
)

// GetRecentModelHealth covers the window ending at the current minute.
// nil allowedModels keeps every model; an empty set keeps none.
func GetRecentModelHealth(allowedModels map[string]struct{}) (ModelHealthWindow, error) {
	now := time.Now()
	endTs := now.Unix() / 60 * 60
	startTs := endTs - int64(ChannelHealthBlockCount)*int64(ChannelHealthBlockSeconds)

	modelHealthCacheMu.Lock()
	window, expires := modelHealthCache, modelHealthCacheExpires
	modelHealthCacheMu.Unlock()
	if !now.Before(expires) || window.StartTs != startTs {
		models, err := queryModelHealth(startTs, endTs)
		if err != nil {
			return ModelHealthWindow{}, err
		}
		window = ModelHealthWindow{
			Models:       models,
			BlockCount:   ChannelHealthBlockCount,
			BlockSeconds: ChannelHealthBlockSeconds,
			StartTs:      startTs,
		}
		modelHealthCacheMu.Lock()
		modelHealthCache, modelHealthCacheExpires = window, now.Add(modelHealthCacheTTL)
		modelHealthCacheMu.Unlock()
	}

	// Do not let caller filters mutate the shared cache.
	filtered := make([]ModelHealth, 0, len(window.Models))
	for _, item := range window.Models {
		if allowedModels != nil {
			if _, allowed := allowedModels[item.ModelName]; !allowed {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	window.Models = filtered
	return window, nil
}

// queryModelHealth counts like the channel bar (consume = success, error = failure,
// per attempt) and sums per model; rows without a channel or model are skipped.
func queryModelHealth(startTs, endTs int64) ([]ModelHealth, error) {
	bucketExpr, err := channelHealthBucketExpr(startTs, ChannelHealthBlockSeconds)
	if err != nil {
		return nil, err
	}

	var rows []struct {
		ModelName   string
		Type        int
		BucketIndex int
		Total       int64
	}
	err = LOG_DB.Table("logs").
		Select("model_name, type, "+bucketExpr+", count(*) AS total").
		Where("created_at >= ? AND created_at < ?", startTs, endTs).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Where("channel_id > 0").
		Where("model_name <> ''").
		Group("model_name, type, bucket_index").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	byModel := make(map[string][]ChannelHealthBucket)
	for _, row := range rows {
		if row.BucketIndex < 0 || row.BucketIndex >= ChannelHealthBlockCount || row.Total <= 0 {
			continue
		}
		buckets, ok := byModel[row.ModelName]
		if !ok {
			buckets = make([]ChannelHealthBucket, ChannelHealthBlockCount)
			byModel[row.ModelName] = buckets
		}
		switch row.Type {
		case LogTypeConsume:
			buckets[row.BucketIndex].Success += row.Total
		case LogTypeError:
			buckets[row.BucketIndex].Failed += row.Total
		}
	}

	models := make([]ModelHealth, 0, len(byModel))
	for name, buckets := range byModel {
		models = append(models, ModelHealth{ModelName: name, Buckets: buckets})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ModelName < models[j].ModelName
	})
	return models, nil
}
