package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const channelFailureKeyPrefix = "channel_model_disable_failure:v2"

type channelModelFailureKey struct {
	channelID int
	modelName string
}

type channelFailureState struct {
	failureTimes     []time.Time
	consecutiveCount int
}

var channelFailureStates = struct {
	sync.Mutex
	values map[channelModelFailureKey]channelFailureState
}{values: make(map[channelModelFailureKey]channelFailureState)}

// IsChannelModelFailureMatch reports whether an upstream status code belongs to
// the per-channel, per-model circuit-breaker rule.
func IsChannelModelFailureMatch(err *types.NewAPIError) bool {
	// 上游拒答是内容问题，不算渠道×模型故障。
	if err == nil || err.GetErrorCode() == types.ErrorCodeUpstreamRefusal {
		return false
	}
	setting := operation_setting.GetMonitorSetting()
	if !setting.ChannelModelCircuitBreakerEnabled ||
		(setting.ChannelErrorThreshold <= 0 && setting.ChannelErrorConsecutiveThreshold <= 0) {
		return false
	}

	statusCodes, parseErr := operation_setting.ParseHTTPStatusCodeRanges(setting.ChannelErrorStatusCodes)
	if parseErr != nil {
		statusCodes = nil
	}
	if len(statusCodes) == 0 {
		return false
	}
	// Compare against the upstream status code: a channel status-code mapping
	// may already have rewritten StatusCode, and the rule describes upstream
	// behavior rather than what the client eventually sees.
	return operation_setting.MatchStatusCodeRanges(statusCodes, err.UpstreamStatusCode())
}

// RecordChannelModelFailure records one matching error for a channel-model pair
// and returns true when either configured threshold has been reached.
func RecordChannelModelFailure(channelID int, modelName string) bool {
	setting := operation_setting.GetMonitorSetting()
	if channelID <= 0 || modelName == "" || !setting.ChannelModelCircuitBreakerEnabled ||
		operation_setting.IsChannelModelCircuitBreakerExcluded(channelID) ||
		(setting.ChannelErrorThreshold <= 0 && setting.ChannelErrorConsecutiveThreshold <= 0) {
		return false
	}
	window := time.Duration(setting.ChannelErrorWindowMinutes) * time.Minute
	if window <= 0 {
		window = time.Duration(operation_setting.DefaultChannelErrorWindowMinutes) * time.Minute
	}

	if common.RedisEnabled && common.RDB != nil {
		reached, err := recordRedisChannelModelFailure(channelID, modelName, window, setting.ChannelErrorThreshold, setting.ChannelErrorConsecutiveThreshold)
		if err == nil {
			return reached
		}
		common.SysLog(fmt.Sprintf("failed to record channel model failure in Redis: channel_id=%d, model=%s, error=%v", channelID, modelName, err))
	}

	return recordMemoryChannelModelFailure(channelID, modelName, window, setting.ChannelErrorThreshold, setting.ChannelErrorConsecutiveThreshold)
}

// ResetChannelModelFailureConsecutive breaks the consecutive-failure sequence
// while retaining the rolling-window count.
func ResetChannelModelFailureConsecutive(channelID int, modelName string) {
	if channelID <= 0 || modelName == "" {
		return
	}
	if common.RedisEnabled && common.RDB != nil {
		if err := common.RedisDel(redisChannelModelFailureKey(channelID, modelName, "consecutive")); err != nil {
			common.SysLog(fmt.Sprintf("failed to reset channel model failure sequence in Redis: channel_id=%d, model=%s, error=%v", channelID, modelName, err))
		}
	}

	key := channelModelFailureKey{channelID: channelID, modelName: modelName}
	channelFailureStates.Lock()
	if state, ok := channelFailureStates.values[key]; ok {
		state.consecutiveCount = 0
		channelFailureStates.values[key] = state
	}
	channelFailureStates.Unlock()
}

func ClearChannelModelFailureCountersByChannelIDs(channelIDs []int) error {
	if len(channelIDs) == 0 {
		return nil
	}
	excluded := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		excluded[channelID] = struct{}{}
	}
	channelFailureStates.Lock()
	for key := range channelFailureStates.values {
		if _, ok := excluded[key.channelID]; ok {
			delete(channelFailureStates.values, key)
		}
	}
	channelFailureStates.Unlock()

	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ctx := context.Background()
	for _, channelID := range channelIDs {
		cursor := uint64(0)
		pattern := fmt.Sprintf("%s:%d:*", channelFailureKeyPrefix, channelID)
		for {
			keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := common.RDB.Del(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = nextCursor
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}

// ClearChannelModelFailureCounters clears both counters after the channel-model
// pair has been disabled or recovered.
func ClearChannelModelFailureCounters(channelID int, modelName string) {
	if channelID <= 0 || modelName == "" {
		return
	}
	key := channelModelFailureKey{channelID: channelID, modelName: modelName}
	channelFailureStates.Lock()
	delete(channelFailureStates.values, key)
	channelFailureStates.Unlock()

	if common.RedisEnabled && common.RDB != nil {
		for _, kind := range []string{"window", "consecutive"} {
			if err := common.RedisDel(redisChannelModelFailureKey(channelID, modelName, kind)); err != nil {
				common.SysLog(fmt.Sprintf("failed to clear channel model failure counter in Redis: channel_id=%d, model=%s, kind=%s, error=%v", channelID, modelName, kind, err))
			}
		}
	}
}

func ClearAllChannelModelFailureCounters() error {
	channelFailureStates.Lock()
	channelFailureStates.values = make(map[channelModelFailureKey]channelFailureState)
	channelFailureStates.Unlock()

	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ctx := context.Background()
	cursor := uint64(0)
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, channelFailureKeyPrefix+":*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := common.RDB.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			return nil
		}
	}
}

func redisChannelModelFailureKey(channelID int, modelName string, kind string) string {
	return fmt.Sprintf("%s:%d:%s:%s", channelFailureKeyPrefix, channelID, common.Sha1([]byte(modelName)), kind)
}

func recordRedisChannelModelFailure(channelID int, modelName string, window time.Duration, windowThreshold int, consecutiveThreshold int) (bool, error) {
	now := time.Now()
	windowMilliseconds := window.Milliseconds()
	windowCount := int64(0)
	if windowThreshold > 0 {
		count, err := common.RDB.Eval(
			context.Background(),
			`redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
local count = redis.call('ZCARD', KEYS[1])
local limit = tonumber(ARGV[5])
if count > limit then
  redis.call('ZREMRANGEBYRANK', KEYS[1], 0, count - limit - 1)
  count = limit
end
return count`,
			[]string{redisChannelModelFailureKey(channelID, modelName, "window")},
			now.Add(-window).UnixMilli(),
			now.UnixMilli(),
			fmt.Sprintf("%d-%s", now.UnixNano(), common.GetRandomString(8)),
			windowMilliseconds,
			windowThreshold,
		).Int64()
		if err != nil {
			return false, err
		}
		windowCount = count
	}
	consecutiveCount := int64(0)
	if consecutiveThreshold > 0 {
		count, err := common.RDB.Eval(
			context.Background(),
			`local count = tonumber(redis.call('GET', KEYS[1]) or '0')
if tonumber(ARGV[2]) <= 0 or count < tonumber(ARGV[2]) then
  count = redis.call('INCR', KEYS[1])
end
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return count`,
			[]string{redisChannelModelFailureKey(channelID, modelName, "consecutive")},
			windowMilliseconds,
			consecutiveThreshold,
		).Int64()
		if err != nil {
			return false, err
		}
		consecutiveCount = count
	}
	return (windowThreshold > 0 && windowCount >= int64(windowThreshold)) ||
		(consecutiveThreshold > 0 && consecutiveCount >= int64(consecutiveThreshold)), nil
}

func recordMemoryChannelModelFailure(channelID int, modelName string, window time.Duration, windowThreshold int, consecutiveThreshold int) bool {
	now := time.Now()
	channelFailureStates.Lock()
	defer channelFailureStates.Unlock()

	key := channelModelFailureKey{channelID: channelID, modelName: modelName}
	state := channelFailureStates.values[key]
	cutoff := now.Add(-window)
	firstRetained := 0
	for firstRetained < len(state.failureTimes) && !state.failureTimes[firstRetained].After(cutoff) {
		firstRetained++
	}
	if firstRetained > 0 {
		state.failureTimes = append([]time.Time(nil), state.failureTimes[firstRetained:]...)
		if len(state.failureTimes) == 0 {
			state.consecutiveCount = 0
		}
	}
	state.failureTimes = append(state.failureTimes, now)
	if windowThreshold > 0 && len(state.failureTimes) > windowThreshold {
		state.failureTimes = state.failureTimes[len(state.failureTimes)-windowThreshold:]
	}
	if consecutiveThreshold <= 0 || state.consecutiveCount < consecutiveThreshold {
		state.consecutiveCount++
	}
	reached := (windowThreshold > 0 && len(state.failureTimes) >= windowThreshold) ||
		(consecutiveThreshold > 0 && state.consecutiveCount >= consecutiveThreshold)
	channelFailureStates.values[key] = state
	return reached
}
