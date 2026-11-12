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
import { keepPreviousData, useQuery } from '@tanstack/react-query'

import { requireServerSuccess } from '@/lib/server-error-message'

import { getRouteLog, getRouteLogs } from './api'
import type { RouteLogFilters } from './types'

export const ROUTE_LOG_PAGE_SIZE = 20

export const EMPTY_ROUTE_LOG_FILTERS: RouteLogFilters = {
  request_id: '',
  user_id: '',
  model: '',
  task_type: '',
  outcome: '',
  judgment: '',
  start_time: '',
  end_time: '',
}

/** Empty filters are left out of the request instead of being sent as blanks. */
function activeFilters(filters: RouteLogFilters) {
  return Object.fromEntries(
    Object.entries(filters).filter(([, value]) => value !== '')
  ) as Partial<RouteLogFilters>
}

export function useRouteLogs(filters: RouteLogFilters, pageIndex: number) {
  return useQuery({
    queryKey: ['auto-route-logs', filters, pageIndex],
    queryFn: async () =>
      requireServerSuccess(
        await getRouteLogs({
          ...activeFilters(filters),
          p: pageIndex + 1,
          page_size: ROUTE_LOG_PAGE_SIZE,
        })
      ).data,
    placeholderData: keepPreviousData,
    retry: false,
  })
}

/** The log of one request; data is null when none was recorded. */
export function useRouteLog(requestId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['auto-route-log', requestId],
    queryFn: async () =>
      requireServerSuccess(await getRouteLog(requestId)).data,
    enabled: enabled && requestId !== '',
    retry: false,
  })
}
