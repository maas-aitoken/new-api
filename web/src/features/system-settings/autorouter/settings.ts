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
import type { TFunction } from 'i18next'
import * as z from 'zod'

import {
  AUTO_PREFERENCES,
  type SELECTABLE_AUTO_PREFERENCES,
} from '@/features/keys/lib'

import type { UpdateOptionRequest } from '../types'

export const OPTION_PREFIX = 'autorouter_setting.'

/** An integer in [min, max], the same range the server checks before storing. */
export function integerBetween(t: TFunction, min: number, max: number) {
  return z
    .number()
    .int(t('Must be an integer between {{min}} and {{max}}', { min, max }))
    .min(min, t('Must be an integer between {{min}} and {{max}}', { min, max }))
    .max(max, t('Must be an integer between {{min}} and {{max}}', { min, max }))
}

/** A single token that goes into a URL or an HTTP header: no spaces or line breaks. */
export function plainText(t: TFunction, maxLength: number) {
  return z
    .string()
    .trim()
    .max(maxLength, t('Must be at most {{max}} characters', { max: maxLength }))
    .regex(/^\S*$/, t('Must not contain spaces or line breaks'))
}

/** A number in [0, 1]. */
export function fractionBetween(t: TFunction) {
  const message = t('Must be a number between {{min}} and {{max}}', {
    min: 0,
    max: 1,
  })
  return z.number().min(0, message).max(1, message)
}

/** A preference that carries a cost weight of its own. */
export type AutoPreference = (typeof AUTO_PREFERENCES)[number]
export type CostWeights = Record<AutoPreference, number>
/** What the platform default can be: a weighted preference, or deciding per request. */
export type SelectableAutoPreference =
  (typeof SELECTABLE_AUTO_PREFERENCES)[number]

/** Reasoning efforts a route can carry, weakest first. */
export const AUTO_ROUTER_EFFORTS: readonly string[] = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
]

/** The judge scores difficulty on four rungs: simple, moderate, complex, very complex. */
export const DIFFICULTY_RUNGS = 4

/** The weights the router uses for a preference the option leaves out. */
export const DEFAULT_COST_WEIGHTS: CostWeights = {
  capability: 0,
  balanced: 0.07,
  cost: 0.3,
}

/** Reads the stored cost_weights JSON; a missing or malformed entry keeps its default. */
export function parseCostWeights(raw: string): CostWeights {
  let stored: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed === 'object' && parsed !== null) {
      stored = parsed as Record<string, unknown>
    }
  } catch {
    /* A malformed value falls back to the defaults. */
  }
  const weights = { ...DEFAULT_COST_WEIGHTS }
  for (const preference of AUTO_PREFERENCES) {
    const value = stored[preference]
    if (typeof value === 'number' && Number.isFinite(value)) {
      weights[preference] = value
    }
  }
  return weights
}

/** The stored form of the weights, always with every preference in a fixed order. */
export function serializeCostWeights(weights: CostWeights): string {
  return JSON.stringify(
    Object.fromEntries(
      AUTO_PREFERENCES.map((preference) => [preference, weights[preference]])
    )
  )
}

/**
 * One effort per difficulty rung, the way the router reads the list: a rung past
 * the end reuses the last entry, and an unknown effort counts as medium.
 */
export function normalizeDifficultyEfforts(
  efforts: readonly string[]
): string[] {
  return Array.from({ length: DIFFICULTY_RUNGS }, (_, rung) => {
    const effort = efforts.at(Math.min(rung, efforts.length - 1)) ?? ''
    return AUTO_ROUTER_EFFORTS.includes(effort) ? effort : 'medium'
  })
}

/**
 * The option writes needed to turn previous into next. Both are keyed by the
 * option name without its prefix; composite values are already serialized.
 */
export function changedOptions(
  previous: Record<string, string | number | boolean>,
  next: Record<string, string | number | boolean>
): UpdateOptionRequest[] {
  return Object.keys(next)
    .filter((name) => next[name] !== previous[name])
    .map((name) => ({ key: OPTION_PREFIX + name, value: next[name] }))
}
