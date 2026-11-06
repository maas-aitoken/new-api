import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import type { IselfUsageRow } from '@/features/dashboard/types'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { IselfUsageTable } from '../iself-usage-table'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { get } }))

const clients: QueryClient[] = []
const params = { start_timestamp: 1000, end_timestamp: 2000 }
const row: IselfUsageRow = {
  email: 'alice@example.com',
  source: 'iself',
  model_name: 'model-a',
  request_count: 2,
  quota: 150,
  input_tokens: 30,
  completion_tokens: 15,
  cache_read_tokens: 12,
  cache_write_tokens: 6,
  token_used: 45,
  last_called_at: 1900,
}

function renderTable() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      <TooltipProvider>
        <IselfUsageTable params={params} />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  localStorage.clear()
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'alice', role: ROLE.USER })
})

afterEach(() => {
  for (const client of clients.splice(0)) client.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

test('ordinary users load their iSelf endpoint and see email, source and exact model totals', async () => {
  get.mockResolvedValue({ data: { success: true, data: [row] } })
  renderTable()
  const region = screen.getByRole('region', { name: 'iSelf data' })
  expect(
    await within(region).findByText('alice@example.com')
  ).toBeInTheDocument()
  expect(within(region).getByText('iSelf')).toBeInTheDocument()
  expect(within(region).getByText('model-a')).toBeInTheDocument()
  expect(within(region).getByText('45')).toBeInTheDocument()
  expect(get).toHaveBeenCalledWith('/api/data/iself/self', { params })
})

test('administrators load the all-email endpoint', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  get.mockResolvedValue({
    data: { success: true, data: [row, { ...row, email: 'bob@example.com' }] },
  })
  renderTable()
  expect(await screen.findByText('bob@example.com')).toBeInTheDocument()
  expect(screen.getByText('alice@example.com')).toBeInTheDocument()
  expect(get).toHaveBeenCalledWith('/api/data/iself', { params })
})

test('users without iSelf traffic get a visible empty table with its headers', async () => {
  get.mockResolvedValue({ data: { success: true, data: [] } })
  renderTable()
  expect(
    await screen.findByText('No iSelf usage in this time range')
  ).toBeInTheDocument()
  expect(
    screen.getByRole('columnheader', { name: 'Email' })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('columnheader', { name: 'Model' })
  ).toBeInTheDocument()
})

test('pending requests show skeletons and failed requests show an error instead of empty usage', async () => {
  let reject: (error: Error) => void = () => undefined
  get.mockReturnValue(
    new Promise((_resolve, rejectPromise) => {
      reject = rejectPromise
    })
  )
  const view = renderTable()
  expect(
    view.container.querySelector('[data-slot="skeleton"]')
  ).toBeInTheDocument()
  expect(screen.queryByText('No iSelf usage in this time range')).toBeNull()
  reject(new Error('offline'))
  expect(await screen.findByText('Failed to load')).toBeInTheDocument()
})

test('the cache hit rate column shows cache reads over input by default', async () => {
  get.mockResolvedValue({ data: { success: true, data: [row] } })
  renderTable()
  await screen.findByText('alice@example.com')

  expect(
    screen.getByRole('columnheader', { name: 'Cache hit rate' })
  ).toBeInTheDocument()
  expect(screen.getByText('40%')).toBeInTheDocument()
})

test('cache read and write columns stay hidden until enabled from the View menu', async () => {
  get.mockResolvedValue({ data: { success: true, data: [row] } })
  const user = userEvent.setup()
  renderTable()
  await screen.findByText('alice@example.com')
  expect(screen.queryByRole('columnheader', { name: 'Cache Read' })).toBeNull()
  expect(screen.queryByRole('columnheader', { name: 'Cache Write' })).toBeNull()

  await user.click(screen.getByRole('button', { name: 'View' }))
  await user.click(
    await screen.findByRole('menuitemcheckbox', { name: 'Cache Read' })
  )
  await user.click(
    await screen.findByRole('menuitemcheckbox', { name: 'Cache Write' })
  )

  expect(
    screen.getByRole('columnheader', { name: 'Cache Read' })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('columnheader', { name: 'Cache Write' })
  ).toBeInTheDocument()
  expect(screen.getByText('12')).toBeInTheDocument()
  expect(screen.getByText('6')).toBeInTheDocument()
})

test('sorting the email column orders the recorded emails', async () => {
  get.mockResolvedValue({
    data: {
      success: true,
      data: [
        { ...row, email: 'zoe@example.com', quota: 200 },
        { ...row, email: 'alice@example.com', quota: 100 },
      ],
    },
  })
  const user = userEvent.setup()
  renderTable()
  await screen.findByText('zoe@example.com')
  await user.click(
    within(screen.getByRole('columnheader', { name: 'Email' })).getByRole(
      'button'
    )
  )
  await user.click(await screen.findByRole('menuitem', { name: 'Asc' }))
  const rows = screen.getAllByRole('row')
  expect(rows[1]).toHaveTextContent('alice@example.com')
})
