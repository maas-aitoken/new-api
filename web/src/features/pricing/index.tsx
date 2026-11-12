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
import { useNavigate } from '@tanstack/react-router'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'
import { cn } from '@/lib/utils'

import {
  LoadingSkeleton,
  EmptyState,
  PricingTable,
  PricingSidebar,
  PricingToolbar,
  ModelCardGrid,
  ModelDetailsDrawer,
} from './components'
import { EXCLUDED_GROUPS, VIEW_MODES } from './constants'
import { useFilters } from './hooks/use-filters'
import { usePricingData } from './hooks/use-pricing-data'

type PricingContentProps = {
  embedded?: boolean
  modelSquare?: boolean
}

export function Pricing() {
  return (
    <PublicLayout showMainContainer={false}>
      <PricingContent />
    </PublicLayout>
  )
}

export function PricingContent({
  embedded = false,
  modelSquare = false,
}: PricingContentProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [selectedModelName, setSelectedModelName] = useState<string | null>(
    null
  )
  const [filtersOpen, setFiltersOpen] = useState(modelSquare)

  const {
    models,
    vendors,
    groupRatio,
    usableGroup,
    endpointMap,
    autoGroups,
    isLoading,
    priceRate,
    usdExchangeRate,
  } = usePricingData({ modelSquare })

  const {
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
    clearFilters,
    clearSearch,
    advancedFilters,
    advancedOptions,
    setAdvancedFilter,
  } = useFilters(models || [])

  const handleModelClick = useCallback(
    (modelName: string) => {
      if (modelSquare) {
        const model = (models || []).find(
          (item) => item.model_name === modelName
        )
        const catalogKey = model?.catalog_key || String(model?.id ?? '')
        if (catalogKey) {
          navigate({
            to: '/model-square/$modelId',
            params: { modelId: catalogKey },
          })
        }
        return
      }
      setSelectedModelName(modelName)
    },
    [modelSquare, models, navigate]
  )

  const selectedModel = useMemo(
    () =>
      selectedModelName
        ? (models || []).find(
            (model) => model.model_name === selectedModelName
          ) || null
        : null,
    [models, selectedModelName]
  )

  const availableGroups = useMemo(
    () =>
      Object.keys(usableGroup || {}).filter(
        (g) => !EXCLUDED_GROUPS.includes(g)
      ),
    [usableGroup]
  )

  const handleClearAll = useCallback(() => {
    clearFilters()
    clearSearch()
  }, [clearFilters, clearSearch])

  const renderPricingContent = () => {
    if (filteredModels.length === 0) {
      return (
        <EmptyState
          searchQuery={searchInput}
          hasActiveFilters={hasActiveFilters}
          onClearFilters={handleClearAll}
        />
      )
    }

    if (viewMode === VIEW_MODES.CARD) {
      return (
        <ModelCardGrid
          models={filteredModels}
          onModelClick={handleModelClick}
          priceRate={priceRate}
          usdExchangeRate={usdExchangeRate}
          tokenUnit={tokenUnit}
          showRechargePrice={showRechargePrice}
          selectedGroup={groupFilter}
        />
      )
    }

    return (
      <PricingTable
        models={filteredModels}
        priceRate={priceRate}
        usdExchangeRate={usdExchangeRate}
        tokenUnit={tokenUnit}
        showRechargePrice={showRechargePrice}
        selectedGroup={groupFilter}
        onModelClick={handleModelClick}
      />
    )
  }

  if (isLoading) {
    let loadingClassName =
      'mx-auto w-full max-w-[1800px] px-3 pt-16 pb-8 sm:px-6 sm:pt-20 sm:pb-10 xl:px-8'
    if (embedded) {
      loadingClassName =
        'mx-auto w-full max-w-[1800px] px-4 py-6 sm:px-6 sm:py-8 xl:px-8'
    }
    if (modelSquare) loadingClassName = 'w-full'
    return (
      <div className={loadingClassName}>
        <LoadingSkeleton viewMode={viewMode} />
      </div>
    )
  }

  const pricingSidebarProps = {
    quotaTypeFilter,
    endpointTypeFilter,
    vendorFilter,
    groupFilter,
    tagFilter,
    onQuotaTypeChange: setQuotaTypeFilter,
    onEndpointTypeChange: setEndpointTypeFilter,
    onVendorChange: setVendorFilter,
    onGroupChange: setGroupFilter,
    onTagChange: setTagFilter,
    vendors: vendors || [],
    groups: availableGroups,
    groupRatios: groupRatio,
    tags: availableTags,
    models: models || [],
    hasActiveFilters,
    onClearFilters: clearFilters,
    advancedFilters,
    advancedOptions,
    onAdvancedFilterChange: setAdvancedFilter,
  }

  let contentClassName =
    'mx-auto w-full max-w-[1800px] px-3 pt-16 pb-8 sm:px-6 sm:pt-20 sm:pb-10 xl:px-8'
  if (embedded) {
    contentClassName =
      'mx-auto w-full max-w-[1800px] px-4 py-5 sm:px-6 sm:py-6 xl:px-8'
  }
  if (modelSquare) contentClassName = 'w-full'

  return (
    <PageTransition className={contentClassName}>
      {!modelSquare && (
        <div className='mb-4 flex items-center sm:mb-5'>
          <h1 className='truncate text-base font-bold tracking-tight sm:text-lg'>
            {t('Model Square')}
          </h1>
        </div>
      )}

      <div
        className={cn(
          'grid gap-4',
          filtersOpen && 'xl:grid-cols-[260px_minmax(0,1fr)]'
        )}
      >
        {filtersOpen && (
          <PricingSidebar
            {...pricingSidebarProps}
            className='hover-scrollbar hidden max-h-[calc(100dvh-10rem)] self-start overflow-y-auto xl:block'
          />
        )}

        <main className='min-w-0 space-y-4'>
          <PricingToolbar
            searchInput={searchInput}
            onSearchChange={setSearchInput}
            onClearSearch={clearSearch}
            filteredCount={filteredModels.length}
            totalCount={models?.length}
            sortBy={sortBy}
            onSortChange={setSortBy}
            tokenUnit={tokenUnit}
            onTokenUnitChange={setTokenUnit}
            showRechargePrice={showRechargePrice}
            onRechargePriceChange={setShowRechargePrice}
            viewMode={viewMode}
            onViewModeChange={setViewMode}
            {...pricingSidebarProps}
            activeFilterCount={activeFilterCount}
            filtersOpen={filtersOpen}
            onFiltersOpenChange={setFiltersOpen}
          />

          {renderPricingContent()}
        </main>
      </div>

      {!modelSquare && selectedModel && (
        <ModelDetailsDrawer
          open={Boolean(selectedModel)}
          onOpenChange={(open) => {
            if (!open) setSelectedModelName(null)
          }}
          model={selectedModel}
          groupRatio={groupRatio || {}}
          usableGroup={usableGroup || {}}
          endpointMap={
            (endpointMap as Record<
              string,
              { path?: string; method?: string }
            >) || {}
          }
          autoGroups={autoGroups || []}
          priceRate={priceRate ?? 1}
          usdExchangeRate={usdExchangeRate ?? 1}
          tokenUnit={tokenUnit}
          showRechargePrice={showRechargePrice}
        />
      )}
    </PageTransition>
  )
}
