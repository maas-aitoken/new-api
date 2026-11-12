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
import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import z from 'zod'

import { SectionPageLayout } from '@/components/layout'
import { PricingContent } from '@/features/pricing'

export const Route = createFileRoute('/_authenticated/model-square/')({
  validateSearch: z.object({
    search: z.string().optional(),
    sort: z.string().optional(),
    vendor: z.string().optional(),
    group: z.string().optional(),
    quotaType: z.string().optional(),
    endpointType: z.string().optional(),
    tag: z.string().optional(),
    tokenUnit: z.enum(['M', 'K']).optional(),
    view: z.enum(['card', 'table']).optional().catch(undefined),
    rechargePrice: z.boolean().optional(),
    contextLength: z.string().optional(),
    parameterCount: z.string().optional(),
    releaseDate: z.string().optional(),
  }),
  component: ModelSquarePage,
})

function ModelSquarePage() {
  const { t } = useTranslation()
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Square')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <PricingContent embedded modelSquare />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
