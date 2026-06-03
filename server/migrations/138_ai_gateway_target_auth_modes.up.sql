ALTER TABLE ai_gateway_route_target
    ADD COLUMN IF NOT EXISTS auth_mode text NOT NULL DEFAULT 'api_key',
    ADD COLUMN IF NOT EXISTS cookie_env text,
    ADD COLUMN IF NOT EXISTS custom_header_envs jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE ai_gateway_route_target
SET auth_mode = 'api_key'
WHERE auth_mode IS NULL OR auth_mode = '';

ALTER TABLE ai_gateway_route_target
    DROP CONSTRAINT IF EXISTS ai_gateway_route_target_auth_mode_check;

ALTER TABLE ai_gateway_route_target
    ADD CONSTRAINT ai_gateway_route_target_auth_mode_check
        CHECK (auth_mode IN ('api_key', 'custom_headers_cookie'));

ALTER TABLE ai_gateway_usage
    ADD COLUMN IF NOT EXISTS auth_mode text NOT NULL DEFAULT 'api_key';

UPDATE ai_gateway_usage
SET auth_mode = 'api_key'
WHERE auth_mode IS NULL OR auth_mode = '';

ALTER TABLE ai_gateway_usage
    DROP CONSTRAINT IF EXISTS ai_gateway_usage_auth_mode_check;

ALTER TABLE ai_gateway_usage
    ADD CONSTRAINT ai_gateway_usage_auth_mode_check
        CHECK (auth_mode IN ('api_key', 'custom_headers_cookie'));
