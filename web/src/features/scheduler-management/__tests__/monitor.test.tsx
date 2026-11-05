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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { SchedulerMonitor } from '../api'
import { SchedulerMonitorPage } from '../monitor'

const monitor: SchedulerMonitor = {
  configured: true,
  url: 'http://scheduler:18080',
  reachable: true,
  checked_at: '2026-11-02T12:00:00Z',
  observability: {
    status: 'healthy',
    endpoints: [
      {
        endpoint_id: 'new-api-ch-1',
        channel_id: 1,
        model: 'gpt-5',
        capacity_pool_id: 'c1-aaaaaaaaaaaa',
        rpm_capacity: 100,
        tpm_capacity: 9000,
        max_concurrency: 4,
        pool_rpm_used: 40,
        pool_tpm_used: 1500,
        pool_inflight: 2,
        load_ratio: 0.4,
      },
      {
        endpoint_id: 'new-api-ch-2',
        channel_id: 2,
        model: 'gpt-5',
        capacity_pool_id: 'c2-bbbbbbbbbbbb',
        pool_rpm_used: 7,
      },
    ],
  },
}

type ApiGet = (url: string) => Promise<{ data: Record<string, unknown> }>
const apiClient = api as unknown as { get: ApiGet }
const originalGet = apiClient.get

afterEach(() => {
  apiClient.get = originalGet
})

async function renderMonitor() {
  // 与生产配置（src/i18n/config.ts）一致不转义插值，否则 "2 / 4" 会显示成 "&#x2F;"。
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <SchedulerMonitorPage />
    </I18nextProvider>
  )
  await screen.findByText('new-api-ch-1')
}

function cellsOf(endpointId: string) {
  const row = screen.getByText(endpointId).closest('tr')
  expect(row).not.toBeNull()
  return within(row as HTMLElement).getAllByRole('cell')
}

test('each channel row shows its counting pool usage against the pool quota without health or per-key columns', async () => {
  apiClient.get = vi.fn<ApiGet>(async () => ({
    data: { success: true, data: monitor },
  }))
  await renderMonitor()

  // 列顺序：Endpoint、Model、Load、RPM、TPM。调度器不再维护健康状态，也不按 key 拆端点。
  expect(screen.queryByRole('columnheader', { name: 'Health' })).toBeNull()
  const limited = cellsOf('new-api-ch-1')
  expect(limited[0]).toHaveTextContent('channel 1')
  expect(limited[0]).not.toHaveTextContent('key')
  expect(limited[0]).toHaveTextContent('Pool c1-aaaaaaaaaaaa')
  expect(limited[2].textContent).toBe('40.0%In flight 2 / 4')
  expect(limited[3].textContent).toBe('40 / 100')
  expect(limited[4].textContent).toBe('1500 / 9000')

  // 不限额的池：负载未知，用量对着 ∞。
  const unlimited = cellsOf('new-api-ch-2')
  expect(unlimited[2].textContent).toBe('-In flight 0 / ∞')
  expect(unlimited[3].textContent).toBe('7 / ∞')
})

test('typing a channel id in the filter reloads the monitor scoped to that channel', async () => {
  const get = vi.fn<ApiGet>(async () => ({
    data: { success: true, data: monitor },
  }))
  apiClient.get = get
  const user = userEvent.setup()
  await renderMonitor()

  await user.type(screen.getByRole('textbox', { name: 'Channel ID' }), '2')

  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/scheduler/monitor?channel_id=2')
  )
})
