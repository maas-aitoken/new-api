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
// 落到哪个渠道按原生选渠道的规则算：会话亲和命中就是那个渠道；否则按分组顺序取第一个过滤后
// 还有渠道的分组，最高优先级档内按权重给概率，倍率取期望（model.FirstAttemptChannelShares）。
// 只覆盖首次尝试，重试换渠道不在估算里。
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
}

func (a autoRouteCost) estimate(names []string, effort string, ignoreClientReasoning bool) map[string]autorouter.Cost {
	serves := func(name string) bool { return a.serves(name, ignoreClientReasoning) }
	groups := []string{a.usingGroup}
	if a.usingGroup == "auto" {
		groups = service.GetRequestAutoGroups(a.c, common.GetContextKeyString(a.c, constant.ContextKeyUserGroup))
	}
	estimates := make(map[string]autorouter.Cost, len(names))
	for _, name := range names {
		serving, _, ok := service.AutoServingModel(name, effort, serves)
		if !ok {
			continue
		}
		// 选定模型后能力门控在 enforce 下会把渠道收窄成通过判定的那些，这里先按同一口径收窄。
		filters := service.GetChannelConstraints(a.c).Filters
		channelIDs := a.channelsOf(serving)
		if kept := a.gate.KeptChannels(serving, channelIDs, ignoreClientReasoning); len(kept) != len(channelIDs) {
			filters = append(slices.Clone(filters), taskdto.ChannelFilter{Kind: taskdto.FilterCapability, AllowedChannelIDs: kept})
		}
		group, shares, source := a.affinityShare(groups, serving, filters)
		if len(shares) == 0 {
			group, shares = model.FirstAttemptChannelShares(groups, serving, filters)
			source = autorouter.CostSourceExpected
		}
		if len(shares) == 0 {
			continue
		}
		quote := a.quoteAt(name, group)
		if quote.Kind != helper.RoutingPerToken {
			continue
		}
		cost := autorouter.Cost{Price: quote.PricePerMillion, Channels: len(shares), Source: source}
		for _, share := range shares {
			cost.Multiplier += share.Probability * channelCostMultiplier(share.Channel, serving)
		}
		if len(shares) == 1 {
			cost.ChannelID = shares[0].Channel.Id
		}
		estimates[name] = cost
	}
	return estimates
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
