package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
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
		`CREATE TABLE IF NOT EXISTS feishu_chat_session_binding (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
			agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
			chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
			feishu_chat_id TEXT NOT NULL,
			feishu_root_id TEXT NOT NULL DEFAULT '',
			last_message_id TEXT NOT NULL DEFAULT '',
			project_id UUID REFERENCES project(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (workspace_id, user_id, feishu_chat_id, feishu_root_id)
		)`,
		`ALTER TABLE feishu_chat_session_binding ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES project(id) ON DELETE SET NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_chat_session_binding_session
			ON feishu_chat_session_binding(chat_session_id)`,
		`CREATE TABLE IF NOT EXISTS feishu_chat_pending_selection (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
			open_id TEXT NOT NULL,
			feishu_chat_id TEXT NOT NULL,
			feishu_root_id TEXT NOT NULL DEFAULT '',
			feishu_message_id TEXT NOT NULL,
			original_content TEXT NOT NULL,
			candidate_project_ids UUID[] NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			selected_project_id UUID REFERENCES project(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 minutes',
			consumed_at TIMESTAMPTZ
		)`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_status TEXT NOT NULL DEFAULT 'not_applicable'`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivered_at TIMESTAMPTZ`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_attempts INT NOT NULL DEFAULT 0`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_last_error TEXT`,
	} {
		if _, err := testPool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup feishu table: %v", err)
		}
	}
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	if _, err := testPool.Exec(context.Background(), `DELETE FROM feishu_event_delivery WHERE event_id LIKE 'evt_chat_%' OR event_id LIKE 'evt_bound_no_chat_%'`); err != nil {
		t.Fatalf("cleanup feishu event test state: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM feishu_chat_session_binding WHERE feishu_chat_id LIKE 'oc_chat_%'`); err != nil {
		t.Fatalf("cleanup feishu chat binding test state: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM feishu_chat_pending_selection WHERE feishu_chat_id LIKE 'oc_chat_%'`); err != nil {
		t.Fatalf("cleanup feishu chat pending selection test state: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM agent WHERE workspace_id = $1 AND builtin_key = 'multica/feishu-chat'`, testWorkspaceID); err != nil {
		t.Fatalf("cleanup feishu chat test state: %v", err)
	}
	prev := testHandler.FeishuIssues
	testHandler.FeishuIssues = service.NewFeishuIssueServiceFromEnv(testHandler.Queries, testPool)
	if testHandler.FeishuChat != nil {
		testHandler.FeishuChat.Feishu = testHandler.FeishuIssues
	}
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

func setupFeishuWebhookHTTPMock(t *testing.T) *httptest.Server {
	t.Helper()
	var messages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(w, http.StatusOK, map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-test-token",
				"expire":              3600,
			})
		case "/open-apis/im/v1/messages":
			messages++
			writeJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"message_id": fmt.Sprintf("om_sent_%d", messages),
					"root_id":    fmt.Sprintf("om_sent_%d", messages),
					"chat_id":    "oc_chat",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("FEISHU_BASE_URL", srv.URL)
	t.Cleanup(srv.Close)
	return srv
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

func TestFeishuLongConnectionCardActionUpdatesIssueStatus(t *testing.T) {
	setupFeishuWebhookTest(t)
	issueID, inboxID := createFeishuWebhookIssue(t, "todo")
	userID := "ou_handler_test"
	value := map[string]interface{}{
		"multica_action": "set_status",
		"workspace_id":   testWorkspaceID,
		"issue_id":       issueID,
		"inbox_item_id":  inboxID,
		"status":         "blocked",
	}
	event := &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{OpenID: userID},
			Action:   &callback.CallBackAction{Value: value},
		},
	}
	result := testHandler.ProcessFeishuCardAction(context.Background(), feishuCardActionEventMap(event))
	toast := nestedMap(result, "toast")
	if got := mapString(toast, "type"); got != "success" {
		t.Fatalf("toast type: got %q result=%v", got, result)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" {
		t.Fatalf("status: got %q", status)
	}
}

func TestFeishuCardActionUsesMessageBindingWorkspace(t *testing.T) {
	setupFeishuWebhookTest(t)
	issueID, inboxID := createFeishuWebhookIssue(t, "todo")
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO feishu_message_binding (
	workspace_id, issue_id, inbox_item_id, recipient_id,
	receive_id_type, receive_id, message_id, root_id, chat_id
) VALUES ($1, $2, $3, $4, 'open_id', 'ou_handler_test', 'om_card_binding', 'om_card_binding', 'oc_card_binding')
`, testWorkspaceID, issueID, inboxID, testUserID); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	event := map[string]any{
		"operator": map[string]any{"open_id": "ou_handler_test"},
		"context":  map[string]any{"open_message_id": "om_card_binding", "open_chat_id": "oc_card_binding"},
		"action": map[string]any{"value": map[string]any{
			"multica_action": "set_status",
			"workspace_id":   "00000000-0000-0000-0000-000000000000",
			"issue_id":       "00000000-0000-0000-0000-000000000000",
			"inbox_item_id":  "00000000-0000-0000-0000-000000000000",
			"status":         "blocked",
		}},
	}
	result := testHandler.ProcessFeishuCardAction(context.Background(), event)
	toast := nestedMap(result, "toast")
	if got := mapString(toast, "type"); got != "success" {
		t.Fatalf("toast type: got %q result=%v", got, result)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" {
		t.Fatalf("status: got %q", status)
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

func TestFeishuWebhookUnboundMessageCreatesChatTask(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	eventID := "evt_chat_unbound_" + suffix
	messageID := "om_chat_user_" + suffix
	chatID := "oc_chat_unbound_" + suffix
	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   eventID,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   messageID,
				"chat_id":      chatID,
				"message_type": "text",
				"content":      `{"text":"lost pet 现在进度如何"}`,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "chat_task_enqueued" {
		t.Fatalf("status: got %q body=%s", resp["status"], w.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*)
FROM feishu_chat_session_binding b
JOIN chat_message cm ON cm.chat_session_id = b.chat_session_id
JOIN agent_task_queue atq ON atq.chat_session_id = b.chat_session_id
WHERE b.feishu_chat_id = $1
  AND cm.role = 'user'
  AND cm.content = 'lost pet 现在进度如何'
  AND atq.status = 'queued'
`, chatID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one chat message/task binding, got %d", count)
	}
}

func TestFeishuWebhookUnboundMessagesReuseChatSession(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	chatID := "oc_chat_reuse_" + suffix
	post := func(eventID, messageID, text string) map[string]string {
		t.Helper()
		w := postFeishuWebhook(t, map[string]any{
			"header": map[string]any{
				"event_id":   eventID,
				"event_type": "im.message.receive_v1",
			},
			"event": map[string]any{
				"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
				"message": map[string]any{
					"message_id":   messageID,
					"chat_id":      chatID,
					"message_type": "text",
					"content":      `{"text":"` + text + `"}`,
				},
			},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
		}
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := post("evt_chat_reuse_1_"+suffix, "om_chat_reuse_1_"+suffix, "lost pet 现在进度如何")
	second := post("evt_chat_reuse_2_"+suffix, "om_chat_reuse_2_"+suffix, "继续说")
	if first["status"] != "chat_task_enqueued" || second["status"] != "chat_task_enqueued" {
		t.Fatalf("unexpected statuses: first=%v second=%v", first, second)
	}
	if first["chat_session_id"] == "" || first["chat_session_id"] != second["chat_session_id"] {
		t.Fatalf("expected same chat session, first=%q second=%q", first["chat_session_id"], second["chat_session_id"])
	}
	var sessions, messages int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(DISTINCT b.chat_session_id), count(cm.id)
FROM feishu_chat_session_binding b
JOIN chat_message cm ON cm.chat_session_id = b.chat_session_id
WHERE b.feishu_chat_id = $1
`, chatID).Scan(&sessions, &messages); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || messages != 2 {
		t.Fatalf("expected one session and two messages, got sessions=%d messages=%d", sessions, messages)
	}
}

func createFeishuChatExtraWorkspace(t *testing.T, slugSuffix string) string {
	t.Helper()
	ctx := context.Background()
	var workspaceID string
	slug := "feishu-chat-" + strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(slugSuffix))
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2 || '-' || substr(gen_random_uuid()::text, 1, 8), '', 'FCP')
RETURNING id
`, "Feishu Chat "+slugSuffix, slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create extra workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, testUserID); err != nil {
		t.Fatalf("add member to extra workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO agent_runtime (workspace_id, name, owner_id, daemon_id, provider, runtime_mode, status, device_info, metadata, visibility, timezone, last_seen_at)
VALUES ($1, 'Feishu Chat Runtime', $2, 'feishu-chat-daemon-' || substr(gen_random_uuid()::text, 1, 8), 'codex', 'local', 'online', '{}', '{}', 'public', 'UTC', now())
`, workspaceID, testUserID); err != nil {
		t.Fatalf("create extra workspace runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	return workspaceID
}

func createFeishuChatProject(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO project (workspace_id, title, priority)
VALUES ($1, $2, 'none')
RETURNING id
`, workspaceID, title).Scan(&projectID); err != nil {
		t.Fatalf("create project %q: %v", title, err)
	}
	return projectID
}

func TestFeishuWebhookMultiWorkspaceMessageWithProjectNameCreatesChatTask(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	extraWorkspaceID := createFeishuChatExtraWorkspace(t, t.Name())
	projectID := createFeishuChatProject(t, extraWorkspaceID, "Lost Pet")
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	chatID := "oc_chat_named_project_" + suffix

	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_chat_named_project_" + suffix,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   "om_chat_named_project_" + suffix,
				"chat_id":      chatID,
				"message_type": "text",
				"content":      `{"text":"lost pet 现在进度如何"}`,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "chat_task_enqueued" {
		t.Fatalf("status: got %q body=%s", resp["status"], w.Body.String())
	}
	var workspaceID, boundProjectID, content string
	if err := testPool.QueryRow(context.Background(), `
SELECT b.workspace_id::text, b.project_id::text, cm.content
FROM feishu_chat_session_binding b
JOIN chat_message cm ON cm.chat_session_id = b.chat_session_id
WHERE b.feishu_chat_id = $1 AND cm.role = 'user'
`, chatID).Scan(&workspaceID, &boundProjectID, &content); err != nil {
		t.Fatal(err)
	}
	if workspaceID != extraWorkspaceID || boundProjectID != projectID {
		t.Fatalf("expected workspace/project %s/%s, got %s/%s", extraWorkspaceID, projectID, workspaceID, boundProjectID)
	}
	if !strings.Contains(content, "当前项目: Lost Pet ("+projectID+")") || !strings.Contains(content, "用户消息: lost pet 现在进度如何") {
		t.Fatalf("message missing project context: %q", content)
	}
}

func TestFeishuWebhookMultiWorkspaceMessageWithoutProjectSendsSelectionCard(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	extraWorkspaceID := createFeishuChatExtraWorkspace(t, t.Name())
	createFeishuChatProject(t, extraWorkspaceID, "Lost Pet")
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	chatID := "oc_chat_select_project_" + suffix

	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_chat_select_project_" + suffix,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   "om_chat_select_project_" + suffix,
				"chat_id":      chatID,
				"message_type": "text",
				"content":      `{"text":"现在进度如何"}`,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "project_selection_required" || resp["pending_id"] == "" {
		t.Fatalf("unexpected response: %v", resp)
	}
	var pendingCount, taskCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM feishu_chat_pending_selection WHERE feishu_chat_id = $1 AND status = 'pending'`, chatID).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*)
FROM feishu_chat_session_binding b
JOIN agent_task_queue atq ON atq.chat_session_id = b.chat_session_id
WHERE b.feishu_chat_id = $1
`, chatID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 || taskCount != 0 {
		t.Fatalf("expected one pending and zero tasks, got pending=%d tasks=%d", pendingCount, taskCount)
	}
}

func TestFeishuProjectSelectionCardActionEnqueuesOriginalMessage(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	extraWorkspaceID := createFeishuChatExtraWorkspace(t, t.Name())
	projectID := createFeishuChatProject(t, extraWorkspaceID, "Lost Pet")
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	chatID := "oc_chat_click_project_" + suffix
	messageID := "om_chat_click_project_" + suffix
	var pendingID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO feishu_chat_pending_selection (
	user_id, open_id, feishu_chat_id, feishu_root_id, feishu_message_id,
	original_content, candidate_project_ids, expires_at
) VALUES ($1, 'ou_handler_test', $2, '', $3, '现在进度如何', ARRAY[$4::uuid], now() + interval '30 minutes')
RETURNING id::text
`, testUserID, chatID, messageID, projectID).Scan(&pendingID); err != nil {
		t.Fatalf("create pending selection: %v", err)
	}

	result := testHandler.ProcessFeishuCardAction(context.Background(), map[string]any{
		"operator": map[string]any{"open_id": "ou_handler_test"},
		"action": map[string]any{"value": map[string]any{
			"multica_action": "feishu_chat_select_project",
			"pending_id":     pendingID,
			"project_id":     projectID,
		}},
	})
	toast := nestedMap(result, "toast")
	if got := mapString(toast, "type"); got != "success" {
		t.Fatalf("toast type: got %q result=%v", got, result)
	}
	var status, workspaceID, boundProjectID, content string
	if err := testPool.QueryRow(context.Background(), `
SELECT p.status, b.workspace_id::text, b.project_id::text, cm.content
FROM feishu_chat_pending_selection p
JOIN feishu_chat_session_binding b ON b.feishu_chat_id = p.feishu_chat_id
JOIN chat_message cm ON cm.chat_session_id = b.chat_session_id
WHERE p.id = $1
`, pendingID).Scan(&status, &workspaceID, &boundProjectID, &content); err != nil {
		t.Fatal(err)
	}
	if status != "consumed" || workspaceID != extraWorkspaceID || boundProjectID != projectID {
		t.Fatalf("unexpected status/workspace/project: %s %s %s", status, workspaceID, boundProjectID)
	}
	if !strings.Contains(content, "当前项目: Lost Pet ("+projectID+")") || !strings.Contains(content, "用户消息: 现在进度如何") {
		t.Fatalf("message missing selected project context: %q", content)
	}
}

func TestFeishuWebhookMultiWorkspaceMessageReusesSelectedProject(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	extraWorkspaceID := createFeishuChatExtraWorkspace(t, t.Name())
	projectID := createFeishuChatProject(t, extraWorkspaceID, "Lost Pet")
	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	chatID := "oc_chat_reuse_project_" + suffix

	first := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_chat_reuse_project_1_" + suffix,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   "om_chat_reuse_project_1_" + suffix,
				"chat_id":      chatID,
				"message_type": "text",
				"content":      `{"text":"lost pet 现在进度如何"}`,
			},
		},
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first status: got %d body=%s", first.Code, first.Body.String())
	}
	second := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_chat_reuse_project_2_" + suffix,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   "om_chat_reuse_project_2_" + suffix,
				"chat_id":      chatID,
				"message_type": "text",
				"content":      `{"text":"继续"}`,
			},
		},
	})
	if second.Code != http.StatusOK {
		t.Fatalf("second status: got %d body=%s", second.Code, second.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "chat_task_enqueued" {
		t.Fatalf("second response: %v", resp)
	}
	var sessions, messages int
	var boundProjectID string
	if err := testPool.QueryRow(context.Background(), `
SELECT count(DISTINCT b.chat_session_id), count(cm.id), max(b.project_id::text)
FROM feishu_chat_session_binding b
JOIN chat_message cm ON cm.chat_session_id = b.chat_session_id
WHERE b.feishu_chat_id = $1
`, chatID).Scan(&sessions, &messages, &boundProjectID); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || messages != 2 || boundProjectID != projectID {
		t.Fatalf("expected one session, two messages, project %s; got sessions=%d messages=%d project=%s", projectID, sessions, messages, boundProjectID)
	}
}

func TestFeishuWebhookBoundMessageDoesNotCreateChatTask(t *testing.T) {
	setupFeishuWebhookHTTPMock(t)
	setupFeishuWebhookTest(t)
	issueID, inboxID := createFeishuWebhookIssue(t, "todo")
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO feishu_message_binding (
	workspace_id, issue_id, inbox_item_id, recipient_id,
	receive_id_type, receive_id, message_id, root_id, chat_id
) VALUES ($1, $2, $3, $4, 'open_id', 'ou_handler_test', 'om_bound_root', 'om_bound_root', 'oc_bound_chat')
`, testWorkspaceID, issueID, inboxID, testUserID); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	w := postFeishuWebhook(t, map[string]any{
		"header": map[string]any{
			"event_id":   "evt_bound_no_chat_" + issueID,
			"event_type": "im.message.receive_v1",
		},
		"event": map[string]any{
			"sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_handler_test"}, "sender_type": "user"},
			"message": map[string]any{
				"message_id":   "om_bound_reply",
				"root_id":      "om_bound_root",
				"chat_id":      "oc_bound_chat",
				"message_type": "text",
				"content":      `{"text":"这是 issue 评论"}`,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", w.Code, w.Body.String())
	}
	var chatBindings int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM feishu_chat_session_binding WHERE feishu_chat_id = 'oc_bound_chat'`).Scan(&chatBindings); err != nil {
		t.Fatal(err)
	}
	if chatBindings != 0 {
		t.Fatalf("expected no feishu chat binding, got %d", chatBindings)
	}
}

func TestFeishuEventLongConnectionEnabled(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	if !FeishuEventLongConnectionEnabled() {
		t.Fatal("long connection should default to enabled when Feishu app credentials exist")
	}
	t.Setenv("FEISHU_EVENT_MODE", "webhook")
	if FeishuEventLongConnectionEnabled() {
		t.Fatal("webhook mode should disable long connection")
	}
	t.Setenv("FEISHU_EVENT_MODE", "")
	t.Setenv("FEISHU_WS_ENABLED", "false")
	if FeishuEventLongConnectionEnabled() {
		t.Fatal("FEISHU_WS_ENABLED=false should disable long connection")
	}
	t.Setenv("FEISHU_WS_ENABLED", "true")
	if !FeishuEventLongConnectionEnabled() {
		t.Fatal("FEISHU_WS_ENABLED=true should enable long connection")
	}
}
