package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const FeishuChatBuiltinKey = "multica/feishu-chat"
const FeishuChatProjectSelectionAction = "feishu_chat_select_project"
const maxFeishuProjectSelectionCandidates = 10

type FeishuChatService struct {
	Queries   *db.Queries
	TxStarter interface {
		Begin(ctx context.Context) (pgx.Tx, error)
	}
	TaskService *TaskService
	Feishu      *FeishuIssueService
}

type FeishuChatMessageInput struct {
	OpenID    string
	ChatID    string
	RootID    string
	MessageID string
	Content   string
}

type FeishuChatProjectSelectionInput struct {
	OpenID            string
	PendingID         string
	SelectedProjectID string
}

type feishuChatResolvedProject struct {
	Project db.Project
	Source  string
}

func (s *FeishuChatService) Enabled() bool {
	return s != nil && s.Queries != nil && s.TxStarter != nil && s.TaskService != nil && s.Feishu != nil && s.Feishu.ChatEnabled()
}

func (s *FeishuChatService) replyEnabled() bool {
	return s != nil && s.Queries != nil && s.Feishu != nil && s.Feishu.ChatEnabled()
}

func (s *FeishuChatService) HandleIncomingText(ctx context.Context, in FeishuChatMessageInput) (map[string]string, error) {
	if !s.Enabled() {
		return map[string]string{"status": "disabled"}, nil
	}
	in.OpenID = strings.TrimSpace(in.OpenID)
	in.ChatID = strings.TrimSpace(in.ChatID)
	in.RootID = strings.TrimSpace(in.RootID)
	in.MessageID = strings.TrimSpace(in.MessageID)
	in.Content = strings.TrimSpace(in.Content)
	if in.OpenID == "" || in.ChatID == "" || in.MessageID == "" || in.Content == "" {
		return map[string]string{"status": "ignored"}, nil
	}
	userID, err := s.Feishu.UserIDForOpenID(ctx, in.OpenID)
	if err != nil {
		slog.Warn("feishu chat user resolve failed", "open_id", in.OpenID, "error", err)
		_ = s.Feishu.SendTextToChat(ctx, in.ChatID, "无法识别你的 Multica 账号，请确认飞书邮箱已经在 Multica 中注册。")
		return map[string]string{"status": "unknown_user"}, nil
	}
	userUUID := util.MustParseUUID(userID)
	workspaces, err := s.workspacesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(workspaces) == 0 {
		_ = s.Feishu.SendTextToChat(ctx, in.ChatID, "你的 Multica 账号还没有可用工作区，暂时无法处理这条消息。")
		return map[string]string{"status": "workspace_ambiguous"}, nil
	}
	if len(workspaces) == 1 {
		return s.enqueueResolvedChat(ctx, workspaces[0], userUUID, in, nil)
	}

	resolved, candidates, err := s.resolveProjectForUserMessage(ctx, userUUID, in)
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		workspace, err := s.Queries.GetWorkspace(ctx, resolved.Project.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return s.enqueueResolvedChat(ctx, workspace, userUUID, in, resolved)
	}
	if len(candidates) > 0 {
		pending, err := s.createProjectSelection(ctx, userUUID, in, candidates)
		if err != nil {
			return nil, err
		}
		if err := s.Feishu.SendInteractiveToChat(ctx, in.ChatID, s.buildProjectSelectionCard(pending, candidates)); err != nil {
			return nil, fmt.Errorf("send feishu project selection card: %w", err)
		}
		return map[string]string{"status": "project_selection_required", "pending_id": util.UUIDToString(pending.ID)}, nil
	}
	_ = s.Feishu.SendTextToChat(ctx, in.ChatID, "我还不能确定你指的是哪个项目。请在消息里带上项目名后再试。")
	return map[string]string{"status": "project_ambiguous"}, nil
}

func (s *FeishuChatService) HandleProjectSelection(ctx context.Context, in FeishuChatProjectSelectionInput) (map[string]string, error) {
	if !s.Enabled() {
		return map[string]string{"status": "disabled"}, nil
	}
	in.OpenID = strings.TrimSpace(in.OpenID)
	in.PendingID = strings.TrimSpace(in.PendingID)
	in.SelectedProjectID = strings.TrimSpace(in.SelectedProjectID)
	if in.OpenID == "" || in.PendingID == "" || in.SelectedProjectID == "" {
		return map[string]string{"status": "invalid"}, nil
	}
	userID, err := s.Feishu.UserIDForOpenID(ctx, in.OpenID)
	if err != nil {
		slog.Warn("feishu project selection user resolve failed", "open_id", in.OpenID, "error", err)
		return map[string]string{"status": "unknown_user"}, nil
	}
	pending, err := s.Queries.GetFeishuChatPendingSelection(ctx, util.MustParseUUID(in.PendingID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{"status": "selection_not_found"}, nil
		}
		return nil, err
	}
	userUUID := util.MustParseUUID(userID)
	if uuidToComparable(pending.UserID) != uuidToComparable(userUUID) || pending.OpenID != in.OpenID {
		return map[string]string{"status": "forbidden"}, nil
	}
	if pending.Status != "pending" || (pending.ExpiresAt.Valid && time.Now().After(pending.ExpiresAt.Time)) {
		return map[string]string{"status": "selection_expired"}, nil
	}
	projectID := util.MustParseUUID(in.SelectedProjectID)
	if !projectInCandidates(projectID, pending.CandidateProjectIds) {
		return map[string]string{"status": "project_not_in_candidates"}, nil
	}
	project, err := s.Queries.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{"status": "project_not_found"}, nil
		}
		return nil, err
	}
	if _, err := s.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{WorkspaceID: project.WorkspaceID, UserID: userUUID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{"status": "forbidden"}, nil
		}
		return nil, err
	}
	if _, err := s.Queries.ConsumeFeishuChatPendingSelection(ctx, db.ConsumeFeishuChatPendingSelectionParams{
		ID:                pending.ID,
		SelectedProjectID: project.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{"status": "selection_consumed"}, nil
		}
		return nil, err
	}
	workspace, err := s.Queries.GetWorkspace(ctx, project.WorkspaceID)
	if err != nil {
		return nil, err
	}
	msgID := pending.FeishuMessageID
	if msgID == "" {
		msgID = util.UUIDToString(pending.ID)
	}
	return s.enqueueResolvedChat(ctx, workspace, userUUID, FeishuChatMessageInput{
		OpenID:    in.OpenID,
		ChatID:    pending.FeishuChatID,
		RootID:    pending.FeishuRootID,
		MessageID: msgID,
		Content:   pending.OriginalContent,
	}, &feishuChatResolvedProject{Project: project, Source: "card"})
}

func (s *FeishuChatService) enqueueResolvedChat(ctx context.Context, workspace db.Workspace, userID pgtype.UUID, in FeishuChatMessageInput, resolvedProject *feishuChatResolvedProject) (map[string]string, error) {
	agent, err := s.ensureFeishuChatAgent(ctx, workspace.ID)
	if err != nil {
		if errors.Is(err, errFeishuChatNoRuntime) {
			_ = s.Feishu.SendTextToChat(ctx, in.ChatID, "当前工作区没有在线的 Multica 运行时，暂时无法处理这条消息。")
			return map[string]string{"status": "runtime_unavailable"}, nil
		}
		return nil, err
	}
	projectID := pgtype.UUID{}
	if resolvedProject != nil {
		projectID = resolvedProject.Project.ID
	}
	binding, session, err := s.getOrCreateBinding(ctx, workspace.ID, userID, agent, in, projectID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(binding.LastMessageID) == in.MessageID {
		return map[string]string{"status": "duplicate", "chat_session_id": util.UUIDToString(session.ID)}, nil
	}
	if resolvedProject == nil && binding.ProjectID.Valid {
		project, err := s.Queries.GetProject(ctx, binding.ProjectID)
		if err == nil {
			resolvedProject = &feishuChatResolvedProject{Project: project, Source: "binding"}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	content := formatFeishuChatUserContent(in.Content, resolvedProject)

	msg, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: session.ID,
		Role:          "user",
		Content:       content,
	})
	if err != nil {
		return nil, fmt.Errorf("create feishu chat message: %w", err)
	}
	task, err := s.TaskService.EnqueueChatTask(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("enqueue feishu chat task: %w", err)
	}
	if err := s.Queries.TouchChatSession(ctx, session.ID); err != nil {
		slog.Warn("failed to touch feishu chat session", "chat_session_id", util.UUIDToString(session.ID), "error", err)
	}
	_ = s.Queries.UpdateFeishuChatBindingLastMessage(ctx, db.UpdateFeishuChatBindingLastMessageParams{
		ID:            binding.ID,
		LastMessageID: in.MessageID,
	})
	if resolvedProject != nil && (!binding.ProjectID.Valid || uuidToComparable(binding.ProjectID) != uuidToComparable(resolvedProject.Project.ID)) {
		_ = s.Queries.UpdateFeishuChatBindingProject(ctx, db.UpdateFeishuChatBindingProjectParams{
			ID:        binding.ID,
			ProjectID: resolvedProject.Project.ID,
		})
	}

	_ = s.Feishu.SendTextToChat(ctx, in.ChatID, "已收到，正在处理。")
	return map[string]string{
		"status":          "chat_task_enqueued",
		"chat_session_id": util.UUIDToString(session.ID),
		"message_id":      util.UUIDToString(msg.ID),
		"task_id":         util.UUIDToString(task.ID),
	}, nil
}

func (s *FeishuChatService) SendAssistantReply(ctx context.Context, chatSessionID, content string) error {
	if !s.replyEnabled() {
		return nil
	}
	content = strings.TrimSpace(content)
	if chatSessionID == "" || content == "" {
		return nil
	}
	binding, err := s.Queries.GetFeishuChatSessionBindingBySession(ctx, util.MustParseUUID(chatSessionID))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	}
	return s.Feishu.SendPostToChat(ctx, binding.FeishuChatID, content)
}

var errFeishuChatNoRuntime = errors.New("no online runtime for feishu chat agent")

func (s *FeishuChatService) singleWorkspaceForUser(ctx context.Context, userID string) (db.Workspace, bool, error) {
	rows, err := s.workspacesForUser(ctx, userID)
	if err != nil {
		return db.Workspace{}, false, err
	}
	if len(rows) != 1 {
		return db.Workspace{}, false, nil
	}
	return rows[0], true, nil
}

func (s *FeishuChatService) workspacesForUser(ctx context.Context, userID string) ([]db.Workspace, error) {
	rows, err := s.Queries.ListWorkspacesForUser(ctx, util.MustParseUUID(userID))
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *FeishuChatService) ensureFeishuChatAgent(ctx context.Context, workspaceID pgtype.UUID) (db.Agent, error) {
	runtime, err := s.Queries.GetPreferredFeishuChatRuntime(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Agent{}, errFeishuChatNoRuntime
		}
		return db.Agent{}, err
	}
	model := pgtype.Text{}
	if raw := strings.TrimSpace(os.Getenv("FEISHU_CHAT_AGENT_MODEL")); raw != "" {
		model = pgtype.Text{String: raw, Valid: true}
	}
	existing, err := s.Queries.GetBuiltInAgentByKey(ctx, db.GetBuiltInAgentByKeyParams{
		WorkspaceID: workspaceID,
		BuiltinKey:  pgtype.Text{String: FeishuChatBuiltinKey, Valid: true},
	})
	if err == nil {
		return s.Queries.UpdateBuiltInAgent(ctx, db.UpdateBuiltInAgentParams{
			ID:                 existing.ID,
			Name:               "Feishu Chat Agent",
			Description:        feishuChatAgentDescription,
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeID:          runtime.ID,
			MaxConcurrentTasks: 4,
			Instructions:       feishuChatAgentInstructions,
			DisplayName:        pgtype.Text{String: "飞书聊天 Agent", Valid: true},
			BuiltinKey:         pgtype.Text{String: FeishuChatBuiltinKey, Valid: true},
			Model:              model,
		})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, err
	}
	runtimeConfig, _ := json.Marshal(map[string]any{})
	customEnv, _ := json.Marshal(map[string]string{})
	customArgs, _ := json.Marshal([]string{})
	agent, err := s.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               "Feishu Chat Agent",
		Description:        feishuChatAgentDescription,
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      runtimeConfig,
		RuntimeID:          runtime.ID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 4,
		OwnerID:            pgtype.UUID{},
		Instructions:       feishuChatAgentInstructions,
		CustomEnv:          customEnv,
		CustomArgs:         customArgs,
		McpConfig:          nil,
		Model:              model,
		ThinkingLevel:      pgtype.Text{},
		IsInternal:         true,
		BuiltinKey:         pgtype.Text{String: FeishuChatBuiltinKey, Valid: true},
	})
	if err != nil {
		return db.Agent{}, err
	}
	return s.Queries.UpdateBuiltInAgent(ctx, db.UpdateBuiltInAgentParams{
		ID:                 agent.ID,
		Name:               "Feishu Chat Agent",
		Description:        feishuChatAgentDescription,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeID:          runtime.ID,
		MaxConcurrentTasks: 4,
		Instructions:       feishuChatAgentInstructions,
		DisplayName:        pgtype.Text{String: "飞书聊天 Agent", Valid: true},
		BuiltinKey:         pgtype.Text{String: FeishuChatBuiltinKey, Valid: true},
		Model:              model,
	})
}

func (s *FeishuChatService) getOrCreateBinding(ctx context.Context, workspaceID, userID pgtype.UUID, agent db.Agent, in FeishuChatMessageInput, projectID pgtype.UUID) (db.FeishuChatSessionBinding, db.ChatSession, error) {
	binding, err := s.Queries.GetFeishuChatSessionBinding(ctx, db.GetFeishuChatSessionBindingParams{
		WorkspaceID:  workspaceID,
		UserID:       userID,
		FeishuChatID: in.ChatID,
		FeishuRootID: in.RootID,
	})
	if err == nil {
		session, sessionErr := s.Queries.GetChatSessionInWorkspace(ctx, db.GetChatSessionInWorkspaceParams{
			ID:          binding.ChatSessionID,
			WorkspaceID: workspaceID,
		})
		return binding, session, sessionErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.FeishuChatSessionBinding{}, db.ChatSession{}, err
	}

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return db.FeishuChatSessionBinding{}, db.ChatSession{}, err
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)
	session, err := qtx.CreateChatSession(ctx, db.CreateChatSessionParams{
		WorkspaceID: workspaceID,
		AgentID:     agent.ID,
		CreatorID:   userID,
		Title:       "飞书聊天",
	})
	if err != nil {
		return db.FeishuChatSessionBinding{}, db.ChatSession{}, err
	}
	binding, err = qtx.CreateFeishuChatSessionBinding(ctx, db.CreateFeishuChatSessionBindingParams{
		WorkspaceID:   workspaceID,
		UserID:        userID,
		AgentID:       agent.ID,
		ChatSessionID: session.ID,
		FeishuChatID:  in.ChatID,
		FeishuRootID:  in.RootID,
		LastMessageID: "",
		ProjectID:     projectID,
	})
	if err != nil {
		return db.FeishuChatSessionBinding{}, db.ChatSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.FeishuChatSessionBinding{}, db.ChatSession{}, err
	}
	return binding, session, nil
}

func (s *FeishuChatService) resolveProjectForUserMessage(ctx context.Context, userID pgtype.UUID, in FeishuChatMessageInput) (*feishuChatResolvedProject, []db.Project, error) {
	projects, err := s.Queries.ListProjectsForUserWorkspaces(ctx, db.ListProjectsForUserWorkspacesParams{
		UserID: userID,
		Status: pgtype.Text{},
	})
	if err != nil {
		return nil, nil, err
	}
	if len(projects) == 0 {
		return nil, nil, nil
	}
	matches := matchProjectsFromText(in.Content, projects)
	if len(matches) == 1 {
		return &feishuChatResolvedProject{Project: matches[0], Source: "text"}, nil, nil
	}
	if len(matches) > 1 {
		return nil, limitProjects(matches, maxFeishuProjectSelectionCandidates), nil
	}
	if binding, err := s.findExistingBindingInAnyWorkspace(ctx, userID, in); err == nil && binding.ProjectID.Valid {
		if project, err := s.Queries.GetProject(ctx, binding.ProjectID); err == nil {
			return &feishuChatResolvedProject{Project: project, Source: "binding"}, nil, nil
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}
	return nil, limitProjects(projects, maxFeishuProjectSelectionCandidates), nil
}

func (s *FeishuChatService) findExistingBindingInAnyWorkspace(ctx context.Context, userID pgtype.UUID, in FeishuChatMessageInput) (db.FeishuChatSessionBinding, error) {
	return s.Queries.GetRecentFeishuChatSessionBindingForUserChat(ctx, db.GetRecentFeishuChatSessionBindingForUserChatParams{
		UserID:       userID,
		FeishuChatID: in.ChatID,
		FeishuRootID: in.RootID,
	})
}

func (s *FeishuChatService) createProjectSelection(ctx context.Context, userID pgtype.UUID, in FeishuChatMessageInput, candidates []db.Project) (db.FeishuChatPendingSelection, error) {
	ids := make([]pgtype.UUID, 0, len(candidates))
	for _, project := range candidates {
		ids = append(ids, project.ID)
	}
	return s.Queries.CreateFeishuChatPendingSelection(ctx, db.CreateFeishuChatPendingSelectionParams{
		UserID:              userID,
		OpenID:              in.OpenID,
		FeishuChatID:        in.ChatID,
		FeishuRootID:        in.RootID,
		FeishuMessageID:     in.MessageID,
		OriginalContent:     in.Content,
		CandidateProjectIds: ids,
		Ttl:                 pgtype.Interval{Microseconds: int64((30 * time.Minute) / time.Microsecond), Valid: true},
	})
}

func (s *FeishuChatService) buildProjectSelectionCard(pending db.FeishuChatPendingSelection, candidates []db.Project) map[string]any {
	elements := []any{
		map[string]any{
			"tag":  "div",
			"text": map[string]string{"tag": "lark_md", "content": "我还不能确定你指的是哪个项目。请选择一个项目继续处理这条消息。"},
		},
	}
	for _, project := range limitProjects(candidates, maxFeishuProjectSelectionCandidates) {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{
				feishuButton(project.Title, "primary", map[string]string{
					"multica_action": FeishuChatProjectSelectionAction,
					"pending_id":     util.UUIDToString(pending.ID),
					"project_id":     util.UUIDToString(project.ID),
				}),
			},
		})
	}
	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]string{"tag": "plain_text", "content": "选择 Multica 项目"},
		},
		"elements": elements,
	}
}

func matchProjectsFromText(text string, projects []db.Project) []db.Project {
	normalizedText := normalizeProjectMatchText(text)
	if normalizedText == "" {
		return nil
	}
	exact := make([]db.Project, 0)
	contains := make([]db.Project, 0)
	seen := map[string]bool{}
	for _, project := range projects {
		names := []string{project.Title}
		for _, name := range names {
			normalizedName := normalizeProjectMatchText(name)
			if normalizedName == "" || seen[util.UUIDToString(project.ID)] {
				continue
			}
			switch {
			case normalizedText == normalizedName:
				exact = append(exact, project)
				seen[util.UUIDToString(project.ID)] = true
			case strings.Contains(normalizedText, normalizedName):
				contains = append(contains, project)
				seen[util.UUIDToString(project.ID)] = true
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return contains
}

func normalizeProjectMatchText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		default:
			if r > unicode.MaxASCII {
				b.WriteRune(r)
				lastSpace = false
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func limitProjects(projects []db.Project, limit int) []db.Project {
	if limit <= 0 || len(projects) <= limit {
		return projects
	}
	out := append([]db.Project(nil), projects...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.Time.After(out[j].UpdatedAt.Time)
	})
	return out[:limit]
}

func projectInCandidates(projectID pgtype.UUID, candidates []pgtype.UUID) bool {
	target := uuidToComparable(projectID)
	for _, candidate := range candidates {
		if uuidToComparable(candidate) == target {
			return true
		}
	}
	return false
}

func formatFeishuChatUserContent(content string, resolvedProject *feishuChatResolvedProject) string {
	content = strings.TrimSpace(content)
	if resolvedProject == nil {
		return content
	}
	projectID := util.UUIDToString(resolvedProject.Project.ID)
	title := strings.TrimSpace(resolvedProject.Project.Title)
	return fmt.Sprintf("当前项目: %s (%s)\n用户消息: %s", title, projectID, content)
}

func uuidToComparable(id pgtype.UUID) string {
	return util.UUIDToString(id)
}

const feishuChatAgentDescription = "Built-in agent that answers Feishu chat messages by using Multica issues, comments, projects, and project knowledge."

const feishuChatAgentInstructions = `You are Multica's built-in Feishu chat agent.

You handle messages that users send to the Multica Feishu bot. Treat the Feishu message as the user's request in the current workspace.
If the user message includes a "当前项目: <title> (<project-id>)" line, use that project as authoritative. Do not switch projects unless the user explicitly names another project.

Use the Multica CLI as the source of truth:
- For progress questions, inspect issues with commands such as "multica issue list --output json", "multica issue get <id> --output json", and "multica issue comment list <issue-id> --output json".
- If the user asks to create or file an issue, create it with "multica issue create"; include a clear title and description derived from the chat message.
- For knowledge, wiki, memory, or project-context questions, first use "multica project knowledge search <project-id> --query <text> --output json". If that command is unavailable in the runtime CLI, fall back to project wiki list/get commands, but do not expose the command fallback or tool exploration process in the final answer.
- If you update issue status or comments, use the corresponding Multica CLI command and mention what changed.

Reply concisely in the user's language. Include issue numbers, titles, statuses, and links or IDs when useful. If you cannot determine the workspace/project/issue unambiguously, ask a short clarifying question instead of guessing.`
