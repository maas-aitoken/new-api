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
import type { Row } from '@tanstack/react-table'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { TableCell, TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'

/**
 * Chevron that flips a row's expansion state. Pair it with `renderRowDetail`
 * on {@link DataTableView} and `withExpandedRowModel` on `useDataTable`; the
 * detail renderer owns what is shown, so a row without children simply
 * renders nothing when expanded.
 */
export function DataTableExpansionToggle<TData>({
  row,
  className,
}: {
  row: Row<TData>
  className?: string
}) {
  const { t } = useTranslation()
  const expanded = row.getIsExpanded()
  const label = expanded ? t('Collapse') : t('Expand')
  return (
    <Button
      type='button'
      variant='ghost'
      size='icon'
      className={cn('size-6 shrink-0', className)}
      aria-label={label}
      aria-expanded={expanded}
      onClick={() => row.toggleExpanded()}
    >
      {expanded ? (
        <ChevronDown className='size-4' />
      ) : (
        <ChevronRight className='size-4' />
      )}
    </Button>
  )
}

/** Full-width row spanning every visible column, shown below an expanded row. */
export function DataTableDetailRow<TData>({
  row,
  children,
  className,
}: {
  row: Row<TData>
  children: ReactNode
  className?: string
}) {
  if (!row.getIsExpanded()) {
    return null
  }
  return (
    <TableRow className={cn('bg-muted/30 hover:bg-muted/30', className)}>
      <TableCell colSpan={row.getVisibleCells().length} className='p-0'>
        {children}
      </TableCell>
    </TableRow>
  )
}
