ALTER TABLE ai_gateway_usage
    DROP CONSTRAINT IF EXISTS ai_gateway_usage_auth_mode_check;

ALTER TABLE ai_gateway_usage
    DROP COLUMN IF EXISTS auth_mode;

ALTER TABLE ai_gateway_route_target
    DROP CONSTRAINT IF EXISTS ai_gateway_route_target_auth_mode_check;

ALTER TABLE ai_gateway_route_target
    DROP COLUMN IF EXISTS custom_header_envs,
    DROP COLUMN IF EXISTS cookie_env,
    DROP COLUMN IF EXISTS auth_mode;
