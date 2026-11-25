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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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
    'autorouter_setting.preference_weights': '{}',
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
  screen.getByRole('combobox', {
    name: 'Default preference for keys without one',
  })

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('choosing another preference writes only the default preference', async () => {
  const user = userEvent.setup()
  renderSection()

  await user.click(defaultPreferenceSelect())
  await user.click(await screen.findByRole('option', { name: 'Cost first' }))
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.default_preference',
    value: 'cost',
  })
})

test('deciding per request is no longer offered as the default preference', async () => {
  const user = userEvent.setup()
  renderSection()

  await user.click(defaultPreferenceSelect())
  expect(await screen.findAllByRole('option')).toHaveLength(3)
  expect(
    screen.queryByRole('option', { name: 'Decide per request' })
  ).not.toBeInTheDocument()
})

test('a stored adaptive default is shown as balanced, the preference the server applies', () => {
  renderSection('adaptive')

  expect(defaultPreferenceSelect()).toHaveTextContent('Balanced')
})

test.each(['balanced', 'cost'])(
  'with %s as the default the four weights are edited for exactly the three weighted preferences',
  async (defaultPreference) => {
    const user = userEvent.setup()
    renderSection(defaultPreference)

    expect(screen.getAllByRole('spinbutton')).toHaveLength(4)
    await user.click(screen.getByRole('tab', { name: 'Capability first' }))
    expect(screen.getAllByRole('spinbutton')).toHaveLength(4)
    expect(screen.getByRole('spinbutton', { name: 'Cost weight' })).toHaveValue(
      0
    )
    await user.click(screen.getByRole('tab', { name: 'Cost first' }))
    expect(screen.getAllByRole('spinbutton')).toHaveLength(4)
    expect(screen.getByRole('spinbutton', { name: 'Cost weight' })).toHaveValue(
      0.4
    )
  }
)

test('a stored preference policy fills its four fields', async () => {
  const user = userEvent.setup()
  const settings = {
    'autorouter_setting.default_preference': 'balanced',
    'autorouter_setting.preference_weights':
      '{"balanced":{"quality":0.4,"cost":0.1,"capacity":0.2,"success":0.3,"temperature":0}}',
  } as AutoRouterSettings
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Fixture settings={settings} />
    </QueryClientProvider>
  )

  expect(
    screen.getByRole('spinbutton', { name: 'Capability weight' })
  ).toHaveValue(0.4)
  expect(screen.getByRole('spinbutton', { name: 'Cost weight' })).toHaveValue(
    0.1
  )
  expect(
    screen.getByRole('spinbutton', { name: 'Capacity weight' })
  ).toHaveValue(0.2)
  expect(
    screen.getByRole('spinbutton', { name: 'Success weight' })
  ).toHaveValue(0.3)
  await user.click(screen.getByRole('tab', { name: 'Cost first' }))
  expect(screen.getByRole('spinbutton', { name: 'Cost weight' })).toHaveValue(
    0.4
  )
})

test('editing one preference writes the complete policy for all preferences', async () => {
  const user = userEvent.setup()
  renderSection()

  fireEvent.change(
    screen.getByRole('spinbutton', { name: 'Capability weight' }),
    {
      target: { value: '0.8' },
    }
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.preference_weights',
    value:
      '{"capability":{"quality":0.6,"cost":0,"capacity":0.2,"success":0.2},"balanced":{"quality":0.8,"cost":0.2,"capacity":0.2,"success":0.2},"cost":{"quality":0.2,"cost":0.4,"capacity":0.2,"success":0.2}}',
  })
})

test('each weight exposes its explanation from the question mark button', async () => {
  const user = userEvent.setup()
  renderSection()

  await user.click(screen.getAllByRole('button', { name: 'Learn more' })[0])

  expect(
    screen.getByText('The weight given to model capability and answer quality.')
  ).toBeVisible()
})

test('a weight above one is rejected before anything is saved', async () => {
  const user = userEvent.setup()
  renderSection()

  fireEvent.change(screen.getByRole('spinbutton', { name: 'Success weight' }), {
    target: { value: '1.1' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  expect(
    await screen.findByText('Must be a number between 0 and 1')
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

test('saving with nothing changed writes nothing', async () => {
  const user = userEvent.setup()
  renderSection('cost')

  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).not.toHaveBeenCalled()
})
