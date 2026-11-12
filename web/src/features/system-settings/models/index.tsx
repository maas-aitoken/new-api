/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { SettingsPage } from '../components/settings-page'
import type { ModelSettings } from '../types'
import {
  MODELS_DEFAULT_SECTION,
  getModelsSectionContent,
  getModelsSectionMeta,
} from './section-registry.tsx'

const defaultModelSettings: ModelSettings = {
  'global.pass_through_request_enabled': false,
  'global.thinking_model_blacklist': '[]',
  'global.chat_completions_to_responses_policy': '{}',
  'global.gpt_request_policy.tags': 'codex2api',
  'global.gpt_request_policy.fast_policy': 'disabled',
  'global.gpt_request_policy.reasoning_policy': 'client',
  'general_setting.ping_interval_enabled': false,
  'general_setting.ping_interval_seconds': 60,
  'gemini.safety_settings': '',
  'gemini.version_settings': '',
  'gemini.supported_imagine_models': '',
  'gemini.thinking_adapter_enabled': false,
  'gemini.thinking_adapter_budget_tokens_percentage': 0.6,
  'gemini.function_call_thought_signature_enabled': true,
  'gemini.remove_function_response_id_enabled': true,
  'claude.model_headers_settings': '',
  'claude.default_max_tokens': '',
  'claude.thinking_adapter_enabled': true,
  'claude.thinking_adapter_budget_tokens_percentage': 0.8,
  'grok.violation_deduction_enabled': true,
  'grok.violation_deduction_amount': 0.05,
  ModelPrice: '',
  ModelRatio: '',
  CacheRatio: '',
  CreateCacheRatio: '',
  CompletionRatio: '',
  ImageRatio: '',
  AudioRatio: '',
  AudioCompletionRatio: '',
  ExposeRatioEnabled: false,
  'billing_setting.billing_mode': '{}',
  'billing_setting.billing_expr': '{}',
  'billing_setting.plugin_billing_expr': '{}',
  'tool_price_setting.prices': '{}',
  TopupGroupRatio: '',
  GroupRatio: '',
  UserUsableGroups: '',
  GroupGroupRatio: '',
  AutoGroups: '',
  MaxTokenAutoGroups: 5,
  DefaultUseAutoGroup: false,
  'group_ratio_setting.group_special_usable_group': '{}',
  RetryTimes: 0,
  ChannelDisableThreshold: '',
  AutomaticDisableChannelEnabled: false,
  AutomaticEnableChannelEnabled: false,
  AutomaticDisableKeywords: '',
  AutomaticDisableStatusCodes: '401',
  AutomaticRetryStatusCodes:
    '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
  'monitor_setting.auto_test_channel_enabled': false,
  'monitor_setting.auto_test_channel_minutes': 10,
  'monitor_setting.channel_test_concurrency': 1,
  'monitor_setting.channel_test_mode': 'scheduled_all',
  'monitor_setting.channel_model_circuit_breaker_enabled': false,
  'monitor_setting.channel_model_excluded_channel_ids': '',
  'monitor_setting.channel_error_window_minutes': 5,
  'monitor_setting.channel_error_threshold': 20,
  'monitor_setting.channel_error_consecutive_threshold': 10,
  'monitor_setting.channel_error_status_codes': '429,503',
  'monitor_setting.channel_model_recovery_enabled': false,
  'monitor_setting.channel_model_recovery_success_threshold': 2,
  'monitor_setting.channel_model_recovery_delay_minutes': 5,
  'monitor_setting.channel_model_recovery_interval_minutes': 1,
  'monitor_setting.channel_model_recovery_concurrency': 4,
  'monitor_setting.channel_model_event_retention_days': 30,
  'monitor_setting.channel_model_wecom_bot_enabled': false,
  'monitor_setting.channel_model_wecom_bot_url':
    'https://alertplus.intsig.net/api/alarm/receive/',
  'monitor_setting.channel_model_wecom_bot_webhook_key_configured': false,
  'monitor_setting.channel_model_wecom_bot_contact': '',
  'capability_gate_setting.mode': 'shadow',
  'capability_gate_setting.max_body_bytes': 8388608,
  'capability_gate_setting.event_retention_days': 30,
  'capability_gate_setting.record_only_dimensions': 'params,tools.parallel',
  'capability_gate_setting.record_passed': false,
  'channel_affinity_setting.enabled': false,
  'channel_affinity_setting.switch_on_success': true,
  'channel_affinity_setting.keep_on_channel_disabled': false,
  'channel_affinity_setting.max_entries': 100000,
  'channel_affinity_setting.default_ttl_seconds': 3600,
  'channel_affinity_setting.rules': '[]',
  'model_deployment.ionet.api_key': '',
  'model_deployment.ionet.enabled': false,
}

export function ModelSettings() {
  return (
    <SettingsPage
      routePath='/_authenticated/system-settings/models/$section'
      defaultSettings={defaultModelSettings}
      defaultSection={MODELS_DEFAULT_SECTION}
      getSectionContent={getModelsSectionContent}
      getSectionMeta={getModelsSectionMeta}
    />
  )
}
