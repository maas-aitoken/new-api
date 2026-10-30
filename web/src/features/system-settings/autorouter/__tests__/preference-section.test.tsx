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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { PreferenceSection } from '../preference-section'
import type { AutoRouterSettings } from '../types'

function Fixture(props: { settings: AutoRouterSettings }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <PreferenceSection settings={props.settings} />
      </SettingsPageProvider>
    </>
  )
}

function renderSection(defaultPreference = 'balanced') {
  const settings = {
    'autorouter_setting.default_preference': defaultPreference,
    'autorouter_setting.cost_weights': '{}',
  } as AutoRouterSettings
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture settings={settings} />
    </QueryClientProvider>
  )
}

const defaultPreferenceSelect = () =>
  screen.getByRole('combobox', { name: 'Default preference for keys without one' })

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('choosing to decide per request writes only the default preference', async () => {
  const user = userEvent.setup()
  renderSection()

  await user.click(defaultPreferenceSelect())
  await user.click(await screen.findByRole('option', { name: 'Decide per request' }))
  expect(
    screen.getByText(
      'Judges each request on whether it stresses result quality or cost, then reads the cost weight for that request from the three weights below.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.default_preference',
    value: 'adaptive',
  })
})

test('a stored adaptive default is shown as chosen', () => {
  renderSection('adaptive')

  expect(defaultPreferenceSelect()).toHaveTextContent('Decide per request')
})

test.each(['balanced', 'adaptive'])(
  'with %s as the default the cost weights are still edited for exactly the three weighted preferences',
  (defaultPreference) => {
    renderSection(defaultPreference)

    const fields = screen.getAllByRole('spinbutton')
    expect(fields).toHaveLength(3)
    ;['Capability first', 'Balanced', 'Cost first'].forEach((name) => {
      expect(screen.getByRole('spinbutton', { name })).toBeVisible()
    })
  }
)

test('saving with nothing changed writes nothing', async () => {
  const user = userEvent.setup()
  renderSection('adaptive')

  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).not.toHaveBeenCalled()
})
