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
  RouteLog,
  RouteLogFilters,
  RouteLogPage,
  RouteLogResponse,
} from './types'

type RouteLogListParams = Partial<RouteLogFilters> & {
  p: number
  page_size: number
}

export async function getRouteLogs(params: RouteLogListParams) {
  const res = await api.get<RouteLogResponse<RouteLogPage>>(
    '/api/auto-route-logs/',
    { params }
  )
  return res.data
}

/** The data is null when no log was recorded for the request. */
export async function getRouteLog(requestId: string) {
  const res = await api.get<RouteLogResponse<RouteLog | null>>(
    `/api/auto-route-logs/${encodeURIComponent(requestId)}`
  )
  return res.data
}
