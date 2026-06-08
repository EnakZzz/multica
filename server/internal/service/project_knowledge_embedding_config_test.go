package service

import (
	"context"
	"testing"
)

type projectKnowledgeTestEmbedder struct{}

func (projectKnowledgeTestEmbedder) Model() string { return "test-model" }

func (projectKnowledgeTestEmbedder) Dimensions() int { return 3 }

func (projectKnowledgeTestEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 2, 3}, nil
}

func TestNewProjectKnowledgeServiceLeavesEmbedderNilWhenEnvUnconfigured(t *testing.T) {
	t.Setenv("AI_GATEWAY_VIRTUAL_KEY", "")

	svc := NewProjectKnowledgeService(nil, nil, nil)
	if svc.EmbeddingsConfigured() {
		t.Fatalf("EmbeddingsConfigured() = true, want false")
	}
	if svc.Embedder != nil {
		t.Fatalf("Embedder = %#v, want nil", svc.Embedder)
	}
}

func TestProjectKnowledgeServiceEmbeddingsConfiguredRejectsTypedNilEmbedder(t *testing.T) {
	var embedder *OpenAICompatibleEmbedder
	svc := &ProjectKnowledgeService{Embedder: embedder}

	if svc.EmbeddingsConfigured() {
		t.Fatalf("EmbeddingsConfigured() = true for typed nil embedder, want false")
	}
}

func TestProjectKnowledgeServiceUsesExplicitEmbedderWhenProvided(t *testing.T) {
	t.Setenv("AI_GATEWAY_VIRTUAL_KEY", "")

	svc := NewProjectKnowledgeService(nil, nil, projectKnowledgeTestEmbedder{})
	if !svc.EmbeddingsConfigured() {
		t.Fatalf("EmbeddingsConfigured() = false, want true")
	}
	if got := svc.Embedder.Model(); got != "test-model" {
		t.Fatalf("Model() = %q, want test-model", got)
	}
}

func TestNewOpenAICompatibleEmbedderFromEnvUsesAIGatewayDefaults(t *testing.T) {
	t.Setenv("AI_GATEWAY_VIRTUAL_KEY", "mvk_test")
	t.Setenv("AI_GATEWAY_UPSTREAM_URL", "http://ai-gateway:9111")
	t.Setenv("MULTICA_EMBEDDINGS_BASE_URL", "")
	t.Setenv("MULTICA_EMBEDDINGS_MODEL", "")
	t.Setenv("MULTICA_EMBEDDINGS_DIMENSIONS", "")

	embedder := NewOpenAICompatibleEmbedderFromEnv()
	if embedder == nil {
		t.Fatal("NewOpenAICompatibleEmbedderFromEnv() = nil, want embedder")
	}
	if got := embedder.APIKey; got != "mvk_test" {
		t.Fatalf("APIKey = %q, want fixed AI gateway virtual key", got)
	}
	if got := embedder.BaseURL; got != "http://ai-gateway:9111/v1" {
		t.Fatalf("BaseURL = %q, want AI gateway upstream URL", got)
	}
}
