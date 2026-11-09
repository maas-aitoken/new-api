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
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Main } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

import { getSchedulerMonitor, type SchedulerMonitor } from '../api'

function Value({
  label,
  value,
}: {
  label: string
  value: string | number | boolean | undefined
}) {
  return (
    <div className='rounded-lg border p-3'>
      <div className='text-muted-foreground text-xs'>{label}</div>
      <div className='mt-1 text-lg font-medium'>{value ?? '-'}</div>
    </div>
  )
}

function formatScore(score: number | undefined) {
  return score === undefined ? '-' : score.toFixed(3)
}

function formatLoad(load: number | undefined) {
  return load === undefined ? '-' : `${(load * 100).toFixed(1)}%`
}

function formatCapacity(
  used: number | undefined,
  capacity: number | undefined
) {
  if (!capacity || capacity <= 0) {
    return used === undefined ? '∞' : `${used} / ∞`
  }
  return `${used ?? 0} / ${capacity}`
}

export function SchedulerMonitorPage() {
  const { t } = useTranslation()
  const [monitor, setMonitor] = useState<SchedulerMonitor | null>(null)
  const [model, setModel] = useState('')
  const [channel, setChannel] = useState('')
  const [endpoint, setEndpoint] = useState('')
  const load = useCallback(
    () =>
      void getSchedulerMonitor({
        model,
        channel_id: channel,
        endpoint_id: endpoint,
      })
        .then((response) => setMonitor(response.data))
        .catch(() => toast.error(t('Failed to load scheduler monitor'))),
    [channel, endpoint, model, t]
  )
  useEffect(() => {
    load()
    const timer = window.setInterval(load, 10000)
    return () => window.clearInterval(timer)
  }, [load])
  const o = monitor?.observability
  const endpoints = o?.endpoints ?? []
  const profileNames = [
    ...new Set(
      endpoints.flatMap((endpoint) => Object.keys(endpoint.profiles ?? {}))
    ),
  ].sort()
  return (
    <Main>
      {/* Main 是 overflow-hidden 的页面框，自己不滚动；端点一多页面就被截断，滚动放在这一层。 */}
      <div className='min-h-0 flex-1 overflow-y-auto'>
        <div className='mx-auto w-full max-w-6xl space-y-4 p-4 sm:p-6'>
          <div className='flex items-center justify-between'>
            <div>
              <h1 className='text-2xl font-semibold'>
                {t('Scheduling Monitor')}
              </h1>
              <p className='text-muted-foreground'>
                {t('Live status from the Scheduler service.')}
              </p>
            </div>
            <Button variant='outline' onClick={load}>
              {t('Refresh')}
            </Button>
          </div>
          <Card>
            <CardHeader>
              <CardTitle>{t('Service status')}</CardTitle>
            </CardHeader>
            <CardContent className='grid gap-3 sm:grid-cols-4'>
              <Value
                label={t('Configured')}
                value={monitor?.configured ? t('Yes') : t('No')}
              />
              <Value
                label={t('Reachable')}
                value={monitor?.reachable ? t('Yes') : t('No')}
              />
              <Value
                label={t('Scheduler status')}
                value={
                  o?.status === 'healthy_idle' ? t('Healthy (idle)') : o?.status
                }
              />
              <Value
                label={t('Checked at')}
                value={
                  monitor?.checked_at
                    ? new Date(monitor.checked_at).toLocaleString()
                    : undefined
                }
              />
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>{t('Endpoint details')}</CardTitle>
            </CardHeader>
            <CardContent>
              <div className='mb-4 flex flex-wrap gap-2'>
                <Input
                  className='w-48'
                  aria-label={t('Model')}
                  placeholder={t('Model')}
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                />
                <Input
                  className='w-48'
                  aria-label={t('Channel ID')}
                  placeholder={t('Channel ID')}
                  value={channel}
                  onChange={(e) => setChannel(e.target.value)}
                />
                <Input
                  className='w-48'
                  aria-label={t('Endpoint')}
                  placeholder={t('Endpoint')}
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </div>
              {endpoints.length === 0 ? (
                <p className='text-muted-foreground text-sm'>
                  {t('No endpoint data')}
                </p>
              ) : (
                <div className='-mx-2 overflow-x-auto px-2'>
                  <table className='w-full min-w-[980px] text-sm'>
                    <thead>
                      <tr className='text-muted-foreground border-b text-left'>
                        <th className='px-2 py-2'>{t('Endpoint')}</th>
                        <th className='px-2 py-2'>{t('Model')}</th>
                        <th className='px-2 py-2'>{t('Load')}</th>
                        <th className='px-2 py-2'>{t('RPM')}</th>
                        <th className='px-2 py-2'>{t('TPM')}</th>
                        {profileNames.map((profile) => (
                          <th className='px-2 py-2' key={profile}>
                            {profile}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {endpoints.map((endpoint) => (
                        <tr
                          className='border-b last:border-0'
                          key={endpoint.endpoint_id}
                        >
                          <td className='px-2 py-3'>
                            <div className='font-medium'>
                              {endpoint.endpoint_id}
                            </div>
                            <div className='text-muted-foreground text-xs'>
                              channel {endpoint.channel_id}
                            </div>
                            {endpoint.capacity_pool_id && (
                              <div className='text-muted-foreground font-mono text-xs'>
                                {t('Pool {{id}}', {
                                  id: endpoint.capacity_pool_id,
                                })}
                              </div>
                            )}
                            {endpoint.pool_exhausted ? (
                              <Badge variant='destructive' className='mt-1'>
                                {t('Exhausted')}
                              </Badge>
                            ) : null}
                            {!endpoint.pool_exhausted &&
                            endpoint.pool_dynamic_limit ? (
                              <Badge variant='secondary' className='mt-1'>
                                {t('Dynamic limit')}
                              </Badge>
                            ) : null}
                          </td>
                          <td className='px-2 py-3'>
                            <div>{endpoint.model ?? '-'}</div>
                            <div className='text-muted-foreground text-xs'>
                              {endpoint.provider ?? '-'}
                            </div>
                          </td>
                          <td className='px-2 py-3'>
                            {formatLoad(endpoint.load_ratio)}
                            {endpoint.capacity_pool_id && (
                              <div className='text-muted-foreground text-xs'>
                                {t('In flight {{value}}', {
                                  value: formatCapacity(
                                    endpoint.pool_inflight ?? 0,
                                    endpoint.max_concurrency
                                  ),
                                })}
                              </div>
                            )}
                          </td>
                          <td className='px-2 py-3'>
                            {/* 额度和用量都是整个计数池的（渠道所有 key 合计）。 */}
                            {formatCapacity(
                              endpoint.pool_rpm_used,
                              endpoint.rpm_capacity
                            )}
                          </td>
                          <td className='px-2 py-3'>
                            {formatCapacity(
                              endpoint.pool_tpm_used,
                              endpoint.tpm_capacity
                            )}
                          </td>
                          {profileNames.map((profile) => {
                            const score = endpoint.profiles?.[profile]
                            return (
                              <td className='px-2 py-3' key={profile}>
                                <div>{formatScore(score?.score)}</div>
                                <div className='text-muted-foreground text-xs'>
                                  tier {score?.gate_tier ?? '-'}
                                </div>
                              </td>
                            )
                          })}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </Main>
  )
}
