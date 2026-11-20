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
import { useAutoRouterStatus, useSaveAutoRouterOptions } from './hooks'
import { RankingStatus } from './ranking-status'
import { RankingViewer } from './ranking-viewer'
import { SecretField } from './secret-field'
import { changedOptions, integerBetween, plainText } from './settings'
import type { AutoRouterSettings } from './types'

const SECRET_NAMES = [
  'artificial_analysis_api_key',
  'design_arena_api_key',
  'openrouter_api_key',
] as const

function createSchema(t: TFunction) {
  return z.object({
    rank_refresh_enabled: z.boolean(),
    rank_refresh_interval_hours: integerBetween(t, 1, 720),
    artificial_analysis_api_key: plainText(t, 512),
    design_arena_api_key: plainText(t, 512),
    openrouter_api_key: plainText(t, 512),
  })
}

type RankingValues = z.infer<ReturnType<typeof createSchema>>

type RankingSectionProps = {
  settings: AutoRouterSettings
}

/** Where the model ranking comes from, how often it is rebuilt, and the keys that need. */
export function RankingSection(props: RankingSectionProps) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const status = useAutoRouterStatus()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<RankingValues>(
    () => ({
      rank_refresh_enabled:
        props.settings['autorouter_setting.rank_refresh_enabled'],
      rank_refresh_interval_hours:
        props.settings['autorouter_setting.rank_refresh_interval_hours'],
      artificial_analysis_api_key: '',
      design_arena_api_key: '',
      openrouter_api_key: '',
    }),
    [props.settings]
  )
  const form = useForm<RankingValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const sources = status.data?.data.sources
  const keys = [
    {
      name: 'artificial_analysis_api_key',
      label: t('Artificial Analysis API key'),
      envName: 'ARTIFICIAL_ANALYSIS_API_KEY',
    },
    {
      name: 'design_arena_api_key',
      label: t('Design Arena API key'),
      envName: 'DESIGN_ARENA_API_KEY',
    },
    {
      name: 'openrouter_api_key',
      label: t('OpenRouter API key'),
      envName: 'OPENROUTER_API_KEY',
    },
  ] as const

  const onSubmit = async (values: RankingValues) => {
    // A key's default is empty, so it only differs when one was typed.
    const updates = changedOptions(defaults, values)
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    if (await save(updates)) {
      for (const name of SECRET_NAMES) form.setValue(name, '')
    }
  }

  return (
    <SettingsSection title={t('Leaderboard')}>
      <RankingStatus />

      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={saving}
          />

          <FormField
            control={form.control}
            name='rank_refresh_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Refresh the ranking periodically')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Rebuilds the ranking from public leaderboards on a schedule and replaces the stored one. It needs the keys below.'
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
              name='rank_refresh_interval_hours'
              label={t('Refresh interval (hours)')}
              min={1}
              max={720}
            />
            {keys.map((key) => (
              <SecretField
                key={key.name}
                control={form.control}
                name={key.name}
                connection={key.name}
                label={key.label}
                envName={key.envName}
                source={sources?.[key.name]}
              />
            ))}
          </SettingsFormGrid>
        </SettingsForm>
      </Form>

      <RankingViewer />
    </SettingsSection>
  )
}
