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
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  DataTableToolbar,
  StaticDataTable,
  useDataTable,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { cn } from '@/lib/utils'

import {
  EMPTY_ROUTE_LOG_FILTERS,
  ROUTE_LOG_PAGE_SIZE,
  useRouteLogs,
} from './hooks'
import { judgmentLabel, outcomeLabel } from './labels'
import { RouteProcessDrawer } from './route-process-drawer'
import { useRouteLogColumns } from './route-log-columns'
import type { RouteLogFilters, RouteLogItem, RouteLogOutcome } from './types'

const EMPTY_ITEMS: RouteLogItem[] = []

// 筛选值是字符串（空串表示不限），时间范围存成 Unix 秒，与后端的 start_time / end_time 一致。
function dateOf(seconds: string) {
  return seconds === '' ? undefined : new Date(Number(seconds) * 1000)
}

function secondsOf(date?: Date) {
  return date ? String(Math.floor(date.getTime() / 1000)) : ''
}
const JUDGMENTS = [
  'live',
  'live_failed',
  'cache',
  'recent_failure',
  'session',
  'auxiliary',
  'no_instruction',
  'unroutable',
]

/** The routing logs, newest first; a row opens the process of its request. */
export function RouteLogList() {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<RouteLogFilters>(EMPTY_ROUTE_LOG_FILTERS)
  const [filters, setFilters] = useState<RouteLogFilters>(
    EMPTY_ROUTE_LOG_FILTERS
  )
  const [pageIndex, setPageIndex] = useState(0)
  const [viewing, setViewing] = useState('')
  const logsQuery = useRouteLogs(filters, pageIndex)
  const columns = useRouteLogColumns(setViewing)

  const items = logsQuery.data?.items ?? EMPTY_ITEMS
  const { table } = useDataTable({
    data: items,
    columns: [],
    totalCount: logsQuery.data?.total ?? 0,
    manualPagination: true,
    columnFilters: [],
    pagination: { pageIndex, pageSize: ROUTE_LOG_PAGE_SIZE },
    onPaginationChange: (updater) => {
      const next =
        typeof updater === 'function'
          ? updater({ pageIndex, pageSize: ROUTE_LOG_PAGE_SIZE })
          : updater
      setPageIndex(next.pageIndex)
    },
    columnVisibilityStorageKey: false,
    columnSizingStorageKey: false,
  })

  const search = () => {
    setPageIndex(0)
    setFilters({ ...draft, request_id: draft.request_id.trim() })
  }
  const reset = () => {
    setDraft(EMPTY_ROUTE_LOG_FILTERS)
    setFilters(EMPTY_ROUTE_LOG_FILTERS)
    setPageIndex(0)
  }
  const field = (name: keyof RouteLogFilters, label: string) => (
    <Input
      aria-label={label}
      placeholder={label}
      value={draft[name]}
      className='w-full sm:w-[180px]'
      onChange={(event) =>
        setDraft((previous) => ({ ...previous, [name]: event.target.value }))
      }
      onKeyDown={(event) => event.key === 'Enter' && search()}
    />
  )

  return (
    <div className='space-y-3'>
      <div>
        <h4 className='text-sm font-medium'>{t('Routing logs')}</h4>
        <p className='text-muted-foreground mt-0.5 text-xs'>
          {t('How each auto request got to its model, newest first.')}
        </p>
      </div>
      <DataTableToolbar
        table={table}
        hideViewOptions
        hasAdditionalFilters={
          Object.values(draft).some(Boolean) ||
          Object.values(filters).some(Boolean)
        }
        onSearch={search}
        onReset={reset}
        preActions={
          <Button
            type='button'
            variant='outline'
            size='icon'
            aria-label={t('Refresh')}
            aria-busy={logsQuery.isFetching}
            disabled={logsQuery.isFetching}
            onClick={() => void logsQuery.refetch()}
          >
            <RefreshCw
              aria-hidden
              className={cn(logsQuery.isFetching && 'animate-spin')}
            />
          </Button>
        }
        customSearch={
          <>
            <CompactDateTimeRangePicker
              className='sm:w-auto'
              start={dateOf(draft.start_time)}
              end={dateOf(draft.end_time)}
              onChange={({ start, end }) =>
                setDraft((previous) => ({
                  ...previous,
                  start_time: secondsOf(start),
                  end_time: secondsOf(end),
                }))
              }
            />
            {field('request_id', t('Request ID'))}
            {field('user_id', t('User ID'))}
            {field('model', t('Model'))}
            {field('task_type', t('Task type'))}
            <NativeSelect
              aria-label={t('Result')}
              className='max-w-full'
              value={draft.outcome}
              onChange={(event) =>
                setDraft((previous) => ({
                  ...previous,
                  outcome: event.target.value as '' | RouteLogOutcome,
                }))
              }
            >
              <NativeSelectOption value=''>{t('All results')}</NativeSelectOption>
              <NativeSelectOption value='routed'>
                {outcomeLabel(t, 'routed')}
              </NativeSelectOption>
              <NativeSelectOption value='failed'>
                {outcomeLabel(t, 'failed')}
              </NativeSelectOption>
            </NativeSelect>
            <NativeSelect
              aria-label={t('Judgment')}
              className='max-w-full'
              value={draft.judgment}
              onChange={(event) =>
                setDraft((previous) => ({
                  ...previous,
                  judgment: event.target.value,
                }))
              }
            >
              <NativeSelectOption value=''>
                {t('All judgments')}
              </NativeSelectOption>
              {JUDGMENTS.map((judgment) => (
                <NativeSelectOption key={judgment} value={judgment}>
                  {judgmentLabel(t, judgment)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </>
        }
      />
      <div aria-busy={logsQuery.isFetching}>
        {logsQuery.isPending && <Skeleton className='h-24 w-full' />}
        {logsQuery.isError && (
          <ErrorState
            title={t('Failed to load the routing logs')}
            description={logsQuery.error.message}
            onRetry={() => void logsQuery.refetch()}
            className='min-h-40'
          />
        )}
        {logsQuery.isSuccess && items.length === 0 && (
          <EmptyState
            title={t('No routing logs found')}
            className='min-h-32'
            bordered
          />
        )}
        {logsQuery.isSuccess && items.length > 0 && (
          <StaticDataTable
            tableClassName='min-w-[1000px]'
            columns={columns}
            data={items}
            getRowKey={(row) => row.id}
          />
        )}
        <div className='mt-3'>
          <DataTablePagination table={table} compact />
        </div>
      </div>
      <RouteProcessDrawer requestId={viewing} onClose={() => setViewing('')} />
    </div>
  )
}
