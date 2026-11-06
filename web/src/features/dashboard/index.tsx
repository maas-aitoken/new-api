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
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { Eye, EyeOff } from 'lucide-react'
import { useState, useCallback, useMemo, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { FadeIn } from '@/components/page-transition'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ROLE } from '@/lib/roles'
import { computeTimeRange, getRollingDateRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { ModelsChartPreferences } from './components/models/models-chart-preferences'
import { ModelsFilter } from './components/models/models-filter-dialog'
import { UsageExportButton } from './components/models/usage-export-button'
import { OverviewDashboard } from './components/overview/overview-dashboard'
import { UserAnalyticsToolbar } from './components/users/user-analytics-toolbar'
import { DEFAULT_TIME_GRANULARITY } from './constants'
import {
  buildDefaultDashboardFilters,
  getDefaultDays,
  getSavedChartPreferences,
  getSavedGranularity,
  saveChartPreferences,
} from './lib'
import { USER_USAGE_EXPORT_GROUPINGS } from './lib/usage-export'
import {
  type DashboardSectionId,
  DASHBOARD_DEFAULT_SECTION,
  DASHBOARD_SECTION_IDS,
} from './section-registry'
import type {
  DashboardChartPreferences,
  DashboardFilters,
  QuotaDataItem,
  UserChartsFilters,
} from './types'

const route = getRouteApi('/_authenticated/dashboard/$section')

const LOG_STAT_CARD_FALLBACK_KEYS = [
  'count',
  'quota',
  'tokens',
  'cache-read',
  'cache-creation',
  'average-rpm',
  'average-tpm',
] as const

const LazyLogStatCards = lazy(() =>
  import('./components/models/log-stat-cards').then((m) => ({
    default: m.LogStatCards,
  }))
)

const LazyModelCharts = lazy(() =>
  import('./components/models/model-charts').then((m) => ({
    default: m.ModelCharts,
  }))
)

const LazyModelMetricsTable = lazy(() =>
  import('./components/models/model-metrics-table').then((m) => ({
    default: m.ModelMetricsTable,
  }))
)

const LazyConsumptionDistributionChart = lazy(() =>
  import('./components/models/consumption-distribution-chart').then((m) => ({
    default: m.ConsumptionDistributionChart,
  }))
)

const LazyModelAvailability = lazy(() =>
  import('./components/models/model-availability').then((m) => ({
    default: m.ModelAvailability,
  }))
)

const LazyPeakTpmChart = lazy(() =>
  import('./components/models/peak-tpm-chart').then((m) => ({
    default: m.PeakTpmChart,
  }))
)

const LazyUserCharts = lazy(() =>
  import('./components/users/user-charts').then((m) => ({
    default: m.UserCharts,
  }))
)

const LazyUserUsageTable = lazy(() =>
  import('./components/users/user-usage-table').then((m) => ({
    default: m.UserUsageTable,
  }))
)

const LazyIselfUsageTable = lazy(() =>
  import('./components/users/iself-usage-table').then((m) => ({
    default: m.IselfUsageTable,
  }))
)

const LazyFlowCharts = lazy(() =>
  import('./components/flow/flow-charts').then((m) => ({
    default: m.FlowCharts,
  }))
)

function LogStatCardsFallback() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='divide-border/60 grid min-w-0 grid-cols-2 divide-x sm:grid-cols-3 lg:grid-cols-7'>
        {LOG_STAT_CARD_FALLBACK_KEYS.map((key, index) => (
          <div
            key={key}
            className={cn(
              'min-w-0 px-2.5 py-1.5 sm:px-3 sm:py-4 xl:px-4',
              index === LOG_STAT_CARD_FALLBACK_KEYS.length - 1 &&
                'col-span-2 sm:col-span-1'
            )}
          >
            <div className='flex items-center gap-1.5 sm:gap-2'>
              <Skeleton className='size-4 rounded-sm sm:size-7 sm:rounded-md' />
              <Skeleton className='h-4 w-16' />
            </div>
            <Skeleton className='mt-1 h-5 w-16 sm:mt-2 sm:h-7 sm:w-20' />
            <Skeleton className='mt-1 hidden h-3.5 w-28 md:block' />
          </div>
        ))}
      </div>
      <div className='border-t px-3 py-2'>
        <Skeleton className='h-4 w-full max-w-md' />
      </div>
    </div>
  )
}

function ModelChartsFallback() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex items-center justify-between border-b px-4 py-3 sm:px-5'>
        <Skeleton className='h-5 w-32' />
        <Skeleton className='h-8 w-72' />
      </div>
      <div className='h-96 p-2'>
        <Skeleton className='h-full w-full' />
      </div>
    </div>
  )
}

function ModelMetricsFallback() {
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex items-center gap-2 border-b px-4 py-3 sm:px-5'>
        <Skeleton className='size-7 rounded-md' />
        <div className='space-y-1'>
          <Skeleton className='h-4 w-32' />
          <Skeleton className='h-3 w-48' />
        </div>
      </div>
      <div className='space-y-3 p-4'>
        {Array.from({ length: 4 }, (_, index) => (
          <div key={index} className='flex items-center gap-4'>
            <Skeleton className='h-4 flex-1' />
            <Skeleton className='h-4 w-16' />
            <Skeleton className='h-4 w-20' />
            <Skeleton className='h-4 w-20' />
          </div>
        ))}
      </div>
    </div>
  )
}

const SECTION_META: Record<DashboardSectionId, { titleKey: string }> = {
  overview: {
    titleKey: 'Overview',
  },
  models: {
    titleKey: 'Model Call Analytics',
  },
  flow: {
    titleKey: 'Flow',
  },
  users: {
    titleKey: 'User Analytics',
  },
}

export function Dashboard() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = route.useParams()
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const activeSection = (params.section ??
    DASHBOARD_DEFAULT_SECTION) as DashboardSectionId

  const [modelData, setModelData] = useState<QuotaDataItem[]>([])
  const [dataLoading, setDataLoading] = useState(false)
  const [chartPreferences, setChartPreferences] =
    useState<DashboardChartPreferences>(() => getSavedChartPreferences())
  const [modelFilters, setModelFilters] = useState<DashboardFilters>(() =>
    buildDefaultDashboardFilters(getSavedChartPreferences())
  )
  const [userChartsFilters, setUserChartsFilters] = useState<UserChartsFilters>(
    () => {
      const granularity = getSavedGranularity()
      return {
        timeGranularity: granularity,
        selectedRange: getDefaultDays(granularity),
        topUserLimit: 10,
        dimension: 'user',
        org: {},
      }
    }
  )
  // Shared by the user page cards and export.
  const userDashboardFilters = useMemo<DashboardFilters>(() => {
    const { start, end } = getRollingDateRange(userChartsFilters.selectedRange)
    return {
      start_timestamp: start,
      end_timestamp: end,
      time_granularity: userChartsFilters.timeGranularity,
    }
  }, [userChartsFilters.selectedRange, userChartsFilters.timeGranularity])
  const [flowSensitiveVisible, setFlowSensitiveVisible] = useState(true)
  const [iselfVisible, setIselfVisible] = useState(false)

  const iselfParams = useMemo(() => {
    if (activeSection === 'users') {
      const { start, end } = getRollingDateRange(
        userChartsFilters.selectedRange
      )
      return {
        start_timestamp: Math.floor(start.getTime() / 1000),
        end_timestamp: Math.floor(end.getTime() / 1000),
      }
    }
    return {
      ...computeTimeRange(
        getDefaultDays(modelFilters.time_granularity),
        modelFilters.start_timestamp,
        modelFilters.end_timestamp
      ),
      username: modelFilters.username,
    }
  }, [activeSection, modelFilters, userChartsFilters.selectedRange])

  const handleFilterChange = useCallback((filters: DashboardFilters) => {
    setModelFilters(filters)
  }, [])

  const handleResetFilters = useCallback(() => {
    setModelFilters(buildDefaultDashboardFilters(chartPreferences))
  }, [chartPreferences])

  const handleDataUpdate = useCallback(
    (data: QuotaDataItem[], loading: boolean) => {
      setModelData(data)
      setDataLoading(loading)
    },
    []
  )

  const handleChartPreferencesChange = useCallback(
    (preferences: DashboardChartPreferences) => {
      setChartPreferences(preferences)
      setModelFilters(buildDefaultDashboardFilters(preferences))
      saveChartPreferences(preferences)
    },
    []
  )

  const meta = SECTION_META[activeSection] ?? SECTION_META.overview
  const isAdmin = Boolean(userRole && userRole >= ROLE.ADMIN)
  const visibleSections = useMemo(
    () =>
      DASHBOARD_SECTION_IDS.filter(
        (section) => section !== 'overview' && (section !== 'users' || isAdmin)
      ),
    [isAdmin]
  )
  const handleSectionChange = useCallback(
    (section: string) => {
      void navigate({
        to: '/dashboard/$section',
        params: { section: section as DashboardSectionId },
      })
    },
    [navigate]
  )
  const showSectionTabs =
    activeSection !== 'overview' && visibleSections.length > 1
  const modelActions =
    activeSection === 'models' ? (
      <>
        <ModelsChartPreferences
          preferences={chartPreferences}
          onPreferencesChange={handleChartPreferencesChange}
        />
        <ModelsFilter
          preferences={chartPreferences}
          currentFilters={modelFilters}
          onFilterChange={handleFilterChange}
          onReset={handleResetFilters}
        />
        {isAdmin && <UsageExportButton filters={modelFilters} />}
      </>
    ) : null
  const userActions =
    activeSection === 'users' && isAdmin ? (
      <UsageExportButton
        filters={userDashboardFilters}
        org={userChartsFilters.org}
        groupings={USER_USAGE_EXPORT_GROUPINGS}
      />
    ) : null
  const flowActions =
    activeSection === 'flow' ? (
      <>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon'
                onClick={() => setFlowSensitiveVisible((prev) => !prev)}
                aria-label={
                  flowSensitiveVisible
                    ? t('Hide sensitive data')
                    : t('Show sensitive data')
                }
                className='text-muted-foreground hover:text-foreground size-8'
              />
            }
          >
            {flowSensitiveVisible ? <Eye /> : <EyeOff />}
          </TooltipTrigger>
          <TooltipContent>
            {flowSensitiveVisible
              ? t('Hide sensitive data')
              : t('Show sensitive data')}
          </TooltipContent>
        </Tooltip>
        <ModelsFilter
          preferences={chartPreferences}
          currentFilters={modelFilters}
          onFilterChange={handleFilterChange}
          onReset={handleResetFilters}
          titleKey='Flow Filters'
          descriptionKey='Filter the traffic flow view by time range and user.'
        />
      </>
    ) : null
  const sectionActions = (
    <>
      {(activeSection === 'models' ||
        (activeSection === 'users' && isAdmin)) && (
        <Button
          variant={iselfVisible ? 'secondary' : 'outline'}
          size='sm'
          aria-pressed={iselfVisible}
          aria-controls='dashboard-iself-usage'
          onClick={() => setIselfVisible((visible) => !visible)}
        >
          {t('iSelf data')}
        </Button>
      )}
      {modelActions ?? flowActions ?? userActions}
    </>
  )

  if (activeSection === 'overview') {
    return <OverviewDashboard />
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t(meta.titleKey)}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-3 sm:space-y-4'>
          <div className='flex flex-wrap items-center justify-between gap-1.5 sm:gap-2'>
            {showSectionTabs ? (
              <Tabs value={activeSection} onValueChange={handleSectionChange}>
                <TabsList className='max-w-full flex-wrap justify-start group-data-horizontal/tabs:h-auto'>
                  {visibleSections.map((section) => (
                    <TabsTrigger key={section} value={section}>
                      {t(SECTION_META[section].titleKey)}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            ) : (
              <div />
            )}
            {sectionActions != null && (
              <div className='flex shrink-0 flex-wrap items-center gap-1.5 sm:gap-2'>
                {sectionActions}
              </div>
            )}
          </div>
          {activeSection === 'models' && (
            <>
              <FadeIn>
                <Suspense fallback={<LogStatCardsFallback />}>
                  <LazyLogStatCards
                    filters={modelFilters}
                    onDataUpdate={handleDataUpdate}
                  />
                </Suspense>
              </FadeIn>
              <FadeIn delay={0.05}>
                <Suspense fallback={<ModelMetricsFallback />}>
                  <LazyModelMetricsTable filters={modelFilters} />
                </Suspense>
              </FadeIn>
              {iselfVisible && (
                <Suspense fallback={<ModelMetricsFallback />}>
                  <LazyIselfUsageTable params={iselfParams} />
                </Suspense>
              )}
              <FadeIn delay={0.1}>
                <Suspense fallback={<ModelMetricsFallback />}>
                  <LazyModelAvailability />
                </Suspense>
              </FadeIn>
              <FadeIn delay={0.15}>
                <Suspense fallback={<ModelChartsFallback />}>
                  <LazyConsumptionDistributionChart
                    data={modelData}
                    loading={dataLoading}
                    defaultChartType={
                      chartPreferences.consumptionDistributionChart
                    }
                    timeGranularity={
                      modelFilters.time_granularity || DEFAULT_TIME_GRANULARITY
                    }
                  />
                </Suspense>
              </FadeIn>
              <FadeIn delay={0.2}>
                <Suspense fallback={<ModelChartsFallback />}>
                  <LazyModelCharts
                    data={modelData}
                    loading={dataLoading}
                    defaultChartTab={chartPreferences.modelAnalyticsChart}
                    timeGranularity={
                      modelFilters.time_granularity || DEFAULT_TIME_GRANULARITY
                    }
                  />
                </Suspense>
              </FadeIn>
              {isAdmin && (
                <FadeIn delay={0.25}>
                  <Suspense fallback={<ModelChartsFallback />}>
                    <LazyPeakTpmChart filters={modelFilters} />
                  </Suspense>
                </FadeIn>
              )}
            </>
          )}
          {activeSection === 'users' && (
            <>
              <UserAnalyticsToolbar
                filters={userChartsFilters}
                onFiltersChange={setUserChartsFilters}
                showOrgFilter={isAdmin}
              />
              {isAdmin && (
                <FadeIn>
                  <Suspense fallback={<LogStatCardsFallback />}>
                    <LazyLogStatCards
                      filters={userDashboardFilters}
                      orgFilter={userChartsFilters.org}
                    />
                  </Suspense>
                </FadeIn>
              )}
              <FadeIn delay={0.05}>
                <Suspense fallback={<ModelChartsFallback />}>
                  <LazyUserCharts
                    filters={userChartsFilters}
                    onFiltersChange={setUserChartsFilters}
                    showDimensions={isAdmin}
                  />
                </Suspense>
              </FadeIn>
              {isAdmin && (
                <FadeIn delay={0.1}>
                  <Suspense fallback={<ModelMetricsFallback />}>
                    <LazyUserUsageTable
                      selectedRange={userChartsFilters.selectedRange}
                      org={userChartsFilters.org}
                    />
                  </Suspense>
                </FadeIn>
              )}
              {iselfVisible && (
                <Suspense fallback={<ModelMetricsFallback />}>
                  <LazyIselfUsageTable params={iselfParams} />
                </Suspense>
              )}
            </>
          )}
          {activeSection === 'flow' && (
            <FadeIn>
              <Suspense fallback={<ModelChartsFallback />}>
                <LazyFlowCharts
                  filters={modelFilters}
                  sensitiveVisible={flowSensitiveVisible}
                />
              </Suspense>
            </FadeIn>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
