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
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { ChannelMultipliersSection } from '../channel-multipliers-section'
import type { ModelPricingEntry } from '../api'

const entry: ModelPricingEntry = {
  model_name: 'example-model',
  version: 'v1',
  configured: { ModelPrice: 1 },
  effective: { ModelPrice: 1 },
}

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/models/capability-targets') {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                id: 1,
                model_name: 'example-model',
                declared: true,
                channels: [
                  {
                    id: 12,
                    name: 'discount-route',
                    type: 1,
                    configured: false,
                    cost_multiplier: 0.5,
                    upstream_model: 'example-model',
                    model_limit: {},
                    capacity_pool_id: 0,
                    capacity_pool_name: '',
                  },
                ],
              },
            ],
            total: 1,
            page: 1,
            page_size: 20,
          },
        },
      }
    }
    return { data: { success: true, data: { items: [] } } }
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true },
  })
  const result = render(
    <QueryClientProvider client={client}>
      <ChannelMultipliersSection modelName='example-model' entry={entry} />
    </QueryClientProvider>
  )
  return { ...result, client, get, put }
}

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: { ...DEFAULT_CURRENCY_CONFIG, quotaDisplayType: 'USD' },
  })
})

afterEach(() => {
  cleanup()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  vi.restoreAllMocks()
})

it('previews the effective channel price after the multiplier and saves edits on blur', async () => {
  const { put } = renderSection()
  const user = userEvent.setup()

  expect(await screen.findByText('#12 · discount-route')).toBeInTheDocument()
  // Base per-request price is 1 USD; with a 0.5× multiplier the preview shows 0.5.
  const priceCell = document.querySelector(
    '.channel-multipliers-table, table'
  )
  expect(priceCell?.textContent).toContain('0.5')

  const input = await screen.findByLabelText(
    'Cost multiplier for channel #12'
  )
  // Unchanged value should not trigger a save.
  await user.clear(input)
  await user.type(input, '0.5')
  await user.tab()
  expect(put).not.toHaveBeenCalled()

  await user.clear(input)
  await user.type(input, '1.5')
  await user.tab()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/channel-model-capabilities/cost-multiplier',
      { channel_id: 12, model: 'example-model', multiplier: 1.5 },
      { skipBusinessError: true, skipErrorHandler: true }
    )
  )

  // Clearing the input resets to the default 1× by writing 0.
  await user.clear(input)
  await user.tab()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/channel-model-capabilities/cost-multiplier',
      { channel_id: 12, model: 'example-model', multiplier: 0 },
      { skipBusinessError: true, skipErrorHandler: true }
    )
  )
})
