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
import { Scale } from 'lucide-react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
} from '@/components/drawer-layout'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import type { ApiKeyFormValues } from '../lib'

type ApiKeyAutoPreferenceSectionProps = {
  form: UseFormReturn<ApiKeyFormValues>
}

/**
 * How the "auto" model weighs capability against price for this API key. It only
 * decides which model auto picks; which provider serves that model is the
 * Scheduler's routing preference, a separate section with its own endpoint.
 * Rendered inside the drawer's form, so it shares the token form instance.
 */
export function ApiKeyAutoPreferenceSection(
  props: ApiKeyAutoPreferenceSectionProps
) {
  const { t } = useTranslation()
  const options = [
    {
      value: 'default',
      label: t('Follow platform setting'),
      description: t(
        'Uses the preference the platform administrator has set for keys without one.'
      ),
    },
    {
      value: 'capability',
      label: t('Capability first'),
      description: t(
        'Always picks the best-ranked model for the task, whatever it costs.'
      ),
    },
    {
      value: 'balanced',
      label: t('Balanced'),
      description: t(
        'Gives up a little ranking for a clearly lower price when the models are close.'
      ),
    },
    {
      value: 'cost',
      label: t('Cost first'),
      description: t(
        'Prefers much cheaper models and accepts a weaker one to save money.'
      ),
    },
  ]

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader
        title={t('Auto Model Preference')}
        description={t(
          'Decides which model auto picks for this API key. Which provider serves that model is set under Routing Preference.'
        )}
        icon={<Scale className='size-4' />}
      />

      <FormField
        control={props.form.control}
        name='auto_preference'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Capability vs. price')}</FormLabel>
            <Select
              items={options.map((option) => ({
                value: option.value,
                label: option.label,
              }))}
              value={field.value}
              onValueChange={field.onChange}
            >
              <FormControl>
                <SelectTrigger className='w-full'>
                  <SelectValue />
                </SelectTrigger>
              </FormControl>
              <SelectContent>
                {options.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FormDescription>
              {
                options.find((option) => option.value === field.value)
                  ?.description
              }
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </SideDrawerSection>
  )
}
