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
import { createSectionRegistry } from '../utils/section-registry'
import { ConnectionSection } from './connection-section'
import { JudgmentSection } from './judgment-section'
import { PreferenceSection } from './preference-section'
import { RankingSection } from './ranking-section'
import { RouteLogSection } from './route-log/route-log-section'
import type { AutoRouterSettings } from './types'

const AUTOROUTER_SECTIONS = [
  {
    id: 'basics',
    titleKey: 'Basics',
    build: (settings: AutoRouterSettings) => (
      <ConnectionSection settings={settings} />
    ),
  },
  {
    id: 'preference',
    titleKey: 'Preference and cost',
    build: (settings: AutoRouterSettings) => (
      <PreferenceSection settings={settings} />
    ),
  },
  {
    id: 'judgment',
    titleKey: 'Judgment and effort',
    build: (settings: AutoRouterSettings) => (
      <JudgmentSection settings={settings} />
    ),
  },
  {
    id: 'ranking',
    titleKey: 'Leaderboard',
    build: (settings: AutoRouterSettings) => (
      <RankingSection settings={settings} />
    ),
  },
  {
    id: 'route-log',
    titleKey: 'Routing logs',
    build: (settings: AutoRouterSettings) => (
      <RouteLogSection settings={settings} />
    ),
  },
] as const

export type AutoRouterSectionId = (typeof AUTOROUTER_SECTIONS)[number]['id']

const autoRouterRegistry = createSectionRegistry<
  AutoRouterSectionId,
  AutoRouterSettings
>({
  sections: AUTOROUTER_SECTIONS,
  defaultSection: 'basics',
  basePath: '/system-settings/autorouter',
  urlStyle: 'path',
})

export const AUTOROUTER_SECTION_IDS = autoRouterRegistry.sectionIds
export const AUTOROUTER_DEFAULT_SECTION = autoRouterRegistry.defaultSection
export const getAutoRouterSectionNavItems =
  autoRouterRegistry.getSectionNavItems
export const getAutoRouterSectionContent = autoRouterRegistry.getSectionContent
export const getAutoRouterSectionMeta = autoRouterRegistry.getSectionMeta
