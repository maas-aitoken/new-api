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
import { render, screen, within } from '@testing-library/react'
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
        endpoint_id: 'new-api-ch-1-k0',
        channel_id: 1,
        key_index: 0,
        model: 'gpt-5',
        runtime_known: true,
        capacity_pool_id: 'c1-aaaaaaaaaaaa',
        rpm_used: 12,
        rpm_capacity: 100,
        tpm_used: 300,
        tpm_capacity: 9000,
        max_concurrency: 4,
        pool_rpm_used: 40,
        pool_tpm_used: 1500,
        pool_inflight: 2,
        load_ratio: 0.4,
      },
      {
        endpoint_id: 'new-api-ch-2-k0',
        channel_id: 2,
        key_index: 0,
        model: 'gpt-5',
        runtime_known: true,
        rpm_used: 7,
        rpm_capacity: 50,
        load_ratio: 0.14,
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

function cellsOf(endpointId: string) {
  const row = screen.getByText(endpointId).closest('tr')
  expect(row).not.toBeNull()
  return within(row as HTMLElement).getAllByRole('cell')
}

test('a pooled endpoint shows the shared pool usage against the pool quota and its own share below', async () => {
  apiClient.get = vi.fn(async () => ({
    data: { success: true, data: monitor },
  }))
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
  await screen.findByText('new-api-ch-1-k0')

  // 列顺序：Endpoint、Model、Health、Load、RPM、TPM。
  const pooled = cellsOf('new-api-ch-1-k0')
  expect(pooled[0]).toHaveTextContent('Pool c1-aaaaaaaaaaaa')
  expect(pooled[3]).toHaveTextContent('40.0%In flight 2 / 4')
  expect(pooled[4]).toHaveTextContent('40 / 100This key 12')
  expect(pooled[5]).toHaveTextContent('1500 / 9000This key 300')

  // 没有计数池的端点（旧版 new-api 的目录）仍按 key 显示，不出现池的说明。
  const legacy = cellsOf('new-api-ch-2-k0')
  expect(legacy[4]).toHaveTextContent('7 / 50')
  expect(legacy[4]).not.toHaveTextContent('This key')
  expect(legacy[0]).not.toHaveTextContent('Pool')
})
