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
import { TriangleAlert } from 'lucide-react'
import { useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
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
import { SecretField } from './secret-field'
import { changedOptions, integerBetween, plainText } from './settings'
import type { AutoRouterSettings } from './types'

const MAX_RUNES = 100000
const MAX_CACHE_TTL_SECONDS = 30 * 24 * 3600

function createSchema(t: TFunction) {
  return z.object({
    enabled: z.boolean(),
    jev_endpoint: z
      .string()
      .trim()
      .max(2048, t('Must be at most {{max}} characters', { max: 2048 }))
      .refine((value) => {
        if (value === '') return true
        try {
          const url = new URL(value)
          return url.protocol === 'http:' || url.protocol === 'https:'
        } catch {
          return false
        }
      }, t('Must be an http or https URL')),
    jev_model: plainText(t, 128),
    jev_api_key: plainText(t, 512),
    jev_timeout_ms: integerBetween(t, 100, 60000),
    max_runes: integerBetween(t, 0, MAX_RUNES),
    previous_max_runes: integerBetween(t, 0, MAX_RUNES),
    cache_ttl_seconds: integerBetween(t, 1, MAX_CACHE_TTL_SECONDS),
  })
}

type ConnectionValues = z.infer<ReturnType<typeof createSchema>>

type ConnectionSectionProps = {
  settings: AutoRouterSettings
}

/** The switch, the classifier ("jev") connection, and how its answers are limited and cached. */
export function ConnectionSection(props: ConnectionSectionProps) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const status = useAutoRouterStatus()
  const schema = useMemo(() => createSchema(t), [t])
  const defaults = useMemo<ConnectionValues>(
    () => ({
      enabled: props.settings['autorouter_setting.enabled'],
      jev_endpoint: props.settings['autorouter_setting.jev_endpoint'],
      jev_model: props.settings['autorouter_setting.jev_model'],
      jev_api_key: '',
      jev_timeout_ms: props.settings['autorouter_setting.jev_timeout_ms'],
      max_runes: props.settings['autorouter_setting.max_runes'],
      previous_max_runes:
        props.settings['autorouter_setting.previous_max_runes'],
      cache_ttl_seconds: props.settings['autorouter_setting.cache_ttl_seconds'],
    }),
    [props.settings]
  )
  const form = useForm<ConnectionValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  useResetForm(form, defaults)

  const onSubmit = async (values: ConnectionValues) => {
    // The key's default is empty, so it only differs when one was typed.
    const updates = changedOptions(defaults, values)
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    if (await save(updates)) form.setValue('jev_api_key', '')
  }

  const sources = status.data?.data.sources
  const enabled = form.watch('enabled')
  const typedEndpoint = form.watch('jev_endpoint').trim() !== ''
  const endpointMissing =
    enabled && status.data?.data.jev_ready === false && !typedEndpoint

  let endpointDescription = t('Where each request is sent to be classified.')
  if (sources?.jev_endpoint === 'env') {
    endpointDescription = t(
      'Using the {{name}} environment variable. A value entered here overrides it.',
      { name: 'JEV_ENDPOINT' }
    )
  }
  let modelDescription = t(
    'Required by OpenRouter, ignored by a self-hosted endpoint.'
  )
  if (sources?.jev_model === 'env') {
    modelDescription = t(
      'Using the {{name}} environment variable. A value entered here overrides it.',
      { name: 'JEV_MODEL' }
    )
  }

  return (
    <SettingsSection title={t('Basics')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={saving}
          />

          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable auto routing')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Lets clients request the "auto" model; the gateway then picks the model for each request.'
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

          {endpointMissing ? (
            <Alert>
              <TriangleAlert aria-hidden='true' />
              <AlertTitle>{t('No classifier endpoint is configured')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Auto cannot tell what a request is about, so every request uses the default models.'
                )}
              </AlertDescription>
            </Alert>
          ) : null}

          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='jev_endpoint'
              render={({ field }) => (
                <FormItem data-settings-form-span='full'>
                  <FormLabel>{t('Classifier endpoint')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      autoComplete='off'
                      placeholder='https://openrouter.ai/api/alpha/decisions'
                    />
                  </FormControl>
                  <FormDescription>{endpointDescription}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='jev_model'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Classifier model')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      autoComplete='off'
                      placeholder='typesafe/jev-1.13'
                    />
                  </FormControl>
                  <FormDescription>{modelDescription}</FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <SecretField
              control={form.control}
              name='jev_api_key'
              connection='jev_api_key'
              label={t('Classifier API key')}
              envName='JEV_API_KEY'
              source={sources?.jev_api_key}
            />
            <NumberSettingField
              control={form.control}
              name='jev_timeout_ms'
              label={t('Classification timeout (ms)')}
              description={t(
                'How long to wait for the classifier. On a timeout or an error the request keeps the previous model, or uses the default models.'
              )}
              min={100}
              max={60000}
              step={100}
            />
            <NumberSettingField
              control={form.control}
              name='cache_ttl_seconds'
              label={t('Decision cache (seconds)')}
              description={t(
                'How long a classification is reused for the same instruction, so an agent loop does not call the classifier on every turn.'
              )}
              min={1}
              max={MAX_CACHE_TTL_SECONDS}
            />
            <NumberSettingField
              control={form.control}
              name='max_runes'
              label={t('Instruction length limit')}
              description={t(
                'The most characters of the current instruction sent to the classifier; longer text keeps its head and tail. 0 means no limit.'
              )}
              min={0}
              max={MAX_RUNES}
            />
            <NumberSettingField
              control={form.control}
              name='previous_max_runes'
              label={t('Previous instruction length limit')}
              description={t(
                'The most characters of the previous instruction sent along for context. 0 means no limit.'
              )}
              min={0}
              max={MAX_RUNES}
            />
          </SettingsFormGrid>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
