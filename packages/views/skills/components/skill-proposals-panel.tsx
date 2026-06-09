"use client";

import { useMemo, useState } from "react";
import { Check, FileText, GitMerge, RotateCw, X } from "lucide-react";
import type { SkillProposal, SkillSummary } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  skillListOptions,
  skillProposalListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { ScrollArea } from "@multica/ui/components/ui/scroll-area";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

function proposalTone(proposal: SkillProposal) {
  if (proposal.risk_level === "high") return "destructive" as const;
  if (proposal.validation_status === "failed") return "destructive" as const;
  if (proposal.operation === "insert") return "secondary" as const;
  return "outline" as const;
}

function gateTone(proposal: SkillProposal) {
  if (proposal.validation_status === "passed") return "secondary" as const;
  if (proposal.validation_status === "failed") return "destructive" as const;
  return "outline" as const;
}

function formatEvidence(refs: SkillProposal["evidence_refs"]) {
  if (!refs.length) return "No evidence refs recorded.";
  return refs
    .map((ref) => {
      const type = String(ref.type ?? "ref");
      const id = String(ref.id ?? "");
      const title = String(ref.title ?? ref.name ?? "");
      return [type, title, id].filter(Boolean).join(" · ");
    })
    .join("\n");
}

function targetName(proposal: SkillProposal, skills: SkillSummary[]) {
  if (!proposal.target_skill_id) return proposal.proposed_name || "New skill";
  return (
    skills.find((skill) => skill.id === proposal.target_skill_id)?.name ??
    proposal.proposed_name ??
    proposal.target_skill_id
  );
}

function formatScore(value: number | null) {
  return typeof value === "number" ? value.toFixed(2) : "n/a";
}

function formatEditOp(op: SkillProposal["edit_ops"][number], index: number) {
  const label = `${index + 1}. ${op.op.toUpperCase()} ${op.path}`;
  const section = op.section ? ` · ${op.section}` : "";
  const body = op.new_content ?? op.content ?? op.old_content ?? "";
  return `${label}${section}\n${body || "No content recorded."}`;
}

export function SkillProposalsPanel() {
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const { data: proposals = [], isLoading, error } = useQuery(
    skillProposalListOptions(wsId, "pending"),
  );
  const { data: skills = [] } = useQuery(skillListOptions(wsId));
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const selected = useMemo(() => {
    if (proposals.length === 0) return null;
    return proposals.find((p) => p.id === selectedId) ?? proposals[0];
  }, [proposals, selectedId]);

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: workspaceKeys.skillProposals(wsId, "pending"),
      }),
      queryClient.invalidateQueries({ queryKey: workspaceKeys.skills(wsId) }),
    ]);
  };

  const applyMutation = useMutation({
    mutationFn: (id: string) => api.applySkillProposal(id),
    onSuccess: async () => {
      toast.success("Skill proposal applied");
      setSelectedId(null);
      await refresh();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to apply proposal");
    },
  });

  const rejectMutation = useMutation({
    mutationFn: (id: string) => api.rejectSkillProposal(id),
    onSuccess: async () => {
      toast.success("Skill proposal rejected");
      setSelectedId(null);
      await refresh();
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to reject proposal");
    },
  });

  if (isLoading) {
    return (
      <div className="flex flex-1 min-h-0 gap-4">
        <Skeleton className="h-full w-80 rounded-md" />
        <Skeleton className="h-full flex-1 rounded-md" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
        <RotateCw className="h-6 w-6" />
        <p>{error instanceof Error ? error.message : "Failed to load proposals"}</p>
        <Button size="sm" variant="outline" onClick={() => refresh()}>
          Retry
        </Button>
      </div>
    );
  }

  if (proposals.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 rounded-lg border bg-background px-6 py-16 text-center">
        <GitMerge className="h-8 w-8 text-muted-foreground/50" />
        <div>
          <p className="text-sm font-medium">No pending skill proposals</p>
          <p className="mt-1 max-w-md text-xs text-muted-foreground">
            The curator will add proposals here when task evidence suggests a
            reusable skill change.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-1 min-h-0 overflow-hidden rounded-lg border bg-background">
      <div className="flex w-80 shrink-0 flex-col border-r">
        <div className="flex h-11 items-center justify-between border-b px-3">
          <div className="flex items-center gap-2 text-sm font-medium">
            <GitMerge className="h-4 w-4 text-muted-foreground" />
            Pending
          </div>
          <Badge variant="secondary">{proposals.length}</Badge>
        </div>
        <ScrollArea className="min-h-0 flex-1">
          <div className="p-2">
            {proposals.map((proposal) => {
              const active = selected?.id === proposal.id;
              return (
                <button
                  key={proposal.id}
                  type="button"
                  onClick={() => setSelectedId(proposal.id)}
                  className={[
                    "mb-2 w-full rounded-md border p-3 text-left transition-colors",
                    active
                      ? "border-brand bg-brand/5"
                      : "bg-background hover:bg-muted/60",
                  ].join(" ")}
                >
                  <div className="flex items-center gap-2">
                    <Badge variant={proposalTone(proposal)}>
                      {proposal.operation}
                    </Badge>
                    <Badge variant={gateTone(proposal)}>
                      {proposal.validation_status}
                    </Badge>
                    <span className="truncate text-xs text-muted-foreground">
                      {proposal.risk_level}
                    </span>
                  </div>
                  <p className="mt-2 line-clamp-2 text-sm font-medium">
                    {proposal.title}
                  </p>
                  <p className="mt-1 truncate text-xs text-muted-foreground">
                    {targetName(proposal, skills)}
                  </p>
                </button>
              );
            })}
          </div>
        </ScrollArea>
      </div>

      {selected && (
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex min-h-11 items-center justify-between gap-3 border-b px-4 py-2">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <Badge variant={proposalTone(selected)}>
                  {selected.operation}
                </Badge>
                <h2 className="truncate text-sm font-semibold">
                  {targetName(selected, skills)}
                </h2>
              </div>
              <p className="mt-1 truncate text-xs text-muted-foreground">
                {selected.title}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => rejectMutation.mutate(selected.id)}
                disabled={rejectMutation.isPending || applyMutation.isPending}
              >
                <X className="h-3.5 w-3.5" />
                Reject
              </Button>
              <Button
                size="sm"
                onClick={() => applyMutation.mutate(selected.id)}
                disabled={
                  rejectMutation.isPending ||
                  applyMutation.isPending ||
                  selected.validation_status === "failed"
                }
              >
                <Check className="h-3.5 w-3.5" />
                Apply
              </Button>
            </div>
          </div>
          <ScrollArea className="min-h-0 flex-1">
            <div className="grid gap-4 p-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
              <section className="space-y-4">
                <div>
                  <h3 className="text-xs font-semibold uppercase text-muted-foreground">
                    Gate
                  </h3>
                  <div className="mt-2 grid gap-2 rounded-md border bg-muted/40 p-3 text-xs text-muted-foreground sm:grid-cols-2">
                    <div>Status: {selected.validation_status}</div>
                    <div>Confidence: {selected.confidence}</div>
                    <div>Score: {formatScore(selected.validation_score_before)} - {formatScore(selected.validation_score_after)}</div>
                    <div>Token delta: {selected.token_delta}</div>
                    <div>Rejected similar: {selected.rejected_similar_count}</div>
                    <div>Curator: {selected.curator_model}</div>
                  </div>
                  <p className="mt-2 whitespace-pre-wrap text-xs leading-5 text-muted-foreground">
                    {selected.gate_reason || "No gate reason recorded."}
                  </p>
                </div>
                <div>
                  <h3 className="text-xs font-semibold uppercase text-muted-foreground">
                    Rationale
                  </h3>
                  <p className="mt-2 whitespace-pre-wrap text-sm leading-6">
                    {selected.rationale || selected.summary}
                  </p>
                </div>
                <div>
                  <h3 className="text-xs font-semibold uppercase text-muted-foreground">
                    Evidence
                  </h3>
                  <pre className="mt-2 whitespace-pre-wrap rounded-md bg-muted p-3 text-xs leading-5 text-muted-foreground">
                    {formatEvidence(selected.evidence_refs)}
                  </pre>
                </div>
                <div>
                  <h3 className="text-xs font-semibold uppercase text-muted-foreground">
                    Edit ops
                  </h3>
                  <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-3 text-xs leading-5">
                    {selected.edit_ops.length
                      ? selected.edit_ops.map(formatEditOp).join("\n\n")
                      : "No structured edit ops recorded."}
                  </pre>
                </div>
                <div>
                  <h3 className="text-xs font-semibold uppercase text-muted-foreground">
                    Diff
                  </h3>
                  <pre className="mt-2 max-h-80 overflow-auto rounded-md bg-muted p-3 text-xs leading-5">
                    {selected.diff || "No diff recorded."}
                  </pre>
                </div>
              </section>
              <section className="min-w-0">
                <h3 className="flex items-center gap-2 text-xs font-semibold uppercase text-muted-foreground">
                  <FileText className="h-3.5 w-3.5" />
                  Proposed SKILL.md
                </h3>
                <pre className="mt-2 min-h-96 overflow-auto rounded-md border bg-background p-4 text-xs leading-5">
                  {selected.proposed_content}
                </pre>
              </section>
            </div>
          </ScrollArea>
        </div>
      )}
    </div>
  );
}
