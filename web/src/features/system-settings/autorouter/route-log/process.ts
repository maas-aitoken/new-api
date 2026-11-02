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
import type { RouteLogDetail } from './types'

export type RouteProcessStepId =
  | 'request'
  | 'judgment'
  | 'trust'
  | 'continuation'
  | 'mode'
  | 'selection'
  | 'effort'
  | 'result'

// A trust check only means something when the classifier answered, or when its
// failure decided the route. The other judgments never reach one.
const JUDGMENTS_WITH_TRUST = new Set([
  'live',
  'live_failed',
  'cache',
  'recent_failure',
])

/**
 * The steps a request actually went through, in order. A log without a trace
 * (only the summary the usage log keeps) has nothing but its result.
 */
export function routeProcessSteps(
  detail: RouteLogDetail | null
): RouteProcessStepId[] {
  const trace = detail?.trace
  if (!trace) return ['result']

  const steps: RouteProcessStepId[] = ['request', 'judgment']
  if (trace.trust && JUDGMENTS_WITH_TRUST.has(trace.judgment)) {
    steps.push('trust')
  }
  if (trace.continuation) steps.push('continuation')
  // The cost weight only matters to a request that compared quality and price. One
  // that kept the previous model or took the first servable model never used it.
  if (trace.mode && trace.ladder?.some((step) => step.outcome === 'compared')) {
    steps.push('mode')
  }
  if (trace.ladder?.length) steps.push('selection')
  if (trace.effort) steps.push('effort')
  steps.push('result')
  return steps
}
