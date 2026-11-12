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
import { useState } from 'react'
import type { Control, FieldPath, FieldValues } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'

import { useSaveAutoRouterOptions } from './hooks'
import { OPTION_PREFIX } from './settings'
import type {
  AutoRouterConnectionKey,
  AutoRouterConnectionSource,
} from './types'

type SecretFieldProps<TFieldValues extends FieldValues> = {
  control: Control<TFieldValues>
  name: FieldPath<TFieldValues>
  /** The option this key is stored under, without the prefix. */
  connection: AutoRouterConnectionKey
  label: string
  /** The environment variable the router falls back to. */
  envName: string
  /** Where the key comes from now; undefined while the status has not loaded. */
  source?: AutoRouterConnectionSource
}

/**
 * A write-only key: the server never returns its value, so the input starts empty,
 * a blank input keeps what is saved, and a saved key can only be cleared on purpose.
 */
export function SecretField<TFieldValues extends FieldValues>(
  props: SecretFieldProps<TFieldValues>
) {
  const { t } = useTranslation()
  const { save, saving } = useSaveAutoRouterOptions()
  const [confirmingClear, setConfirmingClear] = useState(false)

  let description = t('Leave blank to keep the existing key')
  if (props.source === 'env') {
    description = t(
      'Using the {{name}} environment variable. A value entered here overrides it.',
      { name: props.envName }
    )
  } else if (props.source === '') {
    description = t(
      'Not set. Enter a value here, or set the {{name}} environment variable.',
      { name: props.envName }
    )
  }

  const clear = async () => {
    const cleared = await save([
      { key: OPTION_PREFIX + props.connection, value: '' },
    ])
    if (cleared) setConfirmingClear(false)
  }

  return (
    <>
      <FormField
        control={props.control}
        name={props.name}
        render={({ field }) => (
          <FormItem>
            <FormLabel>{props.label}</FormLabel>
            <div className='flex items-start gap-2'>
              <FormControl>
                <PasswordInput
                  {...field}
                  className='min-w-0 flex-1'
                  autoComplete='new-password'
                  disabled={saving}
                  placeholder={
                    props.source === 'option'
                      ? t('Enter new token to update')
                      : ''
                  }
                />
              </FormControl>
              {props.source === 'option' ? (
                <Button
                  type='button'
                  variant='outline'
                  disabled={saving}
                  onClick={() => setConfirmingClear(true)}
                >
                  {t('Clear saved key')}
                </Button>
              ) : null}
            </div>
            <FormDescription>{description}</FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <ConfirmDialog
        open={confirmingClear}
        onOpenChange={setConfirmingClear}
        title={t('Clear the saved key?')}
        desc={t(
          'The saved key is deleted. If the {{name}} environment variable is set it is used again, otherwise this feature stops working until a key is entered.',
          { name: props.envName }
        )}
        confirmText={t('Clear')}
        destructive
        isLoading={saving}
        handleConfirm={() => void clear()}
      />
    </>
  )
}
