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
import { useSearch } from '@tanstack/react-router'
import { useMemo, useCallback, useState } from 'react'

import { useDebounce } from '@/hooks/use-debounce'

import {
  FILTER_ALL,
  SORT_OPTIONS,
  QUOTA_TYPES,
  ENDPOINT_TYPES,
  DEFAULT_TOKEN_UNIT,
  VIEW_MODES,
  type ViewMode,
} from '../constants'
import {
  filterAndSortModels,
  extractAllTags,
  filterByAdvanced,
  hasValues,
  type PricingAdvancedFilters,
} from '../lib/filters'
import type { PricingModel, TokenUnit } from '../types'

type FilterState = {
  search?: string
  sort?: string
  vendor?: string
  group?: string
  quotaType?: string
  endpointType?: string
  tag?: string
  tokenUnit?: TokenUnit
  view?: ViewMode
  rechargePrice?: boolean
  contextLength?: string
  parameterCount?: string
  releaseDate?: string
}

function normalizeViewMode(value: unknown): ViewMode {
  if (value === VIEW_MODES.TABLE) {
    return VIEW_MODES.TABLE
  }
  return VIEW_MODES.CARD
}

export function useFilters(models: PricingModel[]) {
  const search = useSearch({ strict: false }) as FilterState
  const [filterState, setFilterState] = useState<FilterState>(() => ({
    search: search.search,
    sort: search.sort,
    vendor: search.vendor,
    group: search.group,
    quotaType: search.quotaType,
    endpointType: search.endpointType,
    tag: search.tag,
    tokenUnit: search.tokenUnit,
    view: search.view,
    rechargePrice: search.rechargePrice,
    contextLength: search.contextLength,
    parameterCount: search.parameterCount,
    releaseDate: search.releaseDate,
  }))

  const searchInput = filterState.search || ''
  const debouncedSearchInput = useDebounce(searchInput, 200)
  const sortBy = filterState.sort || SORT_OPTIONS.NAME
  const vendorFilter = filterState.vendor || FILTER_ALL
  const groupFilter = filterState.group || FILTER_ALL
  const quotaTypeFilter = filterState.quotaType || QUOTA_TYPES.ALL
  const endpointTypeFilter = filterState.endpointType || ENDPOINT_TYPES.ALL
  const tagFilter = filterState.tag || FILTER_ALL
  const tokenUnit: TokenUnit =
    filterState.tokenUnit === 'K' ? 'K' : DEFAULT_TOKEN_UNIT
  const viewMode = normalizeViewMode(filterState.view)
  const showRechargePrice = filterState.rechargePrice === true
  const advancedFilters = useMemo<PricingAdvancedFilters>(
    () => ({
      contextLength: filterState.contextLength || FILTER_ALL,
      parameterCount: filterState.parameterCount || FILTER_ALL,
      releaseDate: filterState.releaseDate || FILTER_ALL,
    }),
    [filterState]
  )

  const updateFilters = useCallback((updates: Record<string, unknown>) => {
    setFilterState((prev) => {
      const next: Record<string, unknown> = { ...prev, ...updates }
      for (const key of Object.keys(next)) {
        if (next[key] === undefined || next[key] === null) {
          delete next[key]
        }
      }
      return next as FilterState
    })
  }, [])

  const setSearchInput = useCallback(
    (v: string) => updateFilters({ search: v || undefined }),
    [updateFilters]
  )
  const setSortBy = useCallback(
    (v: string) =>
      updateFilters({ sort: v === SORT_OPTIONS.NAME ? undefined : v }),
    [updateFilters]
  )
  const setVendorFilter = useCallback(
    (v: string) => updateFilters({ vendor: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setGroupFilter = useCallback(
    (v: string) => updateFilters({ group: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setQuotaTypeFilter = useCallback(
    (v: string) =>
      updateFilters({ quotaType: v === QUOTA_TYPES.ALL ? undefined : v }),
    [updateFilters]
  )
  const setEndpointTypeFilter = useCallback(
    (v: string) =>
      updateFilters({
        endpointType: v === ENDPOINT_TYPES.ALL ? undefined : v,
      }),
    [updateFilters]
  )
  const setTagFilter = useCallback(
    (v: string) => updateFilters({ tag: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setTokenUnit = useCallback(
    (v: TokenUnit) =>
      updateFilters({ tokenUnit: v === DEFAULT_TOKEN_UNIT ? undefined : v }),
    [updateFilters]
  )
  const setViewMode = useCallback(
    (v: ViewMode) =>
      updateFilters({ view: v === VIEW_MODES.CARD ? undefined : v }),
    [updateFilters]
  )
  const setShowRechargePrice = useCallback(
    (v: boolean) => updateFilters({ rechargePrice: v || undefined }),
    [updateFilters]
  )
  const setAdvancedFilter = useCallback(
    (key: keyof PricingAdvancedFilters, value: string) =>
      updateFilters({ [key]: value === FILTER_ALL ? undefined : value }),
    [updateFilters]
  )

  const availableTags = useMemo(() => {
    if (!models || models.length === 0) return []
    return extractAllTags(models)
  }, [models])

  const filteredModels = useMemo(() => {
    if (!models || models.length === 0) return []

    const base = filterAndSortModels(models, {
      search: debouncedSearchInput,
      vendor: vendorFilter,
      group: groupFilter,
      quotaType: quotaTypeFilter,
      endpointType: endpointTypeFilter,
      tag: tagFilter,
      sortBy,
    })
    return filterByAdvanced(base, advancedFilters)
  }, [
    models,
    debouncedSearchInput,
    vendorFilter,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tagFilter,
    sortBy,
    advancedFilters,
  ])

  const hasActiveFilters = useMemo(
    () =>
      vendorFilter !== FILTER_ALL ||
      groupFilter !== FILTER_ALL ||
      quotaTypeFilter !== QUOTA_TYPES.ALL ||
      endpointTypeFilter !== ENDPOINT_TYPES.ALL ||
      tagFilter !== FILTER_ALL ||
      Object.values(advancedFilters).some((value) => value !== FILTER_ALL),
    [
      vendorFilter,
      groupFilter,
      quotaTypeFilter,
      endpointTypeFilter,
      tagFilter,
      advancedFilters,
    ]
  )

  const activeFilterCount = useMemo(
    () =>
      [
        vendorFilter !== FILTER_ALL,
        groupFilter !== FILTER_ALL,
        quotaTypeFilter !== QUOTA_TYPES.ALL,
        endpointTypeFilter !== ENDPOINT_TYPES.ALL,
        tagFilter !== FILTER_ALL,
        ...Object.values(advancedFilters).map((value) => value !== FILTER_ALL),
      ].filter(Boolean).length,
    [
      vendorFilter,
      groupFilter,
      quotaTypeFilter,
      endpointTypeFilter,
      tagFilter,
      advancedFilters,
    ]
  )

  const clearFilters = useCallback(() => {
    updateFilters({
      vendor: undefined,
      group: undefined,
      quotaType: undefined,
      endpointType: undefined,
      tag: undefined,
      contextLength: undefined,
      parameterCount: undefined,
      releaseDate: undefined,
    })
  }, [updateFilters])

  const clearSearch = useCallback(() => {
    updateFilters({ search: undefined })
  }, [updateFilters])

  return {
    searchInput,
    sortBy,
    vendorFilter,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tagFilter,
    tokenUnit,
    viewMode,
    showRechargePrice,
    setSearchInput,
    setSortBy,
    setVendorFilter,
    setGroupFilter,
    setQuotaTypeFilter,
    setEndpointTypeFilter,
    setTagFilter,
    setTokenUnit,
    setViewMode,
    setShowRechargePrice,
    filteredModels,
    hasActiveFilters,
    activeFilterCount,
    availableTags,
    advancedFilters,
    advancedOptions: {
      hasContextLength: hasValues(models, (model) => model.context_length),
      hasParameterCount: hasValues(models, (model) => model.parameter_count),
      hasReleaseDate: hasValues(models, (model) => model.release_date),
    },
    setAdvancedFilter,
    clearFilters,
    clearSearch,
  }
}
