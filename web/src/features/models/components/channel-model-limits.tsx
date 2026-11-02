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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  useEffect,
  useId,
  useState,
  type ChangeEvent,
  type FocusEvent,
  type KeyboardEvent,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { saveChannelModelLimit } from '../api'
import {
  capabilityTargetsQueryKeys,
  channelModelCapabilityQueryKeys,
} from '../lib'
import type {
  CapabilityTargetChannel,
  ChannelModelCapacity,
  ChannelModelLimit,
} from '../types'

type LimitDraft = { rpm: string; tpm: string; max_concurrency: string }

/** 0 表示不限，输入框里显示为空。 */
function toDraft(rpm: number, tpm: number, maxConcurrency: number): LimitDraft {
  return {
    rpm: rpm ? String(rpm) : '',
    tpm: tpm ? String(tpm) : '',
    max_concurrency: maxConcurrency ? String(maxConcurrency) : '',
  }
}

/**
 * <渠道, 模型> 的 RPM / TPM / 并发额度，三个输入框作为一组：焦点离开这一组
 * 或按回车时一起保存，避免逐个保存时后保存的刷新冲掉正在输入的另一项。
 * 留空表示不限。额度存在渠道 setting 的 model_limits 里（键是 model_mapping
 * 链尾的上游名），网关只统计不拦截，执行准入的是调度器。
 */
export function ChannelModelLimitInputs(props: {
  channel: CapabilityTargetChannel
  modelName: string
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const hintId = useId()
  // 按数值同步：props 里的 model_limit 每次渲染都是新对象。
  const storedRpm = props.channel.model_limit.rpm ?? 0
  const storedTpm = props.channel.model_limit.tpm ?? 0
  const storedConcurrency = props.channel.model_limit.max_concurrency ?? 0
  const [draft, setDraft] = useState<LimitDraft>(() =>
    toDraft(storedRpm, storedTpm, storedConcurrency)
  )
  const [error, setError] = useState('')

  useEffect(
    () => setDraft(toDraft(storedRpm, storedTpm, storedConcurrency)),
    [storedRpm, storedTpm, storedConcurrency]
  )

  const save = useMutation({
    mutationFn: async (limit: Required<ChannelModelLimit>) => {
      const response = await saveChannelModelLimit({
        channel_id: props.channel.id,
        model: props.modelName,
        ...limit,
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
      await queryClient.invalidateQueries({
        queryKey: channelModelCapabilityQueryKeys.all,
      })
      toast.success(t('Capacity limits saved'))
    },
    onError: (saveError) => {
      setError(
        getServerErrorMessage(saveError, t('Failed to save capacity limits'))
      )
    },
  })

  const commit = () => {
    // 保存期间输入框被禁用会再触发一次 blur，这里直接忽略。
    if (save.isPending) {
      return
    }
    const parsed: Record<keyof LimitDraft, number> = {
      rpm: 0,
      tpm: 0,
      max_concurrency: 0,
    }
    for (const field of ['rpm', 'tpm', 'max_concurrency'] as const) {
      const trimmed = draft[field].trim()
      const value = trimmed === '' ? 0 : Number(trimmed)
      if (!Number.isInteger(value) || value < 0) {
        setError(t('Enter a whole number, or leave it empty for no limit.'))
        return
      }
      parsed[field] = value
    }
    if (
      parsed.rpm === storedRpm &&
      parsed.tpm === storedTpm &&
      parsed.max_concurrency === storedConcurrency
    ) {
      setError('')
      return
    }
    save.mutate(parsed)
  }

  const onGroupBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      commit()
    }
  }

  const inputProps = (field: keyof LimitDraft, label: string) => ({
    className: 'h-7 w-20 px-1.5 text-right tabular-nums',
    inputMode: 'numeric' as const,
    placeholder: '∞',
    value: draft[field],
    disabled: save.isPending,
    'aria-label': t('{{limit}} limit for channel #{{id}}', {
      limit: label,
      id: props.channel.id,
    }),
    'aria-describedby': hintId,
    'aria-invalid': Boolean(error),
    onChange: (event: ChangeEvent<HTMLInputElement>) =>
      setDraft((current) => ({ ...current, [field]: event.target.value })),
    onKeyDown: (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === 'Enter') {
        event.preventDefault()
        commit()
      }
    },
  })

  return (
    <div className='flex min-w-0 flex-col gap-1'>
      <div
        className='text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-xs'
        onBlur={onGroupBlur}
      >
        <label className='flex items-center gap-1'>
          <span>RPM</span>
          <Input {...inputProps('rpm', 'RPM')} />
        </label>
        <label className='flex items-center gap-1'>
          <span>TPM</span>
          <Input {...inputProps('tpm', 'TPM')} />
        </label>
        <label className='flex items-center gap-1'>
          <span>{t('Concurrency')}</span>
          <Input {...inputProps('max_concurrency', t('Concurrency'))} />
        </label>
        <span id={hintId} className='sr-only'>
          {t('Leave empty for no limit.')}
        </span>
      </div>
      {error ? (
        <span className='text-destructive text-xs' role='alert'>
          {error}
        </span>
      ) : null}
    </div>
  )
}

/** 一份额度此刻的用量与剩余：<渠道, 模型> 的，或资源池页容量池的。 */
export type CapacityUsage = Pick<
  ChannelModelCapacity,
  'limit' | 'rpm_used' | 'tpm_used' | 'in_flight' | 'remaining'
>

/**
 * 实时用量。设了额度的维度显示"已用 / 额度 · 剩余"，剩余为 0 时标红；
 * 没设额度的只显示已用。label 是整行的可访问名称，默认按渠道号生成。
 */
export function ChannelModelCapacityLine(props: {
  capacity?: CapacityUsage & { channel_id?: number }
  label?: string
}) {
  const { t, i18n } = useTranslation()
  if (!props.capacity) {
    return null
  }
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const capacity = props.capacity
  const dimensions = [
    {
      key: 'rpm',
      label: 'RPM',
      used: capacity.rpm_used,
      limit: capacity.limit.rpm ?? 0,
      remaining: capacity.remaining.rpm,
      title: undefined,
    },
    {
      key: 'tpm',
      label: 'TPM',
      used: capacity.tpm_used,
      limit: capacity.limit.tpm ?? 0,
      remaining: capacity.remaining.tpm,
      title: t(
        'In-flight requests count only their estimated input until they finish.'
      ),
    },
    {
      key: 'concurrency',
      label: t('In flight'),
      used: capacity.in_flight,
      limit: capacity.limit.max_concurrency ?? 0,
      remaining: capacity.remaining.concurrency,
      title: undefined,
    },
  ]
  return (
    <div
      className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs tabular-nums'
      aria-label={
        props.label ??
        t('Live usage for channel #{{id}}', { id: capacity.channel_id })
      }
    >
      {dimensions.map((dimension) => (
        <span key={dimension.key} title={dimension.title}>
          {dimension.label} {formatCompactNumber(dimension.used, locale)}
          {dimension.limit > 0 ? (
            <>
              {' / '}
              {formatCompactNumber(dimension.limit, locale)}
              {' · '}
              <span
                className={cn(dimension.remaining === 0 && 'text-destructive')}
              >
                {t('{{value}} left', {
                  value: formatCompactNumber(dimension.remaining, locale),
                })}
              </span>
            </>
          ) : null}
        </span>
      ))}
    </div>
  )
}
