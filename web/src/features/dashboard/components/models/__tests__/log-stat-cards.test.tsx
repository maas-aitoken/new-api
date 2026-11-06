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
import { afterEach, describe, expect, test, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import type {
  DashboardFilters,
  UsageAnalyticsSummary,
  UsageOrgFilter,
} from '@/features/dashboard/types'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { LogStatCards } from '../log-stat-cards'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { get } }))

const FILTERS: DashboardFilters = {
  start_timestamp: new Date(2025, 6, 1, 0, 0),
  end_timestamp: new Date(2025, 6, 2, 0, 0),
  time_granularity: 'hour',
  username: '',
}

const SUMMARY: UsageAnalyticsSummary = {
  request_count: 120,
  quota: 0,
  token_used: 3000,
  input_tokens: 1000,
  completion_tokens: 2000,
  cache_read_tokens: 400,
  cache_write_tokens: 0,
  use_time_total: 0,
  use_time_count: 0,
  stream_error_count: 3,
  failed_count: 2,
  tracked_count: 98,
  last_called_at: 1_751_400_000,
  active_users: 7,
  model_count: 2,
}

function respondWithSummary(summary: UsageAnalyticsSummary) {
  get.mockImplementation(async (url: string) => {
    if (url.startsWith('/api/data/analytics')) {
      return { data: { success: true, data: { summary, rows: [] } } }
    }
    return { data: { success: true, data: [] } }
  })
}

const queryClients: QueryClient[] = []

function renderCards(role: number, orgFilter?: UsageOrgFilter) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'tester', role })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <LogStatCards filters={FILTERS} orgFilter={orgFilter} />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
  useAuthStore.getState().auth.setUser(null)
  get.mockReset()
})

describe('LogStatCards analytics cards', () => {
  test('shows upstream cache totals alongside scoped quality metrics', async () => {
    get.mockImplementation(async (url: string) => ({
      data: {
        success: true,
        data: url.startsWith('/api/data/analytics')
          ? { summary: SUMMARY, rows: [] }
          : [
              {
                count: 10,
                quota: 0,
                token_used: 1000,
                cache_read_tokens: 320,
                cache_creation_tokens: 180,
              },
            ],
      },
    }))
    renderCards(ROLE.ADMIN)

    expect(
      await within(
        screen.getByRole('group', { name: 'Cache Read' })
      ).findByText('320')
    ).toBeInTheDocument()
    expect(
      within(screen.getByRole('group', { name: 'Cache Write' })).getByText(
        '180'
      )
    ).toBeInTheDocument()
    expect(
      await within(
        screen.getByRole('group', { name: 'Success rate' })
      ).findByText('95%')
    ).toBeInTheDocument()
  })

  test('admin sees success rate, cache hit rate and active users from the summary', async () => {
    respondWithSummary(SUMMARY)
    renderCards(ROLE.ADMIN)

    const successCard = screen.getByRole('group', { name: 'Success rate' })
    expect(await within(successCard).findByText('95%')).toBeInTheDocument()
    const cacheCard = screen.getByRole('group', { name: 'Cache hit rate' })
    expect(within(cacheCard).getByText('40%')).toBeInTheDocument()
    const usersCard = screen.getByRole('group', { name: 'Active users' })
    expect(within(usersCard).getByText('7')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/api/data/analytics', {
      params: expect.objectContaining({ group_by: 'summary' }),
    })
  })

  test('an organization filter scopes both the volume and the summary requests', async () => {
    respondWithSummary(SUMMARY)
    renderCards(ROLE.ADMIN, { business_unit: 'BU-A', department: '' })

    const usersCard = screen.getByRole('group', { name: 'Active users' })
    expect(await within(usersCard).findByText('7')).toBeInTheDocument()
    const paramsOf = (url: string) =>
      get.mock.calls
        .filter(([calledUrl]) => calledUrl === url)
        .map(([, config]) => config.params)
    for (const params of [
      ...paramsOf('/api/data'),
      ...paramsOf('/api/data/analytics'),
    ]) {
      expect(params.business_unit).toBe('BU-A')
      expect(params).not.toHaveProperty('department')
    }
    expect(paramsOf('/api/data')).toHaveLength(1)
  })

  test('regular users never send an organization filter', async () => {
    respondWithSummary(SUMMARY)
    renderCards(ROLE.USER, { business_unit: 'BU-A' })

    const successCard = screen.getByRole('group', { name: 'Success rate' })
    expect(await within(successCard).findByText('95%')).toBeInTheDocument()
    for (const [, config] of get.mock.calls) {
      expect(config.params).not.toHaveProperty('business_unit')
    }
  })

  test('regular users query their own summary and get no active users card', async () => {
    respondWithSummary(SUMMARY)
    renderCards(ROLE.USER)

    const successCard = screen.getByRole('group', { name: 'Success rate' })
    expect(await within(successCard).findByText('95%')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Active users' })).toBeNull()
    expect(get).toHaveBeenCalledWith('/api/data/analytics/self', {
      params: expect.objectContaining({ group_by: 'summary' }),
    })
  })

  test('rates without tracked requests or input tokens show a dash', async () => {
    respondWithSummary({
      ...SUMMARY,
      tracked_count: 0,
      stream_error_count: 0,
      failed_count: 0,
      input_tokens: 0,
      cache_read_tokens: 0,
    })
    renderCards(ROLE.ADMIN)

    const usersCard = screen.getByRole('group', { name: 'Active users' })
    expect(await within(usersCard).findByText('7')).toBeInTheDocument()
    const successCard = screen.getByRole('group', { name: 'Success rate' })
    expect(within(successCard).getByText('-')).toBeInTheDocument()
    const cacheCard = screen.getByRole('group', { name: 'Cache hit rate' })
    expect(within(cacheCard).getByText('-')).toBeInTheDocument()
  })

  test('a failed summary request shows the error placeholder', async () => {
    get.mockImplementation(async (url: string) => {
      if (url.startsWith('/api/data/analytics')) {
        return { data: { success: false, message: 'range too long' } }
      }
      return { data: { success: true, data: [] } }
    })
    renderCards(ROLE.ADMIN)

    const successCard = screen.getByRole('group', { name: 'Success rate' })
    expect(await within(successCard).findByText('--')).toBeInTheDocument()
  })

  test('hovering the average TPM hint explains it counts cache tokens unlike the peak chart', async () => {
    const user = userEvent.setup()
    respondWithSummary(SUMMARY)
    renderCards(ROLE.ADMIN)

    const tpmCard = screen.getByRole('group', { name: 'Average TPM' })
    await user.hover(
      within(tpmCard).getByRole('button', { name: 'Learn more' })
    )

    expect(
      await screen.findByText(
        'Average TPM counts all tokens including cache reads and writes; the hourly peak TPM chart counts prompt and completion tokens only.'
      )
    ).toBeInTheDocument()
  })
})
