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
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { SystemTaskStatus } from '../../types'
import { RankingStatus } from '../ranking-status'

type StatusOverrides = {
  rankKeysReady?: boolean
  rankingCurrent?: boolean
  refresh?: { status: SystemTaskStatus; error?: string }
  /** The ranking was saved from the editor, which stamps edited_at. */
  edited?: boolean
}

function mockStatus(overrides: StatusOverrides = {}) {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        enabled: true,
        jev_ready: true,
        rank_keys_ready: overrides.rankKeysReady ?? true,
        ranking_current: overrides.rankingCurrent ?? true,
        taxonomy_version: '3',
        rank_sources: {
          generated_at: '2026-10-28',
          livebench: '20261025',
          ...(overrides.edited ? { edited_at: '2026-11-09T00:30:00Z' } : {}),
        },
        sources: {},
        rank_refresh: overrides.refresh && {
          id: 1,
          task_id: 'systask_1',
          type: 'autorouter_rank',
          created_at: 1790000000,
          updated_at: 1790000100,
          ...overrides.refresh,
        },
      },
    },
  })
}

function renderStatus() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RankingStatus />
    </QueryClientProvider>
  )
}

test('refresh is unavailable and says why while the leaderboard keys are missing', async () => {
  mockStatus({ rankKeysReady: false })
  renderStatus()

  expect(await screen.findByRole('button', { name: 'Refresh now' })).toBeDisabled()
  expect(
    screen.getByText(
      'Set the Artificial Analysis key and either the Design Arena or the OpenRouter key below to refresh.'
    )
  ).toBeVisible()
})

test('refresh now starts the refresh task', async () => {
  const user = userEvent.setup()
  mockStatus()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { created: true, task: { task_id: 'systask_2' } } },
  })
  renderStatus()

  await user.click(await screen.findByRole('button', { name: 'Refresh now' }))

  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith('/api/autorouter/ranking/refresh')
  )
})

test('both actions are disabled while a refresh is running', async () => {
  mockStatus({ refresh: { status: 'running' } })
  renderStatus()

  expect(await screen.findByRole('button', { name: 'Refresh now' })).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Restore built-in ranking' })
  ).toBeDisabled()
})

test('restoring the built-in ranking asks first and only then posts', async () => {
  const user = userEvent.setup()
  mockStatus()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: null },
  })
  renderStatus()

  await user.click(
    await screen.findByRole('button', { name: 'Restore built-in ranking' })
  )
  const dialog = await screen.findByRole('alertdialog')
  expect(api.post).not.toHaveBeenCalled()

  await user.click(within(dialog).getByRole('button', { name: 'Restore' }))

  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith('/api/autorouter/ranking/restore')
  )
})

test('a hand-edited ranking is flagged, and refreshing over it asks first', async () => {
  const user = userEvent.setup()
  mockStatus({ edited: true })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { created: true, task: { task_id: 'systask_2' } } },
  })
  renderStatus()

  expect(await screen.findByText('Edited by hand')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Refresh now' }))
  const dialog = await screen.findByRole('alertdialog')
  expect(api.post).not.toHaveBeenCalled()

  await user.click(within(dialog).getByRole('button', { name: 'Refresh now' }))

  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith('/api/autorouter/ranking/refresh')
  )
})

test('a stored ranking from an older taxonomy is flagged as out of date', async () => {
  mockStatus({ rankingCurrent: false })
  renderStatus()

  expect(await screen.findByText('Out of date')).toBeVisible()
  expect(screen.getByText('The stored ranking is out of date')).toBeVisible()
})

test('a failed refresh shows its error next to the failed status', async () => {
  mockStatus({ refresh: { status: 'failed', error: 'livebench: 502 bad gateway' } })
  renderStatus()

  expect(await screen.findByText('failed')).toBeVisible()
  expect(screen.getByText('livebench: 502 bad gateway')).toBeVisible()
})
