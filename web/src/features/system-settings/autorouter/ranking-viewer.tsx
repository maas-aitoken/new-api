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
import { Pencil, Plus, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

import { useAutoRouterRanking, useSaveAutoRouterRanking } from './hooks'
import { RankingAddDialog } from './ranking-add-dialog'
import {
  addModel,
  changedProfiles,
  moveUnscored,
  removeModel,
  setScore,
  toDraft,
  toEdit,
  type RankingDraft,
} from './ranking-draft'
import { RankingTable } from './ranking-table'
import type { CostWeights } from './settings'
import type { AutoRouterRankedModel } from './types'

/** An edit in progress: the ranking as loaded (with its times, for the save's conflict check) and the draft. */
type EditSession = {
  original: RankingDraft
  draft: RankingDraft
  sources: Record<string, string> | null
}

type RankingViewerProps = {
  costWeights: CostWeights
}

/**
 * The ranking the router routes by, one profile at a time; fetched only while
 * open. Editing works on a draft of every profile and saves them together.
 */
export function RankingViewer(props: RankingViewerProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState('')
  const [search, setSearch] = useState('')
  const [servedOnly, setServedOnly] = useState(false)
  const [session, setSession] = useState<EditSession | null>(null)
  const [adding, setAdding] = useState(false)
  const [confirmingSave, setConfirmingSave] = useState(false)
  const ranking = useAutoRouterRanking(open)
  const save = useSaveAutoRouterRanking()

  const data = ranking.data?.data
  const profiles = data?.profiles ?? []
  const profile =
    profiles.find((item) => item.profile === selected) ?? profiles[0]
  let entries = profile?.models ?? []
  if (session && profile) entries = session.draft[profile.profile] ?? []
  const changed = session
    ? changedProfiles(session.original, session.draft)
    : []

  const term = search.trim().toLowerCase()
  const visible = (entry: AutoRouterRankedModel) =>
    (!servedOnly || entry.served) &&
    (term === '' || entry.model.toLowerCase().includes(term))

  const edit = (change: (draft: RankingDraft) => RankingDraft) =>
    setSession((current) =>
      current ? { ...current, draft: change(current.draft) } : current
    )

  const profileLabel = (name: string, count: number) =>
    changed.includes(name)
      ? t('{{profile}} ({{count}}, edited)', { profile: name, count })
      : `${name} (${count})`

  const confirmSave = () => {
    if (!session) return
    save.mutate(toEdit(session.draft, session.sources), {
      onSuccess: () => {
        setConfirmingSave(false)
        setSession(null)
      },
      onError: () => setConfirmingSave(false),
    })
  }

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          aria-expanded={open}
          disabled={session !== null}
          onClick={() => setOpen((current) => !current)}
        >
          {open ? t('Hide ranking') : t('Show ranking')}
        </Button>
        {open && data && !session ? (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => {
              const loaded = toDraft(data.profiles)
              setSession({
                original: loaded,
                draft: loaded,
                sources: data.sources,
              })
            }}
          >
            <Pencil data-icon='inline-start' aria-hidden='true' />
            {t('Edit ranking')}
          </Button>
        ) : null}
      </div>

      {open && ranking.isPending ? <Skeleton className='h-40 w-full' /> : null}
      {open && ranking.isError ? (
        <p className='text-destructive text-sm'>
          {t('Failed to load the ranking')}
        </p>
      ) : null}

      {open && profile ? (
        <div className='space-y-3'>
          <div className='flex flex-wrap items-end gap-3'>
            <div className='w-full max-w-sm space-y-1.5'>
              <Label htmlFor='autorouter-ranking-profile'>
                {t('Task profile')}
              </Label>
              <Select
                items={profiles.map((item) => ({
                  value: item.profile,
                  label: profileLabel(
                    item.profile,
                    (session?.draft[item.profile] ?? item.models).length
                  ),
                }))}
                value={profile.profile}
                onValueChange={(value) => setSelected(value ?? '')}
              >
                <SelectTrigger
                  id='autorouter-ranking-profile'
                  className='w-full'
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {profiles.map((item) => (
                      <SelectItem key={item.profile} value={item.profile}>
                        {profileLabel(
                          item.profile,
                          (session?.draft[item.profile] ?? item.models).length
                        )}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='relative w-full max-w-xs'>
              <Search
                className='text-muted-foreground absolute top-2 left-2.5 size-4'
                aria-hidden='true'
              />
              <Input
                value={search}
                placeholder={t('Search models')}
                aria-label={t('Search models')}
                className='pl-8'
                onChange={(event) => setSearch(event.target.value)}
              />
            </div>
            <div className='flex h-8 items-center gap-2'>
              <Switch
                id='autorouter-ranking-served-only'
                checked={servedOnly}
                onCheckedChange={setServedOnly}
              />
              <Label htmlFor='autorouter-ranking-served-only'>
                {t('Only models on the gateway')}
              </Label>
            </div>
            {session ? (
              <Button
                type='button'
                variant='outline'
                size='sm'
                className='ml-auto'
                onClick={() => setAdding(true)}
              >
                <Plus data-icon='inline-start' aria-hidden='true' />
                {t('Add a model')}
              </Button>
            ) : null}
          </div>

          {session ? (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Models with a score are ordered by score; the router compares them by score and price. Models without one are only a fallback, tried in the order shown. Saving turns periodic refresh off.'
              )}
            </p>
          ) : null}

          <RankingTable
            entries={entries}
            visible={visible}
            emptyText={
              entries.length === 0
                ? t('No models in this profile')
                : t('No models match the filters')
            }
            editing={
              session
                ? {
                    onScore: (model, score) =>
                      edit((draft) =>
                        setScore(draft, profile.profile, model, score)
                      ),
                    onRemove: (model) =>
                      edit((draft) =>
                        removeModel(draft, profile.profile, model)
                      ),
                    onMove: (model, direction) =>
                      edit((draft) =>
                        moveUnscored(
                          draft,
                          profile.profile,
                          model,
                          direction,
                          visible
                        )
                      ),
                  }
                : undefined
            }
          />

          {session ? (
            <div className='flex flex-wrap items-center justify-end gap-2'>
              <span className='text-muted-foreground mr-auto text-xs'>
                {t('{{count}} profiles changed', { count: changed.length })}
              </span>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => setSession(null)}
              >
                {t('Discard changes')}
              </Button>
              <Button
                type='button'
                size='sm'
                disabled={changed.length === 0}
                onClick={() => setConfirmingSave(true)}
              >
                {t('Save ranking')}
              </Button>
            </div>
          ) : null}
        </div>
      ) : null}

      {session && adding ? (
        <RankingAddDialog
          draft={session.draft}
          costWeights={props.costWeights}
          onOpenChange={setAdding}
          onAdd={(model, served, scores) =>
            edit((draft) => addModel(draft, model, served, scores))
          }
        />
      ) : null}
      <ConfirmDialog
        open={confirmingSave}
        onOpenChange={setConfirmingSave}
        title={t('Save the edited ranking?')}
        desc={t(
          'Saving turns periodic refresh off, so a scheduled refresh does not overwrite your edits. Refreshing now or restoring the built-in ranking later still replaces them.'
        )}
        confirmText={t('Save ranking')}
        isLoading={save.isPending}
        handleConfirm={confirmSave}
      />
    </div>
  )
}
