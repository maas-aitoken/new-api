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
import { api } from '@/lib/api'

export interface SchedulerConfig {
  enabled: boolean
  bootstrap_urls: string
  local_url: string
  token_set: boolean
  mode: 'shadow' | 'enforced' | 'canary'
  canary_percent: number
  canary_salt: string
  shadow_timeout_ms: number
  runtime_prefix: string
  runtime_high_watermark: number
  signing_secret_set: boolean
  catalog_token_set: boolean
  emergency_native_routing: boolean
  emergency_max_duration_seconds: number
  emergency_groups: string
  emergency_models: string
  emergency_local_switch: boolean
  kill_switch: boolean
}

export interface SchedulerMonitor {
  configured: boolean
  url: string
  urls?: string[]
  reachable: boolean
  checked_at: string
  nodes?: Array<{ url: string; reachable: boolean }>
  observability?: {
    status?: string
    catalog?: {
      version?: string
      model?: string
      endpoint_count?: number
      enabled_count?: number
    }
    evaluation?: {
      score_version?: string
      age_seconds?: number
      metric_count?: number
      expired?: boolean
    }
    // known/stale 是读到 / 没读到计数池用量的端点数。
    runtime?: { known_endpoints?: number; stale_endpoints?: number }
    endpoints?: SchedulerMonitorEndpoint[]
  }
}

// 每个渠道一个端点。用量是 new-api 计数池最近 60 秒的数（按尝试计，含失败和重试），
// 调度器准入和排序用的也是这组数；同一计数池的端点显示同一组数。
export interface SchedulerMonitorEndpoint {
  endpoint_id: string
  channel_id: number
  provider?: string
  model?: string
  enabled?: boolean
  // 计数池的生效额度（容量池有动态额度时是动态额度），缺省表示不限。
  rpm_capacity?: number
  tpm_capacity?: number
  max_concurrency?: number
  // 用量对生效额度的比例，取 RPM、TPM、并发里最满的一项；额度全不限时为空。
  load_ratio?: number
  capacity_pool_id?: string
  pool_rpm_used?: number
  pool_tpm_used?: number
  pool_inflight?: number
  // 资源池页容量池（cp-<id>）：额度是 new-api 发布的动态额度；耗尽时调度器不再选它。
  pool_dynamic_limit?: boolean
  pool_exhausted?: boolean
  profiles?: Record<string, { score?: number; gate_tier: number }>
}

export async function getSchedulerConfig() {
  const response = await api.get<{ success: boolean; data: SchedulerConfig }>(
    '/api/scheduler/config'
  )
  return response.data
}

export async function updateSchedulerConfig(
  payload: Partial<SchedulerConfig> & {
    token?: string
    signing_secret?: string
  }
) {
  const response = await api.put<{ success: boolean; data: SchedulerConfig }>(
    '/api/scheduler/config',
    payload
  )
  return response.data
}

export async function getSchedulerMonitor(filters?: { model?: string; endpoint_id?: string; channel_id?: string }) {
  const params = new URLSearchParams()
  Object.entries(filters ?? {}).forEach(([key, value]) => value && params.set(key, value))
  const response = await api.get<{ success: boolean; data: SchedulerMonitor }>(
    `/api/scheduler/monitor${params.toString() ? `?${params.toString()}` : ''}`
  )
  return response.data
}

export async function testSchedulerConnection() {
  const response = await api.post<{ success: boolean; message?: string }>(
    '/api/scheduler/test-connection'
  )
  return response.data
}
