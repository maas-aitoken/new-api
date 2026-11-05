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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import type {
  DashboardFilters,
  UsageAnalyticsParams,
  UsageAnalyticsRow,
  UsageMetrics,
} from '@/features/dashboard/types'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ModelMetricsTable } from '../model-metrics-table'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('@/lib/api', () => ({ api: { get } }))

type AnalyticsResponder = (
  url: string,
  params: UsageAnalyticsParams
) => { success: boolean; message?: string; data?: unknown }

const FILTERS: DashboardFilters = {
  start_timestamp: new Date(2025, 6, 1, 0, 0),
  end_timestamp: new Date(2025, 6, 2, 0, 0),
  time_granularity: 'hour',
  username: '',
}

function metrics(overrides: Partial<UsageMetrics>): UsageMetrics {
  return {
    request_count: 0,
    quota: 0,
    token_used: 0,
    input_tokens: 0,
    completion_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    use_time_total: 0,
    use_time_count: 0,
    stream_error_count: 0,
    failed_count: 0,
    tracked_count: 0,
    last_called_at: 0,
    ...overrides,
  }
}

const GPT_ROW: UsageAnalyticsRow = {
  ...metrics({
    request_count: 1200,
    token_used: 5000,
    input_tokens: 1000,
    cache_read_tokens: 250,
    tracked_count: 98,
    stream_error_count: 3,
    failed_count: 2,
    use_time_total: 30,
    use_time_count: 10,
    last_called_at: 1_751_400_000,
  }),
  model_name: 'gpt-4o',
  user_count: 4,
}
const CLAUDE_ROW: UsageAnalyticsRow = {
  ...metrics({ request_count: 200, token_used: 800 }),
  model_name: 'claude-3-5-sonnet',
  user_count: 1,
}

function respondWith(responder: AnalyticsResponder) {
  get.mockImplementation(
    async (url: string, config?: { params?: UsageAnalyticsParams }) => ({
      data: responder(url, config?.params ?? ({} as UsageAnalyticsParams)),
    })
  )
}

function analyticsCalls(groupBy: string) {
  return get.mock.calls.filter(
    ([, config]) =>
      (config as { params?: UsageAnalyticsParams } | undefined)?.params
        ?.group_by === groupBy
  )
}

const queryClients: QueryClient[] = []

function renderTable(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'tester', role })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClients.push(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <ModelMetricsTable filters={FILTERS} />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
  useAuthStore.getState().auth.setUser(null)
  get.mockReset()
  localStorage.clear()
})

const adminResponder: AnalyticsResponder = (_url, params) => {
  if (params.group_by === 'user_model') {
    return {
      success: true,
      data: {
        summary: null,
        rows: [
          {
            ...metrics({ request_count: 900, tracked_count: 900 }),
            model_name: 'gpt-4o',
            user_id: 3,
            username: 'alice',
            display_name: 'Alice',
          },
        ],
      },
    }
  }
  if (params.group_by === 'model_channel') {
    return {
      success: true,
      data: {
        summary: null,
        rows: [
          {
            ...metrics({ request_count: 1200 }),
            model_name: 'gpt-4o',
            channel_id: 7,
            channel_name: 'primary-openai',
          },
        ],
      },
    }
  }
  return { success: true, data: { summary: null, rows: [GPT_ROW, CLAUDE_ROW] } }
}

describe('ModelMetricsTable', () => {
  // Intl rejects i18next's zhCN/zhTW codes without conversion.
  test.each(['zhCN', 'zhTW'])(
    'renders relative last-call times in %s',
    async (language) => {
      const lastCalledAt = Math.floor(Date.now() / 1000) - 2 * 3600
      respondWith(() => ({
        success: true,
        data: {
          summary: null,
          rows: [{ ...GPT_ROW, last_called_at: lastCalledAt }],
        },
      }))
      await i18next.changeLanguage(language)
      try {
        renderTable(ROLE.ADMIN)
        const intlLocale = language === 'zhCN' ? 'zh-CN' : 'zh-TW'
        const expected = new Intl.RelativeTimeFormat(intlLocale, {
          numeric: 'always',
        }).format(-2, 'hour')
        expect(await screen.findByText(expected)).toBeInTheDocument()
      } finally {
        await i18next.changeLanguage('en')
      }
    }
  )

  test('admin table shows aggregated model rows with derived rates', async () => {
    respondWith(adminResponder)
    renderTable(ROLE.ADMIN)

    expect(await screen.findByText('gpt-4o')).toBeInTheDocument()
    expect(screen.getByText('claude-3-5-sonnet')).toBeInTheDocument()
    expect(screen.getByText('25%')).toBeInTheDocument()
    expect(screen.getByText('95%')).toBeInTheDocument()
    expect(analyticsCalls('model')[0]?.[0]).toBe('/api/data/analytics')
  })

  test('a rate lowered only by interrupted streams shows those streams as failures', async () => {
    respondWith(() => ({
      success: true,
      data: {
        summary: null,
        rows: [
          {
            ...metrics({
              request_count: 9,
              tracked_count: 9,
              stream_error_count: 2,
            }),
            model_name: 'gpt-4o',
            user_count: 1,
          },
        ],
      },
    }))
    renderTable(ROLE.ADMIN)

    const rate = await screen.findByText('77.78%')

    expect(rate.closest('td')).toHaveTextContent(/^77\.78%\s*\/\s*2$/)
  })

  test('hovering the success rate shows the succeeded, interrupted and failed breakdown', async () => {
    const user = userEvent.setup()
    respondWith(adminResponder)
    renderTable(ROLE.ADMIN)

    await user.hover(await screen.findByText('95%'))

    expect(
      await screen.findByText('95 succeeded · 3 interrupted · 2 failed')
    ).toBeInTheDocument()
  })

  test('token breakdown columns are hidden by default', async () => {
    respondWith(adminResponder)
    renderTable(ROLE.ADMIN)

    await screen.findByText('gpt-4o')
    expect(
      screen.getByRole('button', { name: 'Cache hit rate' })
    ).toBeInTheDocument()
    for (const hidden of ['Input', 'Output', 'Cache Read', 'Cache Write']) {
      expect(screen.queryByRole('button', { name: hidden })).toBeNull()
    }
  })

  test('admin drill-down loads per-user usage only after expanding a model', async () => {
    const user = userEvent.setup()
    respondWith(adminResponder)
    renderTable(ROLE.ADMIN)

    const toggle = await screen.findByRole('button', {
      name: 'Usage details for gpt-4o',
    })
    expect(analyticsCalls('user_model')).toHaveLength(0)

    await user.click(toggle)

    expect(await screen.findByText('Alice (alice)')).toBeInTheDocument()
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const params = analyticsCalls('user_model')[0]?.[1] as {
      params: UsageAnalyticsParams
    }
    expect(params.params.model).toBe('gpt-4o')
    expect(analyticsCalls('model_channel')).toHaveLength(0)
  })

  test('opening the channels tab loads per-channel usage for the model', async () => {
    const user = userEvent.setup()
    respondWith(adminResponder)
    renderTable(ROLE.ADMIN)

    await user.click(
      await screen.findByRole('button', { name: 'Usage details for gpt-4o' })
    )
    await user.click(await screen.findByRole('tab', { name: 'Channels' }))

    expect(await screen.findByText('primary-openai (#7)')).toBeInTheDocument()
    const params = analyticsCalls('model_channel')[0]?.[1] as {
      params: UsageAnalyticsParams
    }
    expect(params.params.model).toBe('gpt-4o')
  })

  test('regular users query their own usage without drill-down or users column', async () => {
    respondWith(() => ({
      success: true,
      data: { summary: null, rows: [GPT_ROW] },
    }))
    renderTable(ROLE.USER)

    expect(await screen.findByText('gpt-4o')).toBeInTheDocument()
    expect(analyticsCalls('model')[0]?.[0]).toBe('/api/data/analytics/self')
    expect(
      screen.queryByRole('button', { name: 'Usage details for gpt-4o' })
    ).toBeNull()
    expect(screen.queryByRole('button', { name: 'Users' })).toBeNull()
  })

  test('no rows shows the empty message', async () => {
    respondWith(() => ({ success: true, data: { summary: null, rows: [] } }))
    renderTable(ROLE.ADMIN)

    expect(
      await screen.findByText('No model usage data available')
    ).toBeInTheDocument()
  })

  test('a failed response shows an error with retry', async () => {
    respondWith(() => ({ success: false, message: 'range too long' }))
    renderTable(ROLE.ADMIN)

    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})
