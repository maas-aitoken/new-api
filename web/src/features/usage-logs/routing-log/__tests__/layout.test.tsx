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
import { render, screen } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RoutingLogs } from '..'

test('rows past the visible height scroll inside the page instead of being clipped', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 1,
            created_at: 1_760_000_001,
            request_id: 'req-1',
            request_params: '',
            user_id: 1,
            token_id: 1,
            using_group: 'default',
            outcome: 'routed',
            judgment: 'live',
            source: 'jev',
            task_type: 'code.build',
            model: 'model-1',
            effort: 'high',
            preference: 'balanced',
            jev_latency_ms: 90,
          },
        ],
        total: 1,
        page: 1,
        page_size: 20,
      },
    },
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RoutingLogs />
    </QueryClientProvider>
  )

  const scroller = (await screen.findByRole('table')).closest(
    '.min-h-0.overflow-y-auto'
  )

  expect(scroller).not.toBeNull()
  expect(scroller?.parentElement?.closest('.overflow-hidden')).not.toBeNull()
})
