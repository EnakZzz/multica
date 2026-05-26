ALTER TABLE ai_gateway_route_target
    ADD COLUMN IF NOT EXISTS reasoning_effort text
    CHECK (reasoning_effort IS NULL OR reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh'));
