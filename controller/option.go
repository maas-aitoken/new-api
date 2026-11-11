package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func isPaymentComplianceOptionKey(key string) bool {
	return strings.HasPrefix(key, "payment_setting.compliance_")
}

func isPositiveOptionValue(value string) bool {
	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil {
		return intValue > 0
	}
	floatValue, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && floatValue > 0
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	channelModelWeComBotWebhookKeyConfigured := false
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		if k == "theme.frontend" || k == "billing_setting.billing_mode" || k == "billing_setting.billing_expr" || k == "ResendKeyConfigured" {
			continue
		}
		value := common.Interface2String(v)
		if k == "monitor_setting.channel_model_wecom_bot_webhook_key" {
			channelModelWeComBotWebhookKeyConfigured = strings.TrimSpace(value) != ""
			continue
		}
		isSensitiveKey := strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") ||
			strings.HasSuffix(k, "private_key")
		if isSensitiveKey {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		if slices.Contains(completionRatioMetaOptionKeys, k) {
			optionValues[k] = value
		}
	}
	options = append(options, &model.Option{
		Key: "ResendKeyConfigured", Value: strconv.FormatBool(strings.TrimSpace(common.OptionMap["ResendApiKey"]) != ""),
	})
	common.OptionMapRWMutex.Unlock()
	options = append(options, &model.Option{Key: "monitor_setting.channel_model_wecom_bot_webhook_key_configured", Value: strconv.FormatBool(channelModelWeComBotWebhookKeyConfigured)})
	// Display the same effective expressions used by pricing and settlement,
	// including built-in defaults absent from persisted administrator options.
	for key, values := range map[string]map[string]string{
		"billing_setting.billing_mode": billing_setting.GetBillingModeCopy(),
		"billing_setting.billing_expr": billing_setting.GetBillingExprCopy(),
	} {
		encoded, err := common.Marshal(values)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		options = append(options, &model.Option{Key: key, Value: string(encoded)})
	}
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func UpdatePasskeyDomains(c *gin.Context) {
	var request struct {
		RPID                *string `json:"rp_id"`
		LegacyRPIDs         *string `json:"legacy_rp_ids"`
		Origins             *string `json:"origins"`
		Preview             bool    `json:"preview"`
		RemovalConfirmation string  `json:"removal_confirmation"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.RPID == nil || request.LegacyRPIDs == nil || request.Origins == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	change, err := model.UpdatePasskeyDomainOptions(map[string]string{
		"passkey.rp_id": *request.RPID, "passkey.legacy_rp_ids": *request.LegacyRPIDs, "passkey.origins": *request.Origins,
	}, request.Preview, request.RemovalConfirmation)
	if err != nil {
		writePasskeyDomainSettingsError(c, err)
		if !request.Preview {
			recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", err)
		}
		return
	}
	if !request.Preview {
		recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", nil)
	}
	common.ApiSuccess(c, change)
}

func writePasskeyDomainSettingsError(c *gin.Context, err error) {
	var removal *model.PasskeyDomainRemovalError
	if errors.As(err, &removal) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Review the affected Passkeys and confirm the domain removal. If the settings or impact have changed, confirmation is required again."), gin.H{
			"code": "PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED",
			"data": removal.Change,
		})
		return
	}
	if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiError(c, err)
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "monitor_setting.channel_model_circuit_breaker_enabled", "monitor_setting.channel_model_recovery_enabled":
		if _, parseErr := strconv.ParseBool(option.Value.(string)); parseErr != nil {
			common.ApiErrorMsg(c, "渠道模型熔断开关必须是布尔值")
			return
		}
	case "monitor_setting.channel_model_excluded_channel_ids":
		_, normalized, parseErr := operation_setting.ParseChannelModelExcludedChannelIDs(option.Value.(string))
		if parseErr != nil {
			common.ApiErrorMsg(c, parseErr.Error())
			return
		}
		option.Value = normalized
	case "monitor_setting.channel_error_status_codes":
		if _, parseErr := operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string)); parseErr != nil {
			common.ApiErrorMsg(c, parseErr.Error())
			return
		}
	case "monitor_setting.channel_model_wecom_bot_enabled":
		enabled, parseErr := strconv.ParseBool(option.Value.(string))
		if parseErr != nil {
			common.ApiErrorMsg(c, "企业微信机器人通知开关必须是布尔值")
			return
		}
		monitor := operation_setting.GetMonitorSetting()
		if enabled && (strings.TrimSpace(monitor.ChannelModelWeComBotURL) == "" || strings.TrimSpace(monitor.ChannelModelWeComBotWebhookKey) == "") {
			common.ApiErrorMsg(c, "启用企业微信机器人通知前必须配置 URL 和 Webhook Key")
			return
		}
	case "monitor_setting.channel_model_wecom_bot_url":
		value := strings.TrimSpace(option.Value.(string))
		parsed, parseErr := url.ParseRequestURI(value)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || len(value) > 2048 {
			common.ApiErrorMsg(c, "企业微信机器人 URL 必须是有效的 HTTP 或 HTTPS 地址")
			return
		}
		option.Value = value
	case "monitor_setting.channel_model_wecom_bot_webhook_key":
		value := strings.TrimSpace(option.Value.(string))
		if value == "" || len(value) > 1024 {
			common.ApiErrorMsg(c, "Webhook Key 不能为空且长度不能超过 1024")
			return
		}
		option.Value = value
	case "monitor_setting.channel_model_wecom_bot_contact":
		value := strings.TrimSpace(option.Value.(string))
		if len(value) > 2048 {
			common.ApiErrorMsg(c, "提醒成员长度不能超过 2048")
			return
		}
		option.Value = value
	case "monitor_setting.channel_error_window_minutes", "monitor_setting.channel_model_recovery_success_threshold", "monitor_setting.channel_model_recovery_interval_minutes", "monitor_setting.channel_model_recovery_concurrency", "monitor_setting.channel_model_event_retention_days":
		value, parseErr := strconv.Atoi(option.Value.(string))
		if parseErr != nil || value < 1 {
			common.ApiErrorMsg(c, "该配置必须是正整数")
			return
		}
	case "monitor_setting.channel_error_threshold", "monitor_setting.channel_error_consecutive_threshold", "monitor_setting.channel_model_recovery_delay_minutes":
		value, parseErr := strconv.Atoi(option.Value.(string))
		if parseErr != nil || value < 0 {
			common.ApiErrorMsg(c, "该配置必须是非负整数")
			return
		}
	case "capability_gate_setting.mode":
		mode := operation_setting.NormalizeCapabilityGateMode(option.Value.(string))
		if mode == "" {
			common.ApiErrorMsg(c, "能力门控模式必须是 off、shadow 或 enforce")
			return
		}
		option.Value = mode
	case "capability_gate_setting.max_body_bytes":
		value, parseErr := strconv.ParseInt(option.Value.(string), 10, 64)
		if parseErr != nil || value < operation_setting.MinCapabilityGateMaxBodyBytes || value > operation_setting.MaxCapabilityGateMaxBodyBytes {
			common.ApiErrorMsg(c, "请求体上限必须在 65536 到 67108864 字节之间")
			return
		}
		option.Value = strconv.FormatInt(value, 10)
	case "capability_gate_setting.record_only_dimensions":
		normalized, parseErr := operation_setting.NormalizeCapabilityGateRecordOnlyDimensions(option.Value.(string))
		if parseErr != nil {
			common.ApiErrorMsg(c, parseErr.Error())
			return
		}
		option.Value = normalized
	case "capability_gate_setting.record_passed":
		if _, parseErr := strconv.ParseBool(option.Value.(string)); parseErr != nil {
			common.ApiErrorMsg(c, "该配置必须是布尔值")
			return
		}
	case "capability_gate_setting.event_retention_days":
		value, parseErr := strconv.Atoi(option.Value.(string))
		if parseErr != nil || value < 1 || value > operation_setting.MaxCapabilityGateEventRetentionDays {
			common.ApiErrorMsg(c, "日志保留天数必须是正整数且不超过 3650")
			return
		}
	case "QuotaForInviter", "QuotaForInvitee":
		if isPositiveOptionValue(option.Value.(string)) && !operation_setting.IsPaymentComplianceConfirmed() {
			common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
			return
		}
	default:
		if isPaymentComplianceOptionKey(option.Key) {
			common.ApiErrorT(c, "Compliance confirmation fields cannot be changed through the general settings API")
			return
		}
	}
	if option.Key == "TaskPublicAddress" && option.Value.(string) != "" {
		if err := service.ValidateTaskArtifactBaseURL(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			common.ApiErrorT(c, "Cannot enable {{provider}}. Enter the {{provider}} Client ID and Client Secret first.", map[string]any{"provider": "GitHub OAuth"})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			common.ApiErrorT(c, "Cannot enable {{provider}}. Enter the {{provider}} Client ID and Client Secret first.", map[string]any{"provider": "Discord OAuth"})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			common.ApiErrorT(c, "Cannot enable {{provider}}. Enter the {{provider}} Client ID and Client Secret first.", map[string]any{"provider": "OIDC"})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			common.ApiErrorT(c, "Cannot enable {{provider}}. Enter the {{provider}} Client ID and Client Secret first.", map[string]any{"provider": "LinuxDO OAuth"})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			common.ApiErrorT(c, "Cannot enable the email domain restriction. Enter the allowed email domains first.")
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			common.ApiErrorT(c, "Cannot enable WeChat login. Enter the WeChat login settings first.")
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			common.ApiErrorT(c, "Cannot enable Turnstile verification. Enter the Turnstile settings first.")
			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && !system_setting.GetTelegramSettings().IsConfigured() {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"code":    "TELEGRAM_OAUTH_NOT_CONFIGURED",
				"message": "Telegram OAuth is not configured or enabled. Please contact your administrator.",
			})
			return
		}
	case "theme.frontend":
		if option.Value != "default" {
			common.ApiErrorT(c, "The Classic frontend has been removed. The theme can only be set to default")
			return
		}
	case "GroupRatio":
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "gemini.safety_settings":
		err = model_setting.ValidateGeminiSafetySettings(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "claude.default_max_tokens":
		err = model_setting.ValidateClaudeDefaultMaxTokens(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case operation_setting.ToolPriceOptionKey:
		err = operation_setting.ValidateToolPricesJSON(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(option.Value.(string))
		if err != nil {
			common.ApiErrorT(c, "Failed to set the image ratio: {{error}}", map[string]any{"error": err.Error()})
			return
		}
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(option.Value.(string))
		if err != nil {
			common.ApiErrorT(c, "Failed to set the audio ratio: {{error}}", map[string]any{"error": err.Error()})
			return
		}
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(option.Value.(string))
		if err != nil {
			common.ApiErrorT(c, "Failed to set the audio completion ratio: {{error}}", map[string]any{"error": err.Error()})
			return
		}
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(option.Value.(string))
		if err != nil {
			common.ApiErrorT(c, "Failed to set the cache creation ratio: {{error}}", map[string]any{"error": err.Error()})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "TokenRequestRateLimitCount":
		count, convErr := strconv.Atoi(fmt.Sprintf("%v", option.Value))
		if convErr != nil {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		if err = setting.CheckTokenRequestRateLimitCount(count); err != nil {
			common.ApiError(c, err)
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "billing_setting.billing_expr":
		expressions := make(map[string]string)
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil {
			common.ApiErrorT(c, "Billing expressions must be a JSON object mapping models to expressions: {{error}}", map[string]any{"error": err.Error()})
			return
		}
		models := make([]string, 0, len(expressions))
		for modelName := range expressions {
			models = append(models, modelName)
		}
		sort.Strings(models)
		storedVariants := billing_setting.GetPluginBillingExprCopy()
		for _, modelName := range models {
			variants := make(map[string]any)
			for key, expression := range storedVariants {
				if plugin, name, ok := billing_setting.SplitPluginBillingExprKey(key); ok && name == modelName {
					variants[plugin] = expression
				}
			}
			err = model.ValidateModelPricing(modelName, model.PricingValues{
				"billing_setting.billing_expr":          expressions[modelName],
				billing_setting.PluginBillingExprOption: variants,
			})
			if err != nil {
				common.ApiErrorT(c, "Invalid billing expression for model {{model}}: {{error}}", map[string]any{"model": modelName, "error": err.Error()})
				return
			}
		}
	case billing_setting.PluginBillingExprOption:
		var expressions map[string]string
		if err = common.UnmarshalJsonStr(option.Value.(string), &expressions); err != nil || expressions == nil {
			common.ApiErrorMsg(c, "plugin billing expressions must be a JSON object")
			return
		}
		for key, expression := range expressions {
			plugin, name, valid := billing_setting.SplitPluginBillingExprKey(key)
			if !valid {
				common.ApiErrorMsg(c, "invalid plugin billing expression key: "+key)
				return
			}
			if err = model.ValidateModelPricing(name, model.PricingValues{
				billing_setting.PluginBillingExprOption: map[string]any{plugin: expression},
			}); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	isCircuitBreakerUpdate := option.Key == "monitor_setting.channel_model_circuit_breaker_enabled"
	enableCircuitBreaker := isCircuitBreakerUpdate && option.Value == "true"
	if enableCircuitBreaker && !operation_setting.IsChannelModelCircuitBreakerEnabled() {
		if err := service.ResetChannelModelCircuitBreakerState("circuit breaker enabled from clean state"); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if model.IsPasskeyDomainOption(option.Key) {
		change, updateErr := model.UpdatePasskeyDomainOptions(map[string]string{option.Key: option.Value.(string)}, false, "")
		if updateErr != nil {
			writePasskeyDomainSettingsError(c, updateErr)
			recordPasskeyDomainAudit(c, change, false, updateErr)
			return
		}
		recordPasskeyDomainAudit(c, change, false, nil)
		common.ApiSuccess(c, change)
		return
	}
	err = model.UpdateOption(option.Key, option.Value.(string))
	if err != nil {
		if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
			writeSecurityOperationError(c, err)
		} else {
			common.ApiError(c, err)
		}
		return
	}
	if isCircuitBreakerUpdate && !enableCircuitBreaker {
		if err := service.ResetChannelModelCircuitBreakerState("circuit breaker disabled"); err != nil {
			common.SysError("failed to reset channel-model circuit breaker state: " + err.Error())
		}
	}
	if option.Key == "monitor_setting.channel_model_excluded_channel_ids" {
		channelIDs, _, parseErr := operation_setting.ParseChannelModelExcludedChannelIDs(option.Value.(string))
		if parseErr == nil {
			if err := service.ResetExcludedChannelModelCircuitBreakerState(channelIDs, "channel excluded from circuit breaker"); err != nil {
				common.SysError("failed to reset excluded channel-model state: " + err.Error())
			}
		}
	}
	// 出于安全考虑只记录被修改的配置项名称，不记录配置值（可能含密钥等敏感信息）。
	recordManageAudit(c, "option.update", map[string]any{
		"key": option.Key,
	})
	response := gin.H{
		"success": true,
		"message": "",
	}
	// Return the canonical value only for the GPT policy settings that need
	// immediate frontend reconciliation. Do not echo arbitrary option values,
	// since some settings contain secrets.
	if strings.HasPrefix(option.Key, "global.gpt_request_policy.") {
		common.OptionMapRWMutex.RLock()
		response["data"] = gin.H{
			"key":   option.Key,
			"value": common.OptionMap[option.Key],
		}
		common.OptionMapRWMutex.RUnlock()
	}
	c.JSON(http.StatusOK, response)
}
