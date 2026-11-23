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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Input } from '@/components/ui/input'
import { getChannelTypeLabel } from '@/features/channels/lib/channel-utils'
import {
  getCapabilityTargets,
  saveChannelModelCostMultiplier,
} from '@/features/models/api'
import { capabilityTargetsQueryKeys } from '@/features/models/lib'
import type { CapabilityTargetChannel } from '@/features/models/types'
import { ModelPriceCell } from '@/features/pricing/components/model-price-cell'
import { getServerErrorMessage } from '@/lib/server-error-message'

import type { ModelPricingEntry } from './api'
import { modelPricingDisplay } from './pricing'

function ChannelMultiplierRow(props: {
  modelName: string
  channel: CapabilityTargetChannel
  entry: ModelPricingEntry
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const stored =
    props.channel.cost_multiplier > 0
      ? String(props.channel.cost_multiplier)
      : ''
  const [draft, setDraft] = useState(stored)
  const [error, setError] = useState('')

  useEffect(() => setDraft(stored), [stored])

  const save = useMutation({
    mutationFn: async (multiplier: number) => {
      const response = await saveChannelModelCostMultiplier({
        channel_id: props.channel.id,
        model: props.modelName,
        multiplier,
      })
      if (!response.success) {
        throw new Error(response.message || t('Operation failed'))
      }
    },
    onSuccess: async () => {
      setError('')
      await queryClient.invalidateQueries({
        queryKey: capabilityTargetsQueryKeys.all,
      })
      await queryClient.invalidateQueries({ queryKey: ['channels'] })
      toast.success(t('Cost multiplier saved'))
    },
    onError: (saveError) => {
      setError(
        getServerErrorMessage(saveError, t('Failed to save cost multiplier'))
      )
    },
  })

  const commit = () => {
    const trimmed = draft.trim()
    const next = trimmed === '' ? 0 : Number(trimmed)
    if (!Number.isFinite(next) || next < 0) {
      setError(t('Enter a positive multiplier, or leave it empty to use 1.'))
      return
    }
    if (next === props.channel.cost_multiplier) {
      setError('')
      return
    }
    save.mutate(next)
  }

  const parsed = draft.trim() === '' ? 1 : Number(draft)
  const multiplier =
    Number.isFinite(parsed) && parsed > 0 ? parsed : 1

  const baseModel = modelPricingDisplay(props.entry)
  const isExpression =
    baseModel.billing_mode === 'tiered_expr' ||
    Boolean(baseModel.billing_expr && baseModel.billing_mode !== 'ratio')
  const scaledModel = {
    ...baseModel,
    model_ratio: baseModel.model_ratio * multiplier,
    model_price:
      baseModel.model_price === undefined
        ? undefined
        : baseModel.model_price * multiplier,
  }

  return (
    <tr className='border-b align-middle last:border-0'>
      <td className='px-3 py-2'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-sm font-medium' title={props.channel.name}>
            #{props.channel.id} · {props.channel.name}
          </span>
          <span className='text-muted-foreground rounded-full border px-1.5 py-0.5 text-[11px] leading-none'>
            {t(getChannelTypeLabel(props.channel.type))}
          </span>
        </div>
      </td>
      <td className='px-3 py-2'>
        <div className='flex items-center gap-1.5'>
          <span className='text-muted-foreground text-sm'>×</span>
          <Input
            className='h-7 w-20 px-1.5 text-right tabular-nums'
            inputMode='decimal'
            placeholder='1'
            value={draft}
            disabled={save.isPending}
            aria-label={t('Cost multiplier for channel #{{id}}', {
              id: props.channel.id,
            })}
            aria-invalid={Boolean(error)}
            onChange={(event) => setDraft(event.target.value)}
            onBlur={commit}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                event.currentTarget.blur()
              }
            }}
          />
          {error ? (
            <span
              className='text-destructive max-w-40 truncate text-xs'
              title={error}
              role='alert'
            >
              {error}
            </span>
          ) : null}
        </div>
      </td>
      <td className='px-3 py-2'>
        {isExpression ? (
          <span className='text-muted-foreground text-sm tabular-nums'>
            {t('Base price')} × {multiplier}
          </span>
        ) : (
          <ModelPriceCell
            model={scaledModel}
            options={{ tokenUnit: 'M' }}
            showExpression={false}
          />
        )}
      </td>
    </tr>
  )
}

export function ChannelMultipliersSection(props: {
  modelName: string
  entry: ModelPricingEntry
}) {
  const { t } = useTranslation()
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
    // 这是定价页的辅助区块，加载失败不应触发全局错误 Toast 干扰主流程。
    meta: { errorToast: false },
  })

  const targetItems = targetsQuery.data?.items ?? []
  const channels =
    targetItems.find((item) => item.model_name === props.modelName)?.channels ??
    targetItems[0]?.channels ??
    []

  return (
    <section className='border-t pt-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-sm font-semibold'>{t('Channel cost multipliers')}</h3>
        <span className='text-muted-foreground text-xs'>
          {t('Saved immediately on blur or Enter')}
        </span>
      </div>
      <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
        {t(
          'Model base pricing is shared across channels. Set a per-channel multiplier (default 1×) to scale user charges when requests route through that channel.'
        )}
      </p>
      {!targetsQuery.isPending && channels.length === 0 ? (
        <p className='text-muted-foreground mt-2 text-xs'>
          {t('No enabled channel serves this model.')}
        </p>
      ) : null}
      {!targetsQuery.isPending && channels.length > 0 ? (
        <div className='mt-2 overflow-x-auto rounded-md border'>
          <table className='w-full min-w-[560px] border-collapse text-sm'>
            <thead>
              <tr className='bg-muted/50 border-b text-left'>
                <th className='text-muted-foreground px-3 py-2 text-xs font-medium'>
                  {t('Channel')}
                </th>
                <th className='text-muted-foreground px-3 py-2 text-xs font-medium'>
                  {t('Multiplier')}
                </th>
                <th className='text-muted-foreground px-3 py-2 text-xs font-medium'>
                  {t('Effective channel price')}
                </th>
              </tr>
            </thead>
            <tbody>
              {channels.map((channel) => (
                <ChannelMultiplierRow
                  key={channel.id}
                  modelName={props.modelName}
                  channel={channel}
                  entry={props.entry}
                />
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  )
}
