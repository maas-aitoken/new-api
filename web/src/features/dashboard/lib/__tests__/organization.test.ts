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
import { describe, expect, test } from 'vitest'

import type { QuotaDataItem, UsageOrgOption } from '../../types'
import { processUserChartData } from '../charts'
import {
  drillDownDimension,
  ORG_UNASSIGNED,
  orgFilterChoices,
  orgFilterParams,
  orgPathLabel,
  setOrgFilterLevel,
} from '../organization'

const t = ((key: string) => key) as unknown as TFunction

const OPTIONS: UsageOrgOption[] = [
  { business_unit: '', department: '', team: '' },
  { business_unit: 'BU-B', department: 'Core', team: 'Web' },
  { business_unit: 'BU-A', department: 'Core', team: 'Core' },
  { business_unit: 'BU-A', department: 'Ops', team: '' },
]

describe('orgPathLabel', () => {
  test.each([
    [
      { business_unit: 'BU-A', department: 'Core', team: 'Web' },
      'team',
      'BU-A / Core / Web',
    ],
    // A team named like its department reads once.
    [
      { business_unit: 'BU-A', department: 'Core', team: 'Core' },
      'team',
      'BU-A / Core',
    ],
    [
      { business_unit: 'BU-A', department: 'Core', team: 'Web' },
      'business_unit',
      'BU-A',
    ],
    [{ business_unit: 'BU-A', department: '', team: '' }, 'department', 'BU-A'],
    [{ business_unit: '', department: '', team: '' }, 'team', 'Unassigned'],
    [{}, 'department', 'Unassigned'],
  ] as const)('%o at %s reads %s', (values, dimension, expected) => {
    expect(orgPathLabel(values, dimension, t)).toBe(expected)
  })
})

describe('organization filter cascade', () => {
  test('business units list every value, sorted, with unassigned last', () => {
    expect(orgFilterChoices(OPTIONS, {}, 'business_unit', t)).toEqual([
      { value: 'BU-A', label: 'BU-A' },
      { value: 'BU-B', label: 'BU-B' },
      { value: ORG_UNASSIGNED, label: 'Unassigned' },
    ])
  })

  test('departments narrow to the selected business unit', () => {
    expect(
      orgFilterChoices(OPTIONS, { business_unit: 'BU-A' }, 'department', t)
    ).toEqual([
      { value: 'Core', label: 'Core' },
      { value: 'Ops', label: 'Ops' },
    ])
  })

  test('teams under a department include its unassigned members', () => {
    expect(
      orgFilterChoices(
        OPTIONS,
        { business_unit: 'BU-A', department: 'Ops' },
        'team',
        t
      )
    ).toEqual([{ value: ORG_UNASSIGNED, label: 'Unassigned' }])
  })

  test('without an outer selection, equal department names collapse into one choice', () => {
    expect(orgFilterChoices(OPTIONS, {}, 'department', t)).toEqual([
      { value: 'Core', label: 'Core' },
      { value: 'Ops', label: 'Ops' },
      { value: ORG_UNASSIGNED, label: 'Unassigned' },
    ])
  })

  test('changing an outer level clears the levels below it', () => {
    const filter = { business_unit: 'BU-A', department: 'Core', team: 'Core' }
    expect(setOrgFilterLevel(filter, 'business_unit', 'BU-B')).toEqual({
      business_unit: 'BU-B',
    })
    expect(setOrgFilterLevel(filter, 'department', '')).toEqual({
      business_unit: 'BU-A',
    })
    expect(setOrgFilterLevel(filter, 'team', 'Web')).toEqual({
      business_unit: 'BU-A',
      department: 'Core',
      team: 'Web',
    })
  })

  test('query params drop empty levels', () => {
    expect(
      orgFilterParams({
        business_unit: 'BU-A',
        department: '',
        team: undefined,
      })
    ).toEqual({ business_unit: 'BU-A' })
  })
})

describe('drillDownDimension', () => {
  test.each([
    ['business_unit', { business_unit: 'BU-A' }, 'department'],
    ['department', { business_unit: 'BU-A', department: 'Core' }, 'team'],
    ['business_unit', { team: 'Web' }, 'user'],
    // A dimension finer than the selection is kept.
    ['team', { business_unit: 'BU-A' }, 'team'],
    ['user', { business_unit: 'BU-A' }, 'user'],
    ['department', {}, 'department'],
  ] as const)('%s with %o charts %s', (dimension, org, expected) => {
    expect(drillDownDimension(dimension, org)).toBe(expected)
  })
})

describe('user charts by organization', () => {
  test('rank and trend roll users up by their organization path', () => {
    const hour = 1_751_328_000
    const data: QuotaDataItem[] = [
      {
        business_unit: 'BU-A',
        department: 'Core',
        created_at: hour,
        quota: 300,
      },
      {
        business_unit: 'BU-B',
        department: 'Core',
        created_at: hour,
        quota: 500,
      },
      {
        business_unit: 'BU-A',
        department: 'Core',
        created_at: hour + 3600,
        quota: 400,
      },
      { business_unit: '', department: '', created_at: hour, quota: 50 },
    ]

    const result = processUserChartData(data, 'hour', t, 10, {
      getSeriesKey: (item) => orgPathLabel(item, 'department', t),
      rankTitle: 'By department',
    })

    const rank: Array<{ User: string; rawQuota: number }> =
      result.spec_user_rank.data[0].values
    // Equal department names under different business units stay apart.
    expect(rank.map((row) => [row.User, row.rawQuota])).toEqual([
      ['BU-A / Core', 700],
      ['BU-B / Core', 500],
      ['Unassigned', 50],
    ])
    expect(result.spec_user_rank.title.text).toBe('By department')
    expect(result.spec_user_trend.title.text).toBe('User Consumption Trend')
    const trend: Array<{ User: string; rawQuota: number }> =
      result.spec_user_trend.data[0].values
    expect(
      trend
        .filter((row) => row.User === 'BU-A / Core')
        .map((row) => row.rawQuota)
    ).toEqual([300, 400])
  })
})
