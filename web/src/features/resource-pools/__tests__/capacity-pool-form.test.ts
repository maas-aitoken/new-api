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

import type { CapacityPool, ResourcePoolAccount } from '../api'
import {
  capacityAccountPlan,
  capacityPoolConfigFromForm,
  capacityPoolFormDefaults,
  capacityPoolFormSchema,
  inCapacityScope,
} from '../lib/capacity-pool-form'

function account(partial: Partial<ResourcePoolAccount>): ResourcePoolAccount {
  return {
    id: '1',
    email: 'a@example.com',
    enabled: true,
    usage_current: 0,
    usage_limit: 0,
    usage_percentage: 0,
    source_id: 1,
    source_name: 'kiro',
    source_endpoint: 'https://k.example.com',
    ...partial,
  }
}

const savedPool: CapacityPool = {
  id: 3,
  name: 'kiro-aws1',
  provider: 'claude',
  config: {
    source_ids: [1],
    groups: ['aws1'],
    plan_rates: { 'LEGACY.v1': { rpm: 5 } },
    default_rate: { max_concurrency: 2 },
    threshold_percent: 90,
    pacing_enabled: false,
    burst_factor: 1.5,
    floor: { rpm: 30 },
    grace_seconds: 1200,
  },
  members: [],
  updated_time: 1,
}

describe('capacity pool form', () => {
  it('uses the plan_type and falls back to the Kiro subscription_type', () => {
    expect(capacityAccountPlan(account({ plan_type: ' plus ' }))).toBe('plus')
    expect(capacityAccountPlan(account({ subscription_type: 'POWER' }))).toBe(
      'POWER'
    )
    expect(capacityAccountPlan(account({}))).toBe('')
  })

  it('keeps accounts of the selected sources and groups in scope', () => {
    const inGroup = account({ source_id: 1, groups: ['aws1'] })
    const otherGroup = account({ source_id: 1, source_channel: 'aws2' })
    const otherSource = account({ source_id: 2, groups: ['aws1'] })
    expect(inCapacityScope(inGroup, [1], ['aws1'])).toBe(true)
    expect(inCapacityScope(otherGroup, [1], ['aws1'])).toBe(false)
    expect(inCapacityScope(otherGroup, [1], [])).toBe(true)
    expect(inCapacityScope(otherSource, [1], [])).toBe(false)
  })

  it('defaults a new pool to pacing on and lists the plans seen in the accounts', () => {
    const values = capacityPoolFormDefaults(null, [
      account({ subscription_type: 'PRO' }),
      account({ subscription_type: 'POWER' }),
      account({ subscription_type: 'POWER' }),
    ])
    expect(values.pacing_enabled).toBe(true)
    expect(values.threshold_percent).toBe(95)
    expect(values.grace_minutes).toBe(10)
    expect(values.plan_rates.map((row) => row.plan)).toEqual(['POWER', 'PRO'])
  })

  it('keeps saved plan rates whose plan has no account now and converts the grace period to minutes', () => {
    const values = capacityPoolFormDefaults(savedPool, [
      account({ subscription_type: 'POWER' }),
    ])
    expect(values.plan_rates).toEqual([
      { plan: 'LEGACY.v1', rpm: 5, tpm: 0, max_concurrency: 0 },
      { plan: 'POWER', rpm: 0, tpm: 0, max_concurrency: 0 },
    ])
    expect(values.grace_minutes).toBe(20)
    expect(values.pacing_enabled).toBe(false)
  })

  it('drops empty plan rows and converts minutes back to seconds on save', () => {
    const values = capacityPoolFormSchema.parse({
      ...capacityPoolFormDefaults(savedPool, []),
      plan_rates: [
        { plan: 'LEGACY.v1', rpm: '8', tpm: '', max_concurrency: '' },
        { plan: 'EMPTY', rpm: '', tpm: '', max_concurrency: '' },
      ],
      grace_minutes: '15',
    })
    const config = capacityPoolConfigFromForm(values)
    expect(config.plan_rates).toEqual({
      'LEGACY.v1': { rpm: 8, tpm: 0, max_concurrency: 0 },
    })
    expect(config.grace_seconds).toBe(900)
    expect(config.floor).toEqual({ rpm: 30, tpm: 0, max_concurrency: 0 })
  })

  it('rejects a pool without sources, a negative rate and an out-of-range threshold', () => {
    const base = capacityPoolFormDefaults(null, [])
    const result = capacityPoolFormSchema.safeParse({
      ...base,
      name: 'x',
      source_ids: [],
      default_rate: { rpm: '-1', tpm: '', max_concurrency: '' },
      threshold_percent: '120',
    })
    expect(result.success).toBe(false)
    const messages = result.error?.issues.map((issue) => issue.message) ?? []
    expect(messages).toContain('Select at least one source')
    expect(messages).toContain('Enter a whole number')
    expect(messages).toContain('Enter a percentage between 0 and 100')
  })
})
