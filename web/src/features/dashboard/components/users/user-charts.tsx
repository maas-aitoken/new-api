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
import { useQuery } from '@tanstack/react-query'
import { VChart } from '@visactor/react-vchart'
import { Users, Loader2 } from 'lucide-react'
import { useEffect, useMemo, useState, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useTheme } from '@/context/theme-provider'
import { getUserQuotaDataByUsers } from '@/features/dashboard/api'
import {
  ORG_LEVELS,
  orgFilterParams,
  orgLevelLabel,
  orgPathLabel,
  processUserChartData,
} from '@/features/dashboard/lib'
import type {
  ProcessedUserChartData,
  QuotaDataItem,
  UserChartsDimension,
  UserChartsFilters,
} from '@/features/dashboard/types'
import { requireServerSuccess } from '@/lib/server-error-message'
import { getRollingDateRange } from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

const USER_CHARTS: {
  value: 'rank' | 'trend'
  specKey: keyof ProcessedUserChartData
}[] = [
  { value: 'rank', specKey: 'spec_user_rank' },
  { value: 'trend', specKey: 'spec_user_trend' },
]

const DIMENSIONS: readonly UserChartsDimension[] = ['user', ...ORG_LEVELS]

const TOP_USER_LIMIT_OPTIONS = [5, 10, 20, 50]

interface UserChartsProps {
  filters: UserChartsFilters
  onFiltersChange: (filters: UserChartsFilters) => void
  // Organization dimensions are admin-only.
  showDimensions?: boolean
}

export function UserCharts(props: UserChartsProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const [themeReady, setThemeReady] = useState(false)
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)

  // The selection is owned by the dashboard parent so it persists across
  // sub-section switches; the rolling window is derived from the chosen range.
  const timeGranularity = props.filters.timeGranularity
  const selectedRange = props.filters.selectedRange
  const topUserLimit = props.filters.topUserLimit
  const dimension = props.filters.dimension
  const onFiltersChange = props.onFiltersChange

  const timeRange = useMemo(() => {
    const { start, end } = getRollingDateRange(selectedRange)
    return {
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
    }
  }, [selectedRange])
  const orgParams = useMemo(
    () => orgFilterParams(props.filters.org),
    [props.filters.org]
  )

  const handleTopUserLimitChange = useCallback(
    (limit: number) => {
      onFiltersChange({ ...props.filters, topUserLimit: limit })
    },
    [onFiltersChange, props.filters]
  )

  const handleDimensionChange = useCallback(
    (next: UserChartsDimension) => {
      onFiltersChange({ ...props.filters, dimension: next })
    },
    [onFiltersChange, props.filters]
  )

  const titles = useMemo(() => {
    if (dimension === 'user') {
      return {
        rank: t('User Consumption Ranking'),
        trend: t('User Consumption Trend'),
      }
    }
    const label = orgLevelLabel(dimension, t)
    return {
      rank: t('Consumption ranking by {{dimension}}', { dimension: label }),
      trend: t('Consumption trend by {{dimension}}', { dimension: label }),
    }
  }, [dimension, t])

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }
    updateTheme()
  }, [resolvedTheme])

  const { data: userData, isLoading } = useQuery({
    queryKey: ['dashboard', 'user-quota', timeRange, dimension, orgParams],
    queryFn: async () =>
      requireServerSuccess(
        await getUserQuotaDataByUsers({ ...timeRange, ...orgParams, dimension })
      ),
    select: (res) => (res.success ? res.data : []),
    staleTime: 60_000,
  })

  const chartData = useMemo(
    () =>
      processUserChartData(
        isLoading ? [] : (userData ?? []),
        timeGranularity,
        t,
        topUserLimit,
        {
          getSeriesKey:
            dimension === 'user'
              ? undefined
              : (item: QuotaDataItem) => orgPathLabel(item, dimension, t),
          rankTitle: titles.rank,
          trendTitle: titles.trend,
        }
      ),
    [userData, isLoading, timeGranularity, t, topUserLimit, dimension, titles]
  )

  return (
    <div className='space-y-3'>
      <div className='flex items-center gap-1.5 overflow-x-auto pb-1 sm:gap-2'>
        {props.showDimensions && (
          <Tabs
            value={dimension}
            onValueChange={(value) =>
              handleDimensionChange(value as UserChartsDimension)
            }
            className='shrink-0'
          >
            <TabsList aria-label={t('Chart by')}>
              {DIMENSIONS.map((option) => (
                <TabsTrigger
                  key={option}
                  value={option}
                  className='px-2.5 text-xs'
                >
                  {option === 'user' ? t('User') : orgLevelLabel(option, t)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        )}

        <Tabs
          value={String(topUserLimit)}
          onValueChange={(value) => handleTopUserLimitChange(Number(value))}
          className='shrink-0'
        >
          <TabsList>
            {dimension === 'user' && (
              <span className='text-muted-foreground px-2 text-xs font-medium whitespace-nowrap'>
                {t('Top Users')}
              </span>
            )}
            {TOP_USER_LIMIT_OPTIONS.map((limit) => (
              <TabsTrigger
                key={limit}
                value={String(limit)}
                className='px-2.5 text-xs'
              >
                {t('Top {{count}}', { count: limit })}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        {isLoading && (
          <Loader2 className='text-muted-foreground size-4 animate-spin' />
        )}
      </div>

      <div className='grid gap-3'>
        {USER_CHARTS.map((chart) => {
          const spec = chartData[chart.specKey]

          return (
            <div
              key={chart.value}
              className='overflow-hidden rounded-lg border'
            >
              <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
                <IconBadge tone='info' size='sm'>
                  <Users />
                </IconBadge>
                <h2 className='text-sm font-semibold'>{titles[chart.value]}</h2>
              </div>

              <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
                {isLoading ? (
                  <Skeleton className='h-full w-full' />
                ) : (
                  themeReady &&
                  spec && (
                    <VChart
                      key={`user-${chart.value}-${dimension}-${topUserLimit}-${resolvedTheme}`}
                      spec={{
                        ...spec,
                        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                        background: 'transparent',
                      }}
                      option={VCHART_OPTION}
                    />
                  )
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
