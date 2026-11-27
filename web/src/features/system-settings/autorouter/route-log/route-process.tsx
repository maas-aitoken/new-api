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
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription } from '@/components/ui/alert'

import { ContinuationStep, ModeStep, ValidityStep } from './decision-steps'
import { JudgmentStep, RequestStep } from './input-steps'
import { routeProcessSteps, type RouteProcessStepId } from './process'
import { EffortStep, ResultStep } from './result-steps'
import { SelectionStep } from './selection-step'
import type { RouteLogDetail, RouteLogTruncation } from './types'

function TruncationNotice(props: { truncated?: RouteLogTruncation }) {
  const { t } = useTranslation()
  const fields = props.truncated?.fields ?? []
  const strings = props.truncated?.strings ?? []
  const collections = Object.entries(
    props.truncated?.collections ?? {}
  ).filter(([path]) => path !== 'result.pick.band')
  const reason = props.truncated?.reason
  if (
    !reason &&
    fields.length === 0 &&
    strings.length === 0 &&
    collections.length === 0
  ) {
    return null
  }

  return (
    <Alert>
      <Info />
      <AlertDescription>
        <p>
          {reason === 'encoding_error'
            ? t(
                'The original routing detail could not be encoded; only the readable core is shown.'
              )
            : t('Some routing details were omitted or shortened to fit the log.')}
        </p>
        {collections.map(([path, summary]) => (
          <p key={path} className='font-mono break-all'>
            {path}: {summary.kept}/{summary.total}
          </p>
        ))}
        {fields.length > 0 && (
          <p className='font-mono break-all'>{fields.join(', ')}</p>
        )}
        {strings.length > 0 && (
          <p className='font-mono break-all'>{strings.join(', ')}</p>
        )}
      </AlertDescription>
    </Alert>
  )
}

function ProcessStep(props: {
  id: RouteProcessStepId
  detail: RouteLogDetail
}) {
  const { result, trace, error, truncated, langfuse_trace_url } = props.detail
  switch (props.id) {
    case 'request':
      // trace.request 缺失（detail 被改库改坏）时跳过这一步，不让整个抽屉崩掉。
      return trace?.request ? <RequestStep request={trace.request} /> : null
    case 'judgment':
      return trace ? (
        <JudgmentStep
          trace={trace}
          result={result}
          langfuseTraceUrl={langfuse_trace_url}
        />
      ) : null
    case 'validity':
      return trace?.validity ? <ValidityStep validity={trace.validity} /> : null
    case 'continuation':
      return trace?.continuation ? (
        <ContinuationStep
          continuation={trace.continuation}
          pressure={trace.previous_pressure}
        />
      ) : null
    case 'mode':
      return trace?.mode ? <ModeStep mode={trace.mode} /> : null
    case 'selection':
      return trace?.ladder ? (
        <SelectionStep
          ladder={trace.ladder}
          pick={result?.pick}
          pressure={trace.continuation ? undefined : trace.previous_pressure}
          truncated={truncated}
        />
      ) : null
    case 'effort':
      return trace?.effort ? (
        <EffortStep effort={trace.effort} result={result} />
      ) : null
    case 'result':
      return (
        <ResultStep
          result={result}
          error={error}
          unroutable={trace?.unroutable?.reasons}
          pick={trace?.ladder?.length ? undefined : result?.pick}
          truncated={truncated}
        />
      )
  }
}

/** How one request got to its model, one step after another. */
export function RouteProcess(props: { detail: RouteLogDetail }) {
  const { t } = useTranslation()
  const empty =
    !props.detail.result && !props.detail.trace && !props.detail.error
  return (
    <div className='space-y-4'>
      <TruncationNotice truncated={props.detail.truncated} />
      {empty ? (
        <EmptyState
          title={t('No routing process details were stored for this request')}
          className='min-h-40'
        />
      ) : (
        routeProcessSteps(props.detail).map((id) => (
          <ProcessStep key={id} id={id} detail={props.detail} />
        ))
      )}
    </div>
  )
}
