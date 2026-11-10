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
import { RefreshCw } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getCapabilityGateEvents } from '../api'
import type {
  CapabilityGateEventAction,
  CapabilityGateEventDimension,
} from '../types'

const PAGE_SIZE = 20
const ALL_ACTIONS = 'all'

const actionVariants: Record<
  CapabilityGateEventAction,
  'danger' | 'warning' | 'info' | 'success'
> = {
  passed: 'success',
  filtered: 'info',
  soft: 'info',
  rejected: 'danger',
  would_filter: 'warning',
  would_reject: 'danger',
}

const actionLabels: Record<CapabilityGateEventAction, string> = {
  passed: 'Passed',
  soft: 'Recorded only',
  filtered: 'Passed (channels dropped)',
  rejected: 'Rejected',
  would_filter: 'Would drop channels (shadow)',
  would_reject: 'Would reject (shadow)',
}

function countRejected(raw: string): number {
  return raw ? raw.split(',').filter(Boolean).length : 0
}

function parseDimensions(raw: string): CapabilityGateEventDimension[] {
  try {
    const parsed = JSON.parse(raw) as unknown
    return Array.isArray(parsed)
      ? (parsed as CapabilityGateEventDimension[])
      : []
  } catch {
    return []
  }
}

function describeValue(value: unknown): string {
  if (value === null || value === undefined) return '-'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value)
  }
  if (Array.isArray(value)) return value.join(', ') || '-'
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>
    if ('min' in record && 'max' in record) {
      return `[${record.min}, ${record.max}]`
    }
    if ('max' in record) return `max ${record.max}`
  }
  return JSON.stringify(value)
}

export function CapabilityGateEventLogs() {
  const { t } = useTranslation()
  const [modelDraft, setModelDraft] = useState('')
  const [actionDraft, setActionDraft] = useState<
    CapabilityGateEventAction | typeof ALL_ACTIONS
  >(ALL_ACTIONS)
  const [requestDraft, setRequestDraft] = useState('')
  const [modelFilter, setModelFilter] = useState('')
  const [actionFilter, setActionFilter] = useState<
    CapabilityGateEventAction | typeof ALL_ACTIONS
  >(ALL_ACTIONS)
  const [requestFilter, setRequestFilter] = useState('')
  const [page, setPage] = useState(1)

  const eventsQuery = useQuery({
    queryKey: [
      'capability-gate-events',
      page,
      modelFilter,
      actionFilter,
      requestFilter,
    ],
    queryFn: async () => {
      const response = await getCapabilityGateEvents({
        p: page,
        page_size: PAGE_SIZE,
        model: modelFilter || undefined,
        action: actionFilter === ALL_ACTIONS ? undefined : actionFilter,
        request_id: requestFilter || undefined,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load event logs'))
      }
      return response.data
    },
    placeholderData: keepPreviousData,
    retry: false,
  })

  const total = eventsQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const events = eventsQuery.data?.items ?? []

  let content: ReactNode
  if (eventsQuery.isPending) {
    content = (
      <div className='space-y-2'>
        {['first', 'second', 'third'].map((key) => (
          <Skeleton key={key} className='h-12 w-full' />
        ))}
      </div>
    )
  } else if (eventsQuery.isError) {
    content = (
      <div className='text-destructive rounded-md border border-dashed p-8 text-center text-sm'>
        {eventsQuery.error instanceof Error
          ? eventsQuery.error.message
          : t('Failed to load event logs')}
      </div>
    )
  } else if (events.length === 0) {
    content = (
      <div className='text-muted-foreground rounded-md border border-dashed p-8 text-center text-sm'>
        {t('No capability gate events found')}
      </div>
    )
  } else {
    content = (
      <div className='overflow-hidden rounded-md border'>
        <Table className='min-w-[1100px]'>
          <TableHeader>
            <TableRow className='bg-muted/40 hover:bg-muted/40'>
              <TableHead>{t('Time')}</TableHead>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Decision')}</TableHead>
              <TableHead>{t('Channels kept')}</TableHead>
              <TableHead>{t('Rejected channels')}</TableHead>
              <TableHead className='min-w-[320px]'>
                {t('Violated dimensions')}
              </TableHead>
              <TableHead className='min-w-[220px]'>{t('Request ID')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {events.map((event) => {
              const dimensions = parseDimensions(event.dimensions)
              // 渠道 ID 列表过长时会被截断，准确数量以 rejected_count 为准。
              const shownRejected = countRejected(event.rejected_channels)
              const rejectedTotal = Math.max(event.rejected_count, shownRejected)
              return (
                <TableRow key={event.id}>
                  <TableCell>
                    {formatTimestampToDate(event.created_at)}
                  </TableCell>
                  <TableCell className='font-mono'>{event.model}</TableCell>
                  <TableCell>
                    <StatusBadge
                      label={t(actionLabels[event.action])}
                      variant={actionVariants[event.action] ?? 'neutral'}
                      copyable={false}
                    />
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {event.channels_evaluated
                      ? `${event.channels_evaluated - rejectedTotal} / ${event.channels_evaluated}`
                      : '-'}
                  </TableCell>
                  <TableCell className='font-mono text-xs'>
                    {event.rejected_channels || '-'}
                    {rejectedTotal > shownRejected && (
                      <span className='text-muted-foreground'>
                        {' '}
                        +{rejectedTotal - shownRejected}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    <ul className='space-y-1 text-xs'>
                      {dimensions.map((dimension) => (
                        <li key={dimension.dimension}>
                          <span className='font-mono'>
                            {dimension.dimension}
                          </span>
                          <span className='text-muted-foreground'>
                            {' '}
                            {t('requested')}{' '}
                            {describeValue(dimension.required)}
                            {dimension.supported !== undefined &&
                              ` · ${t('supported')} ${describeValue(dimension.supported)}`}
                          </span>
                          <StatusBadge
                            className='ms-1.5'
                            label={
                              dimension.severity === 'hard'
                                ? t('Blocking')
                                : t('Recorded only')
                            }
                            variant={
                              dimension.severity === 'hard' ? 'danger' : 'info'
                            }
                            size='sm'
                            copyable={false}
                          />
                        </li>
                      ))}
                    </ul>
                  </TableCell>
                  <TableCell>
                    {event.request_id ? (
                      <div className='flex items-center gap-1'>
                        <span className='font-mono text-xs break-all'>
                          {event.request_id}
                        </span>
                        <CopyButton
                          value={event.request_id}
                          size='icon'
                          className='size-7'
                          tooltip={t('Copy request ID')}
                          aria-label={t('Copy request ID')}
                        />
                      </div>
                    ) : (
                      '-'
                    )}
                    {event.user_id > 0 && (
                      <p className='text-muted-foreground mt-0.5 text-xs'>
                        {t('User')} {event.user_id} · {t('Token')}{' '}
                        {event.token_id}
                        {event.using_group &&
                          ` · ${t('Group')} ${event.using_group}`}
                      </p>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <section className='bg-card overflow-hidden rounded-lg border shadow-xs'>
      <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
        <div>
          <h3 className='text-sm font-semibold'>{t('Capability gate logs')}</h3>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            {t(
              'Violations are always recorded. Passed decisions are recorded only when Record passed decisions is on.'
            )}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void eventsQuery.refetch()}
          disabled={eventsQuery.isFetching}
        >
          <RefreshCw
            data-icon='inline-start'
            className={cn('size-3.5', eventsQuery.isFetching && 'animate-spin')}
            aria-hidden='true'
          />
          {t('Refresh')}
        </Button>
      </div>

      <div className='space-y-4 p-4 sm:p-5'>
        <div className='grid gap-3 md:grid-cols-4'>
          <div className='space-y-1.5'>
            <Label htmlFor='capability-gate-model'>{t('Model')}</Label>
            <Input
              id='capability-gate-model'
              value={modelDraft}
              placeholder={t('Exact model name')}
              onChange={(event) => setModelDraft(event.target.value)}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='capability-gate-action'>{t('Decision')}</Label>
            <Select
              value={actionDraft}
              onValueChange={(value) =>
                setActionDraft(
                  value as CapabilityGateEventAction | typeof ALL_ACTIONS
                )
              }
            >
              <SelectTrigger id='capability-gate-action'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value={ALL_ACTIONS}>{t('All')}</SelectItem>
                  {(
                    Object.keys(actionLabels) as CapabilityGateEventAction[]
                  ).map((action) => (
                    <SelectItem key={action} value={action}>
                      {t(actionLabels[action])}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='capability-gate-request-id'>{t('Request ID')}</Label>
            <Input
              id='capability-gate-request-id'
              value={requestDraft}
              placeholder={t('Exact request ID')}
              onChange={(event) => setRequestDraft(event.target.value)}
            />
          </div>
          <div className='flex items-end gap-2'>
            <Button
              type='button'
              onClick={() => {
                setPage(1)
                setModelFilter(modelDraft.trim())
                setActionFilter(actionDraft)
                setRequestFilter(requestDraft.trim())
              }}
            >
              {t('Search')}
            </Button>
            <Button
              type='button'
              variant='outline'
              onClick={() => {
                setModelDraft('')
                setActionDraft(ALL_ACTIONS)
                setRequestDraft('')
                setModelFilter('')
                setActionFilter(ALL_ACTIONS)
                setRequestFilter('')
                setPage(1)
              }}
            >
              {t('Reset')}
            </Button>
          </div>
        </div>

        {content}

        <div className='flex flex-col items-center justify-between gap-3 sm:flex-row'>
          <p className='text-muted-foreground text-xs'>
            {t('{{total}} events', { total })}
          </p>
          <div className='flex items-center gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={page <= 1}
              onClick={() => setPage((current) => Math.max(1, current - 1))}
            >
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-xs tabular-nums'>
              {t('Page {{page}} of {{total}}', { page, total: totalPages })}
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              disabled={page >= totalPages}
              onClick={() =>
                setPage((current) => Math.min(totalPages, current + 1))
              }
            >
              {t('Next')}
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}