CREATE TABLE IF NOT EXISTS ai_gateway_response_state (
    response_id text PRIMARY KEY,
    virtual_key_id uuid NOT NULL REFERENCES ai_gateway_virtual_key(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    upstream_provider text NOT NULL DEFAULT '',
    upstream_model text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ai_gateway_response_state_key_idx
    ON ai_gateway_response_state(virtual_key_id, last_used_at DESC);

CREATE INDEX IF NOT EXISTS ai_gateway_response_state_workspace_idx
    ON ai_gateway_response_state(workspace_id, last_used_at DESC);
