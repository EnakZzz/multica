ALTER TABLE ai_gateway_response_state
    ADD COLUMN IF NOT EXISTS session_id text NOT NULL DEFAULT '';

UPDATE ai_gateway_response_state
SET session_id = response_id
WHERE session_id = '';

ALTER TABLE ai_gateway_usage
    ADD COLUMN IF NOT EXISTS response_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS previous_response_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS response_session_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS ai_gateway_usage_response_session_idx
    ON ai_gateway_usage(response_session_id, created_at DESC)
    WHERE response_session_id <> '';
