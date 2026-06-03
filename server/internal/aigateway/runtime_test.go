package aigateway

import "testing"

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
