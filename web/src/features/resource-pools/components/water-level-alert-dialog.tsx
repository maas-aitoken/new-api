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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BellRing, Send } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { PasswordInput } from '@/components/password-input'
import { Badge } from '@/components/ui/badge'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'

import {
  getResourcePoolAlert,
  saveResourcePoolAlert,
  testResourcePoolAlert,
  type ResourcePoolAlertConfig,
} from '../api'
import {
  ALERT_PROVIDERS,
  waterLevelAlertFormDefaults,
  waterLevelAlertFormSchema,
  waterLevelAlertInputFromForm,
  type WaterLevelAlertFormValues,
} from '../lib/water-level-alert-form'
import { formatResetAt } from '../pool-metrics'
import { ProviderIcon } from '../pool-ui'
import { PROVIDERS } from '../providers'

type FormInput = z.input<typeof waterLevelAlertFormSchema>

const alertQueryKey = ['resource-pools', 'alert'] as const

const providerMeta = (key: string) => PROVIDERS.find((item) => item.key === key)

/** 资源池页右上角的入口：有目标处于告警中时带上数量。 */
export function WaterLevelAlertButton() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const alert = useQuery({
    queryKey: alertQueryKey,
    queryFn: getResourcePoolAlert,
    refetchInterval: 60_000,
    meta: { errorToast: false },
  })
  const alertingCount = alert.data?.alerting.length ?? 0

  return (
    <>
      <Button variant='outline' size='sm' onClick={() => setOpen(true)}>
        <BellRing />
        {t('Water level alerts')}
        {alertingCount > 0 && (
          <Badge variant='destructive' aria-label={t('Currently alerting')}>
            {alertingCount}
          </Badge>
        )}
      </Button>
      <WaterLevelAlertDialog
        open={open}
        onOpenChange={setOpen}
        config={alert.data}
        loadFailed={alert.isError}
      />
    </>
  )
}

/** 水位告警配置：对四个 provider 全局生效，不随页面上选中的 provider 变化。 */
export function WaterLevelAlertDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  config: ResourcePoolAlertConfig | undefined
  loadFailed?: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const form = useForm<FormInput, unknown, WaterLevelAlertFormValues>({
    resolver: zodResolver(waterLevelAlertFormSchema),
    defaultValues: waterLevelAlertFormDefaults(props.config),
  })
  const enabled = useWatch({ control: form.control, name: 'enabled' })

  // 打开时按服务端配置重置；配置在打开之后才加载出来时补一次。
  // 已经开始填写后，每分钟的轮询不能冲掉输入。
  const loaded = useRef<'none' | 'defaults' | 'config'>('none')
  useEffect(() => {
    if (!props.open) {
      loaded.current = 'none'
      return
    }
    const next = props.config ? 'config' : 'defaults'
    if (loaded.current === next) return
    if (loaded.current !== 'none' && form.formState.isDirty) return
    loaded.current = next
    form.reset(waterLevelAlertFormDefaults(props.config))
  }, [props.open, props.config, form])

  const save = useMutation({
    mutationFn: (values: WaterLevelAlertFormValues) =>
      saveResourcePoolAlert(waterLevelAlertInputFromForm(values)),
    onSuccess: (config) => {
      queryClient.setQueryData(alertQueryKey, config)
      toast.success(t('Water level alert settings saved'))
      props.onOpenChange(false)
    },
  })

  const test = useMutation({
    mutationFn: () =>
      testResourcePoolAlert(
        form.getValues('webhook_url').trim(),
        form.getValues('mentions').trim()
      ),
    onSuccess: () => toast.success(t('Test message sent')),
  })
  const sendTest = async () => {
    if (!(await form.trigger(['webhook_url', 'mentions']))) return
    if (
      !form.getValues('webhook_url').trim() &&
      !form.getValues('webhook_configured')
    ) {
      form.setError('webhook_url', {
        message: 'Set the webhook URL before enabling alerts',
      })
      return
    }
    test.mutate()
  }

  const busy = save.isPending || test.isPending
  // 没读到已保存的配置时不让保存：用默认值覆盖会把现有规则冲掉。
  const loading = !props.config && !props.loadFailed
  const unavailable = !props.config
  const alerting = props.config?.alerting ?? []

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => !save.isPending && props.onOpenChange(open)}
      title={t('Water level alerts')}
      description={t(
        'Checks the water level of every enabled provider once an hour and notifies a WeCom group when a target reaches its threshold, then once more when it drops back below.'
      )}
      contentClassName='sm:max-w-3xl'
      footer={
        <>
          <Button
            variant='outline'
            className='sm:me-auto'
            onClick={sendTest}
            disabled={busy || unavailable}
          >
            <Send />
            {t('Send test message')}
          </Button>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={save.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='water-level-alert-form'
            disabled={busy || unavailable}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      {loading && (
        <div className='space-y-3'>
          <Skeleton className='h-9 w-full' />
          <Skeleton className='h-9 w-full' />
          <Skeleton className='h-32 w-full' />
        </div>
      )}
      {!loading && unavailable && (
        <p className='text-destructive text-sm' role='alert'>
          {t('Failed to load water level alert settings')}
        </p>
      )}
      {!unavailable && (
        <Form {...form}>
          <form
            id='water-level-alert-form'
            className='space-y-5'
            onSubmit={form.handleSubmit((values) => save.mutate(values))}
          >
            {alerting.length > 0 && (
              <div className='space-y-2'>
                <div className='text-sm font-medium'>
                  {t('Currently alerting')}
                </div>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Provider')}</TableHead>
                      <TableHead>{t('Alert target')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Water level')}
                      </TableHead>
                      <TableHead>{t('Alerting since')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {alerting.map((target) => (
                      <TableRow
                        key={`${target.provider}/${target.kind}/${target.name}`}
                      >
                        <TableCell>
                          {providerMeta(target.provider)?.label ??
                            target.provider}
                        </TableCell>
                        <TableCell>
                          {target.kind === 'pool' && t('Overall')}
                          {target.kind !== 'pool' && (
                            <>
                              <span className='text-muted-foreground me-1.5 text-xs'>
                                {target.kind === 'group'
                                  ? t('Group')
                                  : t('Channel')}
                              </span>
                              {target.name || t('Ungrouped')}
                            </>
                          )}
                        </TableCell>
                        <TableCell className='text-right text-red-600 tabular-nums dark:text-red-400'>
                          {target.level.toFixed(1)}%
                        </TableCell>
                        <TableCell className='text-muted-foreground tabular-nums'>
                          {formatResetAt(
                            new Date(target.since * 1000).toISOString(),
                            locale
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
                <p className='text-muted-foreground text-xs'>
                  {t('Water levels are as of the last hourly check.')}
                </p>
              </div>
            )}
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-start gap-3'>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <div className='space-y-1'>
                    <FormLabel>{t('Enable water level alerts')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Turning alerts off clears the alerting targets, so turning them back on reports everything that is above its threshold again.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </div>
                </FormItem>
              )}
            />

            <div className='space-y-2'>
              <div className='text-sm font-medium'>{t('Thresholds')}</div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Claude is checked as a whole and per group by used quota. GPT, Gemini and Grok are checked per channel by the average 7-day window usage of live accounts.'
                )}
              </p>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Provider')}</TableHead>
                    <TableHead>{t('Enabled')}</TableHead>
                    <TableHead>{t('Threshold (%)')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ALERT_PROVIDERS.map((provider) => {
                    const meta = providerMeta(provider)
                    const label = meta?.label ?? provider
                    return (
                      <TableRow key={provider}>
                        <TableCell>
                          <span className='flex items-center gap-2 font-medium'>
                            {meta && <ProviderIcon meta={meta} />}
                            {label}
                          </span>
                        </TableCell>
                        <TableCell>
                          <FormField
                            control={form.control}
                            name={`rules.${provider}.enabled`}
                            render={({ field }) => (
                              <Switch
                                aria-label={`${label} ${t('Enabled')}`}
                                checked={field.value}
                                onCheckedChange={field.onChange}
                              />
                            )}
                          />
                        </TableCell>
                        <TableCell>
                          <FormField
                            control={form.control}
                            name={`rules.${provider}.threshold`}
                            render={({ field }) => (
                              <FormItem>
                                <FormControl>
                                  <Input
                                    type='number'
                                    inputMode='decimal'
                                    min={0}
                                    max={100}
                                    step='any'
                                    className='h-8 w-28'
                                    aria-label={`${label} ${t('Threshold (%)')}`}
                                    {...field}
                                    value={String(field.value ?? '')}
                                  />
                                </FormControl>
                                <FormMessage />
                              </FormItem>
                            )}
                          />
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
              <FormField
                control={form.control}
                name='claude_source_enabled'
                render={({ field }) => (
                  <FormItem className='flex items-start gap-3 pt-1'>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <div className='space-y-1'>
                      <FormLabel>{t('Monitor Claude channels')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Also checks every Claude channel on its own, with the Claude threshold. Off by default, so Claude is only checked as a whole and per group.'
                        )}
                      </FormDescription>
                    </div>
                  </FormItem>
                )}
              />
            </div>

            <div className='space-y-3'>
              <div className='text-sm font-medium'>
                {t('WeCom bot notifications')}
              </div>
              <FormField
                control={form.control}
                name='webhook_url'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel
                      required={enabled && !props.config?.webhook_configured}
                    >
                      {t('Webhook URL')}
                    </FormLabel>
                    <FormControl>
                      <PasswordInput
                        autoComplete='new-password'
                        placeholder='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {props.config?.webhook_configured
                        ? t('Configured as {{url}}. Leave blank to keep it.', {
                            url: props.config.webhook_url,
                          })
                        : t(
                            'Not configured. Paste the webhook address of a WeCom group bot.'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='mentions'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Mention contacts')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='zhangsan|13800000000|@all'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'WeCom user IDs or mobile numbers, separated with |. Use @all to mention everyone. Leave blank to mention nobody.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <div className='space-y-3'>
              <div className='text-sm font-medium'>
                {t('Notification hours')}
              </div>
              <div className='grid gap-4 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='notify_start'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Start time')}</FormLabel>
                      <FormControl>
                        <Input type='time' {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='notify_end'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('End time')}</FormLabel>
                      <FormControl>
                        <Input type='time' {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Server time (UTC{{offset}}). Checks run once per clock hour inside these hours, both ends included, and are skipped outside them. A level that crosses its threshold outside these hours is reported by the next check if it still holds. An end earlier than the start runs past midnight, and equal times mean all day.',
                  { offset: props.config?.utc_offset ?? '' }
                )}
              </p>
            </div>
          </form>
        </Form>
      )}
    </Dialog>
  )
}
