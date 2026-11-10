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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { getUsageAnalytics } from '@/features/dashboard/api'
import type {
  UsageAnalyticsParams,
  UsageAnalyticsRow,
  UsageMetrics,
  UsageOrgFilter,
} from '@/features/dashboard/types'

import { UserUsageTable } from '../user-usage-table'

vi.mock('@/features/dashboard/api', () => ({
  getUsageAnalytics: vi.fn(),
}))

type AnalyticsResponse = Awaited<ReturnType<typeof getUsageAnalytics>>

const DAY_SECONDS = 86_400

function metrics(overrides: Partial<UsageMetrics> = {}): UsageMetrics {
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

function userRow(
  userId: number,
  username: string,
  requests: number
): UsageAnalyticsRow {
  return {
    ...metrics({ request_count: requests, quota: requests * 10 }),
    user_id: userId,
    username,
  }
}

function modelRow(username: string, modelName: string): UsageAnalyticsRow {
  return {
    ...metrics({ request_count: 3 }),
    user_id: 1,
    username,
    model_name: modelName,
  }
}

function respondWith(
  responder: (params: UsageAnalyticsParams) => AnalyticsResponse
) {
  vi.mocked(getUsageAnalytics).mockImplementation(async (params) =>
    responder(params)
  )
}

function rowsResponse(rows: UsageAnalyticsRow[]): AnalyticsResponse {
  return { success: true, data: { summary: null, rows } }
}

function callsFor(groupBy: UsageAnalyticsParams['group_by']) {
  return vi
    .mocked(getUsageAnalytics)
    .mock.calls.filter(([params]) => params.group_by === groupBy)
}

function userTableRow(username: string): HTMLElement {
  const row = screen.getByText(username).closest('tr')
  expect(row).not.toBeNull()
  return row as HTMLElement
}

const queryClients: QueryClient[] = []

function renderTable(selectedRange = 7, org?: UsageOrgFilter) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  return render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <UserUsageTable selectedRange={selectedRange} org={org} />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  for (const client of queryClients.splice(0)) client.clear()
  localStorage.clear()
})

describe('UserUsageTable', () => {
  test('while the user rows are loading the table shows skeleton rows', () => {
    vi.mocked(getUsageAnalytics).mockReturnValue(new Promise(() => undefined))

    const { container } = renderTable()

    expect(
      container.querySelectorAll('[data-slot="skeleton"]').length
    ).toBeGreaterThan(0)
    expect(screen.queryByText('No data available')).toBeNull()
  })

  test('a failed request shows the load error in the table body', async () => {
    respondWith(() => ({ success: false, message: 'range too long' }))

    renderTable()

    expect(await screen.findByText('Failed to load')).toBeInTheDocument()
    expect(screen.queryByText('No data available')).toBeNull()
  })

  test('a range without usage shows the empty message', async () => {
    respondWith(() => rowsResponse([]))

    renderTable()

    expect(await screen.findByText('No data available')).toBeInTheDocument()
  })

  test('user rows come from the user grouping over the selected rolling range', async () => {
    respondWith(() =>
      rowsResponse([userRow(1, 'alice', 120), userRow(2, 'bob', 40)])
    )

    renderTable(7)

    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('bob')).toBeInTheDocument()
    const [userCall] = callsFor('user')
    expect(userCall?.[1]).toBe(true)
    const params = userCall?.[0]
    expect(params?.username).toBeUndefined()
    expect((params?.end_timestamp ?? 0) - (params?.start_timestamp ?? 0)).toBe(
      7 * DAY_SECONDS
    )
  })

  test('expanding a user loads its per-model breakdown with one filtered request', async () => {
    const user = userEvent.setup()
    respondWith((params) =>
      params.group_by === 'user_model'
        ? rowsResponse([modelRow(params.username ?? '', 'gpt-4o')])
        : rowsResponse([userRow(1, 'alice', 120), userRow(2, 'bob', 40)])
    )
    renderTable()
    await screen.findByText('alice')
    expect(callsFor('user_model')).toHaveLength(0)

    const toggle = within(userTableRow('alice')).getByRole('button', {
      name: 'Expand',
    })
    await user.click(toggle)

    expect(await screen.findByText('gpt-4o')).toBeInTheDocument()
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const modelCalls = callsFor('user_model')
    expect(modelCalls).toHaveLength(1)
    expect(modelCalls[0]?.[0].username).toBe('alice')
  })

  test('hovering the header hint explains failure counting and tracked coverage', async () => {
    const user = userEvent.setup()
    respondWith(() => rowsResponse([userRow(1, 'alice', 120)]))
    renderTable()
    await screen.findByText('alice')

    await user.hover(screen.getByRole('button', { name: 'Learn more' }))

    expect(
      await screen.findByText(
        'Failures include interrupted streams and final failures; a request retried across channels counts once.'
      )
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        'Success rate, cache hit rate and latency only cover requests recorded after this release.'
      )
    ).toBeInTheDocument()
  })

  test('user rows show their organization levels, a dash when unassigned, and inactive status badges', async () => {
    respondWith(() =>
      rowsResponse([
        {
          ...userRow(1, 'alice', 120),
          user_status: 1,
          business_unit: 'BU-A',
          department: 'Core',
          team: 'Web',
          org_path: 'BU-A/Core/Web',
        },
        {
          ...userRow(2, 'bob', 40),
          user_status: 3,
          business_unit: '',
          department: '',
          team: '',
        },
        { ...userRow(3, 'carol', 10), user_status: 2 },
      ])
    )

    renderTable()

    await screen.findByText('alice')
    const aliceRow = userTableRow('alice')
    expect(within(aliceRow).getByText('BU-A')).toBeInTheDocument()
    expect(within(aliceRow).getByText('Web')).toHaveAttribute(
      'title',
      'BU-A/Core/Web'
    )
    expect(within(aliceRow).queryByText('Resigned')).toBeNull()
    expect(within(aliceRow).queryByText('Disabled')).toBeNull()
    const bobRow = userTableRow('bob')
    expect(within(bobRow).getByText('Resigned')).toBeInTheDocument()
    expect(within(bobRow).getAllByText('—')).toHaveLength(3)
    expect(
      within(userTableRow('carol')).getByText('Disabled')
    ).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: 'Email' })).toBeNull()
    expect(
      screen.getByRole('columnheader', { name: /Business unit/ })
    ).toBeInTheDocument()
  })

  test('an organization filter is sent with the user grouping and drops empty levels', async () => {
    respondWith(() => rowsResponse([userRow(1, 'alice', 120)]))

    renderTable(7, { business_unit: 'BU-A', department: '', team: 'Web' })

    await screen.findByText('alice')
    const params = callsFor('user')[0]?.[0]
    expect(params?.business_unit).toBe('BU-A')
    expect(params?.team).toBe('Web')
    expect(params).not.toHaveProperty('department')
  })

  test('a renamed user with two rows for one user_id renders and expands each row separately', async () => {
    const user = userEvent.setup()
    respondWith((params) =>
      params.group_by === 'user_model'
        ? rowsResponse([
            modelRow(params.username ?? '', `model-of-${params.username}`),
          ])
        : rowsResponse([userRow(5, 'bob', 50), userRow(5, 'bobby', 20)])
    )
    renderTable()
    await screen.findByText('bob')
    expect(screen.getByText('bobby')).toBeInTheDocument()

    await user.click(
      within(userTableRow('bob')).getByRole('button', { name: 'Expand' })
    )

    expect(await screen.findByText('model-of-bob')).toBeInTheDocument()
    expect(
      within(userTableRow('bobby')).getByRole('button', { name: 'Expand' })
    ).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('model-of-bobby')).toBeNull()
    expect(callsFor('user_model').map(([params]) => params.username)).toEqual([
      'bob',
    ])
  })
})
