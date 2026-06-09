"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Archive,
  Check,
  ExternalLink,
  FileCheck2,
  GitBranch,
  Image,
  RotateCcw,
  Send,
  ShieldCheck,
  Sparkles,
  X,
} from "lucide-react";
import { reviewItemListOptions } from "@multica/core/review-items/queries";
import { useReviewItemAction } from "@multica/core/review-items/mutations";
import type {
  ReviewItem,
  ReviewItemAction,
  ReviewItemStatus,
  ReviewItemType,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { PageHeader } from "../../layout/page-header";
import { WorkspaceAvatar } from "../../workspace/workspace-avatar";
import { useNavigation } from "../../navigation";

const TYPE_META: Record<
  ReviewItemType,
  { label: string; icon: typeof ShieldCheck; tone: string }
> = {
  skill_review: {
    label: "Skill 审阅",
    icon: Sparkles,
    tone: "text-violet-600",
  },
  issue_change_review: {
    label: "Issue 修改确认",
    icon: FileCheck2,
    tone: "text-sky-600",
  },
  plan_review: {
    label: "Plan 审阅",
    icon: GitBranch,
    tone: "text-emerald-600",
  },
  artifact_review: {
    label: "Artifact 审阅",
    icon: Image,
    tone: "text-amber-600",
  },
};

const STATUS_LABEL: Record<ReviewItemStatus | "all", string> = {
  all: "全部",
  pending: "待处理",
  approved: "已批准",
  changes_requested: "要求修改",
  rejected: "已驳回",
  superseded: "已过期",
};

const ACTION_LABEL: Record<ReviewItemAction, string> = {
  approve: "批准",
  reject: "驳回",
  request_changes: "要求修改",
  promote: "晋升",
  assign: "分派",
  rerun: "重跑",
  open_source: "打开源对象",
};

const ACTION_ICON: Partial<Record<ReviewItemAction, typeof Check>> = {
  approve: Check,
  reject: X,
  request_changes: Send,
  rerun: RotateCcw,
  open_source: ExternalLink,
};

export function DecisionDeskPage() {
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const [status, setStatus] = useState<ReviewItemStatus | "all">("pending");
  const [type, setType] = useState<ReviewItemType | "all">("all");
  const { data: rawItems, isLoading } = useQuery(
    reviewItemListOptions(wsId, { status, type }),
  );
  const items: ReviewItem[] = rawItems ?? [];
  const [selectedId, setSelectedId] = useState<string>("");
  const selected = useMemo(
    () => items.find((item) => item.id === selectedId) ?? items[0] ?? null,
    [items, selectedId],
  );

  const counts = useMemo(() => {
    const next = new Map<ReviewItemType | "all", number>([["all", items.length]]);
    for (const item of items) {
      next.set(item.type, (next.get(item.type) ?? 0) + 1);
    }
    return next;
  }, [items]);

  const openSource = (item: ReviewItem) => {
    const href = sourceHref(item, paths);
    if (!href) {
      toast.info("这个决策项还没有可打开的源对象");
      return;
    }
    nav.push(href);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader className="gap-1.5">
        <WorkspaceAvatar name={workspace?.name ?? "W"} size="sm" />
        <span className="text-sm text-muted-foreground">
          {workspace?.name ?? "Workspace"}
        </span>
        <span className="text-sm text-muted-foreground">/</span>
        <span className="text-sm font-medium">决策台</span>
      </PageHeader>

      <div className="grid min-h-0 flex-1 grid-cols-[220px_minmax(280px,380px)_1fr] border-t">
        <aside className="border-r bg-muted/20 p-3">
          <div className="mb-4">
            <p className="px-2 text-xs font-medium text-muted-foreground">
              状态
            </p>
            <div className="mt-2 space-y-1">
              {(["pending", "all", "changes_requested", "approved", "rejected"] as const).map((value) => (
                <FilterButton
                  key={value}
                  active={status === value}
                  label={STATUS_LABEL[value]}
                  onClick={() => setStatus(value)}
                />
              ))}
            </div>
          </div>
          <div>
            <p className="px-2 text-xs font-medium text-muted-foreground">
              类型
            </p>
            <div className="mt-2 space-y-1">
              <FilterButton
                active={type === "all"}
                label={`全部 ${counts.get("all") ?? 0}`}
                onClick={() => setType("all")}
              />
              {(Object.keys(TYPE_META) as ReviewItemType[]).map((value) => (
                <FilterButton
                  key={value}
                  active={type === value}
                  label={`${TYPE_META[value].label} ${counts.get(value) ?? 0}`}
                  onClick={() => setType(value)}
                />
              ))}
            </div>
          </div>
        </aside>

        <section className="min-h-0 border-r">
          <div className="flex h-12 items-center justify-between border-b px-4">
            <div>
              <h1 className="text-sm font-semibold">决策台</h1>
              <p className="text-xs text-muted-foreground">
                批准、驳回、要求修改、晋升、分派、重跑
              </p>
            </div>
            <Badge variant="secondary">{items.length}</Badge>
          </div>
          <div className="min-h-0 overflow-y-auto p-2">
            {isLoading ? (
              <DecisionListSkeleton />
            ) : items.length === 0 ? (
              <div className="flex flex-col items-center justify-center px-6 py-16 text-center text-muted-foreground">
                <Archive className="mb-3 size-9 opacity-50" />
                <p className="text-sm">暂无需要处理的决策项</p>
                <p className="mt-1 text-xs">
                  Skill、Issue、Plan 和 Artifact 的待确认内容会出现在这里。
                </p>
              </div>
            ) : (
              items.map((item) => (
                <DecisionListItem
                  key={item.id}
                  item={item}
                  active={selected?.id === item.id}
                  onClick={() => setSelectedId(item.id)}
                />
              ))
            )}
          </div>
        </section>

        <section className="min-h-0 overflow-y-auto">
          {selected ? (
            <DecisionDetail item={selected} onOpenSource={openSource} />
          ) : (
            <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
              选择一个决策项
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function FilterButton({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex h-8 w-full items-center justify-between rounded-md px-2 text-left text-sm ${
        active
          ? "bg-background font-medium text-foreground shadow-sm"
          : "text-muted-foreground hover:bg-background/70 hover:text-foreground"
      }`}
    >
      {label}
    </button>
  );
}

function DecisionListSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 6 }).map((_, index) => (
        <div key={index} className="rounded-md border p-3">
          <Skeleton className="h-4 w-3/4" />
          <Skeleton className="mt-2 h-3 w-1/2" />
        </div>
      ))}
    </div>
  );
}

function DecisionListItem({
  item,
  active,
  onClick,
}: {
  item: ReviewItem;
  active: boolean;
  onClick: () => void;
}) {
  const meta = TYPE_META[item.type];
  const Icon = meta.icon;
  return (
    <button
      type="button"
      onClick={onClick}
      className={`mb-1.5 w-full rounded-md border p-3 text-left transition ${
        active ? "border-primary bg-accent/60" : "hover:bg-muted/50"
      }`}
    >
      <div className="flex items-center gap-2">
        <Icon className={`size-4 ${meta.tone}`} />
        <span className="text-xs text-muted-foreground">{meta.label}</span>
        <RiskBadge level={item.risk_level} />
      </div>
      <p className="mt-2 line-clamp-2 text-sm font-medium">{item.title}</p>
      {item.summary && (
        <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
          {item.summary}
        </p>
      )}
    </button>
  );
}

function DecisionDetail({
  item,
  onOpenSource,
}: {
  item: ReviewItem;
  onOpenSource: (item: ReviewItem) => void;
}) {
  const meta = TYPE_META[item.type];
  const Icon = meta.icon;
  const actionMutation = useReviewItemAction();
  const runAction = (action: ReviewItemAction) => {
    if (action === "open_source") {
      onOpenSource(item);
      return;
    }
    actionMutation.mutate(
      { id: item.id, action },
      {
        onSuccess: () => toast.success(`${ACTION_LABEL[action]}完成`),
        onError: (err: unknown) =>
          toast.error(err instanceof Error ? err.message : "操作失败"),
      },
    );
  };

  return (
    <div className="flex min-h-full flex-col">
      <div className="border-b p-5">
        <div className="flex items-center gap-2">
          <Icon className={`size-4 ${meta.tone}`} />
          <Badge variant="secondary">{meta.label}</Badge>
          <RiskBadge level={item.risk_level} />
          <Badge variant={item.status === "pending" ? "default" : "outline"}>
            {STATUS_LABEL[item.status]}
          </Badge>
        </div>
        <h2 className="mt-3 text-xl font-semibold tracking-normal">{item.title}</h2>
        {item.summary && (
          <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
            {item.summary}
          </p>
        )}
        <div className="mt-4 flex flex-wrap gap-2">
          {item.available_actions.map((action) => {
            const ActionIcon = ACTION_ICON[action] ?? Check;
            const disabled =
              item.status !== "pending" ||
              actionMutation.isPending ||
              ["promote", "assign"].includes(action);
            return (
              <Button
                key={action}
                size="sm"
                variant={action === "approve" ? "default" : "outline"}
                disabled={disabled && action !== "open_source"}
                onClick={() => runAction(action)}
              >
                <ActionIcon className="mr-1.5 size-3.5" />
                {ACTION_LABEL[action]}
              </Button>
            );
          })}
        </div>
      </div>

      <div className="grid gap-4 p-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-4">
          <DetailBlock title={detailTitle(item.type)}>
            <TypeSpecificDetail item={item} />
          </DetailBlock>
          {item.diff && (
            <DetailBlock title="Diff">
              <pre className="max-h-[420px] overflow-auto rounded-md bg-muted p-3 text-xs leading-5">
                {item.diff}
              </pre>
            </DetailBlock>
          )}
        </div>
        <DetailBlock title="来源">
          <dl className="space-y-3 text-sm">
            <MetaRow label="源对象" value={formatObjectRef(item.source_object_type, item.source_object_id)} />
            <MetaRow label="目标对象" value={formatObjectRef(item.target_object_type, item.target_object_id)} />
            <MetaRow label="创建时间" value={formatTime(item.created_at)} />
            <MetaRow label="更新时间" value={formatTime(item.updated_at)} />
          </dl>
        </DetailBlock>
      </div>
    </div>
  );
}

function DetailBlock({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border bg-background">
      <div className="border-b px-4 py-2.5 text-sm font-medium">{title}</div>
      <div className="p-4">{children}</div>
    </section>
  );
}

function TypeSpecificDetail({ item }: { item: ReviewItem }) {
  if (item.type === "plan_review") {
    const spec = (item.payload.spec ?? {}) as Record<string, unknown>;
    return <KeyValuePreview data={spec} />;
  }
  if (item.type === "skill_review") {
    const proposal = ((item.payload.skill_proposal ?? {}) as Record<string, unknown>);
    return <KeyValuePreview data={proposal} keys={["operation", "proposed_name", "summary", "rationale", "validation_status", "confidence"]} />;
  }
  if (item.type === "issue_change_review") {
    const patch = ((item.payload.issue_patch ?? item.payload) as Record<string, unknown>);
    return <KeyValuePreview data={patch} />;
  }
  return <KeyValuePreview data={item.payload} />;
}

function KeyValuePreview({
  data,
  keys,
}: {
  data: Record<string, unknown>;
  keys?: string[];
}) {
  const entries = (keys ?? Object.keys(data)).filter((key) => data[key] != null);
  if (entries.length === 0) {
    return <p className="text-sm text-muted-foreground">暂无结构化详情</p>;
  }
  return (
    <dl className="space-y-3 text-sm">
      {entries.map((key) => (
        <MetaRow
          key={key}
          label={key}
          value={
            typeof data[key] === "string"
              ? (data[key] as string)
              : JSON.stringify(data[key], null, 2)
          }
        />
      ))}
    </dl>
  );
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 whitespace-pre-wrap break-words text-foreground">
        {value || "-"}
      </dd>
    </div>
  );
}

function RiskBadge({ level }: { level: ReviewItem["risk_level"] }) {
  const label = level === "high" ? "高风险" : level === "medium" ? "中风险" : "低风险";
  return (
    <Badge variant={level === "high" ? "destructive" : "outline"}>{label}</Badge>
  );
}

function detailTitle(type: ReviewItemType) {
  switch (type) {
    case "skill_review":
      return "Skill 变更";
    case "issue_change_review":
      return "Issue 修改内容";
    case "plan_review":
      return "Plan 规格";
    case "artifact_review":
      return "Artifact 预览";
  }
}

function formatObjectRef(type: string, id: string | null) {
  return id ? `${type || "object"}:${id}` : type || "-";
}

function formatTime(value: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function sourceHref(item: ReviewItem, paths: ReturnType<typeof useWorkspacePaths>) {
  if (item.target_object_type === "issue" && item.target_object_id) {
    return paths.issueDetail(item.target_object_id);
  }
  if (item.target_object_type === "plan" && item.target_object_id) {
    return paths.planDetail(item.target_object_id);
  }
  if (item.target_object_type === "skill" && item.target_object_id) {
    return paths.skillDetail(item.target_object_id);
  }
  const projectId = getStringPayload(item.payload, "project_id");
  if (projectId) return paths.projectDetail(projectId);
  return "";
}

function getStringPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "string" ? value : "";
}
