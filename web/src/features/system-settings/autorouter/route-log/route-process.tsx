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
import { ContinuationStep, ModeStep, TrustStep } from './decision-steps'
import { JudgmentStep, RequestStep } from './input-steps'
import { routeProcessSteps, type RouteProcessStepId } from './process'
import { EffortStep, ResultStep } from './result-steps'
import { SelectionStep } from './selection-step'
import type { RouteLogDetail } from './types'

function ProcessStep(props: { id: RouteProcessStepId; detail: RouteLogDetail }) {
  const { result, trace, error } = props.detail
  switch (props.id) {
    case 'request':
      return trace ? <RequestStep request={trace.request} /> : null
    case 'judgment':
      return trace ? <JudgmentStep trace={trace} result={result} /> : null
    case 'trust':
      return trace?.trust ? <TrustStep trust={trace.trust} /> : null
    case 'continuation':
      return trace?.continuation ? (
        <ContinuationStep continuation={trace.continuation} />
      ) : null
    case 'mode':
      return trace?.mode ? <ModeStep mode={trace.mode} /> : null
    case 'selection':
      return trace?.ladder ? (
        <SelectionStep ladder={trace.ladder} pick={result?.pick} />
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
        />
      )
  }
}

/** How one request got to its model, one step after another. */
export function RouteProcess(props: { detail: RouteLogDetail }) {
  return (
    <div className='space-y-4'>
      {routeProcessSteps(props.detail).map((id) => (
        <ProcessStep key={id} id={id} detail={props.detail} />
      ))}
    </div>
  )
}
