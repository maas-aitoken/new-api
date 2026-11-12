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
import { useTranslation } from 'react-i18next'

import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import {
  drillDownDimension,
  getDefaultDays,
  saveGranularity,
} from '@/features/dashboard/lib'
import type {
  UsageOrgFilter as UsageOrgFilterValue,
  UserChartsFilters,
} from '@/features/dashboard/types'
import type { TimeGranularity } from '@/lib/time'

import { UsageOrgFilter } from './usage-org-filter'

interface UserAnalyticsToolbarProps {
  filters: UserChartsFilters
  onFiltersChange: (filters: UserChartsFilters) => void
  // The options endpoint is admin-only.
  showOrgFilter: boolean
}

export function UserAnalyticsToolbar(props: UserAnalyticsToolbarProps) {
  const { t } = useTranslation()

  const handleGranularityChange = (granularity: TimeGranularity) => {
    saveGranularity(granularity)
    props.onFiltersChange({
      ...props.filters,
      timeGranularity: granularity,
      selectedRange: getDefaultDays(granularity),
    })
  }

  const handleOrgChange = (org: UsageOrgFilterValue) => {
    props.onFiltersChange({
      ...props.filters,
      org,
      dimension: drillDownDimension(props.filters.dimension, org),
    })
  }

  return (
    <div className='flex flex-wrap items-center gap-1.5 sm:gap-2'>
      <Tabs
        value={String(props.filters.selectedRange)}
        onValueChange={(value) =>
          props.onFiltersChange({
            ...props.filters,
            selectedRange: Number(value),
          })
        }
        className='shrink-0'
      >
        <TabsList>
          {TIME_RANGE_PRESETS.map((preset) => (
            <TabsTrigger
              key={preset.days}
              value={String(preset.days)}
              className='px-2.5 text-xs'
            >
              {t(preset.label)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      <Tabs
        value={props.filters.timeGranularity}
        onValueChange={(value) =>
          handleGranularityChange(value as TimeGranularity)
        }
        className='shrink-0'
      >
        <TabsList>
          {TIME_GRANULARITY_OPTIONS.map((opt) => (
            <TabsTrigger
              key={opt.value}
              value={opt.value}
              className='px-2.5 text-xs'
            >
              {t(opt.label)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {props.showOrgFilter && (
        <UsageOrgFilter value={props.filters.org} onChange={handleOrgChange} />
      )}
    </div>
  )
}
