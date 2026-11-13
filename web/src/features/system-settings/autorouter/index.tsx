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
import {
  AUTOROUTER_DEFAULT_SECTION,
  getAutoRouterSectionContent,
  getAutoRouterSectionMeta,
} from './section-registry.tsx'
import type { AutoRouterSettings } from './types'

const defaultAutoRouterSettings: AutoRouterSettings = {
  'autorouter_setting.enabled': false,
  'autorouter_setting.jev_endpoint': '',
  'autorouter_setting.jev_model': '',
  'autorouter_setting.jev_timeout_ms': 5000,
  'autorouter_setting.max_runes': 2000,
  'autorouter_setting.previous_max_runes': 500,
  'autorouter_setting.cache_ttl_seconds': 7200,
  'autorouter_setting.default_preference': 'balanced',
  'autorouter_setting.cost_weights': '{}',
  'autorouter_setting.capacity_pricing': '{}',
  'autorouter_setting.continuation_task_change': 0.5,
  'autorouter_setting.upgrade_benefit': 0.5,
  'autorouter_setting.default_effort': 'medium',
  'autorouter_setting.difficulty_efforts': ['low', 'medium', 'high', 'xhigh'],
  'autorouter_setting.rank_refresh_enabled': true,
  'autorouter_setting.rank_refresh_interval_hours': 24,
  'autorouter_setting.route_log_mode': 'decisions',
  'autorouter_setting.route_log_retention_days': 7,
}

export function AutoRouterSettingsPage() {
  return (
    <SettingsPage
      routePath='/_authenticated/system-settings/autorouter/$section'
      defaultSettings={defaultAutoRouterSettings}
      defaultSection={AUTOROUTER_DEFAULT_SECTION}
      getSectionContent={getAutoRouterSectionContent}
      getSectionMeta={getAutoRouterSectionMeta}
      loadingMessage='Loading auto routing settings...'
    />
  )
}
