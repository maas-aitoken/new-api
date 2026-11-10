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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { Route as UsageLogsRoute } from '@/routes/_authenticated/usage-logs/$section'

import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

function FilterFixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilter(initialSearch = '') {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/user/self/groups' ? {} : { quota: 0, rpm: 0, tpm: 0 },
    },
  }))
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: FilterFixture,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: [`/usage-logs/common${initialSearch}`],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('combobox', { name: 'Auto routing' })
  const statRequests = () =>
    get.mock.calls
      .map(([url]) => String(url))
      .filter((url) => url.includes('/stat?'))
  return { router, statRequests }
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it.each([
  ['Auto requests only', 'only', 'auto_routed=true'],
  ['Exclude auto requests', 'exclude', 'auto_routed=false'],
])(
  'choosing %s filters the list and the statistics the same way',
  async (label, value, parameter) => {
    const user = userEvent.setup()
    const { router, statRequests } = await renderFilter()

    await user.click(screen.getByRole('combobox', { name: 'Auto routing' }))
    await user.click(screen.getByRole('option', { name: label }))
    await user.click(screen.getByRole('button', { name: 'Search' }))

    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        autoRouted: value,
        page: 1,
      })
    )
    await waitFor(() =>
      expect(statRequests().some((url) => url.includes(parameter))).toBe(true)
    )
  }
)

it('shows the filter from the address and drops it when All requests is chosen', async () => {
  const user = userEvent.setup()
  const { router } = await renderFilter('?autoRouted=only')

  expect(screen.getByRole('combobox', { name: 'Auto routing' })).toHaveTextContent(
    'Auto requests only'
  )

  await user.click(screen.getByRole('combobox', { name: 'Auto routing' }))
  await user.click(screen.getByRole('option', { name: 'All requests' }))
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() =>
    expect(router.state.location.search).not.toHaveProperty('autoRouted')
  )
})

it('Reset clears the auto routing filter', async () => {
  const user = userEvent.setup()
  const { router } = await renderFilter('?autoRouted=exclude')

  await user.click(screen.getByRole('button', { name: 'Reset' }))

  await waitFor(() =>
    expect(router.state.location.search).not.toHaveProperty('autoRouted')
  )
  expect(screen.getByRole('combobox', { name: 'Auto routing' })).toHaveTextContent(
    'All requests'
  )
})

// The tests above run on a pass-through search schema; this one pins the real route's,
// which is what keeps the filter in the address and drops anything else.
it.each([
  ['only', 'only'],
  ['exclude', 'exclude'],
  ['everything', undefined],
  [undefined, undefined],
])('the usage logs route reads autoRouted=%s as %s', (input, expected) => {
  const schema = UsageLogsRoute.options.validateSearch as unknown as {
    parse: (search: unknown) => { autoRouted?: string }
  }

  expect(schema.parse({ autoRouted: input }).autoRouted).toBe(expected)
})
