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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { getUsageOrgOptions } from '@/features/dashboard/api'
import type { UserChartsFilters } from '@/features/dashboard/types'

import { UserAnalyticsToolbar } from '../user-analytics-toolbar'

vi.mock('@/features/dashboard/api', () => ({
  getUsageOrgOptions: vi.fn(),
}))

const INITIAL: UserChartsFilters = {
  timeGranularity: 'day',
  selectedRange: 7,
  topUserLimit: 10,
  dimension: 'business_unit',
  org: {},
}

const queryClients: QueryClient[] = []

function renderToolbar(showOrgFilter = true) {
  const changes: UserChartsFilters[] = []
  function Fixture() {
    const [filters, setFilters] = useState(INITIAL)
    return (
      <UserAnalyticsToolbar
        filters={filters}
        onFiltersChange={(next) => {
          changes.push(next)
          setFilters(next)
        }}
        showOrgFilter={showOrgFilter}
      />
    )
  }
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <Fixture />
    </QueryClientProvider>
  )
  return changes
}

afterEach(() => {
  for (const client of queryClients.splice(0)) client.clear()
  vi.mocked(getUsageOrgOptions).mockReset()
})

describe('UserAnalyticsToolbar organization filter', () => {
  test('choosing a business unit narrows departments and drills the charts down one level', async () => {
    vi.mocked(getUsageOrgOptions).mockResolvedValue({
      success: true,
      data: [
        { business_unit: 'BU-A', department: 'Core', team: 'Core' },
        { business_unit: 'BU-B', department: 'Web', team: 'Web' },
      ],
    })
    const user = userEvent.setup()
    const changes = renderToolbar()

    await user.click(screen.getByRole('combobox', { name: 'Business unit' }))
    await user.click(await screen.findByRole('option', { name: 'BU-A' }))

    await waitFor(() => expect(changes).toHaveLength(1))
    expect(changes[0].org).toEqual({ business_unit: 'BU-A' })
    expect(changes[0].dimension).toBe('department')

    await user.click(screen.getByRole('combobox', { name: 'Department' }))
    expect(await screen.findByRole('option', { name: 'Core' })).toBeVisible()
    expect(screen.queryByRole('option', { name: 'Web' })).toBeNull()
  })

  test('without the admin filter the toolbar keeps only time controls and loads no options', () => {
    renderToolbar(false)

    expect(screen.queryByRole('combobox')).toBeNull()
    expect(screen.getByRole('tab', { name: '7 Days' })).toBeInTheDocument()
    expect(getUsageOrgOptions).not.toHaveBeenCalled()
  })
})
