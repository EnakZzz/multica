package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

const projectKnowledgeEmbeddingInterval = 30 * time.Second

func runProjectKnowledgeEmbeddingWorker(ctx context.Context, svc *service.ProjectKnowledgeService) {
	if svc == nil || !svc.EmbeddingsConfigured() {
		slog.Info("project knowledge embeddings unconfigured", "configured", false)
		return
	}
	slog.Info("project knowledge embeddings configured", "configured", true, "model", svc.Embedder.Model(), "dimensions", svc.Embedder.Dimensions())
	processProjectKnowledgeEmbeddingBatch(ctx, svc)

	ticker := time.NewTicker(projectKnowledgeEmbeddingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processProjectKnowledgeEmbeddingBatch(ctx, svc)
		}
	}
}

func processProjectKnowledgeEmbeddingBatch(ctx context.Context, svc *service.ProjectKnowledgeService) {
	result, err := svc.ProcessEmbeddingJobs(ctx, 25)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotConfigured) {
			return
		}
		slog.Debug("project knowledge embedding worker tick failed", "error", err)
		return
	}
	if result.Processed > 0 {
		slog.Info("project knowledge embedding jobs processed", "processed", result.Processed, "succeeded", result.Succeeded, "failed", result.Failed)
	}
}
