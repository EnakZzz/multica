package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestAIGatewayRoutesProxyEmbeddings(t *testing.T) {
	var gotPath string
	ctx := t.Context()
	if err := ensureAIGatewayEmbeddingsUpstream(ctx, testPool); err != nil {
		t.Fatalf("prepare AI gateway embeddings schema: %v", err)
	}
	token := "mvk_test_proxy_embeddings"
	_, err := testPool.Exec(ctx, `
		INSERT INTO ai_gateway_virtual_key (workspace_id, created_by, name, token_hash, token_prefix)
		VALUES ($1, $2, 'internal@multica.local', $3, 'mvk_test')
	`, testWorkspaceID, testUserID, auth.HashToken(token))
	if err != nil {
		t.Fatalf("insert virtual key: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Fatalf("Authorization = %q, want upstream key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"embedding":[0.1,0.2]}]}`))
	}))
	t.Cleanup(upstream.Close)
	var routeID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO ai_gateway_route (workspace_id, alias)
		VALUES ($1, 'text-embedding-3-small')
		RETURNING id
	`, testWorkspaceID).Scan(&routeID)
	if err != nil {
		t.Fatalf("insert route: %v", err)
	}
	_, err = testPool.Exec(ctx, `
		INSERT INTO ai_gateway_route_target (route_id, provider, base_url, auth_mode, api_key_env, api_key, model, upstream_api)
		VALUES ($1, 'openai', $2, 'api_key', '', 'upstream-key', 'text-embedding-3-small', 'embeddings')
	`, routeID, upstream.URL)
	if err != nil {
		t.Fatalf("insert target: %v", err)
	}

	router := NewRouter(testPool, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"text-embedding-3-small","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/embeddings" {
		t.Fatalf("expected upstream path /embeddings, got %q", gotPath)
	}
}
