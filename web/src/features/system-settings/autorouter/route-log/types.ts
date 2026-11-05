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
import type { AutoRouterResponse } from '../types'

export type RouteLogOutcome = 'routed' | 'failed'

/** One row of the routing log list; the process itself is only in the detail. */
export type RouteLogItem = {
  id: number
  created_at: number
  request_id: string
  user_id: number
  token_id: number
  using_group: string
  outcome: RouteLogOutcome
  judgment: string
  source: string
  task_type: string
  model: string
  effort: string
  preference: string
  jev_latency_ms: number
}

export type RouteLogPage = {
  items: RouteLogItem[]
  total: number
  page: number
  page_size: number
}

/** The list filters as typed into the toolbar; an empty string means no filter. */
export type RouteLogFilters = {
  request_id: string
  user_id: string
  model: string
  task_type: string
  outcome: '' | RouteLogOutcome
  judgment: string
}

export type RouteDecision = {
  task_type: string
  task_type_probability: number
  task_type_probabilities?: Record<string, number>
  difficulty: number
  task_change?: number | null
  larger_model_benefit?: number | null
}

export type RouteCostSource = 'scheduler' | 'affinity' | 'expected'

export type RoutePickCandidate = {
  model: string
  score: number
  /** 用户侧混合价（美元/百万 token）。 */
  price: number
  /**
   * 首次会落到的渠道的费用倍率（几个渠道时是期望）。有它时比较的是 cost = price × 倍率
   * （平台成本，不参与计费）；没有表示没估算，按 price 比较。
   */
  cost_multiplier?: number
  cost?: number
  channel_id?: number
  channels?: number
  cost_source?: RouteCostSource
  /**
   * 调度器回答了预览、却没用上它给这个模型的答案时的原因（not_cached、error、unusable_channel、
   * over_limit），这个模型按原生规则估算。
   */
  preview?: string
  discount: number
  utility: number
}

/**
 * 一次比价问调度器预览的结果。answered 表示调度器回答了；其余是没拿到回答的原因：
 * not_scheduler_decided、not_configured、circuit_open、unsupported、timeout、failed、bad_response。
 */
export type RoutePreviewOutcome = {
  status: string
  /** failed 时调度器回的状态码，连不上时没有。 */
  http_status?: number
}

export type RoutePick = {
  chosen: string
  weight: number
  band: RoutePickCandidate[]
  /** 没配调度器、或这次没估算比较成本时没有。 */
  preview?: RoutePreviewOutcome
}

export type RouteEffortAction = 'keep' | 'set' | 'strip'

/** The routing outcome; the consume log keeps the same object in admin_info.auto_router. */
export type RouteResult = {
  resolved_model?: string
  profile?: string
  model?: string
  effort?: string
  source?: string
  preference?: string
  decision?: RouteDecision
  pick?: RoutePick
  effort_in_body?: boolean
  effort_override?: boolean
  applied_effort?: string
  effort_action?: RouteEffortAction
  jev?: { endpoint: string; latency_ms: number; error?: string }
}

export type RouteTraceRequest = {
  protocol?: string
  auxiliary?: boolean
  agent?: boolean
  human_turns: number
  session?: boolean
  text_runes: number
  previous_runes?: number
  truncated?: boolean
}

export type RouteTraceReason = {
  reason: string
  count: number
  examples?: string[]
}

export type RouteTraceTrust = {
  reason?: string
  profiles?: string[]
}

export type RouteTraceContinuation = {
  previous: string
  task_change: number
  threshold: number
  continued: boolean
}

/** How an adaptive request got its cost weight; absent for a key with a fixed preference. */
export type RouteTraceMode = {
  /** Empty when the weight was judged; 'no_answer' when it fell back to the balanced weight. */
  reason?: string
  /** Each preference's share of the judged probability. */
  shares?: Record<string, number>
  /** Expected position on the capability (0), balanced (1), cost (2) scale. */
  position: number
  weight: number
  /** The platform's three weights when the request ran. */
  tiers: { capability: number; balanced: number; cost: number }
}

export type RouteTraceEffort = {
  difficulty: number
  base: string
  benefit?: number | null
  threshold: number
  upgraded?: boolean
  floored?: boolean
}

export type RouteTraceSkipped = { model: string; score: number; reason: string }

export type RouteTraceStep = {
  profile?: string
  models?: number
  outcome: string
  model?: string
  skipped?: RouteTraceSkipped[]
  skipped_more?: number
}

export type RouteTrace = {
  request: RouteTraceRequest
  judgment: string
  unroutable?: { reasons: RouteTraceReason[] }
  trust?: RouteTraceTrust
  mode?: RouteTraceMode
  continuation?: RouteTraceContinuation
  effort?: RouteTraceEffort
  ladder?: RouteTraceStep[]
}

/** What the log stores about the process; every part can be absent. */
export type RouteLogDetail = {
  result?: RouteResult
  trace?: RouteTrace
  error?: string
}

export type RouteLog = RouteLogItem & { detail: RouteLogDetail | null }

export type RouteLogResponse<TData> = AutoRouterResponse<TData>
