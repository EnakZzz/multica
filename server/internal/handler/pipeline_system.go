package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
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
}

var systemPipelineTemplates = []systemPipelineTemplate{
	{
		Key:         "systematic-debugging",
		Name:        "Systematic Debugging",
		Description: "Superpowers-style bugfix flow: reproduce, trace root cause, validate hypotheses, make the minimal fix, and run regression checks.",
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
			},
		},
	},
	{
		Key:         "test-driven-development",
		Name:        "Test-Driven Development",
		Description: "Superpowers-style TDD flow: write a failing test, implement the smallest passing change, run focused checks, add edge cases, and prepare for review.",
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
		Description: "Feature implementation with explicit spec review and code review gates before human handoff.",
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
			},
			{
				Key:               "code-review",
				NodeType:          "code_review",
				Title:             "Review code quality",
				Description:       "Review maintainability, architecture fit, tests, and risk. Fail the gate for blocking quality issues.",
				DependsOnNodeKeys: []string{"spec-review"},
				PositionX:         560,
				PositionY:         0,
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
