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
import { CircleCheck, Gauge, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  effortDeliveryNote,
  formatScore,
  preferenceLabel,
  routeErrorLabel,
  routeSourceLabel,
  skipReasonLabel,
} from './labels'
import { Chips, Facts, StepFrame, type FactRow } from './route-process-parts'
import { PickTable } from './selection-step'
import type { RoutePick, RouteResult, RouteTraceEffort } from './types'

/** How difficulty became the reasoning effort, and how that effort reached the model. */
export function EffortStep(props: {
  effort: RouteTraceEffort
  result?: RouteResult
}) {
  const { t } = useTranslation()
  const effort = props.effort
  const result = props.result
  // In the order the router applies them: the previous effort first, then the benefit.
  const adjustments = [
    effort.floored && t('Held at the previous effort'),
    effort.upgraded && t('Raised one step by the larger-model benefit'),
  ].filter((note): note is string => Boolean(note))
  const rows: FactRow[] = [
    [t('Difficulty'), effort.difficulty],
    [t('Effort for this difficulty'), effort.base],
    [
      t('Larger-model benefit'),
      t('{{value}} against a threshold of {{threshold}}', {
        value: formatScore(effort.benefit),
        threshold: formatScore(effort.threshold),
      }),
    ],
  ]
  if (result?.effort) rows.push([t('Effort of the route'), result.effort])
  if (result?.effort_in_body) {
    rows.push([t('Effort sent'), result.applied_effort || t('None')])
  }

  return (
    <StepFrame icon={Gauge} title={t('Reasoning effort')}>
      <Facts rows={rows} />
      {adjustments.length > 0 && <Chips items={adjustments} />}
      {result && (
        <p className='text-muted-foreground text-xs'>
          {effortDeliveryNote(t, result)}
        </p>
      )}
    </StepFrame>
  )
}

/** The outcome: the model the request went to, or why no model could serve it. */
export function ResultStep(props: {
  result?: RouteResult
  error?: string
  unroutable?: { reason: string; count: number; examples?: string[] }[]
  /** Shown here only when there is no selection step to hold the comparison. */
  pick?: RoutePick
}) {
  const { t } = useTranslation()
  const result = props.result
  const failed = Boolean(props.error)

  if (failed) {
    return (
      <StepFrame icon={TriangleAlert} title={t('Result')}>
        <p className='text-destructive text-xs break-words'>
          {routeErrorLabel(t, props.error ?? '')}
        </p>
        {props.unroutable?.map((reason) => (
          <div key={reason.reason} className='space-y-1'>
            <p className='text-xs'>
              {skipReasonLabel(t, reason.reason)}
              <span className='text-muted-foreground'> × {reason.count}</span>
            </p>
            {reason.examples && <Chips items={reason.examples} />}
          </div>
        ))}
      </StepFrame>
    )
  }

  const rows: FactRow[] = [
    [
      t('Routed to'),
      <span key='model' className='font-mono font-medium'>
        {result?.resolved_model || result?.model || '-'}
      </span>,
    ],
  ]
  if (result?.profile) {
    rows.push([
      t('Ranking'),
      <span key='profile' className='font-mono'>
        {result.profile}
      </span>,
    ])
  }
  if (result?.source) {
    rows.push([t('Route source'), routeSourceLabel(t, result.source)])
  }
  if (result?.preference) {
    rows.push([t('Preference'), preferenceLabel(t, result.preference)])
  }

  return (
    <StepFrame icon={CircleCheck} title={t('Result')}>
      <Facts rows={rows} />
      {props.pick && <PickTable pick={props.pick} />}
    </StepFrame>
  )
}
