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
import type {
  ResourcePoolAccount,
  ResourcePoolProvider,
  ResourcePoolSourceStatus,
  ResourcePoolUsage,
} from "./api";

export const POOL_PROVIDERS: readonly ResourcePoolProvider[] = [
  "claude",
  "gpt",
  "gemini",
  "grok",
];

// Claude 走 tokenmanagement（额度制）；GPT/Gemini/Grok 走 codex2api（7d 窗口制）。
export const WINDOWED_PROVIDERS: ReadonlySet<ResourcePoolProvider> = new Set([
  "gpt",
  "gemini",
  "grok",
]);

/** Accounts at or above this share are flagged as close to exhaustion. */
export const HIGH_USAGE_PERCENT = 80;
/** Accounts at or above this share are treated as exhausted. */
export const EXHAUSTED_USAGE_PERCENT = 95;

/** Shared query key so the overview and the per-provider tabs reuse one cache entry. */
export const usageQueryKey = (provider: ResourcePoolProvider) =>
  ["resource-pools", "usage", provider] as const;

/** codex2api statuses whose 7d window numbers are no longer being refreshed. */
const STALE_WINDOW_STATUSES: ReadonlySet<string> = new Set([
  "error",
  "unauthorized",
  "banned",
]);

const clampPercent = (value: number) => Math.min(100, Math.max(0, value));

/**
 * 7d window share of one codex2api account, 0–100. Grok reports
 * grok_billing.weekly_percent; Codex and Gemini report usage_percent_7d.
 */
export function windowUsagePercent(account: ResourcePoolAccount): number {
  return clampPercent(
    account.weekly_percent ??
      account.usage_percent_7d ??
      account.usage_percentage ??
      0,
  );
}

/** Whether the account reports a 5h window (antigravity / Gemini pools do). */
export function hasFiveHourWindow(account: ResourcePoolAccount): boolean {
  return account.usage_percent_5h !== undefined && account.usage_percent_5h !== null;
}

/**
 * 5h window share of one account, 0–100. A window whose reset already passed
 * has rolled over upstream, so it counts as unused until the next refresh.
 */
export function fiveHourUsagePercent(
  account: ResourcePoolAccount,
  now: number,
): number {
  if (account.reset_5h_at) {
    const resetAt = new Date(account.reset_5h_at).getTime();
    if (!Number.isNaN(resetAt) && resetAt <= now) return 0;
  }
  return clampPercent(account.usage_percent_5h ?? 0);
}

/** Quota share of one tokenmanagement (Claude) account, 0–100. */
export function quotaUsagePercent(account: ResourcePoolAccount): number {
  if (account.usage_percentage) return clampPercent(account.usage_percentage);
  if (account.usage_limit > 0)
    {return clampPercent((account.usage_current / account.usage_limit) * 100);}
  return 0;
}

/**
 * Whether an account's 7d numbers are current: the upstream still refreshes
 * it and its window has not already rolled over without a refresh.
 */
export function isWindowLive(
  account: ResourcePoolAccount,
  now: number,
): boolean {
  if (!account.enabled) return false;
  if (account.status && STALE_WINDOW_STATUSES.has(account.status)) return false;
  return !isWindowExpired(account, now);
}

/** Whether the account's 7d window already rolled over without a refresh. */
export function isWindowExpired(
  account: ResourcePoolAccount,
  now: number,
): boolean {
  if (!account.reset_7d_at) return false;
  const resetAt = new Date(account.reset_7d_at).getTime();
  return !Number.isNaN(resetAt) && resetAt <= now;
}

export type WindowStatusKind =
  "active" | "rate_limited" | "error" | "unauthorized" | "disabled";

export const WINDOW_STATUS_OPTIONS: {
  value: WindowStatusKind;
  label: string;
}[] = [
  { value: "active", label: "Active" },
  { value: "rate_limited", label: "Rate limited" },
  { value: "error", label: "Error" },
  { value: "unauthorized", label: "Unauthorized" },
  { value: "disabled", label: "Disabled" },
];

/** Buckets a codex2api account status, falling back to the enabled flag. */
export function windowStatusKind(
  account: ResourcePoolAccount,
): WindowStatusKind {
  if (!account.enabled) return "disabled";
  switch (account.status) {
    case "error":
    case "banned":
      return "error";
    case "unauthorized":
      return "unauthorized";
    case "rate_limited":
    case "cooldown":
      return "rate_limited";
    default:
      return "active";
  }
}

/** Account states that take an account out of rotation until someone fixes it. */
const UNHEALTHY_WINDOW_KINDS: ReadonlySet<WindowStatusKind> = new Set([
  "error",
  "unauthorized",
  "disabled",
]);

export type WindowSummary = {
  live: number;
  average: number;
  peak: number;
  high: number;
  nextReset?: string;
};

/** Earliest reset still in the future among the given ISO timestamps. */
function earliestFutureReset(
  values: (string | undefined)[],
  now: number,
): string | undefined {
  let next: string | undefined;
  let nextAt = Number.POSITIVE_INFINITY;
  for (const value of values) {
    const at = value ? new Date(value).getTime() : Number.NaN;
    if (at > now && at < nextAt) {
      nextAt = at;
      next = value;
    }
  }
  return next;
}

/** Aggregates the live accounts of a codex2api pool into 7d window figures. */
export function summarizeWindow(
  accounts: ResourcePoolAccount[],
  now: number,
): WindowSummary {
  const live = accounts.filter((account) => isWindowLive(account, now));
  const percents = live.map(windowUsagePercent);
  return {
    live: live.length,
    average: percents.length
      ? percents.reduce((sum, pct) => sum + pct, 0) / percents.length
      : 0,
    peak: percents.length ? Math.max(...percents) : 0,
    high: percents.filter((pct) => pct >= HIGH_USAGE_PERCENT).length,
    nextReset: earliestFutureReset(
      live.map((account) => account.reset_7d_at),
      now,
    ),
  };
}

/**
 * Aggregates the 5h window of the live accounts that report one; undefined
 * when no account in the set has a 5h window.
 */
export function summarizeFiveHour(
  accounts: ResourcePoolAccount[],
  now: number,
): WindowSummary | undefined {
  if (!accounts.some(hasFiveHourWindow)) return undefined;
  const live = accounts.filter(
    (account) => hasFiveHourWindow(account) && isWindowLive(account, now),
  );
  const percents = live.map((account) => fiveHourUsagePercent(account, now));
  return {
    live: live.length,
    average: percents.length
      ? percents.reduce((sum, pct) => sum + pct, 0) / percents.length
      : 0,
    peak: percents.length ? Math.max(...percents) : 0,
    high: percents.filter((pct) => pct >= HIGH_USAGE_PERCENT).length,
    nextReset: earliestFutureReset(
      live.map((account) => account.reset_5h_at),
      now,
    ),
  };
}

/** Water level = total used quota / total quota limit of the account set. */
export function waterLevel(accounts: ResourcePoolAccount[]): number {
  const limit = accounts.reduce((sum, account) => sum + account.usage_limit, 0);
  const used = accounts.reduce(
    (sum, account) => sum + account.usage_current,
    0,
  );
  if (limit > 0) return clampPercent((used / limit) * 100);
  if (!accounts.length) return 0;
  const pct = accounts.reduce(
    (sum, account) => sum + (account.usage_percentage || 0),
    0,
  );
  return clampPercent(pct / accounts.length);
}

/** One channel (source) row in the channel-stats tab. */
export type SourceRow = {
  sourceId: number;
  name: string;
  /** Distinct account groups of the source, falling back to its config. */
  groups: string[];
  accounts: ResourcePoolAccount[];
};

/**
 * The account's own group. Kiro (tokenmanagement) sources mix several groups
 * (aws1 / aws2 ...) behind one endpoint, so the per-account value wins over
 * the source's configured group.
 */
export function accountGroup(account: ResourcePoolAccount): string {
  return (
    account.groups?.find((group) => group.trim()) ||
    account.source_channel ||
    account.source_group ||
    ""
  );
}

export type GroupRow = {
  group: string;
  accounts: ResourcePoolAccount[];
  enabled: number;
  usage: number;
  limit: number;
  /** Quota water level of the enabled accounts; null when none is enabled. */
  level: number | null;
  rpmCurrent: number;
  rpmLimit: number;
  nextReset?: string;
};

/** Earliest upcoming quota reset (next_reset_date) of the accounts. */
export function nextQuotaReset(
  accounts: ResourcePoolAccount[],
  now: number,
): string | undefined {
  return earliestFutureReset(
    accounts.map((account) => account.next_reset_date),
    now,
  );
}

/** Aggregates quota-based (Kiro) accounts per account group. */
export function summarizeByGroup(
  accounts: ResourcePoolAccount[],
  now: number,
): GroupRow[] {
  const buckets = new Map<string, ResourcePoolAccount[]>();
  for (const account of accounts) {
    const key = accountGroup(account);
    const list = buckets.get(key);
    if (list) list.push(account);
    else buckets.set(key, [account]);
  }
  return [...buckets.entries()]
    .map(([group, list]) => {
      const enabled = list.filter((account) => account.enabled);
      const usage = enabled.reduce((sum, a) => sum + a.usage_current, 0);
      const limit = enabled.reduce((sum, a) => sum + a.usage_limit, 0);
      return {
        group,
        accounts: list,
        enabled: enabled.length,
        usage,
        limit,
        level: enabled.length ? waterLevel(enabled) : null,
        rpmCurrent: enabled.reduce((sum, a) => sum + (a.rpm_current ?? 0), 0),
        rpmLimit: enabled.reduce((sum, a) => sum + (a.rpm_limit ?? 0), 0),
        nextReset: nextQuotaReset(list, now),
      };
    })
    .sort((a, b) => a.group.localeCompare(b.group));
}

/**
 * Groups accounts by the source they came from. Successful sources that
 * returned no accounts still get a row, so an empty upstream channel is
 * visible instead of silently missing.
 */
export function summarizeBySource(
  accounts: ResourcePoolAccount[],
  statuses: ResourcePoolSourceStatus[],
  groupOf: (sourceId: number) => string | undefined,
): SourceRow[] {
  const rows = new Map<number, SourceRow>();
  const ensure = (sourceId: number, name: string) => {
    let row = rows.get(sourceId);
    if (!row) {
      row = { sourceId, name, groups: [], accounts: [] };
      rows.set(sourceId, row);
    }
    return row;
  };
  for (const status of statuses) {
    if (status.status === "success")
      {ensure(status.source_id, status.source_name);}
  }
  for (const account of accounts) {
    const row = ensure(account.source_id, account.source_name);
    row.accounts.push(account);
    const group = accountGroup(account);
    if (group && !row.groups.includes(group)) row.groups.push(group);
  }
  for (const row of rows.values()) {
    const fallback = groupOf(row.sourceId);
    if (!row.groups.length && fallback) row.groups.push(fallback);
    row.groups.sort();
  }
  return [...rows.values()].sort(
    (a, b) => b.accounts.length - a.accounts.length,
  );
}

/** Usage bands of the overview distribution bar, low to high. */
export const USAGE_BANDS = [
  { key: "low", label: "< 50%", max: 50, tone: "bg-emerald-500" },
  {
    key: "medium",
    label: "50–80%",
    max: HIGH_USAGE_PERCENT,
    tone: "bg-lime-500",
  },
  {
    key: "high",
    label: "80–95%",
    max: EXHAUSTED_USAGE_PERCENT,
    tone: "bg-amber-500",
  },
  {
    key: "exhausted",
    label: "≥ 95%",
    max: Number.POSITIVE_INFINITY,
    tone: "bg-red-500",
  },
] as const;

export type PoolAccountUsage = {
  account: ResourcePoolAccount;
  percent: number;
};

/** Pool-level figures the overview shows for one provider. */
export type PoolSummary = {
  provider: ResourcePoolProvider;
  windowed: boolean;
  /** Water level, 0–100; null when no account reports usage. */
  level: number | null;
  total: number;
  /** Accounts whose usage numbers are current (enabled, not stale or expired). */
  reporting: number;
  /** Reporting accounts still below the exhaustion threshold. */
  available: number;
  high: number;
  exhausted: number;
  /** Accounts the upstream reports as disabled or broken. */
  unhealthy: number;
  bands: number[];
  top: PoolAccountUsage[];
  nextReset?: string;
  /** 5h window figures; undefined when the pool reports no 5h window. */
  fiveHour?: WindowSummary;
  sourcesOk: number;
  sourcesFailed: number;
  sourcesTotal: number;
};

/** Folds one provider's usage poll into the overview figures. */
export function summarizePool(
  provider: ResourcePoolProvider,
  usage: ResourcePoolUsage | undefined,
  now: number,
): PoolSummary {
  const windowed = WINDOWED_PROVIDERS.has(provider);
  const accounts = usage?.accounts ?? [];
  const statuses = usage?.sources ?? [];
  const reporting = windowed
    ? accounts.filter((account) => isWindowLive(account, now))
    : accounts.filter((account) => account.enabled);
  const usages: PoolAccountUsage[] = reporting.map((account) => ({
    account,
    percent: windowed
      ? windowUsagePercent(account)
      : quotaUsagePercent(account),
  }));

  const bands: number[] = USAGE_BANDS.map(() => 0);
  for (const { percent } of usages) {
    bands[USAGE_BANDS.findIndex((band) => percent < band.max)] += 1;
  }

  let level: number | null = null;
  if (windowed && usages.length)
    {level = usages.reduce((sum, item) => sum + item.percent, 0) / usages.length;}
  if (!windowed && accounts.length) level = waterLevel(accounts);

  const resets = windowed
    ? reporting.map((account) => account.reset_7d_at)
    : reporting.map((account) => account.next_reset_date);

  return {
    provider,
    windowed,
    level,
    total: accounts.length,
    reporting: reporting.length,
    available: usages.filter((item) => item.percent < EXHAUSTED_USAGE_PERCENT)
      .length,
    high: usages.filter((item) => item.percent >= HIGH_USAGE_PERCENT).length,
    exhausted: usages.filter((item) => item.percent >= EXHAUSTED_USAGE_PERCENT)
      .length,
    unhealthy: windowed
      ? accounts.filter((account) =>
          UNHEALTHY_WINDOW_KINDS.has(windowStatusKind(account)),
        ).length
      : accounts.filter((account) => !account.enabled).length,
    bands,
    top: [...usages].sort((a, b) => b.percent - a.percent).slice(0, 3),
    nextReset: earliestFutureReset(resets, now),
    fiveHour: windowed ? summarizeFiveHour(accounts, now) : undefined,
    sourcesOk: statuses.filter((status) => status.status === "success").length,
    sourcesFailed: statuses.filter((status) => status.status === "failed")
      .length,
    sourcesTotal: statuses.length,
  };
}

/** Tone of a water level: red when exhausted, amber when high. */
export function levelTone(
  percent: number | null,
): "muted" | "ok" | "high" | "exhausted" {
  if (percent === null) return "muted";
  if (percent >= EXHAUSTED_USAGE_PERCENT) return "exhausted";
  if (percent >= HIGH_USAGE_PERCENT) return "high";
  return "ok";
}

export const LEVEL_TEXT_TONE = {
  muted: "text-muted-foreground",
  ok: "text-emerald-600 dark:text-emerald-400",
  high: "text-amber-600 dark:text-amber-400",
  exhausted: "text-red-600 dark:text-red-400",
} as const;

/** Formats a reset timestamp as "MM-DD HH:mm" (date-only values as "MM-DD"). */
export function formatResetAt(
  iso: string | undefined,
  locale?: string,
): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  if (/^\d{4}-\d{2}-\d{2}$/.test(iso))
    {return d.toLocaleDateString(locale, { month: "2-digit", day: "2-digit" });}
  return d.toLocaleString(locale, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

/** Relative distance to a reset, e.g. "in 3 days" / "3 days ago"; empty when unknown. */
export function formatResetDistance(
  iso: string | undefined,
  now: number,
  locale?: string,
): string {
  if (!iso) return "";
  const resetAt = new Date(iso).getTime();
  if (Number.isNaN(resetAt)) return "";
  const minutes = Math.round((resetAt - now) / 60_000);
  const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (Math.abs(minutes) < 60) return format.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 48) return format.format(hours, "hour");
  return format.format(Math.round(hours / 24), "day");
}
