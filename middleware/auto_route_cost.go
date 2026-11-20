package middleware

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	channelcapacity "github.com/QuantumNous/new-api/pkg/channel_capacity"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// autoRouteCost 给 auto 候选里的模型估算比较成本和剩余容量：成本是首次请求命中的分组下的
// 用户价乘上会落到的渠道费用倍率；容量来自同一批首次落点渠道的模型-渠道能力设置。
// 费用倍率按用户请求一侧的模型名配，和计费乘的是同一个倍率；容量按上游模型名配，和容量计数用同一池。
// 调度预览只提供当前可服务性、渠道成本和确定的渠道落点，不参与质量、成功率评分。
//
// 落到哪个渠道：这次请求由调度器决定渠道（强制模式或金丝雀命中）时问调度器的只读预览；
// 预览不可用或某个模型没有答案时按原生选渠道的规则算：会话亲和命中就是那个渠道，否则按分组
// 顺序取第一个过滤后还有渠道的分组，最高优先级档内按权重给概率，倍率取期望
// （model.FirstAttemptChannelShares）。只覆盖首次尝试，重试换渠道不在估算里。
type autoRouteCost struct {
	c          *gin.Context
	usingGroup string
	gate       *service.CapabilityGate
	// serves 判定一个名字能不能服务这个请求，第二个参数是否撇开客户端的推理字段（与 Serves 一致）。
	serves func(name string, ignoreClientReasoning bool) bool
	// channelsOf 是这个名字在令牌分组（auto 时为各自动分组的并集）里可用的渠道。
	channelsOf func(name string) []int
	// quoteAt 是这个模型在某个计费分组下的报价（含分组倍率）。
	quoteAt func(name, billingGroup string) helper.RoutingQuote
	// exclude 把调度器预览说这次一定没有渠道的名字记成不能服务，后面的兜底也不会再选它。
	exclude func(name, reason string)
}

// autoCostPlan 是一个候选的成本估算落点：计费分组、可能落到的渠道及概率，以及
// 调度器回答了却没用上它的原因。shares 为空表示这个候选没法估算。
type autoCostPlan struct {
	group  string
	shares []model.ChannelShare
	source string
	note   string
}

// autoCostTarget 是一个候选真正服务请求的名字，和选定模型后会用的渠道范围。
type autoCostTarget struct {
	name    string // 榜单里的模型名，报价按它
	serving string // 真正服务请求的名字，找渠道、查倍率按它
	allowed []int  // 能力门控收窄之后的渠道，与正式调度发的 allowed_channel_ids 同一口径
	filters []taskdto.ChannelFilter
}

func (a autoRouteCost) estimate(names []string, effort string, ignoreClientReasoning bool) (map[string]autorouter.Cost, *autorouter.PreviewOutcome) {
	serves := func(name string) bool { return a.serves(name, ignoreClientReasoning) }
	groups := a.billingGroups()
	targets := make([]autoCostTarget, 0, len(names))
	for _, name := range names {
		serving, _, ok := service.AutoServingModel(name, effort, serves)
		if !ok {
			continue
		}
		// 选定模型后能力门控在 enforce 下会把渠道收窄成通过判定的那些，这里先按同一口径收窄。
		filters := service.GetChannelConstraints(a.c).Filters
		channelIDs := a.channelsOf(serving)
		kept := a.gate.KeptChannels(serving, channelIDs, ignoreClientReasoning)
		if len(kept) != len(channelIDs) {
			filters = append(slices.Clone(filters), taskdto.ChannelFilter{Kind: taskdto.FilterCapability, AllowedChannelIDs: kept})
		}
		targets = append(targets, autoCostTarget{name: name, serving: serving, allowed: kept, filters: filters})
	}

	previewed := a.previewScheduler(targets)
	plans := make([]autoCostPlan, len(targets))
	excluded := make([]bool, len(targets))
	for i, target := range targets {
		// note 是调度器回答了、却没用上它给这个模型的答案的原因，跟着原生估算写进留痕。
		note := ""
		if previewed.Status == service.SchedulerPreviewAnswered {
			switch result := previewed.Results[i]; result.Status {
			case service.SchedulerPreviewOK:
				if plan, ok := a.channelPlan(groups, target, result.ChannelID); ok && a.perToken(target.name, plan.group) {
					plans[i] = plan
					continue
				}
				note = previewUnusableChannel
			case service.SchedulerPreviewNoCandidate:
				// 正式调度对这个模型一定 503（池耗尽、超额度或容量未知），比较时跳过它。
				a.exclude(target.serving, autorouter.ReasonSchedulerNoCandidate)
				excluded[i] = true
				continue
			default:
				note = result.Status
			}
		}
		plan, ok := a.nativePlan(groups, target)
		if !ok {
			continue
		}
		plan.note = note
		plans[i] = plan
	}

	estimates := make(map[string]autorouter.Cost, len(targets))
	capacities := a.capacityScores(targets, plans)
	for i, target := range targets {
		if excluded[i] {
			estimates[target.name] = autorouter.Cost{Excluded: true, Reason: autorouter.ReasonSchedulerNoCandidate}
			continue
		}
		plan := plans[i]
		if len(plan.shares) == 0 || !a.perToken(target.name, plan.group) {
			continue
		}
		cost := autorouter.Cost{
			Price:    a.quoteAt(target.name, plan.group).PricePerMillion,
			Channels: len(plan.shares),
			Source:   plan.source,
			Preview:  plan.note,
		}
		if capacity, ok := capacities[target.name]; ok {
			cost.Capacity, cost.CapacityKnown = capacity, true
		}
		multiplier := 0.0
		for _, share := range plan.shares {
			multiplier += share.Probability * channelCostMultiplier(share.Channel, target.serving)
		}
		cost.Multiplier = multiplier
		if len(plan.shares) == 1 {
			cost.ChannelID = plan.shares[0].Channel.Id
		}
		estimates[target.name] = cost
	}
	if previewed.Status == "" {
		return estimates, nil
	}
	return estimates, &autorouter.PreviewOutcome{Status: previewed.Status, HTTPStatus: previewed.HTTPStatus}
}

type autoCapacityReference struct {
	share          float64
	channelID      int
	upstreamModel  string
	limit          dto.ModelLimit
	capacityPoolID int
}

type autoCapacityBucket struct {
	limit     dto.ModelLimit
	usage     channelcapacity.Usage
	exhausted bool
}

func (a autoRouteCost) capacityScores(targets []autoCostTarget, plans []autoCostPlan) map[string]float64 {
	references := make([][]autoCapacityReference, len(targets))
	capacityPoolIDs := make(map[int]struct{})
	for i, target := range targets {
		for _, share := range plans[i].shares {
			if share.Channel == nil || share.Probability <= 0 {
				continue
			}
			settingRaw := ""
			if share.Channel.Setting != nil {
				settingRaw = *share.Channel.Setting
			}
			capacityModel := model.ChannelCapacityModel(share.Channel.GetModels(), share.Channel.GetModelMapping(), target.serving)
			capacity := model.ChannelModelCapacityOf(settingRaw, share.Channel.GetModelMapping(), capacityModel)
			references[i] = append(references[i], autoCapacityReference{
				share:          share.Probability,
				channelID:      share.Channel.Id,
				upstreamModel:  capacity.UpstreamModel,
				limit:          capacity.Limit,
				capacityPoolID: capacity.CapacityPoolId,
			})
			if capacity.CapacityPoolId > 0 {
				capacityPoolIDs[capacity.CapacityPoolId] = struct{}{}
			}
		}
	}

	pools := make(map[int]*model.CapacityPool, len(capacityPoolIDs))
	if len(capacityPoolIDs) > 0 {
		allPools, err := model.GetCapacityPools("")
		if err != nil {
			return nil
		}
		for _, pool := range allPools {
			if _, needed := capacityPoolIDs[pool.Id]; needed {
				pools[pool.Id] = pool
			}
		}
	}

	poolViews := make(map[int]service.CapacityPoolLimitView, len(pools))
	if len(pools) > 0 {
		poolList := make([]*model.CapacityPool, 0, len(pools))
		for _, pool := range pools {
			poolList = append(poolList, pool)
		}
		views, err := service.ResolveCapacityPoolLimits(poolList)
		if err != nil {
			return nil
		}
		poolViews = views
	}

	buckets := make(map[string]autoCapacityBucket)
	targetShares := make([]map[string]float64, len(targets))
	for i, refs := range references {
		targetShares[i] = make(map[string]float64)
		for _, reference := range refs {
			key := channelcapacity.PoolID(reference.channelID, reference.upstreamModel)
			bucket := autoCapacityBucket{limit: reference.limit}
			if _, bound := pools[reference.capacityPoolID]; bound {
				key = channelcapacity.CapacityPoolID(reference.capacityPoolID)
				view := poolViews[reference.capacityPoolID]
				bucket.limit, bucket.exhausted = view.Limit, view.Exhausted
			}
			if existing, found := buckets[key]; found {
				bucket = existing
			}
			buckets[key] = bucket
			targetShares[i][key] += reference.share
		}
	}

	poolIDs := make([]string, 0, len(buckets))
	for key, bucket := range buckets {
		if bucket.limit.IsZero() && !bucket.exhausted {
			continue
		}
		poolIDs = append(poolIDs, key)
	}
	usages, err := channelcapacity.Snapshot(poolIDs)
	if err != nil {
		return nil
	}
	for key, bucket := range buckets {
		bucket.usage = usages[key]
		buckets[key] = bucket
	}

	result := make(map[string]float64, len(targets))
	for i, target := range targets {
		samples := make([]autorouter.CapacitySample, 0, len(targetShares[i]))
		for key, share := range targetShares[i] {
			bucket, found := buckets[key]
			if !found || (bucket.limit.IsZero() && !bucket.exhausted) {
				continue
			}
			samples = append(samples, autorouter.CapacitySample{
				Share:            share,
				RPMLimit:         float64(bucket.limit.RPM),
				RPMUsed:          float64(bucket.usage.RPM),
				TPMLimit:         float64(bucket.limit.TPM),
				TPMUsed:          float64(bucket.usage.TPM),
				ConcurrencyLimit: float64(bucket.limit.MaxConcurrency),
				ConcurrencyUsed:  float64(bucket.usage.InFlight),
				Exhausted:        bucket.exhausted,
			})
		}
		result[target.name] = autorouter.AggregateCapacity(samples)
	}
	return result
}

func (a autoRouteCost) perToken(name, group string) bool {
	return a.quoteAt(name, group).Kind == helper.RoutingPerToken
}

func (a autoRouteCost) billingGroups() []string {
	if a.usingGroup == "auto" {
		return service.GetRequestAutoGroups(a.c, common.GetContextKeyString(a.c, constant.ContextKeyUserGroup))
	}
	return []string{a.usingGroup}
}

// autoRouteCost 给 auto 候选里的模型估算比较成本：首次请求命中的分组下的用户价，乘上会落到的
// 渠道的费用倍率。费用倍率按用户请求一侧的模型名配，和计费乘的是同一个倍率。
// 调度预览只提供当前可服务性和渠道成本估算，不参与质量、成功率评分。// previewUnusableChannel 是调度器给了渠道、new-api 却没法按它报价时记的原因（渠道不在缓存里、
// 不在任何分组里服务这个模型，或在那个分组里按次计价），这个模型按原生规则估算。
const previewUnusableChannel = "unusable_channel"

// previewScheduler 问调度器每个候选会落到哪个渠道，参数与正式调度相同（会话亲和的渠道作为
// preferred_channel_id）。问不问、为什么没拿到回答由 service.PreviewScheduler 判断；这次请求
// 不由调度器决定渠道时它不发请求，这里也就不必先查会话亲和。
func (a autoRouteCost) previewScheduler(targets []autoCostTarget) service.SchedulerPreview {
	decided := service.SchedulerEnforcedForRequest(a.c)
	requests := make([]service.SchedulerPreviewTarget, len(targets))
	for i, target := range targets {
		requests[i] = service.SchedulerPreviewTarget{Model: target.serving, AllowedChannelIDs: target.allowed}
		if !decided {
			continue
		}
		if preferred, found := service.PeekPreferredChannelByAffinity(a.c, target.serving, a.usingGroup); found {
			requests[i].PreferredChannelID = preferred
		}
	}
	return service.PreviewScheduler(a.c, a.usingGroup, requests)
}

// channelPlan 是确定落到某个渠道时的估算落点：计费分组取第一个在这个渠道上服务这个模型的分组，
// 与调度器选定渠道后 Distribute 定分组的规则相同。
func (a autoRouteCost) channelPlan(groups []string, target autoCostTarget, channelID int) (autoCostPlan, bool) {
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return autoCostPlan{}, false
	}
	for _, group := range groups {
		if !model.IsChannelEnabledForGroupModel(group, target.serving, channel.Id) {
			continue
		}
		return autoCostPlan{group: group, source: autorouter.CostSourceScheduler,
			shares: []model.ChannelShare{{Channel: channel, Probability: 1}}}, true
	}
	return autoCostPlan{}, false
}

// nativePlan 按原生选渠道的规则估算：会话亲和命中时必落那个渠道，否则按首次命中概率取期望。
func (a autoRouteCost) nativePlan(groups []string, target autoCostTarget) (autoCostPlan, bool) {
	group, shares, source := a.affinityShare(groups, target.serving, target.filters)
	if len(shares) == 0 {
		group, shares = model.FirstAttemptChannelShares(groups, target.serving, target.filters)
		source = autorouter.CostSourceExpected
	}
	if len(shares) == 0 {
		return autoCostPlan{}, false
	}
	return autoCostPlan{group: group, shares: shares, source: source}, true
}

// affinityShare 判断会话亲和会不会把首次请求绑到某个渠道：规则与 SelectChannelForRequest 相同
// （渠道启用、满足过滤条件、在某个分组里服务这个模型），命中时首次必落这个渠道。
func (a autoRouteCost) affinityShare(groups []string, serving string, filters []taskdto.ChannelFilter) (string, []model.ChannelShare, string) {
	channelID, found := service.PeekPreferredChannelByAffinity(a.c, serving, a.usingGroup)
	if !found {
		return "", nil, ""
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
		return "", nil, ""
	}
	if satisfied, _ := model.ChannelSatisfiesFilters(channel, serving, filters); !satisfied {
		return "", nil, ""
	}
	for _, group := range groups {
		if model.IsChannelEnabledForGroupModel(group, serving, channel.Id) {
			return group, []model.ChannelShare{{Channel: channel, Probability: 1}}, autorouter.CostSourceAffinity
		}
	}
	return "", nil, ""
}

// channelCostMultiplier 是渠道对这个请求名配置的费用倍率，没配按 1。查法与记录 cost_actual
// 时相同（model.CostMultiplierModel），只解析 setting，不走 Channel.GetSetting（它解析失败会写库）。
func channelCostMultiplier(channel *model.Channel, serving string) float64 {
	if channel.Setting == nil || strings.TrimSpace(*channel.Setting) == "" {
		return 1
	}
	setting := dto.ChannelSettings{}
	if err := common.UnmarshalJsonStr(*channel.Setting, &setting); err != nil {
		return 1
	}
	return setting.CostMultiplierForModel(model.CostMultiplierModel(channel.GetModels(), serving))
}
