package middleware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ModelRequest struct {
	Model    string `json:"model"`
	Group    string `json:"group,omitempty"`
	Workload string `json:"workload,omitempty"`
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		// Record the configured email header independently of affinity routing.
	emailHeader:
		for _, rule := range operation_setting.GetChannelAffinitySetting().Rules {
			for _, source := range rule.KeySources {
				if source.Type != "request_header" {
					continue
				}
				if email := strings.TrimSpace(c.GetHeader(source.Key)); email != "" {
					address, err := mail.ParseAddress(email)
					if err != nil || address.Address != email {
						continue
					}
					common.SetContextKey(c, constant.ContextKeyIselfEmail, email)
					break emailHeader
				}
			}
		}

		var channel *model.Channel
		defer func() {
			if c.Writer.Status() >= 400 {
				service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("request rejected"), types.ErrorCodeInvalidRequest, c.Writer.Status(), types.ErrOptionWithSkipRetry()))
			}
		}()
		constraints := service.GetChannelConstraints(c)
		constraints.AddFilter(taskdto.ChannelFilter{
			Kind:        taskdto.FilterRequestPath,
			RequestPath: c.Request.URL.Path,
		})
		service.AppendTaskPluginIdentityFilter(c, c.GetString("expected_task_plugin_key"))
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		if err != nil {
			abortWithOpenAiMessage(c, invalidRequestStatus(err), i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
			return
		}
		if workload := strings.TrimSpace(modelRequest.Workload); workload != "" {
			common.SetContextKey(c, constant.ContextKeySchedulerWorkload, workload)
		}
		// 能力门控在模型确定前构建：auto 用它筛候选模型，选定模型后再做渠道级收窄。
		// 未配置、请求体不可用或没有任何模型级声明时返回 nil，行为与改动前一致。
		capabilityGate := service.BuildCapabilityGate(c)
		_, pinned, _ := constraints.ResolvedPin()
		// 额度档位用完后，支持 auto 的请求改走 auto，忽略令牌模型池和路由偏好；
		// 其他接口由计费拦截。业务场景的 key 不受限。
		scene, _ := common.GetContextKeyType[common.TokenScene](c, constant.ContextKeyTokenScene)
		businessKey := scene == common.TokenSceneBusiness
		if shouldSelectChannel && !pinned && !businessKey && service.IsAutoRouterRequest(c, autorouter.AutoModel) {
			exhausted, err := model.UserQuotaTierExhausted(c.GetInt("id"))
			if err != nil {
				logger.LogError(c, common.LogText("failed to check the quota tier of user %d: %s", c.GetInt("id"), err.Error()))
			} else if exhausted {
				modelRequest.Model = autorouter.AutoModel
				common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, false)
				common.SetContextKey(c, constant.ContextKeyTokenAutoPreference, "")
				common.SetContextKey(c, constant.ContextKeyTokenRoutingPreference, nil)
			}
		}
		autoRouting := shouldSelectChannel && service.IsAutoRouterRequest(c, modelRequest.Model)
		if pinned && autoRouting {
			abortWithOpenAiMessage(c, http.StatusBadRequest, "model auto is not supported for a token pinned to a channel")
			return
		}
		if !pinned {
			// Select a channel for the user
			// check token model mapping
			modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
			var tokenModelLimit map[string]bool
			if modelLimitEnable {
				s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
				if !ok {
					// token model limit is empty, all models are not allowed
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenNoModelAccess))
					return
				}
				tokenModelLimit, ok = s.(map[string]bool)
				if !ok {
					tokenModelLimit = map[string]bool{}
				}
				// "auto" is not a whitelist entry; it resolves to a whitelisted model below.
				if !TokenModelLimitAllows(tokenModelLimit, modelRequest.Model) && !autoRouting {
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": modelRequest.Model}))
					return
				}
			}

			if shouldSelectChannel {
				if modelRequest.Model == "" {
					abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorModelNameRequired))
					return
				}
				// check path is /pg/chat/completions
				if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
					usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
					playgroundRequest := &dto.PlayGroundRequest{}
					err = common.UnmarshalBodyReusable(c, playgroundRequest)
					if err != nil {
						abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": err.Error()}))
						return
					}
					if playgroundRequest.Group != "" {
						if !service.GroupInUserUsableGroups(usingGroup, playgroundRequest.Group) && playgroundRequest.Group != usingGroup {
							abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
							return
						}
						common.SetContextKey(c, constant.ContextKeyUsingGroup, playgroundRequest.Group)
					}
				}

				if autoRouting {
					usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
					// The task and default lists hold every ranked model, each
					// probed in up to five effort variants, so availability is
					// memoized per request; without the memory cache every
					// channel lookup is a query, so candidates are first narrowed
					// to the groups' enabled models with one query per group.
					var enabled map[string]bool
					if !common.MemoryCacheEnabled {
						enabled = map[string]bool{}
						groups := []string{usingGroup}
						if usingGroup == "auto" {
							groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
						}
						for _, group := range groups {
							for _, name := range model.GetGroupEnabledModels(group) {
								enabled[name] = true
							}
						}
					}
					refusals := map[string]string{}
					channels := map[string][]int{}
					// 选路阶段估价：只读价格配置，不预扣、不写额度。
					userSetting, _ := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
					pricer := helper.NewRoutingPricer(c, func(candidate string) int64 {
						return service.RoutingInputTokens(c, candidate)
					}, userSetting.AcceptUnsetRatioModel)
					quotes := map[[2]string]helper.RoutingQuote{}
					// quoteAt 是候选在某个计费分组下的报价（含分组倍率），按请求备忘。
					quoteAt := func(candidate, billingGroup string) helper.RoutingQuote {
						key := [2]string{candidate, billingGroup}
						if quote, seen := quotes[key]; seen {
							return quote
						}
						userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
						groupRatio, special := ratio_setting.GetGroupGroupRatio(userGroup, billingGroup)
						if !special {
							groupRatio = ratio_setting.GetGroupRatio(billingGroup)
						}
						quotes[key] = pricer.Quote(candidate, groupRatio)
						return quotes[key]
					}
					// 令牌分组是 auto 时，计费分组是第一个对该模型有可用渠道的自动分组，倍率因模型而异；
					// 固定分组下所有候选的倍率相同，在归一化里抵消。比价时再按首次会命中的分组重新报价（autoRouteCost）。
					quoteOf := func(candidate string) helper.RoutingQuote {
						billingGroup := usingGroup
						if usingGroup == "auto" {
							userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
							for _, group := range service.GetRequestAutoGroups(c, userGroup) {
								if len(model.GetEnabledChannelIDsForGroupModel(group, candidate)) > 0 {
									billingGroup = group
									break
								}
							}
						}
						return quoteAt(candidate, billingGroup)
					}
					channelsOf := func(candidate string) []int {
						channelIDs, seen := channels[candidate]
						if !seen {
							channelIDs = usableChannelIDs(c, candidate, usingGroup, c.Request.URL.Path, model.GetEnabledChannelIDsForGroupModel)
							channels[candidate] = channelIDs
						}
						return channelIDs
					}
					// refusal 说明候选为什么不能服务这个请求，能服务时返回空串。候选按"模型+渠道"判定：
					// 至少有一个可用渠道的生效能力满足请求，渠道级收窄在这里就生效，
					// 不会选中一个渠道全被收窄淘汰的模型再 503。没有价格的模型计费时会直接失败，
					// 所以任何路径都不选它们。判定顺序就是留痕里原因的优先级。
					refusal := func(candidate string, ignoreClientReasoning bool) string {
						key := candidate
						if ignoreClientReasoning {
							key += "|auto-reasoning"
						}
						if reason, seen := refusals[key]; seen {
							return reason
						}
						reason := ""
						switch {
						case modelLimitEnable && !TokenModelLimitAllows(tokenModelLimit, candidate):
							reason = autorouter.ReasonTokenLimit
						case enabled != nil && !enabled[candidate] && !enabled[ratio_setting.FormatMatchingModelName(candidate)]:
							reason = autorouter.ReasonGroupDisabled
						case len(channelsOf(candidate)) == 0:
							reason = autorouter.ReasonNoChannel
						case quoteOf(candidate).Kind == helper.RoutingUnpriced:
							reason = autorouter.ReasonUnpriced
						case capabilityGate != nil && !capabilityGate.AllowsAnyChannel(candidate, channelsOf(candidate), ignoreClientReasoning):
							reason = autorouter.ReasonCapability
						}
						refusals[key] = reason
						return reason
					}
					serves := func(candidate string, ignoreClientReasoning bool) bool {
						return refusal(candidate, ignoreClientReasoning) == ""
					}
					costs := autoRouteCost{c: c, usingGroup: usingGroup, gate: capabilityGate, serves: serves, channelsOf: channelsOf, quoteAt: quoteAt,
						exclude: func(candidate, reason string) {
							// 两种口径的备忘都记上，后面的兜底和最终解析都不会再选它。
							refusals[candidate], refusals[candidate+"|auto-reasoning"] = reason, reason
						}}
					result, err := service.ResolveAutoModel(c, service.AutoCandidates{
						Serves:   serves,
						Why:      func(candidate string) string { return refusal(candidate, true) },
						Channels: func(candidate string) []int { return channels[candidate] },
						Price: func(candidate string) (float64, bool) {
							quote := quoteOf(candidate)
							return quote.PricePerMillion, quote.Kind == helper.RoutingPerToken
						},
						Costs: costs.estimate,
					})
					if capabilityGate != nil {
						capabilityGate.LogSummary(c)
					}
					if err != nil {
						if capabilityGate != nil && capabilityGate.AllEvaluatedRejected() {
							abortWithOpenAiMessage(c, http.StatusServiceUnavailable, capabilityGate.ExplainModelRejection(), types.ErrorCodeModelNotFound)
							return
						}
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, err.Error(), types.ErrorCodeModelNotFound)
						return
					}
					modelRequest.Model = result.ResolvedModel
					// 透传渠道（全局或渠道级）不重建请求体而是原样转发存储的字节，
					// 所以 auto 定下的结论要直接写进存储的请求体：model 改成解析后的模型
					// （否则上游收到的仍是 "auto"，而计费和日志记的是解析后的模型），
					// 推理档位写进入站协议自己的字段。非透传渠道也读这份请求体。
					protocol, _ := service.AutoRouterProtocol(c.Request.URL.Path)
					if err := rewriteJSONBody(c, func(body []byte) ([]byte, error) {
						return autorouter.PatchBody(body, protocol, result.ResolvedModel, result.EffortAction, result.AppliedEffort)
					}); err != nil {
						abortWithOpenAiMessage(c, http.StatusInternalServerError, "failed to rewrite the auto routed request: "+err.Error())
						return
					}
				}
			}
		}
		if !pinned && shouldSelectChannel {
			usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
			var selectGroup string
			allowedChannels := usableChannelIDs(c, modelRequest.Model, usingGroup, c.Request.URL.Path, model.GetRoutableChannelIDsForGroupModel)
			if capabilityGate != nil {
				narrowed := capabilityGate.FilterChannels(c, modelRequest.Model, allowedChannels)
				if len(allowedChannels) > 0 && len(narrowed) == 0 {
					capabilityGate.LogSummary(c)
					abortWithOpenAiMessage(c, http.StatusServiceUnavailable, capabilityGate.ExplainChannelRejection(modelRequest.Model), types.ErrorCodeModelNotFound)
					return
				}
				if len(narrowed) != len(allowedChannels) {
					constraints.AddFilter(taskdto.ChannelFilter{
						Kind:              taskdto.FilterCapability,
						AllowedChannelIDs: narrowed,
					})
					allowedChannels = narrowed
				}
				capabilityGate.LogSummary(c)
			}
			if service.SchedulerClient().Enabled {
				common.SetContextKey(c, constant.ContextKeySchedulerAllowedChannelIDs, allowedChannels)
			}
			if service.SchedulerEnforcedForRequest(c) {
				if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelRequest.Model, usingGroup); found {
					common.SetContextKey(c, constant.ContextKeySchedulerAffinityChannelID, preferredChannelID)
				}
				if err := service.RunSchedulerShadow(c, modelRequest.Model, usingGroup); err != nil {
					if message, ok := service.SchedulerDecisionErrorMessage(err); ok {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, message, types.ErrorCodeModelNotFound)
						return
					} else if service.IsSchedulerTransientUnavailable(err) && service.SchedulerEmergencyNativeAllowed(modelRequest.Model, usingGroup) {
						service.MarkSchedulerEmergency(c, err)
						common.SysLog(fmt.Sprintf("scheduler emergency native routing enabled: request_id=%s model=%s group=%s error=%v", c.GetString(common.RequestIdKey), modelRequest.Model, usingGroup, err))
					} else {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "scheduler unavailable: "+err.Error(), types.ErrorCodeModelNotFound)
						return
					}
				} else {
					candidate, found := service.SchedulerCandidateForInitial(c)
					if !found {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, "scheduler returned no usable candidate", types.ErrorCodeModelNotFound)
						return
					}
					candidateChannel, candidateErr := model.GetChannelById(candidate.ChannelID, true)
					if candidateErr != nil || candidateChannel == nil || candidateChannel.Status != common.ChannelStatusEnabled ||
						!channelSupportsRequestPath(candidateChannel, c.Request.URL.Path, modelRequest.Model) {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, fmt.Sprintf("scheduler candidate channel %d unavailable", candidate.ChannelID), types.ErrorCodeModelNotFound)
						return
					}
					if !channelPassesCapabilityFilter(c, candidateChannel.Id) {
						abortWithOpenAiMessage(c, http.StatusServiceUnavailable, fmt.Sprintf("scheduler candidate channel %d rejected by capability constraints", candidate.ChannelID), types.ErrorCodeModelNotFound)
						return
					}
					if usingGroup == "auto" {
						userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
						for _, g := range service.GetRequestAutoGroups(c, userGroup) {
							if model.IsChannelEnabledForGroupModel(g, modelRequest.Model, candidateChannel.Id) {
								selectGroup = g
								common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
								break
							}
						}
						if selectGroup == "" {
							abortWithOpenAiMessage(c, http.StatusServiceUnavailable, fmt.Sprintf("scheduler candidate channel %d is not enabled for model %s", candidate.ChannelID, modelRequest.Model), types.ErrorCodeModelNotFound)
							return
						}
					} else {
						if !model.IsChannelEnabledForGroupModel(usingGroup, modelRequest.Model, candidateChannel.Id) {
							abortWithOpenAiMessage(c, http.StatusServiceUnavailable, fmt.Sprintf("scheduler candidate channel %d is not enabled for group %s and model %s", candidate.ChannelID, usingGroup, modelRequest.Model), types.ErrorCodeModelNotFound)
							return
						}
						selectGroup = usingGroup
					}
					channel = candidateChannel
				}
			}
		}
		if pinned || (shouldSelectChannel && channel == nil) {
			usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
			var selectErr *service.ChannelSelectError
			channel, _, selectErr = service.SelectChannelForRequest(c, modelRequest.Model, &service.RetryParam{
				Ctx:         c,
				ModelName:   modelRequest.Model,
				TokenGroup:  usingGroup,
				RequestPath: c.Request.URL.Path,
				Retry:       common.GetPointer(0),
			})
			if selectErr != nil {
				if selectErr.FilterKind == taskdto.FilterTaskPluginIdentity {
					logTaskPluginChannelDecision(c, selectErr.Channel, modelRequest.Model, "channel_rejected", "identity_mismatch")
				}
				message := selectErr.Message
				if selectErr.NoAvailableChannel {
					message = noAvailableChannelMessage(c, usingGroup, modelRequest.Model)
				} else if selectErr.MessageID != "" {
					message = i18n.T(c, selectErr.MessageID, selectErr.Params)
				}
				abortWithOpenAiMessage(c, selectErr.StatusCode, message, selectErr.Code)
				return
			}
		}
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		SetupContextForSelectedChannel(c, channel, modelRequest.Model)
		if !pinned && channel != nil && !service.SchedulerEnforcedForRequest(c) {
			if err := service.RunSchedulerShadow(c, modelRequest.Model, common.GetContextKeyString(c, constant.ContextKeyUsingGroup)); err != nil {
				common.SysLog(fmt.Sprintf("scheduler shadow skipped: %v", err))
			}
		}
		c.Next()
		if service.SchedulerEmergencyNativeActive(c) {
			common.SysLog(fmt.Sprintf("scheduler degraded native selected: request_id=%s model=%s group=%s channel_id=%d status=%d", c.GetString(common.RequestIdKey), modelRequest.Model, common.GetContextKeyString(c, constant.ContextKeyUsingGroup), common.GetContextKeyInt(c, constant.ContextKeyChannelId), c.Writer.Status()))
		}
		if channel != nil && c.Writer != nil && c.Writer.Status() < http.StatusBadRequest {
			service.RecordChannelAffinity(c, channel.Id)
		}
	}
}

// usableChannelIDs is new-api's effective group/model permission scope: the
// enabled channels of the group (or auto groups) that serve the model on this
// request path. Scheduler must never rank a channel outside it, and the auto
// router only picks models that have one.
// lookup 决定按什么口径找分组里的渠道：选定模型后的请求范围用
// model.GetRoutableChannelIDsForGroupModel，与原生选路一致（带推理后缀的名字退到
// 路由名）；auto 判断候选用 model.GetEnabledChannelIDsForGroupModel，强度变体只有
// 渠道真的列了才算可用。
func usableChannelIDs(c *gin.Context, modelName, usingGroup, requestPath string, lookup func(group, modelName string) []int) []int {
	groups := []string{usingGroup}
	if usingGroup == "auto" {
		groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	}
	allowed := make(map[int]struct{})
	for _, group := range groups {
		for _, channelID := range lookup(group, modelName) {
			channel, err := model.CacheGetChannel(channelID)
			if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled ||
				!channelSupportsRequestPath(channel, requestPath, modelName) {
				continue
			}
			allowed[channelID] = struct{}{}
		}
	}
	ids := make([]int, 0, len(allowed))
	for channelID := range allowed {
		ids = append(ids, channelID)
	}
	slices.Sort(ids)
	return ids
}

// getModelFromRequest 从请求中读取模型信息
// 根据 Content-Type 自动处理：
// - application/json
// - application/x-www-form-urlencoded
// - multipart/form-data

// noAvailableChannelMessage explains a 503 for a task-plugin-claimed model.
// The response tells the caller the model is plugin-claimed without naming the
// plugin; the candidate plugin keys go to the server log under the request id.
func noAvailableChannelMessage(c *gin.Context, group, modelName string) string {
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if exists && ok && pinned.Plugin != nil {
		keys := []string{pinned.Plugin.Meta.Key}
		if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
			if endpoint, ok := value.(jsplugin.PinnedEndpoint); ok && len(endpoint.Candidates) > 0 {
				keys = nil
				for _, candidate := range endpoint.Candidates {
					if candidate.Plugin != nil {
						keys = append(keys, candidate.Plugin.Meta.Key)
					}
				}
			}
		}
		logger.LogWarn(c, common.LogText("task_plugin subsystem=distribution event=no_available_channel group=%q model=%q plugins=%q reason=no_eligible_channel", group, modelName, strings.Join(keys, ",")))
		return i18n.T(c, i18n.MsgDistributorNoAvailableChannelTaskPlugin, map[string]any{"Group": group, "Model": modelName})
	}
	return i18n.T(c, i18n.MsgDistributorNoAvailableChannel, map[string]any{"Group": group, "Model": modelName})
}

func channelMatchesExpectedTaskPlugin(c *gin.Context, channel *model.Channel, expected string) bool {
	if channel == nil {
		return false
	}
	if c != nil {
		if _, matched := pinnedEndpointCandidateForChannel(c, channel, expected); matched {
			return true
		}
	}
	if expected == "" {
		return channel.Type != constant.ChannelTypeTaskPlugin
	}
	if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
		return channel.GetSetting().BindsTaskPlugin(expected)
	}
	if c == nil {
		return false
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return false
	}
	plugin, ok := pinned.Generation.GetByChannelType(channel.Type)
	return ok && plugin == pinned.Plugin
}

// channelPassesCapabilityFilter 复核渠道是否落在本请求的能力允许集内。
// 没有能力过滤器时一律放行。
func channelPassesCapabilityFilter(c *gin.Context, channelID int) bool {
	for _, filter := range service.GetChannelConstraints(c).Filters {
		if filter.Kind != taskdto.FilterCapability {
			continue
		}
		if filter.AllowedChannelIDs == nil {
			continue
		}
		if !slices.Contains(filter.AllowedChannelIDs, channelID) {
			return false
		}
	}
	return true
}

// channelSupportsRequestPath reports whether a channel can serve the request path.
// Only Advanced Custom (type 58) channels are path-checked; all other channel types
// always pass. A type-58 channel is usable only when one of its routes matches.
func channelSupportsRequestPath(channel *model.Channel, requestPath string, requestModel string) bool {
	if channel == nil {
		return false
	}
	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return true
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPathForModel(requestPath, requestModel)
}

func pinnedEndpointCandidateForChannel(c *gin.Context, channel *model.Channel, expected string) (jsplugin.ProtocolBinding, bool) {
	if c == nil || channel == nil || expected == "" {
		return jsplugin.ProtocolBinding{}, false
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	pinned, ok := value.(jsplugin.PinnedEndpoint)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil {
		return jsplugin.ProtocolBinding{}, false
	}
	candidates := pinned.Candidates
	if len(candidates) == 0 {
		candidates = []jsplugin.ProtocolBinding{{Plugin: pinned.Plugin, Protocol: pinned.Protocol, Operation: pinned.Operation, Model: pinned.Model}}
	}
	expectedOwned := false
	selected := jsplugin.ProtocolBinding{}
	setting := channel.GetSetting()
	for _, candidate := range candidates {
		if candidate.Plugin == nil {
			continue
		}
		if candidate.Plugin.Meta.Key == expected {
			expectedOwned = true
		}
		if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
			// A New API channel may bind several candidates. The first bound
			// candidate in generation order executes, so the billing provider
			// depends only on the channel and the request, never on which
			// channels an earlier retry attempt happened to try.
			if selected.Plugin == nil && setting.BindsTaskPlugin(candidate.Plugin.Meta.Key) {
				selected = candidate
			}
			continue
		}
		plugin, indexed := pinned.Generation.GetByChannelType(channel.Type)
		if indexed && plugin == candidate.Plugin {
			selected = candidate
		}
	}
	return selected, expectedOwned && selected.Plugin != nil
}

// getModelFromRequest 从请求中读取模型信息
// 根据 Content-Type 自动处理：
// - application/json
// - application/x-www-form-urlencoded
// - multipart/form-data
func getModelFromRequest(c *gin.Context) (*ModelRequest, error) {
	if cached, exists := c.Get(contextKeyTaskPluginEndpointModel); exists {
		if modelRequest, ok := cached.(ModelRequest); ok {
			cachedRequest := modelRequest
			return &cachedRequest, nil
		}
	}
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return getModelFromJSONBody(c)
	}

	var modelRequest ModelRequest
	err := common.UnmarshalBodyReusable(c, &modelRequest)
	if err != nil {
		return nil, err
	}
	return &modelRequest, nil
}

func getModelFromJSONBody(c *gin.Context) (*ModelRequest, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	requestBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(requestBody) {
		return nil, errors.New("invalid JSON request body")
	}
	if countTopLevelJSONKey(requestBody, "model") > 1 {
		return nil, errors.New("model must be provided once")
	}

	values := gjson.GetManyBytes(requestBody, "model", "group", "provider", "workload")
	modelName, err := getJSONStringValue(values[0], "model")
	if err != nil {
		return nil, err
	}
	group, err := getJSONStringValue(values[1], "group")
	if err != nil {
		return nil, err
	}
	providerPresent := values[2].Exists() && values[2].Type != gjson.Null
	cleanedBody, err := sjson.DeleteBytes(requestBody, "provider")
	if err != nil {
		return nil, fmt.Errorf("failed to remove provider field: %w", err)
	}
	if string(cleanedBody) != string(requestBody) {
		replacement, err := common.CreateBodyStorage(cleanedBody)
		if err != nil {
			return nil, err
		}
		_ = storage.Close()
		c.Set(common.KeyBodyStorage, replacement)
		storage = replacement
	}
	if providerPresent {
		// 供应商层已移除。保留剥离动作是为了不让该字段透传给上游（部分供应商会 400），
		// 但路由偏好不再生效，因此显式告警，避免管理员以为配置仍起作用。
		logger.LogWarn(c.Request.Context(), common.LogText(
			"request provider field is ignored: model=%s request_id=%s; provider routing has been removed",
			modelName, c.GetString(common.RequestIdKey)))
	}
	workload, err := getJSONStringValue(values[3], "workload")
	if err != nil {
		return nil, err
	}

	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}
	c.Request.Body = io.NopCloser(storage)

	return &ModelRequest{
		Model:    modelName,
		Group:    group,
		Workload: workload,
	}, nil
}

func countTopLevelJSONKey(data []byte, target string) int {
	depth := 0
	inString := false
	escaped := false
	stringStart := 0
	expectingKey := false
	count := 0
	for index, current := range data {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current != '"' {
				continue
			}
			inString = false
			if depth == 1 && expectingKey {
				key := string(data[stringStart:index])
				var decodedKey string
				if common.Unmarshal(data[stringStart-1:index+1], &decodedKey) == nil {
					key = decodedKey
				}
				cursor := index + 1
				for cursor < len(data) && (data[cursor] == ' ' || data[cursor] == '\t' || data[cursor] == '\r' || data[cursor] == '\n') {
					cursor++
				}
				if cursor < len(data) && data[cursor] == ':' && key == target {
					count++
				}
				expectingKey = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
			stringStart = index + 1
		case '{':
			depth++
			if depth == 1 {
				expectingKey = true
			}
		case '}':
			depth--
		case ',':
			if depth == 1 {
				expectingKey = true
			}
		}
	}
	return count
}

func getJSONStringValue(result gjson.Result, field string) (string, error) {
	if !result.Exists() || result.Type == gjson.Null {
		return "", nil
	}
	if result.Type != gjson.String {
		return "", fmt.Errorf("field %s must be a string", field)
	}
	return result.String(), nil
}

func getModelRequest(c *gin.Context) (*ModelRequest, bool, error) {
	var modelRequest ModelRequest
	shouldSelectChannel := true
	var err error
	if modelName := c.GetString("resolved_task_model"); modelName != "" {
		modelRequest.Model = modelName
	} else if strings.Contains(c.Request.URL.Path, "/mj/") {
		relayMode := relayconstant.Path2RelayModeMidjourney(c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
			relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
			relayMode == relayconstant.RelayModeMidjourneyNotify ||
			relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
			shouldSelectChannel = false
		} else {
			midjourneyRequest := taskdto.MidjourneyRequest{}
			err = common.UnmarshalBodyReusable(c, &midjourneyRequest)
			if err != nil {
				return nil, false, err
			}
			midjourneyModel, mjErr, success := service.GetMjRequestModel(relayMode, &midjourneyRequest)
			if mjErr != nil {
				return nil, false, fmt.Errorf("%s", mjErr.Description)
			}
			if midjourneyModel == "" {
				if !success {
					return nil, false, fmt.Errorf("%s", i18n.T(c, i18n.MsgDistributorInvalidParseModel))
				} else {
					// task fetch, task fetch by condition, notify
					shouldSelectChannel = false
				}
			}
			modelRequest.Model = midjourneyModel
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos/") && strings.HasSuffix(c.Request.URL.Path, "/remix") {
		relayMode := relayconstant.RelayModeVideoSubmit
		c.Set("relay_mode", relayMode)
		shouldSelectChannel = false
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos") {
		//curl https://api.openai.com/v1/videos \
		//  -H "Authorization: Bearer $OPENAI_API_KEY" \
		//  -F "model=sora-2" \
		//  -F "prompt=A calico cat playing a piano on stage"
		//	-F input_reference="@image.jpg"
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			relayMode = relayconstant.RelayModeVideoSubmit
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			if req != nil {
				modelRequest.Model = req.Model
			}
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/video/generations") {
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			modelRequest.Model = req.Model
			relayMode = relayconstant.RelayModeVideoSubmit
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		if _, ok := c.Get("relay_mode"); !ok {
			c.Set("relay_mode", relayMode)
		}
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
		relayMode := relayconstant.RelayModeGemini
		modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
		if modelName != "" {
			modelRequest.Model = modelName
		}
		c.Set("relay_mode", relayMode)
	} else if !strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") && !strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime") {
		//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
		modelRequest.Model = c.Query("model")
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	}
	// The routing name is also the billing name, so a model named in the body
	// must not replace it: DeepSeek ignores that field.
	if strings.HasPrefix(c.Request.URL.Path, "/v1/tools/search") {
		modelRequest.Model = constant.DeepSeekSearchModel
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/tools/fetch") {
		modelRequest.Model = constant.DeepSeekFetchModel
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
		modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "dall-e")
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits") {
		//modelRequest.Model = common.GetStringIfEmpty(c.PostForm("model"), "gpt-image-1")
		contentType := c.ContentType()
		if slices.Contains([]string{gin.MIMEPOSTForm, gin.MIMEMultipartPOSTForm}, contentType) {
			req, err := getModelFromRequest(c)
			if err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		relayMode := relayconstant.RelayModeAudioSpeech
		if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/speech") {

			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "tts-1")
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranslation
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranscription
		}
		c.Set("relay_mode", relayMode)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
		// playground chat completions
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
		modelRequest.Group = req.Group
		common.SetContextKey(c, constant.ContextKeyTokenGroup, modelRequest.Group)
	}

	return &modelRequest, shouldSelectChannel, nil
}

// TokenModelLimitAllows reports whether a token model-limit map authorizes
// model. Exact name, wildcard-normalized name, and routing-normalized name
// (modifiers and legacy aliases stripped) are all accepted. The Responses
// WebSocket relay shares this rule so both transports admit the same names.
func TokenModelLimitAllows(limit map[string]bool, model string) bool {
	if limit[model] {
		return true
	}
	if formatted := ratio_setting.FormatMatchingModelName(model); limit[formatted] {
		return true
	}
	return limit[ratio_setting.RoutingMatchModelName(model)]
}

// 修复 #4834: GET /v1/video/generations/:task_id && /v1/video/:task_id 此前不解析 model，
// 当 token 启用「可用模型限制」时，下游 modelLimitEnable 校验会因
// modelRequest.Model 为空而误报 "This token has no access to model"。
// 从已存储的任务记录中回填 OriginModelName 即可让校验走在正确的模型上。
func getTaskOriginModelName(c *gin.Context) string {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return ""
	}

	taskId := c.Param("task_id")
	if taskId == "" {
		return ""
	}

	userId := c.GetInt("id")
	if task, exist, err := model.GetByTaskId(userId, taskId); err == nil && exist && task != nil {
		return task.Properties.OriginModelName
	}
	return ""
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) *types.NewAPIError {
	c.Set("original_model", modelName) // for retry
	expectedPlugin := c.GetString("expected_task_plugin_key")
	if channel == nil {
		logTaskPluginChannelDecision(c, nil, modelName, "channel_rejected", "nil_channel")
		return types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if expectedPlugin != "" && !channelMatchesExpectedTaskPlugin(c, channel, expectedPlugin) {
		logTaskPluginChannelDecision(c, channel, modelName, "channel_rejected", "identity_mismatch")
		return types.NewError(
			errors.New("selected channel does not match the pinned task plugin"),
			types.ErrorCodeGetChannelFailed,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if candidate, matched := pinnedEndpointCandidateForChannel(c, channel, expectedPlugin); matched {
		if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
			if pinned, ok := value.(jsplugin.PinnedEndpoint); ok && candidate.Plugin != nil && candidate.Plugin != pinned.Plugin {
				previousPlugin := pinned.Plugin.Meta.Key
				pinned.Plugin = candidate.Plugin
				pinned.Protocol = candidate.Protocol
				pinned.Operation = candidate.Operation
				c.Set(jsplugin.ContextKeyPinnedEndpoint, pinned)
				c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: pinned.Generation, Plugin: candidate.Plugin})
				c.Set("expected_task_plugin_key", candidate.Plugin.Meta.Key)
				c.Set("task_plugin_key", candidate.Plugin.Meta.Key)
				c.Set("platform", candidate.Plugin.Meta.Key)
				logger.LogDebug(
					c,
					"task_plugin subsystem=endpoint event=provider_selected generation=%d previous_plugin=%q plugin=%q model=%q channel_id=%d channel_type=%d",
					pinned.Generation.Number,
					previousPlugin,
					candidate.Plugin.Meta.Key,
					modelName,
					channel.Id,
					channel.Type,
				)
			}
		}
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelName, channel.Name)
	common.SetContextKey(c, constant.ContextKeyChannelTag, channel.GetTag())
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelCreateTime, channel.CreatedTime)
	channelSetting := channel.GetSetting()
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channelSetting)
	// 费用倍率按用户请求一侧的模型名查（和计费同侧），在这里就算好：这时还没有映射，
	// 请求名还是请求名；结束时再查，拿到的是映射后、甚至被流式响应改过的上游名。
	common.SetContextKey(c, constant.ContextKeyChannelCostMultiplier,
		channelSetting.CostMultiplierForModel(model.CostMultiplierModel(channel.GetModels(), modelName)))
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	switch channel.Type {
	case constant.ChannelTypeTaskPlugin:
		c.Set("task_plugin_key", channelSetting.TaskPluginKey)
	case constant.ChannelTypeNewAPI:
		// The bound plugin verified above executes; ordinary requests through
		// the same gateway channel carry no pinned plugin.
		if executing := c.GetString("expected_task_plugin_key"); executing != "" {
			c.Set("task_plugin_key", executing)
		}
	}
	logTaskPluginChannelDecision(c, channel, modelName, "channel_selected", "")
	paramOverride := channel.GetParamOverride()
	headerOverride := channel.GetHeaderOverride()
	if mergedParam, applied := service.ApplyChannelAffinityOverrideTemplate(c, paramOverride); applied {
		paramOverride = mergedParam
	}
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, paramOverride)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
	if nil != channel.OpenAIOrganization && *channel.OpenAIOrganization != "" {
		common.SetContextKey(c, constant.ContextKeyChannelOrganization, *channel.OpenAIOrganization)
	}
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, channel.GetAutoBan())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, channel.GetModelMapping())
	common.SetContextKey(c, constant.ContextKeyChannelCapacityModel, model.ChannelCapacityModel(channel.GetModels(), channel.GetModelMapping(), modelName))
	common.SetContextKey(c, constant.ContextKeyChannelStatusCodeMapping, channel.GetStatusCodeMapping())

	key, index, newAPIError := channel.GetNextEnabledKey()
	if newAPIError != nil {
		return newAPIError
	}
	if channel.ChannelInfo.IsMultiKey {
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	} else {
		// 必须设置为 false，否则在重试到单个 key 的时候会导致日志显示错误
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	}
	// c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, false)

	// TODO: api_version统一
	switch channel.Type {
	case constant.ChannelTypeAzure:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeVertexAi:
		c.Set("region", channel.Other)
	case constant.ChannelTypeXunfei:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeGemini:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeAli:
		c.Set("plugin", channel.Other)
	case constant.ChannelCloudflare:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeMokaAI:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeCoze:
		c.Set("bot_id", channel.Other)
	}
	return nil
}

// extractModelNameFromGeminiPath 从 Gemini API URL 路径中提取模型名
// 输入格式: /v1beta/models/gemini-2.0-flash:generateContent
// 输出: gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 查找 "/models/" 的位置
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}

	// 从 "/models/" 之后开始提取
	startIndex := modelsIndex + len(modelsPrefix)
	if startIndex >= len(path) {
		return ""
	}

	// 查找 ":" 的位置，模型名在 ":" 之前
	colonIndex := strings.Index(path[startIndex:], ":")
	if colonIndex == -1 {
		// 如果没有找到 ":"，返回从 "/models/" 到路径结尾的部分
		return path[startIndex:]
	}

	// 返回模型名部分
	return path[startIndex : startIndex+colonIndex]
}
