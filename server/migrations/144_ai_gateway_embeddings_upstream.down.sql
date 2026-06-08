ALTER TABLE ai_gateway_route_target
    DROP CONSTRAINT IF EXISTS ai_gateway_route_target_upstream_api_check;

ALTER TABLE ai_gateway_route_target
    ADD CONSTRAINT ai_gateway_route_target_upstream_api_check
    CHECK (upstream_api IN ('responses', 'chat_completions'));
