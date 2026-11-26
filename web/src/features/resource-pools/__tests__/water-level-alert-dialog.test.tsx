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
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { ResourcePoolAlertConfig } from '../api'
import { WaterLevelAlertButton } from '../components/water-level-alert-dialog'
import {
  waterLevelAlertFormDefaults,
  waterLevelAlertFormSchema,
} from '../lib/water-level-alert-form'

const ALERT_URL = '/api/channel/resource_pools/alert'
const WEBHOOK = 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc-1234'

const clients: QueryClient[] = []

afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

const saved: ResourcePoolAlertConfig = {
  enabled: true,
  webhook_url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=****1234',
  webhook_configured: true,
  mentions: '@all',
  notify_start: '09:00',
  notify_end: '21:00',
  rules: {
    claude: { enabled: true, threshold: 85 },
    gpt: { enabled: false, threshold: 80 },
    gemini: { enabled: false, threshold: 80 },
    grok: { enabled: true, threshold: 90 },
  },
  claude_source_enabled: false,
  alerting: [
    {
      provider: 'claude',
      kind: 'group',
      name: 'aws1',
      level: 91.25,
      since: 1_800_000_000,
    },
    {
      provider: 'claude',
      kind: 'pool',
      name: '',
      level: 88,
      since: 1_800_000_000,
    },
  ],
  utc_offset: '+08:00',
}

const unconfigured: ResourcePoolAlertConfig = {
  ...saved,
  enabled: false,
  webhook_url: '',
  webhook_configured: false,
  mentions: '',
  rules: {
    claude: { enabled: false, threshold: 80 },
    gpt: { enabled: false, threshold: 80 },
    gemini: { enabled: false, threshold: 80 },
    grok: { enabled: false, threshold: 80 },
  },
  alerting: [],
}

function renderButton(config: ResourcePoolAlertConfig | 'fail') {
  const get = vi.spyOn(api, 'get').mockImplementation(async () => {
    if (config === 'fail') throw new Error('network down')
    return { data: { success: true, data: config } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => ({
    data: { success: true, data: { ...saved, ...(body as object) } },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <WaterLevelAlertButton />
    </QueryClientProvider>
  )
  return { get, post }
}

async function openDialog() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: /Water level alerts/ }))
  return { user, dialog: await screen.findByRole('dialog') }
}

it('shows the alerting targets and saves edits without resending the masked webhook', async () => {
  const { post } = renderButton(saved)
  expect(await screen.findByLabelText('Currently alerting')).toHaveTextContent(
    '2'
  )

  const { user, dialog } = await openDialog()
  const row = within(dialog).getByText('aws1').closest('tr') as HTMLElement
  expect(row).toHaveTextContent('Claude')
  expect(row).toHaveTextContent('Group')
  expect(row).toHaveTextContent('91.3%')
  // Claude 整体没有名称，不能显示成"未分组"。
  const overall = within(dialog).getByText('88.0%').closest('tr') as HTMLElement
  expect(overall).toHaveTextContent('Overall')
  expect(overall).not.toHaveTextContent('Ungrouped')
  // Claude 渠道监控默认关闭。
  const claudeChannels = within(dialog).getByRole('switch', {
    name: 'Monitor Claude channels',
  })
  expect(claudeChannels).not.toBeChecked()
  // The stored URL is only shown masked, never put back into the input.
  expect(within(dialog).getByLabelText(/^Webhook URL/)).toHaveValue('')
  expect(
    within(dialog).getByText(/Configured as .*key=\*\*\*\*1234/)
  ).toBeVisible()
  expect(within(dialog).getByText(/UTC\+08:00/)).toBeVisible()

  const threshold = within(dialog).getByLabelText('GPT Threshold (%)')
  await user.clear(threshold)
  await user.type(threshold, '75.5')
  await user.click(within(dialog).getByLabelText('GPT Enabled'))
  await user.click(claudeChannels)
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))

  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(ALERT_URL, {
    enabled: true,
    webhook_url: '',
    mentions: '@all',
    notify_start: '09:00',
    notify_end: '21:00',
    rules: {
      claude: { enabled: true, threshold: 85 },
      gpt: { enabled: true, threshold: 75.5 },
      gemini: { enabled: false, threshold: 80 },
      grok: { enabled: true, threshold: 90 },
    },
    claude_source_enabled: true,
  })
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

it('requires a webhook and a provider before alerts can be enabled', async () => {
  const { post } = renderButton(unconfigured)
  const { user, dialog } = await openDialog()
  await user.click(
    await within(dialog).findByRole('switch', {
      name: 'Enable water level alerts',
    })
  )
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  expect(
    await within(dialog).findByText(
      'Set the webhook URL before enabling alerts'
    )
  ).toBeVisible()
  expect(
    within(dialog).getByText('Enable alerts for at least one provider')
  ).toBeVisible()

  await user.type(
    within(dialog).getByLabelText(/^Webhook URL/),
    'https://example.com/hook?key=abc'
  )
  await user.click(within(dialog).getByLabelText('Grok Enabled'))
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  expect(
    await within(dialog).findByText('Enter a valid WeCom group bot webhook URL')
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()

  await user.clear(within(dialog).getByLabelText(/^Webhook URL/))
  await user.type(within(dialog).getByLabelText(/^Webhook URL/), WEBHOOK)
  await user.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post.mock.calls[0][1]).toMatchObject({
    enabled: true,
    webhook_url: WEBHOOK,
    rules: { grok: { enabled: true, threshold: 80 } },
    claude_source_enabled: false,
  })
})

it('sends a test message to the typed webhook, or to the stored one when blank', async () => {
  const { post } = renderButton(saved)
  const { user, dialog } = await openDialog()
  await within(dialog).findByLabelText(/^Webhook URL/)
  const send = within(dialog).getByRole('button', { name: 'Send test message' })

  await user.click(send)
  await waitFor(() =>
    expect(post).toHaveBeenLastCalledWith(`${ALERT_URL}/test`, {
      webhook_url: '',
      mentions: '@all',
    })
  )

  await user.type(within(dialog).getByLabelText(/^Webhook URL/), WEBHOOK)
  await user.click(send)
  await waitFor(() =>
    expect(post).toHaveBeenLastCalledWith(`${ALERT_URL}/test`, {
      webhook_url: WEBHOOK,
      mentions: '@all',
    })
  )
  // Testing does not save or close the dialog.
  expect(post).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('dialog')).toBeVisible()
})

it('does not let defaults overwrite settings that failed to load', async () => {
  renderButton('fail')
  const { dialog } = await openDialog()
  expect(
    await within(dialog).findByText('Failed to load water level alert settings')
  ).toBeVisible()
  expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(
    within(dialog).getByRole('button', { name: 'Send test message' })
  ).toBeDisabled()
})

it('validates thresholds and notification hours', () => {
  const base = waterLevelAlertFormDefaults(unconfigured)
  const check = (patch: object) =>
    waterLevelAlertFormSchema.safeParse({ ...base, ...patch }).success

  expect(check({})).toBe(true)
  expect(check({ notify_start: '22:00', notify_end: '06:00' })).toBe(true)
  expect(check({ notify_end: '24:00' })).toBe(false)
  expect(check({ notify_start: '9:00' })).toBe(false)
  for (const threshold of ['0', '100.1', '-5', 'abc']) {
    expect(
      check({ rules: { ...base.rules, gpt: { enabled: true, threshold } } })
    ).toBe(false)
  }
  expect(
    check({
      rules: { ...base.rules, gpt: { enabled: true, threshold: '100' } },
    })
  ).toBe(true)
  expect(check({ mentions: 'x'.repeat(513) })).toBe(false)
})
