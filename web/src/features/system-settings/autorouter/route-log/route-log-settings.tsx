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
import { zodResolver } from '@hookform/resolvers/zod'
import type { TFunction } from 'i18next'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Form } from '@/components/ui/form'

import {
  SettingsForm,
  SettingsFormGrid,
} from '../../components/settings-form-layout'
import { SettingsPageFormActions } from '../../components/settings-page-context'
import { useResetForm } from '../../hooks/use-reset-form'
import { NumberSettingField, SelectSettingField } from '../fields'
import { useSaveAutoRouterOptions } from '../hooks'
import { changedOptions, integerBetween } from '../settings'
import type { AutoRouterSettings } from '../types'

// Mirrors the range the server accepts for route_log_retention_days.
const MAX_RETENTION_DAYS = 365

function createSchema(t: TFunction) {
  return z.object({
    route_log_mode: z.enum(['off', 'decisions', 'all']),
    route_log_retention_days: integerBetween(t, 1, MAX_RETENTION_DAYS),
  })
}

type RouteLogSettingsValues = z.infer<ReturnType<typeof createSchema>>

/** Which requests leave a routing log, and for how long the logs are kept. */
export function RouteLogSettings(props: { settings: AutoRouterSettings }) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<RouteLogSettingsValues>(
    () => ({
      route_log_mode: props.settings['autorouter_setting.route_log_mode'],
      route_log_retention_days:
        props.settings['autorouter_setting.route_log_retention_days'],
    }),
    [props.settings]
  )
  const form = useForm<RouteLogSettingsValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const onSubmit = async (values: RouteLogSettingsValues) => {
    const updates = changedOptions(defaults, values)
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    await save(updates)
  }

  return (
    <Form {...form}>
      <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
        <SettingsPageFormActions
          onSave={form.handleSubmit(onSubmit)}
          isSaving={saving}
        />
        <SettingsFormGrid>
          <SelectSettingField
            control={form.control}
            name='route_log_mode'
            label={t('Requests to log')}
            description={t(
              'Decisions only logs requests that called the classifier or found no model. Cached, background and sub-agent requests repeat earlier decisions and are logged only with All requests.'
            )}
            options={[
              { value: 'off', label: t('Off') },
              { value: 'decisions', label: t('Decisions only') },
              { value: 'all', label: t('All requests') },
            ]}
          />
          <NumberSettingField
            control={form.control}
            name='route_log_retention_days'
            label={t('Routing log retention (days)')}
            description={t(
              'Older logs are deleted hourly. With a ClickHouse log database they expire through the table TTL instead.'
            )}
            min={1}
            max={MAX_RETENTION_DAYS}
            step={1}
          />
        </SettingsFormGrid>
      </SettingsForm>
    </Form>
  )
}
