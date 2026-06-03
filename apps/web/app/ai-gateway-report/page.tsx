"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertCircle, BarChart3, Loader2, RefreshCw } from "lucide-react";
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
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@multica/ui/components/ui/tooltip";

const WORKSPACE_SLUG = "local-agents";
const DAY_OPTIONS = [7, 30, 90] as const;

interface AIGatewayReportRow {
  email: string;
  model: string;
  request_count: number;
  success_count: number;
  error_count: number;
  input_tokens: number;
  cached_input_tokens: number;
  billable_input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  input_cost_micros: number;
  cached_input_cost_micros: number;
  output_cost_micros: number;
  total_cost_micros: number;
  long_context_request_count: number;
  average_latency_ms: number;
  last_request_at: string;
}

const PRICING_HINTS = {
  model: "按实际上游模型字段分组展示。字段不同就会自然拆开，例如 gpt-5.5 和 gpt-5.4。",
  input: "标准 GPT-5.5：输入 $5 / 1M tokens；缓存输入单独按 $0.50 / 1M tokens 计费。",
  cachedInput: "缓存命中的输入 tokens。标准 GPT-5.5 缓存输入 $0.50 / 1M tokens；长上下文请求不再单独拆缓存价。",
  output: "标准 GPT-5.5：输出 $30 / 1M tokens；reasoning tokens 按 output tokens 计费。",
  reasoning: "reasoning tokens 来自 output_tokens_details.reasoning_tokens，已包含在输出计费里，这里单独展示便于核对。",
  cost: "费用预估 = 输入费用 + 缓存输入费用 + 输出费用。历史缺少明细的行会按已有 token 和模型价格补估。",
  longContext: "GPT-5.5 单次输入超过 272K tokens 时按长上下文价：输入 $10 / 1M，输出 $45 / 1M。",
} as const;

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
  return (await resp.json()) as AIGatewayReportRow[];
}

export default function AIGatewayReportPage() {
  const [days, setDays] = useState<number>(30);
  const [summary, setSummary] = useState<AIGatewayReportRow[]>([]);
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
          acc.inputTokens += item.input_tokens;
          acc.cachedInputTokens += item.cached_input_tokens;
          acc.billableInputTokens += item.billable_input_tokens;
          acc.outputTokens += item.output_tokens;
          acc.reasoningTokens += item.reasoning_tokens;
          acc.tokens += item.total_tokens;
          acc.inputCostMicros += item.input_cost_micros;
          acc.cachedInputCostMicros += item.cached_input_cost_micros;
          acc.outputCostMicros += item.output_cost_micros;
          acc.costMicros += item.total_cost_micros;
          acc.longContextRequests += item.long_context_request_count;
          return acc;
        },
        {
          requests: 0,
          errors: 0,
          inputTokens: 0,
          cachedInputTokens: 0,
          billableInputTokens: 0,
          outputTokens: 0,
          reasoningTokens: 0,
          tokens: 0,
          inputCostMicros: 0,
          cachedInputCostMicros: 0,
          outputCostMicros: 0,
          costMicros: 0,
          longContextRequests: 0,
        },
      ),
    [summary],
  );

  const totalTokens = formatCompactNumber(totals.tokens);
  const inputTokens = formatCompactNumber(totals.inputTokens);
  const outputTokens = formatCompactNumber(totals.outputTokens);
  const cachedTokens = formatCompactNumber(totals.cachedInputTokens);
  const reasoningTokens = formatCompactNumber(totals.reasoningTokens);

  return (
    <TooltipProvider>
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

        <section className="grid gap-3 md:grid-cols-2">
          <Card size="sm" className="rounded-lg">
            <CardContent>
              <div className="mb-3 text-xs font-medium text-muted-foreground">Token 明细</div>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <MiniMetric label="输入" value={inputTokens.value} unit={inputTokens.unit || "tokens"} tooltip={PRICING_HINTS.input} />
                <MiniMetric label="缓存输入" value={cachedTokens.value} unit={cachedTokens.unit || "tokens"} tooltip={PRICING_HINTS.cachedInput} />
                <MiniMetric label="输出" value={outputTokens.value} unit={outputTokens.unit || "tokens"} tooltip={PRICING_HINTS.output} />
                <MiniMetric label="Reasoning" value={reasoningTokens.value} unit={reasoningTokens.unit || "tokens"} tooltip={PRICING_HINTS.reasoning} />
              </div>
            </CardContent>
          </Card>
          <Card size="sm" className="rounded-lg">
            <CardContent>
              <div className="mb-3 text-xs font-medium text-muted-foreground">费用明细</div>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <MiniMetric label="输入" value={formatCost(totals.inputCostMicros)} tooltip={PRICING_HINTS.input} />
                <MiniMetric label="缓存输入" value={formatCost(totals.cachedInputCostMicros)} tooltip={PRICING_HINTS.cachedInput} />
                <MiniMetric label="输出" value={formatCost(totals.outputCostMicros)} tooltip={PRICING_HINTS.output} />
                <MiniMetric label="长上下文" value={formatNumber(totals.longContextRequests)} unit="次" tooltip={PRICING_HINTS.longContext} />
              </div>
            </CardContent>
          </Card>
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
                    <TableHead className="min-w-56">邮箱</TableHead>
                    <TableHead><HeaderWithTooltip label="模型" tooltip={PRICING_HINTS.model} /></TableHead>
                    <TableHead className="text-right">请求</TableHead>
                    <TableHead className="text-right">错误</TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="输入" tooltip={PRICING_HINTS.input} align="right" /></TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="缓存" tooltip={PRICING_HINTS.cachedInput} align="right" /></TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="输出" tooltip={PRICING_HINTS.output} align="right" /></TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="Reasoning" tooltip={PRICING_HINTS.reasoning} align="right" /></TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="费用" tooltip={PRICING_HINTS.cost} align="right" /></TableHead>
                    <TableHead className="text-right"><HeaderWithTooltip label="长上下文" tooltip={PRICING_HINTS.longContext} align="right" /></TableHead>
                    <TableHead className="text-right">平均延迟</TableHead>
                    <TableHead className="text-right">最近调用</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {summary.map((item) => (
                    <TableRow key={`${item.email}-${item.model}`}>
                      <TableCell>
                        <div className="max-w-72 truncate font-medium">{item.email || "unknown"}</div>
                      </TableCell>
                      <TableCell>
                        <div className="whitespace-nowrap text-sm">{item.model || "unknown"}</div>
                      </TableCell>
                      <TableCell className="text-right">{formatNumber(item.request_count)}</TableCell>
                      <TableCell className="text-right">{formatNumber(item.error_count)}</TableCell>
                      <TableCell className="text-right">{formatCompactNumberText(item.input_tokens)}</TableCell>
                      <TableCell className="text-right">{formatCompactNumberText(item.cached_input_tokens)}</TableCell>
                      <TableCell className="text-right">{formatCompactNumberText(item.output_tokens)}</TableCell>
                      <TableCell className="text-right">{formatCompactNumberText(item.reasoning_tokens)}</TableCell>
                      <TableCell className="text-right">{formatCost(item.total_cost_micros)}</TableCell>
                      <TableCell className="text-right">{formatNumber(item.long_context_request_count)}</TableCell>
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
    </TooltipProvider>
  );
}

function HeaderWithTooltip({
  label,
  tooltip,
  align = "left",
}: {
  label: string;
  tooltip: string;
  align?: "left" | "right";
}) {
  const content = (
    <span className={align === "right" ? "inline-flex w-full justify-end underline decoration-dotted underline-offset-4" : "inline-flex underline decoration-dotted underline-offset-4"}>
      {label}
    </span>
  );
  return (
    <Tooltip>
      <TooltipTrigger render={content} />
      <TooltipContent side="top" align={align === "right" ? "end" : "start"} className="max-w-72 leading-relaxed">
        {tooltip}
      </TooltipContent>
    </Tooltip>
  );
}

function MiniMetric({ label, value, unit, tooltip }: { label: string; value: string; unit?: string; tooltip?: string }) {
  const labelNode = (
    <div className={tooltip ? "text-xs text-muted-foreground underline decoration-dotted underline-offset-4" : "text-xs text-muted-foreground"}>
      {label}
    </div>
  );
  return (
    <div className="min-w-0">
      {tooltip ? (
        <Tooltip>
          <TooltipTrigger render={labelNode} />
          <TooltipContent side="top" align="start" className="max-w-72 leading-relaxed">
            {tooltip}
          </TooltipContent>
        </Tooltip>
      ) : (
        labelNode
      )}
      <div className="mt-1 flex min-w-0 items-baseline gap-1">
        <span className="truncate text-sm font-semibold">{value}</span>
        {unit ? <span className="text-xs text-muted-foreground">{unit}</span> : null}
      </div>
    </div>
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
