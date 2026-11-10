/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useQuery } from '@tanstack/react-query'
import type {
  ColumnDef,
  PaginationState,
  Row,
  SortingState,
  VisibilityState,
} from '@tanstack/react-table'
import { ChevronDown, ChevronRight, Gauge } from 'lucide-react'
import { Fragment, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  DataTableRow,
  DataTableView,
  DataTableViewOptions,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'
import { getUsageAnalytics } from '@/features/dashboard/api'
import {
  avgUseTime,
  cacheHitRate,
  getDashboardDurationMinutes,
  getDefaultDays,
  ratePerMinute,
  successRate,
} from '@/features/dashboard/lib'
import type {
  DashboardFilters,
  UsageAnalyticsParams,
  UsageAnalyticsRow,
} from '@/features/dashboard/types'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
  formatUseTime,
} from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  CompactNumberCell,
  LastCalledCell,
  MetricsHint,
  ModelUsageExpansion,
  SuccessRateCell,
} from './model-usage-expansion'

const COLUMN_VISIBILITY_STORAGE_KEY =
  'dashboard:model-metrics:column-visibility'
// Token breakdown columns are opt-in through the "View" menu.
const INITIAL_COLUMN_VISIBILITY: VisibilityState = {
  input_tokens: false,
  completion_tokens: false,
  cache_read_tokens: false,
  cache_write_tokens: false,
}
const INITIAL_SORTING: SortingState = [{ id: 'request_count', desc: true }]
const INITIAL_PAGINATION: PaginationState = { pageIndex: 0, pageSize: 10 }
const EMPTY_ROWS: UsageAnalyticsRow[] = []
const SKELETON_ROW_KEYS = ['first', 'second', 'third', 'fourth'] as const

function getColumnClassName(columnId: string, kind: 'header' | 'cell') {
  if (columnId === 'expand') return 'w-10 px-1'
  if (kind === 'header') return 'text-muted-foreground'
  return 'py-1.5 font-mono'
}

function ModelMetricsSkeleton() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex items-center gap-2 border-b px-4 py-3 sm:px-5'>
        <Skeleton className='size-6 rounded-md' />
        <Skeleton className='h-4 w-36' />
      </div>
      <div className='space-y-3 p-4'>
        {SKELETON_ROW_KEYS.map((key) => (
          <div key={key} className='flex items-center gap-4'>
            <Skeleton className='h-4 flex-1' />
            <Skeleton className='h-4 w-16' />
            <Skeleton className='h-4 w-20' />
            <Skeleton className='h-4 w-20' />
          </div>
        ))}
      </div>
    </div>
  )
}

function useModelMetricsColumns(options: {
  isAdmin: boolean
  durationMinutes: number
}): ColumnDef<UsageAnalyticsRow>[] {
  const { t } = useTranslation()
  const { isAdmin, durationMinutes } = options

  return useMemo(() => {
    const metricColumns: ColumnDef<UsageAnalyticsRow>[] = [
      {
        id: 'model_name',
        accessorFn: (row) => row.model_name ?? '',
        header: t('Model'),
        enableHiding: false,
        cell: ({ row }) => row.original.model_name || t('Unknown model'),
      },
      {
        id: 'request_count',
        accessorKey: 'request_count',
        header: t('Requests'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.request_count} />
        ),
      },
      {
        id: 'quota',
        accessorKey: 'quota',
        header: t('Quota'),
        cell: ({ row }) => formatQuota(row.original.quota),
      },
      {
        id: 'token_used',
        accessorKey: 'token_used',
        header: t('Tokens'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.token_used} />
        ),
      },
      {
        id: 'input_tokens',
        accessorKey: 'input_tokens',
        header: t('Input'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.input_tokens} />
        ),
      },
      {
        id: 'completion_tokens',
        accessorKey: 'completion_tokens',
        header: t('Output'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.completion_tokens} />
        ),
      },
      {
        id: 'cache_read_tokens',
        accessorKey: 'cache_read_tokens',
        header: t('Cache Read'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.cache_read_tokens} />
        ),
      },
      {
        id: 'cache_write_tokens',
        accessorKey: 'cache_write_tokens',
        header: t('Cache Write'),
        cell: ({ row }) => (
          <CompactNumberCell value={row.original.cache_write_tokens} />
        ),
      },
      {
        id: 'cache_hit_rate',
        accessorFn: (row) => cacheHitRate(row) ?? undefined,
        header: t('Cache hit rate'),
        sortUndefined: 'last',
        cell: ({ row }) => formatPercent(cacheHitRate(row.original)),
      },
      {
        id: 'average_rpm',
        accessorFn: (row) => ratePerMinute(row.request_count, durationMinutes),
        header: t('Average RPM'),
        cell: ({ getValue }) => formatNumber(getValue<number>()),
      },
      {
        id: 'average_tpm',
        accessorFn: (row) => ratePerMinute(row.token_used, durationMinutes),
        header: t('Average TPM'),
        cell: ({ getValue }) => formatCompactNumber(getValue<number>()),
      },
      {
        id: 'average_latency',
        accessorFn: (row) => avgUseTime(row) ?? undefined,
        header: t('Average latency'),
        sortUndefined: 'last',
        cell: ({ getValue }) => {
          const seconds = getValue<number | undefined>()
          return seconds === undefined ? '-' : formatUseTime(seconds)
        },
      },
      {
        id: 'success_rate',
        accessorFn: (row) => successRate(row) ?? undefined,
        header: t('Success rate / Failures'),
        sortUndefined: 'last',
        cell: ({ row }) => <SuccessRateCell metrics={row.original} />,
      },
      {
        id: 'last_called_at',
        accessorKey: 'last_called_at',
        header: t('Last Used'),
        cell: ({ row }) => (
          <LastCalledCell timestamp={row.original.last_called_at} />
        ),
      },
    ]
    if (!isAdmin) return metricColumns

    return [
      {
        id: 'expand',
        header: () => null,
        enableSorting: false,
        enableHiding: false,
        cell: ({ row }) => {
          const modelName = row.original.model_name
          if (!modelName) return null
          const expanded = row.getIsExpanded()
          return (
            <Button
              type='button'
              variant='ghost'
              size='icon'
              className='size-7'
              aria-expanded={expanded}
              aria-label={t('Usage details for {{model}}', {
                model: modelName,
              })}
              onClick={() => row.toggleExpanded()}
            >
              {expanded ? (
                <ChevronDown className='size-4' aria-hidden='true' />
              ) : (
                <ChevronRight className='size-4' aria-hidden='true' />
              )}
            </Button>
          )
        },
      },
      ...metricColumns,
      {
        id: 'user_count',
        accessorFn: (row) => row.user_count ?? 0,
        header: t('Users'),
        cell: ({ getValue }) => (
          <CompactNumberCell value={getValue<number>()} />
        ),
      },
    ]
  }, [durationMinutes, isAdmin, t])
}

export function ModelMetricsTable(props: { filters?: DashboardFilters }) {
  const { t } = useTranslation()
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const isAdmin = Boolean(userRole && userRole >= ROLE.ADMIN)
  const filters = props.filters

  const range = useMemo(
    () =>
      computeTimeRange(
        getDefaultDays(filters?.time_granularity),
        filters?.start_timestamp,
        filters?.end_timestamp
      ),
    [filters]
  )
  const durationMinutes = useMemo(
    () => getDashboardDurationMinutes(filters),
    [filters]
  )
  const username = isAdmin ? filters?.username || undefined : undefined
  const params = useMemo<UsageAnalyticsParams>(
    () => ({
      ...range,
      group_by: 'model',
      ...(username && { username }),
    }),
    [range, username]
  )

  const query = useQuery({
    queryKey: [
      'dashboard',
      'usage-analytics',
      isAdmin ? 'admin' : 'self',
      params,
    ],
    queryFn: async () => {
      const res = await getUsageAnalytics(params, isAdmin)
      if (!res.success) {
        throw new Error(res.message || 'Failed to load usage analytics')
      }
      return res.data ?? { summary: null, rows: [] }
    },
    staleTime: 60 * 1000,
    retry: false,
  })
  const rows = query.data?.rows ?? EMPTY_ROWS
  const columns = useModelMetricsColumns({ isAdmin, durationMinutes })

  const { table } = useDataTable({
    data: rows,
    columns,
    initialSorting: INITIAL_SORTING,
    initialColumnVisibility: INITIAL_COLUMN_VISIBILITY,
    columnVisibilityStorageKey: COLUMN_VISIBILITY_STORAGE_KEY,
    initialPagination: INITIAL_PAGINATION,
    getRowId: (row) => row.model_name ?? '',
  })

  if (query.isLoading) {
    return <ModelMetricsSkeleton />
  }

  const renderRow = (row: Row<UsageAnalyticsRow>) => {
    const expanded = isAdmin && row.getIsExpanded()
    return (
      <Fragment key={row.id}>
        <DataTableRow
          row={row}
          // The class also re-renders the memoized row when it is toggled.
          className={cn(expanded && 'border-b-0')}
          getColumnClassName={getColumnClassName}
          cellRenderColumns={columns}
        />
        {expanded && (
          <TableRow>
            <TableCell
              colSpan={table.getVisibleLeafColumns().length}
              className='bg-muted/30 p-0 whitespace-normal'
            >
              <ModelUsageExpansion
                modelName={row.original.model_name ?? ''}
                range={range}
                username={username}
              />
            </TableCell>
          </TableRow>
        )}
      </Fragment>
    )
  }

  return (
    <section className='overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center gap-2 border-b px-4 py-3 sm:px-5'>
        <div className='flex size-7 items-center justify-center rounded-md bg-sky-500/10 text-sky-600 dark:text-sky-400'>
          <Gauge className='size-4' aria-hidden='true' />
        </div>
        <h2 className='text-sm font-semibold'>{t('Model metrics')}</h2>
        <MetricsHint
          lines={[
            t(
              'Failures include interrupted streams and final failures; a request retried across channels counts once.'
            ),
            t(
              'Success rate, cache hit rate and latency only cover requests recorded after this release.'
            ),
          ]}
        />
        {!query.isError && (
          <div className='ml-auto'>
            <DataTableViewOptions table={table} />
          </div>
        )}
      </div>
      {query.isError ? (
        <div
          role='alert'
          className='text-muted-foreground flex flex-wrap items-center justify-center gap-2 px-4 py-8 text-sm'
        >
          <span>{t('Failed to load')}</span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void query.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : (
        <>
          <DataTableView
            table={table}
            containerClassName='rounded-none border-0'
            tableClassName='text-sm [&_tbody>tr]:h-10'
            tableHeaderClassName='[background-color:var(--table-header)]'
            getColumnClassName={getColumnClassName}
            emptyContent={t('No model usage data available')}
            emptyCellClassName='text-muted-foreground h-24 text-center'
            renderRow={renderRow}
          />
          {rows.length > 0 && (
            <div className='border-t px-4 py-2 sm:px-5'>
              <DataTablePagination table={table} />
            </div>
          )}
        </>
      )}
    </section>
  )
}
