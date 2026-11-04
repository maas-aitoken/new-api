package constant

type ContextKey string

const (
	ContextKeyTokenCountMeta  ContextKey = "token_count_meta"
	ContextKeyPromptTokens    ContextKey = "prompt_tokens"
	ContextKeyEstimatedTokens ContextKey = "estimated_tokens"

	ContextKeyOriginalModel    ContextKey = "original_model"
	ContextKeyCapabilityGate   ContextKey = "capability_gate"
	ContextKeyRequestStartTime ContextKey = "request_start_time"

	// 选路阶段共用的输入 token 估算器，见 service.RoutingInputTokens。
	ContextKeyRoutingInputEstimator ContextKey = "routing_input_estimator"

	/* token related keys */
	ContextKeyTokenUnlimited         ContextKey = "token_unlimited_quota"
	ContextKeyTokenKey               ContextKey = "token_key"
	ContextKeyTokenId                ContextKey = "token_id"
	ContextKeyTokenGroup             ContextKey = "token_group"
	ContextKeyOriginTasks            ContextKey = "origin_tasks"
	ContextKeyChannelConstraints     ContextKey = "channel_constraints"
	ContextKeyTokenModelLimitEnabled ContextKey = "token_model_limit_enabled"
	ContextKeyTokenModelLimit        ContextKey = "token_model_limit"
	ContextKeyTokenCrossGroupRetry   ContextKey = "token_cross_group_retry"
	ContextKeyTokenAutoGroups        ContextKey = "token_auto_groups"
	// ContextKeyTokenRoutingPreference holds the token's parsed *dto.RoutingPreference for Scheduler.
	ContextKeyTokenRoutingPreference ContextKey = "token_routing_preference"
	// ContextKeyTokenAutoPreference holds the token's capability/price preference for the "auto" model.
	// It has nothing to do with the Scheduler's routing preference above.
	ContextKeyTokenAutoPreference ContextKey = "token_auto_preference"
	// ContextKeyAutoRouted is true once the "auto" model has been resolved to a concrete model
	// for this request, so the usage log can tell it apart from one that asked for that model.
	ContextKeyAutoRouted ContextKey = "auto_routed"
	ContextKeyTokenScene ContextKey = "token_scene"
	// ContextKeyTokenRPM holds the token's per-minute request limit; 0 means unlimited.
	// TokenRateLimit reads it from the context so it does not repeat the token lookup.
	ContextKeyTokenRPM ContextKey = "token_rpm"

	/* channel related keys */
	ContextKeyChannelId                ContextKey = "channel_id"
	ContextKeyChannelName              ContextKey = "channel_name"
	ContextKeyChannelTag               ContextKey = "channel_tag"
	ContextKeyChannelCreateTime        ContextKey = "channel_create_time"
	ContextKeyChannelBaseUrl           ContextKey = "base_url"
	ContextKeyChannelType              ContextKey = "channel_type"
	ContextKeyChannelSetting           ContextKey = "channel_setting"
	ContextKeyChannelOtherSetting      ContextKey = "channel_other_setting"
	ContextKeyChannelParamOverride     ContextKey = "param_override"
	ContextKeyChannelHeaderOverride    ContextKey = "header_override"
	ContextKeyChannelOrganization      ContextKey = "channel_organization"
	ContextKeyChannelAutoBan           ContextKey = "auto_ban"
	ContextKeyChannelModelMapping      ContextKey = "model_mapping"
	ContextKeyChannelStatusCodeMapping ContextKey = "status_code_mapping"
	ContextKeyChannelIsMultiKey        ContextKey = "channel_is_multi_key"
	ContextKeyChannelMultiKeyIndex     ContextKey = "channel_multi_key_index"
	ContextKeyChannelKey               ContextKey = "channel_key"

	// ContextKeyRelayAttempt stores the in-progress relay attempt telemetry
	// record, so the upstream request layer and the billing layer can annotate
	// the attempt they are part of without threading a parameter through every
	// relay handler signature.
	ContextKeyRelayAttempt ContextKey = "relay_attempt"

	// ContextKeyFinishReason stores the terminal finish/stop reason reported by
	// the upstream model, extracted where the response is already being parsed.
	ContextKeyFinishReason ContextKey = "finish_reason"

	// 调度器的决策结果：candidates 是排好序的渠道，其余是审计用的版本号。
	ContextKeySchedulerCandidates        ContextKey = "scheduler_candidates"
	ContextKeySchedulerDecisionID        ContextKey = "scheduler_decision_id"
	ContextKeySchedulerScoreVersion      ContextKey = "scheduler_score_version"
	ContextKeySchedulerPreferenceVersion ContextKey = "scheduler_preference_version"
	ContextKeySchedulerShadowMatch       ContextKey = "scheduler_shadow_match"
	// 这次尝试上游报的输入/输出 token，计数池结束租约时按它记 TPM。
	ContextKeySchedulerInputTokens   ContextKey = "scheduler_input_tokens"
	ContextKeySchedulerOutputTokens  ContextKey = "scheduler_output_tokens"
	ContextKeySchedulerStreamStarted ContextKey = "scheduler_stream_started"
	ContextKeySchedulerWorkload      ContextKey = "scheduler_workload"
	// ContextKeySchedulerAllowedChannelIDs carries the effective group/model
	// channel scope into the Scheduler request. An empty-but-present slice
	// means the group has no usable channel and must not fall back globally.
	ContextKeySchedulerAllowedChannelIDs ContextKey = "scheduler_allowed_channel_ids"
	// ContextKeySchedulerAffinityChannelID carries the currently bound
	// conversation-affinity channel into an enforced Scheduler request.
	ContextKeySchedulerAffinityChannelID ContextKey = "scheduler_affinity_channel_id"
	// ContextKeySchedulerEmergencyNative marks a request that explicitly fell
	// back to native routing because Scheduler infrastructure was unavailable.
	ContextKeySchedulerEmergencyNative ContextKey = "scheduler_emergency_native"
	ContextKeySchedulerDegradedReason  ContextKey = "scheduler_degraded_reason"
	ContextKeySchedulerFailureReason   ContextKey = "scheduler_failure_reason"
	ContextKeySchedulerEnforcedRequest ContextKey = "scheduler_enforced_request"
	// ContextKeyUpstreamRequestOrigin is the scheme and host of the last upstream
	// request, which differs from the base URL for absolute Advanced Custom routes
	// and for adaptors that build official endpoints themselves.
	ContextKeyUpstreamRequestOrigin ContextKey = "upstream_request_origin"

	ContextKeyAutoGroup           ContextKey = "auto_group"
	ContextKeyAutoGroupIndex      ContextKey = "auto_group_index"
	ContextKeyAutoGroupRetryIndex ContextKey = "auto_group_retry_index"

	/* user related keys */
	ContextKeyUserId      ContextKey = "id"
	ContextKeyUserSetting ContextKey = "user_setting"
	ContextKeyUserQuota   ContextKey = "user_quota"
	ContextKeyUserStatus  ContextKey = "user_status"
	ContextKeyUserEmail   ContextKey = "user_email"
	ContextKeyUserGroup   ContextKey = "user_group"
	ContextKeyUsingGroup  ContextKey = "group"
	ContextKeyUserName    ContextKey = "username"

	// external platform
	ContextKeyIselfEmail ContextKey = "iself_email"

	ContextKeyLocalCountTokens ContextKey = "local_count_tokens"

	ContextKeySystemPromptOverride ContextKey = "system_prompt_override"

	// ContextKeyFileSourcesToCleanup stores file sources that need cleanup when request ends
	ContextKeyFileSourcesToCleanup ContextKey = "file_sources_to_cleanup"

	// ContextKeyAdminRejectReason stores an admin-only reject/block reason extracted from upstream responses.
	// It is not returned to end users, but can be persisted into consume/error logs for debugging.
	ContextKeyAdminRejectReason ContextKey = "admin_reject_reason"

	// ContextKeyLanguage stores the user's language preference for i18n
	ContextKeyLanguage             ContextKey = "language"
	ContextKeyIsStream             ContextKey = "is_stream"
	ContextKeyResponseStreamStatus ContextKey = "response_stream_status"

	// ContextKeyAuditLogged marks that the current request has already recorded
	// a manage/operation audit log inside the handler. When set, the admin-audit
	// fallback in authHelper (finishAdminAudit) skips its record to avoid
	// duplicate entries.
	ContextKeyAuditLogged ContextKey = "audit_logged"

	// ContextKeyTokenAuditParams contains only the API token operation's safe metadata.
	ContextKeyTokenAuditParams ContextKey = "token_audit_params"
	// ContextKeyTokenAuditSucceeded disambiguates token responses that exceed the audit buffer.
	ContextKeyTokenAuditSucceeded ContextKey = "token_audit_succeeded"
)
