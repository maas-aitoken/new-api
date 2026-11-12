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
import { Info } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  TruncatedCell,
  staticDataTableClassNames as tableStyles,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getUsageAnalytics } from '@/features/dashboard/api'
import { avgUseTime, cacheHitRate, successRate } from '@/features/dashboard/lib'
import type {
  UsageAnalyticsData,
  UsageAnalyticsParams,
  UsageAnalyticsRow,
  UsageMetrics,
} from '@/features/dashboard/types'
import { getSuccessRateTextClass } from '@/features/performance-metrics/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
  formatTimestampRelative,
  formatTimestampToDate,
  formatUseTime,
} from '@/lib/format'
import { cn } from '@/lib/utils'

type DrilldownTab = 'users' | 'channels'

type DrilldownParams = Omit<UsageAnalyticsParams, 'group_by'>

const EMPTY_ROWS: UsageAnalyticsRow[] = []
const DRILLDOWN_SKELETON_ROWS = ['first', 'second', 'third'] as const

function selectRows(data: UsageAnalyticsData): UsageAnalyticsRow[] {
  return data.rows
}

function formatAvgLatency(row: UsageMetrics): string {
  const seconds = avgUseTime(row)
  return seconds === null ? '-' : formatUseTime(seconds)
}

export function MetricsHint(props: { lines: string[]; className?: string }) {
  const { t } = useTranslation()

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type='button'
            className={cn(
              'text-muted-foreground/60 hover:text-foreground flex size-5 shrink-0 items-center justify-center rounded-md',
              props.className
            )}
            aria-label={t('Learn more')}
          />
        }
      >
        <Info className='size-3.5' aria-hidden='true' />
      </TooltipTrigger>
      <TooltipContent className='max-w-[18rem]'>
        <div className='space-y-1'>
          {props.lines.map((line) => (
            <p key={line}>{line}</p>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

export function CompactNumberCell(props: { value: number }) {
  return (
    <TruncatedCell tooltipContent={formatNumber(props.value)}>
      {formatCompactNumber(props.value)}
    </TruncatedCell>
  )
}

export function LastCalledCell(props: { timestamp: number }) {
  const { i18n } = useTranslation()
  if (!props.timestamp) {
    return <span className='text-muted-foreground'>-</span>
  }

  return (
    <TruncatedCell tooltipContent={formatTimestampToDate(props.timestamp)}>
      {formatTimestampRelative(
        props.timestamp,
        'seconds',
        // Intl requires BCP-47 codes rather than i18next's zhCN/zhTW.
        toIntlLocale(i18n.resolvedLanguage || i18n.language)
      )}
    </TruncatedCell>
  )
}

/** "value / failures"; failures include interrupted streams. */
export function OutcomeCountsCell(props: {
  value: ReactNode
  valueClassName?: string
  succeeded: number
  interrupted: number
  failed: number
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const failures = props.interrupted + props.failed

  return (
    <TruncatedCell
      tooltipContent={t(
        '{{success}} succeeded · {{interrupted}} interrupted · {{failed}} failed',
        {
          success: formatNumber(props.succeeded, locale),
          interrupted: formatNumber(props.interrupted, locale),
          failed: formatNumber(props.failed, locale),
        }
      )}
    >
      <span className='inline-flex items-baseline gap-1 font-mono whitespace-nowrap tabular-nums'>
        <span className={props.valueClassName}>{props.value}</span>
        <span className='text-muted-foreground'>/</span>
        <span
          className={
            failures > 0 ? 'text-destructive' : 'text-muted-foreground'
          }
        >
          {formatCompactNumber(failures, locale)}
        </span>
      </span>
    </TruncatedCell>
  )
}

export function SuccessRateCell(props: {
  metrics: Pick<
    UsageMetrics,
    'tracked_count' | 'stream_error_count' | 'failed_count'
  >
}) {
  const rate = successRate(props.metrics)

  return (
    <OutcomeCountsCell
      value={formatPercent(rate)}
      valueClassName={getSuccessRateTextClass(rate ?? Number.NaN)}
      succeeded={props.metrics.tracked_count - props.metrics.stream_error_count}
      interrupted={props.metrics.stream_error_count}
      failed={props.metrics.failed_count}
    />
  )
}

function useDrilldownRows(params: UsageAnalyticsParams, enabled: boolean) {
  return useQuery({
    queryKey: ['dashboard', 'usage-analytics', 'admin', params],
    queryFn: async () => {
      const res = await getUsageAnalytics(params, true)
      if (!res.success) {
        throw new Error(res.message || 'Failed to load usage analytics')
      }
      return res.data ?? { summary: null, rows: [] }
    },
    select: selectRows,
    enabled,
    staleTime: 60 * 1000,
    retry: false,
  })
}

function DrilldownTable(props: {
  rows: UsageAnalyticsRow[]
  columns: StaticDataTableColumn<UsageAnalyticsRow>[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  getRowKey: (row: UsageAnalyticsRow) => string
}) {
  const { t } = useTranslation()

  if (props.isLoading) {
    return (
      <div className='space-y-2 rounded-md border p-3'>
        {DRILLDOWN_SKELETON_ROWS.map((key) => (
          <Skeleton key={key} className='h-5 w-full' />
        ))}
      </div>
    )
  }

  if (props.isError) {
    return (
      <div
        role='alert'
        className='text-muted-foreground flex flex-wrap items-center justify-center gap-2 rounded-md border px-3 py-4 text-sm'
      >
        <span>{t('Failed to load')}</span>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={props.onRetry}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }

  return (
    <StaticDataTable
      className='bg-background max-h-80 overflow-y-auto'
      tableClassName='text-sm [&_tbody>tr]:h-9'
      headerRowClassName={tableStyles.mutedHeaderRow}
      data={props.rows}
      columns={props.columns}
      getRowKey={props.getRowKey}
      emptyContent={t('No data')}
      emptyClassName='text-muted-foreground'
    />
  )
}

export function ModelUsageExpansion(props: {
  modelName: string
  range: { start_timestamp: number; end_timestamp: number }
  username?: string
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<DrilldownTab>('users')

  const baseParams: DrilldownParams = {
    ...props.range,
    model: props.modelName,
    ...(props.username && { username: props.username }),
  }
  const usersQuery = useDrilldownRows(
    { ...baseParams, group_by: 'user_model' },
    tab === 'users'
  )
  const channelsQuery = useDrilldownRows(
    { ...baseParams, group_by: 'model_channel' },
    tab === 'channels'
  )

  const userColumns = useMemo<StaticDataTableColumn<UsageAnalyticsRow>[]>(
    () => [
      {
        id: 'user',
        header: t('User'),
        className: tableStyles.compactHeaderCell,
        cell: (row) => {
          const username = row.username || `#${row.user_id ?? 0}`
          return row.display_name && row.display_name !== row.username
            ? `${row.display_name} (${username})`
            : username
        },
      },
      {
        id: 'requests',
        header: t('Requests'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.request_count} />,
      },
      {
        id: 'quota',
        header: t('Quota'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => formatQuota(row.quota),
      },
      {
        id: 'tokens',
        header: t('Tokens'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.token_used} />,
      },
      {
        id: 'success-rate',
        header: t('Success rate / Failures'),
        className: tableStyles.compactHeaderCell,
        cell: (row) => <SuccessRateCell metrics={row} />,
      },
      {
        id: 'cache-hit-rate',
        header: t('Cache hit rate'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => formatPercent(cacheHitRate(row)),
      },
      {
        id: 'avg-latency',
        header: t('Average latency'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: formatAvgLatency,
      },
      {
        id: 'last-called',
        header: t('Last Used'),
        className: tableStyles.compactHeaderCell,
        cell: (row) => <LastCalledCell timestamp={row.last_called_at} />,
      },
    ],
    [t]
  )

  const channelColumns = useMemo<StaticDataTableColumn<UsageAnalyticsRow>[]>(
    () => [
      {
        id: 'channel',
        header: t('Channel'),
        className: tableStyles.compactHeaderCell,
        cell: (row) => {
          if (!row.channel_id) return t('Unknown')
          return row.channel_name
            ? `${row.channel_name} (#${row.channel_id})`
            : `#${row.channel_id}`
        },
      },
      {
        id: 'requests',
        header: t('Requests'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.request_count} />,
      },
      {
        id: 'input-tokens',
        header: t('Input'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.input_tokens} />,
      },
      {
        id: 'cache-read-tokens',
        header: t('Cache Read'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.cache_read_tokens} />,
      },
      {
        id: 'cache-write-tokens',
        header: t('Cache Write'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => <CompactNumberCell value={row.cache_write_tokens} />,
      },
      {
        id: 'cache-hit-rate',
        header: t('Cache hit rate'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: (row) => formatPercent(cacheHitRate(row)),
      },
      {
        id: 'avg-latency',
        header: t('Average latency'),
        className: tableStyles.compactHeaderCell,
        cellClassName: 'font-mono',
        cell: formatAvgLatency,
      },
      {
        id: 'success-rate',
        header: t('Success rate / Failures'),
        className: tableStyles.compactHeaderCell,
        cell: (row) => <SuccessRateCell metrics={row} />,
      },
    ],
    [t]
  )

  return (
    <Tabs
      value={tab}
      onValueChange={(value) => setTab(value as DrilldownTab)}
      className='gap-2 p-2 sm:p-3'
    >
      <TabsList>
        <TabsTrigger value='users'>{t('Users')}</TabsTrigger>
        <TabsTrigger value='channels'>{t('Channels')}</TabsTrigger>
      </TabsList>
      <TabsContent value='users'>
        <DrilldownTable
          rows={usersQuery.data ?? EMPTY_ROWS}
          columns={userColumns}
          isLoading={usersQuery.isLoading}
          isError={usersQuery.isError}
          onRetry={() => void usersQuery.refetch()}
          // Renamed users retain separate rows for each username.
          getRowKey={(row) =>
            `${row.user_id ?? 0}:${row.username ?? ''}:${row.model_name ?? ''}`
          }
        />
      </TabsContent>
      <TabsContent value='channels'>
        <DrilldownTable
          rows={channelsQuery.data ?? EMPTY_ROWS}
          columns={channelColumns}
          isLoading={channelsQuery.isLoading}
          isError={channelsQuery.isError}
          onRetry={() => void channelsQuery.refetch()}
          getRowKey={(row) => String(row.channel_id ?? 0)}
        />
      </TabsContent>
    </Tabs>
  )
}
