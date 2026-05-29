package handler

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const superpowersUpstreamCommit = "f2cbfbefebbfef77321e4c9abc9e949826bea9d7"

//go:embed builtin_superpowers/skills/*/SKILL.md
var builtInSuperpowersFS embed.FS

type builtInSkillTemplate struct {
	Key         string
	Name        string
	Description string
	Content     string
	Path        string
}

type builtInAgentTemplate struct {
	Key                string
	Name               string
	DisplayName        string
	Description        string
	Instructions       string
	MaxConcurrentTasks int32
	SkillKeys          []string
}

var builtInAgentTemplates = []builtInAgentTemplate{
	{
		Key:                "multica/planner",
		Name:               "规划Agent",
		DisplayName:        "规划 Agent",
		Description:        internalPlannerDescription,
		Instructions:       internalPlannerInstructions,
		MaxConcurrentTasks: 1,
		SkillKeys: []string{
			"superpowers/brainstorming",
			"superpowers/writing-plans",
			"superpowers/systematic-debugging",
			"superpowers/test-driven-development",
			"superpowers/verification-before-completion",
			"superpowers/requesting-code-review",
		},
	},
	{
		Key:                "multica/plan-writer",
		Name:               "Plan Writer",
		DisplayName:        "计划撰写 Agent",
		Description:        "Built-in planning agent for turning ambiguous goals into reviewable specs and executable plan slices.",
		Instructions:       "Use the visible Superpowers skills attached to this agent as the working contract. Clarify scope with brainstorming, then write concise specs and plans with acceptance criteria, dependencies, validation evidence, and review gates.",
		MaxConcurrentTasks: 2,
		SkillKeys: []string{
			"superpowers/brainstorming",
			"superpowers/writing-plans",
			"superpowers/verification-before-completion",
		},
	},
	{
		Key:                "multica/verifier",
		Name:               "Verifier",
		DisplayName:        "验证 Agent",
		Description:        "Built-in verification agent for completion checks, evidence gathering, and final readiness review.",
		Instructions:       "Use verification-before-completion as the working contract. Inspect the actual changed state, run or request focused checks, identify missing evidence, and report blockers before a task is marked complete.",
		MaxConcurrentTasks: 2,
		SkillKeys: []string{
			"superpowers/verification-before-completion",
		},
	},
	{
		Key:                "multica/code-reviewer",
		Name:               "Code Reviewer",
		DisplayName:        "代码评审 Agent",
		Description:        "Built-in review agent for requesting, performing, and responding to code review gates.",
		Instructions:       "Use requesting-code-review and receiving-code-review as the working contract. Lead with actionable findings, verify test evidence, and keep review feedback tied to concrete files, behavior, and risk.",
		MaxConcurrentTasks: 2,
		SkillKeys: []string{
			"superpowers/requesting-code-review",
			"superpowers/receiving-code-review",
			"superpowers/verification-before-completion",
		},
	},
	{
		Key:                "multica/debugging-agent",
		Name:               "Debugging Agent",
		DisplayName:        "调试 Agent",
		Description:        "Built-in debugging agent for reproducing failures, tracing root cause, and validating minimal fixes.",
		Instructions:       "Use systematic-debugging as the working contract. Reproduce the failure first, trace the real code path, validate hypotheses with evidence, make the smallest fix, and finish with regression checks.",
		MaxConcurrentTasks: 2,
		SkillKeys: []string{
			"superpowers/systematic-debugging",
			"superpowers/verification-before-completion",
		},
	},
	{
		Key:                "multica/merge-agent",
		Name:               "Merge Agent",
		DisplayName:        "合入 Agent",
		Description:        "Built-in integration agent for PR-first branch merge, protected-main handling, conflict reporting, and merge result audit comments.",
		Instructions:       "Use finishing-a-development-branch, using-git-worktrees, verification-before-completion, and requesting-code-review as the working contract. Integrate only after the human confirmation gate has authorized it. Prefer `multica repo integrate --strategy pr-first` to create or update a GitHub Pull Request or GitLab Merge Request into the project default branch/main. Do not target another feature branch unless the issue explicitly authorizes that target; then pass `--target <branch> --allow-non-default-target` and record the authorization. Detect protected main branches and do not silently direct-push. If PR/MR creation fails, stop and report the exact reason instead of falling back to direct merge. If direct integration is explicitly authorized, record that mode and validate the result. On failure, report the exact reason, conflict files, and retry or rollback recommendation instead of presenting success.",
		MaxConcurrentTasks: 1,
		SkillKeys: []string{
			"superpowers/finishing-a-development-branch",
			"superpowers/using-git-worktrees",
			"superpowers/verification-before-completion",
			"superpowers/requesting-code-review",
		},
	},
}

func loadBuiltInSuperpowersSkills() ([]builtInSkillTemplate, error) {
	entries, err := fs.ReadDir(builtInSuperpowersFS, "builtin_superpowers/skills")
	if err != nil {
		return nil, err
	}
	out := make([]builtInSkillTemplate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		skillPath := path.Join("builtin_superpowers/skills", name, "SKILL.md")
		raw, err := fs.ReadFile(builtInSuperpowersFS, skillPath)
		if err != nil {
			return nil, err
		}
		content := sanitizeNullBytes(string(raw))
		frontmatterName, description := parseSkillFrontmatter(content)
		if frontmatterName == "" {
			frontmatterName = name
		}
		out = append(out, builtInSkillTemplate{
			Key:         "superpowers/" + name,
			Name:        frontmatterName,
			Description: description,
			Content:     content,
			Path:        "skills/" + name + "/SKILL.md",
		})
	}
	return out, nil
}

func (h *Handler) ensureBuiltInAgents(ctx context.Context, runtime db.AgentRuntime) {
	workspaceID := runtime.WorkspaceID
	if !workspaceID.Valid {
		return
	}
	if err := h.ensureBuiltInSkills(ctx, workspaceID, runtime.OwnerID); err != nil {
		slog.Warn("built-in agent skill seed failed", "workspace_id", uuidToString(workspaceID), "error", err)
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("built-in agent seed transaction failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	createdAny := false
	for _, tmpl := range builtInAgentTemplates {
		created, err := h.ensureBuiltInAgent(ctx, qtx, workspaceID, runtime, tmpl)
		if err != nil {
			slog.Warn("built-in agent seed failed", "workspace_id", uuidToString(workspaceID), "builtin_key", tmpl.Key, "error", err)
			return
		}
		createdAny = createdAny || created
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("built-in agent seed commit failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	if createdAny {
		h.publish(protocol.EventAgentCreated, uuidToString(workspaceID), "system", "", map[string]any{})
	}
}

func (h *Handler) ensureBuiltInAgentsForWorkspace(ctx context.Context, workspaceID pgtype.UUID) {
	if !workspaceID.Valid {
		return
	}
	needed, err := h.builtInAgentsNeedSeed(ctx, workspaceID)
	if err != nil {
		slog.Warn("built-in agent seed check failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return
	}
	if !needed {
		return
	}
	runtime, ok := h.builtInAgentRuntimeForWorkspace(ctx, workspaceID)
	if !ok {
		return
	}
	h.ensureBuiltInAgents(ctx, runtime)
}

func (h *Handler) builtInAgentsNeedSeed(ctx context.Context, workspaceID pgtype.UUID) (bool, error) {
	for _, tmpl := range builtInAgentTemplates {
		agent, err := h.Queries.GetBuiltInAgentByKey(ctx, db.GetBuiltInAgentByKeyParams{
			WorkspaceID: workspaceID,
			BuiltinKey:  builtInKeyText(tmpl.Key),
		})
		if isNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid || !agent.DisplayName.Valid || agent.DisplayName.String != builtInAgentDisplayName(tmpl) {
			return true, nil
		}
	}
	return false, nil
}

func (h *Handler) builtInAgentRuntimeForWorkspace(ctx context.Context, workspaceID pgtype.UUID) (db.AgentRuntime, bool) {
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		slog.Warn("built-in agent runtime lookup failed", "workspace_id", uuidToString(workspaceID), "error", err)
		return db.AgentRuntime{}, false
	}
	var fallback db.AgentRuntime
	hasFallback := false
	for _, runtime := range runtimes {
		if runtime.Status != "online" {
			continue
		}
		if runtimeHasCapability(runtime.Metadata, daemonCapabilityIssuePlan) {
			return runtime, true
		}
		if !hasFallback {
			fallback = runtime
			hasFallback = true
		}
	}
	if hasFallback {
		return fallback, true
	}
	return db.AgentRuntime{}, false
}

func (h *Handler) ensureBuiltInAgent(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, runtime db.AgentRuntime, tmpl builtInAgentTemplate) (bool, error) {
	existing, err := qtx.GetBuiltInAgentByKey(ctx, db.GetBuiltInAgentByKeyParams{
		WorkspaceID: workspaceID,
		BuiltinKey:  builtInKeyText(tmpl.Key),
	})
	if isNotFound(err) && tmpl.Key == "multica/planner" {
		existing, err = qtx.GetInternalPlannerAgent(ctx, workspaceID)
	}
	created := false
	var agent db.Agent
	if err == nil {
		agent, err = qtx.UpdateBuiltInAgent(ctx, db.UpdateBuiltInAgentParams{
			ID:                 existing.ID,
			Name:               tmpl.Name,
			Description:        tmpl.Description,
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeID:          runtime.ID,
			MaxConcurrentTasks: tmpl.MaxConcurrentTasks,
			Instructions:       tmpl.Instructions,
			DisplayName:        pgtype.Text{String: builtInAgentDisplayName(tmpl), Valid: true},
			BuiltinKey:         builtInKeyText(tmpl.Key),
			Model:              pgtype.Text{},
		})
		if err != nil {
			return false, err
		}
	} else {
		if !isNotFound(err) {
			return false, err
		}
		runtimeConfig, _ := json.Marshal(map[string]any{})
		customEnv, _ := json.Marshal(map[string]string{})
		customArgs, _ := json.Marshal([]string{})
		agent, err = qtx.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID:        workspaceID,
			Name:               tmpl.Name,
			Description:        tmpl.Description,
			AvatarUrl:          pgtype.Text{},
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeConfig:      runtimeConfig,
			RuntimeID:          runtime.ID,
			Visibility:         "workspace",
			MaxConcurrentTasks: tmpl.MaxConcurrentTasks,
			OwnerID:            pgtype.UUID{},
			Instructions:       tmpl.Instructions,
			CustomEnv:          customEnv,
			CustomArgs:         customArgs,
			McpConfig:          nil,
			Model:              pgtype.Text{},
			ThinkingLevel:      pgtype.Text{},
			IsInternal:         true,
			BuiltinKey:         pgtype.Text{String: tmpl.Key, Valid: true},
		})
		if err != nil {
			return false, err
		}
		agent, err = qtx.UpdateBuiltInAgent(ctx, db.UpdateBuiltInAgentParams{
			ID:                 agent.ID,
			Name:               tmpl.Name,
			Description:        tmpl.Description,
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeID:          runtime.ID,
			MaxConcurrentTasks: tmpl.MaxConcurrentTasks,
			Instructions:       tmpl.Instructions,
			DisplayName:        pgtype.Text{String: builtInAgentDisplayName(tmpl), Valid: true},
			BuiltinKey:         builtInKeyText(tmpl.Key),
			Model:              pgtype.Text{},
		})
		if err != nil {
			return false, err
		}
		created = true
	}
	if err := attachBuiltInAgentSkills(ctx, qtx, workspaceID, agent.ID, tmpl.SkillKeys); err != nil {
		return false, err
	}
	return created, nil
}

func builtInAgentDisplayName(t builtInAgentTemplate) string {
	if strings.TrimSpace(t.DisplayName) != "" {
		return t.DisplayName
	}
	return t.Name
}

func builtInKeyText(key string) pgtype.Text {
	return pgtype.Text{String: key, Valid: key != ""}
}

func attachBuiltInAgentSkills(ctx context.Context, qtx *db.Queries, workspaceID, agentID pgtype.UUID, skillKeys []string) error {
	for _, key := range skillKeys {
		skill, err := qtx.GetBuiltInSkillByKey(ctx, db.GetBuiltInSkillByKeyParams{
			WorkspaceID: workspaceID,
			BuiltinKey:  builtInKeyText(key),
		})
		if err != nil {
			return fmt.Errorf("lookup built-in skill %s: %w", key, err)
		}
		if err := qtx.AddAgentSkill(ctx, db.AddAgentSkillParams{
			AgentID: agentID,
			SkillID: skill.ID,
		}); err != nil {
			return fmt.Errorf("attach built-in skill %s: %w", key, err)
		}
	}
	return nil
}

func builtInSkillConfig(t builtInSkillTemplate) []byte {
	config, _ := json.Marshal(map[string]any{
		"origin": map[string]any{
			"type":            "builtin_snapshot",
			"source":          "obra/superpowers",
			"upstream_url":    "https://github.com/obra/superpowers",
			"upstream_commit": superpowersUpstreamCommit,
			"upstream_path":   t.Path,
			"license":         "MIT",
		},
	})
	return config
}

func (h *Handler) ensureBuiltInSkills(ctx context.Context, workspaceID, createdBy pgtype.UUID) error {
	if !workspaceID.Valid {
		return nil
	}
	templates, err := loadBuiltInSuperpowersSkills()
	if err != nil {
		return err
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	for _, tmpl := range templates {
		if _, err := ensureBuiltInSkill(ctx, qtx, workspaceID, createdBy, tmpl); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func ensureBuiltInSkill(ctx context.Context, qtx *db.Queries, workspaceID, createdBy pgtype.UUID, tmpl builtInSkillTemplate) (db.Skill, error) {
	existing, err := qtx.GetBuiltInSkillByKey(ctx, db.GetBuiltInSkillByKeyParams{
		WorkspaceID: workspaceID,
		BuiltinKey:  builtInKeyText(tmpl.Key),
	})
	if isNotFound(err) {
		existing, err = qtx.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        tmpl.Name,
		})
	}
	if err == nil {
		return qtx.UpdateBuiltInSkill(ctx, db.UpdateBuiltInSkillParams{
			ID:          existing.ID,
			Name:        tmpl.Name,
			Description: tmpl.Description,
			Content:     tmpl.Content,
			Config:      builtInSkillConfig(tmpl),
			BuiltinKey:  builtInKeyText(tmpl.Key),
		})
	}
	if !isNotFound(err) {
		return db.Skill{}, err
	}
	created, err := qtx.CreateBuiltInSkill(ctx, db.CreateBuiltInSkillParams{
		WorkspaceID: workspaceID,
		Name:        tmpl.Name,
		Description: tmpl.Description,
		Content:     tmpl.Content,
		Config:      builtInSkillConfig(tmpl),
		CreatedBy:   createdBy,
		BuiltinKey:  builtInKeyText(tmpl.Key),
	})
	if err != nil {
		return db.Skill{}, fmt.Errorf("seed built-in skill %s: %w", tmpl.Key, err)
	}
	slog.Info("seeded built-in skill", "workspace_id", uuidToString(workspaceID), "builtin_key", tmpl.Key)
	return created, nil
}
