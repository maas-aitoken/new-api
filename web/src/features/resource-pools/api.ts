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
import { api } from "@/lib/api";
import { requireServerSuccess } from "@/lib/server-error-message";

export type ResourcePoolProvider = "claude" | "gpt" | "gemini" | "grok";

export type ResourcePoolSource = {
  id: number;
  provider: ResourcePoolProvider;
  name: string;
  endpoint: string;
  admin_password: string;
  group_name: string;
  enabled: boolean;
  snapshot_at: number;
};

export type ResourcePoolAccount = {
  id: string;
  email: string;
  user_id?: string;
  name?: string;
  enabled: boolean;
  subscription_type?: string;
  subscription_title?: string;
  usage_current: number;
  usage_limit: number;
  usage_percent?: number;
  usage_percentage: number;
  next_reset_date?: string;
  groups?: string[];
  source_group?: string;
  source_channel?: string;
  rpm_limit?: number;
  rpm_current?: number;
  last_refresh?: number;
  source_id: number;
  source_name: string;
  source_endpoint: string;
  // codex2api (Grok) fields
  plan_type?: string;
  plan_display?: string;
  status?: string;
  models?: string[];
  total_requests?: number;
  usage_percent_7d?: number;
  usage_percent_5h?: number;
  weekly_percent?: number;
  requests_7d?: number;
  tokens_7d?: number;
  account_billed_7d?: number;
  reset_7d_at?: string;
  reset_5h_at?: string;
};

export type ResourcePoolSourceStatus = {
  source_id: number;
  source_name: string;
  source_endpoint: string;
  status: string;
  count: number;
  error: string;
};

export type ResourcePoolUsage = {
  accounts: ResourcePoolAccount[];
  sources: ResourcePoolSourceStatus[];
  /** Unix seconds of the upstream poll; the server caches polls briefly. */
  refreshed_at: number;
};

export async function getResourcePoolSources(
  provider: ResourcePoolProvider,
): Promise<ResourcePoolSource[]> {
  const response = await api.get<{
    success: boolean;
    data: ResourcePoolSource[];
  }>("/api/channel/resource_pools/sources", { params: { provider } });
  return requireServerSuccess(response.data).data ?? [];
}

export async function saveResourcePoolSources(
  provider: ResourcePoolProvider,
  data: Omit<ResourcePoolSource, "provider" | "snapshot_at">[],
): Promise<void> {
  const response = await api.post<{ success: boolean }>(
    "/api/channel/resource_pools/sources",
    { provider, data },
  );
  requireServerSuccess(response.data);
}

/** `refresh` bypasses the server-side poll cache. */
export async function getResourcePoolUsage(
  provider: ResourcePoolProvider,
  refresh = false,
): Promise<ResourcePoolUsage> {
  const response = await api.get<{
    success: boolean;
    data: ResourcePoolUsage;
  }>("/api/channel/resource_pools/usage", {
    params: refresh ? { provider, refresh: true } : { provider },
  });
  return requireServerSuccess(response.data).data;
}

export async function deleteResourcePoolSource(id: number): Promise<void> {
  const response = await api.delete<{ success: boolean }>(
    `/api/channel/resource_pools/sources/${id}`,
  );
  requireServerSuccess(response.data);
}

/** RPM / TPM / 并发三项，0 表示这一项不限（单账号速率里表示没填）。 */
export type CapacityPoolLimit = {
  rpm?: number;
  tpm?: number;
  max_concurrency?: number;
};

/** 容量池的可调参数，与后端 model.CapacityPoolConfig 一致。 */
export type CapacityPoolConfig = {
  source_ids: number[];
  /** 只取这些上游分组的账号，空表示来源里的全部账号。 */
  groups?: string[];
  /** 按套餐填的单账号速率，没填的格子回落到默认行。 */
  plan_rates?: Record<string, CapacityPoolLimit>;
  default_rate: CapacityPoolLimit;
  threshold_percent: number;
  pacing_enabled: boolean;
  burst_factor: number;
  /** 保底额度：动态额度过期后按它准入。 */
  floor: CapacityPoolLimit;
  grace_seconds: number;
};

/** 容量池里的一个 <渠道, 上游模型>；stale 表示已没有公开模型指向它。 */
export type CapacityPoolMember = {
  channel_id: number;
  channel_name: string;
  upstream_model: string;
  models: string[] | null;
  stale: boolean;
};

export type CapacityPool = {
  id: number;
  name: string;
  provider: ResourcePoolProvider;
  config: CapacityPoolConfig;
  config_error?: string;
  members: CapacityPoolMember[];
  updated_time: number;
};

export type CapacityDimensionDetail = {
  burst: number;
  pace: number;
  paced_accounts: number;
  computed: number;
  unbounded: boolean;
};

/** 动态额度的计算明细。 */
export type CapacityPoolDetail = {
  accounts: number;
  usable: number;
  excluded?: Record<string, number>;
  plans: { plan: string; total: number; usable: number }[] | null;
  rpm: CapacityDimensionDetail;
  tpm: CapacityDimensionDetail;
  concurrency: CapacityDimensionDetail;
  pacing: boolean;
  upstream_factor_accounts: number;
  learned?: {
    requests_per_unit: number;
    tokens_per_unit: number;
    unit: "credit" | "percent";
  };
};

export type CapacityPoolStatus = {
  status: string;
  error?: string;
  attempt_at: number;
};

/** 一个容量池此刻生效的额度、整池用量和每个成员的用量。 */
export type CapacityPoolCapacity = {
  id: number;
  pool_id: string;
  limit: CapacityPoolLimit;
  /** dynamic：主节点发布的动态额度；floor：还没发布或已过期，用保底额度。 */
  source: "dynamic" | "floor";
  exhausted: boolean;
  computed_at: number;
  detail?: CapacityPoolDetail;
  status?: CapacityPoolStatus;
  rpm_used: number;
  tpm_used: number;
  in_flight: number;
  remaining: {
    rpm: number | null;
    tpm: number | null;
    concurrency: number | null;
  };
  members: {
    channel_id: number;
    upstream_model: string;
    rpm_used: number;
    tpm_used: number;
    in_flight: number;
  }[];
};

export type CapacityPoolCapacityResult = {
  items: CapacityPoolCapacity[];
  shared: boolean;
  window_seconds: number;
};

const CAPACITY_POOLS_URL = "/api/channel/resource_pools/capacity_pools";

export async function getCapacityPools(
  provider: ResourcePoolProvider,
): Promise<CapacityPool[]> {
  const response = await api.get<{ success: boolean; data: CapacityPool[] }>(
    CAPACITY_POOLS_URL,
    { params: { provider } },
  );
  return requireServerSuccess(response.data).data ?? [];
}

export async function saveCapacityPool(pool: {
  id: number;
  name: string;
  provider: ResourcePoolProvider;
  config: CapacityPoolConfig;
}): Promise<number> {
  const response = await api.post<{ success: boolean; data: { id: number } }>(
    CAPACITY_POOLS_URL,
    pool,
  );
  return requireServerSuccess(response.data).data.id;
}

export async function deleteCapacityPool(id: number): Promise<void> {
  const response = await api.delete<{ success: boolean }>(
    `${CAPACITY_POOLS_URL}/${id}`,
  );
  requireServerSuccess(response.data);
}

/** models 为空表示把渠道的全部模型加进来。 */
export async function addCapacityPoolMembers(
  poolId: number,
  channelId: number,
  models: string[],
): Promise<string[]> {
  const response = await api.post<{
    success: boolean;
    data: { added: string[] | null };
  }>(`${CAPACITY_POOLS_URL}/${poolId}/members`, {
    channel_id: channelId,
    models,
  });
  return requireServerSuccess(response.data).data.added ?? [];
}

export async function removeCapacityPoolMember(
  poolId: number,
  channelId: number,
  upstreamModel: string,
): Promise<void> {
  const response = await api.delete<{ success: boolean }>(
    `${CAPACITY_POOLS_URL}/${poolId}/members`,
    { params: { channel_id: channelId, upstream_model: upstreamModel } },
  );
  requireServerSuccess(response.data);
}

export async function getCapacityPoolCapacity(
  provider: ResourcePoolProvider,
): Promise<CapacityPoolCapacityResult> {
  const response = await api.get<{
    success: boolean;
    data: CapacityPoolCapacityResult;
  }>(`${CAPACITY_POOLS_URL}/capacity`, { params: { provider } });
  return requireServerSuccess(response.data).data;
}
