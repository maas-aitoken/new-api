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
import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const thinkingBlacklistExample = JSON.stringify(
  ['moonshotai/kimi-k2-thinking', 'kimi-k2-thinking', 're:.*@sha256:.*'],
  null,
  2
)

const chatToResponsesPolicyExample = JSON.stringify(
  {
    enabled: true,
    all_channels: false,
    channel_ids: [1, 2],
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const chatToResponsesPolicyAllChannelsExample = JSON.stringify(
  {
    enabled: true,
    all_channels: true,
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const gptRequestFastPolicyOptions = [
  { value: 'disabled', label: 'Disable fast' },
  { value: 'allow', label: 'Allow client choice' },
] as const

const gptRequestReasoningPolicyOptions = [
  { value: 'client', label: 'Use client value' },
  { value: 'cap_xhigh', label: 'Cap at xhigh' },
] as const

const jsonString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  try {
    JSON.parse(trimmed)
    return true
  } catch {
    return false
  }
}, 'Invalid JSON format')

const schema = z.object({
  global: z.object({
    pass_through_request_enabled: z.boolean(),
    thinking_model_blacklist: jsonString,
    chat_completions_to_responses_policy: jsonString,
    gpt_request_policy: z.object({
      fast_policy: z.enum(['disabled', 'allow']),
      reasoning_policy: z.enum(['client', 'cap_xhigh']),
    }),
  }),
  general_setting: z.object({
    ping_interval_enabled: z.boolean(),
    ping_interval_seconds: z.coerce.number().min(1),
  }),
})

type GlobalModelSettingsFormValues = z.output<typeof schema>
type GlobalModelSettingsFormInput = z.input<typeof schema>

type FlatGlobalModelSettings = {
  'global.pass_through_request_enabled': boolean
  'global.thinking_model_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'global.gpt_request_policy.fast_policy': 'disabled' | 'allow'
  'global.gpt_request_policy.reasoning_policy': 'client' | 'cap_xhigh'
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
}

const flattenGlobalValues = (
  values: GlobalModelSettingsFormValues
): FlatGlobalModelSettings => ({
  'global.pass_through_request_enabled':
    values.global.pass_through_request_enabled,
  'global.thinking_model_blacklist': normalizeJsonText(
    values.global.thinking_model_blacklist,
    '[]'
  ),
  'global.chat_completions_to_responses_policy': normalizeJsonText(
    values.global.chat_completions_to_responses_policy,
    '{}'
  ),
  'global.gpt_request_policy.fast_policy':
    values.global.gpt_request_policy.fast_policy,
  'global.gpt_request_policy.reasoning_policy':
    values.global.gpt_request_policy.reasoning_policy,
  'general_setting.ping_interval_enabled':
    values.general_setting.ping_interval_enabled,
  'general_setting.ping_interval_seconds':
    values.general_setting.ping_interval_seconds,
})

function normalizeJsonText(value: string, fallback: string) {
  const trimmed = (value ?? '').toString().trim()
  return trimmed ? trimmed : fallback
}

type GlobalSettingsCardProps = {
  defaultValues: GlobalModelSettingsFormValues
}

export function GlobalSettingsCard({ defaultValues }: GlobalSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const defaultValuesKey = JSON.stringify(defaultValues)
  const stableDefaultValues = useMemo(
    () => JSON.parse(defaultValuesKey) as GlobalModelSettingsFormValues,
    [defaultValuesKey]
  )

  const form = useForm<
    GlobalModelSettingsFormInput,
    unknown,
    GlobalModelSettingsFormValues
  >({
    resolver: zodResolver(schema),
    defaultValues: stableDefaultValues as GlobalModelSettingsFormInput,
  })

  useEffect(() => {
    form.reset(stableDefaultValues as GlobalModelSettingsFormInput)
  }, [form, stableDefaultValues])

  const pingEnabled = form.watch('general_setting.ping_interval_enabled')

  const onSubmit = async (values: GlobalModelSettingsFormValues) => {
    const flattenedDefaults = flattenGlobalValues(stableDefaultValues)
    const flattenedValues = flattenGlobalValues(values)
    const updates = Object.entries(flattenedValues).filter(
      ([key, value]) =>
        value !== flattenedDefaults[key as keyof FlatGlobalModelSettings]
    )

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    try {
      for (const [key, value] of updates) {
        await updateOption.mutateAsync({
          key,
          value,
        })
      }
      // The mutation updates the query cache and refetches the authoritative
      // server snapshot. Resetting here also clears stale dirty state while
      // that refetch is in flight.
      form.reset(values as GlobalModelSettingsFormInput)
    } catch {
      // useUpdateOption already reports the server error; keep unsaved values
      // in the form so the user can correct and retry them.
    }
  }

  return (
    <SettingsSection title={t('Global Model Configuration')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='global.pass_through_request_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Request Passthrough')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Forward requests directly to upstream providers without any post-processing.'
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

          <Separator />

          <div className='space-y-4'>
            <div className='flex items-center gap-2'>
              <h3 className='text-base font-semibold'>
                {t('GPT Request Parameter Policy')}
              </h3>
              <StatusBadge
                label={t('Preview')}
                variant='neutral'
                copyable={false}
              />
            </div>

            <Alert>
              <AlertTitle>{t('Central policy')}</AlertTitle>
              <AlertDescription>
                {t(
                  'These controls apply to GPT and o-series model requests sent through supported GPT channels.'
                )}
              </AlertDescription>
            </Alert>

            <div className='grid gap-5 lg:grid-cols-2'>
              <FormField
                control={form.control}
                name='global.gpt_request_policy.fast_policy'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Fast service tier policy')}</FormLabel>
                    <Select
                      items={gptRequestFastPolicyOptions.map((option) => ({
                        value: option.value,
                        label: t(option.label),
                      }))}
                      value={field.value}
                      onValueChange={(value) => {
                        if (value) field.onChange(value)
                      }}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {gptRequestFastPolicyOptions.map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {t(option.label)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        'Choose whether fast is removed or passed through for GPT requests.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='global.gpt_request_policy.reasoning_policy'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Reasoning effort policy')}</FormLabel>
                    <Select
                      items={gptRequestReasoningPolicyOptions.map((option) => ({
                        value: option.value,
                        label: t(option.label),
                      }))}
                      value={field.value}
                      onValueChange={(value) => {
                        if (value) field.onChange(value)
                      }}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {gptRequestReasoningPolicyOptions.map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {t(option.label)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        'Choose whether GPT requests keep the client value or cap reasoning effort at the configured maximum.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>

          <FormField
            control={form.control}
            name='global.thinking_model_blacklist'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Models that skip thinking suffix processing')}
                </FormLabel>
                <FormControl>
                  <JsonCodeEditor
                    value={field.value}
                    onChange={(value) => field.onChange(value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    textareaRef={field.ref}
                    placeholder={`${t('Example:')}\n${thinkingBlacklistExample}`}
                    heightClassName='h-32 min-h-32 max-h-32'
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Models listed here skip automatic -thinking / -nothinking suffix handling. Matched names are also exempt from @-modifier parsing and 400 validation. Prefix an entry with re: to match the full model name as a Go regular expression, for example re:.*@sha256:.*'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div className='space-y-4'>
            <div className='flex items-center gap-2'>
              <h3 className='text-base font-semibold'>
                {t('ChatCompletions -> Responses Compatibility')}
              </h3>
              <StatusBadge
                label={t('Preview')}
                variant='neutral'
                copyable={false}
              />
            </div>

            <Alert>
              <AlertTitle>{t('Warning')}</AlertTitle>
              <AlertDescription>
                {t(
                  'This feature is experimental. Configuration format and behavior may change.'
                )}
              </AlertDescription>
            </Alert>

            <FormField
              control={form.control}
              name='global.chat_completions_to_responses_policy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Policy JSON')}</FormLabel>
                  <FormControl>
                    <JsonCodeEditor
                      value={field.value}
                      onChange={(value) => field.onChange(value)}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      placeholder={`${t('Example (specific channels):')}\n${chatToResponsesPolicyExample}\n\n${t('Example (all channels):')}\n${chatToResponsesPolicyAllChannelsExample}`}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Empty value will be saved as {}.')}
                  </FormDescription>
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (specific channels)')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyAllChannelsExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (all channels)')}
                    </Button>
                  </div>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <FormField
            control={form.control}
            name='general_setting.ping_interval_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Keep-alive Ping')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Periodically send ping frames to keep streaming connections active.'
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

          <FormField
            control={form.control}
            name='general_setting.ping_interval_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Ping Interval (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    disabled={!pingEnabled}
                    className='w-24'
                    value={
                      field.value === undefined || field.value === null
                        ? ''
                        : String(field.value)
                    }
                    onChange={(event) => field.onChange(event.target.value)}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Recommended to keep this high to avoid upstream throttling.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
