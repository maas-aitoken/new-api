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
import { describe, expect, test } from 'vitest'

import {
  DEFAULT_PREFERENCE_WEIGHTS,
  changedOptions,
  normalizeDifficultyEfforts,
  parsePreferenceWeights,
  serializePreferenceWeights,
} from '../settings'

describe('parsePreferenceWeights', () => {
  test.each([
    ['an empty object', '{}'],
    ['malformed JSON', '{not json'],
    ['JSON null', 'null'],
    ['an empty string', ''],
  ])('%s keeps every built-in weight', (_name, raw) => {
    expect(parsePreferenceWeights(raw)).toEqual(DEFAULT_PREFERENCE_WEIGHTS)
  })

  test('stored dimensions replace their defaults and the others keep theirs', () => {
    expect(
      parsePreferenceWeights(
        '{"balanced":{"quality":0.4,"cost":0.12,"temperature":0}}'
      )
    ).toEqual({
      ...DEFAULT_PREFERENCE_WEIGHTS,
      balanced: {
        ...DEFAULT_PREFERENCE_WEIGHTS.balanced,
        quality: 0.4,
        cost: 0.12,
        temperature: 0,
      },
    })
  })

  test('a stored policy without quality keeps the built-in quality strength', () => {
    expect(parsePreferenceWeights('{"balanced":{"cost":0.2}}')).toEqual({
      ...DEFAULT_PREFERENCE_WEIGHTS,
      balanced: { ...DEFAULT_PREFERENCE_WEIGHTS.balanced, cost: 0.2 },
    })
  })

  test('a non-numeric dimension and an unknown preference are ignored', () => {
    expect(
      parsePreferenceWeights('{"cost":"high","cheap":{"cost":0.9}}')
    ).toEqual(DEFAULT_PREFERENCE_WEIGHTS)
  })

  test('adaptive has no weights of its own, so a stored one is ignored and never written back', () => {
    const weights = parsePreferenceWeights(
      '{"adaptive":{"cost":0.5},"cost":{"cost":0.2}}'
    )

    expect(weights).toEqual({
      ...DEFAULT_PREFERENCE_WEIGHTS,
      cost: { ...DEFAULT_PREFERENCE_WEIGHTS.cost, cost: 0.2 },
    })
    expect(serializePreferenceWeights(weights)).not.toContain('adaptive')
  })
})

test('serializePreferenceWeights writes every preference in a fixed order', () => {
  const weights = parsePreferenceWeights('{}')
  expect(serializePreferenceWeights(weights)).toBe(
    '{"capability":{"quality":0.6,"cost":0,"capacity":0.2,"success":0.2,"temperature":0.12},"balanced":{"quality":0.4,"cost":0.2,"capacity":0.2,"success":0.2,"temperature":0.12},"cost":{"quality":0.2,"cost":0.4,"capacity":0.2,"success":0.2,"temperature":0.12}}'
  )
})

describe('normalizeDifficultyEfforts', () => {
  test('a list shorter than the four rungs repeats its last entry', () => {
    expect(normalizeDifficultyEfforts(['low', 'high'])).toEqual([
      'low',
      'high',
      'high',
      'high',
    ])
  })

  test('entries past the fourth rung are dropped', () => {
    expect(
      normalizeDifficultyEfforts(['low', 'medium', 'high', 'xhigh', 'max'])
    ).toEqual(['low', 'medium', 'high', 'xhigh'])
  })

  test('an unknown effort and an empty list both read as medium', () => {
    expect(normalizeDifficultyEfforts(['extreme', 'low'])).toEqual([
      'medium',
      'low',
      'low',
      'low',
    ])
    expect(normalizeDifficultyEfforts([])).toEqual([
      'medium',
      'medium',
      'medium',
      'medium',
    ])
  })
})

describe('changedOptions', () => {
  test('returns only the options whose value differs, under the option prefix', () => {
    expect(
      changedOptions(
        { enabled: false, jev_timeout_ms: 5000, jev_model: 'a' },
        { enabled: true, jev_timeout_ms: 5000, jev_model: 'b' }
      )
    ).toEqual([
      { key: 'autorouter_setting.enabled', value: true },
      { key: 'autorouter_setting.jev_model', value: 'b' },
    ])
  })

  test('returns nothing when no value differs', () => {
    expect(changedOptions({ enabled: true }, { enabled: true })).toEqual([])
  })

  test('a secret whose default is empty is returned only once something is typed', () => {
    expect(changedOptions({ jev_api_key: '' }, { jev_api_key: '' })).toEqual([])
    expect(
      changedOptions({ jev_api_key: '' }, { jev_api_key: 'sk-new' })
    ).toEqual([{ key: 'autorouter_setting.jev_api_key', value: 'sk-new' }])
  })
})
