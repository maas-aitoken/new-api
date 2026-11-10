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

import type { RouteResult } from '@/features/system-settings/autorouter/route-log/types'
import { api } from '@/lib/api'

import type { LogOtherData } from '../../types'
import { AutoRouteDetail } from '../dialogs/auto-route-detail'
import { DetailsDialog } from '../dialogs/details-dialog'

const summary: RouteResult = {
  resolved_model: 'sonnet-high',
  model: 'sonnet',
  source: 'cache',
  preference: 'cost',
  decision: {
    task_type: 'code.build',
    task_type_probability: 0.9,
    difficulty: 3,
  },
}

function renderDetail(requestId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AutoRouteDetail requestId={requestId} summary={summary} />
    </QueryClientProvider>
  )
}

test('the section summarises how the request was routed, in the viewer’s terms', () => {
  renderDetail('req-1')

  expect(screen.getByText('Auto routing')).toBeVisible()
  expect(screen.getByText('sonnet-high')).toBeVisible()
  expect(screen.getByText('code.build')).toBeVisible()
  expect(screen.getByText('Cache')).toBeVisible()
  expect(screen.getByText('Cost first')).toBeVisible()
})

test('nothing is fetched until the routing process is asked for', () => {
  const get = vi.spyOn(api, 'get')
  renderDetail('req-1')

  expect(get).not.toHaveBeenCalled()
  expect(screen.queryByText('Routing process')).not.toBeInTheDocument()
})

test('the button opens the drawer with the log of this request and falls back to the summary', async () => {
  const user = userEvent.setup()
  const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: null } })
  renderDetail('req-1')

  await user.click(screen.getByRole('button', { name: 'View routing process' }))

  expect(await screen.findByText('Routing process')).toBeVisible()
  await waitFor(() => expect(get).toHaveBeenCalledWith('/api/auto-route-logs/req-1'))
  expect(
    await screen.findByText(/No detailed routing log was recorded for this request/)
  ).toBeVisible()
})

test('a log without a request id has no way into the process', () => {
  renderDetail('')

  expect(
    screen.queryByRole('button', { name: 'View routing process' })
  ).not.toBeInTheDocument()
})

function renderDialog(isAdmin: boolean, other: LogOtherData) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60_000 })
  render(
    <QueryClientProvider client={client}>
      <DetailsDialog
        log={{
          id: 1,
          user_id: 1,
          created_at: 1,
          type: 2,
          content: '',
          username: 'user',
          token_name: 'token',
          model_name: 'sonnet-high',
          quota: 0,
          prompt_tokens: 0,
          completion_tokens: 0,
          use_time: 0,
          is_stream: false,
          fast_mode: false,
          auto_routed: true,
          iself_email: '',
          channel: 1,
          channel_name: 'channel',
          token_id: 1,
          group: 'default',
          ip: '',
          other: JSON.stringify(other),
          request_id: 'req-1',
          upstream_request_id: '',
        }}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

test('the log details show an admin how an auto request was routed', () => {
  renderDialog(true, { admin_info: { auto_router: summary } })

  expect(screen.getByText('Auto routing')).toBeVisible()
  expect(screen.getByRole('button', { name: 'View routing process' })).toBeVisible()
})

test('a user who is not an admin never sees how the request was routed', () => {
  renderDialog(false, { admin_info: { auto_router: summary } })

  expect(screen.queryByText('Auto routing')).not.toBeInTheDocument()
  expect(screen.queryByText('View routing process')).not.toBeInTheDocument()
})

test('a request that was not auto routed has no auto routing section', () => {
  renderDialog(true, { admin_info: {} })

  expect(screen.queryByText('Auto routing')).not.toBeInTheDocument()
})
