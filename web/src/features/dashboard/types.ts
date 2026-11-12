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
import type { TimeGranularity } from '@/lib/time'

// ============================================================================
// Quota & Usage Data Types
// ============================================================================

export interface QuotaDataItem {
  id?: number
  user_id?: number
  username?: string
  model_name?: string
  business_unit?: string
  department?: string
  team?: string
  created_at: number
  cache_read_tokens?: number
  cache_creation_tokens?: number
  token_used?: number
  count?: number
  quota?: number
}

export interface ModelMetric {
  modelName: string
  requestCount: number
  totalTokens: number
  averageRpm: number
  averageTpm: number
}

export interface FlowQuotaDataItem {
  user_id?: number
  username?: string
  node_name?: string
  use_group?: string
  token_id?: number
  token_name?: string
  channel_id?: number
  channel_name?: string
  model_name?: string
  token_used?: number
  count?: number
  quota?: number
}

export type FlowMetric = 'quota' | 'tokens' | 'requests'

export type FlowOverflowMode = 'aggregate' | 'hide'

export type FlowRole = 'user' | 'admin' | 'root'

export type FlowNodeKind =
  | 'user'
  | 'node'
  | 'token'
  | 'group'
  | 'model'
  | 'channel'

export interface FlowNodeFilter {
  kind: FlowNodeKind
  id: string
}

export interface FlowLinkSelection {
  source: string
  target: string
}

export interface FlowBuildOptions {
  role?: FlowRole
  selectedUsers?: string[]
  selectedNodes?: FlowNodeFilter[]
  activeNode?: FlowNodeFilter
  activeLink?: FlowLinkSelection
  colorPalette?: readonly string[]
  visibleStages?: FlowNodeKind[]
  topNodeLimit?: number
  overflowMode?: FlowOverflowMode
  // When true, sensitive node labels (users, tokens, nodes, groups, channels)
  // are partially masked in the rendered graph while keeping node identity so
  // the Sankey shape stays intact.
  maskSensitive?: boolean
  // Resolves the label for a token whose record no longer exists (deleted).
  // Lets the caller inject a localized string such as "Deleted (123)".
  deletedTokenLabel?: (tokenId: number) => string
  otherNodeLabel?: (kind: FlowNodeKind) => string
}

export interface DashboardFlowNode {
  id: string
  label: string
  kind: FlowNodeKind
  value: number
  requests: number
  quota: number
  tokens: number
  color: string
  colorKey: string
  highlighted?: boolean
  dimmed?: boolean
}

export interface DashboardFlowLink {
  source: string
  target: string
  value: number
  requests: number
  quota: number
  tokens: number
  sourceLabel: string
  targetLabel: string
  color: string
  linkColor: string
  linkAlpha: number
  hoverColor: string
  colorKey: string
  share: number
  highlighted?: boolean
  dimmed?: boolean
}

export interface DashboardFlowGraph {
  nodes: DashboardFlowNode[]
  links: DashboardFlowLink[]
}

export interface FlowUserFilterOption {
  value: string
  label: string
  valueLabel: string
  valueRaw: number
  color: string
}

export interface FlowNodeFilterOption {
  kind: FlowNodeKind
  value: string
  label: string
  valueLabel: string
  valueRaw: number
  color: string
}

export interface FlowFilterOptions {
  users: FlowUserFilterOption[]
  nodes: FlowNodeFilterOption[]
}

export interface FlowSummary {
  quota: number
  tokens: number
  requests: number
}

export interface ProcessedFlowData {
  summary: FlowSummary
  flow: DashboardFlowGraph
  filterOptions: FlowFilterOptions
}

// ============================================================================
// Usage Analytics Types (/api/data/analytics, /api/data/analytics/self)
// ============================================================================

export type UsageOrgLevel = 'business_unit' | 'department' | 'team'

export type UsageAnalyticsGroupBy =
  | 'summary'
  | 'model'
  | 'user'
  | 'user_model'
  | 'model_channel'
  | UsageOrgLevel

// An empty level does not filter; usage follows each user's current organization.
export type UsageOrgFilter = Partial<Record<UsageOrgLevel, string>>

// Empty levels are unassigned.
export type UsageOrgOption = Record<UsageOrgLevel, string>

export type UserChartsDimension = 'user' | UsageOrgLevel

// Aggregated quota_data counters shared by the summary and every grouped row.
export interface UsageMetrics {
  // Consume requests including stream-interrupted ones.
  request_count: number
  quota: number
  token_used: number
  input_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  // Seconds; successful, non-interrupted requests only.
  use_time_total: number
  use_time_count: number
  stream_error_count: number
  // Final failures after retries (a request retried across channels counts once).
  failed_count: number
  // Requests recorded after the detailed counters existed (legacy rows excluded).
  tracked_count: number
  // Unix seconds; 0 when only legacy rows matched.
  last_called_at: number
}

export interface UsageAnalyticsSummary extends UsageMetrics {
  active_users: number
  model_count: number
}

// Dimension fields are present only when they are part of the grouping.
export interface UsageAnalyticsRow extends UsageMetrics {
  model_name?: string
  user_id?: number
  username?: string
  display_name?: string
  channel_id?: number
  channel_name?: string
  user_count?: number
  // user_status is 0 for users that no longer exist.
  email?: string
  user_status?: number
  business_unit?: string
  department?: string
  team?: string
  org_path?: string
}

export interface UsageAnalyticsParams extends UsageOrgFilter {
  start_timestamp: number
  end_timestamp: number
  group_by: UsageAnalyticsGroupBy
  username?: string
  model?: string
}

export interface UsageAnalyticsData {
  summary: UsageAnalyticsSummary | null
  rows: UsageAnalyticsRow[]
}

// ============================================================================
// Peak TPM Types (/api/data/peak-tpm)
// ============================================================================

export interface PeakTpmParams {
  start_timestamp: number
  end_timestamp: number
  username?: string
  model?: string
}

export interface PeakTpmPoint {
  // Unix seconds at the start of the hour.
  hour: number
  // Highest per-minute token sum observed within the hour.
  peak_tpm: number
}

export interface IselfUsageRow {
  email: string
  source: 'iself'
  model_name: string
  request_count: number
  quota: number
  // Includes cache reads and writes, like UsageMetrics.input_tokens.
  input_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  token_used: number
  last_called_at: number
}

export type IselfUsageParams = Omit<UsageAnalyticsParams, 'group_by'>

// ============================================================================
// Model Availability Types (/api/perf-metrics/availability)
// ============================================================================

export type ModelAvailabilityMinutes = 10 | 30 | 60

export interface ModelAvailabilityRow {
  model_name: string
  success_count: number
  stream_error_count: number
  failed_count: number
  total_tokens: number
  last_called_at: number
}

export type ModelAvailabilityCounts = Pick<
  ModelAvailabilityRow,
  'success_count' | 'stream_error_count' | 'failed_count'
>

export interface ModelAvailabilityData {
  minutes: number
  start_timestamp: number
  end_timestamp: number
  // Hard failures come from error logs; when logging is off, failed_count
  // stays 0 and availability is overstated.
  error_log_enabled: boolean
  models: ModelAvailabilityRow[]
}

export type ModelAvailabilityStatusId =
  | 'insufficient_samples'
  | 'stable'
  | 'fair'
  | 'degraded'
  | 'unstable'

export interface ModelAvailabilityStatus {
  id: ModelAvailabilityStatusId
  // English i18n key; render with t(status.labelKey).
  labelKey: string
}

export interface RankedModelAvailabilityRow extends ModelAvailabilityRow {
  // 1-based position after ranking.
  rank: number
  // success + stream_error + failed.
  total: number
  // Success percentage (0-100); null when total is 0.
  rate: number | null
  // Wilson score lower bound as a percentage (0-100); 0 when total is 0.
  wilson: number
  // Share of all requests in the window, percentage (0-100).
  share: number
  rpm: number
  tpm: number
  status: ModelAvailabilityStatus
}

// ============================================================================
// Uptime Monitoring Types
// ============================================================================

export interface UptimeMonitor {
  name: string
  uptime: number
  status: number
  group?: string
}

export interface UptimeGroupResult {
  categoryName: string
  monitors: UptimeMonitor[]
}

// ============================================================================
// Dashboard Filter Types
// ============================================================================

export interface DashboardFilters {
  start_timestamp?: Date
  end_timestamp?: Date
  time_granularity?: TimeGranularity
  username?: string
}

export type ConsumptionDistributionChartType = 'bar' | 'area'

export type ModelAnalyticsChartTab = 'trend' | 'proportion' | 'top'

export interface DashboardChartPreferences {
  consumptionDistributionChart: ConsumptionDistributionChartType
  modelAnalyticsChart: ModelAnalyticsChartTab
  defaultTimeRangeDays: number
  defaultTimeGranularity: TimeGranularity
}

// User analytics selections are held by the dashboard parent so they survive
// switching between dashboard sub-sections, matching the model/flow filters.
export interface UserChartsFilters {
  timeGranularity: TimeGranularity
  selectedRange: number
  topUserLimit: number
  dimension: UserChartsDimension
  org: UsageOrgFilter
}

// ============================================================================
// API Info Types
// ============================================================================

export interface ApiInfoItem {
  url: string
  route: string
  description: string
  color: string
}

export interface PingStatus {
  latency: number | null
  testing: boolean
  error: boolean
}

export type PingStatusMap = Record<string, PingStatus>

// ============================================================================
// Chart Types
// ============================================================================

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type VChartSpec = Record<string, any>

export interface ProcessedChartData {
  spec_pie: VChartSpec
  spec_line: VChartSpec
  spec_area: VChartSpec
  spec_model_line: VChartSpec
  spec_rank_bar: VChartSpec
  totalQuotaDisplay: string
  totalCountDisplay: string
}

export interface ProcessedUserChartData {
  spec_user_rank: VChartSpec
  spec_user_trend: VChartSpec
}

// ============================================================================
// Announcement Types
// ============================================================================

export interface AnnouncementItem {
  id?: number
  content: string
  publishDate?: string
  type?: 'default' | 'ongoing' | 'success' | 'warning' | 'error'
  extra?: string
}

// ============================================================================
// FAQ Types
// ============================================================================

export interface FAQItem {
  id?: number
  question: string
  answer: string
}
