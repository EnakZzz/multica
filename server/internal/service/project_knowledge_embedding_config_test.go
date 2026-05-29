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
	t.Setenv("MULTICA_EMBEDDINGS_API_KEY", "")

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
	t.Setenv("MULTICA_EMBEDDINGS_API_KEY", "")

	svc := NewProjectKnowledgeService(nil, nil, projectKnowledgeTestEmbedder{})
	if !svc.EmbeddingsConfigured() {
		t.Fatalf("EmbeddingsConfigured() = false, want true")
	}
	if got := svc.Embedder.Model(); got != "test-model" {
		t.Fatalf("Model() = %q, want test-model", got)
	}
}
