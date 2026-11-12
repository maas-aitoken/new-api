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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import {
  useFieldArray,
  useForm,
  useWatch,
  type Control,
  type FieldPath,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
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
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  saveCapacityPool,
  type CapacityPool,
  type ResourcePoolAccount,
  type ResourcePoolProvider,
  type ResourcePoolSource,
} from '../api'
import {
  capacityAccountPlan,
  capacityPoolConfigFromForm,
  capacityPoolFormDefaults,
  capacityPoolFormSchema,
  inCapacityScope,
  type CapacityPoolFormValues,
} from '../lib/capacity-pool-form'
import { accountGroup } from '../pool-metrics'
import { capacityPoolQueryKeys } from './capacity-pool-query-keys'

type FormInput = z.input<typeof capacityPoolFormSchema>
type NumberPath = FieldPath<FormInput>

/** 表格里的一个数字格子：没有可见标签，靠 aria-label 说明是哪一行哪一列。 */
function NumberCell(props: {
  control: Control<FormInput, unknown, CapacityPoolFormValues>
  name: NumberPath
  label: string
  placeholder?: string
}) {
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field, fieldState }) => (
        <Input
          type='number'
          inputMode='numeric'
          min={0}
          className='h-8 w-24'
          aria-label={props.label}
          aria-invalid={fieldState.invalid}
          placeholder={props.placeholder ?? '∞'}
          name={field.name}
          ref={field.ref}
          onBlur={field.onBlur}
          value={field.value ? String(field.value) : ''}
          onChange={(event) => field.onChange(event.target.value)}
        />
      )}
    />
  )
}

export function CapacityPoolDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: ResourcePoolProvider
  pool: CapacityPool | null
  sources: ResourcePoolSource[]
  accounts: ResourcePoolAccount[]
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<FormInput, unknown, CapacityPoolFormValues>({
    resolver: zodResolver(capacityPoolFormSchema),
    defaultValues: capacityPoolFormDefaults(props.pool, props.accounts),
  })
  const planRows = useFieldArray({ control: form.control, name: 'plan_rates' })
  const sourceIds = useWatch({ control: form.control, name: 'source_ids' })
  const groups = useWatch({ control: form.control, name: 'groups' })

  // 只在打开或换了池时重置；账号列表每分钟刷新，不能冲掉正在填的值。
  const loadedKey = useRef('')
  useEffect(() => {
    if (!props.open) {
      loadedKey.current = ''
      return
    }
    const key = String(props.pool?.id ?? 'new')
    if (loadedKey.current === key) return
    loadedKey.current = key
    form.reset(capacityPoolFormDefaults(props.pool, props.accounts))
  }, [props.open, props.pool, props.accounts, form])

  const groupOptions = [
    ...new Set(
      props.accounts
        .filter((account) => sourceIds.includes(account.source_id))
        .map(accountGroup)
        .filter(Boolean)
    ),
  ]
    .sort()
    .map((group) => ({ label: group, value: group }))
  const planCounts = new Map<string, number>()
  for (const account of props.accounts) {
    if (!inCapacityScope(account, sourceIds, groups)) continue
    const plan = capacityAccountPlan(account)
    planCounts.set(plan, (planCounts.get(plan) ?? 0) + 1)
  }

  const save = useMutation({
    mutationFn: (values: CapacityPoolFormValues) =>
      saveCapacityPool({
        id: props.pool?.id ?? 0,
        name: values.name,
        provider: props.provider,
        config: capacityPoolConfigFromForm(values),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: capacityPoolQueryKeys.provider(props.provider),
      })
      toast.success(t('Capacity pool saved'))
      props.onOpenChange(false)
    },
  })

  const rpmHint =
    props.provider === 'claude' ? t('Account rpm_limit') : undefined
  const columns = [
    { key: 'rpm', label: 'RPM' },
    { key: 'tpm', label: 'TPM' },
    { key: 'max_concurrency', label: t('Concurrency') },
  ] as const

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => !save.isPending && props.onOpenChange(open)}
      title={props.pool ? t('Edit capacity pool') : t('Create capacity pool')}
      description={t(
        'Members of a capacity pool share one RPM / TPM / concurrency budget, recomputed every minute from the usable accounts.'
      )}
      contentClassName='sm:max-w-3xl'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={save.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='capacity-pool-form'
            disabled={save.isPending}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='capacity-pool-form'
          className='space-y-5'
          onSubmit={form.handleSubmit((values) => save.mutate(values))}
        >
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel required>{t('Name')}</FormLabel>
                <FormControl>
                  <Input maxLength={128} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='source_ids'
            render={({ field }) => (
              <FormItem>
                <FormLabel required>{t('Sources')}</FormLabel>
                <MultiSelect
                  options={props.sources
                    .filter((source) => source.id > 0)
                    .map((source) => ({
                      label: source.name || source.endpoint,
                      value: String(source.id),
                    }))}
                  selected={field.value.map(String)}
                  onChange={(values) => field.onChange(values.map(Number))}
                  placeholder={t('Select sources')}
                />
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='groups'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream groups')}</FormLabel>
                <MultiSelect
                  options={groupOptions}
                  selected={field.value}
                  onChange={field.onChange}
                  placeholder={t('All groups')}
                />
                <FormDescription>
                  {t(
                    'Leave empty to include every account of the selected sources. Two pools cannot share accounts.'
                  )}
                </FormDescription>
              </FormItem>
            )}
          />

          <div className='space-y-2'>
            <div className='text-sm font-medium'>{t('Per-account rate')}</div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Empty cells of a plan fall back to the default row. An empty cell in the default row leaves that dimension unlimited.'
              )}
            </p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Plan')}</TableHead>
                  <TableHead className='text-right'>{t('Accounts')}</TableHead>
                  {columns.map((column) => (
                    <TableHead key={column.key}>{column.label}</TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell className='font-medium'>{t('Default')}</TableCell>
                  <TableCell />
                  {columns.map((column) => (
                    <TableCell key={column.key}>
                      <NumberCell
                        control={form.control}
                        name={`default_rate.${column.key}`}
                        label={`${t('Default')} ${column.label}`}
                        placeholder={column.key === 'rpm' ? rpmHint : undefined}
                      />
                    </TableCell>
                  ))}
                </TableRow>
                {planRows.fields.map((row, index) => (
                  <TableRow key={row.id}>
                    <TableCell>{row.plan || t('No plan')}</TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {planCounts.get(row.plan) ?? 0}
                    </TableCell>
                    {columns.map((column) => (
                      <TableCell key={column.key}>
                        <NumberCell
                          control={form.control}
                          name={`plan_rates.${index}.${column.key}`}
                          label={`${row.plan || t('No plan')} ${column.label}`}
                          placeholder={t('Default')}
                        />
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className='grid gap-4 sm:grid-cols-3'>
            <FormField
              control={form.control}
              name='threshold_percent'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Exhaustion threshold (%)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      inputMode='decimal'
                      {...field}
                      value={String(field.value ?? '')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='burst_factor'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Burst factor')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      inputMode='decimal'
                      {...field}
                      value={String(field.value ?? '')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='grace_minutes'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Grace period (minutes)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      inputMode='numeric'
                      {...field}
                      value={String(field.value ?? '')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
          <FormField
            control={form.control}
            name='pacing_enabled'
            render={({ field }) => (
              <FormItem className='flex items-start gap-3'>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <div className='space-y-1'>
                  <FormLabel>{t('Pacing')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Also cap RPM / TPM at remaining quota ÷ time to reset × burst factor, so a weekly budget is not used up in two days.'
                    )}
                  </FormDescription>
                </div>
              </FormItem>
            )}
          />

          <div className='space-y-2'>
            <div className='text-sm font-medium'>{t('Floor limit')}</div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Used by the scheduler while no dynamic limit is published, for example after the account pool cannot be polled for the whole grace period.'
              )}
            </p>
            <div className='flex flex-wrap gap-3'>
              {columns.map((column) => (
                <label key={column.key} className='space-y-1 text-xs'>
                  <span className='text-muted-foreground block'>
                    {column.label}
                  </span>
                  <NumberCell
                    control={form.control}
                    name={`floor.${column.key}`}
                    label={`${t('Floor limit')} ${column.label}`}
                  />
                </label>
              ))}
            </div>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}
