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
import { describe, expect, it } from 'vitest'

import { buildApiParams } from '../utils'

describe('buildApiParams', () => {
  it('maps the stream error filter to the dedicated backend parameter', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      searchParams: { type: ['stream_error'] },
      columnFilters: [],
      isAdmin: true,
    })

    expect(params.stream_error).toBe(true)
    expect(params.type).toBeUndefined()
  })

  it('maps the retry filter to the dedicated backend parameter', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      searchParams: { type: ['retry'] },
      columnFilters: [],
      isAdmin: true,
    })

    expect(params.retry).toBe(true)
    expect(params.type).toBeUndefined()
  })

  it('does not expose the retry filter in self view API parameters', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      searchParams: { type: ['retry'] },
      columnFilters: [],
      isAdmin: false,
    })

    expect(params.retry).toBeUndefined()
    expect(params.type).toBeUndefined()
  })

  it('uses the same type resolver when a column filter overrides the URL', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 20,
      searchParams: { type: ['5'] },
      columnFilters: [{ id: 'type', value: ['retry'] }],
      isAdmin: true,
    })

    expect(params.retry).toBe(true)
    expect(params.stream_error).toBeUndefined()
    expect(params.type).toBeUndefined()
  })

  it.each([
    ['only', true],
    ['exclude', false],
    [undefined, undefined],
  ] as const)(
    'maps the auto routing filter %s to auto_routed=%s for admins and users alike',
    (autoRouted, expected) => {
      for (const isAdmin of [true, false]) {
        const params = buildApiParams({
          page: 1,
          pageSize: 20,
          searchParams: { autoRouted },
          columnFilters: [],
          isAdmin,
        })

        expect(params.auto_routed).toBe(expected)
      }
    }
  )
})
