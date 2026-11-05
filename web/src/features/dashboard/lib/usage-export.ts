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
import { t } from 'i18next'

import type {
  UsageAnalyticsGroupBy,
  UsageAnalyticsRow,
} from '@/features/dashboard/types'
import dayjs from '@/lib/dayjs'
import { formatQuota, formatTimestampToDate } from '@/lib/format'

import {
  avgUseTime,
  cacheHitRate,
  ratePerMinute,
  successRate,
} from './analytics'
import { ORG_LEVELS, orgLevelLabel } from './organization'

export type UsageExportGroupBy = Exclude<UsageAnalyticsGroupBy, 'summary'>

export const USAGE_EXPORT_GROUPINGS: readonly UsageExportGroupBy[] = [
  'model',
  'user',
  'user_model',
  'model_channel',
]

// User analytics export.
export const USER_USAGE_EXPORT_GROUPINGS: readonly UsageExportGroupBy[] = [
  'business_unit',
  'department',
  'team',
  'user',
  'user_model',
]

export type UsageExportDatasets = Partial<
  Record<UsageExportGroupBy, UsageAnalyticsRow[]>
>

type UsageExportCell = string | number | null

interface UsageExportColumn {
  header: string
  width: number
  // Excel number format; omitted for text and identifier columns.
  numFmt?: string
}

export interface UsageExportSheet {
  groupBy: UsageExportGroupBy
  name: string
  columns: UsageExportColumn[]
  rows: UsageExportCell[][]
}

interface UsageExportColumnSpec extends UsageExportColumn {
  value: (row: UsageAnalyticsRow) => UsageExportCell
}

const INTEGER_FORMAT = '#,##0'
const DECIMAL_FORMAT = '#,##0.00'

function roundTo2(value: number | null): number | null {
  if (value === null || !Number.isFinite(value)) return null
  return Math.round(value * 100) / 100
}

/** Percentages 0-100, latency in seconds, rates over `minutes`; one sheet per grouping. */
export function buildUsageSheets(
  datasets: UsageExportDatasets,
  minutes: number,
  groupings: readonly UsageExportGroupBy[] = USAGE_EXPORT_GROUPINGS
): UsageExportSheet[] {
  const modelColumn: UsageExportColumnSpec = {
    header: t('Model'),
    width: 32,
    value: (row) => row.model_name || null,
  }
  // Rollup rows name unassigned users; user rows leave them blank.
  const orgColumns = (
    rollup: boolean,
    depth: number
  ): UsageExportColumnSpec[] =>
    ORG_LEVELS.slice(0, depth).map((level) => ({
      header: orgLevelLabel(level, t),
      width: 20,
      value: (row) => row[level] || (rollup ? t('Unassigned') : null),
    }))
  const userCountColumn: UsageExportColumnSpec = {
    header: t('Users'),
    width: 10,
    numFmt: INTEGER_FORMAT,
    value: (row) => row.user_count ?? null,
  }
  const userColumns: UsageExportColumnSpec[] = [
    {
      header: t('User ID'),
      width: 10,
      value: (row) => row.user_id ?? null,
    },
    {
      header: t('Username'),
      width: 20,
      value: (row) => row.username || null,
    },
    {
      header: t('Display Name'),
      width: 20,
      value: (row) => row.display_name || null,
    },
    {
      header: t('Email'),
      width: 28,
      value: (row) => row.email || null,
    },
    ...orgColumns(false, ORG_LEVELS.length),
  ]
  const metricColumns: UsageExportColumnSpec[] = [
    {
      header: t('Requests'),
      width: 12,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.request_count,
    },
    {
      header: t('Amount'),
      width: 14,
      value: (row) => formatQuota(row.quota),
    },
    {
      header: t('Tokens'),
      width: 16,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.token_used,
    },
    {
      header: t('Input Tokens'),
      width: 16,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.input_tokens,
    },
    {
      header: t('Output Tokens'),
      width: 16,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.completion_tokens,
    },
    {
      header: t('Cache Write'),
      width: 14,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.cache_write_tokens,
    },
    {
      header: t('Cache Read'),
      width: 14,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.cache_read_tokens,
    },
    {
      header: `${t('Cache hit rate')} (%)`,
      width: 16,
      numFmt: DECIMAL_FORMAT,
      value: (row) => roundTo2(cacheHitRate(row)),
    },
    {
      header: `${t('Average latency')} (s)`,
      width: 16,
      numFmt: DECIMAL_FORMAT,
      value: (row) => roundTo2(avgUseTime(row)),
    },
    {
      header: `${t('Success rate')} (%)`,
      width: 14,
      numFmt: DECIMAL_FORMAT,
      value: (row) => roundTo2(successRate(row)),
    },
    {
      header: t('Tracked requests'),
      width: 16,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.tracked_count,
    },
    {
      header: t('Stream errors'),
      width: 14,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.stream_error_count,
    },
    {
      // Excludes stream errors, unlike the on-screen failures.
      header: t('Final failures'),
      width: 14,
      numFmt: INTEGER_FORMAT,
      value: (row) => row.failed_count,
    },
    {
      header: t('Average RPM'),
      width: 14,
      numFmt: DECIMAL_FORMAT,
      value: (row) => roundTo2(ratePerMinute(row.request_count, minutes)),
    },
    {
      header: t('Average TPM'),
      width: 14,
      numFmt: DECIMAL_FORMAT,
      value: (row) => roundTo2(ratePerMinute(row.token_used, minutes)),
    },
    {
      header: t('Last called'),
      width: 20,
      value: (row) =>
        row.last_called_at > 0
          ? formatTimestampToDate(row.last_called_at)
          : null,
    },
  ]

  const layouts: Record<
    UsageExportGroupBy,
    { name: string; dimensions: UsageExportColumnSpec[] }
  > = {
    model: {
      name: t('Models'),
      dimensions: [modelColumn, userCountColumn],
    },
    user: { name: t('Users'), dimensions: userColumns },
    user_model: {
      name: t('User x Model'),
      dimensions: [...userColumns, modelColumn],
    },
    model_channel: {
      name: t('Channels'),
      dimensions: [
        {
          header: t('Channel ID'),
          width: 12,
          value: (row) => row.channel_id ?? null,
        },
        {
          header: t('Channel'),
          width: 24,
          value: (row) => row.channel_name || null,
        },
        modelColumn,
      ],
    },
    business_unit: {
      name: t('Business units'),
      dimensions: [...orgColumns(true, 1), userCountColumn],
    },
    department: {
      name: t('Departments'),
      dimensions: [...orgColumns(true, 2), userCountColumn],
    },
    team: {
      name: t('Teams'),
      dimensions: [...orgColumns(true, 3), userCountColumn],
    },
  }

  return groupings.map((groupBy) => {
    const layout = layouts[groupBy]
    const specs = [...layout.dimensions, ...metricColumns]
    return {
      groupBy,
      name: layout.name,
      columns: specs.map((spec) => ({
        header: spec.header,
        width: spec.width,
        numFmt: spec.numFmt,
      })),
      rows: (datasets[groupBy] ?? []).map((row) =>
        specs.map((spec) => spec.value(row))
      ),
    }
  })
}

export async function buildUsageWorkbook(
  sheets: UsageExportSheet[]
): Promise<Uint8Array<ArrayBuffer>> {
  // Keep exceljs out of the initial bundle.
  const ExcelJS = await import('exceljs')
  const workbook = new ExcelJS.Workbook()
  workbook.created = new Date()

  for (const sheet of sheets) {
    const worksheet = workbook.addWorksheet(
      sheet.name.replaceAll(/[\\/?*:[\]]/g, ' ').slice(0, 31),
      { views: [{ state: 'frozen', ySplit: 1 }] }
    )
    worksheet.columns = sheet.columns.map((column) => ({
      header: column.header,
      width: column.width,
      style: column.numFmt ? { numFmt: column.numFmt } : undefined,
    }))
    worksheet.getRow(1).font = { bold: true }
    worksheet.addRows(sheet.rows)
  }

  return new Uint8Array(await workbook.xlsx.writeBuffer())
}

export function usageExportFileName(
  startTimestamp: number,
  endTimestamp: number
): string {
  const start = dayjs(startTimestamp * 1000).format('YYYYMMDD')
  const end = dayjs(endTimestamp * 1000).format('YYYYMMDD')
  return `usage-${start}-${end}.xlsx`
}
