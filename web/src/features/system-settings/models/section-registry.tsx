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
import { IoNetDeploymentSettingsSection } from '../integrations/ionet-deployment-settings-section'
import type { ModelSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { ClaudeSettingsCard } from './claude-settings-card'
import { CapabilityGateSection } from './capability-gate-section'
import { ChannelModelCircuitBreakerSection } from './channel-model-circuit-breaker-section'
import { GeminiSettingsCard } from './gemini-settings-card'
import { GlobalSettingsCard } from './global-settings-card'
import { GrokSettingsCard } from './grok-settings-card'

function formatJsonForEditor(value: string, fallback: string) {
  const raw = (value ?? '').toString().trim()
  if (!raw) return fallback
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return fallback
  }
}

const MODELS_SECTIONS = [
  {
    id: 'global',
    titleKey: 'Global Model Configuration',
    build: (settings: ModelSettings) => (
      <GlobalSettingsCard
        defaultValues={{
          global: {
            pass_through_request_enabled:
              settings['global.pass_through_request_enabled'],
            thinking_model_blacklist: formatJsonForEditor(
              settings['global.thinking_model_blacklist'],
              '[]'
            ),
            chat_completions_to_responses_policy: formatJsonForEditor(
              settings['global.chat_completions_to_responses_policy'],
              '{}'
            ),
            gpt_request_policy: {
              fast_policy:
                settings['global.gpt_request_policy.fast_policy'] === 'allow'
                  ? 'allow'
                  : 'disabled',
              reasoning_policy:
                settings['global.gpt_request_policy.reasoning_policy'] ===
                'cap_xhigh'
                  ? settings['global.gpt_request_policy.reasoning_policy']
                  : 'client',
            },
          },
          general_setting: {
            ping_interval_enabled:
              settings['general_setting.ping_interval_enabled'],
            ping_interval_seconds:
              settings['general_setting.ping_interval_seconds'],
          },
        }}
      />
    ),
  },

  {
    id: 'channel-model-circuit-breaker',
    titleKey: 'Channel-model circuit breaker',
    build: (settings: ModelSettings) => (
      <ChannelModelCircuitBreakerSection
        defaultValues={{
          'monitor_setting.channel_model_circuit_breaker_enabled': settings['monitor_setting.channel_model_circuit_breaker_enabled'],
          'monitor_setting.channel_model_excluded_channel_ids': settings['monitor_setting.channel_model_excluded_channel_ids'],
          'monitor_setting.channel_error_window_minutes': settings['monitor_setting.channel_error_window_minutes'],
          'monitor_setting.channel_error_threshold': settings['monitor_setting.channel_error_threshold'],
          'monitor_setting.channel_error_consecutive_threshold': settings['monitor_setting.channel_error_consecutive_threshold'],
          'monitor_setting.channel_error_status_codes': settings['monitor_setting.channel_error_status_codes'],
          'monitor_setting.channel_model_recovery_enabled': settings['monitor_setting.channel_model_recovery_enabled'],
          'monitor_setting.channel_model_recovery_success_threshold': settings['monitor_setting.channel_model_recovery_success_threshold'],
          'monitor_setting.channel_model_recovery_delay_minutes': settings['monitor_setting.channel_model_recovery_delay_minutes'],
          'monitor_setting.channel_model_recovery_interval_minutes': settings['monitor_setting.channel_model_recovery_interval_minutes'],
          'monitor_setting.channel_model_recovery_concurrency': settings['monitor_setting.channel_model_recovery_concurrency'],
          'monitor_setting.channel_model_event_retention_days': settings['monitor_setting.channel_model_event_retention_days'],
          'monitor_setting.channel_model_wecom_bot_enabled': settings['monitor_setting.channel_model_wecom_bot_enabled'],
          'monitor_setting.channel_model_wecom_bot_url': settings['monitor_setting.channel_model_wecom_bot_url'],
          'monitor_setting.channel_model_wecom_bot_webhook_key_configured': settings['monitor_setting.channel_model_wecom_bot_webhook_key_configured'],
          'monitor_setting.channel_model_wecom_bot_contact': settings['monitor_setting.channel_model_wecom_bot_contact'],
        }}
      />
    ),
  },
  {
    id: 'capability-gate',
    titleKey: 'Capability Gate',
    build: (settings: ModelSettings) => (
      <CapabilityGateSection
        defaultValues={{
          mode: settings['capability_gate_setting.mode'] ?? 'shadow',
          max_body_bytes:
            settings['capability_gate_setting.max_body_bytes'] ?? 8388608,
          event_retention_days:
            settings['capability_gate_setting.event_retention_days'] ?? 30,
          record_only_dimensions:
            settings['capability_gate_setting.record_only_dimensions'] ??
            'params,tools.parallel',
          record_passed:
            settings['capability_gate_setting.record_passed'] ?? true,
        }}
      />
    ),
  },
  {
    id: 'gemini',
    titleKey: 'Gemini',
    build: (settings: ModelSettings) => (
      <GeminiSettingsCard
        defaultValues={{
          gemini: {
            safety_settings: settings['gemini.safety_settings'],
            version_settings: settings['gemini.version_settings'],
            supported_imagine_models:
              settings['gemini.supported_imagine_models'],
            thinking_adapter_enabled:
              settings['gemini.thinking_adapter_enabled'],
            thinking_adapter_budget_tokens_percentage:
              settings['gemini.thinking_adapter_budget_tokens_percentage'],
            function_call_thought_signature_enabled:
              settings['gemini.function_call_thought_signature_enabled'],
            remove_function_response_id_enabled:
              settings['gemini.remove_function_response_id_enabled'],
          },
        }}
      />
    ),
  },
  {
    id: 'claude',
    titleKey: 'Claude',
    build: (settings: ModelSettings) => (
      <ClaudeSettingsCard
        defaultValues={{
          claude: {
            model_headers_settings: settings['claude.model_headers_settings'],
            default_max_tokens: settings['claude.default_max_tokens'],
            thinking_adapter_enabled:
              settings['claude.thinking_adapter_enabled'],
            thinking_adapter_budget_tokens_percentage:
              settings['claude.thinking_adapter_budget_tokens_percentage'],
          },
        }}
      />
    ),
  },
  {
    id: 'grok',
    titleKey: 'Grok',
    build: (settings: ModelSettings) => (
      <GrokSettingsCard
        defaultValues={{
          'grok.violation_deduction_enabled':
            settings['grok.violation_deduction_enabled'] ?? true,
          'grok.violation_deduction_amount':
            settings['grok.violation_deduction_amount'] ?? 0.05,
        }}
      />
    ),
  },

  {
    id: 'model-deployment',
    titleKey: 'Model Deployment',
    build: (settings: ModelSettings) => (
      <IoNetDeploymentSettingsSection
        defaultValues={{
          enabled: settings['model_deployment.ionet.enabled'],
          apiKey: settings['model_deployment.ionet.api_key'],
        }}
      />
    ),
  },
] as const

export type ModelSectionId = (typeof MODELS_SECTIONS)[number]['id']

const modelsRegistry = createSectionRegistry<ModelSectionId, ModelSettings>({
  sections: MODELS_SECTIONS,
  defaultSection: 'global',
  basePath: '/system-settings/models',
  urlStyle: 'path',
})

export const MODELS_SECTION_IDS = modelsRegistry.sectionIds
export const MODELS_DEFAULT_SECTION = modelsRegistry.defaultSection
export const getModelsSectionNavItems = modelsRegistry.getSectionNavItems
export const getModelsSectionContent = modelsRegistry.getSectionContent
export const getModelsSectionMeta = modelsRegistry.getSectionMeta
