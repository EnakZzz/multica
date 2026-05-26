ALTER TABLE ai_gateway_usage
    ADD COLUMN IF NOT EXISTS reasoning_effort text
    CHECK (reasoning_effort IS NULL OR reasoning_effort IN ('minimal', 'low', 'medium', 'high', 'xhigh'));
