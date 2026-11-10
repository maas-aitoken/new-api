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
import {
  AlertTriangle,
  ArrowRight,
  Gauge,
  Layers,
  ServerCrash,
  Users,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

import type { ResourcePoolProvider, ResourcePoolUsage } from "./api";
import {
  HIGH_USAGE_PERCENT,
  levelTone,
  summarizePool,
  type PoolSummary,
  LEVEL_TEXT_TONE,
  formatResetAt,
  formatResetDistance,
} from "./pool-metrics";
import {
  Metric,
  ProviderIcon,
  UsageBandBar,
  UsageBar,
  WaterRing,
} from "./pool-ui";
import { PROVIDERS, type ProviderMeta } from "./providers";

export type PoolQueryState = {
  data?: ResourcePoolUsage;
  isPending: boolean;
  error: Error | null;
};

export type PoolDetailTab = "channels" | "accounts" | "sources";

/** Water-level overview of every provider's pool on one screen. */
export function ResourcePoolOverview(props: {
  pools: Record<ResourcePoolProvider, PoolQueryState>;
  now: number;
  locale?: string;
  onOpen: (provider: ResourcePoolProvider, tab: PoolDetailTab) => void;
}) {
  const { t } = useTranslation();
  const summaries = PROVIDERS.map((meta) => ({
    meta,
    state: props.pools[meta.key],
    summary: summarizePool(meta.key, props.pools[meta.key].data, props.now),
  }));
  const loaded = summaries.filter((item) => item.state.data);
  const total = loaded.reduce((sum, item) => sum + item.summary.total, 0);
  const available = loaded.reduce(
    (sum, item) => sum + item.summary.available,
    0,
  );
  const reporting = loaded.reduce(
    (sum, item) => sum + item.summary.reporting,
    0,
  );
  const high = loaded.reduce((sum, item) => sum + item.summary.high, 0);
  const sourcesTotal = loaded.reduce(
    (sum, item) => sum + item.summary.sourcesTotal,
    0,
  );
  const sourcesFailed = loaded.reduce(
    (sum, item) => sum + item.summary.sourcesFailed,
    0,
  );
  const pressured = loaded.filter(
    (item) =>
      levelTone(item.summary.level) === "exhausted" ||
      levelTone(item.summary.level) === "high",
  );

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric
          icon={<Users className="size-3.5" />}
          label={t("Total accounts")}
          value={formatNumber(total, props.locale)}
          hint={t("{{count}} reporting usage", { count: reporting })}
        />
        <Metric
          icon={<Gauge className="size-3.5" />}
          label={t("Available accounts")}
          value={formatNumber(available, props.locale)}
          hint={t("Reporting and below {{percent}}%", { percent: 95 })}
          tone={available ? "ok" : "exhausted"}
        />
        <Metric
          icon={<AlertTriangle className="size-3.5" />}
          label={t("High usage accounts")}
          value={formatNumber(high, props.locale)}
          hint={
            pressured.length
              ? t("Pools under pressure: {{pools}}", {
                  pools: pressured.map((item) => item.meta.label).join(", "),
                })
              : t("At or above {{percent}}%", { percent: HIGH_USAGE_PERCENT })
          }
          tone={high ? "high" : undefined}
        />
        <Metric
          icon={<ServerCrash className="size-3.5" />}
          label={t("Interface count")}
          value={`${sourcesTotal - sourcesFailed}/${sourcesTotal}`}
          hint={
            sourcesFailed
              ? t("{{count}} sources failed", { count: sourcesFailed })
              : t("All sources reachable")
          }
          tone={sourcesFailed ? "exhausted" : undefined}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {summaries.map(({ meta, state, summary }) => (
          <PoolCard
            key={meta.key}
            meta={meta}
            state={state}
            summary={summary}
            now={props.now}
            locale={props.locale}
            onOpen={(tab) => props.onOpen(meta.key, tab)}
          />
        ))}
      </div>
    </div>
  );
}

function PoolCard(props: {
  meta: ProviderMeta;
  state: PoolQueryState;
  summary: PoolSummary;
  now: number;
  locale?: string;
  onOpen: (tab: PoolDetailTab) => void;
}) {
  const { t } = useTranslation();
  const { meta, state, summary } = props;
  const refreshedAt = state.data?.refreshed_at
    ? state.data.refreshed_at * 1000
    : undefined;

  return (
    <Card className="overflow-hidden">
      <div className="h-1" style={{ background: meta.color }} />
      <CardContent className="space-y-4 p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <ProviderIcon meta={meta} size="md" />
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="font-semibold">{meta.label}</span>
                <Badge variant="outline" className="text-[11px]">
                  {summary.windowed ? t("7d window") : t("Quota-based")}
                </Badge>
              </div>
              <p className="text-muted-foreground truncate text-xs">
                {refreshedAt
                  ? t("Updated {{time}}", {
                      time: formatResetDistance(
                        new Date(refreshedAt).toISOString(),
                        props.now,
                        props.locale,
                      ),
                    })
                  : t("Loading...")}
              </p>
            </div>
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => props.onOpen("channels")}
          >
            {t("View details")}
            <ArrowRight />
          </Button>
        </div>

        <PoolCardBody {...props} />
      </CardContent>
    </Card>
  );
}

function PoolCardBody(props: {
  state: PoolQueryState;
  summary: PoolSummary;
  now: number;
  locale?: string;
  onOpen: (tab: PoolDetailTab) => void;
}) {
  const { t } = useTranslation();
  const { state, summary } = props;

  if (!state.data && state.isPending) {
    return (
      <div className="flex items-center gap-5">
        <Skeleton className="size-28 rounded-full" />
        <div className="flex-1 space-y-2">
          <Skeleton className="h-4 w-3/4" />
          <Skeleton className="h-4 w-1/2" />
          <Skeleton className="h-4 w-2/3" />
        </div>
      </div>
    );
  }
  if (!state.data) {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-sm text-red-700 dark:text-red-400">
        <AlertTriangle className="mt-0.5 size-4 shrink-0" />
        <span className="min-w-0 break-all">
          {state.error?.message || t("Failed")}
        </span>
      </div>
    );
  }
  if (!summary.sourcesTotal) {
    return (
      <div className="text-muted-foreground flex flex-col items-center gap-2 py-6 text-sm">
        <Layers className="size-6" />
        {t("No sources yet")}
        <Button
          variant="outline"
          size="sm"
          onClick={() => props.onOpen("sources")}
        >
          {t("Interface config")}
        </Button>
      </div>
    );
  }

  return (
    <>
      <div className="flex items-center gap-5">
        <WaterRing
          percent={summary.level}
          label={summary.windowed ? t("Usage (7d)") : t("Pool water level")}
        />
        <dl className="grid flex-1 grid-cols-2 gap-x-4 gap-y-3 text-sm">
          <PoolStat
            label={t("Available accounts")}
            value={`${summary.available}/${summary.total}`}
            tone={summary.available ? undefined : "exhausted"}
          />
          <PoolStat
            label={t("High usage accounts")}
            value={String(summary.high)}
            hint={
              summary.exhausted
                ? t("{{count}} exhausted", { count: summary.exhausted })
                : undefined
            }
            tone={summary.high ? "high" : undefined}
          />
          <PoolStat
            label={t("Unhealthy accounts")}
            value={String(summary.unhealthy)}
            tone={summary.unhealthy ? "exhausted" : undefined}
          />
          <PoolStat
            label={t("Next reset")}
            value={formatResetAt(summary.nextReset, props.locale)}
            hint={
              formatResetDistance(summary.nextReset, props.now, props.locale) ||
              undefined
            }
          />
        </dl>
      </div>

      {summary.fiveHour && (
        <div className="space-y-1.5 rounded-lg border p-3">
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">{t("Usage (5h)")}</span>
            <span className="text-muted-foreground">
              {t("Next reset")}{" "}
              <span className="text-foreground font-medium">
                {formatResetAt(summary.fiveHour.nextReset, props.locale)}
              </span>
              {summary.fiveHour.nextReset && (
                <span className="ml-1">
                  {formatResetDistance(
                    summary.fiveHour.nextReset,
                    props.now,
                    props.locale,
                  )}
                </span>
              )}
            </span>
          </div>
          <UsageBar
            percent={summary.fiveHour.average}
            digits={1}
            muted={!summary.fiveHour.live}
          />
          <p className="text-muted-foreground text-xs">
            {t("Peak {{percent}}%, {{count}} accounts at or above {{high}}%", {
              percent: summary.fiveHour.peak.toFixed(0),
              count: summary.fiveHour.high,
              high: HIGH_USAGE_PERCENT,
            })}
          </p>
        </div>
      )}

      <UsageBandBar bands={summary.bands} />

      <div className="space-y-2">
        <p className="text-muted-foreground text-xs">
          {t("Highest usage accounts")}
        </p>
        {summary.top.length ? (
          summary.top.map(({ account, percent }) => (
            <div
              key={`${account.source_id}-${account.id || account.email}`}
              className="flex items-center gap-3"
            >
              <span
                className="w-40 shrink-0 truncate text-xs"
                title={account.email}
              >
                {account.email || account.name || account.id}
              </span>
              <UsageBar percent={percent} digits={0} className="flex-1" />
            </div>
          ))
        ) : (
          <p className="text-muted-foreground text-xs">
            {t("No accounts reporting usage")}
          </p>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3 text-xs">
        <span
          className={cn(
            "inline-flex items-center gap-1.5",
            summary.sourcesFailed
              ? LEVEL_TEXT_TONE.exhausted
              : "text-muted-foreground",
          )}
        >
          <span
            className={cn(
              "size-1.5 rounded-full",
              summary.sourcesFailed ? "bg-red-500" : "bg-emerald-500",
            )}
          />
          {t("{{ok}}/{{total}} sources reachable", {
            ok: summary.sourcesOk,
            total: summary.sourcesTotal,
          })}
        </span>
        <Button
          variant="link"
          size="sm"
          className="h-auto p-0 text-xs"
          onClick={() => props.onOpen("accounts")}
        >
          {t("Account details")}
        </Button>
      </div>
    </>
  );
}

function PoolStat(props: {
  label: string;
  value: string;
  hint?: string;
  tone?: keyof typeof LEVEL_TEXT_TONE;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground text-xs">{props.label}</dt>
      <dd
        className={cn(
          "truncate font-medium tabular-nums",
          props.tone && LEVEL_TEXT_TONE[props.tone],
        )}
      >
        {props.value}
        {props.hint && (
          <span className="text-muted-foreground ml-1.5 text-xs font-normal">
            {props.hint}
          </span>
        )}
      </dd>
    </div>
  );
}
