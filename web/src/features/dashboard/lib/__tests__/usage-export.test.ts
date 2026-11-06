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
import { describe, expect, test } from 'vitest'

import type { UsageAnalyticsRow } from '../../types'
import {
  buildUsageSheets,
  buildUsageWorkbook,
  USER_USAGE_EXPORT_GROUPINGS,
  usageExportFileName,
  type UsageExportDatasets,
  type UsageExportSheet,
} from '../usage-export'

function usageRow(overrides: Partial<UsageAnalyticsRow>): UsageAnalyticsRow {
  return {
    request_count: 0,
    quota: 0,
    token_used: 0,
    input_tokens: 0,
    completion_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    use_time_total: 0,
    use_time_count: 0,
    stream_error_count: 0,
    failed_count: 0,
    tracked_count: 0,
    last_called_at: 0,
    ...overrides,
  }
}

function datasets(
  overrides: Partial<UsageExportDatasets> = {}
): UsageExportDatasets {
  return {
    model: [],
    user: [],
    user_model: [],
    model_channel: [],
    ...overrides,
  }
}

function cellByHeader(
  sheet: UsageExportSheet,
  rowIndex: number,
  header: string
) {
  const columnIndex = sheet.columns.findIndex(
    (column) => column.header === header
  )
  expect(columnIndex).toBeGreaterThanOrEqual(0)
  return sheet.rows[rowIndex][columnIndex]
}

describe('buildUsageSheets', () => {
  test('creates one sheet per grouping in a fixed order', () => {
    const sheets = buildUsageSheets(datasets(), 60)

    expect(sheets.map((sheet) => sheet.name)).toEqual([
      'Models',
      'Users',
      'User x Model',
      'Channels',
    ])
  })

  test('leads each sheet with the dimensions of its grouping', () => {
    const sheets = buildUsageSheets(datasets(), 60)
    const userDimensions = [
      'User ID',
      'Username',
      'Display Name',
      'Email',
      'Business unit',
      'Department',
      'Team',
    ]

    expect(
      sheets.map((sheet) =>
        sheet.columns
          .slice(
            0,
            sheet.columns.findIndex((c) => c.header === 'Requests')
          )
          .map((column) => column.header)
      )
    ).toEqual([
      ['Model', 'Users'],
      userDimensions,
      [...userDimensions, 'Model'],
      ['Channel ID', 'Channel', 'Model'],
    ])
  })

  test('user analytics groupings export organization rollups before users', () => {
    const sheets = buildUsageSheets(
      {
        business_unit: [
          usageRow({ business_unit: 'BU-A', user_count: 2, request_count: 9 }),
          usageRow({ business_unit: '', request_count: 1 }),
        ],
        department: [
          usageRow({
            business_unit: 'BU-A',
            department: 'Core',
            user_count: 2,
          }),
        ],
        user: [
          usageRow({
            user_id: 1,
            username: 'alice',
            email: 'alice@example.com',
            business_unit: 'BU-A',
            department: 'Core',
            team: '',
          }),
        ],
      },
      60,
      USER_USAGE_EXPORT_GROUPINGS
    )

    expect(sheets.map((sheet) => sheet.name)).toEqual([
      'Business units',
      'Departments',
      'Teams',
      'Users',
      'User x Model',
    ])
    const [businessUnits, departments, teams, users] = sheets
    expect(businessUnits.columns.slice(0, 3).map((c) => c.header)).toEqual([
      'Business unit',
      'Users',
      'Requests',
    ])
    expect(businessUnits.rows.map((row) => row.slice(0, 3))).toEqual([
      ['BU-A', 2, 9],
      ['Unassigned', null, 1],
    ])
    expect(departments.rows[0].slice(0, 3)).toEqual(['BU-A', 'Core', 2])
    expect(teams.rows).toEqual([])
    expect(cellByHeader(users, 0, 'Email')).toBe('alice@example.com')
    expect(cellByHeader(users, 0, 'Department')).toBe('Core')
    expect(cellByHeader(users, 0, 'Team')).toBeNull()
  })

  test('exports the formatted amount without raw quota in every sheet', () => {
    const row = usageRow({ model_name: 'gpt-4o', quota: 1_234_567 })
    const sheets = buildUsageSheets(
      datasets({
        model: [row],
        user: [row],
        user_model: [row],
        model_channel: [row],
      }),
      60
    )

    for (const sheet of sheets) {
      expect(cellByHeader(sheet, 0, 'Amount')).toBe('$2.47')
      expect(sheet.columns.map((column) => column.header)).not.toContain(
        'Quota'
      )
      expect(sheet.rows[0]).not.toContain(1_234_567)
    }
  })

  test('rounds rates and latency to two decimals', () => {
    const [models] = buildUsageSheets(
      datasets({
        model: [
          usageRow({
            model_name: 'gpt-4o',
            tracked_count: 3,
            stream_error_count: 1,
            input_tokens: 3,
            cache_read_tokens: 1,
            use_time_total: 10,
            use_time_count: 3,
          }),
        ],
      }),
      60
    )

    expect([
      cellByHeader(models, 0, 'Success rate (%)'),
      cellByHeader(models, 0, 'Cache hit rate (%)'),
      cellByHeader(models, 0, 'Average latency (s)'),
    ]).toEqual([66.67, 33.33, 3.33])
  })

  test('leaves rates empty for rows recorded before tracking existed', () => {
    const [models] = buildUsageSheets(
      datasets({
        model: [usageRow({ model_name: 'legacy', request_count: 5 })],
      }),
      60
    )

    expect([
      cellByHeader(models, 0, 'Success rate (%)'),
      cellByHeader(models, 0, 'Cache hit rate (%)'),
      cellByHeader(models, 0, 'Average latency (s)'),
      cellByHeader(models, 0, 'Last called'),
    ]).toEqual([null, null, null, null])
  })

  test('averages RPM and TPM over the export range in minutes', () => {
    const [, users] = buildUsageSheets(
      datasets({
        user: [
          usageRow({ user_id: 7, username: 'alice', request_count: 90 }),
          usageRow({ user_id: 8, username: 'bob', token_used: 1000 }),
        ],
      }),
      60
    )

    expect([
      cellByHeader(users, 0, 'Average RPM'),
      cellByHeader(users, 1, 'Average TPM'),
    ]).toEqual([1.5, 16.67])
  })
})

describe('buildUsageWorkbook', () => {
  test('writes an xlsx file that keeps sheet names, bold headers and numbers', async () => {
    const sheets = buildUsageSheets(
      datasets({
        model_channel: [
          usageRow({
            model_name: 'gpt-4o',
            channel_id: 3,
            channel_name: 'primary',
            request_count: 42,
          }),
        ],
      }),
      60
    )

    const requestsColumn =
      sheets[3].columns.findIndex((column) => column.header === 'Requests') + 1

    const bytes = await buildUsageWorkbook(sheets)
    const ExcelJS = await import('exceljs')
    const workbook = new ExcelJS.Workbook()
    await workbook.xlsx.load(bytes.buffer)
    const channels = workbook.getWorksheet('Channels')

    expect(workbook.worksheets.map((sheet) => sheet.name)).toEqual([
      'Models',
      'Users',
      'User x Model',
      'Channels',
    ])
    expect(channels?.getRow(1).getCell(1).font?.bold).toBe(true)
    expect(channels?.getRow(1).getCell(requestsColumn).value).toBe('Requests')
    expect(channels?.getRow(2).getCell(requestsColumn).value).toBe(42)
  }, 60_000)
})

describe('usageExportFileName', () => {
  test('names the file after the local start and end dates', () => {
    const start = new Date(2026, 0, 5, 8, 30).getTime() / 1000
    const end = new Date(2026, 1, 3, 23, 59).getTime() / 1000

    expect(usageExportFileName(start, end)).toBe('usage-20260105-20260203.xlsx')
  })
})
