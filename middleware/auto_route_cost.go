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
// 渠道的费用倍率（autorouter.Candidates.Costs）。费用倍率按用户请求一侧的模型名配，不参与计费，
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

// autoCostTarget 是一个候选真正服务请求的名字，和选定模型后会用的渠道范围。
type autoCostTarget struct {
	name    string // 榜单里的模型名，报价按它
	serving string // 真正服务请求的名字，找渠道、查倍率按它
	allowed []int  // 能力门控收窄之后的渠道，与正式调度发的 allowed_channel_ids 同一口径
	filters []taskdto.ChannelFilter
}

func (a autoRouteCost) estimate(names []string, effort string, ignoreClientReasoning bool) map[string]autorouter.Cost {
	serves := func(name string) bool { return a.serves(name, ignoreClientReasoning) }
	groups := []string{a.usingGroup}
	if a.usingGroup == "auto" {
		groups = service.GetRequestAutoGroups(a.c, common.GetContextKeyString(a.c, constant.ContextKeyUserGroup))
	}
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
	estimates := make(map[string]autorouter.Cost, len(targets))
	for i, target := range targets {
		if previewed != nil {
			switch previewed[i].Status {
			case service.SchedulerPreviewOK:
				if cost, ok := a.costAtChannel(groups, target, previewed[i].ChannelID); ok {
					estimates[target.name] = cost
					continue
				}
			case service.SchedulerPreviewNoCandidate:
				// 正式调度对这个模型一定 503（池耗尽、超额度或容量未知），比较时跳过它。
				a.exclude(target.serving, autorouter.ReasonSchedulerNoCandidate)
				estimates[target.name] = autorouter.Cost{Excluded: true, Reason: autorouter.ReasonSchedulerNoCandidate}
				continue
			}
		}
		if cost, ok := a.nativeCost(groups, target); ok {
			estimates[target.name] = cost
		}
	}
	return estimates
}

// previewScheduler 问调度器每个候选会落到哪个渠道，参数与正式调度相同（会话亲和的渠道作为
// preferred_channel_id）。这次请求不由调度器决定渠道、或预览不可用时返回 nil。
func (a autoRouteCost) previewScheduler(targets []autoCostTarget) []service.SchedulerPreviewResult {
	if len(targets) == 0 || !service.SchedulerEnforcedForRequest(a.c) {
		return nil
	}
	requests := make([]service.SchedulerPreviewTarget, len(targets))
	for i, target := range targets {
		requests[i] = service.SchedulerPreviewTarget{Model: target.serving, AllowedChannelIDs: target.allowed}
		if preferred, found := service.PeekPreferredChannelByAffinity(a.c, target.serving, a.usingGroup); found {
			requests[i].PreferredChannelID = preferred
		}
	}
	return service.PreviewScheduler(a.c, a.usingGroup, requests)
}

// costAtChannel 是确定落到某个渠道时的比较成本：计费分组取第一个在这个渠道上服务这个模型的分组，
// 与调度器选定渠道后 Distribute 定分组的规则相同。
func (a autoRouteCost) costAtChannel(groups []string, target autoCostTarget, channelID int) (autorouter.Cost, bool) {
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return autorouter.Cost{}, false
	}
	for _, group := range groups {
		if !model.IsChannelEnabledForGroupModel(group, target.serving, channel.Id) {
			continue
		}
		quote := a.quoteAt(target.name, group)
		if quote.Kind != helper.RoutingPerToken {
			return autorouter.Cost{}, false
		}
		return autorouter.Cost{Price: quote.PricePerMillion, Multiplier: channelCostMultiplier(channel, target.serving),
			ChannelID: channel.Id, Channels: 1, Source: autorouter.CostSourceScheduler}, true
	}
	return autorouter.Cost{}, false
}

// nativeCost 按原生选渠道的规则估算：会话亲和命中时必落那个渠道，否则按首次命中概率取期望倍率。
func (a autoRouteCost) nativeCost(groups []string, target autoCostTarget) (autorouter.Cost, bool) {
	group, shares, source := a.affinityShare(groups, target.serving, target.filters)
	if len(shares) == 0 {
		group, shares = model.FirstAttemptChannelShares(groups, target.serving, target.filters)
		source = autorouter.CostSourceExpected
	}
	if len(shares) == 0 {
		return autorouter.Cost{}, false
	}
	quote := a.quoteAt(target.name, group)
	if quote.Kind != helper.RoutingPerToken {
		return autorouter.Cost{}, false
	}
	cost := autorouter.Cost{Price: quote.PricePerMillion, Channels: len(shares), Source: source}
	for _, share := range shares {
		cost.Multiplier += share.Probability * channelCostMultiplier(share.Channel, target.serving)
	}
	if len(shares) == 1 {
		cost.ChannelID = shares[0].Channel.Id
	}
	return cost, true
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
