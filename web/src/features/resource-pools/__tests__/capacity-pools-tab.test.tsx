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
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type {
  CapacityPool,
  CapacityPoolCapacity,
  CapacityPoolCapacityResult,
} from '../api'
import { CapacityPoolsTab } from '../components/capacity-pools-tab'

const clients: QueryClient[] = []

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

const pool: CapacityPool = {
  id: 3,
  name: 'kiro-aws1',
  provider: 'claude',
  config: {
    source_ids: [1],
    groups: ['aws1'],
    default_rate: { rpm: 100 },
    threshold_percent: 95,
    pacing_enabled: true,
    burst_factor: 2,
    floor: { rpm: 30 },
    grace_seconds: 600,
  },
  members: [
    {
      channel_id: 12,
      channel_name: 'main',
      upstream_model: 'claude-sonnet',
      models: ['sonnet', 'sonnet-latest'],
      stale: false,
    },
    {
      channel_id: 19,
      channel_name: 'backup',
      upstream_model: 'claude-old',
      models: null,
      stale: true,
    },
  ],
  updated_time: 1,
}

const dynamic: CapacityPoolCapacity = {
  id: 3,
  pool_id: 'cp-3',
  limit: { rpm: 300, tpm: 0, max_concurrency: 0 },
  source: 'dynamic',
  exhausted: false,
  computed_at: 1_800_000_000,
  detail: {
    accounts: 5,
    usable: 3,
    excluded: { disabled: 1, exhausted: 1 },
    plans: [{ plan: 'POWER', total: 5, usable: 3 }],
    rpm: {
      burst: 3000,
      pace: 300,
      paced_accounts: 3,
      computed: 300,
      unbounded: false,
    },
    tpm: { burst: 0, pace: 0, paced_accounts: 0, computed: 0, unbounded: true },
    concurrency: {
      burst: 0,
      pace: 0,
      paced_accounts: 0,
      computed: 0,
      unbounded: true,
    },
    pacing: true,
    upstream_factor_accounts: 0,
    learned: { requests_per_unit: 0.5, tokens_per_unit: 2000, unit: 'credit' },
  },
  status: { status: 'ok', attempt_at: 1_800_000_000 },
  rpm_used: 40,
  tpm_used: 0,
  in_flight: 3,
  remaining: { rpm: 260, tpm: null, concurrency: null },
  members: [
    {
      channel_id: 12,
      upstream_model: 'claude-sonnet',
      rpm_used: 40,
      tpm_used: 0,
      in_flight: 3,
    },
  ],
}

function renderTab(
  pools: CapacityPool[],
  capacity: CapacityPoolCapacityResult
) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/resource_pools/capacity_pools') {
      return { data: { success: true, data: pools } }
    }
    if (url === '/api/channel/resource_pools/capacity_pools/capacity') {
      return { data: { success: true, data: capacity } }
    }
    if (url === '/api/channel/search') {
      return {
        data: {
          success: true,
          data: {
            items: [
              { id: 7, name: 'kiro-relay', models: 'claude-opus,claude-haiku' },
            ],
            total: 1,
          },
        },
      }
    }
    return { data: { success: true, data: [] } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async () => ({
    data: { success: true, data: { added: ['claude-opus'] } },
  }))
  const remove = vi
    .spyOn(api, 'delete')
    .mockImplementation(async () => ({ data: { success: true } }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <CapacityPoolsTab
        provider='claude'
        sources={[
          {
            id: 1,
            provider: 'claude',
            name: 'kiro',
            endpoint: 'https://k.example.com',
            admin_password: '',
            group_name: '',
            enabled: true,
            snapshot_at: 0,
          },
        ]}
        accounts={[]}
        locale='en-US'
        now={1_800_000_060_000}
      />
    </QueryClientProvider>
  )
  return { get, post, remove }
}

it('shows an empty state when the provider has no capacity pools', async () => {
  renderTab([], { items: [], shared: true, window_seconds: 60 })
  expect(await screen.findByText('No capacity pools')).toBeVisible()
})

it('shows the dynamic limit, its breakdown and each member with live usage', async () => {
  renderTab([pool], { items: [dynamic], shared: true, window_seconds: 60 })

  expect(await screen.findByText('kiro-aws1')).toBeVisible()
  expect(screen.getByText('kiro · aws1')).toBeVisible()
  expect(await screen.findByText('Dynamic limit')).toBeVisible()
  expect(
    screen.getByLabelText('Live usage of capacity pool kiro-aws1')
  ).toHaveTextContent('RPM 40 / 300 · 260 left')
  expect(
    screen.getByText('3 of 5 accounts usable · Disabled 1, Over threshold 1')
  ).toBeVisible()
  expect(
    screen.getByText('RPM: burst 3K, pacing 300 (3 accounts)')
  ).toBeVisible()
  expect(screen.getByText('TPM: Unlimited')).toBeVisible()
  expect(
    screen.getByText(
      'Learned from our traffic: 0.50 requests / 2K tokens per credit'
    )
  ).toBeVisible()

  const active = screen.getByText('#12 main').closest('tr') as HTMLElement
  expect(within(active).getByText('sonnet, sonnet-latest')).toBeVisible()
  expect(within(active).getByText('40')).toBeVisible()
  const stale = screen.getByText('#19 backup').closest('tr') as HTMLElement
  expect(within(stale).getByText('No model maps here any more')).toBeVisible()
})

it('explains why the limit is not updating and that the last one is kept', async () => {
  renderTab([pool], {
    items: [
      {
        ...dynamic,
        status: {
          status: 'source_failed',
          error: 'kiro: HTTP 502',
          attempt_at: 1,
        },
      },
    ],
    shared: false,
    window_seconds: 60,
  })
  expect(
    await screen.findByText(
      'A source of this pool failed: kiro: HTTP 502 · Keeping the last limit until the grace period ends'
    )
  ).toBeVisible()
  expect(
    screen.getByText(
      'Redis is not configured, so live usage reflects only this node.'
    )
  ).toBeVisible()
})

it('marks an exhausted pool and a pool that falls back to its floor limit', async () => {
  const floorPool = { ...pool, id: 4, name: 'kiro-aws2', members: [] }
  renderTab([pool, floorPool], {
    items: [
      {
        ...dynamic,
        exhausted: true,
        remaining: { rpm: 0, tpm: 0, concurrency: 0 },
      },
      {
        ...dynamic,
        id: 4,
        source: 'floor',
        computed_at: 0,
        detail: undefined,
        status: undefined,
      },
    ],
    shared: true,
    window_seconds: 60,
  })
  expect(await screen.findByText('Exhausted')).toBeVisible()
  expect(screen.getByText('Floor limit')).toBeVisible()
  expect(
    screen.getByText('Not computed yet. The floor limit applies.')
  ).toBeVisible()
})

it('removes a member from the pool by channel and upstream model', async () => {
  const { remove } = renderTab([pool], {
    items: [dynamic],
    shared: true,
    window_seconds: 60,
  })
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', {
      name: 'Remove claude-sonnet of channel #12',
    })
  )
  await waitFor(() =>
    expect(remove).toHaveBeenCalledWith(
      '/api/channel/resource_pools/capacity_pools/3/members',
      { params: { channel_id: 12, upstream_model: 'claude-sonnet' } }
    )
  )
})

it('asks the server to reset the pool only after the reset is confirmed', async () => {
  const { post } = renderTab([pool], {
    items: [dynamic],
    shared: true,
    window_seconds: 60,
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Reset' }))

  const dialog = await screen.findByRole('alertdialog')
  expect(
    within(dialog).getByText('Reset capacity pool kiro-aws1?')
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Reset' }))

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/resource_pools/capacity_pools/3/reset'
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('adds the checked models of a channel to the pool', async () => {
  const { post } = renderTab([pool], {
    items: [dynamic],
    shared: true,
    window_seconds: 60,
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Add members' }))

  const dialog = await screen.findByRole('dialog')
  const addSelected = within(dialog).getByRole('button', {
    name: 'Add selected',
  })
  expect(addSelected).toBeDisabled()
  await within(dialog).findByRole('option', { name: '#7 kiro-relay' })
  await user.selectOptions(within(dialog).getByLabelText('Channel'), '7')
  await user.click(
    within(dialog).getByRole('checkbox', { name: 'claude-opus' })
  )
  expect(addSelected).toBeEnabled()
  await user.click(addSelected)

  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/resource_pools/capacity_pools/3/members',
      { channel_id: 7, models: ['claude-opus'] }
    )
  )
})
