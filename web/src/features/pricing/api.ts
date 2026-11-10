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
import { api } from '@/lib/api'

import type { ModelSquareData, PricingData, PricingModel } from './types'

// ----------------------------------------------------------------------------
// Pricing APIs
// ----------------------------------------------------------------------------

// Get model pricing data
export async function getPricing(): Promise<PricingData> {
  const res = await api.get('/api/pricing')
  return res.data
}

export async function getModelSquare(params?: {
  offset?: number
  limit?: number
}): Promise<ModelSquareData> {
  const res = await api.get('/api/model-square', { params })
  return res.data
}

export async function getAllModelSquare(): Promise<ModelSquareData> {
  const pageSize = 100
  const firstPage = await getModelSquare({ offset: 0, limit: pageSize })
  if (!firstPage.success) {
    throw new Error(firstPage.message || 'Failed to load model square')
  }
  const total = firstPage.data?.total ?? firstPage.data?.items?.length ?? 0
  const pageCount = Math.ceil(total / pageSize)
  if (pageCount <= 1) return firstPage

  const pages = await Promise.all(
    Array.from({ length: pageCount - 1 }, (_, index) =>
      getModelSquare({ offset: (index + 1) * pageSize, limit: pageSize })
    )
  )
  const failedPage = pages.find((page) => !page.success)
  if (failedPage) {
    throw new Error(failedPage.message || 'Failed to load model square')
  }
  return {
    ...firstPage,
    data: {
      items: [
        ...(firstPage.data?.items ?? []),
        ...pages.flatMap((page) => page.data?.items ?? []),
      ],
      total,
      offset: 0,
      limit: pageSize,
    },
  }
}

export async function getModelSquareDetail(
  modelId: string
): Promise<{ success: boolean; data: PricingModel }> {
  const res = await api.get(`/api/model-square/${encodeURIComponent(modelId)}`)
  return res.data
}
