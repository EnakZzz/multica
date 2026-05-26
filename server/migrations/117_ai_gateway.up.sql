CREATE TABLE IF NOT EXISTS ai_gateway_virtual_key (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by uuid REFERENCES "user"(id) ON DELETE SET NULL,
    name text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    token_prefix text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_gateway_virtual_key_workspace_idx
    ON ai_gateway_virtual_key(workspace_id, created_at DESC);

CREATE TABLE IF NOT EXISTS ai_gateway_route (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    alias text NOT NULL,
    strategy text NOT NULL DEFAULT 'fallback' CHECK (strategy IN ('fallback', 'single', 'round_robin', 'weighted')),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, alias)
);

CREATE INDEX IF NOT EXISTS ai_gateway_route_workspace_idx
    ON ai_gateway_route(workspace_id, enabled, alias);

CREATE TABLE IF NOT EXISTS ai_gateway_route_target (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id uuid NOT NULL REFERENCES ai_gateway_route(id) ON DELETE CASCADE,
    provider text NOT NULL,
    base_url text NOT NULL,
    api_key_env text NOT NULL,
    model text NOT NULL,
    upstream_api text NOT NULL DEFAULT 'responses' CHECK (upstream_api IN ('responses', 'chat_completions')),
    reasoning_effort text CHECK (reasoning_effort IS NULL OR reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh')),
    organization_env text,
    project_env text,
    timeout_seconds integer NOT NULL DEFAULT 60,
    weight integer NOT NULL DEFAULT 1 CHECK (weight > 0),
    priority integer NOT NULL DEFAULT 0,
    enabled boolean NOT NULL DEFAULT true,
    input_price_per_million_micros bigint NOT NULL DEFAULT 0,
    output_price_per_million_micros bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_gateway_route_target_route_idx
    ON ai_gateway_route_target(route_id, enabled, priority, created_at);

CREATE TABLE IF NOT EXISTS ai_gateway_usage (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    virtual_key_id uuid REFERENCES ai_gateway_virtual_key(id) ON DELETE SET NULL,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    request_id text NOT NULL,
    caller_id text,
    endpoint text NOT NULL,
    model_alias text NOT NULL,
    upstream_provider text NOT NULL,
    upstream_model text NOT NULL,
    reasoning_effort text CHECK (reasoning_effort IS NULL OR reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh')),
    status_code integer NOT NULL,
    prompt_tokens bigint NOT NULL DEFAULT 0,
    completion_tokens bigint NOT NULL DEFAULT 0,
    total_tokens bigint NOT NULL DEFAULT 0,
    input_cost_micros bigint NOT NULL DEFAULT 0,
    output_cost_micros bigint NOT NULL DEFAULT 0,
    total_cost_micros bigint NOT NULL DEFAULT 0,
    latency_ms bigint NOT NULL DEFAULT 0,
    error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE ai_gateway_usage
    ADD COLUMN IF NOT EXISTS caller_id text,
    ADD COLUMN IF NOT EXISTS input_cost_micros bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_cost_micros bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_cost_micros bigint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS ai_gateway_usage_workspace_created_idx
    ON ai_gateway_usage(workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS ai_gateway_usage_key_created_idx
    ON ai_gateway_usage(virtual_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS ai_gateway_usage_workspace_caller_created_idx
    ON ai_gateway_usage(workspace_id, caller_id, created_at DESC);
