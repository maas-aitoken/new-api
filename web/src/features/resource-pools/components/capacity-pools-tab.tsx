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
import { Gauge, Pencil, Plus, Trash2, UserPlus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatCompactNumber } from '@/lib/format'

import { ChannelModelCapacityLine } from '../../models/components/channel-model-limits'
import {
  deleteCapacityPool,
  getCapacityPoolCapacity,
  getCapacityPools,
  removeCapacityPoolMember,
  type CapacityDimensionDetail,
  type CapacityPool,
  type CapacityPoolCapacity,
  type ResourcePoolAccount,
  type ResourcePoolProvider,
  type ResourcePoolSource,
} from '../api'
import { formatResetDistance } from '../pool-metrics'
import { CapacityPoolDialog } from './capacity-pool-dialog'
import { CapacityPoolMemberDialog } from './capacity-pool-member-dialog'
import { capacityPoolQueryKeys } from './capacity-pool-query-keys'

const EXCLUDED_REASON_LABELS: Record<string, string> = {
  disabled: 'Disabled',
  unhealthy: 'Error or banned',
  rate_limited: 'Rate limited',
  exhausted: 'Over threshold',
  exhausted_5h: 'Over threshold (5h)',
}

const STATUS_LABELS: Record<string, string> = {
  fetch_failed: 'Polling the account pool failed',
  source_failed: 'A source of this pool failed',
  empty_scope: 'No accounts in scope',
  config_error: 'Invalid pool config',
  publish_failed: 'Publishing the limit failed',
}

/** 资源池页的「容量池」：按账号数与余量动态算额度，成员共用一个计数桶。 */
export function CapacityPoolsTab(props: {
  provider: ResourcePoolProvider
  sources: ResourcePoolSource[]
  accounts: ResourcePoolAccount[]
  locale?: string
  now: number
}) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<CapacityPool | 'new' | null>(null)
  const pools = useQuery({
    queryKey: capacityPoolQueryKeys.list(props.provider),
    queryFn: () => getCapacityPools(props.provider),
  })
  const capacity = useQuery({
    queryKey: capacityPoolQueryKeys.capacity(props.provider),
    queryFn: () => getCapacityPoolCapacity(props.provider),
    refetchInterval: 5000,
    placeholderData: (previous) => previous,
    meta: { errorToast: false },
  })
  const capacityById = new Map(
    (capacity.data?.items ?? []).map((item) => [item.id, item])
  )

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Channel models in a capacity pool share one budget, recomputed every minute from how many accounts are usable and how much quota they have left. Only the scheduler enforces it.'
          )}
        </p>
        <Button size='sm' onClick={() => setEditing('new')}>
          <Plus />
          {t('Create capacity pool')}
        </Button>
      </div>
      {capacity.isError && (
        <p className='text-muted-foreground text-xs' role='status'>
          {t('Live usage is unavailable right now.')}
        </p>
      )}
      {capacity.data && !capacity.data.shared && (
        <p className='text-muted-foreground text-xs'>
          {t('Redis is not configured, so live usage reflects only this node.')}
        </p>
      )}
      {pools.data?.length === 0 && (
        <EmptyState
          icon={Gauge}
          bordered
          title={t('No capacity pools')}
          description={t(
            'Create a pool from some sources of this provider, then add the channel models that use those accounts.'
          )}
        />
      )}
      {(pools.data ?? []).map((pool) => (
        <CapacityPoolCard
          key={pool.id}
          pool={pool}
          capacity={capacityById.get(pool.id)}
          sources={props.sources}
          locale={props.locale}
          now={props.now}
          onEdit={() => setEditing(pool)}
        />
      ))}
      <CapacityPoolDialog
        open={editing !== null}
        onOpenChange={(open) => !open && setEditing(null)}
        provider={props.provider}
        pool={editing === 'new' ? null : editing}
        sources={props.sources}
        accounts={props.accounts}
      />
    </div>
  )
}

function CapacityPoolCard(props: {
  pool: CapacityPool
  capacity?: CapacityPoolCapacity
  sources: ResourcePoolSource[]
  locale?: string
  now: number
  onEdit: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [addingMembers, setAddingMembers] = useState(false)
  const pool = props.pool
  const capacity = props.capacity
  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: capacityPoolQueryKeys.provider(pool.provider),
    })
  const remove = useMutation({
    mutationFn: () => deleteCapacityPool(pool.id),
    onSuccess: async () => {
      await invalidate()
      setConfirmDelete(false)
      toast.success(t('Capacity pool deleted'))
    },
  })
  const removeMember = useMutation({
    mutationFn: (member: { channelId: number; upstreamModel: string }) =>
      removeCapacityPoolMember(pool.id, member.channelId, member.upstreamModel),
    onSuccess: invalidate,
  })
  const sourceNames = pool.config.source_ids.map(
    (id) => props.sources.find((source) => source.id === id)?.name ?? `#${id}`
  )
  const memberUsage = new Map(
    (capacity?.members ?? []).map((member) => [
      `${member.channel_id}\u0000${member.upstream_model}`,
      member,
    ])
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex flex-wrap items-center gap-2'>
          {pool.name}
          <CapacitySourceBadge capacity={capacity} />
        </CardTitle>
        <CardDescription>
          {sourceNames.join(', ')}
          {pool.config.groups?.length
            ? ` · ${pool.config.groups.join(', ')}`
            : ''}
        </CardDescription>
        <CardAction className='flex gap-1'>
          <Button
            variant='ghost'
            size='icon'
            aria-label={t('Add members')}
            onClick={() => setAddingMembers(true)}
          >
            <UserPlus />
          </Button>
          <Button
            variant='ghost'
            size='icon'
            aria-label={t('Edit')}
            onClick={props.onEdit}
          >
            <Pencil />
          </Button>
          <Button
            variant='ghost'
            size='icon'
            aria-label={t('Delete')}
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className='space-y-3'>
        {pool.config_error && (
          <Alert variant='destructive'>
            <AlertDescription>{pool.config_error}</AlertDescription>
          </Alert>
        )}
        {capacity && (
          <ChannelModelCapacityLine
            capacity={capacity}
            label={t('Live usage of capacity pool {{name}}', {
              name: pool.name,
            })}
          />
        )}
        {capacity && (
          <CapacityPoolStatusLine
            capacity={capacity}
            locale={props.locale}
            now={props.now}
          />
        )}
        {capacity?.detail && (
          <CapacityPoolDetailLines capacity={capacity} locale={props.locale} />
        )}
        {pool.members.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t(
              'No members yet. Add the channel models that use these accounts.'
            )}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Upstream')}</TableHead>
                <TableHead>{t('Models')}</TableHead>
                <TableHead className='text-right'>RPM</TableHead>
                <TableHead className='text-right'>{t('In flight')}</TableHead>
                <TableHead className='w-12'>
                  <span className='sr-only'>{t('Actions')}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {pool.members.map((member) => {
                const usage = memberUsage.get(
                  `${member.channel_id}\u0000${member.upstream_model}`
                )
                return (
                  <TableRow
                    key={`${member.channel_id}-${member.upstream_model}`}
                  >
                    <TableCell>
                      #{member.channel_id} {member.channel_name}
                    </TableCell>
                    <TableCell className='font-mono text-xs'>
                      {member.upstream_model}
                    </TableCell>
                    <TableCell className='text-xs'>
                      {member.stale ? (
                        <Badge variant='warning'>
                          {t('No model maps here any more')}
                        </Badge>
                      ) : (
                        (member.models ?? []).join(', ')
                      )}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatCompactNumber(usage?.rpm_used ?? 0, props.locale)}
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {usage?.in_flight ?? 0}
                    </TableCell>
                    <TableCell>
                      <Button
                        variant='ghost'
                        size='icon'
                        disabled={removeMember.isPending}
                        aria-label={t('Remove {{model}} of channel #{{id}}', {
                          model: member.upstream_model,
                          id: member.channel_id,
                        })}
                        onClick={() =>
                          removeMember.mutate({
                            channelId: member.channel_id,
                            upstreamModel: member.upstream_model,
                          })
                        }
                      >
                        <Trash2 />
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('Delete capacity pool {{name}}?', { name: pool.name })}
        desc={t(
          'Its members go back to their own channel limits. This cannot be undone.'
        )}
        destructive
        confirmText={t('Delete')}
        isLoading={remove.isPending}
        handleConfirm={() => remove.mutate()}
      />
      <CapacityPoolMemberDialog
        open={addingMembers}
        onOpenChange={setAddingMembers}
        pool={pool}
      />
    </Card>
  )
}

function CapacitySourceBadge(props: { capacity?: CapacityPoolCapacity }) {
  const { t } = useTranslation()
  if (!props.capacity) return null
  if (props.capacity.exhausted) {
    return <Badge variant='destructive'>{t('Exhausted')}</Badge>
  }
  if (props.capacity.source === 'dynamic') {
    return <Badge variant='secondary'>{t('Dynamic limit')}</Badge>
  }
  return <Badge variant='outline'>{t('Floor limit')}</Badge>
}

/** 最近一次计算的结果：正常时显示算了多久以前，失败时说明为什么额度没在更新。 */
function CapacityPoolStatusLine(props: {
  capacity: CapacityPoolCapacity
  locale?: string
  now: number
}) {
  const { t } = useTranslation()
  const status = props.capacity.status
  if (status && status.status !== 'ok') {
    const label = STATUS_LABELS[status.status] ?? status.status
    return (
      <p className='text-warning text-xs' role='status'>
        {t(label)}
        {status.error ? `: ${status.error}` : ''}
        {props.capacity.source === 'dynamic'
          ? ` · ${t('Keeping the last limit until the grace period ends')}`
          : ''}
      </p>
    )
  }
  if (!props.capacity.computed_at) {
    return (
      <p className='text-muted-foreground text-xs'>
        {t('Not computed yet. The floor limit applies.')}
      </p>
    )
  }
  return (
    <p className='text-muted-foreground text-xs'>
      {t('Computed {{time}}', {
        time: formatResetDistance(
          new Date(props.capacity.computed_at * 1000).toISOString(),
          props.now,
          props.locale
        ),
      })}
    </p>
  )
}

function CapacityPoolDetailLines(props: {
  capacity: CapacityPoolCapacity
  locale?: string
}) {
  const { t } = useTranslation()
  const detail = props.capacity.detail
  if (!detail) return null
  const excluded = Object.entries(detail.excluded ?? {})
  const dimensions: { label: string; value: CapacityDimensionDetail }[] = [
    { label: 'RPM', value: detail.rpm },
    { label: 'TPM', value: detail.tpm },
    { label: t('Concurrency'), value: detail.concurrency },
  ]
  const format = (value: number) => formatCompactNumber(value, props.locale)
  return (
    <div className='text-muted-foreground space-y-1 text-xs'>
      <p>
        {t('{{usable}} of {{total}} accounts usable', {
          usable: detail.usable,
          total: detail.accounts,
        })}
        {excluded.length > 0 &&
          ` · ${excluded
            .map(
              ([reason, count]) =>
                `${t(EXCLUDED_REASON_LABELS[reason] ?? reason)} ${count}`
            )
            .join(', ')}`}
      </p>
      <ul className='space-y-0.5'>
        {dimensions.map((dimension) => (
          <li key={dimension.label} className='tabular-nums'>
            {dimension.label}:{' '}
            {dimension.value.unbounded
              ? t('Unlimited')
              : t('burst {{burst}}, pacing {{pace}} ({{paced}} accounts)', {
                  burst: format(dimension.value.burst),
                  pace: format(dimension.value.pace),
                  paced: dimension.value.paced_accounts,
                })}
          </li>
        ))}
      </ul>
      {detail.pacing && (
        <p>
          {detail.learned
            ? t(
                'Learned from our traffic: {{requests}} requests / {{tokens}} tokens per {{unit}}',
                {
                  requests: detail.learned.requests_per_unit.toFixed(2),
                  tokens: format(detail.learned.tokens_per_unit),
                  unit:
                    detail.learned.unit === 'credit'
                      ? t('credit')
                      : t('percentage point'),
                }
              )
            : t('No conversion learned from our traffic yet')}
          {detail.upstream_factor_accounts > 0 &&
            ` · ${t('{{count}} accounts use upstream 7-day totals', {
              count: detail.upstream_factor_accounts,
            })}`}
        </p>
      )}
    </div>
  )
}
