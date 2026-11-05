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

import type {
  FlowQuotaDataItem,
  IselfUsageParams,
  IselfUsageRow,
  ModelAvailabilityData,
  ModelAvailabilityMinutes,
  PeakTpmParams,
  PeakTpmPoint,
  QuotaDataItem,
  UptimeGroupResult,
  UsageAnalyticsData,
  UsageAnalyticsParams,
  UsageOrgFilter,
  UsageOrgOption,
  UserChartsDimension,
} from './types'

// ============================================================================
// Dashboard APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Quota & Usage Data
// ----------------------------------------------------------------------------

// Get user quota data within a time range
// Admin users get all users' data by default.
export async function getUserQuotaDates(
  params: UsageOrgFilter & {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data' : '/api/data/self'
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    endpoint,
    { params }
  )
  return res.data
}

export async function getUsageAnalytics(
  params: UsageAnalyticsParams,
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data/analytics' : '/api/data/analytics/self'
  const res = await api.get<{
    success: boolean
    message?: string
    data?: UsageAnalyticsData
  }>(endpoint, { params })
  return res.data
}

// Admin-only hourly peaks; hours without traffic are omitted.
export async function getPeakTpm(params: PeakTpmParams) {
  const res = await api.get<{
    success: boolean
    message?: string
    data?: PeakTpmPoint[]
  }>('/api/data/peak-tpm', { params })
  return res.data
}

export async function getIselfUsage(params: IselfUsageParams, isAdmin = false) {
  const endpoint = isAdmin ? '/api/data/iself' : '/api/data/iself/self'
  const res = await api.get<{
    success: boolean
    message?: string
    data?: IselfUsageRow[]
  }>(endpoint, { params })
  return res.data
}

export async function getModelAvailability(
  minutes: ModelAvailabilityMinutes = 10
) {
  const res = await api.get<{
    success: boolean
    message?: string
    data?: ModelAvailabilityData
  }>('/api/perf-metrics/availability', { params: { minutes } })
  return res.data
}

// ----------------------------------------------------------------------------
// System Monitoring
// ----------------------------------------------------------------------------

// Hourly consumption per username, or per organization path of `dimension`.
export async function getUserQuotaDataByUsers(
  params: UsageOrgFilter & {
    start_timestamp: number
    end_timestamp: number
    dimension?: UserChartsDimension
  }
) {
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    '/api/data/users',
    { params }
  )
  return res.data
}

// Organization paths for the user analytics filters.
export async function getUsageOrgOptions() {
  const res = await api.get<{
    success: boolean
    message?: string
    data?: UsageOrgOption[]
  }>('/api/data/org-options')
  return res.data
}

export async function getFlowQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  isAdmin = false
) {
  const endpoint = isAdmin ? '/api/data/flow' : '/api/data/flow/self'
  const res = await api.get<{
    success: boolean
    data?: FlowQuotaDataItem[]
    message?: string
  }>(endpoint, { params })
  return res.data
}

// Get uptime monitoring status for all services
export async function getUptimeStatus() {
  const res = await api.get<{ success: boolean; data: UptimeGroupResult[] }>(
    '/api/uptime/status'
  )
  return res.data
}
