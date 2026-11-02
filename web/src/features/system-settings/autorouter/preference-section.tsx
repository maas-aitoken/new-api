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
import { SELECTABLE_AUTO_PREFERENCES } from '@/features/keys/lib'

import {
  SettingsForm,
  SettingsFormGrid,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { NumberSettingField, SelectSettingField } from './fields'
import { useSaveAutoRouterOptions } from './hooks'
import {
  DEFAULT_COST_WEIGHTS,
  changedOptions,
  fractionBetween,
  parseCostWeights,
  serializeCostWeights,
  type AutoPreference,
} from './settings'
import type { AutoRouterSettings } from './types'

function createSchema(t: TFunction) {
  return z.object({
    default_preference: z.enum(SELECTABLE_AUTO_PREFERENCES),
    weights: z.object({
      capability: fractionBetween(t),
      balanced: fractionBetween(t),
      cost: fractionBetween(t),
    }),
  })
}

type PreferenceValues = z.infer<ReturnType<typeof createSchema>>

type PreferenceSectionProps = {
  settings: AutoRouterSettings
}

/** What the router does when a key sets no preference, and how much each preference trades ranking for price. */
export function PreferenceSection(props: PreferenceSectionProps) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<PreferenceValues>(() => {
    const stored = props.settings['autorouter_setting.default_preference']
    return {
      default_preference:
        SELECTABLE_AUTO_PREFERENCES.find(
          (preference) => preference === stored
        ) ?? 'balanced',
      weights: parseCostWeights(
        props.settings['autorouter_setting.cost_weights']
      ),
    }
  }, [props.settings])
  const form = useForm<PreferenceValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  // The three preferences that carry a cost weight, which the weights below edit.
  const tiers: Array<{
    value: AutoPreference
    label: string
    description: string
  }> = [
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
  const options = [
    ...tiers,
    {
      value: 'adaptive',
      label: t('Decide per request'),
      description: t(
        'Judges each request on whether it stresses result quality or cost, then reads the cost weight for that request from the three weights below.'
      ),
    },
  ]
  const selected = form.watch('default_preference')

  const onSubmit = async (values: PreferenceValues) => {
    const updates = changedOptions(
      {
        default_preference: defaults.default_preference,
        cost_weights: serializeCostWeights(defaults.weights),
      },
      {
        default_preference: values.default_preference,
        cost_weights: serializeCostWeights(values.weights),
      }
    )
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    await save(updates)
  }

  return (
    <SettingsSection title={t('Preference and cost')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={saving}
          />

          <SelectSettingField
            control={form.control}
            name='default_preference'
            label={t('Default preference for keys without one')}
            description={
              options.find((option) => option.value === selected)?.description
            }
            options={options}
          />

          <div data-settings-form-span='full' className='space-y-1'>
            <h4 className='text-sm font-semibold'>{t('Cost weight')}</h4>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The most ranking score (0 to 1) a model may give up to be cheaper. It also sets how far below the best score a model can still be picked. 0 always picks the best-ranked model.'
              )}
            </p>
          </div>

          <SettingsFormGrid>
            {tiers.map((tier) => (
              <NumberSettingField
                key={tier.value}
                control={form.control}
                name={`weights.${tier.value}`}
                label={tier.label}
                description={t('Built-in value: {{value}}', {
                  value: DEFAULT_COST_WEIGHTS[tier.value],
                })}
                min={0}
                max={1}
                step={0.01}
              />
            ))}
          </SettingsFormGrid>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
