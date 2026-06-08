ALTER TABLE ai_gateway_route_target
    ADD COLUMN IF NOT EXISTS api_key text;
