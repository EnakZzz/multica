package aigateway

import (
	"encoding/json"
	"io"
	"net/http"
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

func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return data
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

func TestResolveUpstreamAuthUsesAPIKeyPoolWhenPresent(t *testing.T) {
	auth, err := ResolveUpstreamAuth(Target{
		AuthMode: AuthModeAPIKey,
		Provider: "he-tokenapi",
		APIKeyPool: []APIKeyPoolItem{
			{Label: "team-a", APIKey: "pool-key-a", SharedByEmail: "a@example.com", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("ResolveUpstreamAuth: %v", err)
	}
	if got := auth.Headers.Get("Authorization"); got != "Bearer pool-key-a" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestResolveUpstreamAuthUsesStoredAPIKeyBeforeEnv(t *testing.T) {
	t.Setenv("TEST_OPENAI_KEY", "env-key")
	auth, err := ResolveUpstreamAuth(Target{
		AuthMode:  AuthModeAPIKey,
		Provider:  "openai",
		APIKey:    "stored-key",
		APIKeyEnv: "TEST_OPENAI_KEY",
	})
	if err != nil {
		t.Fatalf("ResolveUpstreamAuth: %v", err)
	}
	if got := auth.Headers.Get("Authorization"); got != "Bearer stored-key" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestNormalizeRoutesSupportsHEAPIKeyPool(t *testing.T) {
	routes, err := NormalizeRoutes([]Route{{
		Alias: "openai/gpt-5.5",
		Targets: []Target{{
			Provider: "he-tokenapi",
			BaseURL:  "https://tokenapi.happyelements.net/v1",
			AuthMode: AuthModeAPIKey,
			Model:    "vp/gpt-5.5",
			APIKeyPool: []APIKeyPoolItem{{
				Label:         "team-a",
				APIKey:        "he-key",
				SharedByEmail: "team-a@example.com",
				Enabled:       true,
			}},
		}},
	}})
	if err != nil {
		t.Fatalf("NormalizeRoutes: %v", err)
	}
	if len(routes) != 1 || len(routes[0].Targets) != 1 || len(routes[0].Targets[0].APIKeyPool) != 1 {
		t.Fatalf("unexpected normalized routes: %+v", routes)
	}
}

func TestNormalizeRoutesRejectsAPIKeyPoolForNonHEProvider(t *testing.T) {
	_, err := NormalizeRoutes([]Route{{
		Alias: "gpt-5.5",
		Targets: []Target{{
			Provider: "openai",
			BaseURL:  "https://api.openai.com/v1",
			AuthMode: AuthModeAPIKey,
			Model:    "gpt-5.5",
			APIKeyPool: []APIKeyPoolItem{{
				Label:         "bad",
				APIKey:        "sk-test",
				SharedByEmail: "owner@example.com",
				Enabled:       true,
			}},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "api_key_pool only supports provider he-tokenapi") {
		t.Fatalf("expected he-only validation error, got %v", err)
	}
}

func TestBuildAPIKeyPoolAttemptsPrefersOwnerKey(t *testing.T) {
	target := Target{
		Provider: "he-tokenapi",
		AuthMode: AuthModeAPIKey,
		APIKeyPool: []APIKeyPoolItem{
			{ID: "key-a", Label: "A", APIKey: "a", SharedByEmail: "a@example.com", Enabled: true},
			{ID: "key-b", Label: "B", APIKey: "b", SharedByEmail: "b@example.com", Enabled: true},
			{ID: "key-c", Label: "C", APIKey: "c", SharedByEmail: "c@example.com", Enabled: true},
		},
	}
	attempts := buildAPIKeyPoolAttempts(target, "b@example.com")
	if len(attempts) != 3 {
		t.Fatalf("attempt count = %d", len(attempts))
	}
	if attempts[0].Item.ID != "key-b" || !attempts[0].OwnerMatch {
		t.Fatalf("owner key should be first: %+v", attempts)
	}
}

func TestInheritWildcardHEAPIKeyPools(t *testing.T) {
	routes := []Route{
		{
			Alias: "*",
			Targets: []Target{
				{
					Provider:  "he-tokenapi",
					BaseURL:   "https://tokenapi.happyelements.net/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_HE_TOKENAPI_KEY",
					Model:     "",
					APIKeyPool: []APIKeyPoolItem{
						{ID: "pool-1", Label: "shared", APIKey: "he-key", SharedByEmail: "owner@example.com", Enabled: true},
					},
				},
			},
		},
		{
			Alias: "gpt-5.5",
			Targets: []Target{
				{
					Provider:  "he-tokenapi",
					BaseURL:   "https://tokenapi.happyelements.net/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_HE_TOKENAPI_KEY",
					Model:     "vp/gpt-5.5",
				},
				{
					Provider:  "openai",
					BaseURL:   "https://api.openai.com/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_OPENAI_KEY",
					Model:     "gpt-5.5",
				},
			},
		},
	}

	route, ok := FindRoute(routes, "gpt-5.5")
	if !ok {
		t.Fatal("expected exact gpt-5.5 route")
	}
	got := inheritWildcardHEAPIKeyPools(routes, route)
	if len(got.Targets) != 2 {
		t.Fatalf("target count = %d", len(got.Targets))
	}
	if len(got.Targets[0].APIKeyPool) != 1 {
		t.Fatalf("expected he target to inherit wildcard pool, got %+v", got.Targets[0].APIKeyPool)
	}
	if got.Targets[0].APIKeyPool[0].ID != "pool-1" {
		t.Fatalf("unexpected inherited pool item: %+v", got.Targets[0].APIKeyPool[0])
	}
	if len(got.Targets[1].APIKeyPool) != 0 {
		t.Fatalf("openai target should not inherit HE pool, got %+v", got.Targets[1].APIKeyPool)
	}
}

func TestMergeWildcardFallbackTargetsAppendsSharedFallbackWithoutDuplicatingHE(t *testing.T) {
	routes := []Route{
		{
			Alias: "*",
			Targets: []Target{
				{
					Provider:  "he-tokenapi",
					BaseURL:   "https://tokenapi.happyelements.net/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_HE_TOKENAPI_KEY",
					APIKeyPool: []APIKeyPoolItem{
						{ID: "pool-1", Label: "shared", APIKey: "he-key", SharedByEmail: "owner@example.com", Enabled: true},
					},
				},
				{
					Provider:    "openai",
					BaseURL:     "https://api.openai.com/v1",
					AuthMode:    AuthModeAPIKey,
					APIKeyEnv:   "TEST_OPENAI_KEY",
					Model:       "gpt-5.5",
					UpstreamAPI: "responses",
				},
			},
		},
		{
			Alias: "gpt-5.5",
			Targets: []Target{
				{
					Provider:  "he-tokenapi",
					BaseURL:   "https://tokenapi.happyelements.net/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_HE_TOKENAPI_KEY",
					Model:     "vp/gpt-5.5",
				},
			},
		},
	}

	route, ok := FindRoute(routes, "gpt-5.5")
	if !ok {
		t.Fatal("expected exact gpt-5.5 route")
	}
	got := inheritWildcardHEAPIKeyPools(routes, route)
	got = mergeWildcardFallbackTargets(routes, got, "gpt-5.5")

	if len(got.Targets) != 2 {
		t.Fatalf("target count = %d, want 2", len(got.Targets))
	}
	if got.Targets[0].Provider != "he-tokenapi" || got.Targets[0].Model != "vp/gpt-5.5" {
		t.Fatalf("unexpected primary target: %+v", got.Targets[0])
	}
	if len(got.Targets[0].APIKeyPool) != 1 {
		t.Fatalf("expected inherited HE pool on primary target, got %+v", got.Targets[0].APIKeyPool)
	}
	if got.Targets[1].Provider != "openai" {
		t.Fatalf("expected shared wildcard openai fallback, got %+v", got.Targets[1])
	}
	if got.Targets[1].Model != "gpt-5.5" {
		t.Fatalf("expected wildcard fallback model to stay gpt-5.5, got %q", got.Targets[1].Model)
	}
}

func TestMergeWildcardFallbackTargetsSkipsWildcardFallbackForDifferentModel(t *testing.T) {
	routes := []Route{
		{
			Alias: "*",
			Targets: []Target{
				{
					Provider:    "openai",
					BaseURL:     "https://api.openai.com/v1",
					AuthMode:    AuthModeAPIKey,
					APIKeyEnv:   "TEST_OPENAI_KEY",
					Model:       "gpt-5.5",
					UpstreamAPI: "responses",
				},
			},
		},
		{
			Alias: "anthropic/claude-sonnet-4-6",
			Targets: []Target{
				{
					Provider:  "he-tokenapi",
					BaseURL:   "https://tokenapi.happyelements.net/v1",
					AuthMode:  AuthModeAPIKey,
					APIKeyEnv: "TEST_HE_TOKENAPI_KEY",
					Model:     "anthropic/claude-sonnet-4-6",
				},
			},
		},
	}

	route, ok := FindRoute(routes, "anthropic/claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected exact claude route")
	}
	got := mergeWildcardFallbackTargets(routes, route, "anthropic/claude-sonnet-4-6")
	if len(got.Targets) != 1 {
		t.Fatalf("expected no inherited wildcard fallback for different model, got %d targets", len(got.Targets))
	}
}

func TestShouldCooldownAPIKeyPoolItemUsesQuotaSignalsOnly(t *testing.T) {
	if !shouldCooldownAPIKeyPoolItem(http.StatusForbidden, `{"error":{"message":"insufficient balance"}}`) {
		t.Fatal("expected insufficient balance to trigger cooldown")
	}
	if shouldCooldownAPIKeyPoolItem(http.StatusForbidden, `{"error":{"message":"Image generation is not enabled for this group","type":"permission_error"}}`) {
		t.Fatal("hard permission error should not trigger cooldown")
	}
	if shouldCooldownAPIKeyPoolItem(http.StatusTooManyRequests, `{"error":{"message":"too many requests"}}`) {
		t.Fatal("rate limit should not trigger cooldown")
	}
}

func TestNormalizeRoutesRejectsMixedAuthModeFields(t *testing.T) {
	_, err := NormalizeRoutes([]Route{{
		Alias: "team-agent",
		Targets: []Target{{
			Provider:  "test",
			BaseURL:   "https://example.com/v1",
			AuthMode:  AuthModeCustomHeadersCookie,
			APIKeyEnv: "TEST_OPENAI_KEY",
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
		target Target
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
		{
			name:   "403 image generation permission body retries for non he target",
			status: 403,
			body:   `{"error":{"message":"Image generation is not enabled for this group","type":"permission_error"}}`,
			want:   true,
			target: Target{Provider: "openai"},
		},
		{
			name:   "403 model access denied retries",
			status: 403,
			body:   `{"error":{"message":"key not allowed to access model","code":"key_model_access_denied"}}`,
			want:   true,
			target: Target{Provider: "he-tokenapi"},
		},
		{
			name:   "403 he image generation permission body does not retry",
			status: 403,
			body:   `{"error":{"message":"Image generation is not enabled for this group","type":"permission_error"}}`,
			want:   false,
			target: Target{Provider: "he-tokenapi"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetryAIGatewayFailure(tc.target, tc.status, tc.body); got != tc.want {
				t.Fatalf("shouldRetryAIGatewayFailure(%+v, %d, %q) = %v, want %v", tc.target, tc.status, tc.body, got, tc.want)
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

func TestProxyDoesNotFallbackAfterHeImagePermission403(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Image generation is not enabled for this group","type":"permission_error"}}`))
	}))
	defer upstream.Close()

	rt := &Runtime{}
	route := Route{
		Alias:    "gpt-5.4",
		Strategy: "fallback",
		Targets: []Target{
			{Provider: "he-tokenapi", BaseURL: upstream.URL, AuthMode: AuthModeAPIKey, APIKeyEnv: "TEST_KEY_ONE", Model: "vp/gpt-5.4", Enabled: true},
			{Provider: "openai", BaseURL: upstream.URL, AuthMode: AuthModeAPIKey, APIKeyEnv: "TEST_KEY_TWO", Model: "gpt-5.4", Enabled: true},
		},
	}
	t.Setenv("TEST_KEY_ONE", "token-one")
	t.Setenv("TEST_KEY_TWO", "token-two")

	requestID := "req_test"
	key := VirtualKey{ID: "vk_test", WorkspaceID: "ws_test", CreatedByEmail: "user@example.com"}
	rec := httptest.NewRecorder()
	var lastErr string
	var lastStatus int

	for _, target := range selectTargets(route, "gpt-5.4", "/responses") {
		auth, err := ResolveUpstreamAuth(target)
		if err != nil {
			t.Fatalf("ResolveUpstreamAuth: %v", err)
		}
		status, retry, errText := rt.forward(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), ForwardRequest{
			Key:              key,
			RequestID:        requestID,
			CallerID:         key.CallerID(),
			Endpoint:         "/responses",
			UpstreamEndpoint: "/responses",
			ModelAlias:       "gpt-5.4",
			Target:           target,
			TargetModel:      target.Model,
			AuthHeaders:      auth.Headers,
			Body:             []byte(`{"model":"gpt-5.4","input":"hello"}`),
		})
		if !retry {
			lastStatus = status
			lastErr = errText
			break
		}
		lastStatus = status
		lastErr = errText
		if !retry && status > 0 && status < http.StatusInternalServerError && status != http.StatusTooManyRequests {
			break
		}
	}

	if attempts != 1 {
		t.Fatalf("expected he permission error to stop without fallback, got %d attempts", attempts)
	}
	if lastStatus != http.StatusForbidden {
		t.Fatalf("expected forbidden status, got status=%d err=%q", lastStatus, lastErr)
	}
}

func TestSummarizeAIGatewayRequestCapturesAuditShape(t *testing.T) {
	summary := summarizeAIGatewayRequest([]byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"hello"},
				{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}
			]}
		],
		"tools":[
			{"type":"web_search_preview"},
			{"type":"function","name":"memory_lookup"}
		]
	}`))
	if !summary.HasImageURL {
		t.Fatal("expected image_url to be detected")
	}
	if summary.TextOnly {
		t.Fatal("expected request with image_url to not be text-only")
	}
	if summary.ToolCount != 2 {
		t.Fatalf("tool_count = %d, want 2", summary.ToolCount)
	}
	if strings.Join(summary.ToolTypes, ",") != "web_search_preview,function" {
		t.Fatalf("tool_types = %v", summary.ToolTypes)
	}
	if strings.Join(summary.ToolDescriptors, ",") != "web_search_preview,function:memory_lookup" {
		t.Fatalf("tool_descriptors = %v", summary.ToolDescriptors)
	}
	if strings.Join(summary.InputItemTypes, ",") != "message,input_text,image_url" {
		t.Fatalf("input_item_types = %v", summary.InputItemTypes)
	}
}

func TestSummarizeAIGatewayRequestMarksTextOnlyRequests(t *testing.T) {
	summary := summarizeAIGatewayRequest([]byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"just text"}
			]}
		]
	}`))
	if summary.HasImageURL {
		t.Fatal("expected no image_url")
	}
	if !summary.TextOnly {
		t.Fatal("expected text-only request")
	}
	if summary.ToolCount != 0 {
		t.Fatalf("tool_count = %d, want 0", summary.ToolCount)
	}
	if strings.Join(summary.InputItemTypes, ",") != "message,input_text" {
		t.Fatalf("input_item_types = %v", summary.InputItemTypes)
	}
}

func TestPreparePayloadForUpstreamRemovesImageGenerationToolForHeResponses(t *testing.T) {
	rt := &Runtime{}
	payload := map[string]any{
		"model": "gpt-5.5",
		"tools": []any{
			map[string]any{"type": "web_search"},
			map[string]any{"type": "image_generation"},
			map[string]any{"type": "function", "name": "memory_lookup"},
		},
	}
	got := rt.preparePayloadForUpstream(t.Context(), VirtualKey{}, "/responses", payload, Target{Provider: "he-tokenapi"})
	tools, ok := got["tools"].([]any)
	if !ok {
		t.Fatalf("tools missing after filtering: %#v", got["tools"])
	}
	summary := summarizeAIGatewayRequest(mustJSONMarshal(t, got))
	if strings.Contains(strings.Join(summary.ToolTypes, ","), "image_generation") {
		t.Fatalf("expected image_generation to be removed, got %v", summary.ToolTypes)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after filtering, got %d", len(tools))
	}
}

func TestPreparePayloadForUpstreamKeepsImageGenerationToolForOpenAIResponses(t *testing.T) {
	rt := &Runtime{}
	payload := map[string]any{
		"model": "gpt-5.5",
		"tools": []any{
			map[string]any{"type": "image_generation"},
		},
	}
	got := rt.preparePayloadForUpstream(t.Context(), VirtualKey{}, "/responses", payload, Target{Provider: "openai"})
	summary := summarizeAIGatewayRequest(mustJSONMarshal(t, got))
	if strings.Join(summary.ToolTypes, ",") != "image_generation" {
		t.Fatalf("expected image_generation to remain, got %v", summary.ToolTypes)
	}
}

func TestPreparePayloadForUpstreamFiltersUnsupportedAnthropicHETools(t *testing.T) {
	rt := &Runtime{}
	payload := map[string]any{
		"model":               "anthropic/claude-opus-4-8",
		"client_metadata":     map[string]any{"session": "abc"},
		"metadata":            map[string]any{"foo": "bar"},
		"store":               true,
		"parallel_tool_calls": true,
		"text":                map[string]any{"format": "json"},
		"tools": []any{
			map[string]any{"type": "function", "name": "memory_lookup"},
			map[string]any{"type": "namespace", "name": "mcp__codegraph"},
			map[string]any{"type": "web_search"},
		},
	}
	got := rt.preparePayloadForUpstream(t.Context(), VirtualKey{}, "/responses", payload, Target{
		Provider: "he-tokenapi",
		Model:    "anthropic/claude-opus-4-8",
	})
	tools, ok := got["tools"].([]any)
	if !ok {
		t.Fatalf("tools missing after filtering: %#v", got["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("expected only supported anthropic HE tool to remain, got %d", len(tools))
	}
	for _, removedKey := range []string{"client_metadata", "metadata", "store", "parallel_tool_calls", "text"} {
		if _, exists := got[removedKey]; exists {
			t.Fatalf("expected %q to be removed for anthropic HE responses payload", removedKey)
		}
	}
	summary := summarizeAIGatewayRequest(mustJSONMarshal(t, got))
	if strings.Join(summary.ToolTypes, ",") != "function" {
		t.Fatalf("expected only function tool to remain, got %v", summary.ToolTypes)
	}
}

func TestProxyHeAvailabilitySuccessDoesNotFallback(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer upstream.Close()

	rt := &Runtime{}
	route := Route{
		Alias:    "gpt-5.5",
		Strategy: "fallback",
		Targets: []Target{
			{Provider: "he-tokenapi", BaseURL: upstream.URL, AuthMode: AuthModeAPIKey, APIKeyEnv: "TEST_KEY_ONE", Model: "vp/gpt-5.5", Enabled: true},
			{Provider: "openai", BaseURL: upstream.URL, AuthMode: AuthModeAPIKey, APIKeyEnv: "TEST_KEY_TWO", Model: "gpt-5.5", Enabled: true},
		},
	}
	t.Setenv("TEST_KEY_ONE", "token-one")
	t.Setenv("TEST_KEY_TWO", "token-two")

	requestID := "req_he_ok"
	key := VirtualKey{ID: "vk_test", WorkspaceID: "ws_test", CreatedByEmail: "user@example.com"}
	rec := httptest.NewRecorder()
	var lastErr string
	var lastStatus int

	for _, target := range selectTargets(route, "gpt-5.5", "/responses") {
		auth, err := ResolveUpstreamAuth(target)
		if err != nil {
			t.Fatalf("ResolveUpstreamAuth: %v", err)
		}
		status, retry, errText := rt.forward(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), ForwardRequest{
			Key:              key,
			RequestID:        requestID,
			CallerID:         key.CallerID(),
			Endpoint:         "/responses",
			UpstreamEndpoint: "/responses",
			ModelAlias:       "gpt-5.5",
			Target:           target,
			TargetModel:      target.Model,
			AuthHeaders:      auth.Headers,
			Body:             []byte(`{"model":"gpt-5.5","input":"hello"}`),
		})
		lastStatus = status
		lastErr = errText
		if !retry {
			break
		}
	}

	if attempts != 1 {
		t.Fatalf("expected he target to serve request without fallback, got %d attempts", attempts)
	}
	if lastStatus >= 400 {
		t.Fatalf("expected final success, got status=%d err=%q", lastStatus, lastErr)
	}
}

func TestHEPoolQuotaOnSingleSubKeyFallsThroughWithinPoolBeforeOpenAIFallback(t *testing.T) {
	var authHeaders []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		switch r.Header.Get("Authorization") {
		case "Bearer he-key-1":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance"}}`))
		case "Bearer he-key-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
		case "Bearer openai-fallback":
			t.Fatal("openai fallback should not be used while another HE sub-key is still available")
		default:
			t.Fatalf("unexpected auth header %q", r.Header.Get("Authorization"))
		}
	}))
	defer upstream.Close()

	rt := &Runtime{}
	target := Target{
		ID:       "target-he-pool",
		Provider: "he-tokenapi",
		BaseURL:  upstream.URL,
		AuthMode: AuthModeAPIKey,
		Model:    "vp/gpt-5.5",
		APIKeyPool: []APIKeyPoolItem{
			{ID: "key-1", Label: "key-1", APIKey: "he-key-1", SharedByEmail: "a@example.com", Enabled: true},
			{ID: "key-2", Label: "key-2", APIKey: "he-key-2", SharedByEmail: "b@example.com", Enabled: true},
		},
	}

	rec := httptest.NewRecorder()
	status, retry, errText, fatalErr := rt.forwardTargetWithHEAPIKeyPool(
		rec,
		httptest.NewRequest(http.MethodPost, "/v1/responses", nil),
		VirtualKey{ID: "vk_test", WorkspaceID: "ws_test", CreatedByEmail: "c@example.com"},
		"req_pool_retry",
		"c@example.com",
		"/responses",
		"gpt-5.5",
		map[string]any{"model": "gpt-5.5", "input": "hello"},
		false,
		target,
	)
	if fatalErr != nil {
		t.Fatalf("forwardTargetWithHEAPIKeyPool fatalErr: %v", fatalErr)
	}
	if retry {
		t.Fatalf("expected pool-internal retry to succeed without escalating to outer fallback, got retry=true status=%d err=%q", status, errText)
	}
	if status >= 400 {
		t.Fatalf("expected final success from second HE sub-key, got status=%d err=%q", status, errText)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("expected two HE sub-key attempts, got %d (%v)", len(authHeaders), authHeaders)
	}
	if authHeaders[0] != "Bearer he-key-1" || authHeaders[1] != "Bearer he-key-2" {
		t.Fatalf("unexpected attempt order: %v", authHeaders)
	}
}
