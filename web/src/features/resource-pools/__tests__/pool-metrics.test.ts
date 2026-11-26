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
import { describe, expect, it } from 'vitest'

import type { ResourcePoolAccount, ResourcePoolUsage } from '../api'
import { summarizeByGroup, summarizePool, waterLevel } from '../pool-metrics'

function account(partial: Partial<ResourcePoolAccount>): ResourcePoolAccount {
  return {
    id: '1',
    email: 'a@example.com',
    enabled: true,
    usage_current: 0,
    usage_limit: 0,
    usage_percentage: 0,
    groups: ['aws1'],
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
    refreshed_at: 0,
  }
}

const live = account({
  id: 'live',
  subscription_type: 'POWER',
  subscription_title: 'KIRO POWER',
  usage_current: 7000,
  usage_limit: 10000,
  usage_percentage: 70,
})
// The backend reports a disabled Kiro account as a used-up KIRO POWER account.
const disabled = account({
  id: 'disabled',
  enabled: false,
  subscription_type: 'POWER',
  subscription_title: 'KIRO POWER',
  usage_current: 10000,
  usage_limit: 10000,
  usage_percentage: 100,
})

describe('quota pool statistics with disabled accounts', () => {
  const usage = usageOf([live, disabled])

  it('counts disabled accounts in the overview', () => {
    const summary = summarizePool('claude', usage, 0)
    expect(summary.level).toBe(85)
    expect(summary.total).toBe(2)
    expect(summary.available).toBe(1)
    expect(summary.high).toBe(1)
    expect(summary.exhausted).toBe(1)
    expect(summary.unhealthy).toBe(1)
    expect(summary.bands).toEqual([0, 1, 0, 1])
    expect(summary.top.map((item) => item.account.id)).toEqual(['live'])
  })

  it('counts disabled accounts in the channel statistics', () => {
    expect(waterLevel(usage.accounts)).toBe(85)
    const [group] = summarizeByGroup(usage.accounts, 0)
    expect(group).toMatchObject({
      group: 'aws1',
      enabled: 1,
      usage: 17000,
      limit: 20000,
      level: 85,
    })
  })

  it('gives a group of only disabled accounts a full water level', () => {
    const [group] = summarizeByGroup(usageOf([disabled]).accounts, 0)
    expect(group).toMatchObject({ enabled: 0, usage: 10000, level: 100 })
  })
})
