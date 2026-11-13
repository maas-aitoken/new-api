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
import type { TFunction } from 'i18next'
import { ListOrdered } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { cn } from '@/lib/utils'

import {
  formatScore,
  ladderOutcomeLabel,
  previewNoteLabel,
  previewOutcomeLabel,
  skipReasonLabel,
} from './labels'
import { StepFrame, ToneBadge } from './route-process-parts'
import type {
  RoutePick,
  RoutePickCandidate,
  RoutePreviousPressure,
  RouteTraceStep,
} from './types'

const priceFormat = { digitsLarge: 4, digitsSmall: 6, abbreviate: false }

/** 倍率从哪来：调度器预览、会话亲和绑定的渠道，或按原生选渠道规则的命中概率取的期望。 */
function costSourceLabel(t: TFunction, row: RoutePickCandidate): string {
  switch (row.cost_source) {
    case 'scheduler':
      return t('Scheduler picks channel #{{id}}', { id: row.channel_id })
    case 'affinity':
      return t('Session bound to channel #{{id}}', { id: row.channel_id })
    default:
      if (row.channel_id) {
        return t('Only channel #{{id}}', { id: row.channel_id })
      }
      return t('Expected over {{count}} channels', { count: row.channels ?? 0 })
  }
}

/** The candidates the quality and price comparison weighed, the chosen one marked. */
export function PickTable(props: { pick: RoutePick }) {
  const { t } = useTranslation()
  // 有估算时比较的是"用户价 × 首次会落到的渠道的费用倍率"，多出两列；旧日志没有这两列。
  const comparesCost = props.pick.band.some(
    (row) => (row.cost_multiplier ?? 0) > 0
  )
  // 有容量溢价时多一列：池子越接近额度上限，比较成本越贵。
  const hasCapacityPremium = props.pick.band.some(
    (row) => (row.capacity_premium ?? 0) > 0
  )
  // 这次没拿到调度器预览的回答时说明原因，所有倍率都按原生规则算。
  const previewOutcome = previewOutcomeLabel(t, props.pick.preview)
  const costColumns: StaticDataTableColumn<RoutePickCandidate>[] = [
    {
      id: 'cost_multiplier',
      header: t('Cost multiplier'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) =>
        row.cost_multiplier ? (
          <div className='flex flex-col items-end'>
            <span>× {formatScore(row.cost_multiplier)}</span>
            <span className='text-muted-foreground text-xs font-normal'>
              {costSourceLabel(t, row)}
            </span>
            {row.preview && (
              <span className='text-muted-foreground text-xs font-normal'>
                {previewNoteLabel(t, row.preview)}
              </span>
            )}
          </div>
        ) : (
          '-'
        ),
    },
    ...(hasCapacityPremium
      ? [
          {
            id: 'capacity_premium',
            header: t('Capacity premium'),
            className: 'h-9 text-right',
            cellClassName: tableStyles.topNumericCell,
            cell: (row: RoutePickCandidate) =>
              row.capacity_premium ? (
                <div className='flex flex-col items-end'>
                  <span>× {formatScore(row.capacity_premium)}</span>
                  <span className='text-muted-foreground text-xs font-normal'>
                    {t('Pool pressure')}{' '}
                    {Math.round((row.capacity_pressure ?? 0) * 100)}%
                  </span>
                </div>
              ) : (
                '-'
              ),
          },
        ]
      : []),
    {
      id: 'cost',
      header: t('Compared cost'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) =>
        formatBillingCurrencyFromUSD(
          row.cost_multiplier ? (row.cost ?? 0) : row.price,
          priceFormat
        ),
    },
  ]
  const columns: StaticDataTableColumn<RoutePickCandidate>[] = [
    {
      id: 'model',
      header: t('Model'),
      className: 'h-9',
      cellClassName: 'py-2',
      cell: (row) => (
        <div className='flex flex-wrap items-center gap-1.5'>
          <span className='font-mono'>{row.model}</span>
          {row.model === props.pick.chosen && (
            <ToneBadge tone='success' label={t('Chosen')} />
          )}
        </div>
      ),
    },
    {
      id: 'score',
      header: t('Leaderboard score'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) => formatScore(row.score),
    },
    {
      id: 'price',
      header: t('Price per million tokens'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) => formatBillingCurrencyFromUSD(row.price, priceFormat),
    },
    ...(comparesCost ? costColumns : []),
    {
      id: 'discount',
      header: t('Price discount'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) => formatScore(row.discount),
    },
    {
      id: 'utility',
      header: t('Score after discount'),
      className: 'h-9 text-right',
      cellClassName: cn(tableStyles.topNumericCell, 'font-semibold'),
      cell: (row) => formatScore(row.utility),
    },
  ]

  return (
    <div className='space-y-1'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Candidates within {{weight}} of the best score, ranked by score after the price discount.',
          { weight: formatScore(props.pick.weight) }
        )}
        {comparesCost
          ? ` ${t('The discount uses the compared cost: the user price times the cost multiplier of the channel the first attempt lands on. The multiplier never changes what the user is billed.')}`
          : null}
        {hasCapacityPremium
          ? ` ${t('The capacity premium raises the compared cost when the pool is close to its RPM, TPM or in-flight limit, so traffic shifts to models with headroom. Pools without a limit have no premium, and it never changes what the user is billed.')}`
          : null}
      </p>
      {previewOutcome && (
        <p className='text-muted-foreground text-xs'>{previewOutcome}</p>
      )}
      <StaticDataTable
        className={tableStyles.sectionContainer}
        headerRowClassName={tableStyles.mutedHeaderRow}
        columns={columns}
        data={props.pick.band}
        getRowKey={(row) => row.model}
        getRowClassName={(row) =>
          cn(row.model === props.pick.chosen && 'bg-success/10')
        }
      />
    </div>
  )
}

function StepSummary(props: { step: RouteTraceStep }) {
  const { t } = useTranslation()
  const step = props.step
  const profile = step.profile || t('Default models')

  return (
    <div className='space-y-1'>
      <div className='flex flex-wrap items-baseline gap-x-2 text-xs'>
        <span className='font-mono font-medium'>{profile}</span>
        {step.models ? (
          <span className='text-muted-foreground'>
            {t('{{count}} models ranked', { count: step.models })}
          </span>
        ) : null}
        <span className='text-muted-foreground'>
          {ladderOutcomeLabel(t, step.outcome)}
        </span>
        {step.model && (
          <span className='text-success font-mono font-medium'>
            → {step.model}
          </span>
        )}
      </div>
      {step.skipped && step.skipped.length > 0 && (
        <ul className='text-muted-foreground space-y-0.5 ps-3 text-xs'>
          {step.skipped.map((skipped) => (
            <li key={skipped.model}>
              <span className='font-mono'>{skipped.model}</span> (
              {formatScore(skipped.score)}):{' '}
              {skipReasonLabel(t, skipped.reason)}
            </li>
          ))}
          {step.skipped_more ? (
            <li>{t('and {{count}} more', { count: step.skipped_more })}</li>
          ) : null}
        </ul>
      )}
    </div>
  )
}

/** Every ranking tried in order, and why a better-ranked model was passed over. */
export function SelectionStep(props: {
  ladder: RouteTraceStep[]
  pick?: RoutePick
  pressure?: RoutePreviousPressure
}) {
  const { t } = useTranslation()
  // A ranking that compares successfully ends the walk, so at most one step did.
  const compared = props.ladder.find(
    (step) => step.outcome === 'compared' && step.model === props.pick?.chosen
  )
  // A ranking can be tried twice (its class, then the defaults), so repeats are numbered.
  const seen = new Map<string, number>()
  const rows = props.ladder.map((step) => {
    const name = step.profile ?? 'default'
    const occurrence = (seen.get(name) ?? 0) + 1
    seen.set(name, occurrence)
    return { step, key: `${name}#${occurrence}` }
  })

  return (
    <StepFrame icon={ListOrdered} title={t('Model selection')}>
      {props.pressure && !props.pressure.reused && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Previous model {{model}} was not reused: pool pressure {{value}} exceeds the knee {{knee}}.',
            {
              model: props.pressure.model,
              value: formatScore(props.pressure.pressure),
              knee: formatScore(props.pressure.knee),
            }
          )}
        </p>
      )}
      {props.pressure?.reused && (
        <p className='text-muted-foreground text-xs'>
          {t('Previous model {{model}} is within the no-premium knee.', {
            model: props.pressure.model,
          })}
        </p>
      )}
      {rows.map((row) => (
        <div key={row.key} className='space-y-2'>
          <StepSummary step={row.step} />
          {row.step === compared && props.pick && (
            <PickTable pick={props.pick} />
          )}
        </div>
      ))}
    </StepFrame>
  )
}
