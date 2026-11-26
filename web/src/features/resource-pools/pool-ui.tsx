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
import { AlertTriangle, CircleCheck, CircleX } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

import type { ResourcePoolAccount } from "./api";
import {
  LEVEL_TEXT_TONE,
  USAGE_BANDS,
  formatResetAt,
  formatResetDistance,
  levelTone,
  windowStatusKind,
} from "./pool-metrics";
import type { ProviderMeta } from "./providers";

const LEVEL_BAR_TONE = {
  muted: "bg-muted-foreground/40",
  ok: "bg-emerald-500",
  high: "bg-amber-500",
  exhausted: "bg-red-500",
} as const;

const PROVIDER_ICON_SIZE = {
  xs: "size-5",
  sm: "size-7",
  md: "size-9",
} as const;

export function ProviderIcon(props: {
  meta: ProviderMeta;
  size?: "xs" | "sm" | "md";
}) {
  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md",
        PROVIDER_ICON_SIZE[props.size ?? "sm"],
      )}
      style={{ background: props.meta.color }}
    >
      {props.meta.icon}
    </span>
  );
}

/** Reset time and its relative distance on one line; a reset already in the past is flagged. */
export function ResetAt(props: { iso?: string; now: number; locale?: string }) {
  const { t } = useTranslation();
  if (!props.iso)
    {return <span className="text-muted-foreground text-xs">-</span>;}
  const overdue = new Date(props.iso).getTime() <= props.now;
  return (
    <span className="inline-flex items-baseline gap-1.5 whitespace-nowrap">
      <span className={overdue ? "text-muted-foreground line-through" : ""}>
        {formatResetAt(props.iso, props.locale)}
      </span>
      <span
        data-table-text="secondary"
        className={cn(
          "text-xs",
          overdue
            ? "text-amber-600 dark:text-amber-400"
            : "text-muted-foreground",
        )}
      >
        {overdue
          ? t("Expired")
          : formatResetDistance(props.iso, props.now, props.locale)}
      </span>
    </span>
  );
}

/** Horizontal usage bar; turns amber/red as the share approaches exhaustion. */
export function UsageBar(props: {
  percent: number;
  digits: number;
  muted?: boolean;
  /** Draw the bar only; the caller shows the figure itself. */
  hideValue?: boolean;
  className?: string;
}) {
  const percent = Math.min(100, Math.max(0, props.percent));
  const tone = props.muted ? "muted" : levelTone(percent);
  return (
    <div className={cn("flex items-center gap-2", props.className)}>
      <div className="bg-muted h-1.5 min-w-16 flex-1 overflow-hidden rounded-full">
        <div
          className={cn(
            "h-full rounded-full transition-all",
            LEVEL_BAR_TONE[tone],
          )}
          style={{ width: `${percent}%` }}
        />
      </div>
      {!props.hideValue && (
        <span
          className={cn(
            "w-12 shrink-0 text-right text-xs font-medium tabular-nums",
            LEVEL_TEXT_TONE[tone],
          )}
        >
          {percent.toFixed(props.digits)}%
        </span>
      )}
    </div>
  );
}

/**
 * Circular water-level gauge; null renders an empty ring with a dash. The
 * label names the gauge for assistive tech and as a tooltip.
 */
export function WaterRing(props: {
  percent: number | null;
  label: string;
  size?: number;
}) {
  const size = props.size ?? 84;
  const strokeWidth = 8;
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const percent =
    props.percent === null ? 0 : Math.min(100, Math.max(0, props.percent));
  const tone = levelTone(props.percent);
  return (
    <div
      className="relative shrink-0"
      style={{ width: size, height: size }}
      title={props.label}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        className={cn("-rotate-90", LEVEL_TEXT_TONE[tone])}
        role="img"
        aria-label={`${props.label} ${props.percent === null ? "-" : `${percent.toFixed(0)}%`}`}
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          strokeWidth={strokeWidth}
          className="stroke-muted"
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeWidth={strokeWidth}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - percent / 100)}
          className="transition-[stroke-dashoffset] duration-700"
        />
      </svg>
      <div
        aria-hidden="true"
        className={cn(
          "absolute inset-0 flex items-center justify-center text-xl font-semibold tabular-nums",
          LEVEL_TEXT_TONE[tone],
        )}
      >
        {props.percent === null ? "-" : `${percent.toFixed(0)}%`}
      </div>
    </div>
  );
}

/** Stacked bar of how many accounts sit in each usage band. */
export function UsageBandBar(props: { bands: number[] }) {
  const total = props.bands.reduce((sum, count) => sum + count, 0);
  return (
    <div className="space-y-1">
      <div className="bg-muted flex h-1.5 overflow-hidden rounded-full">
        {total > 0 &&
          USAGE_BANDS.map((band, index) =>
            props.bands[index] ? (
              <div
                key={band.key}
                className={cn("h-full", band.tone)}
                style={{ width: `${(props.bands[index] / total) * 100}%` }}
                title={`${band.label}: ${props.bands[index]}`}
              />
            ) : null,
          )}
      </div>
      <div className="text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
        {USAGE_BANDS.map((band, index) => (
          <span key={band.key} className="inline-flex items-center gap-1">
            <span className={cn("size-2 rounded-full", band.tone)} />
            {band.label}
            <span className="text-foreground font-medium tabular-nums">
              {props.bands[index]}
            </span>
          </span>
        ))}
      </div>
    </div>
  );
}

/** codex2api account status, falling back to the enabled flag. */
export function WindowStatusBadge(props: { account: ResourcePoolAccount }) {
  const { t } = useTranslation();
  switch (windowStatusKind(props.account)) {
    case "disabled":
      return (
        <Badge variant="destructive">
          <CircleX />
          {t("Disabled")}
        </Badge>
      );
    case "error":
      return (
        <Badge variant="destructive">
          <CircleX />
          {t("Error")}
        </Badge>
      );
    case "unauthorized":
      return (
        <Badge variant="destructive">
          <CircleX />
          {t("Unauthorized")}
        </Badge>
      );
    case "rate_limited":
      return (
        <Badge variant="outline" className="text-amber-600">
          <AlertTriangle />
          {t("Rate limited")}
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="text-emerald-600">
          <CircleCheck />
          {t("Active")}
        </Badge>
      );
  }
}

export function Metric(props: {
  label: string;
  value: string;
  hint: string;
  icon?: React.ReactNode;
  tone?: keyof typeof LEVEL_TEXT_TONE;
}) {
  return (
    <div className="bg-muted/50 min-w-0 rounded-lg px-3 py-2">
      <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
        {props.icon}
        <span className="truncate">{props.label}</span>
      </p>
      <p className="mt-0.5 flex min-w-0 items-baseline gap-2">
        <span
          className={cn(
            "shrink-0 text-lg leading-6 font-semibold tabular-nums",
            props.tone && LEVEL_TEXT_TONE[props.tone],
          )}
        >
          {props.value}
        </span>
        <span
          className="text-muted-foreground truncate text-xs"
          title={props.hint}
        >
          {props.hint}
        </span>
      </p>
    </div>
  );
}
