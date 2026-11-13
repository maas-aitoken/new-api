package service

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 这些用例走内存计数（common.RedisEnabled 为 false 时的实现），每个用例用不同的渠道 id，
// 避免进程内的计数池互相串。容量溢价不参与计费，这里只验证它怎么改变比较成本。

func setupCapacityPricing(t *testing.T, pricing autorouter.CapacityPricing) {
	t.Helper()
	resetCapacityPriceCache()
	setting := autorouter_setting.GetAutoRouterSetting()
	previous := setting.CapacityPricing
	setting.CapacityPricing = pricing
	previousRedisEnabled, previousRDB := common.RedisEnabled, common.RDB
	// 用内存计数：样本只来自本进程，测试之间靠不同的渠道 id 隔离。
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() {
		setting.CapacityPricing = previous
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRDB
		resetCapacityPriceCache()
	})
}

func capacityPriceChannel(id int, setting string) *model.Channel {
	channel := &model.Channel{Id: id, Models: "gpt-x"}
	if setting != "" {
		channel.Setting = &setting
	}
	return channel
}

// capacityAttempts 在 <渠道, gpt-x> 池上登记 count 次还在进行的尝试。
func capacityAttempts(channelID, capacityPoolID, count int) {
	for range count {
		channelcapacity.Begin(channelID, "gpt-x", capacityPoolID, 0)
	}
}

func capacityFactorOf(requests ...CapacityCostRequest) []CapacityFactor {
	return CapacityCostFactors(requests, func(*model.Channel, string) float64 { return 1 })
}

func oneShare(channel *model.Channel) CapacityCostRequest {
	return CapacityCostRequest{Shares: []model.ChannelShare{{Channel: channel, Probability: 1}}, Serving: "gpt-x"}
}

func TestCapacityCostFactorWithoutLimit(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	channel := capacityPriceChannel(9101, "")
	capacityAttempts(9101, 0, 9)
	factor := capacityFactorOf(oneShare(channel))[0]
	assert.Equal(t, 1.0, factor.Multiplier)
	// 三项都不限是弹性资源：再忙也没有溢价。
	assert.Zero(t, factor.Premium)
	assert.False(t, factor.Known)
}

func TestCapacityCostFactorStaticPressure(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	channel := capacityPriceChannel(9102, `{"model_limits":{"gpt-x":{"rpm":10}}}`)
	capacityAttempts(9102, 0, 8)
	factor := capacityFactorOf(oneShare(channel))[0]
	require.True(t, factor.Known)
	assert.InDelta(t, 0.8, factor.Pressure, 1e-9)
	assert.InDelta(t, 2.5, factor.Premium, 1e-9)
}

func TestCapacityCostFactorSaturated(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	channel := capacityPriceChannel(9103, `{"model_limits":{"gpt-x":{"rpm":5}}}`)
	capacityAttempts(9103, 0, 6)
	factor := capacityFactorOf(oneShare(channel))[0]
	require.True(t, factor.Known)
	assert.Equal(t, autorouter.DefaultCapacityPricing().SaturatedMultiplier, factor.Premium)
}

func TestCapacityCostFactorDynamicLimit(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	// 静态额度 1000 与动态额度 4 并存时必须用动态额度：配速才反映订阅池的真实余量。
	channel := capacityPriceChannel(9104, `{"model_limits":{"gpt-x":{"rpm":1000}},"capacity_pools":{"gpt-x":7}}`)
	require.NoError(t, channelcapacity.PublishLimit("cp-7", channelcapacity.PublishedLimit{RPM: 4}, time.Minute))
	capacityAttempts(9104, 7, 3)
	factor := capacityFactorOf(oneShare(channel))[0]
	require.True(t, factor.Known)
	assert.InDelta(t, 0.75, factor.Pressure, 1e-9)
	assert.InDelta(t, 2.0, factor.Premium, 1e-9)

	// 动态额度没发布时退回静态保底。
	resetCapacityPriceCache()
	fallback := capacityPriceChannel(9105, `{"model_limits":{"gpt-x":{"rpm":10}},"capacity_pools":{"gpt-x":8}}`)
	capacityAttempts(9105, 8, 8)
	factor = capacityFactorOf(oneShare(fallback))[0]
	assert.InDelta(t, 2.5, factor.Premium, 1e-9)
}

func TestCapacityCostFactorExhaustedPool(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	channel := capacityPriceChannel(9106, `{"capacity_pools":{"gpt-x":9}}`)
	// 三项都是 0 但已耗尽：这是"没有了"，不是"不限"。
	require.NoError(t, channelcapacity.PublishLimit("cp-9", channelcapacity.PublishedLimit{Exhausted: true}, time.Minute))
	factor := capacityFactorOf(oneShare(channel))[0]
	require.True(t, factor.Known)
	assert.Equal(t, autorouter.DefaultCapacityPricing().SaturatedMultiplier, factor.Premium)
}

func TestCapacityCostFactorExpectation(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	busy := capacityPriceChannel(9107, `{"model_limits":{"gpt-x":{"rpm":10}}}`)
	idle := capacityPriceChannel(9108, "")
	capacityAttempts(9107, 0, 9)
	multipliers := map[int]float64{9107: 1, 9108: 3}
	request := CapacityCostRequest{
		Shares: []model.ChannelShare{
			{Channel: busy, Probability: 0.5},
			{Channel: idle, Probability: 0.5},
		},
		Serving: "gpt-x",
	}
	factor := CapacityCostFactors([]CapacityCostRequest{request}, func(channel *model.Channel, _ string) float64 {
		return multipliers[channel.Id]
	})[0]
	// 期望倍率 2，Σ 概率×倍率×溢价 = 0.5×1×5 + 0.5×3×1 = 4，折回溢价是 2。
	// 期望倍率×期望溢价会算成 2×3=6，贵的渠道恰好也闲时两者不相等。
	assert.InDelta(t, 2.0, factor.Multiplier, 1e-9)
	assert.InDelta(t, 2.0, factor.Premium, 1e-9)
	assert.InDelta(t, 0.9, factor.Pressure, 1e-9)
}

func TestCapacityCostFactorDisabled(t *testing.T) {
	pricing := autorouter.DefaultCapacityPricing()
	pricing.Enabled = false
	setupCapacityPricing(t, pricing)
	channel := capacityPriceChannel(9109, `{"model_limits":{"gpt-x":{"rpm":5}}}`)
	capacityAttempts(9109, 0, 6)
	factor := capacityFactorOf(oneShare(channel))[0]
	assert.Equal(t, 1.0, factor.Multiplier)
	assert.Zero(t, factor.Premium)
	assert.False(t, factor.Known)
}

func TestCapacityPriceCacheReusesSample(t *testing.T) {
	setupCapacityPricing(t, autorouter.DefaultCapacityPricing())
	channel := capacityPriceChannel(9110, `{"model_limits":{"gpt-x":{"rpm":10}}}`)
	capacityAttempts(9110, 0, 8)
	first := capacityFactorOf(oneShare(channel))[0]
	// 缓存有效期内新产生的用量不刷新样本，避免每个请求都读一遍 Redis。
	capacityAttempts(9110, 0, 2)
	second := capacityFactorOf(oneShare(channel))[0]
	assert.Equal(t, first.Premium, second.Premium)
	resetCapacityPriceCache()
	third := capacityFactorOf(oneShare(channel))[0]
	assert.Equal(t, autorouter.DefaultCapacityPricing().SaturatedMultiplier, third.Premium)
}

func TestSmoothCapacityPressure(t *testing.T) {
	pricing := autorouter.DefaultCapacityPricing()
	now := time.Now()
	previous := capacitySample{pressure: 0.9, known: true, updatedAt: now.Add(-30 * time.Second)}
	// 30 秒时间常数下，过了 30 秒只走完约 63% 的差距。
	smoothed := smoothCapacityPressure(previous, true, 0.5, true, false, pricing, now)
	assert.InDelta(t, 0.9+(0.5-0.9)*(1-math.Exp(-1)), smoothed, 1e-9)
	// 首次采样、饱和、离开饱和、关闭平滑都直接按原值。
	assert.Equal(t, 0.5, smoothCapacityPressure(capacitySample{}, false, 0.5, true, false, pricing, now))
	assert.Equal(t, 1.2, smoothCapacityPressure(previous, true, 1.2, true, true, pricing, now))
	assert.Equal(t, 0.5, smoothCapacityPressure(capacitySample{pressure: 1, known: true, saturated: true, updatedAt: now}, true, 0.5, true, false, pricing, now))
	flat := pricing
	flat.SmoothingSeconds = 0
	assert.Equal(t, 0.5, smoothCapacityPressure(previous, true, 0.5, true, false, flat, now))
	// 未知不是 0：没配额度的池子不参与平滑。
	assert.Equal(t, -1.0, smoothCapacityPressure(previous, true, -1, false, false, pricing, now))
}
