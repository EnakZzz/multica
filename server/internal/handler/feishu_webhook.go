package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
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
		h.handleFeishuCardAction(w, r, event)
		return
	}
	if strings.Contains(eventType, "im.message") || nestedMap(event, "message") != nil {
		h.handleFeishuMessageEvent(w, r, event)
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

func (h *Handler) handleFeishuCardAction(w http.ResponseWriter, r *http.Request, event map[string]any) {
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
		writeJSON(w, http.StatusOK, feishuToast("warning", "没有可处理的操作"))
		return
	}

	userID, err := h.FeishuIssues.UserIDForOpenID(r.Context(), openID)
	if err != nil {
		slog.Warn("feishu card action user resolve failed", "open_id", openID, "error", err)
		writeJSON(w, http.StatusOK, feishuToast("error", "无法识别飞书用户"))
		return
	}

	workspaceID := mapString(value, "workspace_id")
	issueID := mapString(value, "issue_id")
	inboxID := mapString(value, "inbox_item_id")
	actionName := mapString(value, "multica_action")
	if workspaceID == "" || issueID == "" || actionName == "" {
		writeJSON(w, http.StatusOK, feishuToast("warning", "卡片缺少操作参数"))
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, workspaceID); err != nil {
		writeJSON(w, http.StatusOK, feishuToast("error", "没有该工作区权限"))
		return
	}

	switch actionName {
	case "set_status":
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          parseUUID(issueID),
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			writeJSON(w, http.StatusOK, feishuToast("error", "Issue 不存在"))
			return
		}
		if _, err := h.updateIssueStatusForMember(r.Context(), issue, userID, mapString(value, "status")); err != nil {
			var statusErr handlerStatusError
			if errors.As(err, &statusErr) {
				writeJSON(w, http.StatusOK, feishuToast("error", statusErr.Message))
				return
			}
			slog.Warn("feishu set issue status failed", "issue_id", issueID, "error", err)
			writeJSON(w, http.StatusOK, feishuToast("error", "状态更新失败"))
			return
		}
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(r, workspaceID, userID, inboxID, "read")
		}
		writeJSON(w, http.StatusOK, feishuToast("success", "Issue 状态已更新"))
	case "mark_read":
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(r, workspaceID, userID, inboxID, "read")
		}
		writeJSON(w, http.StatusOK, feishuToast("success", "已读"))
	case "archive_inbox":
		if inboxID != "" {
			_ = h.applyFeishuInboxAction(r, workspaceID, userID, inboxID, "archive")
		}
		writeJSON(w, http.StatusOK, feishuToast("success", "已归档"))
	default:
		writeJSON(w, http.StatusOK, feishuToast("warning", "未知操作"))
	}
}

func (h *Handler) applyFeishuInboxAction(r *http.Request, workspaceID, userID, inboxID, action string) error {
	item, err := h.Queries.GetInboxItemInWorkspace(r.Context(), db.GetInboxItemInWorkspaceParams{
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
		_, err = h.Queries.MarkInboxRead(r.Context(), item.ID)
	case "archive":
		_, err = h.Queries.ArchiveInboxItem(r.Context(), item.ID)
	}
	return err
}

func (h *Handler) handleFeishuMessageEvent(w http.ResponseWriter, r *http.Request, event map[string]any) {
	message := nestedMap(event, "message")
	if message == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	messageID := mapString(message, "message_id")
	rootID := mapString(message, "root_id")
	if rootID == "" {
		rootID = mapString(message, "parent_id")
	}
	chatID := mapString(message, "chat_id")
	if messageID == "" || rootID == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	if mapString(message, "message_type") != "text" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	binding, err := h.FeishuIssues.BindingForMessage(r.Context(), messageID, rootID, chatID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("feishu message binding lookup failed", "message_id", messageID, "root_id", rootID, "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "unbound"})
		return
	}

	openID := nestedString(event, "sender", "sender_id", "open_id")
	userID, err := h.FeishuIssues.UserIDForOpenID(r.Context(), openID)
	if err != nil {
		slog.Warn("feishu message user resolve failed", "open_id", openID, "error", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "unknown_user"})
		return
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, binding.WorkspaceID); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "forbidden"})
		return
	}
	content := feishuTextContent(mapString(message, "content"))
	if content == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "empty"})
		return
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          parseUUID(binding.IssueID),
		WorkspaceID: parseUUID(binding.WorkspaceID),
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "issue_not_found"})
		return
	}
	if _, err := h.createIssueCommentForActor(r.Context(), issue, CreateCommentRequest{Content: content, Type: "comment"}, "member", userID, ""); err != nil {
		slog.Warn("feishu message create comment failed", "message_id", messageID, "issue_id", binding.IssueID, "error", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "comment_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "comment_created"})
}

func feishuToast(kind, content string) map[string]any {
	return map[string]any{
		"toast": map[string]string{
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
