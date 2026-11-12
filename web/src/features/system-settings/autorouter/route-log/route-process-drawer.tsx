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
import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { formatTimestampToDate } from '@/lib/format'

import { outcomeLabel, outcomeTone } from './labels'
import { useRouteLog } from './hooks'
import { RouteProcess } from './route-process'
import { Facts, ToneBadge } from './route-process-parts'
import type { RouteLog, RouteResult } from './types'

type RouteProcessDrawerProps = {
  /** The request to explain; the drawer is closed while it is empty. */
  requestId: string
  /** The summary the usage log keeps, shown when no detailed log was recorded. */
  summary?: RouteResult
  onClose: () => void
}

function LogHeader(props: { log: RouteLog }) {
  const { t } = useTranslation()
  const log = props.log
  return (
    <div className='space-y-2'>
      <ToneBadge
        tone={outcomeTone(log.outcome)}
        label={outcomeLabel(t, log.outcome)}
      />
      <Facts
        rows={[
          [t('Time'), formatTimestampToDate(log.created_at)],
          [t('User'), log.user_id || '-'],
          [t('Token'), log.token_id || '-'],
          [t('Group'), log.using_group || '-'],
        ]}
      />
    </div>
  )
}

function DrawerBody(props: RouteProcessDrawerProps) {
  const { t } = useTranslation()
  const query = useRouteLog(props.requestId, true)

  if (query.isPending) {
    return (
      <div className='space-y-3'>
        {['first', 'second', 'third'].map((key) => (
          <Skeleton key={key} className='h-16 w-full' />
        ))}
      </div>
    )
  }
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load the routing process')}
        description={query.error.message}
        onRetry={() => void query.refetch()}
        className='min-h-40'
      />
    )
  }
  if (query.data) {
    return (
      <>
        <LogHeader log={query.data} />
        <RouteProcess detail={query.data.detail ?? {}} />
      </>
    )
  }
  if (props.summary) {
    return (
      <>
        <Alert>
          <Info />
          <AlertDescription>
            {t(
              'No detailed routing log was recorded for this request. Only the summary kept with the usage log is shown.'
            )}
          </AlertDescription>
        </Alert>
        <RouteProcess detail={{ result: props.summary }} />
      </>
    )
  }
  return (
    <EmptyState
      title={t('No routing log was recorded for this request')}
      description={t(
        'Logging may be off, its mode may not cover this kind of request, or the log may have passed its retention period.'
      )}
      className='min-h-40'
    />
  )
}

/** The routing process of one request, opened from the routing log or a usage log. */
export function RouteProcessDrawer(props: RouteProcessDrawerProps) {
  const { t } = useTranslation()
  const open = props.requestId !== ''

  return (
    <Sheet open={open} onOpenChange={(next) => !next && props.onClose()}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-2xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Routing process')}</SheetTitle>
          <SheetDescription className='font-mono break-all'>
            {props.requestId}
          </SheetDescription>
        </SheetHeader>
        <div className={sideDrawerFormClassName()}>
          {open && <DrawerBody {...props} />}
        </div>
      </SheetContent>
    </Sheet>
  )
}
