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
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { ConnectionSection } from '../connection-section'
import type { AutoRouterConnectionSource, AutoRouterSettings } from '../types'

const settings: AutoRouterSettings = {
  'autorouter_setting.enabled': true,
  'autorouter_setting.jev_endpoint': 'https://jev.example/v1/decisions',
  'autorouter_setting.jev_model': '',
  'autorouter_setting.jev_timeout_ms': 5000,
  'autorouter_setting.max_runes': 2000,
  'autorouter_setting.previous_max_runes': 500,
  'autorouter_setting.cache_ttl_seconds': 7200,
  'autorouter_setting.default_preference': 'balanced',
  'autorouter_setting.cost_weights': '{}',
  'autorouter_setting.continuation_task_change': 0.5,
  'autorouter_setting.upgrade_benefit': 0.5,
  'autorouter_setting.default_effort': 'medium',
  'autorouter_setting.difficulty_efforts': ['low', 'medium', 'high', 'xhigh'],
  'autorouter_setting.rank_refresh_enabled': true,
  'autorouter_setting.rank_refresh_interval_hours': 24,
  'autorouter_setting.route_log_mode': 'decisions',
  'autorouter_setting.route_log_retention_days': 7,
}

function Fixture(props: { values?: Partial<AutoRouterSettings> }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <ConnectionSection settings={{ ...settings, ...props.values }} />
      </SettingsPageProvider>
    </>
  )
}

function mockStatus(
  jevApiKey: AutoRouterConnectionSource,
  jevReady = true
) {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        enabled: true,
        jev_ready: jevReady,
        rank_keys_ready: false,
        ranking_current: true,
        taxonomy_version: '3',
        rank_sources: {},
        sources: {
          jev_endpoint: jevReady ? 'option' : '',
          jev_model: '',
          jev_api_key: jevApiKey,
          artificial_analysis_api_key: '',
          design_arena_api_key: '',
          openrouter_api_key: '',
        },
      },
    },
  })
}

function renderSection(values?: Partial<AutoRouterSettings>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture values={values} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('saving writes only the classifier key that was typed and empties the field afterwards', async () => {
  const user = userEvent.setup()
  mockStatus('option')
  renderSection()

  const key = await screen.findByLabelText('Classifier API key')
  expect(key).toHaveValue('')
  await user.type(key, 'sk-new-key')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.jev_api_key',
    value: 'sk-new-key',
  })
  await waitFor(() => expect(key).toHaveValue(''))
})

test('saving with nothing changed writes nothing', async () => {
  const user = userEvent.setup()
  mockStatus('option')
  renderSection()

  await screen.findByLabelText('Classifier API key')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).not.toHaveBeenCalled()
})

test('clearing a saved key asks first and then writes an empty value', async () => {
  const user = userEvent.setup()
  mockStatus('option')
  renderSection()

  await user.click(
    await screen.findByRole('button', { name: 'Clear saved key' })
  )
  const dialog = await screen.findByRole('alertdialog')
  expect(api.put).not.toHaveBeenCalled()

  await user.click(within(dialog).getByRole('button', { name: 'Clear' }))

  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'autorouter_setting.jev_api_key',
      value: '',
    })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

test('a key that comes from the environment names its variable and offers no clear button', async () => {
  mockStatus('env')
  renderSection()

  expect(
    await screen.findByText(
      'Using the JEV_API_KEY environment variable. A value entered here overrides it.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Clear saved key' })
  ).not.toBeInTheDocument()
})

test('a timeout under 100 ms is rejected on the field and nothing is saved', async () => {
  const user = userEvent.setup()
  mockStatus('option')
  renderSection()

  const timeout = await screen.findByLabelText('Classification timeout (ms)')
  fireEvent.change(timeout, { target: { value: '50' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(timeout).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()
})

test('enabled without any classifier endpoint warns, and typing an endpoint clears the warning', async () => {
  const user = userEvent.setup()
  mockStatus('', false)
  renderSection({ 'autorouter_setting.jev_endpoint': '' })

  expect(
    await screen.findByText('No classifier endpoint is configured')
  ).toBeVisible()

  await user.type(
    screen.getByLabelText('Classifier endpoint'),
    'https://jev.example'
  )
  await waitFor(() =>
    expect(
      screen.queryByText('No classifier endpoint is configured')
    ).not.toBeInTheDocument()
  )
})
