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

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormLabel,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { NumberSettingField } from './fields'
import { useSaveAutoRouterOptions } from './hooks'
import {
  changedOptions,
  integerBetween,
  parseCapacityPricing,
  serializeCapacityPricing,
} from './settings'
import type { AutoRouterSettings } from './types'

function createSchema(t: TFunction) {
  const range = (min: number, max: number) =>
    z
      .number()
      .min(min, t('Must be a number between {{min}} and {{max}}', { min, max }))
      .max(max, t('Must be a number between {{min}} and {{max}}', { min, max }))
  return z
    .object({
      enabled: z.boolean(),
      knee: range(0, 0.95),
      gamma: range(0.1, 8),
      max_multiplier: range(1, 10000),
      saturated_multiplier: range(1, 100000),
      cache_seconds: integerBetween(t, 0, 300),
      smoothing_seconds: integerBetween(t, 0, 3600),
    })
    .refine((values) => values.saturated_multiplier >= values.max_multiplier, {
      message: t('The saturated premium must be at least the premium cap'),
      path: ['saturated_multiplier'],
    })
}

type CapacityValues = z.infer<ReturnType<typeof createSchema>>

type CapacitySectionProps = {
  settings: AutoRouterSettings
}

/** How the price of a model reacts to the capacity left in the pool it would land on. */
export function CapacitySection(props: CapacitySectionProps) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<CapacityValues>(
    () =>
      parseCapacityPricing(
        props.settings['autorouter_setting.capacity_pricing']
      ),
    [props.settings]
  )
  const form = useForm<CapacityValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const onSubmit = async (values: CapacityValues) => {
    const updates = changedOptions(
      { capacity_pricing: serializeCapacityPricing(defaults) },
      { capacity_pricing: serializeCapacityPricing(values) }
    )
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    await save(updates)
  }

  const enabled = form.watch('enabled')

  return (
    <SettingsSection title={t('Capacity premium')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={saving}
          />

          <div data-settings-form-span='full' className='space-y-1'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Pressure is the highest of request, token and in-flight usage divided by their limits. Pools without a limit have no pressure and never get a premium. The premium only changes which model auto picks, never what the user is billed.'
              )}
            </p>
          </div>

          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Charge more for models whose capacity is running out')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Uses the dynamic limit of a bound capacity pool when one is published, otherwise the channel model limits. Turn this off to compare price and cost multiplier only.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <SettingsFormGrid>
            <NumberSettingField
              control={form.control}
              name='knee'
              label={t('No-premium knee')}
              description={t(
                'Pressure at or below this costs nothing extra. Built-in value: 0.5.'
              )}
              min={0}
              max={0.95}
              step={0.05}
            />
            <NumberSettingField
              control={form.control}
              name='gamma'
              label={t('Curve exponent')}
              description={t(
                'How fast the premium rises past the knee. Built-in value: 1.'
              )}
              min={0.1}
              max={8}
              step={0.1}
            />
            <NumberSettingField
              control={form.control}
              name='max_multiplier'
              label={t('Premium cap')}
              description={t(
                'The most the curve can multiply the compared cost by while the pool still has headroom. Built-in value: 20.'
              )}
              min={1}
              max={10000}
              step={1}
            />
            <NumberSettingField
              control={form.control}
              name='saturated_multiplier'
              label={t('Saturated premium')}
              description={t(
                'Applied when the pool is full or exhausted, so a busy model loses inside the candidate band. Built-in value: 100.'
              )}
              min={1}
              max={100000}
              step={1}
            />
            <NumberSettingField
              control={form.control}
              name='cache_seconds'
              label={t('Pool snapshot cache (seconds)')}
              description={t(
                'How long a node reuses one capacity reading instead of asking again for every request. Built-in value: 5.'
              )}
              min={0}
              max={300}
              step={1}
            />
            <NumberSettingField
              control={form.control}
              name='smoothing_seconds'
              label={t('Pressure smoothing (seconds)')}
              description={t(
                'Time constant of the exponential smoothing that keeps traffic from oscillating between models. 0 reacts immediately. Built-in value: 30.'
              )}
              min={0}
              max={3600}
              step={5}
            />
          </SettingsFormGrid>

          {!enabled && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'The curve below is kept but not applied while the switch is off.'
              )}
            </p>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
