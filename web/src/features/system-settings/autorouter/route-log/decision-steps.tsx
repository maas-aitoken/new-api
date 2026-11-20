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
import { GitCompareArrows, Scale, ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { AUTO_PREFERENCES } from '@/features/keys/lib'

import {
  decisionErrorLabel,
  formatScore,
  preferenceLabel,
  type RouteTone,
} from './labels'
import {
  Chips,
  Facts,
  StepFrame,
  ToneBadge,
  type FactRow,
} from './route-process-parts'
import type {
  RouteTraceContinuation,
  RouteTraceMode,
  RouteTraceValidity,
} from './types'

/** Whether the classifier's answer was valid, and which rankings it routes by. */
export function ValidityStep(props: { validity: RouteTraceValidity }) {
  const { t } = useTranslation()
  const error = props.validity.error
  const profiles = props.validity.profiles

  return (
    <StepFrame icon={ShieldCheck} title={t('Validity check')}>
      <ToneBadge
        tone={error ? 'warning' : 'success'}
        label={error ? decisionErrorLabel(t, error) : t('Valid')}
      />
      {profiles && profiles.length > 0 && (
        <>
          <p className='text-muted-foreground text-xs'>
            {t('Rankings to try, most specific first')}
          </p>
          <Chips items={profiles} />
        </>
      )}
    </StepFrame>
  )
}

function continuationVerdict(
  t: (key: string) => string,
  continuation: RouteTraceContinuation
): { tone: RouteTone; label: string } {
  if (continuation.continued) {
    return { tone: 'success', label: t('Continued the previous task') }
  }
  if (continuation.task_change >= continuation.threshold) {
    return { tone: 'info', label: t('A new task') }
  }
  return { tone: 'warning', label: t('The previous model can no longer serve') }
}

export function ContinuationStep(props: {
  continuation: RouteTraceContinuation
}) {
  const { t } = useTranslation()
  const continuation = props.continuation
  const verdict = continuationVerdict(t, continuation)

  return (
    <StepFrame icon={GitCompareArrows} title={t('Continuation check')}>
      <ToneBadge tone={verdict.tone} label={verdict.label} />
      <Facts
        rows={[
          [
            t('Previous model'),
            <span key='previous' className='font-mono'>
              {continuation.previous}
            </span>,
          ],
          [
            t('Task change'),
            t('{{value}} against a threshold of {{threshold}}', {
              value: formatScore(continuation.task_change),
              threshold: formatScore(continuation.threshold),
            }),
          ],
        ]}
      />
    </StepFrame>
  )
}

/** How an adaptive request got its policy for the four-dimensional comparison. */
export function ModeStep(props: { mode: RouteTraceMode }) {
  const { t } = useTranslation()
  const mode = props.mode
  const judged = !mode.reason
  const shares = AUTO_PREFERENCES.map(
    (preference) =>
      `${preferenceLabel(t, preference)} ${formatScore(mode.shares?.[preference] ?? 0)}`
  )
  const tiers = AUTO_PREFERENCES.map((preference) => {
    const weights = mode.tiers[preference]
    return `${preferenceLabel(t, preference)} ${t(
      'Quality {{quality}}, cost {{cost}}, capacity {{capacity}} and success {{success}}; sampling temperature {{temperature}}.',
      {
        quality: formatScore(weights.quality),
        cost: formatScore(weights.cost),
        capacity: formatScore(weights.capacity),
        success: formatScore(weights.success),
        temperature: formatScore(weights.temperature),
      }
    )}`
  })
  const facts: FactRow[] = [
    [t('Quality weight'), formatScore(mode.weights.quality)],
    [t('Cost weight'), formatScore(mode.weights.cost)],
    [t('Capacity weight'), formatScore(mode.weights.capacity)],
    [t('Success weight'), formatScore(mode.weights.success)],
    [t('Sampling temperature'), formatScore(mode.weights.temperature)],
  ]
  if (judged) {
    facts.unshift([t('Expected position'), formatScore(mode.position)])
  }

  return (
    <StepFrame icon={Scale} title={t('Routing weights')}>
      <ToneBadge
        tone={judged ? 'success' : 'warning'}
        label={judged ? t('Judged from the request') : t('No usable judgment')}
      />
      {judged && (
        <>
          <p className='text-muted-foreground text-xs'>
            {t('Probability of each preference')}
          </p>
          <Chips items={shares} />
        </>
      )}
      <Facts rows={facts} />
      <p className='text-muted-foreground text-xs'>
        {t('Platform preference policies')}
      </p>
      <Chips items={tiers} />
      <p className='text-muted-foreground text-xs'>
        {judged
          ? t(
              'The expected position runs from 0 (capability first) through 1 (balanced) to 2 (cost first). The four strengths and sampling temperature are read along the line through the three preference policies. The strengths are then normalized to sum to one; temperature remains independent.'
            )
          : t(
              'The classifier gave no usable answer, so the balanced policy applies.'
            )}
      </p>
    </StepFrame>
  )
}
