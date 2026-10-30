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
import { render, screen, within } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RouteProcessDrawer } from '../route-process-drawer'
import type { RouteLog, RouteResult, RouteTraceMode } from '../types'

const logItem = {
  id: 1,
  created_at: 1_760_000_000,
  request_id: 'req-1',
  user_id: 42,
  token_id: 7,
  using_group: 'vip',
  outcome: 'routed' as const,
  judgment: 'live',
  source: 'jev',
  task_type: 'code.build',
  model: 'sonnet',
  effort: 'high',
  preference: 'balanced',
  jev_latency_ms: 120,
}

const routed: RouteLog = {
  ...logItem,
  outcome: 'routed',
  detail: {
    result: {
      resolved_model: 'sonnet',
      model: 'sonnet',
      profile: 'code.build',
      source: 'jev',
      preference: 'balanced',
      effort: 'high',
      effort_in_body: true,
      effort_action: 'set',
      applied_effort: 'high',
      decision: {
        task_type: 'code.build',
        task_type_probability: 0.91,
        difficulty: 3,
        task_change: 0.1,
        larger_model_benefit: 0.2,
      },
      pick: {
        chosen: 'sonnet',
        weight: 0.07,
        band: [
          { model: 'sonnet', score: 0.95, price: 4, discount: 0, utility: 0.95 },
          { model: 'haiku', score: 0.93, price: 1, discount: 0, utility: 0.93 },
        ],
      },
      jev: { endpoint: 'https://jev.example', latency_ms: 120 },
    },
    trace: {
      request: { protocol: 'anthropic', human_turns: 2, text_runes: 80 },
      judgment: 'live',
      trust: { threshold: 0.5, profiles: ['code.build', 'code'] },
      effort: { difficulty: 3, base: 'high', threshold: 0.5, benefit: 0.2 },
      ladder: [
        {
          profile: 'code.build',
          models: 3,
          outcome: 'compared',
          model: 'sonnet',
          skipped: [{ model: 'opus', score: 0.97, reason: 'capability' }],
          skipped_more: 3,
        },
      ],
    },
  },
}

function renderDrawer(props: {
  requestId?: string
  summary?: RouteResult
  onClose?: () => void
}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouteProcessDrawer
        requestId={props.requestId ?? 'req-1'}
        summary={props.summary}
        onClose={props.onClose ?? (() => undefined)}
      />
    </QueryClientProvider>
  )
}

function mockLog(data: RouteLog | null) {
  return vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
}

test('a recorded log shows its steps in order, from the request to the result', async () => {
  mockLog(routed)
  renderDrawer({})

  await screen.findByText('Request')
  const titles = [
    'Request',
    'Classification',
    'Trust check',
    'Model selection',
    'Reasoning effort',
    'Result',
  ]
  titles.slice(1).forEach((title, index) => {
    const previous = screen.getByText(titles[index])
    expect(
      previous.compareDocumentPosition(screen.getByText(title)) &
        Node.DOCUMENT_POSITION_FOLLOWING,
      `${title} comes after ${titles[index]}`
    ).toBeTruthy()
  })
  expect(api.get).toHaveBeenCalledWith('/api/auto-route-logs/req-1')
})

test('the model that was chosen is marked in the comparison, and a better-ranked model says why it was skipped', async () => {
  mockLog(routed)
  renderDrawer({})

  const chosenRow = (await screen.findByText('Chosen')).closest('tr')
  expect(chosenRow).not.toBeNull()
  expect(within(chosenRow as HTMLElement).getByText('sonnet')).toBeVisible()
  expect(screen.queryAllByText('Chosen')).toHaveLength(1)
  expect(
    screen.getByText('Capabilities fall short of the request', { exact: false })
  ).toBeVisible()
  expect(screen.getByText('opus')).toBeVisible()
  expect(screen.getByText('and 3 more')).toBeVisible()
})

test('a request no model could serve shows the failure and the reasons by count', async () => {
  mockLog({
    ...logItem,
    outcome: 'failed',
    judgment: 'unroutable',
    model: '',
    detail: {
      error: 'auto router found no available model for this token and group',
      trace: {
        request: { human_turns: 1, text_runes: 5 },
        judgment: 'unroutable',
        unroutable: {
          reasons: [
            { reason: 'no_channel', count: 7, examples: ['gpt-6', 'opus'] },
            { reason: 'unpriced', count: 1, examples: ['mystery'] },
          ],
        },
      },
    },
  })
  renderDrawer({})

  expect(await screen.findByText('No model available')).toBeVisible()
  expect(
    screen.getByText('auto router found no available model for this token and group')
  ).toBeVisible()
  expect(screen.getByText('No enabled channel serves it')).toBeVisible()
  expect(screen.getByText('× 7')).toBeVisible()
  expect(screen.getByText('gpt-6')).toBeVisible()
})

test('without a recorded log the summary of the usage log is shown, with a notice', async () => {
  mockLog(null)
  renderDrawer({
    summary: {
      resolved_model: 'sonnet-high',
      model: 'sonnet',
      source: 'cache',
      pick: routed.detail?.result?.pick,
    },
  })

  expect(
    await screen.findByText(/No detailed routing log was recorded for this request/)
  ).toBeVisible()
  expect(screen.getByText('sonnet-high')).toBeVisible()
  expect(screen.queryByText('Classification')).not.toBeInTheDocument()
  // With no selection step to hold it, the comparison sits under the result.
  expect(screen.getAllByText('Chosen')).toHaveLength(1)
})

test('without a recorded log or a summary it says nothing was recorded', async () => {
  mockLog(null)
  renderDrawer({})

  expect(
    await screen.findByText('No routing log was recorded for this request')
  ).toBeVisible()
})

test('a failed request for the log offers a retry instead of an empty drawer', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('network down'))
  renderDrawer({})

  expect(await screen.findByText('Failed to load the routing process')).toBeVisible()
  expect(screen.getByText('network down')).toBeVisible()
})

test('a continued request names the previous model and says its effort was held', async () => {
  mockLog({
    ...logItem,
    detail: {
      result: { resolved_model: 'coder', model: 'coder', effort: 'high', source: 'inherit' },
      trace: {
        request: { human_turns: 3, text_runes: 20, previous_runes: 30, truncated: true },
        judgment: 'cache',
        trust: { threshold: 0.5, profiles: ['code.build'] },
        continuation: { previous: 'coder', task_change: 0.2, threshold: 0.5, continued: true },
        effort: { difficulty: 1, base: 'low', threshold: 0.5, floored: true, benefit: 0.1 },
      },
    },
  })
  renderDrawer({})

  expect(await screen.findByText('Continued the previous task')).toBeVisible()
  expect(screen.getByText('Held at the previous effort')).toBeVisible()
  expect(screen.getByText('0.2000 against a threshold of 0.5000')).toBeVisible()
  expect(screen.getByText('30 characters')).toBeVisible()
  expect(screen.getByText('Truncated before classification')).toBeVisible()
  expect(screen.queryByText('Model selection')).not.toBeInTheDocument()
})

test.each([
  [0.9, 'A new task'],
  [0.1, 'The previous model can no longer serve'],
])(
  'a request that left the previous model after a task change of %s says "%s"',
  async (taskChange, verdict) => {
    mockLog({
      ...logItem,
      detail: {
        trace: {
          request: { human_turns: 2, text_runes: 20 },
          judgment: 'live',
          continuation: { previous: 'coder', task_change: taskChange, threshold: 0.5, continued: false },
        },
      },
    })
    renderDrawer({})

    expect(await screen.findByText(verdict)).toBeVisible()
  }
)

test.each([
  ['set', 'high', "Written into the request, replacing the client's own setting."],
  ['strip', '', "The model does not support reasoning, so the client's reasoning settings were removed."],
  ['keep', '', "The client's own reasoning settings were left untouched."],
] as const)(
  'effort written into the body with action %s says what became of the client’s settings',
  async (action, applied, note) => {
    mockLog({
      ...logItem,
      detail: {
        result: { model: 'reasoner', effort: 'high', effort_in_body: true, effort_action: action, applied_effort: applied },
        trace: {
          request: { human_turns: 1, text_runes: 5 },
          judgment: 'live',
          effort: { difficulty: 2, base: 'medium', threshold: 0.5, benefit: 0.7, upgraded: true },
        },
      },
    })
    renderDrawer({})

    expect(await screen.findByText(note)).toBeVisible()
    expect(screen.getByText('Raised one step by the larger-model benefit')).toBeVisible()
    expect(screen.getByText('Effort sent').nextElementSibling).toHaveTextContent(applied || 'None')
  }
)

test.each([
  ['sonnet-high', 'Sent as part of the model name.'],
  ['sonnet', 'No model variant carries this effort, so the provider default applies.'],
])(
  'a model without a reasoning declaration that ran as %s says how the effort reached it',
  async (resolved, note) => {
    mockLog({
      ...logItem,
      detail: {
        result: { model: 'sonnet', resolved_model: resolved, effort: 'high' },
        trace: {
          request: { human_turns: 1, text_runes: 5 },
          judgment: 'live',
          effort: { difficulty: 2, base: 'high', threshold: 0.5, benefit: 0.1 },
        },
      },
    })
    renderDrawer({})

    expect(await screen.findByText(note)).toBeVisible()
    expect(screen.queryByText('Effort sent')).not.toBeInTheDocument()
  }
)

test('an untrusted classification says why and that the previous route is kept', async () => {
  mockLog({
    ...logItem,
    detail: {
      result: {
        resolved_model: 'general',
        model: 'general',
        jev: { endpoint: 'https://jev.example', latency_ms: 5000, error: 'jev failed: timeout' },
      },
      trace: {
        request: { human_turns: 1, text_runes: 9 },
        judgment: 'live_failed',
        trust: { reason: 'no_decision', threshold: 0.5 },
        ladder: [{ profile: 'general', models: 2, outcome: 'first_available', model: 'general' }],
      },
    },
  })
  renderDrawer({})

  expect(await screen.findByText('Classifier failed')).toBeVisible()
  expect(screen.getByText('jev failed: timeout')).toBeVisible()
  expect(screen.getByText('The classifier gave no answer')).toBeVisible()
  expect(
    screen.getByText('An untrusted answer keeps the previous route, or uses the default models.')
  ).toBeVisible()
  expect(screen.getByText('First servable model in list order')).toBeVisible()
})

const tiers = { capability: 0, balanced: 0.07, cost: 0.3 }

// An adaptive request that compared quality and price at the weight its mode gave.
function adaptiveLog(mode: RouteTraceMode): RouteLog {
  return {
    ...logItem,
    model: 'haiku',
    preference: 'adaptive',
    detail: {
      result: {
        resolved_model: 'haiku',
        model: 'haiku',
        preference: 'adaptive',
        pick: {
          chosen: 'haiku',
          weight: 0.1275,
          band: [
            { model: 'sonnet', score: 0.95, price: 4, discount: 0.1275, utility: 0.8225 },
            { model: 'haiku', score: 0.93, price: 1, discount: 0, utility: 0.93 },
          ],
        },
      },
      trace: {
        request: { protocol: 'chat', human_turns: 1, text_runes: 30 },
        judgment: 'live',
        trust: { threshold: 0.5, profiles: ['code.build'] },
        mode,
        ladder: [{ profile: 'code.build', models: 2, outcome: 'compared', model: 'haiku' }],
      },
    },
  }
}

test('an adaptive request shows how its cost weight was judged, between the trust check and the model selection', async () => {
  mockLog(
    adaptiveLog({
      shares: { capability: 0.1, balanced: 0.55, cost: 0.35 },
      position: 1.25,
      weight: 0.1275,
      tiers,
    })
  )
  renderDrawer({})

  expect(await screen.findByText('Judged from the request')).toBeVisible()
  const titles = ['Trust check', 'Cost preference', 'Model selection']
  titles.slice(1).forEach((title, index) => {
    expect(
      screen.getByText(titles[index]).compareDocumentPosition(screen.getByText(title)) &
        Node.DOCUMENT_POSITION_FOLLOWING,
      `${title} comes after ${titles[index]}`
    ).toBeTruthy()
  })
  expect(screen.getByText('Balanced 0.5500')).toBeVisible()
  expect(screen.getByText('Cost first 0.3500')).toBeVisible()
  expect(screen.getByText('Cost first 0.3000')).toBeVisible()
  expect(screen.getByText('Expected position').nextElementSibling).toHaveTextContent('1.2500')
  expect(screen.getByText('Cost weight').nextElementSibling).toHaveTextContent('0.1275')
  expect(screen.getByText('Preference').nextElementSibling).toHaveTextContent('Decide per request')
})

test('an adaptive request with no usable judgment says the balanced weight applied', async () => {
  mockLog(adaptiveLog({ reason: 'no_answer', position: 1, weight: 0.07, tiers }))
  renderDrawer({})

  expect(await screen.findByText('No usable judgment')).toBeVisible()
  expect(
    screen.getByText('The classifier gave no usable answer on cost, so the balanced weight applies.')
  ).toBeVisible()
  expect(screen.getByText('Cost weight').nextElementSibling).toHaveTextContent('0.0700')
  expect(screen.queryByText('Expected position')).not.toBeInTheDocument()
  expect(screen.queryByText('Probability of each preference')).not.toBeInTheDocument()
})

test('with no request selected the drawer stays closed and asks for nothing', () => {
  const get = mockLog(routed)
  renderDrawer({ requestId: '' })

  expect(screen.queryByText('Routing process')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})
