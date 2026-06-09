package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxFeishuWebhookBodyBytes = 1 << 20

func (h *Handler) HandleFeishuWebhook(w http.ResponseWriter, r *http.Request) {
	if h.FeishuIssues == nil {
		writeError(w, http.StatusNotFound, "feishu integration is not configured")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFeishuWebhookBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if challenge, _ := payload["challenge"].(string); challenge != "" {
		if !h.verifyFeishuToken(payload) {
			writeError(w, http.StatusUnauthorized, "invalid feishu verification token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
		return
	}
	if !h.verifyFeishuToken(payload) {
		writeError(w, http.StatusUnauthorized, "invalid feishu verification token")
		return
	}

	eventID := nestedString(payload, "header", "event_id")
	if eventID == "" {
		eventID, _ = payload["uuid"].(string)
	}
	reserved, err := h.FeishuIssues.ReserveEvent(r.Context(), eventID)
	if err != nil {
		slog.Warn("feishu webhook event reserve failed", "event_id", eventID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reserve event")
		return
	}
	if !reserved {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	eventType := nestedString(payload, "header", "event_type")
	event, _ := payload["event"].(map[string]any)
	if event == nil {
		event = payload
	}

	if strings.Contains(eventType, "card") || nestedMap(event, "action") != nil {
		writeJSON(w, http.StatusOK, h.ProcessFeishuCardAction(r.Context(), event))
		return
	}
	if strings.Contains(eventType, "im.message") || nestedMap(event, "message") != nil {
		writeJSON(w, http.StatusOK, h.ProcessFeishuIncomingMessage(r.Context(), event))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

func (h *Handler) verifyFeishuToken(payload map[string]any) bool {
	expected := h.FeishuIssues.VerificationToken()
	if expected == "" {
		return true
	}
	token, _ := payload["token"].(string)
	if token == "" {
		token = nestedString(payload, "header", "token")
	}
	return token == expected
}

func (h *Handler) ProcessFeishuCardAction(ctx context.Context, event map[string]any) map[string]any {
	openID := nestedString(event, "operator", "open_id")
	if openID == "" {
		openID = nestedString(event, "operator", "operator_id", "open_id")
	}
	action := nestedMap(event, "action")
	value := nestedMap(action, "value")
	if value == nil {
		value = nestedMap(event, "action_value")
	}
	if value == nil {
		return feishuToast("warning", "没有可处理的操作")
	}

	userID, err := h.FeishuIssues.UserIDForOpenID(ctx, openID)
	if err != nil {
		slog.Warn("feishu card action user resolve failed", "open_id", openID, "error", err)
		return feishuToast("error", "无法识别飞书用户")
	}

	workspaceID := mapString(value, "workspace_id")
	issueID := mapString(value, "issue_id")
	inboxID := mapString(value, "inbox_item_id")
	actionName := mapString(value, "multica_action")
	if actionName == "" {
		return feishuToast("warning", "卡片缺少操作参数")
	}
	if actionName == service.FeishuChatProjectSelectionAction {
		return h.processFeishuChatProjectSelection(ctx, openID, value)
	}
	if binding, ok := h.feishuBindingForCardAction(ctx, event); ok {
		workspaceID = binding.WorkspaceID
		issueID = binding.IssueID
		inboxID = binding.InboxItemID
	}
	if workspaceID == "" || issueID == "" {
		return feishuToast("warning", "卡片不属于当前 Multica 服务或已过期")
	}
	if _, err := h.getWorkspaceMember(ctx, userID, workspaceID); err != nil {
		slog.Warn("feishu card action workspace member check failed",
			"open_id", openID,
			"user_id", userID,
			"workspace_id", workspaceID,
			"issue_id", issueID,
			"error", err)
		return feishuToast("error", "没有该工作区权限")
	}

	switch actionName {
	case "set_status", "reject_review":
		issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID:          parseUUID(issueID),
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			return feishuToast("error", "Issue 不存在")
		}
		nextStatus := mapString(value, "status")
		if actionName == "reject_review" && nextStatus == "" {
			nextStatus = "cancelled"
		}
		if _, err := h.updateIssueStatusForMember(ctx, issue, userID, nextStatus); err != nil {
			var statusErr handlerStatusError
			if errors.As(err, &statusErr) {
				return feishuToast("error", statusErr.Message)
			}
			slog.Warn("feishu set issue status failed", "issue_id", issueID, "error", err)
			return feishuToast("error", "状态更新失败")
		}
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(ctx, workspaceID, userID, inboxID, "read")
		}
		if actionName == "reject_review" {
			return feishuToast("success", "已拒绝；请直接回复这条飞书消息补充原因")
		}
		return feishuToast("success", "Issue 状态已更新")
	case "mark_read":
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(ctx, workspaceID, userID, inboxID, "read")
		}
		return feishuToast("success", "已读")
	case "archive_inbox":
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(ctx, workspaceID, userID, inboxID, "archive")
		}
		return feishuToast("success", "已归档")
	default:
		return feishuToast("warning", "未知操作")
	}
}

func (h *Handler) processFeishuChatProjectSelection(ctx context.Context, openID string, value map[string]any) map[string]any {
	if h.FeishuChat == nil {
		return feishuToast("error", "飞书聊天服务未启用")
	}
	result, err := h.FeishuChat.HandleProjectSelection(ctx, service.FeishuChatProjectSelectionInput{
		OpenID:            openID,
		PendingID:         mapString(value, "pending_id"),
		SelectedProjectID: mapString(value, "project_id"),
	})
	if err != nil {
		slog.Warn("feishu chat project selection failed", "open_id", openID, "error", err)
		return feishuToast("error", "项目选择处理失败")
	}
	switch result["status"] {
	case "chat_task_enqueued":
		return feishuToast("success", "已选择项目，正在处理")
	case "selection_consumed":
		return feishuToast("warning", "这次项目选择已经处理过")
	case "selection_expired", "selection_not_found":
		return feishuToast("warning", "这张项目选择卡片已过期，请重新发送消息")
	case "forbidden":
		return feishuToast("error", "没有该项目所在工作区权限")
	case "project_not_in_candidates", "project_not_found":
		return feishuToast("error", "项目不在可选范围内")
	case "runtime_unavailable":
		return feishuToast("error", "当前工作区没有在线的 Multica 运行时")
	default:
		return feishuToast("warning", "项目选择暂时无法处理")
	}
}

func (h *Handler) feishuBindingForCardAction(ctx context.Context, event map[string]any) (service.FeishuMessageBinding, bool) {
	messageID := nestedString(event, "context", "open_message_id")
	if messageID == "" {
		messageID = mapString(event, "message_id")
	}
	chatID := nestedString(event, "context", "open_chat_id")
	if chatID == "" {
		chatID = mapString(event, "chat_id")
	}
	if messageID == "" && chatID == "" {
		return service.FeishuMessageBinding{}, false
	}
	binding, err := h.FeishuIssues.BindingForMessage(ctx, messageID, "", chatID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("feishu card action binding lookup failed", "message_id", messageID, "chat_id", chatID, "error", err)
		}
		return service.FeishuMessageBinding{}, false
	}
	return binding, true
}

func (h *Handler) applyFeishuInboxAction(ctx context.Context, workspaceID, userID, inboxID, action string) error {
	item, err := h.Queries.GetInboxItemInWorkspace(ctx, db.GetInboxItemInWorkspaceParams{
		ID:          parseUUID(inboxID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return err
	}
	if item.RecipientType != "member" || uuidToString(item.RecipientID) != userID {
		return nil
	}
	switch action {
	case "read":
		_, err = h.Queries.MarkInboxRead(ctx, item.ID)
	case "archive":
		_, err = h.Queries.ArchiveInboxItem(ctx, item.ID)
	}
	return err
}

func (h *Handler) ProcessFeishuMessageReply(ctx context.Context, event map[string]any) map[string]string {
	message := nestedMap(event, "message")
	if message == nil {
		return map[string]string{"status": "ignored"}
	}
	messageID := mapString(message, "message_id")
	rootID := mapString(message, "root_id")
	if rootID == "" {
		rootID = mapString(message, "parent_id")
	}
	chatID := mapString(message, "chat_id")
	if messageID == "" || rootID == "" {
		return map[string]string{"status": "ignored"}
	}
	if mapString(message, "message_type") != "text" {
		return map[string]string{"status": "ignored"}
	}

	binding, err := h.FeishuIssues.BindingForMessage(ctx, messageID, rootID, chatID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("feishu message binding lookup failed", "message_id", messageID, "root_id", rootID, "error", err)
		}
		return map[string]string{"status": "unbound"}
	}

	openID := nestedString(event, "sender", "sender_id", "open_id")
	userID, err := h.FeishuIssues.UserIDForOpenID(ctx, openID)
	if err != nil {
		slog.Warn("feishu message user resolve failed", "open_id", openID, "error", err)
		return map[string]string{"status": "unknown_user"}
	}
	if _, err := h.getWorkspaceMember(ctx, userID, binding.WorkspaceID); err != nil {
		return map[string]string{"status": "forbidden"}
	}
	content := feishuTextContent(mapString(message, "content"))
	if content == "" {
		return map[string]string{"status": "empty"}
	}
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          parseUUID(binding.IssueID),
		WorkspaceID: parseUUID(binding.WorkspaceID),
	})
	if err != nil {
		return map[string]string{"status": "issue_not_found"}
	}
	if _, err := h.createIssueCommentForActor(ctx, issue, CreateCommentRequest{Content: content, Type: "comment"}, "member", userID, ""); err != nil {
		slog.Warn("feishu message create comment failed", "message_id", messageID, "issue_id", binding.IssueID, "error", err)
		return map[string]string{"status": "comment_failed"}
	}
	return map[string]string{"status": "comment_created"}
}

func (h *Handler) ProcessFeishuIncomingMessage(ctx context.Context, event map[string]any) map[string]string {
	replyResult := h.ProcessFeishuMessageReply(ctx, event)
	switch replyResult["status"] {
	case "comment_created", "unknown_user", "forbidden", "comment_failed", "issue_not_found", "empty":
		return replyResult
	}
	message := nestedMap(event, "message")
	if message == nil {
		return replyResult
	}
	if mapString(message, "message_type") != "text" {
		return replyResult
	}
	if senderType := strings.ToLower(nestedString(event, "sender", "sender_type")); senderType == "app" || senderType == "bot" {
		return map[string]string{"status": "ignored_self"}
	}
	content := feishuTextContent(mapString(message, "content"))
	if content == "" {
		return map[string]string{"status": "empty"}
	}
	if h.FeishuChat == nil || !h.FeishuChat.Enabled() {
		return map[string]string{"status": "chat_disabled"}
	}
	rootID := mapString(message, "root_id")
	if rootID == "" {
		rootID = mapString(message, "parent_id")
	}
	result, err := h.FeishuChat.HandleIncomingText(ctx, service.FeishuChatMessageInput{
		OpenID:    nestedString(event, "sender", "sender_id", "open_id"),
		ChatID:    mapString(message, "chat_id"),
		RootID:    rootID,
		MessageID: mapString(message, "message_id"),
		Content:   content,
	})
	if err != nil {
		slog.Warn("feishu chat message handling failed", "message_id", mapString(message, "message_id"), "error", err)
		return map[string]string{"status": "chat_failed"}
	}
	return result
}

func feishuToast(kind, content string) map[string]any {
	return map[string]any{
		"toast": map[string]any{
			"type":    kind,
			"content": content,
		},
	}
}

func feishuTextContent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return strings.TrimSpace(parsed.Text)
	}
	return raw
}

func nestedMap(v any, keys ...string) map[string]any {
	cur, _ := v.(map[string]any)
	for _, key := range keys {
		if cur == nil {
			return nil
		}
		next, _ := cur[key].(map[string]any)
		cur = next
	}
	return cur
}

func nestedString(v any, keys ...string) string {
	cur := v
	for _, key := range keys {
		m, _ := cur.(map[string]any)
		if m == nil {
			return ""
		}
		cur = m[key]
	}
	if s, ok := cur.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func mapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
