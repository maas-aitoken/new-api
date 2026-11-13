package middleware

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// autoRouteCost 给 auto 候选带里的模型估算比较成本：首次请求命中的分组下的用户价，乘上会落到的
// 渠道的费用倍率与容量溢价（autorouter.Candidates.Costs）。费用倍率按用户请求一侧的模型名配，
// 容量溢价按会落到的计数池的繁忙程度算（service.CapacityCostFactors），两者都不参与计费，
// 只影响 auto 选哪个模型。
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

	// 所有候选的容量压力一次取齐：逐个候选读会把候选带乘以渠道数的 Redis 往返放进热路径。
	requests := make([]service.CapacityCostRequest, len(targets))
	for i, plan := range plans {
		requests[i] = service.CapacityCostRequest{Shares: plan.shares, Serving: targets[i].serving}
	}
	factors := service.CapacityCostFactors(requests, channelCostMultiplier)
	estimates := make(map[string]autorouter.Cost, len(targets))
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
			Price:      a.quoteAt(target.name, plan.group).PricePerMillion,
			Multiplier: factors[i].Multiplier,
			Premium:    factors[i].Premium,
			Pressure:   factors[i].Pressure,
			Channels:   len(plan.shares),
			Source:     plan.source,
			Preview:    plan.note,
		}
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

func (a autoRouteCost) perToken(name, group string) bool {
	return a.quoteAt(name, group).Kind == helper.RoutingPerToken
}

func (a autoRouteCost) billingGroups() []string {
	if a.usingGroup == "auto" {
		return service.GetRequestAutoGroups(a.c, common.GetContextKeyString(a.c, constant.ContextKeyUserGroup))
	}
	return []string{a.usingGroup}
}

// pressure 只读地估一个模型首次会落到的池子有多忙，给"要不要继续沿用上一轮模型"用。
// 它按原生选渠道的规则估，不问调度器预览、不报价；没有容量信号时 known 为 false。
func (a autoRouteCost) pressure(name, effort string, ignoreClientReasoning bool) (float64, bool) {
	serving, _, ok := service.AutoServingModel(name, effort, func(candidate string) bool {
		return a.serves(candidate, ignoreClientReasoning)
	})
	if !ok {
		return 0, false
	}
	filters := service.GetChannelConstraints(a.c).Filters
	channelIDs := a.channelsOf(serving)
	kept := a.gate.KeptChannels(serving, channelIDs, ignoreClientReasoning)
	if len(kept) != len(channelIDs) {
		filters = append(slices.Clone(filters), taskdto.ChannelFilter{Kind: taskdto.FilterCapability, AllowedChannelIDs: kept})
	}
	plan, ok := a.nativePlan(a.billingGroups(), autoCostTarget{name: name, serving: serving, allowed: kept, filters: filters})
	if !ok || len(plan.shares) == 0 {
		return 0, false
	}
	factors := service.CapacityCostFactors([]service.CapacityCostRequest{{Shares: plan.shares, Serving: serving}},
		func(*model.Channel, string) float64 { return 1 })
	if !factors[0].Known {
		return 0, false
	}
	return factors[0].Pressure, true
}

// previewUnusableChannel 是调度器给了渠道、new-api 却没法按它报价时记的原因（渠道不在缓存里、
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
