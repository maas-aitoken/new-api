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
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { NumberSettingField, SelectSettingField } from './fields'
import { useSaveAutoRouterOptions } from './hooks'
import {
  AUTO_ROUTER_EFFORTS,
  DIFFICULTY_RUNGS,
  changedOptions,
  fractionBetween,
  normalizeDifficultyEfforts,
} from './settings'
import type { AutoRouterSettings } from './types'

function createSchema(t: TFunction) {
  return z.object({
    continuation_task_change: fractionBetween(t),
    upgrade_benefit: fractionBetween(t),
    default_effort: z.string(),
    difficulty_efforts: z.array(z.string()).length(DIFFICULTY_RUNGS),
  })
}

type JudgmentValues = z.infer<ReturnType<typeof createSchema>>

type JudgmentSectionProps = {
  settings: AutoRouterSettings
}

// The reasoning efforts are the provider's own terms, so their names are not translated.
const effortOptions = AUTO_ROUTER_EFFORTS.map((effort) => ({
  value: effort,
  label: effort,
}))

/** When a request continues the previous task, and which reasoning effort each difficulty gets. */
export function JudgmentSection(props: JudgmentSectionProps) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<JudgmentValues>(
    () => ({
      continuation_task_change:
        props.settings['autorouter_setting.continuation_task_change'],
      upgrade_benefit: props.settings['autorouter_setting.upgrade_benefit'],
      default_effort: props.settings['autorouter_setting.default_effort'],
      difficulty_efforts: normalizeDifficultyEfforts(
        props.settings['autorouter_setting.difficulty_efforts']
      ),
    }),
    [props.settings]
  )
  const form = useForm<JudgmentValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const rungLabels = [
    t('Simple task'),
    t('Moderate task'),
    t('Complex task'),
    t('Very complex task'),
  ]

  const onSubmit = async (values: JudgmentValues) => {
    const updates = changedOptions(
      { ...defaults, difficulty_efforts: JSON.stringify(defaults.difficulty_efforts) },
      { ...values, difficulty_efforts: JSON.stringify(values.difficulty_efforts) }
    )
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    await save(updates)
  }

  return (
    <SettingsSection title={t('Judgment and effort')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={saving}
          />

          <SettingsFormGrid>
            <NumberSettingField
              control={form.control}
              name='continuation_task_change'
              label={t('Continuation threshold')}
              description={t(
                'Below this task-change score the request continues the previous task: the model stays, so the prompt cache survives.'
              )}
              min={0}
              max={1}
              step={0.05}
            />
            <NumberSettingField
              control={form.control}
              name='upgrade_benefit'
              label={t('Effort upgrade threshold')}
              description={t(
                'At or above this larger-model benefit the reasoning effort goes up one step.'
              )}
              min={0}
              max={1}
              step={0.05}
            />
            <SelectSettingField
              control={form.control}
              name='default_effort'
              label={t('Fallback reasoning effort')}
              description={t(
                'Used when a request cannot be classified, such as background requests or a classifier failure.'
              )}
              options={effortOptions}
            />
          </SettingsFormGrid>

          <div data-settings-form-span='full' className='space-y-1'>
            <h4 className='text-sm font-semibold'>
              {t('Reasoning effort by difficulty')}
            </h4>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The classifier rates difficulty on four steps. A model that does not support the chosen effort gets the nearest one it does.'
              )}
            </p>
          </div>

          <SettingsFormGrid>
            {rungLabels.map((label, rung) => (
              <SelectSettingField
                key={label}
                control={form.control}
                name={`difficulty_efforts.${rung}`}
                label={label}
                options={effortOptions}
              />
            ))}
          </SettingsFormGrid>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
