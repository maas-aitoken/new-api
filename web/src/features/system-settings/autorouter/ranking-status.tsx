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
import { useQueryClient } from '@tanstack/react-query'
import { RefreshCw, TriangleAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { SystemTaskStatus } from '../types'
import {
  AUTOROUTER_RANKING_KEY,
  useAutoRouterStatus,
  useRefreshAutoRouterRanking,
  useRestoreAutoRouterRanking,
} from './hooks'

const taskVariants = {
  pending: 'warning',
  running: 'info',
  succeeded: 'success',
  failed: 'danger',
} as const satisfies Record<SystemTaskStatus, string>

/** Whether the ranking is current, where it comes from, how the last refresh ended, and the two actions. */
export function RankingStatus() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const status = useAutoRouterStatus()
  const refresh = useRefreshAutoRouterRanking()
  const restore = useRestoreAutoRouterRanking()
  const [confirmingRestore, setConfirmingRestore] = useState(false)
  const [confirmingRefresh, setConfirmingRefresh] = useState(false)

  const data = status.data?.data
  const task = data?.rank_refresh
  const refreshing = task?.status === 'pending' || task?.status === 'running'
  // 手工编辑保存时在 rank_sources 里记下 edited_at；刷新和恢复内置都会把它连同改动一起换掉。
  const edited = Boolean(data?.rank_sources?.edited_at)

  // The viewer holds the ranking from before the refresh until it is told to reload.
  const wasRefreshing = useRef(false)
  useEffect(() => {
    if (wasRefreshing.current && !refreshing) {
      void queryClient.invalidateQueries({ queryKey: AUTOROUTER_RANKING_KEY })
    }
    wasRefreshing.current = refreshing
  }, [refreshing, queryClient])

  if (!data) {
    if (status.isError) {
      return (
        <p className='text-destructive text-sm'>
          {t('Failed to load the ranking status')}
        </p>
      )
    }
    return <Skeleton className='h-32 w-full' />
  }

  const sources = Object.entries(data.rank_sources ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  )
  const staleMetrics =
    task?.status === 'succeeded' ? (task.result?.stale_metrics ?? []) : []

  return (
    <div className='space-y-4 rounded-lg border p-4'>
      <div className='flex flex-wrap items-center gap-2'>
        <h4 className='text-sm font-semibold'>{t('Model ranking')}</h4>
        <StatusBadge
          label={data.ranking_current ? t('Up to date') : t('Out of date')}
          variant={data.ranking_current ? 'success' : 'warning'}
          copyable={false}
        />
        {edited ? (
          <StatusBadge
            label={t('Edited by hand')}
            variant='info'
            copyable={false}
          />
        ) : null}
      </div>

      {data.ranking_current ? null : (
        <Alert>
          <TriangleAlert aria-hidden='true' />
          <AlertTitle>{t('The stored ranking is out of date')}</AlertTitle>
          <AlertDescription>
            {t(
              'It was built for an older set of task types, so the built-in ranking is used until the next refresh.'
            )}
          </AlertDescription>
        </Alert>
      )}

      {sources.length > 0 ? (
        <dl className='grid gap-x-6 gap-y-1 text-xs sm:grid-cols-2'>
          {sources.map(([name, value]) => (
            <div key={name} className='flex min-w-0 justify-between gap-3'>
              <dt className='text-muted-foreground shrink-0'>{name}</dt>
              <dd className='truncate font-mono'>{value}</dd>
            </div>
          ))}
        </dl>
      ) : null}

      <div className='space-y-1 text-sm'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-muted-foreground'>{t('Last refresh')}</span>
          {task ? (
            <>
              <StatusBadge
                label={t(task.status)}
                variant={taskVariants[task.status]}
                copyable={false}
              />
              <span className='text-muted-foreground text-xs'>
                {formatTimestampToDate(task.updated_at)}
              </span>
            </>
          ) : (
            <span>{t('No refresh has run yet')}</span>
          )}
        </div>
        {task?.status === 'failed' && task.error ? (
          <p className='text-destructive font-mono text-xs break-all'>
            {task.error}
          </p>
        ) : null}
        {staleMetrics.length > 0 ? (
          <p className='text-muted-foreground text-xs'>
            {t('Skipped for stale data: {{metrics}}', {
              metrics: staleMetrics.join(', '),
            })}
          </p>
        ) : null}
      </div>

      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          size='sm'
          disabled={!data.rank_keys_ready || refreshing || refresh.isPending}
          onClick={() =>
            edited ? setConfirmingRefresh(true) : refresh.mutate()
          }
        >
          <RefreshCw
            data-icon='inline-start'
            className={cn('size-3.5', refreshing && 'animate-spin')}
            aria-hidden='true'
          />
          {t('Refresh now')}
        </Button>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={refreshing || restore.isPending}
          onClick={() => setConfirmingRestore(true)}
        >
          {t('Restore built-in ranking')}
        </Button>
      </div>
      {data.rank_keys_ready ? null : (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Set the Artificial Analysis key and either the Design Arena or the OpenRouter key below to refresh.'
          )}
        </p>
      )}

      <ConfirmDialog
        open={confirmingRefresh}
        onOpenChange={setConfirmingRefresh}
        title={t('Replace the hand-edited ranking?')}
        desc={t(
          'A refresh rebuilds the ranking from public leaderboards and replaces the stored one, so your hand edits are lost.'
        )}
        confirmText={t('Refresh now')}
        isLoading={refresh.isPending}
        handleConfirm={() =>
          refresh.mutate(undefined, {
            onSuccess: () => setConfirmingRefresh(false),
          })
        }
      />
      <ConfirmDialog
        open={confirmingRestore}
        onOpenChange={setConfirmingRestore}
        title={t('Restore the built-in ranking?')}
        desc={
          edited
            ? t(
                'The hand-edited ranking is replaced by the one shipped with this version, so your edits are lost.'
              )
            : t(
                'The stored ranking is replaced by the one shipped with this version. If periodic refresh is on, the next scheduled refresh replaces it again.'
              )
        }
        confirmText={t('Restore')}
        isLoading={restore.isPending}
        handleConfirm={() =>
          restore.mutate(undefined, {
            onSuccess: () => setConfirmingRestore(false),
          })
        }
      />
    </div>
  )
}
