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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
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
  DEFAULT_PREFERENCE_WEIGHTS,
  changedOptions,
  fractionBetween,
  parsePreferenceWeights,
  serializePreferenceWeights,
  type AutoPreference,
  type RoutingWeights,
} from './settings'
import type { AutoRouterSettings } from './types'

function createSchema(t: TFunction) {
  return z.object({
    default_preference: z.enum(SELECTABLE_AUTO_PREFERENCES),
    weights: z.object({
      capability: z.object({
        quality: fractionBetween(t),
        cost: fractionBetween(t),
        capacity: fractionBetween(t),
        success: fractionBetween(t),
        temperature: fractionBetween(t),
      }),
      balanced: z.object({
        quality: fractionBetween(t),
        cost: fractionBetween(t),
        capacity: fractionBetween(t),
        success: fractionBetween(t),
        temperature: fractionBetween(t),
      }),
      cost: z.object({
        quality: fractionBetween(t),
        cost: fractionBetween(t),
        capacity: fractionBetween(t),
        success: fractionBetween(t),
        temperature: fractionBetween(t),
      }),
    }),
  })
}

type PreferenceValues = z.infer<ReturnType<typeof createSchema>>

type PreferenceSectionProps = {
  settings: AutoRouterSettings
}

const DIMENSIONS: ReadonlyArray<{
  name: keyof RoutingWeights
  label: string
  helpText: string
}> = [
  {
    name: 'quality',
    label: 'Capability weight',
    helpText: 'The weight given to model capability and answer quality.',
  },
  {
    name: 'cost',
    label: 'Cost weight',
    helpText:
      'The weight given to model cost; higher values favor cheaper models.',
  },
  {
    name: 'capacity',
    label: 'Capacity weight',
    helpText:
      'The weight given to available capacity; higher values favor less-loaded models.',
  },
  {
    name: 'success',
    label: 'Success weight',
    helpText: 'The weight given to recent request success rate.',
  },
  {
    name: 'temperature',
    label: 'Sampling temperature',
    helpText:
      'Sampling temperature controls score variability; lower values produce more stable routing.',
  },
]

/** The default key preference and the model scoring policy of each fixed preference. */
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
      weights: parsePreferenceWeights(
        props.settings['autorouter_setting.preference_weights']
      ),
    }
  }, [props.settings])
  const form = useForm<PreferenceValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const tiers: Array<{
    value: AutoPreference
    label: string
    description: string
  }> = [
    {
      value: 'capability',
      label: t('Capability first'),
      description: t('Gives cost no weight in model selection.'),
    },
    {
      value: 'balanced',
      label: t('Balanced'),
      description: t('Balances model quality against price.'),
    },
    {
      value: 'cost',
      label: t('Cost first'),
      description: t('Gives lower-priced models more weight.'),
    },
  ]
  const options = [
    ...tiers,
    {
      value: 'adaptive',
      label: t('Decide per request'),
      description: t(
        'Judges each request on whether it stresses result quality or cost, then reads the weights for that request from the three preferences below.'
      ),
    },
  ]
  const selected = form.watch('default_preference')

  const onSubmit = async (values: PreferenceValues) => {
    const updates = changedOptions(
      {
        default_preference: defaults.default_preference,
        preference_weights: serializePreferenceWeights(defaults.weights),
      },
      {
        default_preference: values.default_preference,
        preference_weights: serializePreferenceWeights(values.weights),
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

          <Tabs defaultValue='balanced' data-settings-form-span='full'>
            <TabsList className='w-full'>
              {tiers.map((tier) => (
                <TabsTrigger key={tier.value} value={tier.value}>
                  {tier.label}
                </TabsTrigger>
              ))}
            </TabsList>
            {tiers.map((tier) => (
              <TabsContent key={tier.value} value={tier.value}>
                <SettingsFormGrid>
                  {DIMENSIONS.map((dimension) => (
                    <NumberSettingField
                      key={dimension.name}
                      control={form.control}
                      name={`weights.${tier.value}.${dimension.name}`}
                      label={t(dimension.label)}
                      helpText={t(dimension.helpText)}
                      description={t('Built-in value: {{value}}', {
                        value:
                          DEFAULT_PREFERENCE_WEIGHTS[tier.value][
                            dimension.name
                          ],
                      })}
                      min={0}
                      max={1}
                      step={0.01}
                    />
                  ))}
                </SettingsFormGrid>
              </TabsContent>
            ))}
          </Tabs>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
