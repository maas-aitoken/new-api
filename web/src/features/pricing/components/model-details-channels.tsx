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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
} from '@/components/data-table'
import { Badge } from '@/components/ui/badge'

import { formatCostMultiplier } from '../lib/model-helpers'
import type {
  ModelCapability,
  ModelParameter,
  Modality,
  PricingModel,
  SquareChannel,
} from '../types'

type DiffRow = {
  key: string
  label: string
  modelValue: string
  channelValue: string
}

function joinValues(values?: (ModelCapability | Modality)[]): string {
  return values && values.length > 0 ? values.join(', ') : '—'
}

function formatParameter(parameter?: ModelParameter): string {
  if (!parameter) return '—'
  if (parameter.range) return parameter.range
  if (parameter.max_items != null) return `≤ ${parameter.max_items}`
  if (parameter.enum_values && parameter.enum_values.length > 0) {
    return parameter.enum_values.join(', ')
  }
  return parameter.type
}


function indexParameters(parameters?: ModelParameter[]) {
  const byName = new Map<string, ModelParameter>()
  for (const parameter of parameters ?? []) {
    byName.set(parameter.name, parameter)
  }
  return byName
}

/**
 * 只列出渠道与模型级声明不一致的维度，便于一眼看出该渠道少了什么。
 * 参数按并集比较：模型声明了但渠道收窄掉了，也会作为差异出现。
 */
function buildDiffRows(
  model: PricingModel,
  channel: SquareChannel,
  labels: {
    capabilities: string
    inputModalities: string
    outputModalities: string
    maxOutputTokens: string
    notDeclared: string
    notSupported: string
  }
): DiffRow[] {
  const rows: DiffRow[] = []
  const push = (key: string, label: string, modelValue: string, channelValue: string) => {
    if (modelValue === channelValue) return
    rows.push({ key, label, modelValue, channelValue })
  }

  push(
    'capabilities',
    labels.capabilities,
    joinValues(model.capabilities),
    joinValues(channel.capabilities)
  )
  push(
    'input_modalities',
    labels.inputModalities,
    joinValues(model.input_modalities),
    joinValues(channel.input_modalities)
  )
  push(
    'output_modalities',
    labels.outputModalities,
    joinValues(model.output_modalities),
    joinValues(channel.output_modalities)
  )
  push(
    'max_output_tokens',
    labels.maxOutputTokens,
    model.max_output_tokens != null ? String(model.max_output_tokens) : '—',
    channel.max_output_tokens != null
      ? String(channel.max_output_tokens)
      : '—'
  )

  const modelParams = indexParameters(model.parameters)
  const channelParams = indexParameters(channel.parameters)
  const names = [...new Set([...modelParams.keys(), ...channelParams.keys()])]
  names.sort()
  for (const name of names) {
    const modelParam = modelParams.get(name)
    const channelParam = channelParams.get(name)
    const modelValue = modelParam
      ? formatParameter(modelParam)
      : labels.notDeclared
    const channelValue = channelParam
      ? formatParameter(channelParam)
      : labels.notSupported
    if (modelValue === channelValue) continue
    rows.push({ key: `param:${name}`, label: name, modelValue, channelValue })
  }

  return rows
}

function ChannelCapabilityCard(props: {
  model: PricingModel
  channel: SquareChannel
}) {
  const { t } = useTranslation()
  const rows = useMemo(
    () =>
      buildDiffRows(props.model, props.channel, {
        capabilities: t('Capabilities'),
        inputModalities: t('Input modalities'),
        outputModalities: t('Output modalities'),
        maxOutputTokens: t('Max output tokens'),
        notDeclared: t('Not declared'),
        notSupported: t('Not supported'),
      }),
    [props.model, props.channel, t]
  )

  return (
    <section className='bg-card/60 space-y-3 rounded-xl border p-4 shadow-sm'>
      <div className='flex flex-wrap items-center gap-2'>
        <span className='font-mono text-sm font-semibold'>
          #{props.channel.id}
        </span>
        <Badge variant='secondary' className='tabular-nums'>
          {t('Cost multiplier')} ×{' '}
          {formatCostMultiplier(props.channel.cost_multiplier)}
        </Badge>
      </div>
      {rows.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('Consistent with the model-level declaration.')}
        </p>
      ) : (
        <StaticDataTable
          className={tableStyles.sectionContainer}
          headerRowClassName={tableStyles.mutedHeaderRow}
          data={rows}
          getRowKey={(row) => row.key}
          getRowClassName={() => 'hover:bg-muted/20'}
          columns={[
            {
              id: 'dimension',
              header: t('Dimension'),
              className: 'h-9 w-48',
              cellClassName: tableStyles.topCell,
              cell: (row) => (
                <code className='font-mono text-sm'>{row.label}</code>
              ),
            },
            {
              id: 'model',
              header: t('Model declaration'),
              className: 'h-9',
              cellClassName: tableStyles.topMutedCell,
              cell: (row) => (
                <span className='break-words whitespace-normal'>
                  {row.modelValue}
                </span>
              ),
            },
            {
              id: 'channel',
              header: t('This channel'),
              className: 'h-9',
              cellClassName: tableStyles.topCell,
              cell: (row) => (
                <span className='font-medium break-words whitespace-normal text-amber-700 dark:text-amber-400'>
                  {row.channelValue}
                </span>
              ),
            },
          ]}
        />
      )}
    </section>
  )
}

export function ModelDetailsChannels(props: { model: PricingModel }) {
  const { t } = useTranslation()
  const channels = props.model.channels ?? []

  if (channels.length === 0) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('No enabled channel serves this model.')}
      </p>
    )
  }

  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Each channel can narrow the model-level declaration. Only dimensions that differ from the model declaration are listed.'
        )}
      </p>
      {channels.map((channel) => (
        <ChannelCapabilityCard
          key={channel.id}
          model={props.model}
          channel={channel}
        />
      ))}
    </div>
  )
}