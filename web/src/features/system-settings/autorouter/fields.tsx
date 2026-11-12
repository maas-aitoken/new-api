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
import type { Control, FieldPath, FieldValues } from 'react-hook-form'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { safeNumberFieldProps } from '../utils/numeric-field'

type SettingFieldProps<TFieldValues extends FieldValues> = {
  control: Control<TFieldValues>
  name: FieldPath<TFieldValues>
  label: string
  description?: string
}

type NumberSettingFieldProps<TFieldValues extends FieldValues> =
  SettingFieldProps<TFieldValues> & {
    min?: number
    max?: number
    step?: number
  }

type SelectSettingFieldProps<TFieldValues extends FieldValues> =
  SettingFieldProps<TFieldValues> & {
    options: ReadonlyArray<{ value: string; label: string }>
  }

/** A numeric option input; the value stays a finite number or the previous one. */
export function NumberSettingField<TFieldValues extends FieldValues>(
  props: NumberSettingFieldProps<TFieldValues>
) {
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={props.min}
              max={props.max}
              step={props.step}
              {...safeNumberFieldProps(field)}
            />
          </FormControl>
          {props.description ? (
            <FormDescription>{props.description}</FormDescription>
          ) : null}
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

/** A choice among a few fixed values, shown with their labels. */
export function SelectSettingField<TFieldValues extends FieldValues>(
  props: SelectSettingFieldProps<TFieldValues>
) {
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <Select
            items={props.options}
            value={field.value}
            onValueChange={field.onChange}
          >
            <FormControl>
              <SelectTrigger className='w-full'>
                <SelectValue />
              </SelectTrigger>
            </FormControl>
            <SelectContent>
              {props.options.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {props.description ? (
            <FormDescription>{props.description}</FormDescription>
          ) : null}
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
