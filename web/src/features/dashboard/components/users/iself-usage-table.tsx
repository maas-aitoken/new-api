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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  DataTableView,
  DataTableViewOptions,
  useDataTable,
} from '@/components/data-table'
import { getIselfUsage } from '@/features/dashboard/api'
import { cacheHitRate } from '@/features/dashboard/lib'
import type {
  IselfUsageParams,
  IselfUsageRow,
} from '@/features/dashboard/types'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
  formatTimestampToDate,
} from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

const EMPTY_ROWS: IselfUsageRow[] = []
const INITIAL_SORTING = [{ id: 'quota', desc: true }]
const INITIAL_PAGINATION = { pageIndex: 0, pageSize: 10 }
// Cache token columns are opt-in, as in model metrics.
const INITIAL_COLUMN_VISIBILITY = {
  cache_read_tokens: false,
  cache_write_tokens: false,
}

export function IselfUsageTable(props: { params: IselfUsageParams }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const minutes = Math.max(
    (props.params.end_timestamp - props.params.start_timestamp) / 60,
    1
  )
  const query = useQuery({
    queryKey: [
      'dashboard',
      'iself-usage',
      user?.id,
      user?.email,
      isAdmin,
      props.params,
    ],
    queryFn: async () => {
      const res = await getIselfUsage(props.params, isAdmin)
      if (!res.success) throw new Error(res.message || t('Request failed'))
      return res.data ?? []
    },
    enabled: Boolean(user),
    staleTime: 60_000,
    retry: false,
  })

  const columns = useMemo<ColumnDef<IselfUsageRow, unknown>[]>(
    () => [
      {
        accessorKey: 'email',
        header: t('Email'),
        cell: ({ row }) => (
          <span className='block max-w-72 truncate' title={row.original.email}>
            {row.original.email}
          </span>
        ),
        enableHiding: false,
      },
      {
        id: 'source',
        header: t('Source'),
        cell: () => 'iSelf',
        enableSorting: false,
      },
      { accessorKey: 'model_name', header: t('Model'), enableHiding: false },
      {
        accessorKey: 'request_count',
        header: t('Requests'),
        cell: ({ row }) => formatCompactNumber(row.original.request_count),
      },
      {
        accessorKey: 'quota',
        header: t('Quota'),
        cell: ({ row }) => formatQuota(row.original.quota),
      },
      {
        accessorKey: 'input_tokens',
        header: t('Input'),
        cell: ({ row }) => formatCompactNumber(row.original.input_tokens),
      },
      {
        accessorKey: 'completion_tokens',
        header: t('Output'),
        cell: ({ row }) => formatCompactNumber(row.original.completion_tokens),
      },
      {
        accessorKey: 'cache_read_tokens',
        header: t('Cache Read'),
        cell: ({ row }) => formatCompactNumber(row.original.cache_read_tokens),
      },
      {
        accessorKey: 'cache_write_tokens',
        header: t('Cache Write'),
        cell: ({ row }) => formatCompactNumber(row.original.cache_write_tokens),
      },
      {
        id: 'cache_hit_rate',
        accessorFn: (row) => cacheHitRate(row) ?? undefined,
        header: t('Cache hit rate'),
        sortUndefined: 'last',
        cell: ({ row }) => formatPercent(cacheHitRate(row.original)),
      },
      {
        accessorKey: 'token_used',
        header: t('Tokens'),
        cell: ({ row }) => formatCompactNumber(row.original.token_used),
      },
      {
        id: 'average-rpm',
        header: t('Average RPM'),
        accessorFn: (row) => row.request_count / minutes,
        cell: ({ getValue }) => formatNumber(getValue<number>()),
      },
      {
        id: 'average-tpm',
        header: t('Average TPM'),
        accessorFn: (row) => row.token_used / minutes,
        cell: ({ getValue }) => formatCompactNumber(getValue<number>()),
      },
      {
        accessorKey: 'last_called_at',
        header: t('Last called'),
        cell: ({ row }) => formatTimestampToDate(row.original.last_called_at),
      },
    ],
    [minutes, t]
  )
  const { table } = useDataTable({
    data: query.data ?? EMPTY_ROWS,
    columns,
    getRowId: (row) => `${row.email}:${row.model_name}`,
    initialSorting: INITIAL_SORTING,
    initialPagination: INITIAL_PAGINATION,
    initialColumnVisibility: INITIAL_COLUMN_VISIBILITY,
    columnVisibilityStorageKey: 'dashboard-iself-usage:column-visibility',
  })

  return (
    <section
      role='region'
      id='dashboard-iself-usage'
      aria-labelledby='dashboard-iself-usage-title'
      className='overflow-hidden rounded-lg border'
    >
      <div className='flex flex-wrap items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
        <div className='min-w-0 flex-1'>
          <h2
            id='dashboard-iself-usage-title'
            className='text-sm font-semibold'
          >
            {t('iSelf data')}
          </h2>
          <p className='text-muted-foreground text-xs'>
            {t(
              'iSelf usage by recorded email and model. Calls without an email are excluded.'
            )}
          </p>
        </div>
        <DataTableViewOptions table={table} />
      </div>
      <DataTableView
        table={table}
        isLoading={query.isPending}
        skeletonKeyPrefix='iself-usage-skeleton'
        containerClassName='rounded-none border-0'
        tableClassName='min-w-[1000px]'
        tableHeaderClassName='[background-color:var(--table-header)]'
        emptyContent={
          query.isError
            ? t('Failed to load')
            : t('No iSelf usage in this time range')
        }
        emptyCellClassName='text-muted-foreground h-24 text-center'
      />
      {table.getRowModel().rows.length > 0 && (
        <div className='border-t px-3 py-2 sm:px-5'>
          <DataTablePagination table={table} />
        </div>
      )}
    </section>
  )
}
