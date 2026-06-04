package aigateway

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveDefaultModelPricingCanonicalizesModelIDs(t *testing.T) {
	cases := []struct {
		name       string
		model      string
		wantInput  int64
		wantOutput int64
	}{
		{
			name:       "provider prefix",
			model:      "openai/gpt-5-codex",
			wantInput:  1_250_000,
			wantOutput: 10_000_000,
		},
		{
			name:       "pro sku",
			model:      "gpt-5.5-pro",
			wantInput:  30_000_000,
			wantOutput: 180_000_000,
		},
		{
			name:       "newer codex sku",
			model:      "openai/gpt-5.2-codex",
			wantInput:  1_750_000,
			wantOutput: 14_000_000,
		},
		{
			name:       "gpt 5.2 pro",
			model:      "gpt-5.2-pro",
			wantInput:  21_000_000,
			wantOutput: 168_000_000,
		},
		{
			name:       "nano sku",
			model:      "gpt-5.4-nano",
			wantInput:  200_000,
			wantOutput: 1_250_000,
		},
		{
			name:       "gpt 4.1 mini",
			model:      "gpt-4.1-mini",
			wantInput:  400_000,
			wantOutput: 1_600_000,
		},
		{
			name:       "claude dotted suffix",
			model:      "anthropic/claude-sonnet-4.6-20260101",
			wantInput:  3_000_000,
			wantOutput: 15_000_000,
		},
		{
			name:       "date suffix",
			model:      "gpt-5-2025-08-07",
			wantInput:  1_250_000,
			wantOutput: 10_000_000,
		},
		{
			name:       "latest suffix",
			model:      "gpt-4o-mini-latest",
			wantInput:  150_000,
			wantOutput: 600_000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveDefaultModelPricing(tc.model)
			if !ok {
				t.Fatalf("expected pricing for %q", tc.model)
			}
			if got.InputPricePerMillionMicros != tc.wantInput || got.OutputPricePerMillionMicros != tc.wantOutput {
				t.Fatalf("pricing mismatch: got input=%d output=%d", got.InputPricePerMillionMicros, got.OutputPricePerMillionMicros)
			}
		})
	}
}

func TestEffectiveUsagePricingAllowsTargetOverrides(t *testing.T) {
	got := effectiveUsagePricing(Target{
		Model:                       "gpt-5-codex",
		InputPricePerMillionMicros:  42,
		OutputPricePerMillionMicros: 84,
	}, "")
	if got.InputPricePerMillionMicros != 42 || got.OutputPricePerMillionMicros != 84 {
		t.Fatalf("explicit target prices should win, got input=%d output=%d", got.InputPricePerMillionMicros, got.OutputPricePerMillionMicros)
	}

	got = effectiveUsagePricing(Target{
		Model:                      "gpt-5-codex",
		InputPricePerMillionMicros: 42,
	}, "")
	if got.InputPricePerMillionMicros != 42 || got.OutputPricePerMillionMicros != 10_000_000 {
		t.Fatalf("partial override should keep default output price, got input=%d output=%d", got.InputPricePerMillionMicros, got.OutputPricePerMillionMicros)
	}
}

func TestEffectiveUsagePricingDisablesEstimationForCustomHeadersCookie(t *testing.T) {
	got := effectiveUsagePricing(Target{
		AuthMode: AuthModeCustomHeadersCookie,
		Model:    "gpt-5.5",
	}, "")
	if got != (defaultUsagePricing{}) {
		t.Fatalf("custom header/cookie mode should not auto-price, got %+v", got)
	}
}

func TestResolveUpstreamAuthSupportsCustomHeadersCookie(t *testing.T) {
	t.Setenv("AI_GATEWAY_COOKIE", "__Secure-next-auth.session-token=abc")
	t.Setenv("AI_GATEWAY_HEADER_TOKEN", "header-token")
	auth, err := ResolveUpstreamAuth(Target{
		AuthMode:  AuthModeCustomHeadersCookie,
		CookieEnv: "AI_GATEWAY_COOKIE",
		CustomHeaderEnvs: []CustomHeaderEnv{{
			HeaderName: "X-Test-Token",
			EnvName:    "AI_GATEWAY_HEADER_TOKEN",
		}},
	})
	if err != nil {
		t.Fatalf("ResolveUpstreamAuth: %v", err)
	}
	if got := auth.Headers.Get("Cookie"); got != "__Secure-next-auth.session-token=abc" {
		t.Fatalf("Cookie header = %q", got)
	}
	if got := auth.Headers.Get("X-Test-Token"); got != "header-token" {
		t.Fatalf("X-Test-Token header = %q", got)
	}
	if got := auth.Headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should not be set, got %q", got)
	}
}

func TestNormalizeRoutesRejectsMixedAuthModeFields(t *testing.T) {
	_, err := NormalizeRoutes([]Route{{
		Alias: "team-agent",
		Targets: []Target{{
			Provider:  "test",
			BaseURL:   "https://example.com/v1",
			AuthMode:  AuthModeCustomHeadersCookie,
			APIKeyEnv: "OPENAI_API_KEY",
			Model:     "gpt-5.5",
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "cannot set api_key_env") {
		t.Fatalf("expected mixed auth_mode validation error, got %v", err)
	}
}

func TestParseUsageIncludesCacheAndReasoningDetails(t *testing.T) {
	usage := ParseUsage([]byte(`{
		"usage": {
			"input_tokens": 38451,
			"output_tokens": 1275,
			"total_tokens": 39726,
			"input_tokens_details": {"cached_tokens": 36608},
			"output_tokens_details": {"reasoning_tokens": 512}
		}
	}`))
	if usage.PromptTokens != 38451 || usage.CompletionTokens != 1275 || usage.TotalTokens != 39726 {
		t.Fatalf("usage totals mismatch: %+v", usage)
	}
	if usage.CachedInputTokens != 36608 {
		t.Fatalf("cached input tokens: got %d", usage.CachedInputTokens)
	}
	if usage.ReasoningTokens != 512 {
		t.Fatalf("reasoning tokens: got %d", usage.ReasoningTokens)
	}
}

func TestUsageTokensResponsesUsageUsesNestedDetails(t *testing.T) {
	usage := UsageTokens{
		PromptTokens:      38451,
		CompletionTokens:  1275,
		TotalTokens:       39726,
		CachedInputTokens: 36608,
		ReasoningTokens:   512,
	}.responsesUsage()
	inputDetails, ok := usage["input_tokens_details"].(map[string]int64)
	if !ok || inputDetails["cached_tokens"] != 36608 {
		t.Fatalf("input_tokens_details mismatch: %#v", usage["input_tokens_details"])
	}
	outputDetails, ok := usage["output_tokens_details"].(map[string]int64)
	if !ok || outputDetails["reasoning_tokens"] != 512 {
		t.Fatalf("output_tokens_details mismatch: %#v", usage["output_tokens_details"])
	}
	if _, ok := usage["cached_input_tokens"]; ok {
		t.Fatalf("responses usage should not expose non-standard cached_input_tokens: %#v", usage)
	}
}

func TestSSEUsageParserMergesMultipleUsageChunks(t *testing.T) {
	var parser sseUsageParser
	parser.Feed([]byte("event: response.in_progress\ndata: {\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":5}}}\n\n"))
	parser.Feed([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens_details\":{\"cached_tokens\":80},\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n"))
	usage := parser.Usage()
	if usage.PromptTokens != 100 || usage.CompletionTokens != 5 || usage.CachedInputTokens != 80 || usage.ReasoningTokens != 2 {
		t.Fatalf("merged usage mismatch: %+v", usage)
	}
	if usage.TotalTokens != 105 {
		t.Fatalf("merged total tokens = %d, want 105", usage.TotalTokens)
	}
}

func TestCopyChatCompletionStreamAsResponsesMergesUsageChunks(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"ok"},"finish_reason":null}],"usage":{"prompt_tokens":100,"completion_tokens":5}}`,
		`data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":2}}}`,
		`data: [DONE]`,
		"",
	}, "\n")
	rec := httptest.NewRecorder()
	_, usage, responseID, err := copyChatCompletionStreamAsResponses(rec, io.NopCloser(strings.NewReader(body)), ForwardRequest{
		RequestID:   "req_test",
		TargetModel: "gpt-5.5",
	})
	if err != nil {
		t.Fatalf("copyChatCompletionStreamAsResponses error: %v", err)
	}
	if responseID != "resp_req_test" {
		t.Fatalf("responseID = %q", responseID)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 5 || usage.CachedInputTokens != 80 || usage.ReasoningTokens != 2 {
		t.Fatalf("merged chat stream usage mismatch: %+v", usage)
	}
}

func TestEstimateUsageCostBreakdownUsesCacheAndLongContext(t *testing.T) {
	cached := EstimateUsageCostBreakdown("gpt-5.5", 100_000, 2_000, 80_000)
	if cached.LongContext {
		t.Fatal("short request should not use long-context pricing")
	}
	if cached.BillableInputTokens != 20_000 || cached.CachedInputTokens != 80_000 {
		t.Fatalf("token split mismatch: %+v", cached)
	}
	if cached.InputCostMicros != 100_000 || cached.CachedInputCostMicros != 40_000 || cached.OutputCostMicros != 60_000 {
		t.Fatalf("cost split mismatch: %+v", cached)
	}

	long := EstimateUsageCostBreakdown("gpt-5.5", 300_000, 2_000, 80_000)
	if !long.LongContext {
		t.Fatal("large gpt-5.5 request should use long-context pricing")
	}
	if long.BillableInputTokens != 300_000 || long.CachedInputTokens != 0 {
		t.Fatalf("long-context input split mismatch: %+v", long)
	}
	if long.InputCostMicros != 3_000_000 || long.OutputCostMicros != 90_000 {
		t.Fatalf("long-context cost mismatch: %+v", long)
	}
}

func TestEstimateUsageCostBreakdownCanForceLongContextForSession(t *testing.T) {
	pricing, ok := resolveDefaultModelPricing("gpt-5.5")
	if !ok {
		t.Fatal("expected gpt-5.5 pricing")
	}
	got := estimateUsageCostBreakdownWithPricingAndLong(pricing, 100_000, 2_000, 80_000, true)
	if !got.LongContext {
		t.Fatal("forced session long-context pricing should mark row long_context")
	}
	if got.BillableInputTokens != 100_000 || got.CachedInputTokens != 0 {
		t.Fatalf("forced long-context token split mismatch: %+v", got)
	}
	if got.InputCostMicros != 1_000_000 || got.OutputCostMicros != 90_000 {
		t.Fatalf("forced long-context cost mismatch: %+v", got)
	}
}

func TestShouldRetryAIGatewayFailureTreatsQuotaBodiesAsRetryable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{
			name:   "429 always retries",
			status: 429,
			body:   `{"error":{"message":"too many requests"}}`,
			want:   true,
		},
		{
			name:   "403 quota body retries",
			status: 403,
			body:   `{"error":{"message":"insufficient balance"}}`,
			want:   true,
		},
		{
			name:   "400 quota marker retries",
			status: 400,
			body:   `{"error":{"type":"quota_exceeded","message":"quota exhausted"}}`,
			want:   true,
		},
		{
			name:   "401 auth error does not retry",
			status: 401,
			body:   `{"error":{"message":"invalid api key"}}`,
			want:   false,
		},
		{
			name:   "403 non quota body does not retry",
			status: 403,
			body:   `{"error":{"message":"forbidden"}}`,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetryAIGatewayFailure(tc.status, tc.body); got != tc.want {
				t.Fatalf("shouldRetryAIGatewayFailure(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

func TestSelectTargetsUsesOnlyOpenAIForImageGenerations(t *testing.T) {
	route := Route{
		Alias:    "gpt-5.4",
		Strategy: "fallback",
		Targets: []Target{
			{Provider: "he-tokenapi", Model: "vp/gpt-5.4", Enabled: true},
			{Provider: "openai", Model: "gpt-5.4", Enabled: true},
		},
	}

	targets := selectTargets(route, "gpt-5.4", "/images/generations")
	if len(targets) != 1 {
		t.Fatalf("expected one image-generation target, got %d", len(targets))
	}
	if targets[0].Provider != "openai" {
		t.Fatalf("image generation should route to openai target, got %q", targets[0].Provider)
	}
	if targets[0].Model != "gpt-5.4" {
		t.Fatalf("unexpected image-generation model: %q", targets[0].Model)
	}
}

func TestResolveTargetModelForEndpointMapsGPTAliasesToGPTImage1(t *testing.T) {
	cases := []struct {
		name           string
		requestedModel string
		target         Target
		want           string
	}{
		{
			name:           "plain gpt 5.4",
			requestedModel: "gpt-5.4",
			target:         Target{Provider: "openai", Model: "gpt-5.4"},
			want:           "gpt-image-1",
		},
		{
			name:           "provider prefixed gpt 5.5",
			requestedModel: "openai/gpt-5.5",
			target:         Target{Provider: "openai", Model: "gpt-5.5"},
			want:           "gpt-image-1",
		},
		{
			name:           "mini sku",
			requestedModel: "gpt-5.4-mini",
			target:         Target{Provider: "openai", Model: "gpt-5.4-mini"},
			want:           "gpt-image-1",
		},
		{
			name:           "explicit image target remains image model",
			requestedModel: "gpt-image-1",
			target:         Target{Provider: "openai", Model: "gpt-image-1"},
			want:           "gpt-image-1",
		},
		{
			name:           "unmapped model falls back to target model",
			requestedModel: "gpt-4.1-mini",
			target:         Target{Provider: "openai", Model: "gpt-4.1-mini"},
			want:           "gpt-4.1-mini",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTargetModelForEndpoint("/images/generations", tc.requestedModel, tc.target)
			if got != tc.want {
				t.Fatalf("resolveTargetModelForEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTargetModelForEndpointDoesNotRewriteResponses(t *testing.T) {
	got := resolveTargetModelForEndpoint("/responses", "gpt-5.4", Target{
		Provider: "he-tokenapi",
		Model:    "vp/gpt-5.4",
	})
	if got != "vp/gpt-5.4" {
		t.Fatalf("responses endpoint should keep target model, got %q", got)
	}
}
