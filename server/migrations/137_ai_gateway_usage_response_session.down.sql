DROP INDEX IF EXISTS ai_gateway_usage_response_session_idx;

ALTER TABLE ai_gateway_usage
    DROP COLUMN IF EXISTS response_session_id,
    DROP COLUMN IF EXISTS previous_response_id,
    DROP COLUMN IF EXISTS response_id;

ALTER TABLE ai_gateway_response_state
    DROP COLUMN IF EXISTS session_id;
