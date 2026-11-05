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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useDebounce } from '@/hooks/use-debounce'
import { requireServerSuccess } from '@/lib/server-error-message'

import { searchChannels } from '../../channels/api'
import { addCapacityPoolMembers, type CapacityPool } from '../api'
import { capacityPoolQueryKeys } from './capacity-pool-query-keys'

/** 往容量池里加一个渠道的若干模型；不勾模型时加整条渠道。 */
export function CapacityPoolMemberDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  pool: CapacityPool
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const searchId = useId()
  const channelId = useId()
  const [keyword, setKeyword] = useState('')
  const [selectedChannel, setSelectedChannel] = useState<number | null>(null)
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  const debouncedKeyword = useDebounce(keyword.trim(), 300)

  useEffect(() => {
    if (!props.open) {
      setKeyword('')
      setSelectedChannel(null)
      setSelectedModels([])
    }
  }, [props.open])

  const channels = useQuery({
    queryKey: ['resource-pools', 'member-channels', debouncedKeyword],
    queryFn: async () =>
      requireServerSuccess(await searchChannels({ keyword: debouncedKeyword }))
        .data?.items ?? [],
    enabled: props.open,
  })
  const channel = channels.data?.find((item) => item.id === selectedChannel)
  const models = (channel?.models ?? '')
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean)

  const add = useMutation({
    mutationFn: (modelsToAdd: string[]) =>
      addCapacityPoolMembers(props.pool.id, selectedChannel ?? 0, modelsToAdd),
    onSuccess: async (added) => {
      await queryClient.invalidateQueries({
        queryKey: capacityPoolQueryKeys.provider(props.pool.provider),
      })
      if (added.length === 0) {
        toast.info(t('These models are already in this pool'))
      } else {
        toast.success(
          t('Added {{count}} upstream models', { count: added.length })
        )
      }
      props.onOpenChange(false)
    },
  })

  const toggleModel = (name: string, checked: boolean) =>
    setSelectedModels((current) =>
      checked ? [...current, name] : current.filter((item) => item !== name)
    )

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => !add.isPending && props.onOpenChange(open)}
      title={t('Add members to {{name}}', { name: props.pool.name })}
      description={t(
        'Models are bound by the upstream name they map to, so aliases of one upstream model join together.'
      )}
      footer={
        <>
          <Button
            variant='outline'
            disabled={!channel || add.isPending}
            onClick={() => add.mutate([])}
          >
            {t('Add all models')}
          </Button>
          <Button
            disabled={!channel || selectedModels.length === 0 || add.isPending}
            onClick={() => add.mutate(selectedModels)}
          >
            {t('Add selected')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='space-y-1.5'>
          <Label htmlFor={searchId}>{t('Search channels')}</Label>
          <Input
            id={searchId}
            value={keyword}
            placeholder={t('Channel name, ID or base URL')}
            onChange={(event) => setKeyword(event.target.value)}
          />
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor={channelId}>{t('Channel')}</Label>
          <NativeSelect
            id={channelId}
            value={selectedChannel === null ? '' : String(selectedChannel)}
            onChange={(event) => {
              setSelectedChannel(
                event.target.value ? Number(event.target.value) : null
              )
              setSelectedModels([])
            }}
          >
            <NativeSelectOption value=''>
              {t('Select a channel')}
            </NativeSelectOption>
            {(channels.data ?? []).map((item) => (
              <NativeSelectOption key={item.id} value={String(item.id)}>
                #{item.id} {item.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        {channel && (
          <fieldset className='space-y-2'>
            <legend className='text-sm font-medium'>{t('Models')}</legend>
            {models.length === 0 ? (
              <p className='text-muted-foreground text-sm'>
                {t('This channel has no models')}
              </p>
            ) : (
              <div className='grid max-h-64 gap-2 overflow-y-auto sm:grid-cols-2'>
                {models.map((name) => (
                  <label
                    key={name}
                    className='flex min-w-0 items-center gap-2 text-sm'
                  >
                    <Checkbox
                      checked={selectedModels.includes(name)}
                      onCheckedChange={(checked) =>
                        toggleModel(name, Boolean(checked))
                      }
                    />
                    <span className='truncate' title={name}>
                      {name}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </fieldset>
        )}
      </div>
    </Dialog>
  )
}
