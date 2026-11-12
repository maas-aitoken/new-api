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
import { expect, test } from 'vitest'

import { routeProcessSteps } from '../process'
import type { RouteLogDetail, RouteTrace } from '../types'

const request = { human_turns: 1, text_runes: 12 }
const validity = { profiles: ['code.build'] }
const ladder = [{ profile: 'code.build', outcome: 'first_available' }]
const effort = { difficulty: 2, base: 'medium', threshold: 0.5 }
const continuation = {
  previous: 'coder',
  task_change: 0.9,
  threshold: 0.5,
  continued: false,
}
const mode = {
  shares: { capability: 0.1, balanced: 0.6, cost: 0.3 },
  position: 1.2,
  weight: 0.116,
  tiers: { capability: 0, balanced: 0.07, cost: 0.3 },
}
const comparedLadder = [{ profile: 'code.build', outcome: 'compared', model: 'sonnet' }]

function detailOf(trace: Partial<RouteTrace> & { judgment: string }) {
  return { trace: { request, ...trace } } as RouteLogDetail
}

test.each([
  ['no detail at all', null, ['result']],
  ['only the summary the usage log keeps', { result: { model: 'coder' } }, ['result']],
  [
    'a live classification with a valid answer',
    detailOf({ judgment: 'live', validity, ladder, effort }),
    ['request', 'judgment', 'validity', 'selection', 'effort', 'result'],
  ],
  [
    'a cached classification checked against the previous task',
    detailOf({ judgment: 'cache', validity, continuation, ladder, effort }),
    ['request', 'judgment', 'validity', 'continuation', 'selection', 'effort', 'result'],
  ],
  [
    'a classifier failure, which leaves no decision to route by',
    detailOf({
      judgment: 'live_failed',
      validity: { error: 'no_decision' },
      ladder,
    }),
    ['request', 'judgment', 'validity', 'selection', 'result'],
  ],
  [
    'a background request, which is never classified and so has no validity check',
    detailOf({
      judgment: 'auxiliary',
      validity: { error: 'no_decision' },
      ladder,
    }),
    ['request', 'judgment', 'selection', 'result'],
  ],
  [
    'a sub-agent task that followed its session',
    detailOf({ judgment: 'session', ladder }),
    ['request', 'judgment', 'selection', 'result'],
  ],
  [
    'a request no model could serve',
    detailOf({ judgment: 'unroutable' }),
    ['request', 'judgment', 'result'],
  ],
  [
    'an adaptive request that compared quality and price shows how its cost weight was judged, right before the selection',
    detailOf({ judgment: 'live', validity, mode, continuation, ladder: comparedLadder, effort }),
    ['request', 'judgment', 'validity', 'continuation', 'mode', 'selection', 'effort', 'result'],
  ],
  [
    'an adaptive request that kept the previous model never used its cost weight',
    detailOf({ judgment: 'cache', validity, mode, continuation, ladder: [{ outcome: 'inherited' }], effort }),
    ['request', 'judgment', 'validity', 'continuation', 'selection', 'effort', 'result'],
  ],
  [
    'an adaptive request that took the first servable model never used it either',
    detailOf({ judgment: 'live', validity, mode, ladder, effort }),
    ['request', 'judgment', 'validity', 'selection', 'effort', 'result'],
  ],
  [
    'a fixed preference leaves no mode, so none is shown even when it compared',
    detailOf({ judgment: 'live', validity, ladder: comparedLadder, effort }),
    ['request', 'judgment', 'validity', 'selection', 'effort', 'result'],
  ],
])('%s lists exactly the steps it went through', (_name, detail, expected) => {
  expect(routeProcessSteps(detail as RouteLogDetail | null)).toEqual(expected)
})
