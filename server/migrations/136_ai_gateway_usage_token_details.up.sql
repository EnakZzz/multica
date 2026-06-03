ALTER TABLE ai_gateway_usage
    ADD COLUMN IF NOT EXISTS cached_input_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS billable_input_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reasoning_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS long_context boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cached_input_cost_micros bigint NOT NULL DEFAULT 0;

