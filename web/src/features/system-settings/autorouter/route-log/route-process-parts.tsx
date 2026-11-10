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
import type { LucideIcon } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'

import { ChainOfThoughtStep } from '@/components/ai-elements/chain-of-thought'
import { StatusBadge } from '@/components/status-badge'

import type { RouteTone } from './labels'

export type FactRow = [label: string, value: ReactNode]

export function ToneBadge(props: { tone: RouteTone; label: string }) {
  return (
    <StatusBadge
      label={props.label}
      variant={props.tone}
      size='sm'
      copyable={false}
    />
  )
}

/** One numbered stop of the routing process. */
export function StepFrame(props: {
  icon: LucideIcon
  title: string
  children: ReactNode
}) {
  return (
    <ChainOfThoughtStep
      icon={props.icon}
      label={props.title}
      status='active'
      className='font-medium'
    >
      <div className='space-y-2 font-normal'>{props.children}</div>
    </ChainOfThoughtStep>
  )
}

export function Facts(props: { rows: FactRow[] }) {
  return (
    <dl className='grid grid-cols-[7.5rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs'>
      {props.rows.map(([label, value]) => (
        <Fragment key={label}>
          <dt className='text-muted-foreground'>{label}</dt>
          <dd className='min-w-0 break-words'>{value}</dd>
        </Fragment>
      ))}
    </dl>
  )
}

/** Small monospace tags, such as the probability of each task type. */
export function Chips(props: { items: string[] }) {
  return (
    <div className='flex flex-wrap gap-1'>
      {props.items.map((item) => (
        <span
          key={item}
          className='bg-muted rounded px-1.5 py-0.5 font-mono text-[11px]'
        >
          {item}
        </span>
      ))}
    </div>
  )
}
