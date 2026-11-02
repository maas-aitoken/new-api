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
import { z } from 'zod'

// ============================================================================
// Model Types
// ============================================================================

/**
 * Bound channel information
 */
export interface BoundChannel {
  id: number
  name: string
  type: number
}

/**
 * Model entity from API
 */
export type ModelSquareState = 'visible' | 'unavailable' | 'hidden' | 'partial'

export interface Model {
  square_state?: ModelSquareState
  has_metadata?: boolean
  configured_channel_count?: number
  id: number
  model_name: string
  description?: string
  icon?: string
  tags?: string
  vendor_id?: number
  endpoints?: string
  capabilities?: string
  context_length?: number
  parameter_count?: string
  release_date?: string
  supported_endpoints?: string[]
  status: number
  sync_official: number
  created_time: number
  updated_time: number
  name_rule: number
  // Runtime fields
  bound_channels?: BoundChannel[]
  enable_groups?: string[]
  quota_types?: number[]
  matched_models?: string[]
  matched_count?: number
}

/**
 * Vendor entity from API
 */
export interface Vendor {
  model_count?: number
  version?: string
  id: number
  name: string
  description?: string
  icon?: string
  status: number
  created_time: number
  updated_time: number
}

/**
 * <渠道, 上游模型> 的额度，每项 0 或缺省表示不限。额度按整个渠道合计，
 * 网关只统计用量，执行准入的是调度器。
 */
export interface ChannelModelLimit {
  rpm?: number
  tpm?: number
  max_concurrency?: number
}

/** 能力设置页的模型 → 渠道清单。 */
export interface CapabilityTargetChannel {
  id: number
  name: string
  type: number
  configured: boolean
  /** 渠道对该模型的费用倍率；0 表示未配置（按 1 处理）。 */
  cost_multiplier: number
  /** 走完 model_mapping 链后真正发给上游的模型名，额度按它存放。 */
  upstream_model: string
  model_limit: ChannelModelLimit
  /**
   * 不为 0 表示这个上游模型绑在资源池页的容量池上：额度由池动态算出，
   * model_limit 在绑定期间不生效，只能在资源池页移除。池已被删时名字为空。
   */
  capacity_pool_id: number
  capacity_pool_name: string
}

/** 一个 <渠道, 上游模型> 此刻的用量与剩余量。 */
export interface ChannelModelCapacity {
  channel_id: number
  model: string
  upstream_model: string
  pool_id: string
  limit: ChannelModelLimit
  rpm_used: number
  /** 在途请求只含输入估算，完成后才补上输出。 */
  tpm_used: number
  in_flight: number
  /** 每项 = max(额度-用量, 0)；不限时为 null。 */
  remaining: {
    rpm: number | null
    tpm: number | null
    concurrency: number | null
  }
  /** 绑了容量池时，上面的额度、用量和剩余是整个池的；这里是池信息和成员自己的用量。 */
  capacity_pool?: {
    id: number
    name: string
    /** dynamic：动态额度；floor：还没算出或已过期，用保底额度。 */
    source: 'dynamic' | 'floor'
    exhausted: boolean
    computed_at: number
    member_rpm_used: number
    member_tpm_used: number
    member_in_flight: number
  }
}

export interface ChannelModelCapacityResult {
  items: ChannelModelCapacity[]
  /** false 表示没配 Redis，用量只反映处理这次查询的节点。 */
  shared: boolean
  window_seconds: number
}

export interface CapabilityTarget {
  id: number
  model_name: string
  /** 命中的模型级声明行名称；精确模型时与 model_name 相同。 */
  declaration_name: string
  /** 声明行的匹配规则：0 精确、1 前缀、2 包含、3 后缀。 */
  name_rule: number
  capabilities?: string
  /** 没有模型级声明命中时只能配置渠道倍率，不能收窄能力。 */
  declared: boolean
  channels: CapabilityTargetChannel[]
}

export interface CapabilityTargetsPage {
  items: CapabilityTarget[]
  total: number
  page: number
  page_size: number
}

export interface ChannelModelCapabilityDetail {
  channel_id: number
  model: string
  configured: boolean
  capabilities: string
  model_capabilities: string
  effective: string
  cost_multiplier: number
}

/**
 * Prefill group entity
 */
export interface PrefillGroup {
  id: number
  name: string
  type: 'model' | 'tag' | 'endpoint'
  items: string | string[]
  description?: string
}

// ============================================================================
// API Request/Response Types
// ============================================================================

/**
 * Get models list parameters
 */
export interface GetModelsParams {
  square_state?: ModelSquareState
  include_channel_models?: boolean
  p?: number
  page_size?: number
  vendor?: string // vendor ID to filter by
  status?: string // filter by status
  sync_official?: string // filter by sync_official status
}

/**
 * Search models parameters
 */
export interface SearchModelsParams {
  square_state?: ModelSquareState
  include_channel_models?: boolean
  keyword?: string
  vendor?: string // vendor ID to filter by
  status?: string // filter by status
  sync_official?: string // filter by sync_official status
  p?: number
  page_size?: number
}

/**
 * Get models response
 */
export interface GetModelsResponse {
  success: boolean
  message?: string
  data?: {
    items: Model[]
    total: number
    page: number
    page_size: number
    vendor_counts?: Record<string, number>
  }
}

/**
 * Get model detail response
 */
export interface GetModelResponse {
  success: boolean
  message?: string
  data?: Model
}

/**
 * Get vendors response
 */
export interface GetVendorsResponse {
  success: boolean
  message?: string
  data?: {
    items: Vendor[]
    total: number
    page: number
    page_size: number
  }
}

/**
 * Get vendor response
 */
export interface GetVendorResponse {
  success: boolean
  message?: string
  data?: Vendor
}

/**
 * Sync diff data
 */
export type MetadataSyncField =
  | 'description'
  | 'icon'
  | 'tags'
  | 'vendor'
  | 'endpoints'
  | 'name_rule'
  | 'status'
export type MetadataSyncValues = {
  description: string
  icon: string
  tags: string
  vendor: string
  endpoints: string
  name_rule: number
  status: number
}
export type MetadataSyncCandidate = {
  model_name: string
  kind:
    | 'create'
    | 'update'
    | 'unchanged'
    | 'blocked'
    | 'missing_upstream'
    | 'missing_vendor'
  scope: 'site' | 'catalog'
  record_version: string
  fields: Array<{
    field: MetadataSyncField
    local: string | number
    upstream: string | number
  }>
  upstream?: MetadataSyncValues
  vendor_to_create?: string
}
export type MetadataSyncSource = {
  locale: SyncLocale
  models_url: string
  vendors_url: string
  version: string
}
export type MetadataSyncPreview = {
  source: MetadataSyncSource
  candidates: MetadataSyncCandidate[]
}
export type MetadataSyncSelection = {
  model_name: string
  record_version: string
  create: boolean
  fields: MetadataSyncField[]
}
export type MetadataSyncRequest = {
  locale: SyncLocale
  source_version: string
  selections: MetadataSyncSelection[]
}
export type MetadataSyncResult = {
  created_models: string[]
  updated_models: MetadataSyncSelection[]
  created_vendors: string[]
}
export interface SyncUpstreamResponse {
  success: boolean
  message?: string
  data?: MetadataSyncResult
}
export interface PreviewUpstreamDiffResponse {
  success: boolean
  message?: string
  data?: MetadataSyncPreview
}

/**
 * Missing models response
 */
export interface MissingModelsResponse {
  success: boolean
  message?: string
  data?: string[]
}

/**
 * Prefill groups response
 */
export interface PrefillGroupsResponse {
  success: boolean
  message?: string
  data?: PrefillGroup[]
}

// ============================================================================
// Form Data Types
// ============================================================================

/**
 * Model form schema
 */
export const modelFormSchema = z.object({
  id: z.number().optional(),
  model_name: z.string().min(1, 'Model name is required'),
  description: z.string().default(''),
  icon: z.string().default(''),
  tags: z.array(z.string()).default([]),
  vendor_id: z.number().optional(),
  endpoints: z.string().default(''),
  name_rule: z.number().min(0).max(3).default(0),
  status: z.boolean().default(true),
  sync_official: z.boolean().default(true),
})

export type ModelFormValues = z.infer<typeof modelFormSchema>

/**
 * Vendor form schema
 */
export const vendorFormSchema = z.object({
  id: z.number().optional(),
  name: z
    .string()
    .trim()
    .min(1, 'Vendor name is required')
    .max(128, 'Vendor name and icon must not exceed 128 characters.'),
  description: z.string().default(''),
  icon: z
    .string()
    .max(128, 'Vendor name and icon must not exceed 128 characters.')
    .default(''),
  version: z.string().optional(),
})

export type VendorFormValues = z.infer<typeof vendorFormSchema>

/**
 * Prefill group form schema
 */
export const prefillGroupFormSchema = z.object({
  id: z.number().optional(),
  name: z.string().min(1, 'Group name is required'),
  description: z.string().optional(),
  type: z.enum(['model', 'tag', 'endpoint']),
  items: z.union([z.string(), z.array(z.string())]),
})

export type PrefillGroupFormValues = z.infer<typeof prefillGroupFormSchema>

// ============================================================================
// Utility Types
// ============================================================================

/**
 * Name rule type
 */
export type NameRule = 0 | 1 | 2 | 3 // exact, prefix, contains, suffix

/**
 * Model status type
 */
export type ModelStatus = 0 | 1 // disabled, enabled

/**
 * Quota type
 */
export type QuotaType = 0 | 1 // usage-based, per-call

/**
 * Sync locale
 */
export type SyncLocale = 'zh' | 'zh-CN' | 'en' | 'ja'

/**
 * Sync upstream source
 */
export type SyncSource = 'official'

// ============================================================================
// Model Deployments Types
// ============================================================================

/**
 * Model tab type
 */
export type ModelTabCategory =
  | 'metadata'
  | 'vendors'
  | 'deployments'
  | 'capabilities'

/**
 * Deployment entity from API
 */
export interface Deployment {
  id: string | number
  container_name?: string
  deployment_name?: string
  name?: string
  status?: string
  provider?: string
  /**
   * Human readable string returned by backend, e.g. "2 hour 15 minutes"
   * or "completed".
   */
  time_remaining?: string
  /**
   * Remaining minutes (numeric) returned by backend.
   */
  compute_minutes_remaining?: number
  /**
   * Served minutes (numeric) returned by backend.
   */
  compute_minutes_served?: number
  /**
   * Completed percent (0-100) returned by backend.
   */
  completed_percent?: number
  hardware_info?: string | Record<string, unknown>
  hardware_name?: string
  brand_name?: string
  hardware_quantity?: number
  created_at?: string | number
  updated_at?: string | number
  [key: string]: unknown
}

/**
 * Deployment settings response
 */
export interface DeploymentSettingsResponse {
  success: boolean
  message?: string
  data?: {
    enabled?: boolean
    [key: string]: unknown
  }
}

/**
 * List deployments response
 */
export interface ListDeploymentsResponse {
  success: boolean
  message?: string
  data?: {
    items?: Deployment[]
    total?: number
    page?: number
    page_size?: number
    status_counts?: Record<string, number>
  }
}

/**
 * Deployment logs response
 */
export interface DeploymentLogsResponse {
  success: boolean
  message?: string
  data?: {
    logs?: Array<{
      timestamp?: string
      level?: string
      message?: string
      source?: string
    }>
    cursor?: string
  }
}
