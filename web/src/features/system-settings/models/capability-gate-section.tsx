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
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

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
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  SettingsForm,
  SettingsFormGrid,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { CapabilityGateEventLogs } from './capability-gate-event-logs'

const MIN_BODY_BYTES = 65536
const MAX_BODY_BYTES = 67108864
const MAX_RETENTION_DAYS = 3650

type GateMode = 'off' | 'shadow' | 'enforce'

type CapabilityGateDefaults = {
  mode: GateMode
  max_body_bytes: number
  event_retention_days: number
  record_only_dimensions: string
  record_passed: boolean
}

// 与后端 CapabilityGateDimensionGroups 一一对应；值会原样存进
// capability_gate_setting.record_only_dimensions（逗号分隔）。
const DIMENSION_GROUPS: Array<{
  value: string
  label: string
  descriptionKey: string
}> = [
  {
    value: 'context',
    label: 'context.*',
    descriptionKey: 'Input/output token limits',
  },
  {
    value: 'modalities',
    label: 'modalities.*',
    descriptionKey: 'Input/output modalities, formats and media limits',
  },
  {
    value: 'tools',
    label: 'tools.*',
    descriptionKey: 'Tool calling, tool choice and builtin tools',
  },
  {
    value: 'structured_output',
    label: 'structured_output.*',
    descriptionKey: 'JSON mode and JSON schema output',
  },
  {
    value: 'reasoning',
    label: 'reasoning.*',
    descriptionKey: 'Thinking support, efforts and token budget',
  },
  {
    value: 'messages',
    label: 'messages.*',
    descriptionKey: 'System role, multi-system and assistant prefill',
  },
  {
    value: 'streaming',
    label: 'streaming.*',
    descriptionKey: 'Streaming support',
  },
  {
    value: 'params',
    label: 'params.*',
    descriptionKey:
      'temperature / top_p / top_k / stop / n / seed / logprobs / penalties',
  },
  {
    value: 'tools.parallel',
    label: 'tools.parallel',
    descriptionKey: 'Parallel tool calls requested by the client',
  },
]

const createSchema = (t: (key: string) => string) =>
  z.object({
    mode: z.enum(['off', 'shadow', 'enforce']),
    max_body_bytes: z.coerce
      .number()
      .int()
      .min(MIN_BODY_BYTES, t('Minimum 65536 bytes'))
      .max(MAX_BODY_BYTES, t('Maximum 67108864 bytes')),
    event_retention_days: z.coerce
      .number()
      .int()
      .min(1, t('Minimum 1 day'))
      .max(MAX_RETENTION_DAYS, t('Maximum 3650 days')),
    record_only_dimensions: z.string(),
    record_passed: z.boolean(),
  })

type GateFormValues = z.output<ReturnType<typeof createSchema>>
type GateFormInput = z.input<ReturnType<typeof createSchema>>

function formatMebibytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '-'
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`
}

export function CapabilityGateSection(props: {
  defaultValues: CapabilityGateDefaults
}) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState('configuration')
  const updateOption = useUpdateOption()
  const schema = useMemo(() => createSchema(t), [t])
  const formDefaults = useMemo<GateFormValues>(
    () => ({
      mode: props.defaultValues.mode,
      max_body_bytes: props.defaultValues.max_body_bytes,
      event_retention_days: props.defaultValues.event_retention_days,
      record_only_dimensions: props.defaultValues.record_only_dimensions,
      record_passed: props.defaultValues.record_passed,
    }),
    [props.defaultValues]
  )
  const form = useForm<GateFormInput, unknown, GateFormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  })
  useResetForm(form, formDefaults)

  const onSubmit = async (values: GateFormValues) => {
    await updateOption.mutateAsync({
      key: 'capability_gate_setting.mode',
      value: values.mode,
    })
    await updateOption.mutateAsync({
      key: 'capability_gate_setting.max_body_bytes',
      value: values.max_body_bytes,
    })
    await updateOption.mutateAsync({
      key: 'capability_gate_setting.event_retention_days',
      value: values.event_retention_days,
    })
    await updateOption.mutateAsync({
      key: 'capability_gate_setting.record_only_dimensions',
      value: values.record_only_dimensions,
    })
    await updateOption.mutateAsync({
      key: 'capability_gate_setting.record_passed',
      value: values.record_passed,
    })
    toast.success(t('Capability gate settings saved'))
  }

  const maxBodyBytes = Number(form.watch('max_body_bytes'))
  const recordOnlyDimensions = (
    form.watch('record_only_dimensions') || ''
  ).split(',')
  const setRecordOnly = (value: string, recordOnly: boolean) => {
    const next = new Set(
      (form.getValues('record_only_dimensions') || '')
        .split(',')
        .filter(Boolean)
    )
    if (recordOnly) {
      next.add(value)
    } else {
      next.delete(value)
    }
    const ordered = DIMENSION_GROUPS.map((group) => group.value).filter(
      (candidate) => next.has(candidate)
    )
    form.setValue('record_only_dimensions', ordered.join(','), {
      shouldDirty: true,
    })
  }

  return (
    <Tabs value={activeTab} onValueChange={setActiveTab}>
      <TabsList className='grid w-full grid-cols-2'>
        <TabsTrigger value='configuration'>
          {t('Parameter Configuration')}
        </TabsTrigger>
        <TabsTrigger value='logs'>{t('Log Query')}</TabsTrigger>
      </TabsList>

      <TabsContent value='configuration' className='pt-4'>
        <Form {...form}>
          <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
            <SettingsPageFormActions
              onSave={form.handleSubmit(onSubmit)}
              isSaving={updateOption.isPending}
            />

            <div>
              <h3 className='text-sm font-semibold'>
                {t('Capability gate')}
              </h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Checks each request against the model-level capability declaration and each channel narrowing before routing.'
                )}
              </p>
            </div>

            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='mode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Gate mode')}</FormLabel>
                    <Select
                      value={field.value}
                      onValueChange={(value) => field.onChange(value)}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value='off'>{t('Off')}</SelectItem>
                          <SelectItem value='shadow'>
                            {t('Shadow (log only)')}
                          </SelectItem>
                          <SelectItem value='enforce'>
                            {t('Enforce (block)')}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormDescription>
                      {t(
                        'Shadow evaluates and logs decisions without changing routing. Enforce drops channels that cannot satisfy the request.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='max_body_bytes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Max request body bytes')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={MIN_BODY_BYTES}
                        max={MAX_BODY_BYTES}
                        step={1024}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Requests larger than this are not parsed for capability checks. Current: {{size}}',
                        { size: formatMebibytes(maxBodyBytes) }
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='event_retention_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Gate log retention (days)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={MAX_RETENTION_DAYS}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Gate events older than this are removed hourly by a scheduled task. ClickHouse log databases expire them through the table TTL instead.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>

            <FormField
              control={form.control}
              name='record_passed'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Record passed decisions')}</FormLabel>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={(checked) => field.onChange(checked)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'When on, every request writes one event, including requests where all channels satisfy the declaration. Use it briefly for troubleshooting; leave it off to keep only violations.'
                    )}
                  </FormDescription>
                </FormItem>
              )}
            />

            <div className='space-y-2'>
              <h3 className='text-sm font-semibold'>
                {t('Dimension policy')}
              </h3>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Only declared dimensions are checked. Switch a dimension to recorded-only to log the violation without blocking the request.'
                )}
              </p>
              <div className='overflow-hidden rounded-md border'>
                <Table className='min-w-[560px]'>
                  <TableHeader>
                    <TableRow className='bg-muted/40 hover:bg-muted/40'>
                      <TableHead>{t('Dimension')}</TableHead>
                      <TableHead>{t('Policy')}</TableHead>
                      <TableHead>{t('Description')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {DIMENSION_GROUPS.map((group) => {
                      const recordOnly = recordOnlyDimensions.includes(
                        group.value
                      )
                      return (
                        <TableRow key={group.value}>
                          <TableCell className='font-mono text-xs'>
                            {group.label}
                          </TableCell>
                          <TableCell className='whitespace-nowrap'>
                            <label className='flex cursor-pointer items-center gap-2 text-xs'>
                              <Switch
                                checked={!recordOnly}
                                onCheckedChange={(checked) =>
                                  setRecordOnly(group.value, !checked)
                                }
                                aria-label={t('Block {{dimension}} violations', {
                                  dimension: group.label,
                                })}
                              />
                              {recordOnly ? t('Recorded only') : t('Blocking')}
                            </label>
                          </TableCell>
                          <TableCell className='text-muted-foreground text-xs'>
                            {t(group.descriptionKey)}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </div>
            </div>
          </SettingsForm>
        </Form>
      </TabsContent>

      <TabsContent value='logs' className='pt-4'>
        <CapabilityGateEventLogs />
      </TabsContent>
    </Tabs>
  )
}