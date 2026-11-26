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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { ResourcePoolAccount, ResourcePoolUsage } from '../api'
import { ResourcePools } from '../index'

const clients: QueryClient[] = []

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

function account(partial: Partial<ResourcePoolAccount>): ResourcePoolAccount {
  return {
    id: partial.email ?? '1',
    email: 'a@example.com',
    enabled: true,
    usage_current: 0,
    usage_limit: 10000,
    usage_percentage: 0,
    source_id: 2,
    source_name: 'anthropic1',
    source_endpoint: 'https://k1.example.com',
    ...partial,
  }
}

const second = {
  source_id: 4,
  source_name: 'anthropic2',
  source_endpoint: 'https://k2.example.com',
}
const alpha = account({
  email: 'alpha@example.com',
  groups: ['aws1'],
  usage_current: 7000,
  usage_percentage: 70,
})
const beta = account({
  email: 'beta@example.com',
  groups: ['aws2'],
  usage_current: 2000,
  usage_percentage: 20,
})
const gamma = account({
  ...second,
  email: 'gamma@example.com',
  groups: ['aws1'],
  usage_current: 9500,
  usage_percentage: 95,
})
// Listed under both groups, so either group filter keeps it.
const delta = account({
  ...second,
  email: 'Delta.Ops@example.com',
  groups: ['aws1', 'aws2'],
  usage_current: 4500,
  usage_percentage: 45,
})
const epsilon = account({
  ...second,
  email: 'epsilon@example.com',
  usage_current: 500,
  usage_percentage: 5,
})

const claudeUsage: ResourcePoolUsage = {
  accounts: [alpha, beta, gamma, delta, epsilon],
  sources: [],
  refreshed_at: 1_800_000_000,
}
const emptyUsage: ResourcePoolUsage = {
  accounts: [],
  sources: [],
  refreshed_at: 1_800_000_000,
}

async function openClaudeAccounts() {
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url === '/api/channel/resource_pools/usage') {
      const params: { provider?: string } = config?.params ?? {}
      const usage = params.provider === 'claude' ? claudeUsage : emptyUsage
      return { data: { success: true, data: usage } }
    }
    if (url === '/api/channel/resource_pools/sources') {
      return { data: { success: true, data: [] } }
    }
    throw new Error(`unexpected request: ${url}`)
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <ResourcePools />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Account details' }))
  await screen.findByText('alpha@example.com')
  return user
}

/** Account column of every body row, top to bottom. */
function shownAccounts() {
  return within(screen.getByRole('table'))
    .getAllByRole('row')
    .slice(1)
    .map((row) => within(row).getAllByRole('cell')[0].textContent)
}

it('filters Claude accounts by a case-insensitive part of the account', async () => {
  const user = await openClaudeAccounts()

  await user.type(
    screen.getByRole('textbox', { name: 'Search account' }),
    'OPS'
  )

  expect(shownAccounts()).toEqual(['Delta.Ops@example.com'])
  expect(screen.getByText('Showing 1 of 5 accounts')).toBeInTheDocument()

  await user.type(screen.getByRole('textbox', { name: 'Search account' }), 'zz')

  expect(shownAccounts()).toEqual(['No matching resource pool accounts'])
})

it('filters Claude accounts by group, including multi-group and ungrouped accounts', async () => {
  const user = await openClaudeAccounts()
  const group = screen.getByLabelText('Group')

  await user.selectOptions(group, 'aws2')
  expect(shownAccounts()).toEqual(['beta@example.com', 'Delta.Ops@example.com'])

  await user.selectOptions(group, 'Ungrouped')
  expect(shownAccounts()).toEqual(['epsilon@example.com'])

  await user.selectOptions(group, 'All')
  expect(shownAccounts()).toHaveLength(5)
})

it('filters Claude accounts by channel and combines it with the group filter', async () => {
  const user = await openClaudeAccounts()

  await user.selectOptions(screen.getByLabelText('Channel'), 'anthropic2')
  expect(shownAccounts()).toEqual([
    'gamma@example.com',
    'Delta.Ops@example.com',
    'epsilon@example.com',
  ])

  await user.selectOptions(screen.getByLabelText('Group'), 'aws1')
  expect(shownAccounts()).toEqual([
    'gamma@example.com',
    'Delta.Ops@example.com',
  ])
  expect(screen.getByText('Showing 2 of 5 accounts')).toBeInTheDocument()
})

it('sorts Claude accounts by pool water level and returns to the upstream order', async () => {
  const user = await openClaudeAccounts()
  const header = screen.getByRole('columnheader', { name: /Pool water level/ })
  const toggle = within(header).getByRole('button')
  expect(header).toHaveAttribute('aria-sort', 'none')

  await user.click(toggle)
  expect(header).toHaveAttribute('aria-sort', 'descending')
  expect(shownAccounts()).toEqual([
    'gamma@example.com',
    'alpha@example.com',
    'Delta.Ops@example.com',
    'beta@example.com',
    'epsilon@example.com',
  ])

  await user.click(toggle)
  expect(header).toHaveAttribute('aria-sort', 'ascending')
  expect(shownAccounts()).toEqual([
    'epsilon@example.com',
    'beta@example.com',
    'Delta.Ops@example.com',
    'alpha@example.com',
    'gamma@example.com',
  ])

  await user.click(toggle)
  expect(header).toHaveAttribute('aria-sort', 'none')
  expect(shownAccounts()).toEqual(claudeUsage.accounts.map((a) => a.email))
})
