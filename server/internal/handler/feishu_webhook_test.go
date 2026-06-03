package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func setupFeishuWebhookTest(t *testing.T) {
	t.Helper()
	if testHandler == nil {
		t.Skip("handler test fixture not initialized")
	}
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS feishu_user_identity (
			user_id UUID PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
			email TEXT NOT NULL,
			open_id TEXT NOT NULL,
			union_id TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_user_identity_open_id ON feishu_user_identity(open_id)`,
		`CREATE TABLE IF NOT EXISTS feishu_message_binding (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
			inbox_item_id UUID NOT NULL REFERENCES inbox_item(id) ON DELETE CASCADE,
			recipient_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
			receive_id_type TEXT NOT NULL,
			receive_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			root_id TEXT,
			chat_id TEXT,
			card_action_value JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_message_binding_message_id ON feishu_message_binding(message_id)`,
		`CREATE TABLE IF NOT EXISTS feishu_event_delivery (
			event_id TEXT PRIMARY KEY,
			handled_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	} {
		if _, err := testPool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup feishu table: %v", err)
		}
	}
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	prev := testHandler.FeishuIssues
	testHandler.FeishuIssues = service.NewFeishuIssueServiceFromEnv(testHandler.Queries, testPool)
	t.Cleanup(func() { testHandler.FeishuIssues = prev })
	if testHandler.FeishuIssues == nil {
		t.Fatal("FeishuIssues should be configured in test")
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO feishu_user_identity (user_id, email, open_id, updated_at)
VALUES ($1, $2, 'ou_handler_test', now())
ON CONFLICT (user_id) DO UPDATE
SET email = EXCLUDED.email, open_id = EXCLUDED.open_id, updated_at = now()
`, testUserID, handlerTestEmail); err != nil {
		t.Fatalf("seed feishu identity: %v", err)
	}
}

func postFeishuWebhook(t *testing.T, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/feishu", &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	testHandler.HandleFeishuWebhook(w, req)
	return w
}

func createFeishuWebhookIssue(t *testing.T, status string) (string, string) {
	t.Helper()
	var number int32
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, position, number)
VALUES ($1, $2, $3, 'none', 'member', $4, 0, $5)
RETURNING id
`, testWorkspaceID, "Feishu webhook issue", status, testUserID, number).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	var inboxID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, body, details)
VALUES ($1, 'member', $2, 'status_changed', 'info', $3, 'Feishu webhook issue', 'body', '{}')
RETURNING id
`, testWorkspaceID, testUserID, issueID).Scan(&inboxID); err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID, inboxID
}

func TestFeishuWebhookChallenge(t *testing.T) {
	setupFeishuWebhookTest(t)
	w := postFeishuWebhook(t, map[string]any{
		"challenge": "challenge-token",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "challenge-token") {
		t.Fatalf("challenge response: %s", w.Body.String())
	}
}

func TestFeishuWebhookCardActionUpdatesIssueStatus(t *testing.T) {
	setupFeishuWebhookTest(t)
	issueID, inboxID := createFeishuWebhookIssue(t, "todo")
	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_card_" + issueID,
			"event_type": "card.action.trigger",
		},
		"event": map[string]any{
			"operator": map[string]any{"open_id": "ou_handler_test"},
			"action": map[string]any{"value": map[string]any{
				"multica_action": "set_status",
				"workspace_id":   testWorkspaceID,
				"issue_id":       issueID,
				"inbox_item_id":  inboxID,
				"status":         "in_progress",
			}},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "in_progress" {
		t.Fatalf("status: got %q", status)
	}
	var read bool
	if err := testPool.QueryRow(context.Background(), `SELECT read FROM inbox_item WHERE id = $1`, inboxID).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if !read {
		t.Fatal("inbox item should be marked read")
	}
}

func TestFeishuWebhookMessageReplyCreatesIssueComment(t *testing.T) {
	setupFeishuWebhookTest(t)
	issueID, inboxID := createFeishuWebhookIssue(t, "todo")
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO feishu_message_binding (
	workspace_id, issue_id, inbox_item_id, recipient_id,
	receive_id_type, receive_id, message_id, root_id, chat_id
) VALUES ($1, $2, $3, $4, 'open_id', 'ou_handler_test', 'om_root', 'om_root', 'oc_chat')
`, testWorkspaceID, issueID, inboxID, testUserID); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	content, _ := json.Marshal(map[string]string{"text": "飞书回复内容"})
	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_msg_" + issueID,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}},
			"message": map[string]any{
				"message_id":   "om_reply",
				"root_id":      "om_root",
				"chat_id":      "oc_chat",
				"message_type": "text",
				"content":      string(content),
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM comment
WHERE issue_id = $1 AND author_id = $2 AND content = '飞书回复内容'
`, issueID, testUserID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("comment count: got %d", count)
	}
}
