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
import { ListOrdered } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatScore, ladderOutcomeLabel, skipReasonLabel } from './labels'
import { PickTable } from './pick-table'
import { routePickSchema, type RoutePresentationSchema } from './presentation'
import { StepFrame } from './route-process-parts'
import type {
  RouteLogTruncation,
  RoutePick,
  RoutePreviousPressure,
  RouteTraceStep,
} from './types'

function StepSummary(props: {
  step: RouteTraceStep
  schema: RoutePresentationSchema
}) {
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
          {ladderOutcomeLabel(t, step.outcome, props.schema)}
        </span>
        {step.model && (
          <span className='text-success font-mono font-medium'>
            → {step.model}
          </span>
        )}
      </div>
      {((step.skipped?.length ?? 0) > 0 || (step.skipped_more ?? 0) > 0) && (
        <ul className='text-muted-foreground space-y-0.5 ps-3 text-xs'>
          {step.skipped?.map((skipped) => (
            <li key={skipped.model}>
              <span className='font-mono'>{skipped.model}</span> (
              {formatScore(skipped.score)}): {skipReasonLabel(t, skipped.reason)}
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
  truncated?: RouteLogTruncation
}) {
  const { t } = useTranslation()
  const compared = props.ladder.find(
    (step) => step.outcome === 'compared' && step.model === props.pick?.chosen
  )
  const schema = props.pick ? routePickSchema(props.pick) : 'partial'
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
          <StepSummary step={row.step} schema={schema} />
          {row.step === compared && props.pick && (
            <PickTable pick={props.pick} truncated={props.truncated} />
          )}
        </div>
      ))}
    </StepFrame>
  )
}
