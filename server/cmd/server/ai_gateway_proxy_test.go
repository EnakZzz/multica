package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
)

func TestAIGatewayRoutesProxyToConfiguredUpstream(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Host == "" {
			t.Fatal("expected upstream host to be set")
		}
		w.Header().Set("X-Upstream", "ai-gateway")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("AI_GATEWAY_UPSTREAM_URL", upstream.URL)

	router := NewRouter(nil, realtime.NewHub(), events.New(), analytics.NoopClient{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/models" {
		t.Fatalf("expected upstream path /v1/models, got %q", gotPath)
	}
	if rec.Header().Get("X-Upstream") != "ai-gateway" {
		t.Fatalf("expected proxied upstream header")
	}
}
