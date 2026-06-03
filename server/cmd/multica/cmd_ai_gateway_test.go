package main

import "testing"

func TestParseHeaderEnvBindings(t *testing.T) {
	got, err := parseHeaderEnvBindings([]string{"X-Test-Token=AI_GATEWAY_CHATGPT_HEADER_TOKEN"})
	if err != nil {
		t.Fatalf("parseHeaderEnvBindings: %v", err)
	}
	if len(got) != 1 || got[0].HeaderName != "X-Test-Token" || got[0].EnvName != "AI_GATEWAY_CHATGPT_HEADER_TOKEN" {
		t.Fatalf("unexpected bindings: %+v", got)
	}
}

func TestParseHeaderEnvBindingsRejectsInvalidInput(t *testing.T) {
	if _, err := parseHeaderEnvBindings([]string{"missing-separator"}); err == nil {
		t.Fatal("expected invalid binding error")
	}
}
