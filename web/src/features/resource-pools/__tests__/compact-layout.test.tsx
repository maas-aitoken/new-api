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
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import type {
  ResourcePoolAccount,
  ResourcePoolProvider,
  ResourcePoolUsage,
} from '../api'
import { ResourcePoolOverview, type PoolQueryState } from '../overview'
import { Metric, ResetAt, UsageBar, WaterRing } from '../pool-ui'

const NOW = new Date('2026-11-26T08:00:00Z').getTime()

function account(partial: Partial<ResourcePoolAccount>): ResourcePoolAccount {
  return {
    id: '1',
    email: 'a@example.com',
    enabled: true,
    usage_current: 0,
    usage_limit: 0,
    usage_percentage: 0,
    source_id: 1,
    source_name: 'anthropic1',
    source_endpoint: 'https://k.example.com',
    ...partial,
  }
}

function usageOf(accounts: ResourcePoolAccount[]): ResourcePoolUsage {
  return {
    accounts,
    sources: [
      {
        source_id: 1,
        source_name: 'anthropic1',
        source_endpoint: 'https://k.example.com',
        status: 'success',
        count: accounts.length,
        error: '',
      },
    ],
    refreshed_at: NOW / 1000 - 60,
  }
}

const EMPTY_USAGE: ResourcePoolUsage = {
  accounts: [],
  sources: [],
  refreshed_at: NOW / 1000 - 60,
}

function renderOverview(claude: ResourcePoolUsage) {
  const onOpen = vi.fn()
  const loaded = (data: ResourcePoolUsage): PoolQueryState => ({
    data,
    isPending: false,
    error: null,
  })
  const pools: Record<ResourcePoolProvider, PoolQueryState> = {
    claude: loaded(claude),
    gpt: loaded(EMPTY_USAGE),
    gemini: loaded(EMPTY_USAGE),
    grok: loaded(EMPTY_USAGE),
  }
  render(
    <ResourcePoolOverview pools={pools} now={NOW} locale='en' onOpen={onOpen} />
  )
  return onOpen
}

it('shows an upcoming reset as its time followed by the distance, with no expiry flag', () => {
  render(<ResetAt iso='2026-12-01T08:00:00Z' now={NOW} locale='en' />)

  expect(screen.getByText('in 5 days')).toBeVisible()
  expect(screen.queryByText('Expired')).not.toBeInTheDocument()
})

it('flags a reset that already passed as expired instead of showing a distance', () => {
  render(<ResetAt iso='2026-11-20T08:00:00Z' now={NOW} locale='en' />)

  expect(screen.getByText('Expired')).toBeVisible()
  expect(screen.queryByText(/days ago/)).not.toBeInTheDocument()
})

it('renders a dash when the account reports no reset time', () => {
  render(<ResetAt now={NOW} locale='en' />)

  expect(screen.getByText('-')).toBeVisible()
})

it('prints the usage figure next to the bar by default', () => {
  render(<UsageBar percent={91.26} digits={1} />)

  expect(screen.getByText('91.3%')).toBeVisible()
})

it('draws the usage bar without a figure when the caller hides the value', () => {
  render(<UsageBar percent={91.26} digits={1} hideValue />)

  expect(screen.queryByText('91.3%')).not.toBeInTheDocument()
})

it('names the water ring by its label and level for assistive tech', () => {
  render(<WaterRing percent={72.4} label='Pool water level' />)

  expect(
    screen.getByRole('img', { name: 'Pool water level 72%' })
  ).toBeVisible()
})

it('names an empty water ring with a dash when no account reports usage', () => {
  render(<WaterRing percent={null} label='Usage (7d)' />)

  expect(screen.getByRole('img', { name: 'Usage (7d) -' })).toBeVisible()
})

it('keeps the metric hint readable in full through its tooltip', () => {
  render(
    <Metric
      label='Overall share'
      value='95%'
      hint='18 accounts at or above 80%'
    />
  )

  expect(screen.getByText('95%')).toBeVisible()
  expect(screen.getByTitle('18 accounts at or above 80%')).toBeVisible()
})

it('opens the channel stats and the account details of a pool from its overview card', async () => {
  const user = userEvent.setup()
  const onOpen = renderOverview(
    usageOf([account({ usage_current: 50, usage_limit: 100 })])
  )

  const [channels] = screen.getAllByRole('button', { name: 'Channel stats' })
  const [accounts] = screen.getAllByRole('button', { name: 'Account details' })
  await user.click(channels)
  await user.click(accounts)

  expect(onOpen).toHaveBeenNthCalledWith(1, 'claude', 'channels')
  expect(onOpen).toHaveBeenNthCalledWith(2, 'claude', 'accounts')
})

it('shows source reachability in the header of a pool that has sources', () => {
  renderOverview(usageOf([account({ usage_current: 50, usage_limit: 100 })]))

  expect(
    screen.getByText('1/1 sources reachable · Updated 1 minute ago')
  ).toBeInTheDocument()
})

it('lists the usage band counts as bare numbers under the distribution bar', () => {
  renderOverview(
    usageOf([
      account({ id: 'low', usage_current: 10, usage_limit: 100 }),
      account({ id: 'full', usage_current: 100, usage_limit: 100 }),
    ])
  )

  const exhausted = screen.getAllByText('≥ 95%')[0]
  expect(within(exhausted).getByText('1')).toBeVisible()
})

it('offers the interface config of a pool that has no sources yet', async () => {
  const user = userEvent.setup()
  const onOpen = renderOverview(EMPTY_USAGE)

  const [config] = screen.getAllByRole('button', { name: 'Interface config' })
  await user.click(config)

  expect(onOpen).toHaveBeenCalledWith('claude', 'sources')
})
