"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertCircle, BarChart3, Loader2, RefreshCw } from "lucide-react";
import type { AIGatewayUsageSummary } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";

const WORKSPACE_SLUG = "local-agents";
const DAY_OPTIONS = [7, 30, 90] as const;

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatCompactNumber(value: number) {
  const units = [
    { suffix: "B", divisor: 1_000_000_000 },
    { suffix: "M", divisor: 1_000_000 },
    { suffix: "K", divisor: 1_000 },
  ];
  for (const unit of units) {
    if (value >= unit.divisor) {
      const scaled = value / unit.divisor;
      const maximumFractionDigits = scaled >= 100 ? 0 : 1;
      return {
        value: scaled.toLocaleString(undefined, {
          minimumFractionDigits: 0,
          maximumFractionDigits,
        }),
        unit: unit.suffix,
      };
    }
  }
  return { value: formatNumber(value), unit: "" };
}

function formatCompactNumberText(value: number) {
  const compact = formatCompactNumber(value);
  return `${compact.value}${compact.unit}`;
}

function formatCost(micros: number) {
  return `$${(micros / 1_000_000).toFixed(4)}`;
}

function formatDateTime(value: string | null | undefined) {
  if (!value) return "-";
  return new Date(value).toLocaleString();
}

function summaryLabel(item: AIGatewayUsageSummary) {
  return item.caller_id || item.key_name || item.key_prefix || "unknown";
}

async function fetchSummary(days: number, signal?: AbortSignal) {
  const params = new URLSearchParams({
    workspace_slug: WORKSPACE_SLUG,
    days: String(days),
  });
  const resp = await fetch(`/api/public/ai-gateway/usage/summary?${params}`, {
    cache: "no-store",
    signal,
  });
  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status}`);
  }
  return (await resp.json()) as AIGatewayUsageSummary[];
}

export default function AIGatewayReportPage() {
  const [days, setDays] = useState<number>(30);
  const [summary, setSummary] = useState<AIGatewayUsageSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  const loadSummary = useCallback(
    async (signal?: AbortSignal, mode: "initial" | "refresh" = "refresh") => {
      if (mode === "initial") {
        setLoading(true);
      } else {
        setRefreshing(true);
      }
      setError("");
      try {
        const data = await fetchSummary(days, signal);
        setSummary(data);
        setUpdatedAt(new Date());
      } catch (err) {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : "加载失败");
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [days],
  );

  useEffect(() => {
    const controller = new AbortController();
    void loadSummary(controller.signal, "initial");
    return () => controller.abort();
  }, [loadSummary]);

  const totals = useMemo(
    () =>
      summary.reduce(
        (acc, item) => {
          acc.requests += item.request_count;
          acc.errors += item.error_count;
          acc.tokens += item.total_tokens;
          acc.costMicros += item.total_cost_micros;
          return acc;
        },
        { requests: 0, errors: 0, tokens: 0, costMicros: 0 },
      ),
    [summary],
  );

  const totalTokens = formatCompactNumber(totals.tokens);

  return (
    <main className="h-dvh overflow-y-auto bg-background text-foreground">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-5 px-4 py-5 sm:px-6 lg:px-8">
        <header className="flex flex-wrap items-center justify-between gap-3 border-b pb-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <BarChart3 className="size-4" />
              <span>{WORKSPACE_SLUG}</span>
            </div>
            <h1 className="mt-1 text-xl font-semibold tracking-normal">AI 网关按人统计</h1>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex rounded-lg border bg-background p-0.5">
              {DAY_OPTIONS.map((option) => (
                <Button
                  key={option}
                  type="button"
                  size="sm"
                  variant={days === option ? "secondary" : "ghost"}
                  onClick={() => setDays(option)}
                  className="h-7"
                >
                  {option} 天
                </Button>
              ))}
            </div>
            <Button
              type="button"
              variant="outline"
              size="icon"
              aria-label="刷新"
              title="刷新"
              onClick={() => void loadSummary()}
              disabled={loading || refreshing}
            >
              {refreshing ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
            </Button>
          </div>
        </header>

        <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <MetricCard label="请求" value={formatNumber(totals.requests)} />
          <MetricCard label="Token" value={totalTokens.value} unit={totalTokens.unit || "tokens"} />
          <MetricCard label="费用预估" value={formatCost(totals.costMicros)} />
          <MetricCard label="错误" value={formatNumber(totals.errors)} tone={totals.errors > 0 ? "danger" : "default"} />
        </section>

        <section className="min-h-0">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <div className="text-sm font-medium">按人统计</div>
            <div className="text-xs text-muted-foreground">
              {updatedAt ? `更新于 ${updatedAt.toLocaleTimeString()}` : "等待刷新"}
            </div>
          </div>

          {error ? (
            <div className="flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="size-4" />
              <span>{error}</span>
            </div>
          ) : loading ? (
            <div className="space-y-2">
              {Array.from({ length: 6 }).map((_, index) => (
                <Skeleton key={index} className="h-11 w-full" />
              ))}
            </div>
          ) : summary.length > 0 ? (
            <div className="overflow-hidden rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="min-w-56">申请人</TableHead>
                    <TableHead className="text-right">请求</TableHead>
                    <TableHead className="text-right">成功</TableHead>
                    <TableHead className="text-right">错误</TableHead>
                    <TableHead className="text-right">Token</TableHead>
                    <TableHead className="text-right">费用预估</TableHead>
                    <TableHead className="text-right">平均延迟</TableHead>
                    <TableHead className="text-right">最近调用</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {summary.map((item) => (
                    <TableRow key={`${item.caller_id}-${item.key_prefix}`}>
                      <TableCell>
                        <div className="min-w-0">
                          <div className="max-w-72 truncate font-medium">{summaryLabel(item)}</div>
                          <div className="mt-1 flex flex-wrap gap-1">
                            {item.key_name ? <Badge variant="outline">{item.key_name}</Badge> : null}
                            {item.key_prefix ? <Badge variant="secondary">{item.key_prefix}...</Badge> : null}
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className="text-right">{formatNumber(item.request_count)}</TableCell>
                      <TableCell className="text-right">{formatNumber(item.success_count)}</TableCell>
                      <TableCell className="text-right">{formatNumber(item.error_count)}</TableCell>
                      <TableCell className="text-right">{formatCompactNumberText(item.total_tokens)}</TableCell>
                      <TableCell className="text-right">{formatCost(item.total_cost_micros)}</TableCell>
                      <TableCell className="text-right">{formatNumber(item.average_latency_ms)}ms</TableCell>
                      <TableCell className="text-right">{formatDateTime(item.last_request_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="rounded-lg border px-3 py-8 text-center text-sm text-muted-foreground">暂无统计数据</div>
          )}
        </section>
      </div>
    </main>
  );
}

function MetricCard({
  label,
  value,
  unit,
  tone = "default",
}: {
  label: string;
  value: string;
  unit?: string;
  tone?: "default" | "danger";
}) {
  return (
    <Card size="sm" className="rounded-lg">
      <CardContent>
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="mt-2 flex items-baseline gap-1.5">
          <span className={tone === "danger" ? "text-2xl font-semibold text-destructive" : "text-2xl font-semibold"}>
            {value}
          </span>
          {unit ? <span className="text-sm text-muted-foreground">{unit}</span> : null}
        </div>
      </CardContent>
    </Card>
  );
}
