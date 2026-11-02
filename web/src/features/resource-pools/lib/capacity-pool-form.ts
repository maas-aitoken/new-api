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
import { z } from 'zod'

import type {
  CapacityPool,
  CapacityPoolConfig,
  CapacityPoolLimit,
  ResourcePoolAccount,
} from '../api'
import { accountGroup } from '../pool-metrics'

/** 与后端 dto.MaxModelLimitValue 一致。 */
const MAX_LIMIT = 1_000_000_000

const limitValue = z.coerce
  .number<string | number>()
  .int('Enter a whole number')
  .min(0, 'Enter a whole number')
  .max(MAX_LIMIT, 'Value is too large')

const limitSchema = z.object({
  rpm: limitValue,
  tpm: limitValue,
  max_concurrency: limitValue,
})

export const capacityPoolFormSchema = z.object({
  name: z.string().trim().min(1, 'Name is required').max(128),
  source_ids: z.array(z.number()).min(1, 'Select at least one source'),
  groups: z.array(z.string()),
  /** 套餐行用数组而不是以套餐名为键：套餐名里可能有 "."，表单路径会被拆开。 */
  plan_rates: z.array(limitSchema.extend({ plan: z.string() })),
  default_rate: limitSchema,
  threshold_percent: z.coerce
    .number<string | number>()
    .gt(0, 'Enter a percentage between 0 and 100')
    .max(100, 'Enter a percentage between 0 and 100'),
  pacing_enabled: z.boolean(),
  burst_factor: z.coerce
    .number<string | number>()
    .gt(0, 'Enter a number between 0 and 100')
    .max(100, 'Enter a number between 0 and 100'),
  floor: limitSchema,
  grace_minutes: z.coerce
    .number<string | number>()
    .int('Enter a whole number')
    .min(1, 'Enter 1 to 1440 minutes')
    .max(1440, 'Enter 1 to 1440 minutes'),
})

export type CapacityPoolFormValues = z.infer<typeof capacityPoolFormSchema>

/** 账号的套餐键，与后端 capacityAccountPlan 一致：plan_type，没有时用 subscription_type。 */
export function capacityAccountPlan(account: ResourcePoolAccount): string {
  return (
    (account.plan_type ?? '').trim() || (account.subscription_type ?? '').trim()
  )
}

/** 账号是否在池的范围里：来源选中，且没选分组或分组命中。 */
export function inCapacityScope(
  account: ResourcePoolAccount,
  sourceIds: number[],
  groups: string[]
): boolean {
  if (!sourceIds.includes(account.source_id)) return false
  return groups.length === 0 || groups.includes(accountGroup(account))
}

const zeroLimit = { rpm: 0, tpm: 0, max_concurrency: 0 }

function toFormLimit(limit: CapacityPoolLimit | undefined) {
  return {
    rpm: limit?.rpm ?? 0,
    tpm: limit?.tpm ?? 0,
    max_concurrency: limit?.max_concurrency ?? 0,
  }
}

function toApiLimit(limit: {
  rpm: number
  tpm: number
  max_concurrency: number
}): CapacityPoolLimit {
  return {
    rpm: limit.rpm,
    tpm: limit.tpm,
    max_concurrency: limit.max_concurrency,
  }
}

/**
 * 表单初值。套餐行是本 provider 账号里出现过的套餐，加上已保存但现在没有账号的套餐，
 * 按名字排序；新池默认开配速。
 */
export function capacityPoolFormDefaults(
  pool: CapacityPool | null,
  accounts: ResourcePoolAccount[]
): CapacityPoolFormValues {
  const config = pool?.config
  const plans = new Set(accounts.map(capacityAccountPlan).filter(Boolean))
  for (const plan of Object.keys(config?.plan_rates ?? {})) plans.add(plan)
  return {
    name: pool?.name ?? '',
    source_ids: config?.source_ids ?? [],
    groups: config?.groups ?? [],
    plan_rates: [...plans].sort().map((plan) => ({
      plan,
      ...toFormLimit(config?.plan_rates?.[plan]),
    })),
    default_rate: toFormLimit(config?.default_rate),
    threshold_percent: config?.threshold_percent ?? 95,
    pacing_enabled: config?.pacing_enabled ?? true,
    burst_factor: config?.burst_factor ?? 2,
    floor: config ? toFormLimit(config.floor) : { ...zeroLimit },
    grace_minutes: config ? Math.round(config.grace_seconds / 60) : 10,
  }
}

/** 把表单值转成接口要的配置；三项全空的套餐行不保存。 */
export function capacityPoolConfigFromForm(
  values: CapacityPoolFormValues
): CapacityPoolConfig {
  const planRates: Record<string, CapacityPoolLimit> = {}
  for (const row of values.plan_rates) {
    if (row.rpm || row.tpm || row.max_concurrency) {
      planRates[row.plan] = toApiLimit(row)
    }
  }
  return {
    source_ids: values.source_ids,
    groups: values.groups,
    plan_rates: planRates,
    default_rate: toApiLimit(values.default_rate),
    threshold_percent: values.threshold_percent,
    pacing_enabled: values.pacing_enabled,
    burst_factor: values.burst_factor,
    floor: toApiLimit(values.floor),
    grace_seconds: values.grace_minutes * 60,
  }
}
