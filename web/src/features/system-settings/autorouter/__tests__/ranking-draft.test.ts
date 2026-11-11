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
  addModel,
  changedProfiles,
  moveUnscored,
  parseScore,
  previewRank,
  reachablePreferences,
  seedAddRows,
  setScore,
  type RankingDraft,
} from '../ranking-draft'
import type { AutoRouterRankedModel } from '../types'

const WEIGHTS = { capability: 0, balanced: 0.07, cost: 0.3 }

function entry(
  model: string,
  score: number | null,
  served = false
): AutoRouterRankedModel {
  return { model, score, served }
}

const models = (list: AutoRouterRankedModel[]) => list.map((item) => item.model)

// a、b 有分（b 与 c 同分），d、e、f 只当兜底。
const draft: RankingDraft = {
  'code.build': [
    entry('a', 0.95, true),
    entry('b', 0.9),
    entry('c', 0.9, true),
    entry('d', null),
    entry('e', null),
    entry('f', null),
  ],
  general: [entry('g', 0.8, true)],
}

describe('editing scores', () => {
  test('a new score moves the model among the scored ones and leaves the other profiles alone', () => {
    const next = setScore(draft, 'code.build', 'a', 0.85)

    expect(models(next['code.build'])).toEqual(['b', 'c', 'a', 'd', 'e', 'f'])
    expect(next.general).toBe(draft.general)
  })

  test('models with equal scores keep their order in the list', () => {
    const next = setScore(draft, 'code.build', 'c', 0.95)

    expect(models(next['code.build'])).toEqual(['a', 'c', 'b', 'd', 'e', 'f'])
  })

  test('clearing a score makes the model the first fallback, and scoring a fallback lifts it among the scored', () => {
    expect(
      models(setScore(draft, 'code.build', 'b', null)['code.build'])
    ).toEqual(['a', 'c', 'b', 'd', 'e', 'f'])
    expect(
      models(setScore(draft, 'code.build', 'e', 0.99)['code.build'])
    ).toEqual(['e', 'a', 'b', 'c', 'd', 'f'])
  })

  test('a typed score is empty for a fallback, a number from 0 to 1, or invalid', () => {
    expect(parseScore('  ')).toBeNull()
    expect(parseScore('0.9734')).toBe(0.9734)
    expect(parseScore('1')).toBe(1)
    expect(parseScore('1.2')).toBeUndefined()
    expect(parseScore('-0.1')).toBeUndefined()
    expect(parseScore('high')).toBeUndefined()
  })
})

describe('ordering fallbacks', () => {
  const all = () => true

  test('a fallback swaps with its neighbor and never moves above the scored models', () => {
    expect(
      models(moveUnscored(draft, 'code.build', 'e', 'up', all)['code.build'])
    ).toEqual(['a', 'b', 'c', 'e', 'd', 'f'])
    expect(moveUnscored(draft, 'code.build', 'd', 'up', all)).toBe(draft)
    expect(moveUnscored(draft, 'code.build', 'a', 'down', all)).toBe(draft)
  })

  test('a move skips the rows the filters hide', () => {
    const next = moveUnscored(
      draft,
      'code.build',
      'f',
      'up',
      (item) => item.model !== 'e'
    )

    expect(models(next['code.build'])).toEqual(['a', 'b', 'c', 'f', 'e', 'd'])
  })
})

describe('previewing an added model', () => {
  test('a score equal to a listed one lands after it, and no score lands after every listed model', () => {
    const list = draft['code.build']

    expect(previewRank(list, 'x', 0.9)).toBe(4)
    expect(previewRank(list, 'x', 0.96)).toBe(1)
    expect(previewRank(list, 'x', null)).toBe(7)
  })

  test('a model that is listed already is placed by its new score, not counted twice', () => {
    expect(previewRank(draft['code.build'], 'a', 0.1)).toBe(3)
  })

  test('a preference compares the model when it is within the weight of the best served score', () => {
    const list = draft['code.build']

    expect(reachablePreferences(list, 'x', 0.95, WEIGHTS)).toEqual([
      'capability',
      'balanced',
      'cost',
    ])
    expect(reachablePreferences(list, 'x', 0.9, WEIGHTS)).toEqual([
      'balanced',
      'cost',
    ])
    expect(reachablePreferences(list, 'x', 0.6, WEIGHTS)).toEqual([])
  })

  test('only models the gateway serves set the best score', () => {
    const list = [entry('top', 0.99), entry('served', 0.7, true)]

    expect(reachablePreferences(list, 'x', 0.72, WEIGHTS)).toEqual([
      'capability',
      'balanced',
      'cost',
    ])
    expect(
      reachablePreferences([entry('top', 0.99)], 'x', 0.1, WEIGHTS)
    ).toEqual(['capability', 'balanced', 'cost'])
  })
})

describe('adding a model', () => {
  test('the reference model seeds the profiles where it has a score, plus the offset kept within 0 to 1', () => {
    const rows = seedAddRows(draft, 'a', 0.1)

    expect(rows['code.build']).toEqual({ checked: true, scoreText: '1.0000' })
    expect(rows.general).toEqual({ checked: false, scoreText: '' })
    expect(seedAddRows(draft, 'b', -0.95)['code.build'].scoreText).toBe(
      '0.0000'
    )
  })

  test('the model joins each profile with its own score and replaces the score where it is listed', () => {
    const next = addModel(draft, 'b', true, {
      'code.build': 0.99,
      general: null,
    })

    expect(next['code.build'][0]).toEqual(entry('b', 0.99, true))
    expect(next['code.build']).toHaveLength(6)
    expect(models(next.general)).toEqual(['g', 'b'])
  })
})

test('a profile counts as changed when its models, scores or order differ from the loaded ranking', () => {
  const reordered = moveUnscored(draft, 'code.build', 'e', 'up', () => true)
  const unchanged = setScore(draft, 'general', 'g', 0.8)

  expect(changedProfiles(draft, reordered)).toEqual(['code.build'])
  expect(changedProfiles(draft, unchanged)).toEqual([])
  expect(changedProfiles(draft, setScore(draft, 'general', 'g', 0.7))).toEqual([
    'general',
  ])
})
