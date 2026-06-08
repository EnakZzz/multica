CREATE TABLE IF NOT EXISTS ai_gateway_route_target_api_key_pool (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_target_id uuid NOT NULL REFERENCES ai_gateway_route_target(id) ON DELETE CASCADE,
    label text NOT NULL,
    api_key text NOT NULL,
    key_masked text NOT NULL,
    shared_by_email text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    reenable_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_gateway_route_target_api_key_pool_target_idx
    ON ai_gateway_route_target_api_key_pool(route_target_id, created_at ASC);

CREATE INDEX IF NOT EXISTS ai_gateway_route_target_api_key_pool_reenable_idx
    ON ai_gateway_route_target_api_key_pool(route_target_id, enabled, reenable_at);
