package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLoadAIGatewayRoutesFromEnvParsesFallbackTargets(t *testing.T) {
	t.Setenv("AI_GATEWAY_ROUTES", `[
		{
			"alias": "team-agent",
			"targets": [
				{"provider": "openai", "api_key_env": "OPENAI_API_KEY", "model": "gpt-primary"},
				{"provider": "openrouter", "base_url": "https://openrouter.ai/api/v1/", "api_key_env": "OPENROUTER_API_KEY", "model": "anthropic/claude-sonnet"}
			]
		}
	]`)

	routes, err := loadAIGatewayRoutesFromEnv()
	if err != nil {
		t.Fatalf("load routes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	route := routes[0]
	if route.Alias != "team-agent" {
		t.Fatalf("alias: want team-agent, got %q", route.Alias)
	}
	if route.Strategy != "fallback" {
		t.Fatalf("strategy: want fallback, got %q", route.Strategy)
	}
	if route.Targets[0].BaseURL != aiGatewayDefaultURL {
		t.Fatalf("default base url: want %q, got %q", aiGatewayDefaultURL, route.Targets[0].BaseURL)
	}
	if route.Targets[1].BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("trimmed base url mismatch: %q", route.Targets[1].BaseURL)
	}
}

func TestFindAIGatewayRouteSupportsWildcard(t *testing.T) {
	routes := []aiGatewayRoute{
		{Alias: "*", Targets: []aiGatewayTarget{{APIKeyEnv: "OPENAI_API_KEY"}}},
	}
	route, ok := findAIGatewayRoute(routes, "gpt-5")
	if !ok {
		t.Fatal("expected wildcard route")
	}
	if route.Alias != "*" {
		t.Fatalf("alias: want *, got %q", route.Alias)
	}
}

func TestAIGatewayModelsIncludesWildcardTargetModels(t *testing.T) {
	t.Setenv("AI_GATEWAY_ROUTES", `[
		{"alias":"team-agent","targets":[{"provider":"openai","api_key_env":"OPENAI_API_KEY","model":"gpt-5-codex"}]},
		{"alias":"*","targets":[
			{"provider":"openai","api_key_env":"OPENAI_API_KEY","model":"gpt-5-codex"},
			{"provider":"claude-local","api_key_env":"ANTHROPIC_AUTH_TOKEN","model":"claude-sonnet-4-6","upstream_api":"chat_completions"},
			{"provider":"openai","api_key_env":"OPENAI_API_KEY"}
		]}
	]`)

	rawToken, keyID := createAIGatewayTestKey(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()

	testHandler.AIGatewayModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_usage WHERE virtual_key_id = $1`, keyID)
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	var got struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	ids := map[string]int{}
	for _, item := range got.Data {
		ids[item.ID]++
	}
	if ids["team-agent"] != 1 {
		t.Fatalf("expected team-agent once, got ids=%v", ids)
	}
	if ids["gpt-5-codex"] != 1 {
		t.Fatalf("expected gpt-5-codex once, got ids=%v", ids)
	}
	if ids["claude-sonnet-4-6"] != 1 {
		t.Fatalf("expected claude-sonnet-4-6 once, got ids=%v", ids)
	}
	if ids["gpt-5.5"] != 1 {
		t.Fatalf("expected Codex static model gpt-5.5 once, got ids=%v", ids)
	}
	if ids["gpt-5.2"] != 1 {
		t.Fatalf("expected Codex static model gpt-5.2 once, got ids=%v", ids)
	}
	if ids[""] != 0 {
		t.Fatalf("blank pass-through target should not be listed: ids=%v", ids)
	}
}

func TestPatchedAIGatewayBodyRewritesOnlyModel(t *testing.T) {
	body, err := patchedAIGatewayBody(map[string]any{
		"model": "team-agent",
		"input": "hello",
	}, "gpt-5", "/responses", aiGatewayTarget{})
	if err != nil {
		t.Fatalf("patch body: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["model"] != "gpt-5" {
		t.Fatalf("model: want gpt-5, got %v", got["model"])
	}
	if got["input"] != "hello" {
		t.Fatalf("input was not preserved: %v", got["input"])
	}
}

func TestPatchedAIGatewayBodyInjectsReasoningEffort(t *testing.T) {
	body, err := patchedAIGatewayBody(map[string]any{
		"model": "team-agent",
		"input": "hello",
		"reasoning": map[string]any{
			"summary": "auto",
			"effort":  "medium",
		},
	}, "gpt-5", "/responses", aiGatewayTarget{ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("patch body: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning was not injected: %#v", got["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning effort: want high, got %v", reasoning["effort"])
	}
	if reasoning["summary"] != "auto" {
		t.Fatalf("reasoning summary was not preserved: %v", reasoning["summary"])
	}
}

func TestParseAIGatewayUsageSupportsResponsesAndChatCompletions(t *testing.T) {
	responses := parseAIGatewayUsage([]byte(`{"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`))
	if responses.PromptTokens != 12 || responses.CompletionTokens != 5 || responses.TotalTokens != 17 {
		t.Fatalf("responses usage mismatch: %+v", responses)
	}

	chat := parseAIGatewayUsage([]byte(`{"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`))
	if chat.PromptTokens != 9 || chat.CompletionTokens != 3 || chat.TotalTokens != 12 {
		t.Fatalf("chat usage mismatch: %+v", chat)
	}

	streamEvent := parseAIGatewayUsage([]byte(`{"response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`))
	if streamEvent.PromptTokens != 11 || streamEvent.CompletionTokens != 4 || streamEvent.TotalTokens != 15 {
		t.Fatalf("stream event usage mismatch: %+v", streamEvent)
	}
}

func TestListAIGatewayUsagePaginatesRecentRowsOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, keyID := createAIGatewayTestKey(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_usage WHERE virtual_key_id = $1`, keyID)
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO ai_gateway_usage (
				virtual_key_id, workspace_id, request_id, endpoint, model_alias,
				upstream_provider, upstream_model, status_code, total_tokens, latency_ms, created_at
			)
			VALUES ($1, $2, $3, '/responses', 'team-agent', 'openai', 'gpt-5-codex', 200, $4, 10, $5)
		`, keyID, testWorkspaceID, fmt.Sprintf("usage-recent-%d", i), int64(10+i), now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("insert recent usage %d: %v", i, err)
		}
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_gateway_usage (
			virtual_key_id, workspace_id, request_id, endpoint, model_alias,
			upstream_provider, upstream_model, status_code, total_tokens, latency_ms, created_at
		)
		VALUES ($1, $2, 'usage-old', '/responses', 'team-agent', 'openai', 'gpt-5-codex', 200, 99, 10, $3)
	`, keyID, testWorkspaceID, now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("insert old usage: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/ai-gateway/usage?limit=2&offset=1", nil)
	req = req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, db.Member{}))
	testHandler.ListAIGatewayUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got []aiGatewayUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 usage rows, got %d: %+v", len(got), got)
	}
	if got[0].RequestID != "usage-recent-1" || got[1].RequestID != "usage-recent-2" {
		t.Fatalf("unexpected page rows: %+v", got)
	}

	var oldExists bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM ai_gateway_usage
			WHERE virtual_key_id = $1 AND request_id = 'usage-old'
		)
	`, keyID).Scan(&oldExists); err != nil {
		t.Fatalf("query old usage: %v", err)
	}
	if !oldExists {
		t.Fatal("old usage row should be retained for aggregate reporting")
	}
}

func TestResponsesPayloadToChatCompletions(t *testing.T) {
	body, err := responsesPayloadToChatCompletions(map[string]any{
		"model":             "team-agent",
		"input":             "hello",
		"max_output_tokens": float64(3),
		"stream":            true,
	}, "claude-sonnet-4-6", aiGatewayTarget{ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode converted body: %v", err)
	}
	if got["model"] != "claude-sonnet-4-6" {
		t.Fatalf("model: want claude-sonnet-4-6, got %v", got["model"])
	}
	if got["max_tokens"] != float64(3) {
		t.Fatalf("max_tokens not mapped: %v", got["max_tokens"])
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort: %v", got["reasoning_effort"])
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages not converted: %#v", got["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hello" {
		t.Fatalf("message mismatch: %#v", first)
	}
}

func TestChatCompletionToResponses(t *testing.T) {
	converted, err := chatCompletionToResponses([]byte(`{
		"id":"chatcmpl_test",
		"model":"claude-sonnet-4-6",
		"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
	}`), aiGatewayForwardRequest{
		RequestID:   "req_123",
		TargetModel: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("convert response: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(converted, &got); err != nil {
		t.Fatalf("decode converted response: %v", err)
	}
	if got["object"] != "response" || got["model"] != "claude-sonnet-4-6" {
		t.Fatalf("response envelope mismatch: %#v", got)
	}
	output := got["output"].([]any)[0].(map[string]any)
	content := output["content"].([]any)[0].(map[string]any)
	if content["type"] != "output_text" || content["text"] != "OK" {
		t.Fatalf("output text mismatch: %#v", content)
	}
}

func TestAIGatewayWildcardRouteSelectsMatchingTargetModel(t *testing.T) {
	t.Setenv("OPENAI_TEST_KEY", "sk-openai")
	t.Setenv("CLAUDE_TEST_KEY", "sk-claude")
	var openAIHits int32
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&openAIHits, 1)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "wrong upstream"},
		})
	}))
	defer openAI.Close()

	var claudeHits int32
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&claudeHits, 1)
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("upstream path: want /chat/completions, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-claude" {
			t.Fatalf("upstream auth: %q", got)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if req["model"] != "claude-sonnet-4-6" {
			t.Fatalf("model was not routed from Codex selection: %v", req["model"])
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "chatcmpl_test",
			"choices": []any{
				map[string]any{
					"message": map[string]any{"role": "assistant", "content": "OK"},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     2,
				"completion_tokens": 1,
				"total_tokens":      3,
			},
		})
	}))
	defer claude.Close()

	t.Setenv("AI_GATEWAY_ROUTES", fmt.Sprintf(`[
		{"alias":"*","targets":[
			{"provider":"openai","base_url":%q,"api_key_env":"OPENAI_TEST_KEY","model":"gpt-5-codex","upstream_api":"responses"},
			{"provider":"claude-local","base_url":%q,"api_key_env":"CLAUDE_TEST_KEY","model":"claude-sonnet-4-6","upstream_api":"chat_completions"}
		]}
	]`, openAI.URL, claude.URL))

	rawToken, keyID := createAIGatewayTestKey(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_usage WHERE virtual_key_id = $1`, keyID)
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"claude-sonnet-4-6","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testHandler.AIGatewayResponses(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&openAIHits) != 0 {
		t.Fatalf("OpenAI target should have been skipped, got %d hits", openAIHits)
	}
	if atomic.LoadInt32(&claudeHits) != 1 {
		t.Fatalf("Claude target should have been called once, got %d hits", claudeHits)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["model"] != "claude-sonnet-4-6" {
		t.Fatalf("response model mismatch: %#v", body["model"])
	}
}

func TestAIGatewayWildcardRouteUsesOpenAITemplateForCodexStaticModel(t *testing.T) {
	t.Setenv("OPENAI_TEST_KEY", "sk-openai")
	var openAIHits int32
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&openAIHits, 1)
		if r.URL.Path != "/responses" {
			t.Fatalf("upstream path: want /responses, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-openai" {
			t.Fatalf("upstream auth: %q", got)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if req["model"] != "gpt-5.5" {
			t.Fatalf("model was not routed from Codex selection: %v", req["model"])
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":     "resp_test",
			"object": "response",
			"model":  "gpt-5.5",
			"usage": map[string]any{
				"input_tokens":  2,
				"output_tokens": 1,
				"total_tokens":  3,
			},
		})
	}))
	defer openAI.Close()

	t.Setenv("AI_GATEWAY_ROUTES", fmt.Sprintf(`[
		{"alias":"*","targets":[
			{"provider":"openai","base_url":%q,"api_key_env":"OPENAI_TEST_KEY","model":"gpt-5-codex","upstream_api":"responses"}
		]}
	]`, openAI.URL))

	rawToken, keyID := createAIGatewayTestKey(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_usage WHERE virtual_key_id = $1`, keyID)
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	testHandler.AIGatewayResponses(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&openAIHits) != 1 {
		t.Fatalf("OpenAI target should have been called once, got %d hits", openAIHits)
	}
}

func TestAIGatewayProxyResponsesEndToEnd(t *testing.T) {
	t.Setenv("UPSTREAM_TEST_KEY", "sk-test")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("upstream path: want /responses, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("upstream auth: %q", got)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if req["model"] != "real-model" {
			t.Fatalf("model was not rewritten: %v", req["model"])
		}
		reasoning, ok := req["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "high" {
			t.Fatalf("reasoning effort was not injected: %#v", req["reasoning"])
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":     "resp_test",
			"object": "response",
			"usage": map[string]any{
				"input_tokens":  7,
				"output_tokens": 3,
				"total_tokens":  10,
			},
		})
	}))
	defer upstream.Close()

	t.Setenv("AI_GATEWAY_ROUTES", fmt.Sprintf(`[
		{"alias":"team-agent","targets":[{"provider":"test","base_url":%q,"api_key_env":"UPSTREAM_TEST_KEY","model":"real-model","reasoning_effort":"high"}]}
	]`, upstream.URL))

	rawToken, err := generateAIGatewayToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	var keyID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO ai_gateway_virtual_key (workspace_id, created_by, name, token_hash, token_prefix)
		VALUES ($1, $2, 'proxy-test', $3, $4)
		RETURNING id
	`, testWorkspaceID, testUserID, auth.HashToken(rawToken), rawToken[:12]).Scan(&keyID); err != nil {
		t.Fatalf("insert ai gateway key: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"team-agent","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Caller", "alice@example.com")
	rec := httptest.NewRecorder()

	testHandler.AIGatewayResponses(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var callerID, reasoningEffort string
	var totalTokens int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(caller_id, ''), COALESCE(reasoning_effort, ''), total_tokens
		FROM ai_gateway_usage
		WHERE virtual_key_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, keyID).Scan(&callerID, &reasoningEffort, &totalTokens); err != nil {
		t.Fatalf("load usage row: %v", err)
	}
	if callerID != "alice@example.com" {
		t.Fatalf("caller_id: want alice@example.com, got %q", callerID)
	}
	if reasoningEffort != "high" {
		t.Fatalf("reasoning_effort: want high, got %q", reasoningEffort)
	}
	if totalTokens != 10 {
		t.Fatalf("total_tokens: want 10, got %d", totalTokens)
	}

	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load member row: %v", err)
	}
	summaryReq := newRequest(http.MethodGet, "/api/ai-gateway/usage/summary?days=30", nil)
	summaryReq = summaryReq.WithContext(middleware.SetMemberContext(summaryReq.Context(), testWorkspaceID, memberRow))
	summaryRec := httptest.NewRecorder()
	testHandler.ListAIGatewayUsageSummary(summaryRec, summaryReq)
	if summaryRec.Code != http.StatusOK {
		t.Fatalf("summary: expected 200, got %d: %s", summaryRec.Code, summaryRec.Body.String())
	}
	var summary []aiGatewayUsageSummaryResponse
	if err := json.NewDecoder(summaryRec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	found := false
	for _, item := range summary {
		if item.CallerID == "alice@example.com" {
			found = true
			if item.TotalTokens < 10 {
				t.Fatalf("summary total_tokens: want at least 10, got %d", item.TotalTokens)
			}
			break
		}
	}
	if !found {
		t.Fatalf("summary did not include caller_id alice@example.com: %+v", summary)
	}
}

func TestAIGatewayProxyStreamingResponsesRecordsUsage(t *testing.T) {
	t.Setenv("UPSTREAM_TEST_KEY", "sk-test")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("upstream path: want /responses, got %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if req["stream"] != true {
			t.Fatalf("stream flag was not forwarded: %#v", req["stream"])
		}
		reasoning, ok := req["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "medium" {
			t.Fatalf("caller reasoning effort was not forwarded: %#v", req["reasoning"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"OK"}`)
		fmt.Fprint(w, "\n\n")
		fmt.Fprint(w, "event: response.completed\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_test","usage":{"input_tokens":13,"output_tokens":5,"total_tokens":18}}}`)
		fmt.Fprint(w, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	t.Setenv("AI_GATEWAY_ROUTES", fmt.Sprintf(`[
		{"alias":"team-agent","targets":[{"provider":"test","base_url":%q,"api_key_env":"UPSTREAM_TEST_KEY","model":"real-model"}]}
	]`, upstream.URL))

	rawToken, keyID := createAIGatewayTestKey(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_usage WHERE virtual_key_id = $1`, keyID)
		testPool.Exec(context.Background(), `DELETE FROM ai_gateway_virtual_key WHERE id = $1`, keyID)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"team-agent","input":"hello","stream":true,"reasoning":{"effort":"medium"}}`))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Caller", "alice@example.com")
	rec := httptest.NewRecorder()

	testHandler.AIGatewayResponses(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("stream response was not forwarded: %s", rec.Body.String())
	}

	var promptTokens, completionTokens, totalTokens int64
	var reasoningEffort string
	if err := testPool.QueryRow(context.Background(), `
		SELECT prompt_tokens, completion_tokens, total_tokens, COALESCE(reasoning_effort, '')
		FROM ai_gateway_usage
		WHERE virtual_key_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, keyID).Scan(&promptTokens, &completionTokens, &totalTokens, &reasoningEffort); err != nil {
		t.Fatalf("load usage row: %v", err)
	}
	if promptTokens != 13 || completionTokens != 5 || totalTokens != 18 {
		t.Fatalf("stream usage mismatch: prompt=%d completion=%d total=%d", promptTokens, completionTokens, totalTokens)
	}
	if reasoningEffort != "medium" {
		t.Fatalf("reasoning_effort: want medium, got %q", reasoningEffort)
	}
}

func createAIGatewayTestKey(t *testing.T) (string, string) {
	t.Helper()
	rawToken, err := generateAIGatewayToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	var keyID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO ai_gateway_virtual_key (workspace_id, created_by, name, token_hash, token_prefix)
		VALUES ($1, $2, 'proxy-test', $3, $4)
		RETURNING id
	`, testWorkspaceID, testUserID, auth.HashToken(rawToken), rawToken[:12]).Scan(&keyID); err != nil {
		t.Fatalf("insert ai gateway key: %v", err)
	}
	return rawToken, keyID
}

func TestAIGatewayCallerIDSanitizesHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("X-Multica-Caller", "  alice@example.com\r\nignored  ")

	got := aiGatewayCallerID(req)
	if got != "alice@example.comignored" {
		t.Fatalf("caller id mismatch: %q", got)
	}
}
