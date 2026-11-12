/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

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
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  getPublicPlans,
  getSelfSubscriptionFull,
} from '@/features/subscriptions/api'

import { SubscriptionPlansCard } from '../components/subscription-plans-card'

vi.mock('@/features/subscriptions/api', () => ({
  getPublicPlans: vi.fn(),
  getSelfSubscriptionFull: vi.fn(),
  updateBillingPreference: vi.fn(),
}))

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

beforeEach(() => {
  vi.mocked(getPublicPlans).mockResolvedValue({
    success: true,
    data: [
      {
        plan: {
          id: 1,
          title: 'Starter',
          price_amount: 4,
          currency: 'USD',
          duration_unit: 'month',
          duration_value: 1,
          quota_reset_period: 'monthly',
          enabled: true,
          sort_order: 1,
          allow_balance_pay: true,
          allow_wallet_overflow: true,
          max_purchase_per_user: 0,
          total_amount: 4_000_000,
        },
      },
    ],
  })
  vi.mocked(getSelfSubscriptionFull).mockResolvedValue({
    success: true,
    data: {
      billing_preference: 'subscription_first',
      subscriptions: [
        {
          subscription: {
            id: 7,
            user_id: 1,
            plan_id: 1,
            status: 'active',
            start_time: 1,
            end_time: 4_102_444_800,
            amount_total: 4_000_000,
            amount_used: 5_140_000,
          },
        },
      ],
      all_subscriptions: [
        {
          subscription: {
            id: 7,
            user_id: 1,
            plan_id: 1,
            status: 'active',
            start_time: 1,
            end_time: 4_102_444_800,
            amount_total: 4_000_000,
            amount_used: 5_140_000,
          },
        },
      ],
    },
  })
})

describe('SubscriptionPlansCard', () => {
  it('marks an over-quota subscription red in both the used amount and progress bar', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SubscriptionPlansCard topupInfo={null} />
        </I18nextProvider>
      </QueryClientProvider>
    )

    await waitFor(() => expect(screen.getByText(/Used 129%/)).toBeVisible())

    expect(screen.getByText(/Used 129%/)).toHaveClass('text-destructive')
    expect(document.querySelector('span.text-destructive')).toBeVisible()
    const progress = document.querySelector('[data-slot="progress"]')
    expect(progress?.className).toContain(
      '[&_[data-slot=progress-indicator]]:bg-destructive'
    )
  })
})
