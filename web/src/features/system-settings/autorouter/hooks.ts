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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { useState } from 'react'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { useUpdateOption } from '../hooks/use-update-option'
import type { UpdateOptionRequest } from '../types'
import {
  getAutoRouterRanking,
  getAutoRouterStatus,
  refreshAutoRouterRanking,
  restoreAutoRouterRanking,
  saveAutoRouterRanking,
} from './api'
import type { AutoRouterRankingEdit } from './types'

export const AUTOROUTER_STATUS_KEY = ['autorouter-status']
export const AUTOROUTER_RANKING_KEY = ['autorouter-ranking']

// A leaderboard refresh runs in the background; poll its task while it is active.
const REFRESH_POLL_MS = 3000

export function useAutoRouterStatus() {
  return useQuery({
    queryKey: AUTOROUTER_STATUS_KEY,
    queryFn: async () => requireServerSuccess(await getAutoRouterStatus()),
    refetchInterval: (query) => {
      const refresh = query.state.data?.data.rank_refresh
      const active = refresh?.status === 'pending' || refresh?.status === 'running'
      return active ? REFRESH_POLL_MS : false
    },
  })
}

export function useAutoRouterRanking(enabled: boolean) {
  return useQuery({
    queryKey: AUTOROUTER_RANKING_KEY,
    queryFn: async () => requireServerSuccess(await getAutoRouterRanking()),
    enabled,
  })
}

/**
 * Writes option updates one after another and stops at the first failure.
 * useUpdateOption reports each failure itself; this only refreshes the status
 * afterwards, because it is what tells where a connection value comes from.
 */
export function useSaveAutoRouterOptions() {
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const [saving, setSaving] = useState(false)

  const save = async (updates: UpdateOptionRequest[]) => {
    setSaving(true)
    try {
      for (const update of updates) {
        await updateOption.mutateAsync(update)
      }
      return true
    } catch {
      return false
    } finally {
      setSaving(false)
      await queryClient.invalidateQueries({ queryKey: AUTOROUTER_STATUS_KEY })
    }
  }

  return { save, saving }
}

export function useRefreshAutoRouterRanking() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () =>
      requireServerSuccess(await refreshAutoRouterRanking()),
    onSuccess: async (response) => {
      toast.success(
        response.data.created
          ? i18next.t('Leaderboard refresh started')
          : i18next.t('A leaderboard refresh is already running')
      )
      await queryClient.invalidateQueries({ queryKey: AUTOROUTER_STATUS_KEY })
    },
    onError: (error: Error) =>
      handleServerError(
        error,
        i18next.t('Failed to start the leaderboard refresh')
      ),
  })
}

/**
 * Saves a hand-edited ranking. The server turns periodic refresh off in the same
 * write, so the options are reloaded too and the refresh switch shows it.
 */
export function useSaveAutoRouterRanking() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (edit: AutoRouterRankingEdit) =>
      requireServerSuccess(await saveAutoRouterRanking(edit)),
    onSuccess: async () => {
      toast.success(i18next.t('Ranking saved'))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: AUTOROUTER_RANKING_KEY }),
        queryClient.invalidateQueries({ queryKey: AUTOROUTER_STATUS_KEY }),
        queryClient.invalidateQueries({ queryKey: ['system-options'] }),
      ])
    },
    onError: (error: Error) =>
      handleServerError(error, i18next.t('Failed to save the ranking')),
  })
}

export function useRestoreAutoRouterRanking() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () =>
      requireServerSuccess(await restoreAutoRouterRanking()),
    onSuccess: async () => {
      toast.success(i18next.t('Built-in ranking restored'))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: AUTOROUTER_STATUS_KEY }),
        queryClient.invalidateQueries({ queryKey: AUTOROUTER_RANKING_KEY }),
      ])
    },
    onError: (error: Error) =>
      handleServerError(
        error,
        i18next.t('Failed to restore the built-in ranking')
      ),
  })
}
