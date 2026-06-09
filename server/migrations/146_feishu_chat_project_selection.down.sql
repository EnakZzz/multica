DROP TABLE IF EXISTS feishu_chat_pending_selection;

DROP INDEX IF EXISTS idx_feishu_chat_session_binding_project;

ALTER TABLE feishu_chat_session_binding
    DROP COLUMN IF EXISTS project_id;
