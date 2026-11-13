package service

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
)

// 这里把 auto 的比价和渠道容量计数连起来：一个候选模型会落到的计数池越忙，
// 它的比较成本就越贵，auto 因此把边际请求让给还有余量的模型。
//
// 压力 q = max(有上限维度的 用量 / 额度)。额度优先用容量池发布的动态额度——
// 配速已经把"月付订阅还剩多少预算、离重置还有多久"折算成每分钟速率，所以订阅池
// 与签死 RPM/TPM 的合同池用同一条曲线。动态额度没发布时退回渠道自己的静态额度。
// 三项都不限的池子是弹性资源：忙了多花的是钱不是容量，溢价恒为 1。
//
// 只读计数与额度，不写回任何东西，也不参与计费。

// CapacityFactor 是一个候选模型的容量成本因子，按首次尝试命中渠道的概率加权。
type CapacityFactor struct {
	// Multiplier 是命中概率加权的费用倍率，与容量无关。
	Multiplier float64
	// Premium 是乘在倍率上的容量溢价，使 比较成本 = 用户价 × Multiplier × Premium。
	// 0 表示没有可用的容量信号（没配额度、计数不可用或功能关闭），调用方按 1 处理。
	Premium float64
	// Pressure 是参与加权的池子里最大的压力，只用于留痕；Known 为 false 时没有意义。
	Pressure float64
	Known    bool
}

// capacityPair 是"这个渠道服务这个模型"对应的计数池与额度来源。
type capacityPair struct {
	poolID      string
	cpPoolID    string
	staticLimit dto.ModelLimit
}

// CapacityQuote 是一个计数池当前的压力与溢价。
type CapacityQuote struct {
	// Pressure 是平滑后的压力，-1 表示未知（没配额度）。
	Pressure float64
	Premium  float64
	Known    bool
	// Saturated 表示压力到顶或容量池已耗尽，溢价走饱和档。
	Saturated bool
	// Dynamic 表示压力按容量池动态额度算，false 表示按渠道静态额度。
	Dynamic bool
}

type capacitySample struct {
	pressure  float64
	known     bool
	saturated bool
	dynamic   bool
	updatedAt time.Time
	expiresAt time.Time
}

var (
	capacityPriceNow     = time.Now
	capacityPriceCacheMu sync.Mutex
	capacityPriceCache   = map[string]capacitySample{}
)

// CapacityCostRequest 是一个候选模型的成本估算输入：首次尝试可能落到的渠道及概率
// （调度器预览、会话亲和或原生估算），以及真正服务请求的模型名。
type CapacityCostRequest struct {
	Shares  []model.ChannelShare
	Serving string
}

// CapacityCostFactors 批量给出候选模型的费用倍率与容量溢价，一次调用只读一轮计数。
// multiplierFor 是渠道费用倍率的查法。溢价按 Σ 概率×倍率×溢价 合成后再折算回倍率之外，
// 不能写成期望倍率乘期望溢价：贵的渠道恰好也忙时两者不相等。
func CapacityCostFactors(requests []CapacityCostRequest, multiplierFor func(*model.Channel, string) float64) []CapacityFactor {
	pricing := autorouter_setting.GetAutoRouterSetting().CapacityPricing.Sanitized()
	pairs := make(map[string]capacityPair)
	poolIDs := make([][]string, len(requests))
	multipliers := make([][]float64, len(requests))
	for i, request := range requests {
		poolIDs[i] = make([]string, len(request.Shares))
		multipliers[i] = make([]float64, len(request.Shares))
		for j, share := range request.Shares {
			if share.Channel == nil {
				continue
			}
			pair := capacityPairOf(share.Channel, request.Serving)
			pairs[pair.poolID] = pair
			poolIDs[i][j] = pair.poolID
			multipliers[i][j] = multiplierFor(share.Channel, request.Serving)
		}
	}
	quotes := map[string]CapacityQuote{}
	if pricing.Enabled {
		quotes = capacityQuotes(pairs, pricing)
	}
	factors := make([]CapacityFactor, len(requests))
	for i, request := range requests {
		factor := CapacityFactor{}
		combined := 0.0
		for j, share := range request.Shares {
			if share.Channel == nil {
				continue
			}
			multiplier := multipliers[i][j]
			factor.Multiplier += share.Probability * multiplier
			premium := quotes[poolIDs[i][j]].Premium
			if premium <= 0 {
				premium = 1
			}
			combined += share.Probability * multiplier * premium
			if quote := quotes[poolIDs[i][j]]; quote.Known {
				factor.Known = true
				factor.Pressure = max(factor.Pressure, quote.Pressure)
			}
		}
		if factor.Multiplier > 0 && factor.Known {
			factor.Premium = combined / factor.Multiplier
		}
		factors[i] = factor
	}
	return factors
}

// capacityPairOf 找到这次计数落在哪个池：与 relay 登记用量时一样，先按选渠道时定下的
// 计数名走 model_mapping 链尾，额度与容量池绑定都按上游名查。
func capacityPairOf(channel *model.Channel, serving string) capacityPair {
	settingRaw := ""
	if channel.Setting != nil {
		settingRaw = *channel.Setting
	}
	mapping := channel.GetModelMapping()
	public := model.ChannelCapacityModel(channel.GetModels(), mapping, serving)
	capacity := model.ChannelModelCapacityOf(settingRaw, mapping, public)
	pair := capacityPair{
		poolID:      channelcapacity.PoolID(channel.Id, capacity.UpstreamModel),
		staticLimit: capacity.Limit,
	}
	if capacity.CapacityPoolId != 0 {
		pair.cpPoolID = channelcapacity.CapacityPoolID(capacity.CapacityPoolId)
	}
	return pair
}

// capacityQuotes 批量读出这些池的压力，带节点本地缓存：候选带里每个模型都要问一次，
// 没有缓存时每个 auto 请求都要为几十个池各读三个 Redis 键。
func capacityQuotes(pairs map[string]capacityPair, pricing autorouter.CapacityPricing) map[string]CapacityQuote {
	now := capacityPriceNow()
	capacityPriceCacheMu.Lock()
	stale := make([]capacityPair, 0, len(pairs))
	for _, pair := range pairs {
		sample, ok := capacityPriceCache[pair.poolID]
		if !ok || now.After(sample.expiresAt) {
			stale = append(stale, pair)
		}
	}
	capacityPriceCacheMu.Unlock()
	// Redis 读取不持有缓存锁：刷新期间其它请求照常读旧样本。
	if len(stale) > 0 {
		refreshCapacitySamples(stale, pricing, now)
	}
	capacityPriceCacheMu.Lock()
	defer capacityPriceCacheMu.Unlock()
	quotes := make(map[string]CapacityQuote, len(pairs))
	for _, pair := range pairs {
		sample := capacityPriceCache[pair.poolID]
		pressure := -1.0
		if sample.known {
			pressure = sample.pressure
		}
		quotes[pair.poolID] = CapacityQuote{
			Pressure:  pressure,
			Premium:   pricing.Premium(pressure),
			Known:     sample.known,
			Saturated: sample.saturated,
			Dynamic:   sample.dynamic,
		}
	}
	return quotes
}

// refreshCapacitySamples 用一次 Snapshot 加一次 ReadLimits 刷新所有过期池。
// 读取失败时保留旧样本，没有旧样本就按未知处理，绝不让容量信号把请求拖失败。
func refreshCapacitySamples(stale []capacityPair, pricing autorouter.CapacityPricing, now time.Time) {
	poolIDs := make([]string, 0, len(stale)*2)
	cpIDs := make([]string, 0, len(stale))
	for _, pair := range stale {
		poolIDs = append(poolIDs, pair.poolID)
		if pair.cpPoolID != "" {
			poolIDs = append(poolIDs, pair.cpPoolID)
			cpIDs = append(cpIDs, pair.cpPoolID)
		}
	}
	usages, usageErr := channelcapacity.Snapshot(poolIDs)
	if usageErr != nil {
		common.SysLog(fmt.Sprintf("auto capacity pricing: read pool usage: %v", usageErr))
	}
	limits := map[string]channelcapacity.PublishedLimit{}
	if len(cpIDs) > 0 {
		read, err := channelcapacity.ReadLimits(cpIDs)
		if err != nil {
			common.SysLog(fmt.Sprintf("auto capacity pricing: read pool limits: %v", err))
		} else {
			limits = read
		}
	}
	capacityPriceCacheMu.Lock()
	defer capacityPriceCacheMu.Unlock()
	for _, pair := range stale {
		previous, hadPrevious := capacityPriceCache[pair.poolID]
		if usageErr != nil {
			if hadPrevious {
				// 读失败时沿用旧样本，但让它尽快重试。
				previous.expiresAt = now.Add(time.Second)
				capacityPriceCache[pair.poolID] = previous
			} else {
				// 没有旧样本时按未知处理：读不出来不等于池子是空的。
				capacityPriceCache[pair.poolID] = capacitySample{pressure: -1, updatedAt: now, expiresAt: now.Add(time.Second)}
			}
			continue
		}
		sample := capacitySample{pressure: -1, updatedAt: now}
		raw, known, saturated, dynamic := -1.0, false, false, false
		if pair.cpPoolID != "" {
			if limit, published := limits[pair.cpPoolID]; published {
				dynamic = true
				if limit.Exhausted {
					// 没有可用账号是"没有了"，不是"不限"，必须按饱和处理。
					known, saturated, raw = true, true, 1
				} else {
					raw, known = capacityPressure(usages[pair.cpPoolID], limit.RPM, limit.TPM, limit.MaxConcurrency)
				}
			}
		}
		if !dynamic {
			raw, known = capacityPressure(usages[pair.poolID], int64(pair.staticLimit.RPM), int64(pair.staticLimit.TPM), int64(pair.staticLimit.MaxConcurrency))
		}
		sample.known, sample.saturated, sample.dynamic = known, saturated, dynamic
		sample.pressure = smoothCapacityPressure(previous, hadPrevious, raw, known, saturated, pricing, now)
		sample.expiresAt = now.Add(time.Duration(max(pricing.CacheSeconds, 1)) * time.Second)
		capacityPriceCache[pair.poolID] = sample
	}
}

// capacityPressure 返回池子在各维度里最紧的利用率，没有任何上限时 known 为 false。
func capacityPressure(usage channelcapacity.Usage, rpm, tpm, concurrency int64) (float64, bool) {
	pressure := -1.0
	if rpm > 0 {
		pressure = max(pressure, float64(usage.RPM)/float64(rpm))
	}
	if tpm > 0 {
		pressure = max(pressure, float64(usage.TPM)/float64(tpm))
	}
	if concurrency > 0 {
		pressure = max(pressure, float64(usage.InFlight)/float64(concurrency))
	}
	return pressure, pressure >= 0
}

// smoothCapacityPressure 对压力做指数平滑，避免流量在模型之间来回摆。
// 饱和立即生效、离开饱和也立即按原值算，不做平滑；未知不参与平滑。
func smoothCapacityPressure(previous capacitySample, hadPrevious bool, raw float64, known, saturated bool, pricing autorouter.CapacityPricing, now time.Time) float64 {
	if !known {
		return -1
	}
	tau := float64(pricing.SmoothingSeconds)
	if !hadPrevious || !previous.known || tau <= 0 || saturated || previous.saturated || raw >= 1 || previous.pressure >= 1 {
		return raw
	}
	alpha := 1 - math.Exp(-now.Sub(previous.updatedAt).Seconds()/tau)
	return previous.pressure + alpha*(raw-previous.pressure)
}

// resetCapacityPriceCache 清空节点本地缓存，测试用。
func resetCapacityPriceCache() {
	capacityPriceCacheMu.Lock()
	defer capacityPriceCacheMu.Unlock()
	capacityPriceCache = map[string]capacitySample{}
}
