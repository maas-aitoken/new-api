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
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CapabilityGateEventLogs } from '../capability-gate-event-logs'
import type { CapabilityGateEvent } from '../../types'

const event: CapabilityGateEvent = {
  id: 1,
  mode: 'enforce',
  model: 'gpt-test',
  action: 'filtered',
  channels_evaluated: 3,
  rejected_channels: '2',
  rejected_count: 1,
  dimensions: '[{"dimension":"tools.builtin","severity":"hard","required":[]}]',
  created_at: 1_760_000_000,
  request_id: 'req-1',
  user_id: 42,
  token_id: 7,
  using_group: 'vip',
}

function renderLogs(scope?: 'all' | 'self') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <CapabilityGateEventLogs scope={scope} />
    </QueryClientProvider>
  )
}

function mockEvents() {
  return vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [event], total: 1, page: 1, page_size: 20 } },
  })
}

test('the events are listed with the owner facts under the request id', async () => {
  const get = mockEvents()
  renderLogs()

  expect(await screen.findByText('gpt-test')).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/capability-gate/events', {
    params: { p: 1, page_size: 20 },
  })
  expect(screen.getByText(/User/)).toBeVisible()
})

test('self scope asks the /self endpoint and drops the user fact, which is the viewer', async () => {
  const get = mockEvents()
  renderLogs('self')

  expect(await screen.findByText('gpt-test')).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/capability-gate/events/self', {
    params: { p: 1, page_size: 20 },
  })
  expect(screen.queryByText(/User/)).not.toBeInTheDocument()
  expect(screen.getByText(/Token/)).toBeVisible()
})

test('filters are sent only after Search', async () => {
  const user = userEvent.setup()
  const get = mockEvents()
  renderLogs('self')
  await screen.findByText('gpt-test')

  await user.type(screen.getByLabelText('Model'), 'gpt-test')
  await user.click(screen.getByRole('button', { name: 'Search' }))

  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(get).toHaveBeenCalledWith('/api/capability-gate/events/self', {
    params: { p: 1, page_size: 20, model: 'gpt-test' },
  })
})
