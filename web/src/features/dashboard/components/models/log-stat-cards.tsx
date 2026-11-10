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
import { CircleCheck, DatabaseZap, Users, type LucideIcon } from 'lucide-react'
import { useEffect, useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getUsageAnalytics, getUserQuotaDates } from '@/features/dashboard/api'
import { useModelStatCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import {
  buildQueryParams,
  cacheHitRate,
  calculateDashboardStats,
  getDefaultDays,
  getDashboardDurationMinutes,
  orgFilterParams,
  successRate,
} from '@/features/dashboard/lib'
import type {
  QuotaDataItem,
  DashboardFilters,
  UsageAnalyticsParams,
  UsageOrgFilter,
} from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
} from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { MetricsHint } from './model-usage-expansion'

interface LogStatCardsProps {
  filters?: DashboardFilters
  // Admin-only organization filter.
  orgFilter?: UsageOrgFilter
  onDataUpdate?: (data: QuotaDataItem[], loading: boolean) => void
}

interface StatCellItem {
  key: string
  title: string
  value: string
  fullValue: string
  desc: string
  icon: LucideIcon
  iconTone: IconBadgeTone
  loading: boolean
  error: boolean
  hints?: string[]
}

const MAX_INLINE_STAT_CHARS = 7

function formatStatNumber(value: number, locale: Intl.LocalesArgument) {
  const fullValue = formatNumber(value, locale)
  const displayValue =
    fullValue.length > MAX_INLINE_STAT_CHARS
      ? formatCompactNumber(value, locale)
      : fullValue

  return {
    displayValue,
    fullValue,
  }
}

function StatCell(props: { item: StatCellItem; className?: string }) {
  const titleId = useId()
  const Icon = props.item.icon
  let valueContent
  if (props.item.loading) {
    valueContent = (
      <div className='mt-1 flex flex-col gap-1 sm:mt-2 sm:gap-1.5'>
        <Skeleton className='h-5 w-16 sm:h-7 sm:w-20' />
        <Skeleton className='hidden h-3.5 w-28 md:block' />
      </div>
    )
  } else if (props.item.error) {
    valueContent = (
      <>
        <div className='text-muted-foreground mt-1 font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl sm:leading-normal'>
          --
        </div>
        <div className='text-muted-foreground/40 mt-1 hidden text-xs md:block'>
          {props.item.desc}
        </div>
      </>
    )
  } else {
    valueContent = (
      <>
        <div
          className='text-foreground mt-1 max-w-full truncate font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl sm:leading-normal'
          title={props.item.fullValue}
        >
          {props.item.value}
        </div>
        <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
          {props.item.desc}
        </div>
      </>
    )
  }

  return (
    <div
      role='group'
      aria-labelledby={titleId}
      aria-busy={props.item.loading}
      className={cn(
        'min-w-0 px-2.5 py-1.5 sm:px-3 sm:py-4 xl:px-4',
        props.className
      )}
    >
      <div className='flex min-w-0 items-center gap-1.5 sm:gap-2'>
        <IconBadge
          tone={props.item.iconTone}
          size='stat'
          className='size-4 rounded-sm sm:size-7 sm:rounded-md [&>svg]:size-2.5 sm:[&>svg]:size-3.5'
        >
          <Icon />
        </IconBadge>
        <div
          id={titleId}
          className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs sm:tracking-wider'
        >
          {props.item.title}
        </div>
        {props.item.hints && <MetricsHint lines={props.item.hints} />}
      </div>

      {valueContent}
    </div>
  )
}

function oddLastCellClassName(index: number, count: number) {
  return index === count - 1 && count % 2 !== 0
    ? 'col-span-2 sm:col-span-1'
    : undefined
}

export function LogStatCards(props: LogStatCardsProps) {
  const { t, i18n } = useTranslation()
  const statCardsConfig = useModelStatCardsConfig()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = !!(user?.role && user.role >= 10)
  const [stats, setStats] = useState<ReturnType<
    typeof calculateDashboardStats
  > | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const [timeRangeMinutes, setTimeRangeMinutes] = useState(0)

  const { filters, onDataUpdate } = props
  const orgParams = useMemo(
    () => (isAdmin && props.orgFilter ? orgFilterParams(props.orgFilter) : {}),
    [isAdmin, props.orgFilter]
  )

  useEffect(() => {
    const abortController = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)

    setError(false)
    onDataUpdate?.([], true)

    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    setTimeRangeMinutes(getDashboardDurationMinutes(filters))

    void getUserQuotaDates(
      { ...buildQueryParams(timeRange, filters), ...orgParams },
      isAdmin
    )
      .then((res) => {
        if (abortController.signal.aborted) return
        const data = res?.data || []
        setStats(calculateDashboardStats(data))
        onDataUpdate?.(data, false)
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setStats(null)
        setError(true)
        onDataUpdate?.([], false)
      })
      .finally(() => {
        if (!abortController.signal.aborted) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
    }
  }, [filters, isAdmin, onDataUpdate, orgParams])

  const summaryParams = useMemo<UsageAnalyticsParams>(() => {
    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    return {
      ...timeRange,
      ...orgParams,
      group_by: 'summary',
      ...(isAdmin && filters?.username && { username: filters.username }),
    }
  }, [filters, isAdmin, orgParams])
  const summaryQuery = useQuery({
    queryKey: [
      'dashboard',
      'usage-analytics',
      isAdmin ? 'admin' : 'self',
      summaryParams,
    ],
    queryFn: async () => {
      const res = await getUsageAnalytics(summaryParams, isAdmin)
      if (!res.success) {
        throw new Error(res.message || 'Failed to load usage analytics')
      }
      return res.data ?? { summary: null, rows: [] }
    },
    staleTime: 60 * 1000,
    retry: false,
  })

  const adaptedStats = {
    rpm: stats?.totalCount ?? 0,
    quota: stats?.totalQuota ?? 0,
    tpm: stats?.totalTokens ?? 0,
    cacheRead: stats?.totalCacheReadTokens ?? 0,
    cacheCreation: stats?.totalCacheCreationTokens ?? 0,
  }
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const volumeItems: StatCellItem[] = statCardsConfig.map((config) => {
    const rawValue = config.getValue(adaptedStats, timeRangeMinutes)
    const formatted =
      config.key === 'quota'
        ? {
            displayValue: formatQuota(rawValue),
            fullValue: formatQuota(rawValue),
          }
        : formatStatNumber(rawValue, locale)

    return {
      key: config.key,
      title: config.title,
      value: formatted.displayValue,
      fullValue: formatted.fullValue,
      desc: config.description,
      icon: config.icon,
      iconTone: config.iconTone,
      loading,
      error,
      hints:
        config.key === 'avgTpm'
          ? [
              t(
                'Average TPM counts all tokens including cache reads and writes; the hourly peak TPM chart counts prompt and completion tokens only.'
              ),
            ]
          : undefined,
    }
  })

  const summary = summaryQuery.data?.summary ?? null
  const failuresNote = t(
    'Failures include interrupted streams and final failures; a request retried across channels counts once.'
  )
  const trackedNote = t(
    'Success rate, cache hit rate and latency only cover requests recorded after this release.'
  )
  const successRateValue = formatPercent(summary ? successRate(summary) : null)
  const cacheHitRateValue = formatPercent(
    summary ? cacheHitRate(summary) : null
  )
  const qualityItems: StatCellItem[] = [
    {
      key: 'successRate',
      title: t('Success rate'),
      value: successRateValue,
      fullValue: successRateValue,
      desc: t('Requests completed without errors'),
      icon: CircleCheck,
      iconTone: 'success',
      loading: summaryQuery.isLoading,
      error: summaryQuery.isError,
      hints: [failuresNote, trackedNote],
    },
    {
      key: 'cacheHitRate',
      title: t('Cache hit rate'),
      value: cacheHitRateValue,
      fullValue: cacheHitRateValue,
      desc: t('Cached share of input tokens'),
      icon: DatabaseZap,
      iconTone: 'chart-2',
      loading: summaryQuery.isLoading,
      error: summaryQuery.isError,
      hints: [t('Cache read tokens divided by input tokens.'), trackedNote],
    },
  ]
  if (isAdmin) {
    const activeUsers = summary
      ? formatStatNumber(summary.active_users, locale)
      : { displayValue: '-', fullValue: '-' }
    qualityItems.push({
      key: 'activeUsers',
      title: t('Active users'),
      value: activeUsers.displayValue,
      fullValue: activeUsers.fullValue,
      desc: t('Users with requests in this range'),
      icon: Users,
      iconTone: 'chart-5',
      loading: summaryQuery.isLoading,
      error: summaryQuery.isError,
    })
  }

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='divide-border/60 grid min-w-0 grid-cols-2 divide-x sm:grid-cols-3 lg:grid-cols-7'>
        {volumeItems.map((item, idx) => (
          <StatCell
            key={item.key}
            item={item}
            className={oddLastCellClassName(idx, volumeItems.length)}
          />
        ))}
      </div>
      <div
        className={cn(
          'divide-border/60 border-border/60 grid min-w-0 grid-cols-2 divide-x border-t',
          qualityItems.length === 3 ? 'sm:grid-cols-3' : 'sm:grid-cols-2'
        )}
      >
        {qualityItems.map((item, idx) => (
          <StatCell
            key={item.key}
            item={item}
            className={oddLastCellClassName(idx, qualityItems.length)}
          />
        ))}
      </div>
      {/*<p className='text-muted-foreground border-t px-3 py-2 text-xs'>*/}
      {/*  {t(*/}
      {/*    'Cache usage is recorded only after the upgrade; historical data is not backfilled.'*/}
      {/*  )}*/}
      {/*</p>*/}
    </div>
  )
}
