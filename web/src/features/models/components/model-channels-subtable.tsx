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
import { Link } from '@tanstack/react-router'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { JsonEditor } from '@/components/json-editor'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
} from '@/components/data-table'
import { getChannelTypeLabel } from '@/features/channels/lib/channel-utils'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  deleteChannelModelCapability,
  getCapabilityTargets,
  getChannelModelCapability,
  saveChannelModelCapability,
} from '../api'
import { useChannelModelCapacity } from '../hooks/use-channel-model-capacity'
import { capabilityTargetsQueryKeys } from '../lib'
import { diffCapabilitySpecs } from '../lib/capability-diff'
import type {
  CapabilityTarget,
  CapabilityTargetChannel,
  ChannelModelCapacity,
} from '../types'
import {
  ChannelModelCapacityLine,
  ChannelModelLimitInputs,
} from './channel-model-limits'

type Selection = {
  modelName: string
  channel: CapabilityTargetChannel
}

function formatJson(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function upstreamNote(channel: CapabilityTargetChannel, modelName: string, t: (key: string, opts?: Record<string, unknown>) => string): string {
  const sharedModels = channel.shared_models ?? []
  if (sharedModels.length > 0) {
    return t('Upstream {{model}}; limits shared with {{models}}', {
      model: channel.upstream_model,
      models: sharedModels.join(', '),
    })
  }
  if (channel.upstream_model && channel.upstream_model !== modelName) {
    return t('Upstream {{model}}; aliases of the same upstream share these limits', {
      model: channel.upstream_model,
    })
  }
  return ''
}

function CapacityPoolFollowNote(props: {
  channel: CapabilityTargetChannel
  capacity?: ChannelModelCapacity
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const pool = props.capacity?.capacity_pool
  let sourceLabel = ''
  if (pool?.exhausted) {
    sourceLabel = t('Exhausted')
  } else if (pool?.source === 'dynamic') {
    sourceLabel = t('Dynamic limit')
  } else if (pool) {
    sourceLabel = t('Floor limit')
  }
  const limit = props.capacity?.limit ?? props.channel.model_limit ?? {}
  let poolBadgeVariant: 'danger' | 'blue' | 'neutral' = 'neutral'
  if (pool?.exhausted) {
    poolBadgeVariant = 'danger'
  } else if (pool?.source === 'dynamic') {
    poolBadgeVariant = 'blue'
  }
  return (
    <div className='flex min-w-0 flex-col gap-1 text-xs'>
      <div className='flex flex-wrap items-center gap-2'>
        <span className='font-medium'>
          {t('Capacity pool')} ·{' '}
          {props.channel.capacity_pool_name || `#${props.channel.capacity_pool_id}`}
        </span>
        {sourceLabel ? (
          <StatusBadge
            label={sourceLabel}
            variant={poolBadgeVariant}
            size='sm'
            copyable={false}
          />
        ) : null}
      </div>
      <span className='text-muted-foreground tabular-nums'>
        RPM {limit.rpm ? formatCompactNumber(limit.rpm, locale) : '∞'} · TPM{' '}
        {limit.tpm ? formatCompactNumber(limit.tpm, locale) : '∞'} ·{' '}
        {t('Concurrency')}{' '}
        {limit.max_concurrency
          ? formatCompactNumber(limit.max_concurrency, locale)
          : '∞'}
      </span>
      {pool ? (
        <span className='text-muted-foreground tabular-nums'>
          {t('This channel')}: {t('In flight')}{' '}
          {formatCompactNumber(pool.member_in_flight, locale)} · RPM{' '}
          {formatCompactNumber(pool.member_rpm_used, locale)} · TPM{' '}
          {formatCompactNumber(pool.member_tpm_used, locale)}
        </span>
      ) : null}
      <Link to='/resource-pools' className='text-primary text-xs hover:underline'>
        {t('Manage on the Resource Pools page')}
      </Link>
    </div>
  )
}

function ChannelCapabilityNarrowingDialog(props: {
  selection: Selection | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState('')
  const [serverError, setServerError] = useState('')

  const detailQuery = useQuery({
    queryKey: [
      'channel-model-capabilities',
      props.selection?.channel.id ?? 0,
      props.selection?.modelName ?? '',
    ],
    queryFn: async () => {
      if (!props.selection) {
        throw new Error(t('No channel selected'))
      }
      const response = await getChannelModelCapability({
        channel_id: props.selection.channel.id,
        model: props.selection.modelName,
      })
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('Failed to load capability detail')
        )
      }
      return response.data
    },
    enabled: Boolean(props.selection),
    meta: { errorToast: false },
  })

  const detail = detailQuery.data
  useEffect(() => {
    if (!detail) return
    setDraft(detail.capabilities || '')
    setServerError('')
  }, [detail])

  const baseline = useMemo(() => {
    if (!detail?.model_capabilities) return ''
    return formatJson(detail.model_capabilities)
  }, [detail?.model_capabilities])

  const diffRows = useMemo(
    () => diffCapabilitySpecs(detail?.model_capabilities, detail?.effective),
    [detail?.model_capabilities, detail?.effective]
  )

  const refresh = async () => {
    await queryClient.invalidateQueries({
      queryKey: capabilityTargetsQueryKeys.all,
    })
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!props.selection) return
      const response = await saveChannelModelCapability({
        channel_id: props.selection.channel.id,
        model: props.selection.modelName,
        capabilities: draft,
      })
      if (!response.success) {
        throw new Error(response.message || t('Operation failed'))
      }
    },
    onSuccess: async () => {
      await refresh()
      await detailQuery.refetch()
      toast.success(t('Channel capability saved'))
    },
    onError: (error) => {
      setServerError(
        getServerErrorMessage(error, t('Failed to save channel capability'))
      )
    },
  })

  const reset = useMutation({
    mutationFn: async () => {
      if (!props.selection) return
      const response = await deleteChannelModelCapability({
        channel_id: props.selection.channel.id,
        model: props.selection.modelName,
      })
      if (!response.success) {
        throw new Error(response.message || t('Operation failed'))
      }
    },
    onSuccess: async () => {
      setDraft('')
      await refresh()
      await detailQuery.refetch()
      toast.success(t('Channel now matches model-level capabilities'))
    },
    onError: (error) => {
      setServerError(
        getServerErrorMessage(error, t('Failed to reset channel capability'))
      )
    },
  })

  const open = Boolean(props.selection)
  let dialogBody: ReactNode
  if (detailQuery.isError) {
    dialogBody = (
      <ErrorState
        description={detailQuery.error.message}
        onRetry={() => void detailQuery.refetch()}
      />
    )
  } else if (detailQuery.isPending && open) {
    dialogBody = <LoadingState />
  } else {
    dialogBody = (
        <div className='space-y-5'>
          <section>
            <h4 className='mb-2 text-sm font-semibold'>
              {t('Current effective differences')}
            </h4>
            {diffRows.length === 0 ? (
              <p className='text-muted-foreground text-sm'>
                {t('Same as model-level capability declaration.')}
              </p>
            ) : (
              <StaticDataTable
                className={tableStyles.sectionContainer}
                headerRowClassName={tableStyles.mutedHeaderRow}
                data={diffRows}
                getRowKey={(row) => row.path}
                columns={[
                  {
                    id: 'dimension',
                    header: t('Dimension'),
                    className: 'h-9 w-48',
                    cell: (row) => (
                      <code className='font-mono text-xs'>{row.path}</code>
                    ),
                  },
                  {
                    id: 'model',
                    header: t('Model-level declaration'),
                    className: 'h-9',
                    cellClassName: tableStyles.topMutedCell,
                    cell: (row) => (
                      <span className='break-words whitespace-normal'>
                        {t(row.modelValue)}
                      </span>
                    ),
                  },
                  {
                    id: 'effective',
                    header: t('Channel effective'),
                    className: 'h-9',
                    cellClassName: tableStyles.topCell,
                    cell: (row) => (
                      <span className='font-medium break-words whitespace-normal text-amber-700 dark:text-amber-400'>
                        {t(row.effectiveValue)}
                      </span>
                    ),
                  },
                ]}
              />
            )}
          </section>

          <Collapsible>
            <CollapsibleTrigger className='text-muted-foreground text-xs font-medium hover:underline'>
              {t('Model-level declaration (read-only)')}
            </CollapsibleTrigger>
            <CollapsibleContent>
              <pre className='bg-muted/40 mt-2 max-h-56 overflow-auto rounded-md border p-3 font-mono text-xs leading-relaxed'>
                {baseline || t('No model-level declaration.')}
              </pre>
            </CollapsibleContent>
          </Collapsible>

          <section>
            <h4 className='mb-1 text-sm font-semibold'>
              {t('Channel narrowing JSON')}
            </h4>
            <p className='text-muted-foreground mb-2 text-xs'>
              {t(
                'Only write dimensions that this channel needs to restrict; omitted fields automatically follow the model-level declaration.'
              )}
            </p>
            <JsonEditor
              value={draft}
              onChange={setDraft}
              defaultMode='json'
              keyLabel={t('Dimension')}
              valueLabel={t('Value')}
              emptyMessage={t('No narrowing configured.')}
            />
            {serverError ? (
              <Alert variant='destructive' className='mt-3'>
                <AlertDescription className='break-words'>
                  {serverError}
                </AlertDescription>
              </Alert>
            ) : null}
            <div className='mt-3 flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!detail?.model_capabilities}
                onClick={() =>
                  setDraft(
                    detail?.model_capabilities
                      ? formatJson(detail.model_capabilities)
                      : ''
                  )
                }
              >
                {t('Copy model-level declaration')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => setDraft('')}
              >
                {t('Clear')}
              </Button>
            </div>
          </section>
        </div>
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value && !save.isPending && !reset.isPending) props.onClose()
      }}
      title={t('Channel capability narrowing')}
      description={
        props.selection
          ? `#${props.selection.channel.id} · ${props.selection.channel.name} · ${props.selection.modelName}`
          : ''
      }
      contentClassName='sm:max-w-[760px]'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onClose()}
            disabled={save.isPending || reset.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            variant='outline'
            onClick={() => reset.mutate()}
            disabled={!detail?.configured || save.isPending || reset.isPending}
          >
            {t('Restore model-level capabilities')}
          </Button>
          <Button
            onClick={() => save.mutate()}
            disabled={!open || save.isPending || reset.isPending}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      {dialogBody}
    </Dialog>
  )
}

export function ModelChannelsSubtable(props: {
  modelName: string
  onDeclareCapabilities: () => void
}) {
  const { t } = useTranslation()
  const [selection, setSelection] = useState<Selection | null>(null)

  const targetsQuery = useQuery({
    queryKey: capabilityTargetsQueryKeys.list({
      model: props.modelName,
      p: 1,
      page_size: 20,
    }),
    queryFn: async () => {
      const response = await getCapabilityTargets({
        model: props.modelName,
        p: 1,
        page_size: 20,
      })
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('Failed to load serving channels')
        )
      }
      return response.data
    },
    meta: { errorToast: false },
  })

  const target: CapabilityTarget | undefined = useMemo(() => {
    const items = targetsQuery.data?.items ?? []
    return items.find((item) => item.model_name === props.modelName) ?? items[0]
  }, [targetsQuery.data, props.modelName])

  const channels = target?.channels ?? []
  const declared = Boolean(target?.declared)
  const channelIds = channels.map((channel) => channel.id)
  const capacityQuery = useChannelModelCapacity(props.modelName, channelIds)
  const capacityByChannel = new Map(
    (capacityQuery.data?.items ?? []).map((item) => [item.channel_id, item])
  )

  if (targetsQuery.isError) {
    return (
      <ErrorState
        description={targetsQuery.error.message}
        onRetry={() => void targetsQuery.refetch()}
      />
    )
  }
  if (targetsQuery.isPending) {
    return <LoadingState />
  }
  if (channels.length === 0) {
    return (
      <EmptyState title={t('No enabled channel serves this model.')} />
    )
  }

  return (
    <div className='px-2 py-1'>
      <div className='overflow-x-auto rounded-md border'>
        <table className='w-full min-w-[860px] border-collapse text-sm'>
          <thead>
            <tr className='bg-muted/50 border-b text-left'>
              <th className='text-muted-foreground w-[24%] px-3 py-2 text-xs font-medium'>
                {t('Serving channels')}
              </th>
              <th className='text-muted-foreground w-[30%] px-3 py-2 text-xs font-medium'>
                {t('Rate & concurrency limits')}
              </th>
              <th className='text-muted-foreground w-[24%] px-3 py-2 text-xs font-medium'>
                {t('Live usage / in flight')}
              </th>
              <th className='text-muted-foreground w-[22%] px-3 py-2 text-xs font-medium'>
                {t('Capabilities')}
              </th>
            </tr>
          </thead>
          <tbody>
            {channels.map((channel) => {
              const note = upstreamNote(channel, props.modelName, t)
              return (
                <tr key={channel.id} className='border-b align-middle last:border-0'>
                  <td className='px-3 py-2.5'>
                    <div className='flex min-w-0 flex-col gap-0.5'>
                      <div className='flex flex-wrap items-center gap-2'>
                        <span className='font-medium' title={channel.name}>
                          #{channel.id} · {channel.name}
                        </span>
                        <span className='text-muted-foreground rounded-full border px-1.5 py-0.5 text-[11px] leading-none'>
                          {t(getChannelTypeLabel(channel.type))}
                        </span>
                      </div>
                      {note ? (
                        <span
                          className='text-muted-foreground truncate text-xs'
                          title={note}
                        >
                          {note}
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className='px-3 py-2.5'>
                    {channel.capacity_pool_id ? (
                      <CapacityPoolFollowNote
                        channel={channel}
                        capacity={capacityByChannel.get(channel.id)}
                      />
                    ) : (
                      <ChannelModelLimitInputs
                        channel={channel}
                        modelName={props.modelName}
                      />
                    )}
                  </td>
                  <td className='px-3 py-2.5'>
                    <ChannelModelCapacityLine
                      capacity={capacityByChannel.get(channel.id)}
                    />
                  </td>
                  <td className='px-3 py-2.5'>
                    {!declared ? (
                      <div className='flex flex-wrap items-center gap-2'>
                        <span className='text-muted-foreground text-xs'>
                          {t('No model-level declaration')}
                        </span>
                        <Button
                          variant='link'
                          size='sm'
                          className='h-auto px-1'
                          onClick={props.onDeclareCapabilities}
                        >
                          {t('Declare capabilities')}
                        </Button>
                      </div>
                    ) : (
                      <div className='flex flex-wrap items-center gap-2'>
                        <StatusBadge
                          label={
                            channel.configured
                              ? t('Capability narrowed')
                              : t('Same as model capabilities')
                          }
                          variant={channel.configured ? 'amber' : 'neutral'}
                          size='sm'
                          copyable={false}
                        />
                        <Button
                          size='sm'
                          variant='outline'
                          className='h-7'
                          onClick={() =>
                            setSelection({
                              modelName: props.modelName,
                              channel,
                            })
                          }
                        >
                          {channel.configured
                            ? t('Edit narrowing')
                            : t('Configure narrowing')}
                        </Button>
                      </div>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {capacityQuery.isError ? (
        <p className='text-muted-foreground mt-1.5 text-xs'>
          {t('Live usage is unavailable right now.')}
        </p>
      ) : null}
      {capacityQuery.data && !capacityQuery.data.shared ? (
        <p className='text-muted-foreground mt-1.5 text-xs'>
          {t('Redis is not configured, so live usage reflects only this node.')}
        </p>
      ) : null}

      <ChannelCapabilityNarrowingDialog
        selection={selection}
        onClose={() => setSelection(null)}
      />
    </div>
  )
}
