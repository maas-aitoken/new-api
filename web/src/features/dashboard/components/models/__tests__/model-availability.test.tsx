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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { getModelAvailability } from '@/features/dashboard/api'
import type { ModelAvailabilityRow } from '@/features/dashboard/types'

import { ModelAvailability } from '../model-availability'

vi.mock('@/features/dashboard/api', () => ({
  getModelAvailability: vi.fn(),
}))

const NOW = 1_753_964_400 // minute-aligned Unix seconds

function availabilityRow(
  modelName: string,
  success: number,
  streamErrors: number,
  failed: number
): ModelAvailabilityRow {
  return {
    model_name: modelName,
    success_count: success,
    stream_error_count: streamErrors,
    failed_count: failed,
    total_tokens: (success + streamErrors) * 100,
    last_called_at: NOW - 30,
  }
}

const queryClients: QueryClient[] = []

function renderPanel(
  models: ModelAvailabilityRow[] | null,
  options: { errorLogEnabled?: boolean } = {}
) {
  vi.mocked(getModelAvailability).mockImplementation(async (minutes = 10) =>
    models === null
      ? { success: false, message: 'availability unavailable' }
      : {
          success: true,
          data: {
            minutes,
            start_timestamp: NOW - minutes * 60,
            end_timestamp: NOW,
            error_log_enabled: options.errorLogEnabled ?? true,
            models,
          },
        }
  )
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <ModelAvailability />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function bodyRows() {
  return screen.getAllByRole('row').slice(1)
}

// Wilson ranks a, c, b; request totals rank b, a, c; tokens rank c, a, b.
const SORTABLE_ROWS = [
  { ...availabilityRow('model-a', 50, 0, 0), total_tokens: 6000 },
  { ...availabilityRow('model-b', 90, 10, 20), total_tokens: 1500 },
  { ...availabilityRow('model-c', 30, 0, 0), total_tokens: 9000 },
]

function rankAndModelColumns() {
  return bodyRows().map((row) => {
    const cells = within(row).getAllByRole('cell')
    return [cells[0].textContent, cells[1].textContent]
  })
}

async function sortBy(
  user: ReturnType<typeof userEvent.setup>,
  column: string,
  direction: 'Asc' | 'Desc'
) {
  await user.click(
    within(screen.getByRole('columnheader', { name: column })).getByRole(
      'button'
    )
  )
  await user.click(await screen.findByRole('menuitem', { name: direction }))
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
}

function cellInColumn(modelName: string, column: string) {
  const table = screen.getByRole('table')
  const headers = within(table).getAllByRole('columnheader')
  const index = headers.findIndex(
    (header) => header.textContent?.trim() === column
  )
  expect(index).toBeGreaterThanOrEqual(0)
  const row = within(table).getByText(modelName).closest('tr') as HTMLElement
  return within(row).getAllByRole('cell')[index]
}

afterEach(() => {
  for (const client of queryClients.splice(0)) client.clear()
})

describe('ModelAvailability', () => {
  test('rows are ranked by confidence with a status badge per model', async () => {
    renderPanel([
      availabilityRow('model-a', 3, 0, 0),
      availabilityRow('model-b', 200, 0, 0),
      availabilityRow('model-c', 45, 3, 2),
    ])

    await screen.findByRole('table')

    const summary = bodyRows().map((row) => {
      const cells = within(row).getAllByRole('cell')
      return [cells[0].textContent, cells[1].textContent, cells[2].textContent]
    })
    expect(summary).toEqual([
      ['1', 'model-b', 'Stable'],
      ['2', 'model-c', 'Degraded'],
      ['3', 'model-a', 'Insufficient samples'],
    ])
  })

  test('overview shows the top model and the call breakdown', async () => {
    renderPanel([
      availabilityRow('model-a', 3, 0, 0),
      availabilityRow('model-b', 200, 0, 0),
      availabilityRow('model-c', 45, 3, 2),
    ])

    await screen.findByRole('table')

    expect(screen.getByTitle('model-b')).toBeInTheDocument()
    expect(screen.getByTitle('100% · Stable')).toBeInTheDocument()
    expect(screen.getByTitle('253')).toBeInTheDocument()
    expect(
      screen.getByTitle('248 succeeded · 3 interrupted · 2 failed')
    ).toBeInTheDocument()
  })

  test('choosing the 1 hour window requests 60 minutes and marks it pressed', async () => {
    const user = userEvent.setup()
    renderPanel([availabilityRow('model-b', 200, 0, 0)])
    await screen.findByRole('table')

    await user.click(screen.getByRole('button', { name: '1 hour' }))

    await waitFor(() =>
      expect(vi.mocked(getModelAvailability)).toHaveBeenLastCalledWith(60)
    )
    expect(screen.getByRole('button', { name: '1 hour' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    expect(screen.getByRole('button', { name: '10 minutes' })).toHaveAttribute(
      'aria-pressed',
      'false'
    )
  })

  test('a window without calls shows the empty table message', async () => {
    renderPanel([])

    expect(
      await screen.findByText('No model calls in this window')
    ).toBeInTheDocument()
  })

  test('a failed request shows the load error instead of the table', async () => {
    renderPanel(null)

    expect(
      await screen.findByText('Unable to load model availability')
    ).toBeInTheDocument()
    expect(screen.queryByRole('table')).toBeNull()
  })

  test('disabled error logging shows a notice that failures are not counted', async () => {
    renderPanel([availabilityRow('model-b', 200, 0, 0)], {
      errorLogEnabled: false,
    })

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Error logging is disabled, so failed requests are not counted here. Enable ERROR_LOG_ENABLED for accurate availability.'
    )
    expect(screen.getByRole('table')).toBeInTheDocument()
  })

  test('enabled error logging shows no failure-counting notice', async () => {
    renderPanel([availabilityRow('model-b', 200, 0, 0)], {
      errorLogEnabled: true,
    })

    await screen.findByRole('table')

    expect(screen.queryByRole('alert')).toBeNull()
  })

  test.each([
    ['Requests', ['model-b', 'model-a', 'model-c']],
    ['Tokens', ['model-c', 'model-a', 'model-b']],
    ['Average TPM', ['model-c', 'model-a', 'model-b']],
  ])(
    'sorting %s descending reorders models and keeps their confidence rank',
    async (column, expectedModels) => {
      const user = userEvent.setup()
      renderPanel(SORTABLE_ROWS)
      await screen.findByRole('table')

      await sortBy(user, column, 'Desc')

      const confidenceRank: Record<string, string> = {
        'model-a': '1',
        'model-c': '2',
        'model-b': '3',
      }
      expect(rankAndModelColumns()).toEqual(
        expectedModels.map((model) => [confidenceRank[model], model])
      )
    }
  )

  test('sorting rank ascending after another sort restores the confidence order', async () => {
    const user = userEvent.setup()
    renderPanel(SORTABLE_ROWS)
    await screen.findByRole('table')

    await sortBy(user, 'Requests', 'Desc')
    await sortBy(user, 'Rank', 'Asc')

    expect(rankAndModelColumns()).toEqual([
      ['1', 'model-a'],
      ['2', 'model-c'],
      ['3', 'model-b'],
    ])
  })

  test('the successes / failures column counts interrupted streams as failures', async () => {
    renderPanel(SORTABLE_ROWS)
    await screen.findByRole('table')

    expect(cellInColumn('model-b', 'Successes / Failures')).toHaveTextContent(
      /^90\s*\/\s*30$/
    )
    expect(cellInColumn('model-a', 'Successes / Failures')).toHaveTextContent(
      /^50\s*\/\s*0$/
    )
  })

  test('hovering a successes / failures cell shows the per-model breakdown', async () => {
    const user = userEvent.setup()
    renderPanel(SORTABLE_ROWS)
    await screen.findByRole('table')

    await user.hover(
      within(cellInColumn('model-b', 'Successes / Failures')).getByText('90')
    )

    expect(
      await screen.findByText('90 succeeded · 10 interrupted · 20 failed')
    ).toBeInTheDocument()
  })

  test('the table scrolls inside a bounded container with a sticky header', async () => {
    renderPanel(SORTABLE_ROWS)
    const table = await screen.findByRole('table')

    const scrollContainer = table.parentElement as HTMLElement
    expect(scrollContainer).toHaveClass('overflow-auto')
    expect(scrollContainer.className).toMatch(/max-h-\[/)
    expect(within(table).getAllByRole('rowgroup')[0]).toHaveClass(
      'sticky',
      'top-0'
    )
  })

  test('the per-model rate column is labelled Availability, not Success rate', async () => {
    renderPanel([availabilityRow('model-b', 200, 0, 0)])

    await screen.findByRole('table')

    expect(
      screen.getByRole('columnheader', { name: 'Availability' })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('columnheader', { name: 'Success rate' })
    ).toBeNull()
  })
})
