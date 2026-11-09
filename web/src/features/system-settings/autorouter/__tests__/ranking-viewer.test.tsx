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

import { RankingViewer } from '../ranking-viewer'

const SOURCES = { generated_at: '2026-10-28T08:39:28Z', livebench: '20261025' }

// code.build 有两个有分的模型和一个只当兜底的；general 只有一个。网关上有 private-coder。
function mockRanking() {
  return vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url === '/api/channel/models_enabled') {
      return {
        data: {
          success: true,
          data: ['gpt-6.1-terra', 'private-coder', 'auto'],
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          current: true,
          taxonomy_version: '3',
          sources: SOURCES,
          profiles: [
            {
              profile: 'code.build',
              models: [
                { model: 'claude-opus-5-5', score: 0.9734, served: false },
                { model: 'gpt-6.1-terra', score: 0.9, served: true },
                { model: 'private-model', score: null, served: false },
              ],
            },
            {
              profile: 'general',
              models: [{ model: 'gpt-6.1-terra', score: 0.9, served: true }],
            },
          ],
        },
      },
    }
  })
}

function renderViewer() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RankingViewer
        costWeights={{ capability: 0, balanced: 0.07, cost: 0.3 }}
      />
    </QueryClientProvider>
  )
}

async function openEditor(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Show ranking' }))
  await user.click(await screen.findByRole('button', { name: 'Edit ranking' }))
}

test('the ranking is not fetched until the viewer is opened', async () => {
  const get = mockRanking()
  const user = userEvent.setup()
  renderViewer()

  expect(get).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Show ranking' }))

  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/autorouter/ranking')
  )
})

test('the first profile is listed in rank order with four-decimal scores and a dash for a model without one', async () => {
  mockRanking()
  const user = userEvent.setup()
  renderViewer()

  await user.click(screen.getByRole('button', { name: 'Show ranking' }))

  const rows = await screen.findAllByRole('row')
  const body = rows.slice(1)
  expect(body).toHaveLength(3)
  expect(within(body[0]).getByText('claude-opus-5-5')).toBeVisible()
  expect(within(body[0]).getByText('0.9734')).toBeVisible()
  expect(within(body[1]).getByText('On the gateway')).toBeVisible()
  expect(within(body[2]).getByText('private-model')).toBeVisible()
  expect(within(body[2]).getByText('-')).toBeVisible()
})

test('hiding the viewer removes the table', async () => {
  mockRanking()
  const user = userEvent.setup()
  renderViewer()

  await user.click(screen.getByRole('button', { name: 'Show ranking' }))
  await screen.findByText('claude-opus-5-5')
  await user.click(screen.getByRole('button', { name: 'Hide ranking' }))

  expect(screen.queryByText('claude-opus-5-5')).not.toBeInTheDocument()
})

test('the gateway filter keeps only served models and their place in the whole list', async () => {
  mockRanking()
  const user = userEvent.setup()
  renderViewer()

  await user.click(screen.getByRole('button', { name: 'Show ranking' }))
  await screen.findByText('claude-opus-5-5')
  await user.click(
    screen.getByRole('switch', { name: 'Only models on the gateway' })
  )

  const body = screen.getAllByRole('row').slice(1)
  expect(body).toHaveLength(1)
  expect(within(body[0]).getByText('gpt-6.1-terra')).toBeVisible()
  expect(within(body[0]).getByText('2')).toBeVisible()
})

test('a changed score reorders the list and saving sends every profile with the loaded ranking times', async () => {
  mockRanking()
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: null },
  })
  const user = userEvent.setup()
  renderViewer()
  await openEditor(user)

  const score = screen.getByRole('textbox', { name: 'Score of gpt-6.1-terra' })
  await user.clear(score)
  await user.type(score, '0.99')
  await user.tab()

  const body = screen.getAllByRole('row').slice(1)
  expect(within(body[0]).getByText('gpt-6.1-terra')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save ranking' }))
  expect(put).not.toHaveBeenCalled()
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Save ranking' }))

  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/autorouter/ranking', {
      base: { generated_at: SOURCES.generated_at, edited_at: '' },
      profiles: [
        {
          profile: 'code.build',
          models: [
            { model: 'gpt-6.1-terra', score: 0.99 },
            { model: 'claude-opus-5-5', score: 0.9734 },
            { model: 'private-model', score: null },
          ],
        },
        {
          profile: 'general',
          models: [{ model: 'gpt-6.1-terra', score: 0.9 }],
        },
      ],
    })
  )
  expect(
    await screen.findByRole('button', { name: 'Edit ranking' })
  ).toBeVisible()
})

test('saving stays disabled until something changes, and discarding brings the loaded ranking back', async () => {
  mockRanking()
  const put = vi.spyOn(api, 'put')
  const user = userEvent.setup()
  renderViewer()
  await openEditor(user)

  expect(screen.getByRole('button', { name: 'Save ranking' })).toBeDisabled()
  await user.click(
    screen.getByRole('button', {
      name: 'Remove claude-opus-5-5 from this profile',
    })
  )
  expect(screen.queryByText('claude-opus-5-5')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Save ranking' })).toBeEnabled()

  await user.click(screen.getByRole('button', { name: 'Discard changes' }))

  expect(screen.getByText('claude-opus-5-5')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Edit ranking' })).toBeVisible()
  expect(put).not.toHaveBeenCalled()
})

test('a save the server refuses keeps the editor and the edits', async () => {
  mockRanking()
  vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: false,
      message:
        'The ranking was changed after you loaded it. Reload it and make your edits again.',
    },
  })
  const user = userEvent.setup()
  renderViewer()
  await openEditor(user)

  await user.click(
    screen.getByRole('button', {
      name: 'Remove claude-opus-5-5 from this profile',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Save ranking' }))
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Save ranking' }))

  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(screen.getByRole('button', { name: 'Discard changes' })).toBeVisible()
  expect(screen.queryByText('claude-opus-5-5')).not.toBeInTheDocument()
})

test('adding a model takes the scores of a reference model plus the offset in the profiles where it is scored', async () => {
  mockRanking()
  const user = userEvent.setup()
  renderViewer()
  await openEditor(user)

  await user.click(screen.getByRole('button', { name: 'Add a model' }))
  const dialog = await screen.findByRole('dialog')
  await user.type(
    within(dialog).getByRole('combobox', { name: 'Model' }),
    'private-coder'
  )
  await user.click(
    within(dialog).getByRole('combobox', { name: 'Reference model' })
  )
  await user.click(
    await screen.findByRole('option', { name: 'claude-opus-5-5' })
  )
  const offset = within(dialog).getByLabelText('Offset')
  await user.clear(offset)
  await user.type(offset, '-0.1')

  // 0.9734 - 0.1 = 0.8734，比网关上最好的 gpt-6.1-terra（0.9）低 0.0266：
  // 在均衡和成本优先的候选带里，不在能力优先（权重 0）的候选带里。
  expect(
    within(dialog).getByRole('textbox', { name: 'Score in code.build' })
  ).toHaveValue('0.8734')
  expect(
    within(dialog).getByRole('checkbox', { name: 'Add to code.build' })
  ).toBeChecked()
  expect(
    within(dialog).getByRole('checkbox', { name: 'Add to general' })
  ).not.toBeChecked()
  expect(
    within(dialog).getByText('0.0266 below the best on the gateway')
  ).toBeVisible()
  expect(within(dialog).getByText('Balanced')).toBeVisible()
  expect(within(dialog).getByText('Cost first')).toBeVisible()
  expect(within(dialog).queryByText('Capability first')).not.toBeInTheDocument()

  await user.click(
    within(dialog).getByRole('button', { name: 'Add to 1 profiles' })
  )

  const body = screen.getAllByRole('row').slice(1)
  expect(within(body[2]).getByText('private-coder')).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Score of private-coder' })
  ).toHaveValue('0.8734')
})
