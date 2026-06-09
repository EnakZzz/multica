package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFeishuChatListenerSendsAssistantReply(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	var sent atomic.Int32
	var messageRequest map[string]string
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
			sent.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&messageRequest); err != nil {
				t.Fatalf("decode message request: %v", err)
			}
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_BASE_URL", srv.URL)

	var agentID, chatSessionID string
	if err := testPool.QueryRow(ctx, `
SELECT a.id::text
FROM agent a
WHERE a.workspace_id = $1
  AND a.archived_at IS NULL
LIMIT 1
`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id)
VALUES ($1, $2, $3, 'feishu listener test', (SELECT runtime_id FROM agent WHERE id = $2))
RETURNING id::text
`, testWorkspaceID, agentID, testUserID).Scan(&chatSessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, chatSessionID) })
	if _, err := testPool.Exec(ctx, `
INSERT INTO feishu_chat_session_binding (
	workspace_id, user_id, agent_id, chat_session_id,
	feishu_chat_id, feishu_root_id, last_message_id
) VALUES ($1, $2, $3, $4, 'oc_listener', 'om_listener_root', 'om_user')
`, testWorkspaceID, testUserID, agentID, chatSessionID); err != nil {
		t.Fatalf("create feishu chat binding: %v", err)
	}

	bus := events.New()
	registerFeishuChatListeners(bus, &service.FeishuChatService{
		Queries: queries,
		Feishu:  service.NewFeishuIssueServiceFromEnv(queries, testPool),
	})
	bus.Publish(events.Event{
		Type:          protocol.EventChatDone,
		WorkspaceID:   testWorkspaceID,
		ChatSessionID: chatSessionID,
		Payload: protocol.ChatDonePayload{
			ChatSessionID: chatSessionID,
			MessageID:     "assistant-message",
			Content:       "处理完成",
		},
	})
	if sent.Load() != 1 {
		t.Fatalf("expected one Feishu message send, got %d", sent.Load())
	}
	if messageRequest["msg_type"] != "post" {
		t.Fatalf("msg_type = %q, want post", messageRequest["msg_type"])
	}
	if !strings.Contains(messageRequest["content"], `"zh_cn"`) || !strings.Contains(messageRequest["content"], `"tag":"md"`) || !strings.Contains(messageRequest["content"], "处理完成") {
		t.Fatalf("expected post md content, got %q", messageRequest["content"])
	}
}

func TestFeishuChatListenerIgnoresUnboundSession(t *testing.T) {
	bus := events.New()
	registerFeishuChatListeners(bus, &service.FeishuChatService{})
	bus.Publish(events.Event{
		Type:          protocol.EventChatDone,
		WorkspaceID:   testWorkspaceID,
		ChatSessionID: strings.Repeat("0", 36),
		Payload:       protocol.ChatDonePayload{ChatSessionID: strings.Repeat("0", 36), Content: "ignored"},
	})
}
