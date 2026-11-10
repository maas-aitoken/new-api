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
import { Waypoints } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  preferenceLabel,
  routeSourceLabel,
} from '@/features/system-settings/autorouter/route-log/labels'
import { RouteProcessDrawer } from '@/features/system-settings/autorouter/route-log/route-process-drawer'
import type { RouteResult } from '@/features/system-settings/autorouter/route-log/types'

import { DetailRow, DetailSection } from './log-detail-layout'

type AutoRouteDetailProps = {
  /** The request the log belongs to; the routing log is looked up by it. */
  requestId: string
  /** The summary kept with the usage log, which the drawer falls back to. */
  summary: RouteResult
}

/** How an "auto" request was routed, with a way into the full routing process. */
export function AutoRouteDetail(props: AutoRouteDetailProps) {
  const { t } = useTranslation()
  const [viewing, setViewing] = useState('')
  const summary = props.summary

  return (
    <DetailSection
      label={t('Auto routing')}
      icon={<Waypoints className='size-4' aria-hidden='true' />}
    >
      <DetailRow
        label={t('Routed to')}
        value={summary.resolved_model || summary.model || '-'}
        mono
      />
      {summary.decision?.task_type && (
        <DetailRow label={t('Task type')} value={summary.decision.task_type} mono />
      )}
      {summary.source && (
        <DetailRow
          label={t('Route source')}
          value={routeSourceLabel(t, summary.source)}
        />
      )}
      {summary.preference && (
        <DetailRow
          label={t('Preference')}
          value={preferenceLabel(t, summary.preference)}
        />
      )}
      {props.requestId && (
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='mt-1'
          onClick={() => setViewing(props.requestId)}
        >
          {t('View routing process')}
        </Button>
      )}
      <RouteProcessDrawer
        requestId={viewing}
        summary={summary}
        onClose={() => setViewing('')}
      />
    </DetailSection>
  )
}
