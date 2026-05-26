"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, BarChart3, Check, ChevronLeft, ChevronRight, Copy, KeyRound, Plus, RefreshCw, Route, Save, Trash2, Zap } from "lucide-react";
import { toast } from "sonner";
import type {
  AIGatewayKey,
  AIGatewayProbeResult,
  AIGatewayProviderPreset,
  AIGatewayRoute,
  AIGatewayRouteTarget,
  AIGatewayUsage,
  AIGatewayUsageSummary,
} from "@multica/core/types";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions } from "@multica/core/workspace/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Switch } from "@multica/ui/components/ui/switch";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";

const EXPIRY_KEYS = ["30", "90", "365", "never"] as const;
const STRATEGIES = ["fallback", "single", "round_robin", "weighted"] as const;
const UPSTREAM_APIS = ["responses", "chat_completions"] as const;
const REASONING_EFFORTS = ["minimal", "low", "medium", "high", "xhigh"] as const;
const REASONING_EFFORT_DEFAULT = "__default";
const USAGE_PAGE_SIZE = 20;

type RouteTargetForm = AIGatewayRouteTarget;
type RouteEditorMode = "ui" | "json";

interface RouteFormPayload {
  alias: string;
  strategy: string;
  enabled: boolean;
  targets: RouteTargetForm[];
}

function blankTarget(priority = 0): RouteTargetForm {
  return {
    provider: "openai",
    base_url: "https://api.openai.com/v1",
    api_key_env: "OPENAI_API_KEY",
    model: "gpt-5-codex",
    upstream_api: "responses",
    reasoning_effort: "",
    timeout_seconds: 300,
    weight: 1,
    priority,
    enabled: true,
    input_price_per_million_micros: 0,
    output_price_per_million_micros: 0,
  };
}

function formatDateTime(value: string | null | undefined) {
  if (!value) return "";
  return new Date(value).toLocaleString();
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatMillionTokens(value: number) {
  return `${(value / 1_000_000).toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}M`;
}

function formatCost(micros: number) {
  return `$${(micros / 1_000_000).toFixed(4)}`;
}

function microsToUSD(value: number) {
  return value ? String(value / 1_000_000) : "";
}

function usdToMicros(value: string) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? Math.round(parsed * 1_000_000) : 0;
}

function usageRoute(usage: AIGatewayUsage) {
  if (usage.upstream_provider || usage.upstream_model) {
    return `${usage.model_alias} -> ${usage.upstream_provider}/${usage.upstream_model}`;
  }
  return usage.model_alias;
}

function summaryLabel(item: AIGatewayUsageSummary, unknown: string) {
  return item.caller_id || item.key_name || item.key_prefix || unknown;
}

function targetLabel(target: AIGatewayRouteTarget) {
  const effort = target.reasoning_effort ? ` · ${target.reasoning_effort}` : "";
  return `${target.provider}/${target.model || "<requested>"}${effort}`;
}

function formatRouteJson(payload: RouteFormPayload) {
  return JSON.stringify(payload, null, 2);
}

function routeToPayload(route: AIGatewayRoute): RouteFormPayload {
  return {
    alias: route.alias,
    strategy: route.strategy,
    enabled: route.enabled,
    targets: route.targets.length > 0 ? route.targets : [blankTarget()],
  };
}

function normalizeRoutePayload(payload: RouteFormPayload): RouteFormPayload {
  for (const [index, target] of payload.targets.entries()) {
    const apiKeyEnv = target.api_key_env.trim();
    if (apiKeyEnv.startsWith("sk-") || apiKeyEnv.includes("\n") || apiKeyEnv.includes("\r")) {
      throw new Error(`target ${index + 1} api_key_env must be an environment variable name, not a raw API key`);
    }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(apiKeyEnv)) {
      throw new Error(`target ${index + 1} api_key_env must look like OPENAI_API_KEY`);
    }
  }
  return {
    alias: payload.alias.trim(),
    strategy: payload.strategy,
    enabled: payload.enabled,
    targets: payload.targets.map((target, i) => ({
      ...target,
      priority: i,
      provider: target.provider.trim(),
      base_url: target.base_url.trim(),
      api_key_env: target.api_key_env.trim(),
      model: target.model.trim(),
      upstream_api: target.upstream_api,
      reasoning_effort: target.reasoning_effort?.trim() || undefined,
      organization_env: target.organization_env?.trim() || undefined,
      project_env: target.project_env?.trim() || undefined,
    })),
  };
}

function parseRouteJson(raw: string): RouteFormPayload {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw) as unknown;
  } catch (e) {
    if (e instanceof SyntaxError && e.message.includes("control character")) {
      throw new Error("invalid JSON: strings cannot contain raw line breaks; use an environment variable name like OPENAI_API_KEY");
    }
    throw e;
  }
  const candidate = Array.isArray(parsed)
    ? parsed[0]
    : parsed && typeof parsed === "object" && "models" in parsed && Array.isArray((parsed as { models?: unknown }).models)
      ? (parsed as { models: unknown[] }).models[0]
      : parsed;
  if (!candidate || typeof candidate !== "object") {
    throw new Error("route must be an object");
  }
  const route = candidate as Partial<RouteFormPayload>;
  if (typeof route.alias !== "string" || route.alias.trim() === "") {
    throw new Error("alias is required");
  }
  if (!Array.isArray(route.targets) || route.targets.length === 0) {
    throw new Error("targets must contain at least one target");
  }
  return normalizeRoutePayload({
    alias: route.alias,
    strategy: typeof route.strategy === "string" ? route.strategy : "fallback",
    enabled: typeof route.enabled === "boolean" ? route.enabled : true,
    targets: route.targets.map((rawTarget, i) => {
      const target = rawTarget as Partial<RouteTargetForm>;
      return {
        ...blankTarget(i),
        ...target,
        provider: typeof target.provider === "string" ? target.provider : "openai",
        base_url: typeof target.base_url === "string" ? target.base_url : "https://api.openai.com/v1",
        api_key_env: typeof target.api_key_env === "string" ? target.api_key_env : "OPENAI_API_KEY",
        model: typeof target.model === "string" ? target.model : "",
        upstream_api: typeof target.upstream_api === "string" ? target.upstream_api : "responses",
        reasoning_effort: typeof target.reasoning_effort === "string" ? target.reasoning_effort : "",
        timeout_seconds: Number(target.timeout_seconds) > 0 ? Number(target.timeout_seconds) : 60,
        weight: Number(target.weight) > 0 ? Number(target.weight) : 1,
        priority: i,
        enabled: typeof target.enabled === "boolean" ? target.enabled : true,
        input_price_per_million_micros: Number(target.input_price_per_million_micros) || 0,
        output_price_per_million_micros: Number(target.output_price_per_million_micros) || 0,
      };
    }),
  });
}

export function AIGatewayTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const [keys, setKeys] = useState<AIGatewayKey[]>([]);
  const [presets, setPresets] = useState<AIGatewayProviderPreset[]>([]);
  const [routes, setRoutes] = useState<AIGatewayRoute[]>([]);
  const [summary, setSummary] = useState<AIGatewayUsageSummary[]>([]);
  const [usage, setUsage] = useState<AIGatewayUsage[]>([]);
  const [usagePage, setUsagePage] = useState(0);
  const [usageHasNextPage, setUsageHasNextPage] = useState(false);
  const [keyName, setKeyName] = useState("");
  const [keyExpiry, setKeyExpiry] = useState("90");
  const [routeAlias, setRouteAlias] = useState("team-agent");
  const [routeStrategy, setRouteStrategy] = useState("fallback");
  const [routeEnabled, setRouteEnabled] = useState(true);
  const [routeTargets, setRouteTargets] = useState<RouteTargetForm[]>([blankTarget()]);
  const [routeEditorMode, setRouteEditorMode] = useState<RouteEditorMode>("ui");
  const [routeJson, setRouteJson] = useState(() => formatRouteJson({
    alias: "team-agent",
    strategy: "fallback",
    enabled: true,
    targets: [blankTarget()],
  }));
  const [editingRouteId, setEditingRouteId] = useState<string | null>(null);
  const [selectedPresetId, setSelectedPresetId] = useState("");
  const [probeResult, setProbeResult] = useState<AIGatewayProbeResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [routesLoading, setRoutesLoading] = useState(true);
  const [summaryLoading, setSummaryLoading] = useState(true);
  const [usageLoading, setUsageLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [savingRoute, setSavingRoute] = useState(false);
  const [probing, setProbing] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [revokeConfirmId, setRevokeConfirmId] = useState<string | null>(null);

  const currentMember = members.find((m) => m.user_id === user?.id) ?? null;
  const canManage = currentMember?.role === "owner" || currentMember?.role === "admin";
  const firstTarget = routeTargets[0] ?? blankTarget();

  const keyMetadata = useCallback((key: AIGatewayKey) => {
    const lastUsed = key.last_used_at
      ? t(($) => $.ai_gateway.last_used_with_date, { date: formatDateTime(key.last_used_at) })
      : t(($) => $.ai_gateway.last_used_never);
    const expires = key.expires_at
      ? t(($) => $.ai_gateway.expires_with_date, { date: formatDateTime(key.expires_at) })
      : t(($) => $.ai_gateway.expires_never);
    return t(($) => $.ai_gateway.key_metadata, {
      prefix: key.token_prefix,
      created: formatDateTime(key.created_at),
      lastUsed,
      expires,
    });
  }, [t]);

  const routePreview = useMemo(() => (
    routeTargets.map(targetLabel).join(" -> ")
  ), [routeTargets]);

  const routePayload = useMemo(() => normalizeRoutePayload({
    alias: routeAlias,
    strategy: routeStrategy,
    enabled: routeEnabled,
    targets: routeTargets,
  }), [routeAlias, routeStrategy, routeEnabled, routeTargets]);

  useEffect(() => {
    if (routeEditorMode === "ui") {
      setRouteJson(formatRouteJson(routePayload));
    }
  }, [routeEditorMode, routePayload]);

  const loadKeys = useCallback(async () => {
    if (!canManage) {
      setLoading(false);
      return;
    }
    try {
      setKeys(await api.listAIGatewayKeys());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_load_keys_failed));
    } finally {
      setLoading(false);
    }
  }, [canManage, t]);

  const loadRoutes = useCallback(async () => {
    if (!canManage) {
      setRoutesLoading(false);
      return;
    }
    try {
      const [presetList, routeList] = await Promise.all([
        api.listAIGatewayProviderPresets(),
        api.listAIGatewayRoutes(),
      ]);
      setPresets(presetList);
      setRoutes(routeList);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_load_routes_failed));
    } finally {
      setRoutesLoading(false);
    }
  }, [canManage, t]);

  const loadUsage = useCallback(async (page = 0) => {
    if (!canManage) {
      setUsageLoading(false);
      return;
    }
    setUsageLoading(true);
    try {
      const rows = await api.listAIGatewayUsage({
        limit: USAGE_PAGE_SIZE + 1,
        offset: page * USAGE_PAGE_SIZE,
      });
      setUsage(rows.slice(0, USAGE_PAGE_SIZE));
      setUsageHasNextPage(rows.length > USAGE_PAGE_SIZE);
      setUsagePage(page);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_load_usage_failed));
    } finally {
      setUsageLoading(false);
    }
  }, [canManage, t]);

  const loadSummary = useCallback(async () => {
    if (!canManage) {
      setSummaryLoading(false);
      return;
    }
    try {
      setSummary(await api.listAIGatewayUsageSummary(30));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_load_summary_failed));
    } finally {
      setSummaryLoading(false);
    }
  }, [canManage, t]);

  useEffect(() => {
    setLoading(true);
    setRoutesLoading(true);
    setSummaryLoading(true);
    setUsageLoading(true);
    void loadKeys();
    void loadRoutes();
    void loadSummary();
    void loadUsage(0);
  }, [loadKeys, loadRoutes, loadSummary, loadUsage]);

  const handleCreateKey = async () => {
    setCreating(true);
    try {
      const expiresInDays = keyExpiry === "never" ? undefined : Number(keyExpiry);
      const result = await api.createAIGatewayKey({
        name: keyName.trim(),
        expires_in_days: expiresInDays,
      });
      setNewToken(result.token);
      setKeyName("");
      setKeyExpiry("90");
      await loadKeys();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_create_failed));
    } finally {
      setCreating(false);
    }
  };

  const handleRevokeKey = async (id: string) => {
    setRevoking(id);
    try {
      await api.revokeAIGatewayKey(id);
      await loadKeys();
      toast.success(t(($) => $.ai_gateway.toast_revoked));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_revoke_failed));
    } finally {
      setRevoking(null);
    }
  };

  const handleCopyToken = async () => {
    if (!newToken) return;
    await navigator.clipboard.writeText(newToken);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const updateTarget = (index: number, patch: Partial<RouteTargetForm>) => {
    setRouteTargets((items) => items.map((item, i) => i === index ? { ...item, ...patch } : item));
  };

  const resetRouteForm = () => {
    const nextPayload = {
      alias: "team-agent",
      strategy: "fallback",
      enabled: true,
      targets: [blankTarget()],
    };
    setEditingRouteId(null);
    setRouteAlias(nextPayload.alias);
    setRouteStrategy(nextPayload.strategy);
    setRouteEnabled(nextPayload.enabled);
    setRouteTargets(nextPayload.targets);
    setRouteJson(formatRouteJson(nextPayload));
    setSelectedPresetId("");
    setProbeResult(null);
  };

  const applyPreset = (id: string) => {
    setSelectedPresetId(id);
    const preset = presets.find((p) => p.id === id);
    if (!preset) return;
    if (preset.id.includes("wildcard")) {
      setRouteAlias("*");
    }
    setRouteTargets([{
      ...blankTarget(),
      provider: preset.provider,
      base_url: preset.base_url,
      api_key_env: preset.api_key_env,
      model: preset.model,
      upstream_api: preset.upstream_api,
      timeout_seconds: preset.timeout_seconds,
    }]);
  };

  const editRoute = (route: AIGatewayRoute) => {
    const payload = routeToPayload(route);
    setEditingRouteId(route.id);
    setRouteAlias(payload.alias);
    setRouteStrategy(payload.strategy);
    setRouteEnabled(payload.enabled);
    setRouteTargets(payload.targets);
    setRouteJson(formatRouteJson(payload));
    setSelectedPresetId("");
    setProbeResult(null);
  };

  const applyRoutePayloadToUI = (payload: RouteFormPayload) => {
    setRouteAlias(payload.alias);
    setRouteStrategy(payload.strategy);
    setRouteEnabled(payload.enabled);
    setRouteTargets(payload.targets);
    setRouteJson(formatRouteJson(payload));
  };

  const handleRouteEditorModeChange = (mode: RouteEditorMode) => {
    if (mode === routeEditorMode) return;
    if (mode === "json") {
      setRouteJson(formatRouteJson(routePayload));
      setRouteEditorMode("json");
      return;
    }
    try {
      applyRoutePayloadToUI(parseRouteJson(routeJson));
      setRouteEditorMode("ui");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.route_json_invalid));
    }
  };

  const formatJsonEditor = () => {
    try {
      setRouteJson(formatRouteJson(parseRouteJson(routeJson)));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.route_json_invalid));
    }
  };

  const saveRoute = async () => {
    setSavingRoute(true);
    try {
      const payload = routeEditorMode === "json" ? parseRouteJson(routeJson) : routePayload;
      if (editingRouteId) {
        await api.updateAIGatewayRoute(editingRouteId, payload);
      } else {
        await api.createAIGatewayRoute(payload);
      }
      resetRouteForm();
      await loadRoutes();
      toast.success(t(($) => $.ai_gateway.toast_route_saved));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_route_save_failed));
    } finally {
      setSavingRoute(false);
    }
  };

  const deleteRoute = async (id: string) => {
    try {
      await api.deleteAIGatewayRoute(id);
      await loadRoutes();
      if (editingRouteId === id) resetRouteForm();
      toast.success(t(($) => $.ai_gateway.toast_route_deleted));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_route_delete_failed));
    }
  };

  const probeTarget = async () => {
    setProbing(true);
    try {
      const result = await api.probeAIGatewayProvider({
        base_url: firstTarget.base_url,
        api_key_env: firstTarget.api_key_env,
        model: firstTarget.model,
      });
      setProbeResult(result);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.ai_gateway.toast_probe_failed));
    } finally {
      setProbing(false);
    }
  };

  if (!canManage) {
    return (
      <div className="space-y-8">
        <section className="space-y-4">
          <div className="flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-muted-foreground" />
            <h2 className="text-sm font-semibold">{t(($) => $.ai_gateway.section_title)}</h2>
          </div>
          <Card>
            <CardContent>
              <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.manage_hint)}</p>
            </CardContent>
          </Card>
        </section>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.ai_gateway.section_title)}</h2>
        </div>

        <Card>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.description)}</p>
            <div className="grid gap-3 sm:grid-cols-[1fr_120px_auto]">
              <Input type="text" value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder={t(($) => $.ai_gateway.name_placeholder)} />
              <Select value={keyExpiry} onValueChange={(v) => { if (v) setKeyExpiry(v); }}>
                <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {EXPIRY_KEYS.map((key) => (
                    <SelectItem key={key} value={key}>{t(($) => $.ai_gateway.expiry[key])}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button onClick={handleCreateKey} disabled={creating || !keyName.trim()}>
                {creating ? t(($) => $.ai_gateway.creating) : t(($) => $.ai_gateway.create)}
              </Button>
            </div>
          </CardContent>
        </Card>

        {loading ? (
          <div className="space-y-2">{Array.from({ length: 2 }).map((_, i) => <Card key={i}><CardContent><Skeleton className="h-4 w-56" /></CardContent></Card>)}</div>
        ) : keys.length > 0 ? (
          <div className="space-y-2">
            {keys.map((key) => (
              <Card key={key.id}>
                <CardContent className="flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <div className="truncate text-sm font-medium">{key.name}</div>
                      <Badge variant={key.status === "active" ? "secondary" : "outline"}>{t(($) => $.ai_gateway.status[key.status === "active" ? "active" : "revoked"])}</Badge>
                    </div>
                    <div className="text-xs text-muted-foreground">{keyMetadata(key)}</div>
                  </div>
                  {key.status === "active" && (
                    <Tooltip>
                      <TooltipTrigger render={<Button variant="ghost" size="icon-sm" onClick={() => setRevokeConfirmId(key.id)} disabled={revoking === key.id} aria-label={t(($) => $.ai_gateway.revoke_aria, { name: key.name })}><Trash2 className="h-3.5 w-3.5" /></Button>} />
                      <TooltipContent>{t(($) => $.ai_gateway.revoke_tooltip)}</TooltipContent>
                    </Tooltip>
                  )}
                </CardContent>
              </Card>
            ))}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.no_keys)}</p>
        )}
      </section>

      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Route className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.ai_gateway.routes_title)}</h2>
        </div>

        <Card>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="inline-flex rounded-md border bg-muted/30 p-0.5">
                <Button type="button" size="sm" variant={routeEditorMode === "ui" ? "secondary" : "ghost"} onClick={() => handleRouteEditorModeChange("ui")}>{t(($) => $.ai_gateway.route_mode_ui)}</Button>
                <Button type="button" size="sm" variant={routeEditorMode === "json" ? "secondary" : "ghost"} onClick={() => handleRouteEditorModeChange("json")}>{t(($) => $.ai_gateway.route_mode_json)}</Button>
              </div>
              {routeEditorMode === "json" && (
                <Button type="button" variant="outline" size="sm" onClick={formatJsonEditor}>{t(($) => $.ai_gateway.route_json_format)}</Button>
              )}
            </div>

            {routeEditorMode === "ui" ? (
              <>
                <div className="grid gap-3 lg:grid-cols-[1fr_160px_auto]">
                  <label className="space-y-1.5">
                    <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.route_alias_label)}</span>
                    <Input value={routeAlias} onChange={(e) => setRouteAlias(e.target.value)} placeholder={t(($) => $.ai_gateway.route_alias_placeholder)} />
                  </label>
                  <label className="space-y-1.5">
                    <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.route_strategy_label)}</span>
                    <Select value={routeStrategy} onValueChange={(v) => { if (v) setRouteStrategy(v); }}>
                      <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                      <SelectContent>{STRATEGIES.map((s) => <SelectItem key={s} value={s}>{t(($) => $.ai_gateway.strategy[s])}</SelectItem>)}</SelectContent>
                    </Select>
                  </label>
                  <label className="flex items-end gap-2 pb-2">
                    <Switch checked={routeEnabled} onCheckedChange={setRouteEnabled} />
                    <span className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.route_enabled)}</span>
                  </label>
                </div>
                <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.route_model_hint)}</p>

                <div className="grid gap-3 lg:grid-cols-[1fr_auto]">
                  <Select value={selectedPresetId} onValueChange={(v) => { if (v) applyPreset(v); }}>
                    <SelectTrigger size="sm"><SelectValue placeholder={t(($) => $.ai_gateway.preset_placeholder)} /></SelectTrigger>
                    <SelectContent>{presets.map((preset) => <SelectItem key={preset.id} value={preset.id}>{preset.name}</SelectItem>)}</SelectContent>
                  </Select>
                  <Button variant="outline" onClick={() => setRouteTargets((items) => [...items, blankTarget(items.length)])}>
                    <Plus className="h-4 w-4" /> {t(($) => $.ai_gateway.add_target)}
                  </Button>
                </div>

                <div className="space-y-3">
                  {routeTargets.map((target, index) => (
                    <div key={index} className="rounded-md border p-3 space-y-3">
                      <div className="flex items-center justify-between gap-2">
                        <div className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.target_label, { index: index + 1 })}</div>
                        <div className="flex items-center gap-2">
                          <Switch checked={target.enabled} onCheckedChange={(v) => updateTarget(index, { enabled: v })} />
                          {routeTargets.length > 1 && <Button variant="ghost" size="icon-sm" onClick={() => setRouteTargets((items) => items.filter((_, i) => i !== index))}><Trash2 className="h-3.5 w-3.5" /></Button>}
                        </div>
                      </div>
                      <div className="grid gap-3 lg:grid-cols-4">
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.provider_label)}</span>
                          <Input value={target.provider} onChange={(e) => updateTarget(index, { provider: e.target.value })} placeholder={t(($) => $.ai_gateway.provider_placeholder)} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.model_label)}</span>
                          <Input value={target.model} onChange={(e) => updateTarget(index, { model: e.target.value })} placeholder={t(($) => $.ai_gateway.model_placeholder)} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.upstream_api_label)}</span>
                          <Select value={target.upstream_api} onValueChange={(v) => { if (v) updateTarget(index, { upstream_api: v }); }}>
                            <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                            <SelectContent>{UPSTREAM_APIS.map((apiName) => <SelectItem key={apiName} value={apiName}>{t(($) => $.ai_gateway.upstream_api[apiName])}</SelectItem>)}</SelectContent>
                          </Select>
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.reasoning_effort_label)}</span>
                          <Select
                            value={target.reasoning_effort || REASONING_EFFORT_DEFAULT}
                            onValueChange={(v) => updateTarget(index, { reasoning_effort: v && v !== REASONING_EFFORT_DEFAULT ? v : "" })}
                          >
                            <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                            <SelectContent>
                              <SelectItem value={REASONING_EFFORT_DEFAULT}>{t(($) => $.ai_gateway.reasoning_effort_default)}</SelectItem>
                              {REASONING_EFFORTS.map((effort) => (
                                <SelectItem key={effort} value={effort}>{t(($) => $.ai_gateway.reasoning_effort[effort])}</SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </label>
                      </div>
                      <div className="grid gap-3 lg:grid-cols-[1fr_180px_120px_80px]">
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.base_url_label)}</span>
                          <Input value={target.base_url} onChange={(e) => updateTarget(index, { base_url: e.target.value })} placeholder={t(($) => $.ai_gateway.base_url_placeholder)} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.api_key_env_label)}</span>
                          <Input value={target.api_key_env} onChange={(e) => updateTarget(index, { api_key_env: e.target.value })} placeholder={t(($) => $.ai_gateway.api_key_env_placeholder)} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.timeout_label)}</span>
                          <Input type="number" value={target.timeout_seconds} onChange={(e) => updateTarget(index, { timeout_seconds: Number(e.target.value) || 60 })} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.weight_label)}</span>
                          <Input type="number" value={target.weight} onChange={(e) => updateTarget(index, { weight: Number(e.target.value) || 1 })} />
                        </label>
                      </div>
                      <div className="grid gap-3 sm:grid-cols-2">
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.input_price_label)}</span>
                          <Input value={microsToUSD(target.input_price_per_million_micros)} onChange={(e) => updateTarget(index, { input_price_per_million_micros: usdToMicros(e.target.value) })} placeholder={t(($) => $.ai_gateway.input_price_placeholder)} />
                        </label>
                        <label className="space-y-1.5">
                          <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.output_price_label)}</span>
                          <Input value={microsToUSD(target.output_price_per_million_micros)} onChange={(e) => updateTarget(index, { output_price_per_million_micros: usdToMicros(e.target.value) })} placeholder={t(($) => $.ai_gateway.output_price_placeholder)} />
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            ) : (
              <label className="space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">{t(($) => $.ai_gateway.route_json_label)}</span>
                <Textarea
                  className="min-h-80 font-mono text-xs"
                  value={routeJson}
                  onChange={(e) => setRouteJson(e.target.value)}
                  spellCheck={false}
                  placeholder={t(($) => $.ai_gateway.route_json_placeholder)}
                />
              </label>
            )}

            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="min-w-0 text-xs text-muted-foreground truncate">{routeEditorMode === "ui" ? routePreview : t(($) => $.ai_gateway.route_json_hint)}</div>
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" onClick={probeTarget} disabled={routeEditorMode === "json" || probing || !firstTarget.base_url}>
                  <Zap className="h-4 w-4" /> {probing ? t(($) => $.ai_gateway.probing) : t(($) => $.ai_gateway.probe)}
                </Button>
                {editingRouteId && <Button variant="outline" onClick={resetRouteForm}>{t(($) => $.ai_gateway.cancel_edit)}</Button>}
                <Button onClick={saveRoute} disabled={savingRoute || (routeEditorMode === "ui" ? !routeAlias.trim() : !routeJson.trim())}>
                  <Save className="h-4 w-4" /> {savingRoute ? t(($) => $.ai_gateway.saving_route) : t(($) => $.ai_gateway.save_route)}
                </Button>
              </div>
            </div>

            {probeResult && (
              <div className="flex flex-wrap gap-2 text-xs">
                <Badge variant={probeResult.models_endpoint.ok ? "secondary" : "outline"}>{t(($) => $.ai_gateway.probe_models, { status: probeResult.models_endpoint.status })}</Badge>
                <Badge variant={probeResult.responses.ok ? "secondary" : probeResult.responses.supported ? "outline" : "destructive"}>{t(($) => $.ai_gateway.probe_responses, { status: probeResult.responses.status })}</Badge>
                <Badge variant={probeResult.chat_completions.ok ? "secondary" : probeResult.chat_completions.supported ? "outline" : "destructive"}>{t(($) => $.ai_gateway.probe_chat, { status: probeResult.chat_completions.status })}</Badge>
                <Badge variant="outline">{t(($) => $.ai_gateway.probe_model_count, { count: probeResult.models.length })}</Badge>
              </div>
            )}
          </CardContent>
        </Card>

        {routesLoading ? (
          <Card><CardContent><Skeleton className="h-4 w-64" /></CardContent></Card>
        ) : routes.length > 0 ? (
          <div className="space-y-2">
            {routes.map((route) => (
              <Card key={route.id}>
                <CardContent className="flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <div className="text-sm font-medium">{route.alias}</div>
                      <Badge variant={route.enabled ? "secondary" : "outline"}>{route.strategy}</Badge>
                    </div>
                    <div className="truncate text-xs text-muted-foreground">{route.targets.map(targetLabel).join(" -> ")}</div>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => editRoute(route)}>{t(($) => $.ai_gateway.edit_route)}</Button>
                  <Button variant="ghost" size="icon-sm" onClick={() => deleteRoute(route.id)}><Trash2 className="h-3.5 w-3.5" /></Button>
                </CardContent>
              </Card>
            ))}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.no_routes)}</p>
        )}
      </section>

      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <BarChart3 className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.ai_gateway.summary_title)}</h2>
        </div>

        {summaryLoading ? (
          <div className="space-y-2">{Array.from({ length: 3 }).map((_, i) => <Card key={i}><CardContent><Skeleton className="h-4 w-full" /></CardContent></Card>)}</div>
        ) : summary.length > 0 ? (
          <div className="space-y-2">
            {summary.map((item) => (
              <Card key={`${item.caller_id}-${item.key_prefix}`}>
                <CardContent className="space-y-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0 text-sm font-medium truncate">{summaryLabel(item, t(($) => $.ai_gateway.caller_unknown))}</div>
                    <div className="flex gap-2">
                      <Badge variant="secondary">{formatMillionTokens(item.total_tokens)}</Badge>
                      <Badge variant="outline">{formatCost(item.total_cost_micros)}</Badge>
                    </div>
                  </div>
                  <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>{t(($) => $.ai_gateway.summary_requests, { value: formatNumber(item.request_count) })}</span>
                    <span>{t(($) => $.ai_gateway.summary_errors, { value: formatNumber(item.error_count) })}</span>
                    <span>{t(($) => $.ai_gateway.summary_avg_latency, { latency: formatNumber(item.average_latency_ms) })}</span>
                    <span>{t(($) => $.ai_gateway.summary_last, { date: formatDateTime(item.last_request_at) })}</span>
                    {item.key_name && <span>{t(($) => $.ai_gateway.summary_key, { key: item.key_name })}</span>}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.no_summary)}</p>
        )}
      </section>

      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.ai_gateway.usage_title)}</h2>
          <Button variant="ghost" size="icon-sm" onClick={() => { void loadSummary(); void loadUsage(usagePage); }}><RefreshCw className="h-3.5 w-3.5" /></Button>
        </div>

        {usageLoading ? (
          <div className="space-y-2">{Array.from({ length: 3 }).map((_, i) => <Card key={i}><CardContent><Skeleton className="h-4 w-full" /></CardContent></Card>)}</div>
        ) : usage.length > 0 ? (
          <div className="space-y-2">
            {usage.map((item) => (
              <Card key={item.id}>
                <CardContent className="space-y-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0 text-sm font-medium"><span className="truncate">{usageRoute(item)}</span></div>
                    <Badge variant={item.status_code >= 400 ? "destructive" : "outline"}>{item.status_code}</Badge>
                  </div>
                  <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>{item.endpoint}</span>
                    {item.caller_id && <span>{item.caller_id}</span>}
                    {item.key_name && <span>{item.key_name}</span>}
                    {item.key_prefix && <span>{item.key_prefix}...</span>}
                    {item.reasoning_effort && <span>{t(($) => $.ai_gateway.usage_reasoning, { value: item.reasoning_effort })}</span>}
                    <span>{formatNumber(item.total_tokens)} {t(($) => $.ai_gateway.tokens_unit)}</span>
                    <span>{formatCost(item.total_cost_micros)}</span>
                    <span>{item.latency_ms}ms</span>
                    <span>{formatDateTime(item.created_at)}</span>
                  </div>
                  {item.error && <div className="line-clamp-2 text-xs text-destructive">{item.error}</div>}
                </CardContent>
              </Card>
            ))}
            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                variant="outline"
                size="icon-sm"
                disabled={usageLoading || usagePage === 0}
                onClick={() => { void loadUsage(Math.max(0, usagePage - 1)); }}
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </Button>
              <span className="min-w-16 text-center text-xs text-muted-foreground">
                {t(($) => $.ai_gateway.usage_page, { page: String(usagePage + 1) })}
              </span>
              <Button
                variant="outline"
                size="icon-sm"
                disabled={usageLoading || !usageHasNextPage}
                onClick={() => { void loadUsage(usagePage + 1); }}
              >
                <ChevronRight className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{t(($) => $.ai_gateway.no_usage)}</p>
        )}
      </section>

      <AlertDialog open={!!revokeConfirmId} onOpenChange={(v) => { if (!v) setRevokeConfirmId(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.ai_gateway.revoke_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.ai_gateway.revoke_dialog.description)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.ai_gateway.revoke_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={async () => { if (revokeConfirmId) await handleRevokeKey(revokeConfirmId); setRevokeConfirmId(null); }}>{t(($) => $.ai_gateway.revoke_dialog.confirm)}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={!!newToken} onOpenChange={(v) => { if (!v) { setNewToken(null); setCopied(false); } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.ai_gateway.created_dialog.title)}</DialogTitle>
            <DialogDescription>{t(($) => $.ai_gateway.created_dialog.description)}</DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="flex-1 rounded-md border bg-muted/50 px-3 py-2 text-sm break-all select-all">{newToken}</code>
            <Tooltip>
              <TooltipTrigger render={<Button variant="outline" size="icon" onClick={handleCopyToken}>{copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}</Button>} />
              <TooltipContent>{t(($) => $.ai_gateway.created_dialog.copy_tooltip)}</TooltipContent>
            </Tooltip>
          </div>
          <DialogFooter>
            <Button onClick={() => { setNewToken(null); setCopied(false); }}>{t(($) => $.ai_gateway.created_dialog.done)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
