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
import { Download, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { getUsageAnalytics } from '@/features/dashboard/api'
import {
  getDashboardDurationMinutes,
  getDefaultDays,
  orgFilterParams,
} from '@/features/dashboard/lib'
import {
  buildUsageSheets,
  buildUsageWorkbook,
  USAGE_EXPORT_GROUPINGS,
  usageExportFileName,
  type UsageExportDatasets,
  type UsageExportGroupBy,
} from '@/features/dashboard/lib/usage-export'
import type {
  DashboardFilters,
  UsageOrgFilter,
} from '@/features/dashboard/types'
import { computeTimeRange } from '@/lib/time'

const XLSX_MIME_TYPE =
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'

interface UsageExportButtonProps {
  filters: DashboardFilters
  org?: UsageOrgFilter
  groupings?: readonly UsageExportGroupBy[]
}

export function UsageExportButton(props: UsageExportButtonProps) {
  const { t } = useTranslation()
  const [exporting, setExporting] = useState(false)

  const handleExport = async () => {
    setExporting(true)
    try {
      const groupings = props.groupings ?? USAGE_EXPORT_GROUPINGS
      const timeRange = computeTimeRange(
        getDefaultDays(props.filters.time_granularity),
        props.filters.start_timestamp,
        props.filters.end_timestamp
      )
      const entries = await Promise.all(
        groupings.map(async (groupBy) => {
          const response = await getUsageAnalytics(
            {
              ...timeRange,
              ...orgFilterParams(props.org ?? {}),
              group_by: groupBy,
              username: props.filters.username || undefined,
            },
            true
          )
          if (!response.success) throw new Error(response.message)
          return [groupBy, response.data?.rows ?? []] as const
        })
      )
      const datasets = Object.fromEntries(entries) as UsageExportDatasets

      const bytes = await buildUsageWorkbook(
        buildUsageSheets(
          datasets,
          getDashboardDurationMinutes(props.filters),
          groupings
        )
      )
      const url = URL.createObjectURL(
        new Blob([bytes], { type: XLSX_MIME_TYPE })
      )
      const a = document.createElement('a')
      a.href = url
      a.download = usageExportFileName(
        timeRange.start_timestamp,
        timeRange.end_timestamp
      )
      a.click()
      URL.revokeObjectURL(url)
    } catch {
      toast.error(t('Export failed'))
    } finally {
      setExporting(false)
    }
  }

  return (
    <Button
      variant='outline'
      size='sm'
      onClick={() => void handleExport()}
      disabled={exporting}
      aria-busy={exporting}
    >
      {exporting ? (
        <Loader2 className='mr-2 h-4 w-4 animate-spin' aria-hidden='true' />
      ) : (
        <Download className='mr-2 h-4 w-4' aria-hidden='true' />
      )}
      {exporting ? t('Exporting...') : t('Export')}
    </Button>
  )
}
