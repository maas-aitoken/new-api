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
import { useQuery } from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useTranslation } from 'react-i18next'

import { Combobox } from '@/components/ui/combobox'
import { getUsageOrgOptions } from '@/features/dashboard/api'
import {
  ORG_LEVELS,
  orgFilterChoices,
  orgLevelLabel,
  setOrgFilterLevel,
} from '@/features/dashboard/lib'
import type {
  UsageOrgFilter as UsageOrgFilterValue,
  UsageOrgLevel,
  UsageOrgOption,
} from '@/features/dashboard/types'
import { requireServerSuccess } from '@/lib/server-error-message'

const NO_OPTIONS: UsageOrgOption[] = []

function allLevelLabel(level: UsageOrgLevel, t: TFunction): string {
  switch (level) {
    case 'business_unit':
      return t('All business units')
    case 'department':
      return t('All departments')
    case 'team':
      return t('All teams')
  }
}

interface UsageOrgFilterProps {
  value: UsageOrgFilterValue
  onChange: (value: UsageOrgFilterValue) => void
}

export function UsageOrgFilter(props: UsageOrgFilterProps) {
  const { t } = useTranslation()
  const { data } = useQuery({
    queryKey: ['dashboard', 'usage-org-options'],
    queryFn: async () => requireServerSuccess(await getUsageOrgOptions()),
    select: (res) => res.data ?? NO_OPTIONS,
    staleTime: 5 * 60_000,
  })
  const options = data ?? NO_OPTIONS

  return (
    <div className='flex flex-wrap items-center gap-1.5 sm:gap-2'>
      {ORG_LEVELS.map((level) => {
        const allLabel = allLevelLabel(level, t)
        return (
          <Combobox
            key={level}
            aria-label={orgLevelLabel(level, t)}
            className='w-40 sm:w-44'
            options={[
              { value: '', label: allLabel },
              ...orgFilterChoices(options, props.value, level, t),
            ]}
            value={props.value[level] ?? ''}
            placeholder={allLabel}
            onValueChange={(value) =>
              props.onChange(setOrgFilterLevel(props.value, level, value ?? ''))
            }
          />
        )
      })}
    </div>
  )
}
