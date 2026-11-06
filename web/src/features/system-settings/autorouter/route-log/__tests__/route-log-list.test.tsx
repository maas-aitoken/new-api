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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RouteLogList } from '../route-log-list'
import type { RouteLogItem } from '../types'

function item(id: number, overrides: Partial<RouteLogItem> = {}): RouteLogItem {
  return {
    id,
    created_at: 1_760_000_000 + id,
    request_id: `req-${id}`,
    user_id: 42,
    token_id: 7,
    using_group: 'vip',
    outcome: 'routed',
    judgment: 'live',
    source: 'jev',
    task_type: 'code.build',
    model: `model-${id}`,
    effort: 'high',
    preference: 'balanced',
    jev_latency_ms: 90,
    ...overrides,
  }
}

function mockApi(items: RouteLogItem[], total = items.length) {
  return vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === '/api/auto-route-logs/') {
      return { data: { success: true, data: { items, total, page: 1, page_size: 20 } } }
    }
    return { data: { success: true, data: null } }
  })
}

function listCalls(get: ReturnType<typeof mockApi>) {
  return get.mock.calls
    .filter(([url]) => url === '/api/auto-route-logs/')
    .map(([, config]) => (config as { params: Record<string, unknown> }).params)
}

function renderList() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouteLogList />
    </QueryClientProvider>
  )
}

test('the first page is requested without filters and its rows show the outcome of each request', async () => {
  const get = mockApi([item(1), item(2, { outcome: 'failed', judgment: 'unroutable', model: '' })])
  renderList()

  expect(await screen.findByText('model-1')).toBeVisible()
  expect(listCalls(get)[0]).toEqual({ p: 1, page_size: 20 })
  // The filter's own options carry the same labels, so look inside the rows.
  const [, routedRow, failedRow] = screen.getAllByRole('row')
  expect(within(routedRow).getByText('Routed')).toBeVisible()
  expect(within(failedRow).getByText('Failed')).toBeVisible()
  expect(within(failedRow).getByText('No model available')).toBeVisible()
})

test('filters are sent only after Search, trimmed and without the empty ones, and start again from the first page', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)])
  renderList()
  await screen.findByText('model-1')

  await user.type(screen.getByLabelText('Request ID'), '  req-9  ')
  await user.type(screen.getByLabelText('Model'), 'sonnet')
  expect(listCalls(get)).toHaveLength(1)
  await user.selectOptions(screen.getByLabelText('Result'), 'failed')
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() => expect(listCalls(get)).toHaveLength(2))
  expect(listCalls(get)[1]).toEqual({
    request_id: 'req-9',
    model: 'sonnet',
    outcome: 'failed',
    p: 1,
    page_size: 20,
  })
})

test('Reset clears the filters and asks for the unfiltered list again', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)])
  renderList()
  await screen.findByText('model-1')

  await user.type(screen.getByLabelText('Model'), 'sonnet')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() => expect(listCalls(get)).toHaveLength(2))
  await user.click(screen.getByRole('button', { name: 'Reset' }))

  await waitFor(() => expect(listCalls(get)).toHaveLength(3))
  expect(listCalls(get)[2]).toEqual({ p: 1, page_size: 20 })
  expect(screen.getByLabelText('Model')).toHaveValue('')
})

test('the next page is requested when there are more rows than one page holds', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)], 45)
  renderList()
  await screen.findByText('model-1')

  await user.click(screen.getByRole('button', { name: 'Go to next page' }))

  await waitFor(() => expect(listCalls(get)).toHaveLength(2))
  expect(listCalls(get)[1]).toEqual({ p: 2, page_size: 20 })
})

test('a new search starts from the first page even when the list was on a later one', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)], 45)
  renderList()
  await screen.findByText('model-1')
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() => expect(listCalls(get)).toHaveLength(2))

  await user.type(screen.getByLabelText('Model'), 'sonnet')
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() => expect(listCalls(get)).toHaveLength(3))
  expect(listCalls(get)[2]).toEqual({ model: 'sonnet', p: 1, page_size: 20 })
})

test('View process opens the drawer for that row’s request', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1), item(2)])
  renderList()
  await screen.findByText('model-2')

  await user.click(screen.getAllByRole('button', { name: 'View process' })[1])

  expect(await screen.findByText('Routing process')).toBeVisible()
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/auto-route-logs/req-2'))
})

test('a row without a request id has no process to open', async () => {
  mockApi([item(1, { request_id: '' })])
  renderList()

  expect(await screen.findByRole('button', { name: 'View process' })).toBeDisabled()
})

test('an empty result says there are no routing logs', async () => {
  mockApi([])
  renderList()
  expect(await screen.findByText('No routing logs found')).toBeVisible()
})

test('Refresh asks for the same page with the same filters again and spins until the answer arrives', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)], 45)
  renderList()
  await screen.findByText('model-1')
  await user.type(screen.getByLabelText('Model'), 'sonnet')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() => expect(listCalls(get)).toHaveLength(2))
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() => expect(listCalls(get)).toHaveLength(3))
  await screen.findByText('model-1')

  let answer: (value: { data: unknown }) => void = () => undefined
  get.mockImplementationOnce(() => new Promise((resolve) => (answer = resolve)))
  const refresh = screen.getByRole('button', { name: 'Refresh' })
  await user.click(refresh)

  await waitFor(() => expect(listCalls(get)).toHaveLength(4))
  expect(listCalls(get)[3]).toEqual({ model: 'sonnet', p: 2, page_size: 20 })
  expect(refresh).toHaveAttribute('aria-busy', 'true')
  expect(refresh).toBeDisabled()
  answer({
    data: {
      success: true,
      data: { items: [item(3)], total: 45, page: 2, page_size: 20 },
    },
  })
  expect(await screen.findByText('model-3')).toBeVisible()
  expect(refresh).toHaveAttribute('aria-busy', 'false')
  expect(refresh).toBeEnabled()
})

test('a time range is sent in Unix seconds only after Search, and Reset clears it', async () => {
  const user = userEvent.setup()
  const get = mockApi([item(1)])
  renderList()
  await screen.findByText('model-1')

  await user.click(screen.getByRole('button', { name: 'Date Range' }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-11-06T09:00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-11-06T18:30:59' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(
    screen.getByRole('button', {
      name: '2026-11-06 09:00:00 ~ 2026-11-06 18:30:59',
    })
  ).toBeVisible()
  expect(listCalls(get)).toHaveLength(1)
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() => expect(listCalls(get)).toHaveLength(2))
  expect(listCalls(get)[1]).toEqual({
    start_time: String(new Date(2026, 10, 6, 9, 0, 0).getTime() / 1000),
    end_time: String(new Date(2026, 10, 6, 18, 30, 59).getTime() / 1000),
    p: 1,
    page_size: 20,
  })

  await user.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() => expect(listCalls(get)).toHaveLength(3))
  expect(listCalls(get)[2]).toEqual({ p: 1, page_size: 20 })
  expect(screen.getByRole('button', { name: 'Date Range' })).toBeVisible()
})

test('a failed request shows the error with a retry', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('network down'))
  renderList()

  expect(await screen.findByText('Failed to load the routing logs')).toBeVisible()
  expect(screen.getByText('network down')).toBeVisible()
})
