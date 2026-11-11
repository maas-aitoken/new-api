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
import { AUTO_PREFERENCES } from '@/features/keys/lib'

import type { AutoPreference, CostWeights } from './settings'
import type {
  AutoRouterRankedModel,
  AutoRouterRankedProfile,
  AutoRouterRankingEdit,
} from './types'

/** The ranking being edited: each profile's full list, in the order it is saved. */
export type RankingDraft = Record<string, AutoRouterRankedModel[]>

export function toDraft(profiles: AutoRouterRankedProfile[]): RankingDraft {
  return Object.fromEntries(
    profiles.map((profile) => [profile.profile, profile.models])
  )
}

/**
 * Reads a typed score: empty means no score (the model is only a fallback), a
 * number from 0 to 1 is a score, anything else is invalid (undefined).
 */
export function parseScore(text: string): number | null | undefined {
  const trimmed = text.trim()
  if (trimmed === '') return null
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0 || value > 1) return undefined
  return value
}

/**
 * The order an edited list is saved in: scored models by score, highest first
 * (ties keep their order), then models without a score in the order the admin
 * set. The router compares scored models by score and price; the order only
 * decides when that comparison picks nothing, which is when unscored models are
 * tried.
 */
function normalize(entries: AutoRouterRankedModel[]): AutoRouterRankedModel[] {
  const scored = entries.filter((entry) => entry.score !== null)
  scored.sort((a, b) => (b.score ?? 0) - (a.score ?? 0))
  return [...scored, ...entries.filter((entry) => entry.score === null)]
}

/** Puts entry into list, replacing the model's row if it is listed, and reorders. */
function upsertEntry(
  list: AutoRouterRankedModel[],
  entry: AutoRouterRankedModel
): AutoRouterRankedModel[] {
  const at = list.findIndex((item) => item.model === entry.model)
  if (at < 0) return normalize([...list, entry])
  return normalize(list.map((item, index) => (index === at ? entry : item)))
}

/** Sets a listed model's score; null leaves it only as a fallback. */
export function setScore(
  draft: RankingDraft,
  profile: string,
  model: string,
  score: number | null
): RankingDraft {
  const list = draft[profile] ?? []
  return {
    ...draft,
    [profile]: normalize(
      list.map((entry) => (entry.model === model ? { ...entry, score } : entry))
    ),
  }
}

export function removeModel(
  draft: RankingDraft,
  profile: string,
  model: string
): RankingDraft {
  const list = draft[profile] ?? []
  return {
    ...draft,
    [profile]: normalize(list.filter((entry) => entry.model !== model)),
  }
}

/**
 * Adds one model to several profiles, each with its own score (null: only a
 * fallback). A profile that lists the model already gets the new score.
 */
export function addModel(
  draft: RankingDraft,
  model: string,
  served: boolean,
  scores: Record<string, number | null>
): RankingDraft {
  const next = { ...draft }
  for (const [profile, score] of Object.entries(scores)) {
    next[profile] = upsertEntry(next[profile] ?? [], { model, score, served })
  }
  return next
}

/**
 * Where a model without a score can move: the nearest model above or below it
 * that has no score either and passes visible, so a move never lands on a row
 * the filters hide. -1 when there is none. Scored models are ordered by score
 * and do not move.
 */
export function moveTarget(
  list: AutoRouterRankedModel[],
  model: string,
  direction: 'up' | 'down',
  visible: (entry: AutoRouterRankedModel) => boolean
): number {
  const from = list.findIndex((entry) => entry.model === model)
  if (from < 0 || list[from].score !== null) return -1
  const step = direction === 'up' ? -1 : 1
  for (let at = from + step; at >= 0 && at < list.length; at += step) {
    if (list[at].score !== null) return -1
    if (visible(list[at])) return at
  }
  return -1
}

/** Swaps a model without a score with its neighbor from moveTarget. */
export function moveUnscored(
  draft: RankingDraft,
  profile: string,
  model: string,
  direction: 'up' | 'down',
  visible: (entry: AutoRouterRankedModel) => boolean
): RankingDraft {
  const list = draft[profile] ?? []
  const to = moveTarget(list, model, direction, visible)
  if (to < 0) return draft
  const from = list.findIndex((entry) => entry.model === model)
  const next = [...list]
  ;[next[from], next[to]] = [next[to], next[from]]
  return { ...draft, [profile]: next }
}

/** The 1-based place the model would take in the list with this score. */
export function previewRank(
  list: AutoRouterRankedModel[],
  model: string,
  score: number | null
): number {
  const placed = upsertEntry(list, { model, score, served: false })
  return placed.findIndex((entry) => entry.model === model) + 1
}

/** The best score among the other listed models the gateway serves; null when there is none. */
export function topServedScore(
  list: AutoRouterRankedModel[],
  model: string
): number | null {
  let top: number | null = null
  for (const entry of list) {
    if (entry.model === model || !entry.served || entry.score === null) continue
    top = top === null ? entry.score : Math.max(top, entry.score)
  }
  return top
}

/**
 * The fixed preferences whose comparison a model with this score could enter:
 * the router compares the models within the preference's cost weight of the
 * best score it can use. Only the gateway's served models count, and prices,
 * allowlists and groups are left out, so this is an estimate.
 */
export function reachablePreferences(
  list: AutoRouterRankedModel[],
  model: string,
  score: number,
  weights: CostWeights
): AutoPreference[] {
  const top = topServedScore(list, model)
  if (top === null || score >= top) return [...AUTO_PREFERENCES]
  return AUTO_PREFERENCES.filter((preference) => {
    // 与后端一样把权重限制在 [0, 1]：库里被直接改坏的值不能让估算失真。
    const weight = Math.min(Math.max(weights[preference], 0), 1)
    return score >= top - weight
  })
}

/** The profiles whose list differs from the loaded ranking, in profile order. */
export function changedProfiles(
  original: RankingDraft,
  draft: RankingDraft
): string[] {
  return Object.keys(draft).filter((profile) => {
    const before = original[profile] ?? []
    const after = draft[profile]
    return (
      before.length !== after.length ||
      after.some(
        (entry, index) =>
          entry.model !== before[index].model ||
          entry.score !== before[index].score
      )
    )
  })
}

/** The save request: every profile, and the ranking times the editor loaded. */
export function toEdit(
  draft: RankingDraft,
  sources: Record<string, string> | null
): AutoRouterRankingEdit {
  return {
    base: {
      generated_at: sources?.generated_at ?? '',
      edited_at: sources?.edited_at ?? '',
    },
    profiles: Object.entries(draft).map(([profile, models]) => ({
      profile,
      models: models.map((entry) => ({
        model: entry.model,
        score: entry.score,
      })),
    })),
  }
}

/** Every model the ranking lists, for picking a reference. */
export function listedModels(draft: RankingDraft): string[] {
  const names = new Set<string>()
  for (const list of Object.values(draft)) {
    for (const entry of list) names.add(entry.model)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
}

/** One profile's row in the add dialog. */
export type AddRow = { checked: boolean; scoreText: string }

/**
 * The add dialog's rows, seeded from a reference model: a profile where the
 * reference has a score is checked with that score plus offset (kept within 0
 * to 1); the other profiles start unchecked and empty, to fill in by hand.
 */
export function seedAddRows(
  draft: RankingDraft,
  reference: string,
  offset: number
): Record<string, AddRow> {
  return Object.fromEntries(
    Object.entries(draft).map(([profile, list]) => {
      const score = list.find((entry) => entry.model === reference)?.score
      if (score === null || score === undefined) {
        return [profile, { checked: false, scoreText: '' }]
      }
      const seeded = Math.min(Math.max(score + offset, 0), 1)
      return [profile, { checked: true, scoreText: seeded.toFixed(4) }]
    })
  )
}
