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

import { AUTO_PREFERENCES } from '@/features/keys/lib'

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

export type AutoPreference = (typeof AUTO_PREFERENCES)[number]
export type RoutingWeights = {
  quality: number
  cost: number
  capacity: number
  success: number
}
export type PreferenceWeights = Record<AutoPreference, RoutingWeights>

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

/** The policy the router uses for a preference the option leaves out. */
export const DEFAULT_PREFERENCE_WEIGHTS: PreferenceWeights = {
  capability: {
    quality: 0.6,
    cost: 0,
    capacity: 0.2,
    success: 0.2,
  },
  balanced: {
    quality: 0.4,
    cost: 0.2,
    capacity: 0.2,
    success: 0.2,
  },
  cost: {
    quality: 0.2,
    cost: 0.4,
    capacity: 0.2,
    success: 0.2,
  },
}

/** Reads the stored preference_weights JSON; invalid dimensions keep their defaults. */
export function parsePreferenceWeights(raw: string): PreferenceWeights {
  let stored: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed === 'object' && parsed !== null) {
      stored = parsed as Record<string, unknown>
    }
  } catch {
    /* A malformed value falls back to the defaults. */
  }
  const weights: PreferenceWeights = {
    capability: { ...DEFAULT_PREFERENCE_WEIGHTS.capability },
    balanced: { ...DEFAULT_PREFERENCE_WEIGHTS.balanced },
    cost: { ...DEFAULT_PREFERENCE_WEIGHTS.cost },
  }
  for (const preference of AUTO_PREFERENCES) {
    const entry = stored[preference]
    if (typeof entry !== 'object' || entry === null) continue
    for (const dimension of [
      'quality',
      'cost',
      'capacity',
      'success',
    ] as const) {
      const value = (entry as Record<string, unknown>)[dimension]
      if (typeof value === 'number' && Number.isFinite(value)) {
        weights[preference][dimension] = value
      }
    }
  }
  return weights
}

/** The stored form of every preference's policy, in a fixed order. */
export function serializePreferenceWeights(weights: PreferenceWeights): string {
  return JSON.stringify(weights)
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
