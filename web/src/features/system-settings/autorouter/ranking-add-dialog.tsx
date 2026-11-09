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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getEnabledModels } from '@/features/channels/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  listedModels,
  parseScore,
  previewRank,
  reachablePreferences,
  seedAddRows,
  topServedScore,
  type AddRow,
  type RankingDraft,
} from './ranking-draft'
import { preferenceLabel } from './route-log/labels'
import { AUTO_ROUTER_EFFORTS, type CostWeights } from './settings'

type RankingAddDialogProps = {
  draft: RankingDraft
  costWeights: CostWeights
  onOpenChange: (open: boolean) => void
  onAdd: (
    model: string,
    served: boolean,
    scores: Record<string, number | null>
  ) => void
}

/** The offset added to the reference model's scores; empty counts as 0, anything else that is not a number is invalid. */
function parseOffset(text: string): number | undefined {
  if (text.trim() === '') return 0
  const value = Number(text)
  return Number.isFinite(value) ? value : undefined
}

/**
 * Adds one model to several profiles at once. Its scores start from a reference
 * model already in the ranking, plus an offset, and each row shows where the
 * model would land and which preferences could pick it.
 */
export function RankingAddDialog(props: RankingAddDialogProps) {
  const { t } = useTranslation()
  const [model, setModel] = useState('')
  const [reference, setReference] = useState('')
  const [offsetText, setOffsetText] = useState('0')
  const [rows, setRows] = useState<Record<string, AddRow>>(() =>
    seedAddRows(props.draft, '', 0)
  )
  const gateway = useQuery({
    queryKey: ['enabled-models'],
    queryFn: async () => requireServerSuccess(await getEnabledModels()),
  })

  const gatewayModels = (gateway.data?.data ?? []).filter(
    (name) => name !== 'auto'
  )
  const name = model.trim()
  // 与后端判断 served 的口径一致：裸名或任一档位后缀名有启用的渠道就算。
  const served =
    gatewayModels.includes(name) ||
    AUTO_ROUTER_EFFORTS.some((effort) =>
      gatewayModels.includes(`${name}-${effort}`)
    )
  const offset = parseOffset(offsetText)

  const reseed = (nextReference: string, nextOffsetText: string) => {
    const nextOffset = parseOffset(nextOffsetText)
    if (nextOffset !== undefined) {
      setRows(seedAddRows(props.draft, nextReference, nextOffset))
    }
  }
  const updateRow = (profile: string, row: Partial<AddRow>) =>
    setRows((current) => ({
      ...current,
      [profile]: { ...current[profile], ...row },
    }))

  const scores: Record<string, number | null> = {}
  let invalid = false
  for (const [profile, row] of Object.entries(rows)) {
    if (!row.checked) continue
    const score = parseScore(row.scoreText)
    if (score === undefined) invalid = true
    else scores[profile] = score
  }
  const selected = Object.keys(scores).length
  const nameError = name === 'auto'
  const canAdd =
    name !== '' &&
    !nameError &&
    selected > 0 &&
    !invalid &&
    offset !== undefined

  const reachCell = (profile: string, score: number | null) => {
    if (!served) {
      return (
        <span className='text-muted-foreground text-xs'>
          {t('Not on the gateway')}
        </span>
      )
    }
    if (score === null) {
      return (
        <span className='text-muted-foreground text-xs'>
          {t('Fallback only')}
        </span>
      )
    }
    const list = props.draft[profile] ?? []
    const top = topServedScore(list, name)
    const reachable = reachablePreferences(list, name, score, props.costWeights)
    return (
      <div className='space-y-1'>
        <div className='text-muted-foreground text-xs'>
          {top === null || score >= top
            ? t('Best on the gateway')
            : t('{{gap}} below the best on the gateway', {
                gap: (top - score).toFixed(4),
              })}
        </div>
        <div className='flex flex-wrap gap-1'>
          {reachable.length === 0 ? (
            <span className='text-muted-foreground text-xs'>
              {t('No preference compares it')}
            </span>
          ) : (
            reachable.map((preference) => (
              <StatusBadge
                key={preference}
                label={preferenceLabel(t, preference)}
                variant='info'
                copyable={false}
              />
            ))
          )}
        </div>
      </div>
    )
  }

  const columns: StaticDataTableColumn<string>[] = [
    {
      id: 'include',
      header: <span className='sr-only'>{t('Include')}</span>,
      className: 'w-10',
      cell: (profile) => (
        <Checkbox
          checked={rows[profile].checked}
          onCheckedChange={(checked) => updateRow(profile, { checked })}
          aria-label={t('Add to {{profile}}', { profile })}
        />
      ),
    },
    {
      id: 'profile',
      header: t('Task profile'),
      cell: (profile) => {
        const listed = props.draft[profile]?.find(
          (entry) => entry.model === name
        )
        return (
          <div className='space-y-0.5'>
            <div className='font-mono text-xs'>{profile}</div>
            {listed ? (
              <div className='text-muted-foreground text-xs'>
                {t('Listed already, score {{score}}', {
                  score: listed.score === null ? '-' : listed.score.toFixed(4),
                })}
              </div>
            ) : null}
          </div>
        )
      },
    },
    {
      id: 'reference',
      header: t('Reference score'),
      className: 'w-24 text-right',
      cellClassName: 'text-muted-foreground text-right font-mono tabular-nums',
      cell: (profile) => {
        const score = props.draft[profile]?.find(
          (entry) => entry.model === reference
        )?.score
        return score === null || score === undefined ? '-' : score.toFixed(4)
      },
    },
    {
      id: 'score',
      header: t('Score'),
      className: 'w-28',
      cell: (profile) => {
        const row = rows[profile]
        return (
          <Input
            inputMode='decimal'
            value={row.scoreText}
            placeholder={t('Fallback only')}
            aria-label={t('Score in {{profile}}', { profile })}
            aria-invalid={
              row.checked && parseScore(row.scoreText) === undefined
            }
            className='h-7 w-24 text-right font-mono tabular-nums'
            onChange={(event) =>
              updateRow(profile, {
                checked: true,
                scoreText: event.target.value,
              })
            }
          />
        )
      },
    },
    {
      id: 'rank',
      header: t('Rank'),
      className: 'w-16',
      cellClassName: 'tabular-nums',
      cell: (profile) => {
        const score = parseScore(rows[profile].scoreText)
        if (!rows[profile].checked || score === undefined) return '-'
        return previewRank(props.draft[profile] ?? [], name, score)
      },
    },
    {
      id: 'reach',
      header: t('Compared under'),
      cell: (profile) => {
        const score = parseScore(rows[profile].scoreText)
        if (!rows[profile].checked || score === undefined || name === '') {
          return '-'
        }
        return reachCell(profile, score)
      },
    },
  ]

  return (
    <Dialog
      open
      onOpenChange={props.onOpenChange}
      title={t('Add a model to the ranking')}
      description={t(
        'Pick a reference model already in the ranking: each profile where it has a score starts from that score plus the offset. Changing the reference or the offset fills the scores again.'
      )}
      contentClassName='sm:max-w-4xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={!canAdd}
            onClick={() => {
              props.onAdd(name, served, scores)
              props.onOpenChange(false)
            }}
          >
            {t('Add to {{count}} profiles', { count: selected })}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='grid gap-3 sm:grid-cols-[1fr_1fr_8rem]'>
          <div className='space-y-1.5'>
            <Label htmlFor='autorouter-add-model'>{t('Model')}</Label>
            <Combobox
              id='autorouter-add-model'
              aria-label={t('Model')}
              options={gatewayModels.map((value) => ({ value, label: value }))}
              value={model}
              onValueChange={(value) => setModel(value ?? '')}
              allowCustomValue
              openOnFocus={false}
              placeholder={t('Pick a gateway model or type a name')}
              aria-invalid={nameError}
            />
            {nameError ? (
              <p className='text-destructive text-xs'>
                {t('auto cannot be listed in a ranking')}
              </p>
            ) : null}
            {name !== '' && !nameError && gateway.isSuccess && !served ? (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'No enabled channel serves this model, so auto cannot pick it until one does.'
                )}
              </p>
            ) : null}
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='autorouter-add-reference'>
              {t('Reference model')}
            </Label>
            <Combobox
              id='autorouter-add-reference'
              aria-label={t('Reference model')}
              options={listedModels(props.draft).map((value) => ({
                value,
                label: value,
              }))}
              value={reference}
              onValueChange={(value) => {
                setReference(value ?? '')
                reseed(value ?? '', offsetText)
              }}
              placeholder={t('Pick a model in the ranking')}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='autorouter-add-offset'>{t('Offset')}</Label>
            <Input
              id='autorouter-add-offset'
              inputMode='decimal'
              value={offsetText}
              aria-invalid={offset === undefined}
              className='font-mono tabular-nums'
              onChange={(event) => {
                setOffsetText(event.target.value)
                reseed(reference, event.target.value)
              }}
            />
          </div>
        </div>

        <StaticDataTable
          columns={columns}
          data={Object.keys(rows)}
          getRowKey={(profile) => profile}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'A preference compares the models within its cost weight of the best score among the models the gateway serves. Prices, token allowlists and groups are not counted here, so this is an estimate.'
          )}
        </p>
      </div>
    </Dialog>
  )
}
