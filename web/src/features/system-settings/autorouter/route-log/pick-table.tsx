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
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  staticDataTableClassNames as tableStyles,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { toIntlLocale } from '@/i18n/languages'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { formatScore, previewNoteLabel, previewOutcomeLabel } from './labels'
import { routePickSchema } from './presentation'
import { ToneBadge } from './route-process-parts'
import type { RouteLogTruncation, RoutePick, RoutePickCandidate } from './types'

const priceFormat = { digitsLarge: 4, digitsSmall: 6, abbreviate: false }

type NumericCandidateKey =
  | 'score'
  | 'cost_score'
  | 'cost_penalty'
  | 'capacity'
  | 'success'
  | 'utility'
  | 'probability'
  | 'discount'

function scoreColumn(
  id: NumericCandidateKey,
  header: string
): StaticDataTableColumn<RoutePickCandidate> {
  return {
    id,
    header,
    className: 'h-9 text-right',
    cellClassName: tableStyles.topNumericCell,
    cell: (row) => formatScore(row[id]),
  }
}

function countColumn(
  id: 'requests' | 'samples',
  header: string,
  locale: Intl.LocalesArgument
): StaticDataTableColumn<RoutePickCandidate> {
  return {
    id,
    header,
    className: 'h-9 text-right',
    cellClassName: tableStyles.topNumericCell,
    cell: (row) => {
      const value = row[id]
      return typeof value === 'number' && Number.isFinite(value)
        ? formatNumber(value, locale)
        : '-'
    },
  }
}

function formatPrice(value?: number): string {
  return typeof value === 'number' && Number.isFinite(value)
    ? formatBillingCurrencyFromUSD(value, priceFormat)
    : '-'
}

function comparedPrice(row: RoutePickCandidate): string {
  if (row.cost_multiplier && row.cost != null) {
    return formatPrice(row.cost)
  }
  return formatPrice(row.price)
}

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

/** The compared candidates, described by the algorithm that actually produced the log. */
export function PickTable(props: {
  pick: RoutePick
  truncated?: RouteLogTruncation
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const pick = props.pick
  const band = Array.isArray(pick.band) ? pick.band : []
  const schema = routePickSchema(pick)
  const comparesCost = band.some((row) => (row.cost_multiplier ?? 0) > 0)
  const hasRequestShareCapacity = band.some(
    (row) => typeof row.requests === 'number' && Number.isFinite(row.requests)
  )
  const hasCapacityPremium = band.some((row) => (row.capacity_premium ?? 0) > 0)
  const previewOutcome = previewOutcomeLabel(t, pick.preview)
  const bandTruncation = props.truncated?.collections?.['result.pick.band']
  const deterministic = typeof pick.cost_scale === 'number'
  const omittedProbability = bandTruncation?.omitted_probability

  const columns: StaticDataTableColumn<RoutePickCandidate>[] = [
    {
      id: 'model',
      header: t('Model'),
      className: 'h-9',
      cellClassName: 'py-2',
      cell: (row) => (
        <div className='flex flex-wrap items-center gap-1.5'>
          <span className='font-mono'>{row.model}</span>
          {row.model === pick.chosen && (
            <ToneBadge tone='success' label={t('Chosen')} />
          )}
        </div>
      ),
    },
    scoreColumn(
      'score',
      schema === 'legacy' ? t('Leaderboard score') : t('Quality')
    ),
    {
      id: 'price',
      header: t('Price per million tokens'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) => formatPrice(row.price),
    },
  ]

  if (comparesCost) {
    columns.push(
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
      {
        id: 'cost',
        header: t('Compared cost'),
        className: 'h-9 text-right',
        cellClassName: tableStyles.topNumericCell,
        cell: comparedPrice,
      }
    )
  }
  if (hasCapacityPremium) {
    columns.push({
      id: 'capacity_premium',
      header: t('Capacity premium'),
      className: 'h-9 text-right',
      cellClassName: tableStyles.topNumericCell,
      cell: (row) =>
        row.capacity_premium ? (
          <div className='flex flex-col items-end'>
            <span>× {formatScore(row.capacity_premium)}</span>
            <span className='text-muted-foreground text-xs font-normal'>
              {t('Pool pressure')}{' '}
              {typeof row.capacity_pressure === 'number' &&
              Number.isFinite(row.capacity_pressure)
                ? `${Math.round(row.capacity_pressure * 100)}%`
                : '-'}
            </span>
          </div>
        ) : (
          '-'
        ),
    })
  }

  if (schema === 'current') {
    if (deterministic) {
      columns.push(scoreColumn('cost_penalty', t('Cost penalty')))
    } else {
      columns.push(scoreColumn('cost_score', t('Cost score')))
    }
    columns.push(
      scoreColumn('capacity', t('Capacity')),
      scoreColumn('success', t('Success'))
    )
    if (hasRequestShareCapacity) {
      columns.push(countColumn('requests', t('Requests'), locale))
    }
    columns.push(countColumn('samples', t('Samples'), locale))
  } else if (schema === 'legacy') {
    columns.push(scoreColumn('discount', t('Price discount')))
  }
  columns.push({
    ...scoreColumn(
      'utility',
      schema === 'legacy' ? t('Score after discount') : t('Utility')
    ),
    cellClassName: cn(tableStyles.topNumericCell, 'font-semibold'),
  })
  if (schema === 'current' && !deterministic) {
    columns.push(scoreColumn('probability', t('Probability')))
  }
  return (
    <div className='space-y-1'>
      {schema === 'current' && pick.dimensions && (
        <p className='text-muted-foreground text-xs'>
          {deterministic
            ? t(
                'Quality {{quality}}, cost {{cost}}, capacity {{capacity}} and success {{success}}.',
                {
                  quality: formatScore(pick.dimensions.quality),
                  cost: formatScore(pick.dimensions.cost),
                  capacity: formatScore(pick.dimensions.capacity),
                  success: formatScore(pick.dimensions.success),
                }
              )
            : t(
                'Quality {{quality}}, cost {{cost}}, capacity {{capacity}} and success {{success}}; sampling temperature {{temperature}}.',
                {
                  quality: formatScore(pick.dimensions.quality),
                  cost: formatScore(pick.dimensions.cost),
                  capacity: formatScore(pick.dimensions.capacity),
                  success: formatScore(pick.dimensions.success),
                  temperature: formatScore(pick.dimensions.temperature),
                }
              )}
          {comparesCost
            ? ` ${t('The cost multiplier never changes what the user is billed.')}`
            : null}
        </p>
      )}
      {schema === 'current' &&
        typeof pick.need === 'number' &&
        Number.isFinite(pick.need) && (
          <p className='text-muted-foreground text-xs'>
            {pick.need_from_difficulty
              ? t(
                  'Capability need {{need}} from task difficulty sets the capability and cost weights.',
                  { need: formatScore(pick.need) }
                )
              : t(
                  'No task difficulty was available, so the neutral capability need {{need}} applies.',
                  { need: formatScore(pick.need) }
                )}
          </p>
        )}
      {schema === 'current' && deterministic && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Quality scale {{qualityScale}}. Cost tolerance {{costScale}} per million tokens. Capacity penalty coefficient {{capacityPenalty}}, success penalty coefficient {{successPenalty}}.',
            {
              qualityScale: formatScore(pick.quality_scale),
              costScale: formatPrice(pick.cost_scale),
              capacityPenalty: formatScore(pick.capacity_penalty),
              successPenalty: formatScore(pick.success_penalty),
            }
          )}
        </p>
      )}
      {schema === 'current' && (
        <p className='text-muted-foreground text-xs'>
          {t(
            hasRequestShareCapacity
              ? 'Capacity is one minus the request share. Success adds four prior successes to the measured outcomes; Samples shows the evidence strength.'
              : 'Capacity is the lowest remaining fraction of configured RPM, TPM and in-flight limits. Scheduler and affinity use their concrete channel; native estimates use first-attempt probabilities. No effective limit or estimate means full capacity; an exhausted pool means none.'
          )}
        </p>
      )}
      {schema === 'legacy' && (
        <p className='text-muted-foreground text-xs'>
          {typeof pick.weight === 'number' && Number.isFinite(pick.weight)
            ? t(
                'Candidates within {{weight}} of the best score, ranked by score after the price discount.',
                { weight: formatScore(pick.weight) }
              )
            : t('Picked by quality and price')}
          {comparesCost
            ? ` ${t('The discount uses the compared cost: the user price times the cost multiplier of the channel the first attempt lands on. The multiplier never changes what the user is billed.')}`
            : null}
        </p>
      )}
      {hasCapacityPremium && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'The capacity premium raises the compared cost when the pool is close to its RPM, TPM or in-flight limit, so traffic shifts to models with headroom. Pools without a limit have no premium, and it never changes what the user is billed.'
          )}
        </p>
      )}
      {previewOutcome && (
        <p className='text-muted-foreground text-xs'>{previewOutcome}</p>
      )}
      {bandTruncation && (
        <>
          <p className='text-muted-foreground text-xs'>
            {deterministic ||
            typeof omittedProbability !== 'number' ||
            !Number.isFinite(omittedProbability)
              ? t('Showing {{kept}} of {{total}} compared models.', {
                  kept: bandTruncation.kept,
                  total: bandTruncation.total,
                })
              : t(
                  'Showing {{kept}} of {{total}} compared models; omitted sampling probability {{probability}}.',
                  {
                    kept: bandTruncation.kept,
                    total: bandTruncation.total,
                    probability: formatScore(omittedProbability),
                  }
                )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {deterministic
              ? t(
                  'Retained candidates keep their scores from the full comparison.'
                )
              : t(
                  'Retained candidates keep their scores and probabilities from the full comparison.'
                )}
          </p>
        </>
      )}
      <StaticDataTable
        className={tableStyles.sectionContainer}
        headerRowClassName={tableStyles.mutedHeaderRow}
        columns={columns}
        data={band}
        getRowKey={(row) => row.model}
        getRowClassName={(row) =>
          cn(row.model === pick.chosen && 'bg-success/10')
        }
      />
    </div>
  )
}
