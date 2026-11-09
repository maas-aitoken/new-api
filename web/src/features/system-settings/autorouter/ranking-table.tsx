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
import { ArrowDown, ArrowUp, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { moveTarget, parseScore } from './ranking-draft'
import type { AutoRouterRankedModel } from './types'

type ScoreInputProps = {
  model: string
  score: number | null
  onCommit: (score: number | null) => void
}

/**
 * A score cell. The value is committed on blur or Enter, not on every keystroke,
 * so the row does not jump to its new place while the admin is still typing.
 * An invalid value goes back to the saved score.
 */
function ScoreInput(props: ScoreInputProps) {
  const { t } = useTranslation()
  const [text, setText] = useState(
    props.score === null ? '' : String(props.score)
  )
  const parsed = parseScore(text)

  const commit = () => {
    if (parsed === undefined) {
      toast.error(t('Enter a score from 0 to 1, or leave it empty'))
      setText(props.score === null ? '' : String(props.score))
      return
    }
    if (parsed !== props.score) props.onCommit(parsed)
  }

  return (
    <Input
      inputMode='decimal'
      value={text}
      placeholder={t('Fallback only')}
      aria-label={t('Score of {{model}}', { model: props.model })}
      aria-invalid={parsed === undefined}
      className='h-7 w-24 text-right font-mono tabular-nums'
      onChange={(event) => setText(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') event.currentTarget.blur()
      }}
    />
  )
}

type RankingTableProps = {
  /** The profile's full list, in saved order. */
  entries: AutoRouterRankedModel[]
  /** Which rows the search and the gateway filter show. */
  visible: (entry: AutoRouterRankedModel) => boolean
  emptyText: string
  /** Present while editing: the table shows inputs and row actions. */
  editing?: {
    onScore: (model: string, score: number | null) => void
    onRemove: (model: string) => void
    onMove: (model: string, direction: 'up' | 'down') => void
  }
}

/** One profile's ranking. The position is the place in the whole list, filters or not. */
export function RankingTable(props: RankingTableProps) {
  const { t } = useTranslation()
  const editing = props.editing
  const rows = props.entries.filter(props.visible)

  const columns: StaticDataTableColumn<AutoRouterRankedModel>[] = [
    {
      id: 'rank',
      header: t('Rank'),
      className: 'w-16',
      cellClassName: 'text-muted-foreground tabular-nums',
      cell: (row) => props.entries.indexOf(row) + 1,
    },
    {
      id: 'model',
      header: t('Model'),
      cell: (row) => (
        <div className='flex min-w-0 items-center gap-2'>
          <span className='truncate font-mono text-xs'>{row.model}</span>
          {row.served ? (
            <StatusBadge
              label={t('On the gateway')}
              variant='success'
              copyable={false}
            />
          ) : null}
        </div>
      ),
    },
    {
      id: 'score',
      header: t('Ranking score'),
      className: 'w-28 text-right',
      cellClassName: 'text-right font-mono tabular-nums',
      cell: (row) => {
        if (editing) {
          return (
            <div className='flex justify-end'>
              <ScoreInput
                key={`${row.model}:${row.score}`}
                model={row.model}
                score={row.score}
                onCommit={(score) => editing.onScore(row.model, score)}
              />
            </div>
          )
        }
        return row.score === null ? '-' : row.score.toFixed(4)
      },
    },
  ]
  if (editing) {
    columns.push({
      id: 'actions',
      header: <span className='sr-only'>{t('Actions')}</span>,
      className: 'w-28',
      cellClassName: 'text-right',
      cell: (row) => (
        <div className='flex justify-end gap-1'>
          {row.score === null ? (
            <>
              <Button
                type='button'
                variant='ghost'
                size='icon-sm'
                aria-label={t('Move {{model}} up', { model: row.model })}
                disabled={
                  moveTarget(props.entries, row.model, 'up', props.visible) < 0
                }
                onClick={() => editing.onMove(row.model, 'up')}
              >
                <ArrowUp aria-hidden='true' />
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='icon-sm'
                aria-label={t('Move {{model}} down', { model: row.model })}
                disabled={
                  moveTarget(props.entries, row.model, 'down', props.visible) <
                  0
                }
                onClick={() => editing.onMove(row.model, 'down')}
              >
                <ArrowDown aria-hidden='true' />
              </Button>
            </>
          ) : null}
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            aria-label={t('Remove {{model}} from this profile', {
              model: row.model,
            })}
            onClick={() => editing.onRemove(row.model)}
          >
            <Trash2 className='text-destructive' aria-hidden='true' />
          </Button>
        </div>
      ),
    })
  }

  return (
    <div className='max-h-96 overflow-auto rounded-md'>
      <StaticDataTable
        columns={columns}
        data={rows}
        getRowKey={(row) => row.model}
        emptyContent={props.emptyText}
      />
    </div>
  )
}
