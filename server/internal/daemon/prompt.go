package daemon

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// BuildPrompt constructs the task prompt for an agent CLI.
// Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md
// injected by execenv.InjectRuntimeConfig. The provider string is used by
// comment-triggered tasks: Codex's per-turn reply template needs the
// platform-aware "stdin or file" variant, every other provider gets a
// lightweight inline template (or Windows file for any provider on
// Windows).
func BuildPrompt(task Task, provider string) string {
	if task.ChatSessionID != "" {
		return buildChatPrompt(task)
	}
	if task.TriggerCommentID != "" {
		return buildCommentPrompt(task, provider)
	}
	if task.AutopilotRunID != "" {
		return buildAutopilotPrompt(task)
	}
	if task.QuickCreatePrompt != "" {
		return buildQuickCreatePrompt(task)
	}
	if task.IssuePlanPrompt != "" {
		return buildIssuePlanPrompt(task)
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then complete it.\n", task.IssueID)
	fmt.Fprintf(&b, "For comment history, follow the rule in your runtime workflow file (assignment-triggered tasks treat the read as mandatory). `multica issue comment list %s --output json` returns all comments for the issue (server caps at 2000). On long-running issues use `--recent 20 --output json` to read the 20 most recently active threads, then page older threads via the stderr `Next thread cursor: ...` line and the matching `--before` / `--before-id` until you have enough history. `--since <RFC3339>` is still available for incremental polling and may combine with `--recent`.\n", task.IssueID)
	if isReviewGateNodeType(task.PlanItemNodeType) {
		fmt.Fprintf(&b, "This issue is a blocking review gate with `node_type=%s`. Do not mark the issue `done`, `blocked`, or any other status yourself; the server applies the gate state after your task completes.\n", task.PlanItemNodeType)
		if strings.TrimSpace(task.ReviewTargetBranchName) != "" {
			identifier := strings.TrimSpace(task.ReviewTargetIdentifier)
			if identifier == "" {
				identifier = strings.TrimSpace(task.ReviewTargetIssueID)
			}
			fmt.Fprintf(&b, "A completed repair issue is the current review target: %s. Review the repair branch `%s`", identifier, task.ReviewTargetBranchName)
			if strings.TrimSpace(task.ReviewTargetCommitSHA) != "" {
				fmt.Fprintf(&b, " at commit `%s`", task.ReviewTargetCommitSHA)
			}
			b.WriteString(". When checking out the repository, pass this branch as the ref; do not review an older upstream implementation branch unless the repair branch cannot be fetched.\n")
		}
		b.WriteString("You must return the structured `review_gate` JSON required by the issue body. If you post a final issue comment, make that comment a single JSON object containing `review_gate` only: no markdown, no prose before or after it, and no natural-language PASS/FAIL as the automation contract. Your task completion output must also be that same JSON object.\n")
		b.WriteString("Use `review_gate.status=\"pass\"` only when downstream work can continue. Use `review_gate.status=\"fail\"` when blocking findings require implementation repair.\n")
	} else if strings.TrimSpace(task.PublishBranchName) != "" {
		checkoutRef := strings.TrimSpace(task.RepoCheckoutRef)
		if checkoutRef == "" {
			checkoutRef = strings.TrimSpace(task.PublishBranchName)
		}
		fmt.Fprintf(&b, "This issue is a review gate repair. Continue the existing target branch `%s`; do not create a separate repair branch for this work.\n", task.PublishBranchName)
		fmt.Fprintf(&b, "`multica repo checkout <url>` will default to ref `%s` and `multica repo publish` will push your HEAD back to `%s`. Your final issue comment must include `Branch: %s` and `Status: done` after publish succeeds, then mark the issue `done`.\n", checkoutRef, task.PublishBranchName, task.PublishBranchName)
	} else if task.PlanItemExecutionKind == "agent_task" && len(task.UnitTestChecklist) > 0 {
		b.WriteString("This issue was created from a Plan item with a server-gated unit test checklist. Complete the implementation, run the checklist commands below, and do not mark the issue done or blocked yourself; the server will update status from your final JSON report.\n")
		if task.PlanItemRequiresGitCommit && strings.TrimSpace(task.PlanItemBranchName) != "" {
			fmt.Fprintf(&b, "For code changes, use `multica repo checkout <url>` and publish the planned branch `%s` with `multica repo publish`. Never push directly to main or master.\n", task.PlanItemBranchName)
		} else {
			b.WriteString("This Plan item is not expected to produce a git commit unless the issue body or a human comment explicitly changes that. If code changes become necessary, use `multica repo checkout <url>` and publish with `multica repo publish`; never push directly to main or master.\n")
		}
		b.WriteString("Unit test checklist:\n")
		for _, check := range task.UnitTestChecklist {
			label := strings.TrimSpace(check.Title)
			if label == "" {
				label = strings.TrimSpace(check.ID)
			}
			if label == "" {
				label = "unit test"
			}
			required := "required"
			if !check.Required {
				required = "optional"
			}
			fmt.Fprintf(&b, "- id=%s %s title=%q command=%q expected=%q\n", check.ID, required, label, check.Command, check.Expected)
		}
		b.WriteString("Your final task completion output must be one JSON object and must include `unit_test_report`. If any required check fails, report failed rather than hiding it; the server will requeue this same issue for iteration.\n")
		b.WriteString(`Final JSON shape:
{
  "unit_test_report": {
    "status": "passed|failed",
    "checks": [
      {
        "id": "stable-check-id",
        "status": "passed|failed|skipped",
        "command": "go test ./internal/service -run TestName -count=1",
        "summary": "short result",
        "output_excerpt": "bounded failure excerpt"
      }
    ]
  },
  "branch": "published branch when any",
  "notes": "optional"
}
`)
		b.WriteString("Return JSON only: no markdown fences and no prose before or after it.\n")
	} else if task.PlanItemExecutionKind == "agent_task" {
		if task.PlanItemRequiresGitCommit && strings.TrimSpace(task.PlanItemBranchName) != "" {
			fmt.Fprintf(&b, "This issue was created from a Plan item with `execution_kind=agent_task`; that kind is the execution contract. For code changes, use `multica repo checkout <url>` and publish the planned branch `%s` with `multica repo publish`. Never push directly to main or master. Your final issue comment must include `Branch: %s` and `Status: done` after publish succeeds, then mark the issue `done`.\n", task.PlanItemBranchName, task.PlanItemBranchName)
		} else {
			b.WriteString("This issue was created from a Plan item with `execution_kind=agent_task`; that kind is the execution contract. This Plan item is not expected to produce a git commit unless the issue body or a human comment explicitly changes that. If code changes become necessary, use `multica repo checkout <url>` and publish with `multica repo publish`; never push directly to main or master. Your final issue comment must include `Status: done`, include `Branch: ...` only when a branch was actually published, then mark the issue `done`.\n")
		}
	} else {
		b.WriteString("For code changes, use `multica repo checkout <url>` and publish the generated work branch with `multica repo publish`. Never push directly to main or master. Your final issue comment must include `Branch: ...` and `Status: ready for review` after publish succeeds.\n")
	}
	return b.String()
}

func isReviewGateNodeType(nodeType string) bool {
	switch strings.TrimSpace(nodeType) {
	case "spec_review", "code_review":
		return true
	default:
		return false
	}
}

func buildIssuePlanPrompt(task Task) string {
	if task.IssuePlanPhase == "spec" {
		return buildIssuePlanSpecPrompt(task)
	}
	var b strings.Builder
	b.WriteString("You are running as an issue-planning assistant for a Multica workspace.\n\n")
	b.WriteString("A user approved a planning spec and now wants executable issue drafts. Do not create issues, do not call `multica issue create`, and do not modify workspace data. Your only job is to return one JSON object.\n\n")
	if task.IssuePlanID != "" {
		fmt.Fprintf(&b, "Plan ID: %s\n\n", task.IssuePlanID)
	}
	fmt.Fprintf(&b, "User goal:\n> %s\n\n", task.IssuePlanPrompt)
	if task.ProjectTitle != "" {
		fmt.Fprintf(&b, "Project: %s (%s)\n\n", task.ProjectTitle, task.ProjectID)
	}
	b.WriteString("Approved spec:\n")
	writePlanSpec(&b, task.IssuePlanSpec)
	b.WriteString("\n")
	b.WriteString("Language rules:\n")
	b.WriteString("- Write all user-facing natural-language fields in the returned JSON using the same primary language as the approved spec and user goal.\n")
	b.WriteString("- If the approved spec or user goal is primarily Chinese, write titles, descriptions, criteria, risk notes, confirmation questions, and confirmation reasons in Chinese.\n")
	b.WriteString("- Keep JSON property names, code identifiers, commands, file paths, API names, and proper nouns unchanged.\n\n")
	writeIssuePlanItemsQualityRules(&b)
	b.WriteString("Available pipelines you may select:\n")
	if len(task.AvailablePipelines) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, p := range task.AvailablePipelines {
			fmt.Fprintf(&b, "- id=%s name=%q description=%q", p.ID, p.Name, p.Description)
			if p.IsSystem {
				fmt.Fprintf(&b, " system_key=%q readonly=true methodology=true", p.SystemKey)
			}
			b.WriteString("\n")
			for _, n := range p.Nodes {
				fmt.Fprintf(&b, "  - node key=%s type=%s title=%q", n.Key, n.Type, n.Title)
				if n.AgentID != "" {
					fmt.Fprintf(&b, " agent_id=%s", n.AgentID)
				}
				if len(n.DependsOnNodeKeys) > 0 {
					fmt.Fprintf(&b, " depends_on=%q", strings.Join(n.DependsOnNodeKeys, ", "))
				}
				if len(n.Repos) > 0 {
					fmt.Fprintf(&b, " repos=%q", strings.Join(n.Repos, ", "))
				}
				if strings.TrimSpace(n.Description) != "" {
					fmt.Fprintf(&b, " description=%q", trimForPrompt(n.Description, 300))
				}
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")
	b.WriteString("Available agents you may recommend:\n")
	if len(task.AvailableAgents) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, a := range task.AvailableAgents {
			fmt.Fprintf(&b, "- id=%s name=%q description=%q", a.ID, a.Name, a.Description)
			if len(a.Skills) > 0 {
				fmt.Fprintf(&b, " skills=%q", strings.Join(a.Skills, ", "))
			}
			if strings.TrimSpace(a.Instructions) != "" {
				fmt.Fprintf(&b, " instructions=%q", trimForPrompt(a.Instructions, 500))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nOutput JSON schema:\n")
	b.WriteString(`{
  "needs_plan": true,
  "reason": "Why this needs or does not need a plan",
  "pipeline_id": "pipeline uuid when using a pipeline",
  "pipeline_name": "pipeline name fallback",
  "title": "Short plan title",
  "parent_issue": { "title": "Parent issue title", "description": "Parent issue description" },
  "pipeline": {
    "id": "pipeline uuid",
    "name": "pipeline name",
    "parent_issue": { "title": "Parent issue title", "description": "Parent issue description" },
    "nodes": [
        {
          "key": "existing pipeline node key",
          "title": "Issue title for this node",
          "description": "Issue description with node-specific context",
          "acceptance_criteria": ["Observable condition that proves this node is complete"],
          "suggested_test_commands": ["Exact command to verify this node, or []"],
          "unit_test_checklist": [{"id":"stable-check-id","title":"Unit test name","command":"Exact runnable unit test command","expected":"Expected passing result","required":true}],
          "context_resources": ["Relevant file path, repo alias, issue, doc, API, or URL"],
          "risk_notes": ["Concrete edge case, dependency, or failure mode"],
          "node_type": "issue | manual | check | spec_review | code_review",
          "execution_kind": "agent_task | human_confirmation",
          "confirmation_question": "Question a human must answer when execution_kind is human_confirmation, otherwise empty string",
          "confirmation_reason": "Why this cannot be safely confirmed during planning, otherwise empty string",
          "required_evidence": ["Evidence the human should inspect before marking confirmation done"],
          "requires_git_commit": true,
          "branch_name": "feature/module-capability-slug, or empty string when requires_git_commit is false",
          "agent_id": "agent uuid or empty string",
          "depends_on_node_keys": ["earlier-node-key"],
          "selected": true
      }
    ]
  },
    "direct_issue": {
      "title": "Issue title when needs_plan is false",
      "description": "Issue description with enough context for the assigned agent",
      "acceptance_criteria": ["Observable condition that proves this issue is complete"],
      "suggested_test_commands": ["Exact command to verify this issue, or []"],
      "unit_test_checklist": [{"id":"stable-check-id","title":"Unit test name","command":"Exact runnable unit test command","expected":"Expected passing result","required":true}],
      "context_resources": ["Relevant file path, repo alias, issue, doc, API, or URL"],
      "risk_notes": ["Concrete edge case, dependency, or failure mode"],
      "node_type": "issue | manual | check | spec_review | code_review",
      "execution_kind": "agent_task | human_confirmation",
      "confirmation_question": "Question a human must answer when execution_kind is human_confirmation, otherwise empty string",
      "confirmation_reason": "Why this cannot be safely confirmed during planning, otherwise empty string",
      "required_evidence": ["Evidence the human should inspect before marking confirmation done"],
      "requires_git_commit": true,
      "branch_name": "feature/module-capability-slug, or empty string when requires_git_commit is false",
      "recommended_agent_id": "agent uuid, null, or empty string",
      "match_score": 0,
      "match_reason": "Why this agent should handle the direct issue",
    "missing_capability": "Capability gap when no agent fits"
  },
  "items": [
      {
        "title": "Child issue title",
        "description": "Child issue description with enough context for the assigned agent",
        "acceptance_criteria": ["Observable condition that proves this item is complete"],
        "suggested_test_commands": ["Exact command to verify this item, or []"],
        "unit_test_checklist": [{"id":"stable-check-id","title":"Unit test name","command":"Exact runnable unit test command","expected":"Expected passing result","required":true}],
        "context_resources": ["Relevant file path, repo alias, issue, doc, API, or URL"],
        "risk_notes": ["Concrete edge case, dependency, or failure mode"],
        "node_type": "issue | manual | check | spec_review | code_review",
        "execution_kind": "agent_task | human_confirmation",
        "confirmation_question": "Question a human must answer when execution_kind is human_confirmation, otherwise empty string",
        "confirmation_reason": "Why this cannot be safely confirmed during planning, otherwise empty string",
        "required_evidence": ["Evidence the human should inspect before marking confirmation done"],
        "requires_git_commit": true,
        "branch_name": "feature/module-capability-slug, or empty string when requires_git_commit is false",
        "recommended_agent_id": "agent uuid or empty string",
        "match_score": 0,
        "match_reason": "Why this agent matches, or why no agent matches",
      "missing_capability": "Capability gap when match_score < 60 or no agent fits",
      "depends_on_positions": [1, 2],
      "selected": true
    }
  ]
}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Return JSON only. No markdown fences, prose, comments, or trailing text.\n")
	b.WriteString("- First decide whether the goal needs planning. Feature work, multi-step changes, cross-agent work, or unclear large goals should use needs_plan=true. Simple bug fixes or small single-agent changes should use needs_plan=false.\n")
	b.WriteString("- When needs_plan=false, do not execute the work yourself. Fill direct_issue with a concrete title and description so the server can save it as one editable plan item.\n")
	b.WriteString("- For direct_issue, use only agent IDs from Available agents. If no agent fits, set recommended_agent_id to null or empty string, match_score below 60, and put the missing role/tooling in missing_capability; the server will leave the plan item unassigned for a human to route before creating issues.\n")
	b.WriteString("- When needs_plan=true and Available pipelines is non-empty, choose the best existing pipeline and fill pipeline.nodes using only existing node keys. The server will create issues from that pipeline.\n")
	b.WriteString("- Built-in methodology pipelines are readonly references. Prefer systematic-debugging for bugfix/build/integration failures, test-driven-development when the goal calls for TDD, and review-gated-feature-development for high-risk feature work that needs spec and code review gates.\n")
	b.WriteString("- Fill each selected pipeline node with a concrete title, description, optional agent_id override, and dependency keys. Dependency keys should point to prerequisite nodes in the chosen pipeline.\n")
	b.WriteString("- If no available pipeline fits, fall back to items and split into 2-8 child issues.\n")
	b.WriteString("- For every direct_issue, item, or selected pipeline node, include a lightweight execution contract: 2-5 acceptance_criteria when possible, suggested_test_commands as exact runnable commands or [], unit_test_checklist as exact runnable unit-test commands or [], context_resources as known files/repos/docs/issues/APIs/URLs, and risk_notes as concrete edge cases or blockers.\n")
	b.WriteString("- unit_test_checklist is only for true unit tests that the assigned agent can run locally, such as `go test ./internal/service -run TestName -count=1` or `pnpm vitest packages/core/foo.test.ts`. If you do not know a real runnable unit test command, use []. Do not put broad build, typecheck, lint, e2e, smoke, or manual verification commands there; keep those in suggested_test_commands.\n")
	b.WriteString("- Preserve review gate semantics in node_type: use spec_review for spec compliance gates, code_review for blocking code quality gates, manual for human confirmation, check for agent-executable verification, and issue for implementation or ordinary work.\n")
	b.WriteString("- For every direct_issue, item, or selected pipeline node that can produce an actual git commit, set requires_git_commit=true and provide branch_name. Branch names must be module/function based, not agent-role based: prefer `feature/<module>-<capability>` for feature work, `fix/<module>-<bug>` for bug fixes, `refactor/<module>-<change>` for refactors, `test/<module>-<coverage>` for test-only work, `docs/<area>-<topic>` for documentation, `ci/<pipeline>-<change>` for CI, or `chore/<area>-<task>` for maintenance. Do not use `agent/<agent-role>/<issue>` style names.\n")
	b.WriteString("- Only set requires_git_commit=false and branch_name=\"\" when the issue is purely human confirmation, discussion, investigation/reporting, external coordination, or another task that is not expected to create a repository commit.\n")
	b.WriteString("- Set execution_kind=\"human_confirmation\" only when downstream work depends on a human decision that cannot be safely pre-planned, such as destructive changes, deploy or merge approval, ambiguous product choices, credential/access handoff, external dependency decisions, legal/content approval, or explicit risk acceptance.\n")
	b.WriteString("- Do not use human_confirmation for ordinary implementation, tests, routine review gates, or agent-executable checks. Use execution_kind=\"agent_task\" for normal agent work.\n")
	b.WriteString("- For human_confirmation items, leave recommended_agent_id empty, match_score 0, and provide confirmation_question, confirmation_reason, and required_evidence. Downstream items should depend on the confirmation item when they must wait for the human decision.\n")
	b.WriteString("- Keep execution contracts concise. Do not include full code, full patches, or step-by-step implementation scripts.\n")
	b.WriteString("- Do not invent file paths, commands, resources, or risks. Use [] when you do not know a useful value.\n")
	b.WriteString("- Use only agent IDs from Available agents. If no agent fits, set recommended_agent_id to empty string and match_score below 60.\n")
	b.WriteString("- A score of 90-100 means the agent's description/skills strongly match; 60-89 means acceptable; below 60 means缺乏合适智能体.\n")
	b.WriteString("- Use depends_on_positions for execution order. Values are 1-based item positions that must finish before this item starts. Only reference earlier items; use [] when there is no prerequisite. Example: integration testing should usually depend on implementation and QA setup items.\n")
	b.WriteString("- Never invent agents or capabilities. Put missing role/tooling in missing_capability.\n")
	return b.String()
}

func buildIssuePlanSpecPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as an issue-planning assistant for a Multica workspace.\n\n")
	b.WriteString("A user wants a goal evaluated before issue generation. Do not create issues, do not call `multica issue create`, and do not modify workspace data. Your only job is to return one JSON object for human review.\n\n")
	if task.IssuePlanID != "" {
		fmt.Fprintf(&b, "Plan ID: %s\n\n", task.IssuePlanID)
	}
	fmt.Fprintf(&b, "User goal:\n> %s\n\n", task.IssuePlanPrompt)
	if task.ProjectTitle != "" {
		fmt.Fprintf(&b, "Project: %s (%s)\n\n", task.ProjectTitle, task.ProjectID)
	}
	if hasPlanSpecDraft(task.IssuePlanSpec) {
		b.WriteString("Current draft spec and answered clarifications:\n")
		writePlanSpec(&b, task.IssuePlanSpec)
		b.WriteString("\n")
		b.WriteString("Revise the spec using the user's clarification answers. Remove answered questions from open_questions. Keep any still-unresolved decisions in open_questions.\n\n")
	}
	b.WriteString("Language rules:\n")
	b.WriteString("- Keep JSON property names exactly as requested in English.\n")
	b.WriteString("- Write every user-facing natural-language value in the same primary language as the user goal.\n")
	b.WriteString("- If the user goal is primarily Chinese, write summary, goal, criteria, scope, approach, assumptions, and open questions in Chinese.\n")
	b.WriteString("- Keep code identifiers, commands, file paths, API names, and proper nouns unchanged.\n\n")
	writeIssuePlanSpecQualityRules(&b)
	b.WriteString("Available pipelines you may consider:\n")
	if len(task.AvailablePipelines) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, p := range task.AvailablePipelines {
			fmt.Fprintf(&b, "- name=%q description=%q", p.Name, p.Description)
			if p.IsSystem {
				fmt.Fprintf(&b, " system_key=%q readonly=true methodology=true", p.SystemKey)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString("Available agents you may consider:\n")
	if len(task.AvailableAgents) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, a := range task.AvailableAgents {
			fmt.Fprintf(&b, "- name=%q description=%q", a.Name, a.Description)
			if len(a.Skills) > 0 {
				fmt.Fprintf(&b, " skills=%q", strings.Join(a.Skills, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nOutput JSON schema:\n")
	b.WriteString(`{
  "summary": "One-paragraph summary of the proposed plan",
  "goal": "Concrete goal the plan should accomplish",
  "success_criteria": ["Observable outcome that must be true"],
  "in_scope": ["Work that belongs in this plan"],
  "out_of_scope": ["Work intentionally excluded from this plan"],
  "approach": "Recommended implementation approach and important tradeoffs",
  "assumptions": ["Assumption to confirm or carry into execution"],
  "open_questions": ["Blocking question that materially changes scope or execution, max 2, or []"],
  "clarifications": [{"question": "Question answered by the user", "answer": "User answer"}]
}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Return JSON only. No markdown fences, prose, comments, or trailing text.\n")
	b.WriteString("- This is the human-reviewable spec, not the executable issue list. Do not include items, pipeline nodes, direct_issue, or issue creation commands.\n")
	b.WriteString("- Keep scope focused and practical. If a goal is too large, describe a safe first slice in in_scope and move the rest to out_of_scope.\n")
	b.WriteString("- Use built-in methodology pipelines as planning references when relevant: systematic-debugging for bugfix/build/integration failures, test-driven-development for TDD work, and review-gated-feature-development for high-risk feature work.\n")
	b.WriteString("- Put non-blocking uncertainty in assumptions. Use open_questions only for decisions that would materially change scope, data access, destructive behavior, or execution safety.\n")
	b.WriteString("- Ask at most 2 open questions. If you can proceed with a reasonable default, write the default as an assumption instead of asking.\n")
	b.WriteString("- Preserve existing clarifications exactly unless a new answer supersedes the same question.\n")
	b.WriteString("- summary and goal are required and must be non-empty.\n")
	return b.String()
}

func writeIssuePlanSpecQualityRules(b *strings.Builder) {
	b.WriteString("Planning quality rules:\n")
	b.WriteString("- Treat the user goal as the source of truth. Preserve named systems, repos, files, commands, product constraints, and explicit exclusions instead of flattening them into a generic plan.\n")
	b.WriteString("- Produce a reviewable spec, not a task list. The spec should let a human answer: what will be built, what will not be built, what evidence proves success, what risks exist, and what still needs a decision.\n")
	b.WriteString("- Success criteria must be observable. Prefer user-visible behavior, API/database state, generated artifacts, exact commands that should pass, or review evidence over vague goals like \"works well\".\n")
	b.WriteString("- Separate assumptions from open questions. Put reasonable defaults and non-blocking uncertainties in assumptions; put only blocking product, access, data, deployment, legal/content, destructive-change, or security decisions in open_questions.\n")
	b.WriteString("- Keep in_scope as the smallest coherent delivery slice. Move follow-up work, optional polish, broad refactors, and unrelated cleanup to out_of_scope unless the user explicitly requested them.\n")
	b.WriteString("- In approach, call out the likely implementation surfaces, validation strategy, dependency or migration risk, rollback/backout needs, and whether review-gated-feature-development should be used for high-risk work.\n")
	b.WriteString("- If the available agents or pipelines do not cover a required capability, record that gap in assumptions unless it blocks execution; do not invent agents, skills, repos, files, or commands.\n")
	b.WriteString("- Keep open_questions short and high-signal. Never ask more than 2 questions in one spec.\n\n")
}

func writeIssuePlanItemsQualityRules(b *strings.Builder) {
	b.WriteString("Executable planning rules:\n")
	b.WriteString("- The approved spec is binding. Do not add new product scope, omit accepted success criteria, or turn open questions into hidden implementation decisions.\n")
	b.WriteString("- Every selected item must be independently assignable to one agent, have a concrete deliverable, and include enough context for the assignee to start without rereading the whole plan.\n")
	b.WriteString("- Split by deliverable and dependency boundary, not by job title. Avoid both giant catch-all tasks and tiny command-only chores; target work that can be completed and reviewed as a meaningful unit.\n")
	b.WriteString("- No hidden work: include setup, data/schema changes, migrations/backfills, UI/server integration, documentation, verification, release/deploy handoff, and cleanup only when they are required by the approved spec.\n")
	b.WriteString("- Use review-gated-feature-development for high-risk feature work, cross-module changes, migrations, auth/security, data-loss risk, public API changes, release/deploy risk, or work that needs explicit spec and code review gates.\n")
	b.WriteString("- Review gates must depend on the implementation or repair work they review. Use spec_review for checking the delivered behavior against the approved spec; use code_review for correctness, regression, maintainability, security, and test-risk review.\n")
	b.WriteString("- Create explicit dependencies for true blocking order: implementation before verification, data/schema before consumers, setup before integration, review before human handoff, and human_confirmation before work that needs that decision.\n")
	b.WriteString("- Leave independent work dependency-free so it can run in parallel. Never add decorative dependencies, forward dependencies, cycles, or dependencies on items that are merely related but not blocking.\n")
	b.WriteString("- Acceptance criteria should be 2-5 concrete checks per executable item when possible. Suggested test commands must be exact runnable commands when known; use [] instead of inventing commands.\n")
	b.WriteString("- Recommend agents only from Available agents, and base match_score on the visible description, instructions, and skills. If no agent fits, leave the ID empty and state the missing capability.\n")
	b.WriteString("- Branch names must describe the module and change, not the agent. Use requires_git_commit=false only for pure human decisions, external coordination, or non-repository work.\n\n")
}

func writePlanSpec(b *strings.Builder, spec PlanSpecData) {
	fmt.Fprintf(b, "- Summary: %s\n", trimForPrompt(spec.Summary, 1000))
	fmt.Fprintf(b, "- Goal: %s\n", trimForPrompt(spec.Goal, 1000))
	writeSpecList(b, "Success criteria", spec.SuccessCriteria)
	writeSpecList(b, "In scope", spec.InScope)
	writeSpecList(b, "Out of scope", spec.OutOfScope)
	fmt.Fprintf(b, "- Approach: %s\n", trimForPrompt(spec.Approach, 1200))
	writeSpecList(b, "Assumptions", spec.Assumptions)
	writeSpecList(b, "Open questions", spec.OpenQuestions)
	if len(spec.Clarifications) == 0 {
		b.WriteString("- Clarifications: none\n")
		return
	}
	b.WriteString("- Clarifications:\n")
	for _, c := range spec.Clarifications {
		question := strings.TrimSpace(c.Question)
		answer := strings.TrimSpace(c.Answer)
		if question == "" || answer == "" {
			continue
		}
		fmt.Fprintf(b, "  - Q: %s\n", trimForPrompt(question, 500))
		fmt.Fprintf(b, "    A: %s\n", trimForPrompt(answer, 800))
	}
}

func hasPlanSpecDraft(spec PlanSpecData) bool {
	return strings.TrimSpace(spec.Summary) != "" ||
		strings.TrimSpace(spec.Goal) != "" ||
		len(spec.SuccessCriteria) > 0 ||
		len(spec.InScope) > 0 ||
		len(spec.OutOfScope) > 0 ||
		strings.TrimSpace(spec.Approach) != "" ||
		len(spec.Assumptions) > 0 ||
		len(spec.OpenQuestions) > 0 ||
		len(spec.Clarifications) > 0
}

func writeSpecList(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "- %s: none\n", label)
		return
	}
	fmt.Fprintf(b, "- %s:\n", label)
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			fmt.Fprintf(b, "  - %s\n", trimForPrompt(item, 500))
		}
	}
}

func trimForPrompt(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// buildQuickCreatePrompt constructs a prompt for quick-create tasks. The
// user typed a single natural-language sentence in the create-issue modal;
// the agent's job is to translate it into one `multica issue create` CLI
// invocation, using its judgment to decide whether fetching referenced URLs
// would produce a better issue. No issue exists yet, so the agent must NOT
// call `multica issue get` or attempt to comment — there's nothing to read
// or reply to.
func buildQuickCreatePrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a quick-create assistant for a Multica workspace.\n\n")
	b.WriteString("A user captured the following input via the quick-create modal. There is NO existing issue. Your job is to create a well-formed issue from this input with a single `multica issue create` command.\n\n")
	fmt.Fprintf(&b, "User input:\n> %s\n\n", task.QuickCreatePrompt)

	b.WriteString("Field rules:\n\n")

	// title
	b.WriteString("- **title**: required. A concise but semantically rich summary. If the input references external resources (PRs, issues, URLs), use your judgment on whether fetching the resource would produce a meaningfully better title — e.g. \"review PR #123\" → \"Review PR #123: Refactor auth module to OAuth2\". Strip filler words but preserve key semantic information.\n\n")

	// description — the core optimization
	b.WriteString("- **description**: The description is the executing agent's primary context. Aim for high fidelity — they should grasp the user's intent as if they had read the raw input themselves. Use a two-section structure:\n\n")
	b.WriteString("  1. **User request** — Faithfully restate what the user wants in their own words. Preserve specific names, identifiers, file paths, code snippets, and technical terms verbatim. Strip non-spec material before writing it (this is removal, not paraphrasing): verbal routing wrappers about creating the issue (e.g. \"create an issue\", \"分配给 X\") and pure conversational fillers (e.g. \"对吧？\"). When in doubt, keep it.\n\n")
	b.WriteString("     CC exception: `multica issue create` has no `--subscriber` flag, and the platform auto-subscribes members whose `[@Name](mention://member/<uuid>)` link appears in the description. When the user wrote \"cc @Y\", strip the verbal \"cc\" wrapper from the User request body and append a final `CC: <mention link(s)>` line to the description so the cc routing still fires.\n\n")
	b.WriteString("  2. **Context** — include ONLY when the input cited external resources AND you successfully fetched them AND they produced verifiable facts worth recording. Summarize facts only (e.g. \"PR #45 changes auth to JWT\"), not interpretation or unsolicited reference implementations. If you have nothing factual to add, omit the section entirely — never use it as an apology log for resources you could not fetch.\n\n")
	b.WriteString("  Hard rules: never invent requirements, implementation details, or acceptance criteria the user did not express; never reduce multi-sentence input to a single vague sentence; never echo the title.\n\n")

	// priority
	b.WriteString("- **priority**: one of `urgent`, `high`, `medium`, `low`, or omit. Map P0/P1 → urgent/high; \"asap\" → urgent. If unspecified, omit.\n\n")

	// assignee
	b.WriteString("- **assignee**:\n")
	b.WriteString("    - When the user names someone (\"assign to X\" / \"@X\"), call `multica workspace member list --output json` (and `multica agent list --output json` if it might be an agent) and find the matching entity by display name. On a clean unambiguous match, prefer `--assignee-id <uuid>` using the `user_id` (member) or `id` (agent) from that JSON — UUID matching is exact and robust to name collisions in workspaces with overlapping names. `--assignee <name>` (fuzzy) is acceptable as a fallback when names are unambiguous. On no match or ambiguous match, do NOT pass either flag — instead append a final line to the description: `Unrecognized assignee: X`.\n")
	b.WriteString("    - Treat bare @-routing as an assignee directive even when the user did not write the English word \"assign\". This includes Chinese imperatives like `让 @X review 这个 PR`, `给 @X 处理`, or `交给 @X`; strip the leading `@`/`＠` before matching display names. Do not keep that routing wrapper or `@Name` in the description unless it is a true CC-style notification rather than ownership.\n")
	agentID := ""
	agentName := ""
	if task.Agent != nil {
		agentID = task.Agent.ID
		agentName = task.Agent.Name
	}
	if agentID != "" {
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee-id %q` (your agent UUID). The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned. Use the UUID flag, not `--assignee <name>`, so the assignment is unambiguous even when other agents share part of your name.\n\n", agentID)
	} else if agentName != "" {
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee %q`. The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned.\n\n", agentName)
	} else {
		b.WriteString("    - When the user did NOT name an assignee, default to YOURSELF (the picker agent): pass `--assignee-id <your agent UUID>` (preferred) or `--assignee <your agent name>`. Never leave the issue unassigned.\n\n")
	}

	// project — pinned by the modal when the user picked one, otherwise
	// omitted so the platform routes to the workspace default. Always pass
	// the UUID (never a name) so the issue lands in the right project even
	// when several share a title.
	if task.ProjectID != "" {
		if task.ProjectTitle != "" {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in project %q (the user picked it in the quick-create modal). Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID, task.ProjectTitle)
		} else {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in the project the user picked in the quick-create modal. Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID)
		}
	} else {
		b.WriteString("- **project**: omit. The platform will route the issue to the workspace default.\n")
	}
	b.WriteString("- **status**: omit (defaults to `todo`).\n")
	b.WriteString("- **attachments**: do NOT pass `--attachment`. The flag only accepts LOCAL file paths. Any image URL in the user input is already markdown — keep it inline in `--description` instead.\n\n")

	// output format
	b.WriteString("Output format:\n")
	b.WriteString("- Run exactly one `multica issue create --output json` invocation. Do not retry for any reason — even on non-zero exit. The issue may already exist; another attempt would create a duplicate.\n")
	b.WriteString("- Parse the JSON response to read the created issue's `identifier` (preferred) or `id` (fallback). Do not scrape human output and do not assume any workspace issue prefix such as `MUL-`; workspaces can use custom prefixes.\n")
	b.WriteString("- After success, print exactly one line: `Created <identifier-or-id>: <title>` and exit. No commentary, no follow-up tool calls.\n")
	b.WriteString("- Do NOT call `multica issue get` or `multica issue comment add` — there is no issue to query or comment on.\n")
	b.WriteString("- On CLI error or JSON parse error, exit with the error as the only output. The platform writes a failure notification automatically.\n")
	return b.String()
}

// buildCommentPrompt constructs a prompt for comment-triggered tasks.
// The triggering comment content is embedded directly so the agent cannot
// miss it, even when stale output files exist in a reused workdir.
// The reply instructions (including the current TriggerCommentID as --parent)
// are re-emitted on every turn so resumed sessions cannot carry forward a
// previous turn's --parent UUID.
func buildCommentPrompt(task Task, provider string) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	if task.TriggerCommentContent != "" {
		authorLabel := "A user"
		if task.TriggerAuthorType == "agent" {
			name := task.TriggerAuthorName
			if name == "" {
				name = "another agent"
			}
			authorLabel = fmt.Sprintf("Another agent (%s)", name)
		}
		fmt.Fprintf(&b, "[NEW COMMENT] %s just left a new comment. Focus on THIS comment — do not confuse it with previous ones:\n\n", authorLabel)
		fmt.Fprintf(&b, "> %s\n\n", task.TriggerCommentContent)
		if task.TriggerAuthorType == "agent" {
			b.WriteString("⚠️ The triggering comment was posted by another agent. Decide whether a reply is warranted. If you produced actual work this turn (investigated, fixed something, answered a real question), post the result as a normal reply — that is NOT a noise comment, and the standard rule that final results must be delivered via comment still applies. If the triggering comment was a pure acknowledgment, thanks, or sign-off AND you produced no work this turn, do NOT reply — and do NOT post a comment saying 'No reply needed' or similar. Simply exit with no output. Silence is the preferred way to end agent-to-agent threads. If you do reply, do not @mention the other agent as a sign-off (that re-triggers them and starts a loop).\n\n")
		}
	}
	fmt.Fprintf(&b, "Start by running `multica issue get %s --output json` to understand your task, then decide how to proceed.\n\n", task.IssueID)
	fmt.Fprintf(&b, "For comment history, read the triggering thread first: `multica issue comment list %s --thread %s --output json` returns the root and every reply in the same thread as the trigger comment. If you still need more context, `multica issue comment list %s --recent 20 --output json` pulls the 20 most recently active threads on the issue (each `--recent` page prints a `Next thread cursor: --before <ts> --before-id <root-id>` line on stderr — pass the same pair back to scroll older threads). Avoid the unfiltered `--output json` form on long-running issues; it dumps the full flat timeline (cap 2000) and wastes context. `--since <RFC3339>` is still available for incremental polling and may combine with `--thread` or `--recent`.\n\n", task.IssueID, task.TriggerCommentID, task.IssueID)
	b.WriteString(execenv.BuildCommentReplyInstructions(provider, task.IssueID, task.TriggerCommentID))
	return b.String()
}

// buildChatPrompt constructs a prompt for interactive chat tasks.
func buildChatPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a chat assistant for a Multica workspace.\n")
	b.WriteString("A user is chatting with you directly. Respond to their message.\n\n")
	fmt.Fprintf(&b, "User message:\n%s\n", task.ChatMessage)
	// List attachments by id + filename so the agent can fetch them via
	// the CLI. We deliberately do NOT inline the URL: chat attachments
	// live behind a signed CDN with a short TTL, so by the time the agent
	// has finished thinking the URL embedded in the markdown body may
	// have expired. `multica attachment download <id>` re-signs at click
	// time and is the only reliable path.
	if len(task.ChatMessageAttachments) > 0 {
		b.WriteString("\nAttachments on this message:\n")
		for _, a := range task.ChatMessageAttachments {
			if a.ContentType != "" {
				fmt.Fprintf(&b, "- id=%s filename=%q content_type=%s\n", a.ID, a.Filename, a.ContentType)
			} else {
				fmt.Fprintf(&b, "- id=%s filename=%q\n", a.ID, a.Filename)
			}
		}
		b.WriteString("Use `multica attachment download <id>` to fetch each file locally before referring to it.\n")
	}
	return b.String()
}

// buildAutopilotPrompt constructs a prompt for run_only autopilot tasks.
func buildAutopilotPrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a Multica workspace.\n\n")
	b.WriteString("This task was triggered by an Autopilot in run-only mode. There is no assigned Multica issue for this run.\n\n")
	fmt.Fprintf(&b, "Autopilot run ID: %s\n", task.AutopilotRunID)
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Autopilot ID: %s\n", task.AutopilotID)
	}
	if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "Autopilot title: %s\n", task.AutopilotTitle)
	}
	if task.AutopilotSource != "" {
		fmt.Fprintf(&b, "Trigger source: %s\n", task.AutopilotSource)
	}
	if strings.TrimSpace(string(task.AutopilotTriggerPayload)) != "" {
		fmt.Fprintf(&b, "Trigger payload:\n%s\n", strings.TrimSpace(string(task.AutopilotTriggerPayload)))
	}
	b.WriteString("\nAutopilot instructions:\n")
	if strings.TrimSpace(task.AutopilotDescription) != "" {
		b.WriteString(task.AutopilotDescription)
		b.WriteString("\n\n")
	} else if task.AutopilotTitle != "" {
		fmt.Fprintf(&b, "%s\n\n", task.AutopilotTitle)
	} else {
		b.WriteString("No additional autopilot instructions were provided. Inspect the autopilot configuration before proceeding.\n\n")
	}
	if task.AutopilotID != "" {
		fmt.Fprintf(&b, "Start by running `multica autopilot get %s --output json` if you need the full autopilot configuration, then complete the instructions above.\n", task.AutopilotID)
	} else {
		b.WriteString("Complete the instructions above.\n")
	}
	b.WriteString("Do not run `multica issue get`; this run does not have an issue ID.\n")
	return b.String()
}
