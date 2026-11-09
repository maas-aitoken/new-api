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
import type { SystemTask } from '../types'

/**
 * Option values the Auto routing pages read. The secret keys are never returned by
 * the options API, so they are not part of this type; whether they are set comes
 * from the status endpoint.
 */
export type AutoRouterSettings = {
  'autorouter_setting.enabled': boolean
  'autorouter_setting.jev_endpoint': string
  'autorouter_setting.jev_model': string
  'autorouter_setting.jev_timeout_ms': number
  'autorouter_setting.max_runes': number
  'autorouter_setting.previous_max_runes': number
  'autorouter_setting.cache_ttl_seconds': number
  'autorouter_setting.default_preference': string
  /** JSON object: preference -> weight. */
  'autorouter_setting.cost_weights': string
  'autorouter_setting.continuation_task_change': number
  'autorouter_setting.upgrade_benefit': number
  'autorouter_setting.default_effort': string
  /** JSON array: the effort of each difficulty rung. */
  'autorouter_setting.difficulty_efforts': string[]
  'autorouter_setting.rank_refresh_enabled': boolean
  'autorouter_setting.rank_refresh_interval_hours': number
  /** Which requests leave a routing log: off, decisions or all. */
  'autorouter_setting.route_log_mode': 'off' | 'decisions' | 'all'
  'autorouter_setting.route_log_retention_days': number
}

/** Where a connection value comes from; empty when it is not set anywhere. */
export type AutoRouterConnectionSource = 'option' | 'env' | ''

export type AutoRouterConnectionKey =
  | 'jev_endpoint'
  | 'jev_model'
  | 'jev_api_key'
  | 'artificial_analysis_api_key'
  | 'design_arena_api_key'
  | 'openrouter_api_key'

export type AutoRouterStatus = {
  enabled: boolean
  jev_ready: boolean
  rank_keys_ready: boolean
  ranking_current: boolean
  taxonomy_version: string
  rank_sources: Record<string, string> | null
  sources: Record<AutoRouterConnectionKey, AutoRouterConnectionSource>
  /** The latest leaderboard refresh task; absent before the first run. */
  rank_refresh?: SystemTask<
    Record<string, unknown>,
    Record<string, unknown>,
    { stale_metrics?: string[] }
  >
}

export type AutoRouterRankedModel = {
  model: string
  /** Null for a model the list names but the scores do not cover. */
  score: number | null
  /**
   * Whether an enabled channel on the gateway serves the model, bare or with an
   * effort suffix. Token allowlists, groups, capabilities and prices are not
   * considered, so a request may still be unable to use it.
   */
  served: boolean
}

export type AutoRouterRankedProfile = {
  profile: string
  models: AutoRouterRankedModel[]
}

export type AutoRouterRanking = {
  current: boolean
  taxonomy_version: string
  sources: Record<string, string> | null
  profiles: AutoRouterRankedProfile[]
}

/**
 * A hand-edited ranking: every profile with its full list. base carries the
 * generated_at and edited_at the editor loaded, so the server can refuse a save
 * over a ranking that changed in the meantime.
 */
export type AutoRouterRankingEdit = {
  base: { generated_at: string; edited_at: string }
  profiles: {
    profile: string
    models: { model: string; score: number | null }[]
  }[]
}

export type AutoRouterRefreshResult = {
  task: SystemTask
  /** False when a refresh was already running and is returned as is. */
  created: boolean
}

export type AutoRouterResponse<TData> = {
  success: boolean
  message: string
  data: TData
}
