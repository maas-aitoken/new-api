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
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  AlertTriangle,
  CircleCheck,
  CircleX,
  Plus,
  RefreshCw,
  Save,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { SectionPageLayout } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toIntlLocale } from "@/i18n/languages";
import { formatCompactNumber, formatNumber } from "@/lib/format";
import { cn } from "@/lib/utils";

import {
  deleteResourcePoolSource,
  getResourcePoolSources,
  getResourcePoolUsage,
  saveResourcePoolSources,
  type ResourcePoolAccount,
  type ResourcePoolProvider,
  type ResourcePoolSource,
} from "./api";
import { CapacityPoolsTab } from "./components/capacity-pools-tab";
import { ResourcePoolOverview, type PoolDetailTab } from "./overview";
import {
  HIGH_USAGE_PERCENT,
  POOL_PROVIDERS,
  WINDOWED_PROVIDERS,
  WINDOW_STATUS_OPTIONS,
  fiveHourUsagePercent,
  hasFiveHourWindow,
  summarizeFiveHour,
  isWindowExpired,
  isWindowLive,
  levelTone,
  quotaUsagePercent,
  summarizeBySource,
  summarizePool,
  summarizeWindow,
  usageQueryKey,
  waterLevel,
  windowStatusKind,
  windowUsagePercent,
  type WindowStatusKind,
  LEVEL_TEXT_TONE,
  formatResetAt,
  formatResetDistance,
  accountGroup,
  nextQuotaReset,
  summarizeByGroup,
} from "./pool-metrics";
import {
  Metric,
  ProviderIcon,
  ResetAt,
  UsageBar,
  WindowStatusBadge,
} from "./pool-ui";
import { PROVIDERS } from "./providers";

type EditableSource = {
  id: number;
  name: string;
  endpoint: string;
  admin_password: string;
  group_name: string;
  enabled: boolean;
};

const emptySource = (): EditableSource => ({
  id: 0,
  name: "",
  endpoint: "",
  admin_password: "",
  group_name: "",
  enabled: true,
});

// The API returns a masked secret; keep the editor blank so an untouched row
// never echoes the mask back as if it were the real password.
const toEditable = (source: ResourcePoolSource): EditableSource => ({
  id: source.id,
  name: source.name,
  endpoint: source.endpoint,
  admin_password: "",
  group_name: source.group_name,
  enabled: source.enabled,
});

type SourceDraft = {
  provider: ResourcePoolProvider;
  rows: EditableSource[];
};

type PoolView = "overview" | PoolDetailTab;

/** 各 provider 在「接口配置」里的 endpoint 占位符。 */
const ENDPOINT_PLACEHOLDERS: Record<ResourcePoolProvider, string> = {
  claude: "https://tokenmanagement.intsig.net/api/admin/accounts/usage",
  gpt: "https://tokenproxy.intsig.net/api/admin/accounts?channel=codex",
  gemini:
    "https://tokenproxy.intsig.net/api/admin/accounts?channel=antigravity",
  grok: "https://tokenproxy.intsig.net/api/admin/accounts?channel=grok",
};

/** Placeholder rows while a provider's first poll is in flight. */
function LoadingRows(props: { columns: number }) {
  return Array.from({ length: 4 }, (_, row) => (
    <TableRow key={row}>
      {Array.from({ length: props.columns }, (_, column) => (
        <TableCell key={column}>
          <Skeleton className="h-4 w-full max-w-32" />
        </TableCell>
      ))}
    </TableRow>
  ));
}

export function ResourcePools() {
  const { t, i18n } = useTranslation();
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language);
  const queryClient = useQueryClient();
  const [view, setView] = useState<PoolView>("overview");
  const [provider, setProvider] = useState<ResourcePoolProvider>("claude");
  const [draft, setDraft] = useState<SourceDraft | null>(null);
  // Account-detail filters for 7d window pools; expired and disabled accounts are hidden by default.
  const [hideExpired, setHideExpired] = useState(true);
  const [hideDisabled, setHideDisabled] = useState(true);
  const [statusFilter, setStatusFilter] = useState<WindowStatusKind | "all">(
    "all",
  );
  const [accountKeyword, setAccountKeyword] = useState("");

  const sourcesQuery = useQuery({
    queryKey: ["resource-pools", "sources", provider],
    queryFn: () => getResourcePoolSources(provider),
    staleTime: 30_000,
    retry: false,
  });

  // A draft only applies to the provider it was created under — switching
  // providers must never leak another provider's rows into the table.
  const activeDraft = draft && draft.provider === provider ? draft.rows : null;
  const sources: EditableSource[] =
    activeDraft ?? (sourcesQuery.data ?? []).map(toEditable);

  // Every provider is polled up front: the overview needs all four, and
  // switching providers then reads a warm cache instead of waiting ~2s on
  // codex2api. The server caches polls as well, so this stays cheap.
  const poolQueries = useQueries({
    queries: POOL_PROVIDERS.map((key) => ({
      queryKey: usageQueryKey(key),
      queryFn: () => getResourcePoolUsage(key),
      refetchInterval: 60_000,
      staleTime: 30_000,
      retry: false,
    })),
  });
  const usageQuery = poolQueries[POOL_PROVIDERS.indexOf(provider)];

  const invalidateProvider = (key: ResourcePoolProvider) => {
    void queryClient.invalidateQueries({
      queryKey: ["resource-pools", "sources", key],
    });
    void queryClient.invalidateQueries({ queryKey: usageQueryKey(key) });
  };

  const saveMutation = useMutation({
    mutationFn: () =>
      saveResourcePoolSources(
        provider,
        sources.map((source) => ({
          id: source.id,
          name: source.name,
          endpoint: source.endpoint,
          admin_password: source.admin_password,
          group_name: source.group_name,
          enabled: source.enabled,
        })),
      ),
    onSuccess: () => {
      setDraft(null);
      invalidateProvider(provider);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteResourcePoolSource(id),
    onSuccess: () => {
      setDraft(null);
      invalidateProvider(provider);
    },
  });

  // The refresh button bypasses the server cache: every pool on the
  // overview, otherwise only the provider on screen.
  const refreshMutation = useMutation({
    mutationFn: async (targets: readonly ResourcePoolProvider[]) => {
      await Promise.allSettled(
        targets.map(async (key) => {
          const usage = await getResourcePoolUsage(key, true);
          queryClient.setQueryData(usageQueryKey(key), usage);
        }),
      );
    },
  });
  const refreshing =
    refreshMutation.isPending ||
    (view === "overview"
      ? poolQueries.some((query) => query.isFetching)
      : usageQuery.isFetching);

  const accounts = usageQuery.data?.accounts ?? [];
  const statuses = usageQuery.data?.sources ?? [];
  const usageLoading = usageQuery.isPending && !usageQuery.data;
  const successCount = statuses.filter(
    (item) => item.status === "success",
  ).length;
  const failedCount = statuses.filter(
    (item) => item.status === "failed",
  ).length;
  const totalUsage = accounts.reduce(
    (sum, account) => sum + account.usage_current,
    0,
  );
  const totalLimit = accounts.reduce(
    (sum, account) => sum + account.usage_limit,
    0,
  );
  const activeCount = accounts.filter((account) => account.enabled).length;
  const overallShare = waterLevel(accounts);
  const sourceRows = summarizeBySource(
    accounts,
    statuses,
    (sourceId) => sources.find((source) => source.id === sourceId)?.group_name,
  );
  const isWindowedPool = WINDOWED_PROVIDERS.has(provider);
  const groupRows = isWindowedPool
    ? []
    : summarizeByGroup(accounts, Date.now());
  // Reset and expiry checks run against the render time; the 60s refetch
  // re-renders often enough for day-level windows.
  const now = Date.now();
  const poolWindow = summarizeWindow(accounts, now);
  const poolFiveHour = isWindowedPool
    ? summarizeFiveHour(accounts, now)
    : undefined;
  const expiredCount = accounts.filter((account) =>
    isWindowExpired(account, now),
  ).length;
  const disabledCount = accounts.filter((account) => !account.enabled).length;
  const keyword = accountKeyword.trim().toLowerCase();
  const visibleAccounts = accounts.filter((account) => {
    if (hideExpired && isWindowExpired(account, now)) return false;
    // Explicitly picking the "Disabled" status overrides the default hide.
    if (hideDisabled && statusFilter !== "disabled" && !account.enabled)
      {return false;}
    if (statusFilter !== "all" && windowStatusKind(account) !== statusFilter)
      {return false;}
    if (!keyword) return true;
    return [account.email, account.name, account.id, account.source_name].some(
      (value) =>
        String(value ?? "")
          .toLowerCase()
          .includes(keyword),
    );
  });
  const endpointPlaceholder = ENDPOINT_PLACEHOLDERS[provider];
  const refreshedAt = usageQuery.data?.refreshed_at;

  const patchSource = (index: number, patch: Partial<EditableSource>) => {
    setDraft({
      provider,
      rows: sources.map((source, i) =>
        i === index ? { ...source, ...patch } : source,
      ),
    });
  };

  const switchProvider = (next: ResourcePoolProvider) => {
    if (next === provider) return;
    setDraft(null);
    setProvider(next);
  };

  const openPool = (next: ResourcePoolProvider, tab: PoolDetailTab) => {
    switchProvider(next);
    setView(tab);
  };

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className="inline-flex min-w-0 items-center gap-2">
          <span className="truncate">{t("Resource Pools")}</span>
          <Badge variant="outline" className="shrink-0">
            {t("Admin")}
          </Badge>
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {view !== "overview" && refreshedAt ? (
          <span className="text-muted-foreground hidden text-xs sm:inline">
            {t("Updated {{time}}", {
              time: formatResetDistance(
                new Date(refreshedAt * 1000).toISOString(),
                now,
                locale,
              ),
            })}
          </span>
        ) : null}
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            refreshMutation.mutate(
              view === "overview" ? POOL_PROVIDERS : [provider],
            )
          }
          disabled={refreshing}
        >
          <RefreshCw className={refreshing ? "animate-spin" : ""} />
          {t("Refresh")}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className="space-y-4">
          <Tabs
            value={view}
            onValueChange={(value) => setView(value as PoolView)}
          >
            <TabsList variant="line" className="w-full justify-start border-b">
              <TabsTrigger value="overview">{t("Overview")}</TabsTrigger>
              <TabsTrigger value="channels">{t("Channel stats")}</TabsTrigger>
              <TabsTrigger value="accounts">{t("Account details")}</TabsTrigger>
              <TabsTrigger value="sources">{t("Interface config")}</TabsTrigger>
              <TabsTrigger value="capacity">{t("Capacity pools")}</TabsTrigger>
            </TabsList>

            {view !== "overview" && (
              <div className="flex flex-wrap gap-3">
                {PROVIDERS.map((item, index) => {
                  const active = provider === item.key;
                  const level = summarizePool(
                    item.key,
                    poolQueries[index].data,
                    now,
                  ).level;
                  return (
                    <button
                      key={item.key}
                      type="button"
                      onClick={() => switchProvider(item.key)}
                      aria-pressed={active}
                      className={cn(
                        "bg-card flex cursor-pointer items-center gap-2 rounded-lg border py-1.5 pr-4 pl-1.5 text-sm transition-all",
                        active
                          ? "text-foreground font-medium shadow-md"
                          : "text-muted-foreground hover:bg-accent/50 hover:shadow-sm",
                      )}
                      style={active ? { borderColor: item.color } : undefined}
                    >
                      <ProviderIcon meta={item} />
                      <span className="flex flex-col items-start leading-tight">
                        {item.label}
                        <span
                          className={cn(
                            "text-[11px] tabular-nums",
                            LEVEL_TEXT_TONE[levelTone(level)],
                          )}
                        >
                          {level === null ? "-" : `${level.toFixed(0)}%`}
                        </span>
                      </span>
                    </button>
                  );
                })}
              </div>
            )}

            <TabsContent value="overview">
              <ResourcePoolOverview
                pools={{
                  claude: poolQueries[0],
                  gpt: poolQueries[1],
                  gemini: poolQueries[2],
                  grok: poolQueries[3],
                }}
                now={now}
                locale={locale}
                onOpen={openPool}
              />
            </TabsContent>

            <TabsContent value="capacity">
              <CapacityPoolsTab
                provider={provider}
                sources={sourcesQuery.data ?? []}
                accounts={accounts}
                locale={locale}
                now={now}
              />
            </TabsContent>

            <TabsContent value="sources" className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-muted-foreground text-sm">
                  {t("{{enabled}} of {{total}} sources enabled", {
                    enabled: sources.filter((source) => source.enabled).length,
                    total: sources.length,
                  })}
                </p>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      setDraft({ provider, rows: [...sources, emptySource()] })
                    }
                  >
                    <Plus />
                    {t("Add source")}
                  </Button>
                  <Button
                    size="sm"
                    onClick={() => saveMutation.mutate()}
                    disabled={saveMutation.isPending || activeDraft === null}
                  >
                    <Save />
                    {t("Save config")}
                  </Button>
                </div>
              </div>

              <Card>
                <CardContent className="p-0">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-56">{t("Channel")}</TableHead>
                        <TableHead>{t("Endpoint")}</TableHead>
                        <TableHead className="w-48">
                          {t("Admin password")}
                        </TableHead>
                        <TableHead className="w-32">{t("Group")}</TableHead>
                        <TableHead className="w-20">{t("Enabled")}</TableHead>
                        <TableHead className="w-20 text-right">
                          {t("Actions")}
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {sources.map((source, index) => (
                        <TableRow key={source.id || `new-${index}`}>
                          <TableCell>
                            <Input
                              value={source.name}
                              placeholder={`${t("Channel")} ${index + 1}`}
                              onChange={(event) =>
                                patchSource(index, { name: event.target.value })
                              }
                            />
                          </TableCell>
                          <TableCell>
                            <Input
                              value={source.endpoint}
                              placeholder={endpointPlaceholder}
                              onChange={(event) =>
                                patchSource(index, {
                                  endpoint: event.target.value,
                                })
                              }
                            />
                          </TableCell>
                          <TableCell>
                            <Input
                              type="password"
                              value={source.admin_password}
                              placeholder={
                                source.id
                                  ? t("Leave blank to keep current")
                                  : "********"
                              }
                              onChange={(event) =>
                                patchSource(index, {
                                  admin_password: event.target.value,
                                })
                              }
                            />
                          </TableCell>
                          <TableCell>
                            <Input
                              value={source.group_name}
                              placeholder={t("Group")}
                              onChange={(event) =>
                                patchSource(index, {
                                  group_name: event.target.value,
                                })
                              }
                            />
                          </TableCell>
                          <TableCell>
                            <Switch
                              checked={source.enabled}
                              onCheckedChange={(checked) =>
                                patchSource(index, { enabled: checked })
                              }
                            />
                          </TableCell>
                          <TableCell className="text-right">
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={!source.id || deleteMutation.isPending}
                              onClick={() => deleteMutation.mutate(source.id)}
                            >
                              <Trash2 />
                              {t("Delete")}
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))}
                      {!sources.length && (
                        <TableRow>
                          <TableCell
                            colSpan={6}
                            className="text-muted-foreground h-24 text-center"
                          >
                            {t("No sources yet")}
                          </TableCell>
                        </TableRow>
                      )}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="channels" className="space-y-3">
              <div
                className={cn(
                  "grid gap-3 sm:grid-cols-2",
                  poolFiveHour ? "xl:grid-cols-5" : "xl:grid-cols-4",
                )}
              >
                <Metric
                  label={t("Interface count")}
                  value={`${successCount}/${statuses.length}`}
                  hint={t("{{succeeded}} succeeded, {{failed}} failed", {
                    succeeded: successCount,
                    failed: failedCount,
                  })}
                />
                {isWindowedPool ? (
                  <>
                    <Metric
                      label={t("Account count")}
                      value={formatNumber(accounts.length, locale)}
                      hint={t("{{count}} reporting 7d usage", {
                        count: poolWindow.live,
                      })}
                    />
                    <Metric
                      label={t("Average usage (7d)")}
                      value={
                        poolWindow.live
                          ? `${poolWindow.average.toFixed(1)}%`
                          : "-"
                      }
                      tone={levelTone(
                        poolWindow.live ? poolWindow.average : null,
                      )}
                      hint={t("{{count}} accounts at or above {{percent}}%", {
                        count: poolWindow.high,
                        percent: HIGH_USAGE_PERCENT,
                      })}
                    />
                    <Metric
                      label={t("Next reset")}
                      value={formatResetAt(poolWindow.nextReset, locale)}
                      hint={formatResetDistance(
                        poolWindow.nextReset,
                        now,
                        locale,
                      )}
                    />
                    {poolFiveHour && (
                      <Metric
                        label={t("Average usage (5h)")}
                        value={
                          poolFiveHour.live
                            ? `${poolFiveHour.average.toFixed(1)}%`
                            : "-"
                        }
                        tone={levelTone(
                          poolFiveHour.live ? poolFiveHour.average : null,
                        )}
                        hint={t("Next 5h reset {{time}}", {
                          time:
                            formatResetDistance(
                              poolFiveHour.nextReset,
                              now,
                              locale,
                            ) || "-",
                        })}
                      />
                    )}
                  </>
                ) : (
                  <>
                    <Metric
                      label={t("Account count")}
                      value={formatNumber(accounts.length, locale)}
                      hint={t("{{active}} enabled", { active: activeCount })}
                    />
                    <Metric
                      label={t("Total usage")}
                      value={formatCompactNumber(totalUsage, locale)}
                      hint={t("Total {{total}}", {
                        total: formatCompactNumber(totalLimit, locale),
                      })}
                    />
                    <Metric
                      label={t("Overall share")}
                      value={`${Math.round(overallShare)}%`}
                      hint={t("{{count}} accounts at or above {{percent}}%", {
                        count: accounts.filter(
                          (account) =>
                            quotaUsagePercent(account) >= HIGH_USAGE_PERCENT,
                        ).length,
                        percent: HIGH_USAGE_PERCENT,
                      })}
                      tone={levelTone(overallShare)}
                    />
                  </>
                )}
              </div>

              {failedCount > 0 && (
                <div className="space-y-1 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 text-sm">
                  {statuses
                    .filter((item) => item.status === "failed")
                    .map((item) => (
                      <div
                        key={item.source_id}
                        className="flex items-start gap-2 text-amber-700 dark:text-amber-400"
                      >
                        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                        <span className="min-w-0 break-all">
                          {item.source_name || item.source_endpoint}:{" "}
                          {item.error}
                        </span>
                      </div>
                    ))}
                </div>
              )}

              {isWindowedPool ? (
                <Card>
                  <CardContent className="p-0">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t("Channel")}</TableHead>
                          <TableHead>{t("Group")}</TableHead>
                          <TableHead className="text-right">
                            {t("Accounts")}
                          </TableHead>
                          <TableHead className="w-56">
                            {t("Average usage (7d)")}
                          </TableHead>
                          <TableHead className="text-right">
                            {t("Peak usage (7d)")}
                          </TableHead>
                          <TableHead>{t("Next reset")}</TableHead>
                          {poolFiveHour && (
                            <TableHead className="w-56">
                              {t("Average usage (5h)")}
                            </TableHead>
                          )}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {usageLoading && (
                          <LoadingRows columns={poolFiveHour ? 7 : 6} />
                        )}
                        {sourceRows.map((row) => {
                          const rowWindow = summarizeWindow(row.accounts, now);
                          const rowFiveHour = poolFiveHour
                            ? summarizeFiveHour(row.accounts, now)
                            : undefined;
                          return (
                            <TableRow key={row.sourceId}>
                              <TableCell>
                                <span className="inline-flex rounded bg-blue-500/10 px-2 py-0.5 text-xs text-blue-600 dark:text-blue-400">
                                  {row.name}
                                </span>
                              </TableCell>
                              <TableCell>
                                <GroupChips groups={row.groups} />
                              </TableCell>
                              <TableCell className="text-right">
                                {row.accounts.length
                                  ? `${rowWindow.live}/${row.accounts.length}`
                                  : t("Upstream returned no accounts")}
                              </TableCell>
                              <TableCell>
                                <UsageBar
                                  percent={rowWindow.average}
                                  digits={1}
                                />
                              </TableCell>
                              <TableCell className="text-right">
                                {rowWindow.peak.toFixed(0)}%
                              </TableCell>
                              <TableCell>
                                <ResetAt
                                  iso={rowWindow.nextReset}
                                  now={now}
                                  locale={locale}
                                />
                              </TableCell>
                              {poolFiveHour && (
                                <TableCell>
                                  {rowFiveHour ? (
                                    <UsageBar
                                      percent={rowFiveHour.average}
                                      digits={1}
                                      muted={!rowFiveHour.live}
                                    />
                                  ) : (
                                    <span className="text-muted-foreground text-xs">
                                      -
                                    </span>
                                  )}
                                </TableCell>
                              )}
                            </TableRow>
                          );
                        })}
                        {!usageLoading && !sourceRows.length && (
                          <TableRow>
                            <TableCell
                              colSpan={poolFiveHour ? 7 : 6}
                              className="text-muted-foreground h-24 text-center"
                            >
                              {t("No matching resource pool accounts")}
                            </TableCell>
                          </TableRow>
                        )}
                      </TableBody>
                    </Table>
                  </CardContent>
                </Card>
              ) : (
                <>
                  {groupRows.length > 0 && (
                    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                      {groupRows.map((row) => (
                        <Card key={row.group || "-"}>
                          <CardContent className="space-y-3 p-4">
                            <div className="flex items-center justify-between gap-2">
                              <div className="flex items-center gap-2">
                                <span className="inline-flex rounded bg-indigo-500/10 px-2 py-0.5 text-xs font-medium text-indigo-600 dark:text-indigo-400">
                                  {row.group || t("Ungrouped")}
                                </span>
                                <span className="text-muted-foreground text-xs">
                                  {t("{{enabled}}/{{total}} enabled", {
                                    enabled: row.enabled,
                                    total: row.accounts.length,
                                  })}
                                </span>
                              </div>
                              <span
                                className={cn(
                                  "text-lg font-semibold tabular-nums",
                                  LEVEL_TEXT_TONE[levelTone(row.level)],
                                )}
                              >
                                {row.level === null
                                  ? "-"
                                  : `${row.level.toFixed(1)}%`}
                              </span>
                            </div>
                            <UsageBar
                              percent={row.level ?? 0}
                              digits={0}
                              muted={row.level === null}
                            />
                            <div className="text-muted-foreground grid grid-cols-3 gap-2 text-xs">
                              <div>
                                <div>{t("Credits")}</div>
                                <div className="text-foreground font-medium tabular-nums">
                                  {formatCompactNumber(row.usage, locale)} /{" "}
                                  {formatCompactNumber(row.limit, locale)}
                                </div>
                              </div>
                              <div>
                                <div>RPM</div>
                                <div className="text-foreground font-medium tabular-nums">
                                  {formatNumber(row.rpmCurrent, locale)} /{" "}
                                  {formatNumber(row.rpmLimit, locale)}
                                </div>
                              </div>
                              <div>
                                <div>{t("Next reset")}</div>
                                <div className="text-foreground font-medium">
                                  {formatResetAt(row.nextReset, locale)}
                                </div>
                              </div>
                            </div>
                          </CardContent>
                        </Card>
                      ))}
                    </div>
                  )}
                  <Card>
                    <CardContent className="p-0">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>{t("Channel")}</TableHead>
                            <TableHead>{t("Group")}</TableHead>
                            <TableHead className="text-right">
                              {t("Accounts")}
                            </TableHead>
                            <TableHead className="text-right">
                              {t("Total usage")}
                            </TableHead>
                            <TableHead className="text-right">
                              {t("Quota limit")}
                            </TableHead>
                            <TableHead className="w-56">
                              {t("Pool water level")}
                            </TableHead>
                            <TableHead className="text-right">RPM</TableHead>
                            <TableHead>{t("Next reset")}</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {usageLoading && <LoadingRows columns={8} />}
                          {sourceRows.map((row) => {
                            const rowUsage = row.accounts.reduce(
                              (sum, item) => sum + item.usage_current,
                              0,
                            );
                            const rowLimit = row.accounts.reduce(
                              (sum, item) => sum + item.usage_limit,
                              0,
                            );
                            const level = waterLevel(row.accounts);
                            return (
                              <TableRow key={row.sourceId}>
                                <TableCell>
                                  <span className="inline-flex rounded bg-blue-500/10 px-2 py-0.5 text-xs text-blue-600 dark:text-blue-400">
                                    {row.name}
                                  </span>
                                </TableCell>
                                <TableCell>
                                  <GroupChips groups={row.groups} />
                                </TableCell>
                                <TableCell className="text-right">
                                  {t("{{count}} accounts", {
                                    count: row.accounts.length,
                                  })}
                                </TableCell>
                                <TableCell className="text-right">
                                  {formatCompactNumber(rowUsage, locale)}
                                </TableCell>
                                <TableCell className="text-right">
                                  {formatCompactNumber(rowLimit, locale)}
                                </TableCell>
                                <TableCell>
                                  <UsageBar percent={level} digits={0} />
                                </TableCell>
                                <TableCell className="text-right tabular-nums">
                                  {formatRpm(row.accounts)}
                                </TableCell>
                                <TableCell>
                                  <ResetAt
                                    iso={nextQuotaReset(row.accounts, now)}
                                    now={now}
                                    locale={locale}
                                  />
                                </TableCell>
                              </TableRow>
                            );
                          })}
                          {!usageLoading && !sourceRows.length && (
                            <TableRow>
                              <TableCell
                                colSpan={8}
                                className="text-muted-foreground h-24 text-center"
                              >
                                {t("No matching resource pool accounts")}
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </Table>
                    </CardContent>
                  </Card>
                </>
              )}
            </TabsContent>

            <TabsContent value="accounts" className="space-y-3">
              {isWindowedPool && (
                <div className="flex flex-wrap items-center gap-3">
                  <Input
                    value={accountKeyword}
                    onChange={(event) => setAccountKeyword(event.target.value)}
                    placeholder={t("Search account")}
                    aria-label={t("Search account")}
                    className="h-8 w-60"
                  />
                  <NativeSelect
                    size="sm"
                    value={statusFilter}
                    onChange={(event) =>
                      setStatusFilter(
                        event.target.value as WindowStatusKind | "all",
                      )
                    }
                    aria-label={t("Status")}
                  >
                    <NativeSelectOption value="all">
                      {t("All statuses")}
                    </NativeSelectOption>
                    {WINDOW_STATUS_OPTIONS.map((option) => (
                      <NativeSelectOption
                        key={option.value}
                        value={option.value}
                      >
                        {t(option.label)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Switch
                      checked={hideExpired}
                      onCheckedChange={setHideExpired}
                    />
                    {t("Hide expired accounts ({{count}})", {
                      count: expiredCount,
                    })}
                  </label>
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Switch
                      checked={hideDisabled}
                      onCheckedChange={setHideDisabled}
                    />
                    {t("Hide disabled accounts ({{count}})", {
                      count: disabledCount,
                    })}
                  </label>
                  <span className="text-muted-foreground ml-auto text-xs">
                    {t("Showing {{shown}} of {{total}} accounts", {
                      shown: visibleAccounts.length,
                      total: accounts.length,
                    })}
                  </span>
                </div>
              )}
              <Card>
                <CardContent className="p-0">
                  <Table>
                    {isWindowedPool ? (
                      <>
                        <TableHeader>
                          <TableRow>
                            <TableHead>{t("Account")}</TableHead>
                            <TableHead>{t("Plan")}</TableHead>
                            <TableHead className="w-48">
                              {t("Usage (7d)")}
                            </TableHead>
                            <TableHead>{t("Next reset")}</TableHead>
                            {poolFiveHour && (
                              <>
                                <TableHead className="w-48">
                                  {t("Usage (5h)")}
                                </TableHead>
                                <TableHead>{t("Next reset (5h)")}</TableHead>
                              </>
                            )}
                            <TableHead className="text-right">
                              {t("Requests (7d)")}
                            </TableHead>
                            <TableHead className="text-right">
                              {t("Tokens (7d)")}
                            </TableHead>
                            <TableHead>{t("Status")}</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {usageLoading && (
                            <LoadingRows columns={poolFiveHour ? 9 : 7} />
                          )}
                          {visibleAccounts.map((account) => {
                            const live = isWindowLive(account, now);
                            const plan =
                              account.plan_display || account.plan_type;
                            return (
                              <TableRow
                                key={`${account.source_id}-${account.id || account.email}`}
                              >
                                <TableCell>
                                  <div
                                    className="max-w-56 truncate font-medium"
                                    title={account.email}
                                  >
                                    {account.email ||
                                      account.name ||
                                      account.id}
                                  </div>
                                  <div className="text-muted-foreground text-xs">
                                    {account.source_name}
                                  </div>
                                </TableCell>
                                <TableCell>
                                  {plan ? (
                                    <span className="inline-flex rounded bg-violet-500/10 px-2 py-0.5 text-xs text-violet-600 dark:text-violet-400">
                                      {plan}
                                    </span>
                                  ) : (
                                    "-"
                                  )}
                                </TableCell>
                                <TableCell>
                                  <UsageBar
                                    percent={windowUsagePercent(account)}
                                    digits={0}
                                    muted={!live}
                                  />
                                  {!live && (
                                    <div className="text-muted-foreground mt-0.5 text-xs">
                                      {t("Not refreshed")}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell>
                                  <ResetAt
                                    iso={account.reset_7d_at}
                                    now={now}
                                    locale={locale}
                                  />
                                </TableCell>
                                {poolFiveHour &&
                                  (hasFiveHourWindow(account) ? (
                                    <>
                                      <TableCell>
                                        <UsageBar
                                          percent={fiveHourUsagePercent(
                                            account,
                                            now,
                                          )}
                                          digits={0}
                                          muted={!live}
                                        />
                                      </TableCell>
                                      <TableCell>
                                        <ResetAt
                                          iso={account.reset_5h_at}
                                          now={now}
                                          locale={locale}
                                        />
                                      </TableCell>
                                    </>
                                  ) : (
                                    <>
                                      <TableCell className="text-muted-foreground text-xs">
                                        -
                                      </TableCell>
                                      <TableCell className="text-muted-foreground text-xs">
                                        -
                                      </TableCell>
                                    </>
                                  ))}
                                <TableCell className="text-right">
                                  {formatNumber(
                                    account.requests_7d ?? 0,
                                    locale,
                                  )}
                                </TableCell>
                                <TableCell className="text-right">
                                  {formatCompactNumber(
                                    account.tokens_7d ?? 0,
                                    locale,
                                  )}
                                </TableCell>
                                <TableCell>
                                  <WindowStatusBadge account={account} />
                                </TableCell>
                              </TableRow>
                            );
                          })}
                          {!usageLoading && !visibleAccounts.length && (
                            <TableRow>
                              <TableCell
                                colSpan={poolFiveHour ? 9 : 7}
                                className="text-muted-foreground h-24 text-center"
                              >
                                {t(
                                  accounts.length
                                    ? "No matching resource pool accounts"
                                    : "No accounts",
                                )}
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </>
                    ) : (
                      <>
                        <TableHeader>
                          <TableRow>
                            <TableHead>{t("Account")}</TableHead>
                            <TableHead>{t("Channel")}</TableHead>
                            <TableHead>{t("Group")}</TableHead>
                            <TableHead className="text-right">
                              {t("Cumulative usage")}
                            </TableHead>
                            <TableHead className="text-right">
                              {t("Quota limit")}
                            </TableHead>
                            <TableHead className="w-40">
                              {t("Pool water level")}
                            </TableHead>
                            <TableHead className="text-right">RPM</TableHead>
                            <TableHead>{t("Next reset")}</TableHead>
                            <TableHead>{t("Last refresh")}</TableHead>
                            <TableHead>{t("Status")}</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {usageLoading && <LoadingRows columns={10} />}
                          {accounts.map((account) => {
                            const percent = quotaUsagePercent(account);
                            return (
                              <TableRow
                                key={`${account.source_id}-${account.id || account.email}`}
                              >
                                <TableCell>
                                  <div
                                    className="max-w-56 truncate font-medium"
                                    title={account.email}
                                  >
                                    {account.email ||
                                      account.name ||
                                      account.id}
                                  </div>
                                  {(account.subscription_title ||
                                    account.subscription_type) && (
                                    <span className="mt-0.5 inline-flex rounded bg-violet-500/10 px-1.5 py-0.5 text-[11px] text-violet-600 dark:text-violet-400">
                                      {account.subscription_title ||
                                        account.subscription_type}
                                    </span>
                                  )}
                                </TableCell>
                                <TableCell>
                                  <div
                                    className="max-w-48 truncate"
                                    title={account.source_name}
                                  >
                                    {account.source_name}
                                  </div>
                                </TableCell>
                                <TableCell>
                                  <GroupChips
                                    groups={
                                      account.groups?.length
                                        ? account.groups
                                        : [accountGroup(account)]
                                    }
                                  />
                                </TableCell>
                                <TableCell className="text-right">
                                  {formatCompactNumber(
                                    account.usage_current,
                                    locale,
                                  )}
                                </TableCell>
                                <TableCell className="text-right">
                                  {formatCompactNumber(
                                    account.usage_limit,
                                    locale,
                                  )}
                                </TableCell>
                                <TableCell>
                                  <UsageBar
                                    percent={percent}
                                    digits={0}
                                    muted={!account.enabled}
                                  />
                                </TableCell>
                                <TableCell className="text-right text-xs tabular-nums">
                                  {account.rpm_limit
                                    ? `${formatNumber(account.rpm_current ?? 0, locale)} / ${formatNumber(account.rpm_limit, locale)}`
                                    : "-"}
                                </TableCell>
                                <TableCell>
                                  <ResetAt
                                    iso={account.next_reset_date}
                                    now={now}
                                    locale={locale}
                                  />
                                </TableCell>
                                <TableCell className="text-muted-foreground text-xs whitespace-nowrap">
                                  {account.last_refresh
                                    ? formatResetDistance(
                                        new Date(
                                          account.last_refresh * 1000,
                                        ).toISOString(),
                                        now,
                                        locale,
                                      )
                                    : "-"}
                                </TableCell>
                                <TableCell>
                                  {account.enabled ? (
                                    <Badge
                                      variant="outline"
                                      className="text-emerald-600"
                                    >
                                      <CircleCheck />
                                      {t("Active")}
                                    </Badge>
                                  ) : (
                                    <Badge variant="destructive">
                                      <CircleX />
                                      {t("Disabled")}
                                    </Badge>
                                  )}
                                </TableCell>
                              </TableRow>
                            );
                          })}
                          {!usageLoading && !accounts.length && (
                            <TableRow>
                              <TableCell
                                colSpan={10}
                                className="text-muted-foreground h-24 text-center"
                              >
                                {t("No accounts")}
                              </TableCell>
                            </TableRow>
                          )}
                        </TableBody>
                      </>
                    )}
                  </Table>
                </CardContent>
              </Card>
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  );
}

/** Account group badges; an empty list renders the ungrouped placeholder. */
function GroupChips(props: { groups: string[] }) {
  const { t } = useTranslation();
  const groups = props.groups.filter(Boolean);
  if (!groups.length)
    {return <span className="text-muted-foreground">{t("Ungrouped")}</span>;}
  return (
    <div className="flex flex-wrap gap-1">
      {groups.map((group) => (
        <span
          key={group}
          className="inline-flex rounded bg-indigo-500/10 px-1.5 py-0.5 text-xs text-indigo-600 dark:text-indigo-400"
        >
          {group}
        </span>
      ))}
    </div>
  );
}

/** Current / limit RPM summed over the enabled accounts. */
function formatRpm(accounts: ResourcePoolAccount[]): string {
  const enabled = accounts.filter((account) => account.enabled);
  const limit = enabled.reduce((sum, a) => sum + (a.rpm_limit ?? 0), 0);
  if (!limit) return "-";
  const current = enabled.reduce((sum, a) => sum + (a.rpm_current ?? 0), 0);
  return `${current} / ${limit}`;
}
