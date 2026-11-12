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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import type { StaticDataTableColumn } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'

import {
  judgmentLabel,
  judgmentTone,
  outcomeLabel,
  outcomeTone,
  preferenceLabel,
} from './labels'
import { ToneBadge } from './route-process-parts'
import type { RouteLogItem } from './types'

/** The columns of the routing log list; onView opens the process of one request. */
export function useRouteLogColumns(
  onView: (requestId: string) => void
): StaticDataTableColumn<RouteLogItem>[] {
  const { t } = useTranslation()

  return [
    {
      id: 'time',
      header: t('Time'),
      cellClassName: 'text-xs whitespace-nowrap',
      cell: (row) => formatTimestampToDate(row.created_at),
    },
    {
      id: 'result',
      header: t('Result'),
      cell: (row) => (
        <ToneBadge
          tone={outcomeTone(row.outcome)}
          label={outcomeLabel(t, row.outcome)}
        />
      ),
    },
    {
      id: 'model',
      header: t('Model'),
      cellClassName: 'font-mono text-xs',
      cell: (row) => row.model || '-',
    },
    {
      id: 'effort',
      header: t('Effort'),
      cellClassName: 'text-xs',
      cell: (row) => row.effort || '-',
    },
    {
      id: 'task-type',
      header: t('Task type'),
      cellClassName: 'font-mono text-xs',
      cell: (row) => row.task_type || '-',
    },
    {
      id: 'judgment',
      header: t('Judgment'),
      cell: (row) => (
        <ToneBadge
          tone={judgmentTone(row.judgment)}
          label={judgmentLabel(t, row.judgment)}
        />
      ),
    },
    {
      id: 'preference',
      header: t('Preference'),
      cellClassName: 'text-xs',
      cell: (row) => (row.preference ? preferenceLabel(t, row.preference) : '-'),
    },
    {
      id: 'request',
      header: t('Request ID'),
      className: 'min-w-[240px]',
      cell: (row) => (
        <div className='space-y-0.5'>
          <div className='flex items-center gap-1'>
            <span className='font-mono text-xs break-all'>
              {row.request_id || '-'}
            </span>
            {row.request_id && (
              <CopyButton
                value={row.request_id}
                size='icon'
                className='size-7'
                tooltip={t('Copy request ID')}
                aria-label={t('Copy request ID')}
              />
            )}
          </div>
          {row.user_id > 0 && (
            <p className='text-muted-foreground text-xs'>
              {t('User')} {row.user_id}
            </p>
          )}
        </div>
      ),
    },
    {
      id: 'actions',
      header: <span className='sr-only'>{t('Actions')}</span>,
      cell: (row) => (
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={!row.request_id}
          onClick={() => onView(row.request_id)}
        >
          {t('View process')}
        </Button>
      ),
    },
  ]
}
