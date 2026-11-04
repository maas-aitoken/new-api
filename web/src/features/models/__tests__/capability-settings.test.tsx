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
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
} from '@tanstack/react-router'
import {
  render,
  screen,
  waitFor,
  within,
  cleanup,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18n from 'i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { SectionPageLayout } from '@/components/layout'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { CapabilitySettings } from '../components/capability-settings'
import type { CapabilityTarget, ChannelModelCapacityResult } from '../types'

const targets: CapabilityTarget[] = [
  {
    id: 1,
    model_name: 'sonnet-5',
    declaration_name: 'sonnet-5',
    name_rule: 0,
    capabilities: '{"context":{"max_output_tokens":1024}}',
    declared: true,
    channels: [
      {
        id: 12,
        name: 'main',
        type: 1,
        configured: true,
        cost_multiplier: 1.5,
        upstream_model: 'sonnet-5',
        model_limit: { rpm: 60, max_concurrency: 2 },
        capacity_pool_id: 0,
        capacity_pool_name: '',
      },
      {
        id: 19,
        name: 'backup',
        type: 1,
        configured: false,
        cost_multiplier: 0,
        upstream_model: 'claude-sonnet-5-20261001',
        model_limit: {},
        capacity_pool_id: 0,
        capacity_pool_name: '',
      },
    ],
  },
  {
    id: 2,
    model_name: 'gpt-5.5-mini',
    declaration_name: 'gpt-5.5',
    name_rule: 1,
    capabilities: '{"context":{"max_output_tokens":2048}}',
    declared: true,
    channels: [],
  },
]

const clients: QueryClient[] = []

function Page() {
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>Capabilities</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <CapabilitySettings />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

async function renderTargets(
  items: CapabilityTarget[] = targets,
  total = 2,
  capacity: ChannelModelCapacityResult = {
    items: [],
    shared: true,
    window_seconds: 60,
  }
) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'admin', role: 100 })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/models/capability-targets') {
      return {
        data: {
          success: true,
          data: { items, total, page: 1, page_size: 20 },
        },
      }
    }
    if (url === '/api/channel-model-capabilities/capacity') {
      return { data: { success: true, data: capacity } }
    }
    return { data: { success: true, data: { items: [] } } }
  })
  const put = vi
    .spyOn(api, 'put')
    .mockImplementation(async () => ({ data: { success: true } }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const models = createRoute({
    getParentRoute: () => authenticated,
    path: 'models/$section',
    component: Page,
  })
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([models])]),
    history: createMemoryHistory({ initialEntries: ['/models/capabilities'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByText(items[0].model_name)
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return { get, put }
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})

afterEach(async () => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  await i18n.changeLanguage('en')
})

it('shows each serving channel inline after expanding its model row', async () => {
  await renderTargets()
  expect(screen.getByText('Exact model')).toBeVisible()
  expect(screen.getByText('Prefix · gpt-5.5')).toBeVisible()
  // 渠道不再藏在下拉框里，而是展开行内直接列出。
  expect(
    screen.queryByText('Select a channel to narrow')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('#12 · main')).not.toBeInTheDocument()

  const user = userEvent.setup()
  const toggle = screen.getByRole('button', { name: 'Expand' })
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await user.click(toggle)

  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const narrowed = screen.getByText('#12 · main').closest('li')
  const inherited = screen.getByText('#19 · backup').closest('li')
  expect(narrowed).not.toBeNull()
  expect(inherited).not.toBeNull()
  expect(within(narrowed as HTMLElement).getByText('Narrowed')).toBeVisible()
  expect(
    within(narrowed as HTMLElement).getByRole('button', {
      name: 'Edit narrowing',
    })
  ).toBeVisible()
  expect(within(inherited as HTMLElement).getByText('Inherited')).toBeVisible()
  expect(
    within(inherited as HTMLElement).getByRole('button', { name: 'Narrow' })
  ).toBeVisible()
})

it('saves a per-channel cost multiplier on blur', async () => {
  const { put } = await renderTargets()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))

  const configured = screen.getByLabelText('Cost multiplier for channel #12')
  expect(configured).toHaveValue('1.5')
  expect(screen.getByLabelText('Cost multiplier for channel #19')).toHaveValue(
    ''
  )

  await user.clear(configured)
  await user.type(configured, '1.8')
  await user.tab()

  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/channel-model-capabilities/cost-multiplier',
      { channel_id: 12, model: 'sonnet-5', multiplier: 1.8 },
      expect.anything()
    )
  )
})

it('saves the RPM, TPM and concurrency limits together once focus leaves the group', async () => {
  const { put } = await renderTargets()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))

  const rpm = screen.getByLabelText('RPM limit for channel #12')
  const tpm = screen.getByLabelText('TPM limit for channel #12')
  expect(rpm).toHaveValue('60')
  expect(tpm).toHaveValue('')
  expect(
    screen.getByLabelText('Concurrency limit for channel #12')
  ).toHaveValue('2')

  await user.clear(rpm)
  await user.type(rpm, '120')
  await user.click(tpm)
  await user.type(tpm, '90000')
  // 焦点还在这一组里，不逐项保存。
  expect(put).not.toHaveBeenCalled()

  await user.click(screen.getByLabelText('RPM limit for channel #19'))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/channel-model-capabilities/limit',
      {
        channel_id: 12,
        model: 'sonnet-5',
        rpm: 120,
        tpm: 90000,
        max_concurrency: 2,
      },
      expect.anything()
    )
  )
  expect(put).toHaveBeenCalledTimes(1)
})

it('shows live usage and the remaining quota for channels with limits', async () => {
  const { get } = await renderTargets(targets, 2, {
    items: [
      {
        channel_id: 12,
        model: 'sonnet-5',
        upstream_model: 'sonnet-5',
        pool_id: 'c12-aaaaaaaaaaaa',
        limit: { rpm: 60, max_concurrency: 2 },
        rpm_used: 12,
        tpm_used: 900,
        in_flight: 2,
        remaining: { rpm: 48, tpm: null, concurrency: 0 },
      },
      {
        channel_id: 19,
        model: 'sonnet-5',
        upstream_model: 'claude-sonnet-5-20261001',
        pool_id: 'c19-bbbbbbbbbbbb',
        limit: {},
        rpm_used: 3,
        tpm_used: 450,
        in_flight: 1,
        remaining: { rpm: null, tpm: null, concurrency: null },
      },
    ],
    shared: false,
    window_seconds: 60,
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))

  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/channel-model-capabilities/capacity',
      { params: { model: 'sonnet-5', channel_ids: '12,19' } }
    )
  )
  // 设了额度的维度显示"已用 / 额度 · 剩余"，没设的只显示已用。
  expect(
    await screen.findByLabelText('Live usage for channel #12')
  ).toHaveTextContent('RPM 12 / 60 · 48 leftTPM 900In flight 2 / 2 · 0 left')
  expect(screen.getByLabelText('Live usage for channel #19')).toHaveTextContent(
    'RPM 3TPM 450In flight 1'
  )
  // 上游名与公开名不同时标出上游名；没配 Redis 时说明只是本节点的数据。
  expect(
    screen.getByText(
      'Upstream claude-sonnet-5-20261001; aliases of the same upstream share these limits'
    )
  ).toBeVisible()
  expect(
    screen.getByText(
      'Redis is not configured, so live usage reflects only this node.'
    )
  ).toBeVisible()
})

it('shows a read-only capacity pool note instead of limit inputs for a bound channel', async () => {
  const bound: CapabilityTarget[] = [
    {
      ...targets[0],
      channels: [
        {
          ...targets[0].channels[0],
          capacity_pool_id: 3,
          capacity_pool_name: 'kiro-aws1',
        },
        targets[0].channels[1],
      ],
    },
  ]
  await renderTargets(bound, 1, {
    items: [
      {
        channel_id: 12,
        model: 'sonnet-5',
        upstream_model: 'sonnet-5',
        pool_id: 'cp-3',
        limit: { rpm: 300 },
        rpm_used: 40,
        tpm_used: 0,
        in_flight: 3,
        remaining: { rpm: 260, tpm: null, concurrency: null },
        capacity_pool: {
          id: 3,
          name: 'kiro-aws1',
          source: 'dynamic',
          exhausted: false,
          computed_at: 1,
          member_rpm_used: 4,
          member_tpm_used: 0,
          member_in_flight: 1,
        },
      },
    ],
    shared: true,
    window_seconds: 60,
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Expand' }))

  const card = screen.getByText('#12 · main').closest('li') as HTMLElement
  // 绑定期间不能在这里改额度，只显示跟随哪个池、池的数和这个渠道自己用了多少。
  expect(
    within(card).queryByLabelText('RPM limit for channel #12')
  ).not.toBeInTheDocument()
  expect(
    within(card).getByText('Follows capacity pool kiro-aws1')
  ).toBeVisible()
  expect(within(card).getByText('Dynamic limit')).toBeVisible()
  expect(
    within(card).getByText('This channel: RPM 4, in flight 1')
  ).toBeVisible()
  expect(
    within(card).getByRole('link', {
      name: 'Manage on the Resource Pools page',
    })
  ).toHaveAttribute('href', '/resource-pools')
  expect(
    await within(card).findByLabelText('Live usage for channel #12')
  ).toHaveTextContent('RPM 40 / 300 · 260 left')
  // 没绑定的渠道照常显示输入框。
  expect(screen.getByLabelText('RPM limit for channel #19')).toBeVisible()
})

it('requests the filtered page when the model name and channel ID inputs change', async () => {
  const { get } = await renderTargets()
  const user = userEvent.setup()

  await user.type(
    screen.getByPlaceholderText('Filter by model name...'),
    'sonnet'
  )
  await waitFor(
    () =>
      expect(get).toHaveBeenCalledWith('/api/models/capability-targets', {
        params: expect.objectContaining({ keyword: 'sonnet', p: 1 }),
      }),
    { timeout: 3000 }
  )

  await user.type(screen.getByPlaceholderText('Channel ID'), '12')
  await waitFor(
    () =>
      expect(get).toHaveBeenCalledWith('/api/models/capability-targets', {
        params: expect.objectContaining({ keyword: 'sonnet', channel_id: 12 }),
      }),
    { timeout: 3000 }
  )
})

it('requests the next page of model rows through the shared pagination', async () => {
  const { get } = await renderTargets(targets, 40)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/models/capability-targets', {
      params: expect.objectContaining({ p: 2, page_size: 20 }),
    })
  )
})
