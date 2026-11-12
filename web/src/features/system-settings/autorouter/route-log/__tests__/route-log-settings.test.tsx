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

import { SettingsPageProvider } from '../../../components/settings-page-context'
import type { AutoRouterSettings } from '../../types'
import { RouteLogSettings } from '../route-log-settings'

const settings = {
  'autorouter_setting.route_log_mode': 'decisions',
  'autorouter_setting.route_log_retention_days': 7,
} as AutoRouterSettings

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <RouteLogSettings settings={settings} />
      </SettingsPageProvider>
    </>
  )
}

function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('changing the mode writes only the mode', async () => {
  const user = userEvent.setup()
  renderSettings()

  await user.click(screen.getByRole('combobox', { name: 'Requests to log' }))
  await user.click(await screen.findByRole('option', { name: 'All requests' }))
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.route_log_mode',
    value: 'all',
  })
})

test('changing the retention writes only the retention, as a number', async () => {
  const user = userEvent.setup()
  renderSettings()

  fireEvent.change(screen.getByLabelText('Routing log retention (days)'), {
    target: { value: '30' },
  })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'autorouter_setting.route_log_retention_days',
    value: 30,
  })
})

test.each(['0', '366', '1.5'])(
  'a retention of %s days is rejected on the field and nothing is saved',
  async (days) => {
    const user = userEvent.setup()
    renderSettings()

    const retention = screen.getByLabelText('Routing log retention (days)')
    fireEvent.change(retention, { target: { value: days } })
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(retention).toHaveAttribute('aria-invalid', 'true'))
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('saving with nothing changed writes nothing', async () => {
  const user = userEvent.setup()
  renderSettings()

  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).not.toHaveBeenCalled()
})
