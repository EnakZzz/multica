ALTER TABLE project_knowledge_retrieval_log
    ADD COLUMN IF NOT EXISTS search_mode text NOT NULL DEFAULT 'hybrid',
    ADD COLUMN IF NOT EXISTS query_context jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS candidates jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS selected_items jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS token_budget integer,
    ADD COLUMN IF NOT EXISTS injected_item_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS prompt_section_hash text,
    ADD COLUMN IF NOT EXISTS feedback text,
    ADD COLUMN IF NOT EXISTS feedback_note text;

ALTER TABLE project_knowledge_retrieval_log
    DROP CONSTRAINT IF EXISTS project_knowledge_retrieval_log_feedback_check,
    ADD CONSTRAINT project_knowledge_retrieval_log_feedback_check
        CHECK (feedback IS NULL OR feedback IN ('useful', 'noisy', 'wrong', 'stale'));
