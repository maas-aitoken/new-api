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
import { Inbox, ScanSearch } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  formatScore,
  judgmentLabel,
  judgmentNote,
  judgmentTone,
  protocolLabel,
} from './labels'
import {
  Chips,
  Facts,
  StepFrame,
  ToneBadge,
  type FactRow,
} from './route-process-parts'
import type { RouteResult, RouteTrace } from './types'

export function RequestStep(props: { request: RouteTrace['request'] }) {
  const { t } = useTranslation()
  const request = props.request
  const flags = [
    request.auxiliary && t('Background request'),
    request.agent && t('Sub-agent task'),
    request.session && t('Has a session'),
    request.truncated && t('Truncated before classification'),
  ].filter((flag): flag is string => Boolean(flag))
  const rows: FactRow[] = [
    [t('Protocol'), protocolLabel(request.protocol)],
    [t('Human turns'), request.human_turns],
    [
      t('Instruction length'),
      t('{{count}} characters', { count: request.text_runes }),
    ],
  ]
  if (request.previous_runes) {
    rows.push([
      t('Previous instruction'),
      t('{{count}} characters', { count: request.previous_runes }),
    ])
  }

  return (
    <StepFrame icon={Inbox} title={t('Request')}>
      <Facts rows={rows} />
      {flags.length > 0 && <Chips items={flags} />}
      <p className='text-muted-foreground text-xs'>
        {t('Only the length of the instruction is kept, never its text.')}
      </p>
    </StepFrame>
  )
}

export function JudgmentStep(props: {
  trace: RouteTrace
  result?: RouteResult
}) {
  const { t } = useTranslation()
  const judgment = props.trace.judgment
  const decision = props.result?.decision
  const jev = props.result?.jev
  const rows: FactRow[] = []
  if (decision) {
    rows.push(
      [
        t('Task type'),
        <span key='task' className='font-mono'>
          {decision.task_type || '-'}
        </span>,
      ],
      [t('Task type probability'), formatScore(decision.task_type_probability)],
      [t('Difficulty'), decision.difficulty],
      [t('Task change'), formatScore(decision.task_change)],
      [t('Larger-model benefit'), formatScore(decision.larger_model_benefit)]
    )
  }
  if (jev) rows.push([t('Classifier latency'), `${jev.latency_ms} ms`])
  const likelyTypes = Object.entries(decision?.task_type_probabilities ?? {})
    .sort(([, a], [, b]) => b - a)
    .slice(0, 3)
    .map(([type, probability]) => `${type} ${formatScore(probability)}`)

  return (
    <StepFrame icon={ScanSearch} title={t('Classification')}>
      <ToneBadge
        tone={judgmentTone(judgment)}
        label={judgmentLabel(t, judgment)}
      />
      <p className='text-muted-foreground text-xs'>
        {judgmentNote(t, judgment)}
      </p>
      {rows.length > 0 && <Facts rows={rows} />}
      {likelyTypes.length > 1 && <Chips items={likelyTypes} />}
      {jev?.error && (
        <p className='text-destructive font-mono text-xs break-words'>
          {jev.error}
        </p>
      )}
    </StepFrame>
  )
}
