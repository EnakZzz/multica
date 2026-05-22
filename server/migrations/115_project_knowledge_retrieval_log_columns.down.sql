ALTER TABLE project_knowledge_retrieval_log
    DROP CONSTRAINT IF EXISTS project_knowledge_retrieval_log_feedback_check,
    DROP COLUMN IF EXISTS feedback_note,
    DROP COLUMN IF EXISTS feedback,
    DROP COLUMN IF EXISTS prompt_section_hash,
    DROP COLUMN IF EXISTS injected_item_count,
    DROP COLUMN IF EXISTS token_budget,
    DROP COLUMN IF EXISTS selected_items,
    DROP COLUMN IF EXISTS candidates,
    DROP COLUMN IF EXISTS query_context,
    DROP COLUMN IF EXISTS search_mode;
