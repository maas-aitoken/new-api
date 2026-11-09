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
  AutoRouterRanking,
  AutoRouterRankingEdit,
  AutoRouterRefreshResult,
  AutoRouterResponse,
  AutoRouterStatus,
} from './types'

export async function getAutoRouterStatus() {
  const res = await api.get<AutoRouterResponse<AutoRouterStatus>>(
    '/api/autorouter/status'
  )
  return res.data
}

export async function getAutoRouterRanking() {
  const res = await api.get<AutoRouterResponse<AutoRouterRanking>>(
    '/api/autorouter/ranking'
  )
  return res.data
}

export async function saveAutoRouterRanking(edit: AutoRouterRankingEdit) {
  const res = await api.put<AutoRouterResponse<null>>(
    '/api/autorouter/ranking',
    edit
  )
  return res.data
}

export async function refreshAutoRouterRanking() {
  const res = await api.post<AutoRouterResponse<AutoRouterRefreshResult>>(
    '/api/autorouter/ranking/refresh'
  )
  return res.data
}

export async function restoreAutoRouterRanking() {
  const res = await api.post<AutoRouterResponse<null>>(
    '/api/autorouter/ranking/restore'
  )
  return res.data
}
