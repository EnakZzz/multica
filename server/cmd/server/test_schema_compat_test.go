package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ensureInboxFeishuColumns(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_status TEXT NOT NULL DEFAULT 'not_applicable'`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivered_at TIMESTAMPTZ`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_attempts INT NOT NULL DEFAULT 0`,
		`ALTER TABLE inbox_item ADD COLUMN IF NOT EXISTS feishu_delivery_last_error TEXT`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureAIGatewayEmbeddingsUpstream(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range []string{
		`ALTER TABLE ai_gateway_route_target DROP CONSTRAINT IF EXISTS ai_gateway_route_target_upstream_api_check`,
		`ALTER TABLE ai_gateway_route_target ADD CONSTRAINT ai_gateway_route_target_upstream_api_check CHECK (upstream_api IN ('responses', 'chat_completions', 'embeddings'))`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
