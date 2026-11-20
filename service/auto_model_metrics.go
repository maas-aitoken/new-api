package service

import (
	"context"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/go-redis/redis/v8"
)

const autoModelMetricsTTL = 10 * time.Minute

var autoModelMetricsScript = redis.NewScript(`
local fields = redis.call('HKEYS', KEYS[1])
for _, field in ipairs(fields) do
  local second = string.match(field, '^(%d+):[sf]$') or field
  if (tonumber(second) or 0) <= tonumber(ARGV[1]) then
    redis.call('HDEL', KEYS[1], field)
  end
end
redis.call('PEXPIRE', KEYS[1], ARGV[3])
if ARGV[2] == 'success' then
  redis.call('HINCRBY', KEYS[1], ARGV[4] .. ':s', 1)
else
  redis.call('HINCRBY', KEYS[1], ARGV[4] .. ':f', 1)
end
return 1
`)

type autoModelMetrics struct {
	fallback *autorouter.MemoryMetrics
}

func newAutoModelMetrics() *autoModelMetrics {
	return &autoModelMetrics{fallback: autorouter.NewMemoryMetrics()}
}

func autoModelMetricsKey(model, kind string) string {
	return "autorouter:metrics:{" + model + "}:" + kind
}

func (m *autoModelMetrics) update(model, outcome string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := autoModelMetricsScript.Run(ctx, common.RDB,
		[]string{autoModelMetricsKey(model, "outcomes")},
		now.Unix()-300, outcome, autoModelMetricsTTL.Milliseconds(), now.Unix()).Err()
	if err != nil {
		common.SysError("auto model metrics: " + err.Error())
	}
}

func (m *autoModelMetrics) Finish(model string, success bool) {
	m.fallback.Finish(model, success)
	outcome := "success"
	if !success {
		outcome = "failure"
	}
	m.update(model, outcome)
}

func (m *autoModelMetrics) Snapshot(models []string) map[string]autorouter.MetricSample {
	if !common.RedisEnabled || common.RDB == nil {
		return m.fallback.Snapshot(models)
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	type commands struct {
		outcomes *redis.StringStringMapCmd
	}
	cmds := make(map[string]commands, len(models))
	_, err := common.RDB.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, model := range models {
			cmds[model] = commands{
				outcomes: pipe.HGetAll(ctx, autoModelMetricsKey(model, "outcomes")),
			}
		}
		return nil
	})
	if err != nil {
		common.SysError("auto model metrics: " + err.Error())
		return m.fallback.Snapshot(models)
	}
	cutoff := now.Unix() - 300
	result := make(map[string]autorouter.MetricSample, len(models))
	for model, cmd := range cmds {
		var sample autorouter.MetricSample
		for field, value := range cmd.outcomes.Val() {
			second, outcome, found := parseAutoModelOutcome(field)
			if !found || second <= cutoff {
				continue
			}
			if outcome == "s" {
				sample.Successes += redisCount(value)
			} else {
				sample.Failures += redisCount(value)
			}
		}
		if sample.Successes > 0 || sample.Failures > 0 {
			result[model] = sample
		}
	}
	return result
}

func redisCount(value string) int64 {
	count, _ := strconv.ParseInt(value, 10, 64)
	return count
}

func parseAutoModelOutcome(field string) (int64, string, bool) {
	if len(field) < 3 {
		return 0, "", false
	}
	second, err := strconv.ParseInt(field[:len(field)-2], 10, 64)
	outcome := field[len(field)-1:]
	return second, outcome, err == nil && (outcome == "s" || outcome == "f")
}
