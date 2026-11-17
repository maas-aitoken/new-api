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
import { CapacitySection } from '../capacity-section'
import type { AutoRouterSettings } from '../types'

function Fixture(props: { settings: AutoRouterSettings }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <CapacitySection settings={props.settings} />
      </SettingsPageProvider>
    </>
  )
}

function renderSection(capacityPricing = '{}') {
  const settings = {
    'autorouter_setting.capacity_pricing': capacityPricing,
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

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('the stored curve fills the fields and a disabled switch says the curve is kept', () => {
  renderSection(
    JSON.stringify({ enabled: false, knee: 0.6, saturated_multiplier: 50 })
  )

  expect(
    screen.getByRole('spinbutton', { name: 'No-premium knee' })
  ).toHaveValue(0.6)
  expect(
    screen.getByRole('spinbutton', { name: 'Saturated premium' })
  ).toHaveValue(50)
  // 没写的参数按内置值，开关保持存进去的状态。
  expect(screen.getByRole('spinbutton', { name: 'Premium cap' })).toHaveValue(
    20
  )
  expect(
    screen.getByRole('switch', {
      name: 'Charge more for models whose capacity is running out',
    })
  ).not.toBeChecked()
  expect(
    screen.getByText(
      'The curve below is kept but not applied while the switch is off.'
    )
  ).toBeVisible()
})

test('editing the knee writes one option with the whole curve serialized', async () => {
  const user = userEvent.setup()
  renderSection()

  const knee = screen.getByRole('spinbutton', { name: 'No-premium knee' })
  // jsdom 的数字输入不支持选区，用 change 事件代替逐键输入。
  fireEvent.change(knee, { target: { value: '0.7' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const [path, body] = vi.mocked(api.put).mock.calls[0] as unknown as [
    string,
    { key: string; value: string },
  ]
  expect(path).toBe('/api/option/')
  expect(body.key).toBe('autorouter_setting.capacity_pricing')
  expect(JSON.parse(body.value as string)).toEqual({
    enabled: true,
    knee: 0.7,
    gamma: 1,
    max_multiplier: 20,
    saturated_multiplier: 100,
    cache_seconds: 5,
    smoothing_seconds: 30,
  })
})

test('a saturated premium below the cap is rejected before anything is saved', async () => {
  const user = userEvent.setup()
  renderSection()

  const saturated = screen.getByRole('spinbutton', {
    name: 'Saturated premium',
  })
  fireEvent.change(saturated, { target: { value: '10' } })
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  expect(
    await screen.findByText(
      'The saturated premium must be at least the premium cap'
    )
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

test('saving with nothing changed writes nothing', async () => {
  const user = userEvent.setup()
  renderSection()

  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  )
  expect(api.put).not.toHaveBeenCalled()
})
