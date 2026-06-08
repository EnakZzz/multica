package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type systemPipelineTemplate struct {
	Key         string
	Name        string
	Description string
	Nodes       []systemPipelineNodeTemplate
}

type systemPipelineNodeTemplate struct {
	Key               string
	NodeType          string
	Title             string
	Description       string
	DependsOnNodeKeys []string
	PositionX         int32
	PositionY         int32
	HarnessStrategy   service.HarnessStrategy
	ExecutionRouting  service.ExecutionRouting
}

var systemPipelineTemplates = []systemPipelineTemplate{
	{
		Key:         "systematic-debugging",
		Name:        "Systematic Debugging",
		Description: "Built-in pipeline using superpowers/systematic-debugging and superpowers/verification-before-completion: reproduce, trace root cause, validate hypotheses, make the minimal fix, and run regression checks.",
		Nodes: []systemPipelineNodeTemplate{
			{
				Key:         "reproduce",
				NodeType:    "issue",
				Title:       "Reproduce the failure",
				Description: "Create or document the smallest reliable reproduction. Capture the exact command, input, error, environment, and expected behavior before changing code.",
				PositionX:   0,
				PositionY:   0,
			},
			{
				Key:               "root-cause-analysis",
				NodeType:          "issue",
				Title:             "Trace the root cause",
				Description:       "Follow the real code path from symptom to source. Record the failing component, relevant state, and why the observed behavior happens.",
				DependsOnNodeKeys: []string{"reproduce"},
				PositionX:         280,
				PositionY:         0,
			},
			{
				Key:               "hypothesis-check",
				NodeType:          "check",
				Title:             "Validate the fix hypothesis",
				Description:       "Test the root-cause hypothesis before implementing broadly. Prefer a focused failing assertion, log, probe, or minimal experiment that can disprove the theory.",
				DependsOnNodeKeys: []string{"root-cause-analysis"},
				PositionX:         560,
				PositionY:         0,
				HarnessStrategy:   service.HarnessStrategy{Mode: service.HarnessStrategyModeAdversarialVerification, Summary: "Form and test competing root-cause hypotheses before implementation.", Rationale: "Independent hypothesis pressure reduces self-preferential debugging bias.", StopCondition: "One hypothesis is supported by reproduction evidence and competing theories are rejected.", Parallelism: 2},
			},
			{
				Key:               "minimal-fix",
				NodeType:          "issue",
				Title:             "Apply the minimal fix",
				Description:       "Change only the code required by the verified root cause. Preserve existing behavior outside the failing path.",
				DependsOnNodeKeys: []string{"hypothesis-check"},
				PositionX:         840,
				PositionY:         0,
			},
			{
				Key:               "regression-test",
				NodeType:          "check",
				Title:             "Run regression checks",
				Description:       "Run the reproduction again plus the narrowest relevant automated tests. Report exact commands and results.",
				DependsOnNodeKeys: []string{"minimal-fix"},
				PositionX:         1120,
				PositionY:         0,
				HarnessStrategy:   service.HarnessStrategy{Mode: service.HarnessStrategyModeLoopUntilDone, Summary: "Repeat the focused reproduction and regression checks until the failure is gone or a blocker is proven.", Rationale: "Debugging completion needs an explicit evidence-based stop condition.", StopCondition: "Reproduction and focused regression commands pass, or a blocking failure is reported.", Parallelism: 1},
			},
		},
	},
	{
		Key:         "test-driven-development",
		Name:        "Test-Driven Development",
		Description: "Built-in pipeline using superpowers/test-driven-development and superpowers/verification-before-completion: write a failing test, implement the smallest passing change, run focused checks, add edge cases, and prepare for review.",
		Nodes: []systemPipelineNodeTemplate{
			{
				Key:         "failing-test",
				NodeType:    "issue",
				Title:       "Write the failing test",
				Description: "Add the smallest test or assertion that captures the requested behavior or bug. Confirm it fails for the expected reason before implementation.",
				PositionX:   0,
				PositionY:   0,
			},
			{
				Key:               "minimal-implementation",
				NodeType:          "issue",
				Title:             "Implement the minimal passing change",
				Description:       "Make the smallest production change that turns the failing test green. Avoid broad refactors unless the test proves they are required.",
				DependsOnNodeKeys: []string{"failing-test"},
				PositionX:         280,
				PositionY:         0,
			},
			{
				Key:               "focused-test-run",
				NodeType:          "check",
				Title:             "Run focused tests",
				Description:       "Run the new test and nearest existing tests. Capture exact commands and failures before expanding scope.",
				DependsOnNodeKeys: []string{"minimal-implementation"},
				PositionX:         560,
				PositionY:         0,
			},
			{
				Key:               "edge-cases",
				NodeType:          "check",
				Title:             "Cover edge cases",
				Description:       "Add or update narrow tests for important boundaries, regressions, and integration contracts discovered during implementation.",
				DependsOnNodeKeys: []string{"focused-test-run"},
				PositionX:         840,
				PositionY:         0,
			},
			{
				Key:               "ready-for-review",
				NodeType:          "manual",
				Title:             "Prepare for review",
				Description:       "Summarize the behavior change, tests run, residual risks, and any follow-up that should remain out of scope.",
				DependsOnNodeKeys: []string{"edge-cases"},
				PositionX:         1120,
				PositionY:         0,
			},
		},
	},
	{
		Key:         "review-gated-feature-development",
		Name:        "Review-Gated Feature Development",
		Description: "Built-in pipeline using superpowers/writing-plans, superpowers/requesting-code-review, superpowers/receiving-code-review, and superpowers/verification-before-completion for explicit spec and code review gates.",
		Nodes: []systemPipelineNodeTemplate{
			{
				Key:         "implementation",
				NodeType:    "issue",
				Title:       "Implement the feature",
				Description: "Build the requested feature against the approved scope. Include focused verification evidence in the final output.",
				PositionX:   0,
				PositionY:   0,
			},
			{
				Key:               "spec-review",
				NodeType:          "spec_review",
				Title:             "Review spec compliance",
				Description:       "Review the implementation against the approved spec, plan, or issue requirements. Fail the gate for missing behavior or scope drift.",
				DependsOnNodeKeys: []string{"implementation"},
				PositionX:         280,
				PositionY:         0,
				HarnessStrategy:   service.HarnessStrategy{Mode: service.HarnessStrategyModeAdversarialVerification, Summary: "Verify implementation claims against the approved spec before downstream code review.", Rationale: "A separate verifier should challenge scope drift and missing behavior.", StopCondition: "Every accepted success criterion has matching evidence or a blocking finding.", Parallelism: 1},
			},
			{
				Key:               "code-review",
				NodeType:          "code_review",
				Title:             "Review code quality",
				Description:       "Review maintainability, architecture fit, tests, and risk. Fail the gate for blocking quality issues.",
				DependsOnNodeKeys: []string{"spec-review"},
				PositionX:         560,
				PositionY:         0,
				HarnessStrategy:   service.HarnessStrategy{Mode: service.HarnessStrategyModeAdversarialVerification, Summary: "Review correctness, regression risk, and maintainability from a skeptical perspective.", Rationale: "Code review benefits from an explicit adversarial rubric instead of accepting implementation self-report.", StopCondition: "No blocking findings remain, or repair work is required.", Parallelism: 1},
			},
			{
				Key:               "ready-for-human",
				NodeType:          "manual",
				Title:             "Ready for human",
				Description:       "Human handoff after implementation and review gates pass.",
				DependsOnNodeKeys: []string{"code-review"},
				PositionX:         840,
				PositionY:         0,
			},
			{
				Key:               "merge-to-main",
				NodeType:          "merge",
				Title:             "合入 / 集成 · Merge / Integrate",
				Description:       "After human confirmation, integrate the confirmed branch using PR-first behavior. Record source branch, target branch, PR URL or merge commit, test result, final status, and conflict files on failure.",
				DependsOnNodeKeys: []string{"ready-for-human"},
				PositionX:         1120,
				PositionY:         0,
			},
		},
	},
	{
		Key:         "writing-plans",
		Name:        "writing-plans",
		Description: "Built-in pipeline using superpowers/brainstorming and superpowers/writing-plans to turn unclear work into a reviewable spec and executable issue plan.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "clarify-goal", NodeType: "spec_review", Title: "Clarify goal and constraints", Description: "Capture the user goal, assumptions, open questions, out-of-scope work, and success criteria before execution planning.", PositionX: 0, PositionY: 0},
			{Key: "write-plan", NodeType: "issue", Title: "Write the execution plan", Description: "Produce concrete issue slices with acceptance criteria, dependencies, suggested verification, and agent recommendations.", DependsOnNodeKeys: []string{"clarify-goal"}, PositionX: 280, PositionY: 0},
			{Key: "review-plan", NodeType: "manual", Title: "Review plan with human", Description: "Human confirms scope, assumptions, and execution order before downstream issues are created.", DependsOnNodeKeys: []string{"write-plan"}, PositionX: 560, PositionY: 0},
		},
	},
	{
		Key:         "using-git-worktrees",
		Name:        "using-git-worktrees",
		Description: "Built-in pipeline using superpowers/using-git-worktrees for isolated implementation branches and clean handoff.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "prepare-worktree", NodeType: "issue", Title: "Prepare isolated worktree", Description: "Create or select a worktree and branch dedicated to this change. Record repo, branch, base ref, and setup command evidence.", PositionX: 0, PositionY: 0, ExecutionRouting: service.ExecutionRouting{RequiresIsolatedWorktree: true, BranchPolicy: service.ExecutionBranchPolicyPerItem, MergePolicy: service.ExecutionMergePolicyManual}},
			{Key: "implement-in-worktree", NodeType: "issue", Title: "Implement in worktree", Description: "Make the scoped change only inside the isolated branch and avoid mixing unrelated workspace changes.", DependsOnNodeKeys: []string{"prepare-worktree"}, PositionX: 280, PositionY: 0, ExecutionRouting: service.ExecutionRouting{RequiresIsolatedWorktree: true, BranchPolicy: service.ExecutionBranchPolicyPerItem, MergePolicy: service.ExecutionMergePolicyPRRequired}},
			{Key: "verify-and-handoff", NodeType: "check", Title: "Verify and hand off branch", Description: "Run focused checks, summarize changed files and branch state, and leave merge/review instructions.", DependsOnNodeKeys: []string{"implement-in-worktree"}, PositionX: 560, PositionY: 0},
		},
	},
	{
		Key:         "executing-plans",
		Name:        "executing-plans",
		Description: "Built-in pipeline using superpowers/executing-plans and superpowers/verification-before-completion for stepwise plan execution.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "load-plan", NodeType: "issue", Title: "Load and confirm plan", Description: "Read the approved plan, identify dependencies, and confirm the next executable slice before editing.", PositionX: 0, PositionY: 0},
			{Key: "execute-slice", NodeType: "issue", Title: "Execute selected slice", Description: "Complete the selected plan item against its acceptance criteria without expanding scope.", DependsOnNodeKeys: []string{"load-plan"}, PositionX: 280, PositionY: 0},
			{Key: "record-progress", NodeType: "check", Title: "Record progress and blockers", Description: "Update completion evidence, commands run, remaining blockers, and which plan item should run next.", DependsOnNodeKeys: []string{"execute-slice"}, PositionX: 560, PositionY: 0},
		},
	},
	{
		Key:         "subagent-driven-development",
		Name:        "subagent-driven-development",
		Description: "Built-in pipeline using superpowers/subagent-driven-development and superpowers/dispatching-parallel-agents for coordinated multi-agent delivery.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "decompose-work", NodeType: "issue", Title: "Decompose agent work", Description: "Split the goal into independent deliverables with required skills, inputs, outputs, dependencies, and review points.", PositionX: 0, PositionY: 0},
			{Key: "dispatch-agents", NodeType: "issue", Title: "Dispatch parallel agents", Description: "Assign independent work to suitable visible agents. Keep shared context and collision boundaries explicit.", DependsOnNodeKeys: []string{"decompose-work"}, PositionX: 280, PositionY: 0, HarnessStrategy: service.HarnessStrategy{Mode: service.HarnessStrategyModeFanOutSynthesize, Summary: "Fan out independent work to parallel agents with explicit ownership boundaries.", Rationale: "Independent context windows reduce interference and keep large work moving.", StopCondition: "Each delegated slice has a structured result or blocker.", Parallelism: 4, RequiresIsolatedWorktree: true}, ExecutionRouting: service.ExecutionRouting{RequiresIsolatedWorktree: true, BranchPolicy: service.ExecutionBranchPolicyPerAgent, MergePolicy: service.ExecutionMergePolicyManual}},
			{Key: "integrate-results", NodeType: "issue", Title: "Integrate results", Description: "Merge agent outputs, resolve conflicts, and produce a single coherent implementation or report.", DependsOnNodeKeys: []string{"dispatch-agents"}, PositionX: 560, PositionY: 0, HarnessStrategy: service.HarnessStrategy{Mode: service.HarnessStrategyModeFanOutSynthesize, Summary: "Synthesize parallel agent outputs into one coherent result.", Rationale: "Fan-out needs an explicit barrier that deduplicates, resolves conflicts, and preserves the original goal.", StopCondition: "All selected outputs are merged or explicitly rejected with rationale.", Parallelism: 1}, ExecutionRouting: service.ExecutionRouting{BranchPolicy: service.ExecutionBranchPolicyPerIteration, MergePolicy: service.ExecutionMergePolicyPRRequired}},
			{Key: "integration-check", NodeType: "check", Title: "Run integration checks", Description: "Verify the combined result with focused commands or manual evidence before handoff.", DependsOnNodeKeys: []string{"integrate-results"}, PositionX: 840, PositionY: 0},
		},
	},
	{
		Key:         "verification-before-completion",
		Name:        "verification-before-completion",
		Description: "Built-in pipeline using superpowers/verification-before-completion to require evidence before any work is marked complete.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "inspect-state", NodeType: "check", Title: "Inspect actual state", Description: "Check the real files, UI, API, task output, or runtime state that should prove completion.", PositionX: 0, PositionY: 0},
			{Key: "run-focused-verification", NodeType: "check", Title: "Run focused verification", Description: "Run exact relevant tests, builds, smoke checks, or manual checks; record commands and results.", DependsOnNodeKeys: []string{"inspect-state"}, PositionX: 280, PositionY: 0},
			{Key: "completion-decision", NodeType: "manual", Title: "Completion decision", Description: "Mark complete only if evidence satisfies acceptance criteria; otherwise create follow-up or reopen the blocked work.", DependsOnNodeKeys: []string{"run-focused-verification"}, PositionX: 560, PositionY: 0},
		},
	},
	{
		Key:         "requesting-code-review",
		Name:        "requesting-code-review",
		Description: "Built-in pipeline using superpowers/requesting-code-review to prepare a code review gate with clear scope and evidence.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "prepare-review-context", NodeType: "issue", Title: "Prepare review context", Description: "Summarize scope, changed files, intended behavior, known risks, and verification evidence for the reviewer.", PositionX: 0, PositionY: 0},
			{Key: "code-review-gate", NodeType: "code_review", Title: "Request code review", Description: "Reviewer checks correctness, regressions, tests, maintainability, and security before approval.", DependsOnNodeKeys: []string{"prepare-review-context"}, PositionX: 280, PositionY: 0},
		},
	},
	{
		Key:         "receiving-code-review",
		Name:        "receiving-code-review",
		Description: "Built-in pipeline using superpowers/receiving-code-review to triage and resolve review feedback without losing scope.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "triage-feedback", NodeType: "issue", Title: "Triage review feedback", Description: "Classify findings as blocking, non-blocking, or out of scope; preserve the reviewer evidence and rationale.", PositionX: 0, PositionY: 0},
			{Key: "apply-review-fixes", NodeType: "issue", Title: "Apply review fixes", Description: "Fix blocking feedback with focused changes and avoid unrelated refactors.", DependsOnNodeKeys: []string{"triage-feedback"}, PositionX: 280, PositionY: 0},
			{Key: "reverify-review", NodeType: "check", Title: "Re-verify after review", Description: "Run focused checks and summarize how each blocking review item was resolved.", DependsOnNodeKeys: []string{"apply-review-fixes"}, PositionX: 560, PositionY: 0},
		},
	},
	{
		Key:         "finishing-a-development-branch",
		Name:        "finishing-a-development-branch",
		Description: "Built-in pipeline using superpowers/finishing-a-development-branch for final branch cleanup, verification, review, and handoff.",
		Nodes: []systemPipelineNodeTemplate{
			{Key: "branch-cleanup", NodeType: "issue", Title: "Clean up development branch", Description: "Inspect diff, remove accidental changes, update docs or migrations, and ensure branch state is intentional.", PositionX: 0, PositionY: 0},
			{Key: "final-verification", NodeType: "check", Title: "Run final verification", Description: "Run the focused verification set and capture exact commands, results, and residual risks.", DependsOnNodeKeys: []string{"branch-cleanup"}, PositionX: 280, PositionY: 0},
			{Key: "handoff-review", NodeType: "code_review", Title: "Handoff for review", Description: "Prepare review notes, branch/commit information, test evidence, and remaining decisions for a human or reviewer agent.", DependsOnNodeKeys: []string{"final-verification"}, PositionX: 560, PositionY: 0},
			{Key: "merge-to-main", NodeType: "merge", Title: "合入 / 集成 · Merge / Integrate", Description: "After review and human confirmation, integrate the confirmed branch using PR-first behavior. Record source branch, target branch, PR URL or merge commit, test result, final status, and conflict files on failure.", DependsOnNodeKeys: []string{"handoff-review"}, PositionX: 840, PositionY: 0},
		},
	},
}

func (h *Handler) ensureSystemPipelines(ctx context.Context, workspaceID, createdBy pgtype.UUID) error {
	if !workspaceID.Valid || !createdBy.Valid {
		return nil
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	for _, tmpl := range systemPipelineTemplates {
		if err := h.ensureSystemPipeline(ctx, qtx, workspaceID, createdBy, tmpl); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (h *Handler) ensureSystemPipeline(ctx context.Context, qtx *db.Queries, workspaceID, createdBy pgtype.UUID, tmpl systemPipelineTemplate) error {
	pipeline, err := qtx.GetSystemPipelineByKey(ctx, db.GetSystemPipelineByKeyParams{
		WorkspaceID: workspaceID,
		SystemKey:   pgtype.Text{String: tmpl.Key, Valid: true},
	})
	if err != nil {
		if !isNotFound(err) {
			return err
		}
		name, err := h.uniquePipelineName(ctx, qtx, workspaceID, tmpl.Name, pgtype.UUID{})
		if err != nil {
			return err
		}
		pipeline, err = qtx.CreateSystemPipeline(ctx, db.CreateSystemPipelineParams{
			WorkspaceID:      workspaceID,
			Name:             name,
			Description:      tmpl.Description,
			DefaultProjectID: pgtype.UUID{},
			CreatedBy:        createdBy,
			SystemKey:        pgtype.Text{String: tmpl.Key, Valid: true},
		})
		if err != nil {
			if !isUniqueViolation(err) {
				return err
			}
			pipeline, err = qtx.GetSystemPipelineByKey(ctx, db.GetSystemPipelineByKeyParams{
				WorkspaceID: workspaceID,
				SystemKey:   pgtype.Text{String: tmpl.Key, Valid: true},
			})
			if err != nil {
				return err
			}
		}
	}

	name, err := h.uniquePipelineName(ctx, qtx, workspaceID, tmpl.Name, pipeline.ID)
	if err != nil {
		return err
	}
	if pipeline.Name != name || pipeline.Description != tmpl.Description || pipeline.ArchivedAt.Valid {
		pipeline, err = qtx.UpdateSystemPipelineMetadata(ctx, db.UpdateSystemPipelineMetadataParams{
			ID:          pipeline.ID,
			Name:        name,
			Description: tmpl.Description,
		})
		if err != nil {
			return err
		}
	}

	stages, err := qtx.ListPipelineStages(ctx, pipeline.ID)
	if err != nil {
		return err
	}
	if systemPipelineStagesMatch(stages, tmpl.Nodes) {
		return nil
	}
	if err := qtx.DeletePipelineStages(ctx, pipeline.ID); err != nil {
		return err
	}
	if err := qtx.DeletePipelineRoles(ctx, pipeline.ID); err != nil {
		return err
	}
	for i, node := range tmpl.Nodes {
		if _, err := qtx.CreatePipelineStage(ctx, db.CreatePipelineStageParams{
			PipelineID:         pipeline.ID,
			Key:                node.Key,
			Title:              node.Title,
			Description:        node.Description,
			RoleKey:            "",
			NodeType:           node.NodeType,
			AgentID:            pgtype.UUID{},
			DependsOnStageKeys: systemStringListOrEmpty(node.DependsOnNodeKeys),
			Position:           int32(i + 1),
			PositionX:          node.PositionX,
			PositionY:          node.PositionY,
			RepoKeys:           []string{},
			HarnessStrategy:    service.MarshalHarnessStrategy(node.HarnessStrategy),
			ExecutionRouting:   service.MarshalExecutionRouting(node.ExecutionRouting),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) uniquePipelineName(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, base string, currentID pgtype.UUID) (string, error) {
	pipelines, err := qtx.ListPipelines(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, pipeline := range pipelines {
		if currentID.Valid && pipeline.ID == currentID {
			continue
		}
		used[strings.ToLower(strings.TrimSpace(pipeline.Name))] = true
	}
	if !used[strings.ToLower(base)] {
		return base, nil
	}
	for i := 2; ; i++ {
		candidate := base + " (" + strconv.Itoa(i) + ")"
		if !used[strings.ToLower(candidate)] {
			return candidate, nil
		}
	}
}

func systemPipelineStagesMatch(stages []db.PipelineStage, nodes []systemPipelineNodeTemplate) bool {
	if len(stages) != len(nodes) {
		return false
	}
	for i, node := range nodes {
		stage := stages[i]
		if stage.Key != node.Key ||
			stage.NodeType != node.NodeType ||
			stage.Title != node.Title ||
			stage.Description != node.Description ||
			stage.Position != int32(i+1) ||
			stage.PositionX != node.PositionX ||
			stage.PositionY != node.PositionY ||
			string(stage.HarnessStrategy) != string(service.MarshalHarnessStrategy(node.HarnessStrategy)) ||
			string(stage.ExecutionRouting) != string(service.MarshalExecutionRouting(node.ExecutionRouting)) ||
			!sameStringList(stage.DependsOnStageKeys, node.DependsOnNodeKeys) {
			return false
		}
	}
	return true
}

func sameStringList(a, b []string) bool {
	a = systemStringListOrEmpty(a)
	b = systemStringListOrEmpty(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func systemStringListOrEmpty(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return values
}
