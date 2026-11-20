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
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { SettingsCard } from '../components/settings-card'
import { SettingsSwitchField } from '../components/settings-form-layout'
import { safeNumberFieldProps } from '../utils/numeric-field'
import type { RoutingPolicyFormValues } from './routing-form'

export function RetrySection() {
  const { t } = useTranslation()
  const form = useFormContext<RoutingPolicyFormValues>()
  const refusalRetryEnabled = useWatch({
    control: form.control,
    name: 'UpstreamRefusalRetryEnabled',
  })
  return (
    <SettingsCard title={t('Retry budget')} className='shadow-none'>
      <div className='space-y-4'>
        <FormField
          control={form.control}
          name='RetryTimes'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Maximum retries')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  max={99}
                  step={1}
                  {...safeNumberFieldProps(field)}
                />
              </FormControl>
              <FormDescription>
                {t('Excludes the first attempt. Counted per group.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AutomaticRetryStatusCodes'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Auto-retry status codes')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('e.g. 401, 403, 429, 500-599')}
                />
              </FormControl>
              <FormDescription>
                {t('2xx, 504 and 524 are always excluded.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='UpstreamRefusalRetryEnabled'
          render={({ field }) => (
            <SettingsSwitchField
              controlId='UpstreamRefusalRetryEnabled'
              checked={field.value}
              onCheckedChange={field.onChange}
              label={t('Retry on upstream refusal')}
              description={t(
                'When the upstream refuses before any output reaches the client, that attempt is billed as usual but its answer is not sent, and the request is retried. Applies to chat, Responses, Claude and Gemini text requests.'
              )}
            />
          )}
        />
        {refusalRetryEnabled ? (
          <>
            <FormField
              control={form.control}
              name='UpstreamRefusalRetryAutoOnly'
              render={({ field }) => (
                <SettingsSwitchField
                  controlId='UpstreamRefusalRetryAutoOnly'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  label={t('Retry refusals in auto mode only')}
                  description={t(
                    'Only smart-routing (auto) requests retry on a refusal and switch to the next model. Requests locked to a model keep the original behavior: the refusal is returned to the client as usual, with one extra error log.'
                  )}
                />
              )}
            />
            <FormField
              control={form.control}
              name='UpstreamRefusalOpeningTexts'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Refusal opening texts')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={4}
                      placeholder={t('one keyword per line')}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'A streamed answer that starts with one of these texts (case-insensitive) is held until its finish reason arrives. Refusals with other wording are sent through as usual.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </>
        ) : null}
      </div>
    </SettingsCard>
  )
}
