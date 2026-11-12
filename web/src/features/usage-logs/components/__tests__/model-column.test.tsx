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
import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import { usageLogSchema, type UsageLog } from '../../data/schema'
import { useCommonLogsColumns } from '../columns/common-logs-columns'
import { UsageLogsProvider } from '../usage-logs-provider'

function ModelCell(props: { log: UsageLog }) {
  const columns = useCommonLogsColumns(true, false)
  const table = useReactTable({
    data: [props.log],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0].getAllCells()
    .find((candidate) => candidate.column.id === 'model_name')
  if (!cell) throw new Error('Model column is missing')

  return (
    <table>
      <tbody>
        <tr>
          <td>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>
        </tr>
      </tbody>
    </table>
  )
}

function renderModelCell(autoRouted: boolean) {
  const log = usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    model_name: 'claude-opus-5-5',
    auto_routed: autoRouted,
  })
  render(
    <UsageLogsProvider>
      <ModelCell log={log} />
    </UsageLogsProvider>
  )
}

it('marks a request that asked for auto next to the model it resolved to', () => {
  renderModelCell(true)

  expect(screen.getByText('claude-opus-5-5')).toBeVisible()
  expect(screen.getByText('auto')).toBeVisible()
})

it('leaves a request that named its model unmarked', () => {
  renderModelCell(false)

  expect(screen.getByText('claude-opus-5-5')).toBeVisible()
  expect(screen.queryByText('auto')).not.toBeInTheDocument()
})
