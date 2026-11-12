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
import type { TFunction } from 'i18next'

import type {
  UsageOrgFilter,
  UsageOrgLevel,
  UsageOrgOption,
  UserChartsDimension,
} from '@/features/dashboard/types'

// Matches users without that level (backend UsageOrgUnassigned).
export const ORG_UNASSIGNED = '__unassigned__'

// Outermost level first.
export const ORG_LEVELS: readonly UsageOrgLevel[] = [
  'business_unit',
  'department',
  'team',
]

type OrgLevelValues = Partial<Record<UsageOrgLevel, string | undefined>>

function levelsOf(dimension: UsageOrgLevel): readonly UsageOrgLevel[] {
  return ORG_LEVELS.slice(0, ORG_LEVELS.indexOf(dimension) + 1)
}

/** Path label down to `dimension`; repeated levels collapse ("BU/Core/Core" -> "BU/Core"). */
export function orgPathLabel(
  values: OrgLevelValues,
  dimension: UsageOrgLevel,
  t: TFunction
): string {
  const parts: string[] = []
  for (const level of levelsOf(dimension)) {
    const value = values[level]?.trim()
    if (value && parts.at(-1) !== value) parts.push(value)
  }
  return parts.length > 0 ? parts.join(' / ') : t('Unassigned')
}

export function orgLevelLabel(level: UsageOrgLevel, t: TFunction): string {
  switch (level) {
    case 'business_unit':
      return t('Business unit')
    case 'department':
      return t('Department')
    case 'team':
      return t('Team')
  }
}

/** Charts the level below the filtered one; a finer dimension is kept. */
export function drillDownDimension(
  dimension: UserChartsDimension,
  org: UsageOrgFilter
): UserChartsDimension {
  let deepest = -1
  ORG_LEVELS.forEach((level, index) => {
    if (org[level]) deepest = index
  })
  const charted =
    dimension === 'user' ? ORG_LEVELS.length : ORG_LEVELS.indexOf(dimension)
  if (deepest < 0 || charted > deepest) return dimension
  return ORG_LEVELS[deepest + 1] ?? 'user'
}

export function orgFilterParams(filter: UsageOrgFilter): UsageOrgFilter {
  const params: UsageOrgFilter = {}
  for (const level of ORG_LEVELS) {
    const value = filter[level]
    if (value) params[level] = value
  }
  return params
}

function matchesLevel(
  option: UsageOrgOption,
  level: UsageOrgLevel,
  value?: string
) {
  if (!value) return true
  if (value === ORG_UNASSIGNED) return option[level] === ''
  return option[level] === value
}

export interface OrgFilterChoice {
  value: string
  label: string
}

/** Values selectable at `level` under the selected outer levels; unassigned last. */
export function orgFilterChoices(
  options: readonly UsageOrgOption[],
  filter: UsageOrgFilter,
  level: UsageOrgLevel,
  t: TFunction
): OrgFilterChoice[] {
  const outer = ORG_LEVELS.slice(0, ORG_LEVELS.indexOf(level))
  const values = new Set<string>()
  let hasUnassigned = false
  for (const option of options) {
    if (!outer.every((l) => matchesLevel(option, l, filter[l]))) continue
    if (option[level] === '') hasUnassigned = true
    else values.add(option[level])
  }
  const choices = [...values]
    .sort((a, b) => a.localeCompare(b))
    .map((value) => ({ value, label: value }))
  if (hasUnassigned) {
    choices.push({ value: ORG_UNASSIGNED, label: t('Unassigned') })
  }
  return choices
}

export function setOrgFilterLevel(
  filter: UsageOrgFilter,
  level: UsageOrgLevel,
  value: string
): UsageOrgFilter {
  const next: UsageOrgFilter = {}
  for (const l of ORG_LEVELS) {
    if (l === level) {
      if (value) next[l] = value
      break
    }
    if (filter[l]) next[l] = filter[l]
  }
  return next
}
