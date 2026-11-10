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
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { Model } from '../types'
import { useModels } from './models-provider'

/**
 * 展开一条前缀/后缀/包含规则时列出的具体模型。这些名字由渠道声明、没有自己的
 * 元数据行，点击即可为它单独建一条精确元数据。
 */
export function ModelMatchedModels({ model }: { model: Model }) {
  const { t } = useTranslation()
  const { setCurrentRow, setOpen } = useModels()
  const matched = model.matched_models ?? []
  if (model.name_rule === 0 || matched.length === 0) {
    return null
  }
  return (
    <div className='space-y-2 px-4 py-3'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'These models match this rule and have no metadata row of their own.'
        )}
      </p>
      <ul className='flex flex-wrap gap-1.5'>
        {matched.map((name) => (
          <li key={name}>
            <Button
              variant='outline'
              size='sm'
              className='h-7 gap-1.5 font-mono text-xs'
              title={t('Add metadata')}
              onClick={() => {
                setCurrentRow({
                  id: 0,
                  model_name: name,
                  status: 1,
                  sync_official: 1,
                  name_rule: 0,
                  created_time: 0,
                  updated_time: 0,
                })
                setOpen('update-model')
              }}
            >
              {name}
              <Plus className='size-3.5' />
            </Button>
          </li>
        ))}
      </ul>
    </div>
  )
}
