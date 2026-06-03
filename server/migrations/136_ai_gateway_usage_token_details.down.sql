ALTER TABLE ai_gateway_usage
    DROP COLUMN IF EXISTS cached_input_cost_micros,
    DROP COLUMN IF EXISTS long_context,
    DROP COLUMN IF EXISTS reasoning_tokens,
    DROP COLUMN IF EXISTS billable_input_tokens,
    DROP COLUMN IF EXISTS cached_input_tokens;

