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
  DEFAULT_COST_WEIGHTS,
  changedOptions,
  normalizeDifficultyEfforts,
  parseCostWeights,
  serializeCostWeights,
} from '../settings'

describe('parseCostWeights', () => {
  test.each([
    ['an empty object', '{}'],
    ['malformed JSON', '{not json'],
    ['JSON null', 'null'],
    ['an empty string', ''],
  ])('%s keeps every built-in weight', (_name, raw) => {
    expect(parseCostWeights(raw)).toEqual(DEFAULT_COST_WEIGHTS)
  })

  test('a stored weight replaces its default and the others keep theirs', () => {
    expect(parseCostWeights('{"balanced":0.12}')).toEqual({
      ...DEFAULT_COST_WEIGHTS,
      balanced: 0.12,
    })
  })

  test('a non-numeric weight and an unknown preference are ignored', () => {
    expect(parseCostWeights('{"cost":"high","cheap":0.9}')).toEqual(
      DEFAULT_COST_WEIGHTS
    )
  })

  test('adaptive has no weight of its own, so a stored one is ignored and never written back', () => {
    const weights = parseCostWeights('{"adaptive":0.5,"cost":0.2}')

    expect(weights).toEqual({ ...DEFAULT_COST_WEIGHTS, cost: 0.2 })
    expect(serializeCostWeights(weights)).not.toContain('adaptive')
  })
})

test('serializeCostWeights writes every preference in a fixed order', () => {
  expect(
    serializeCostWeights({ cost: 0.4, capability: 0, balanced: 0.1 })
  ).toBe('{"capability":0,"balanced":0.1,"cost":0.4}')
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
