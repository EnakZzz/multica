ALTER TABLE feishu_chat_session_binding
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES project(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_feishu_chat_session_binding_project
    ON feishu_chat_session_binding(project_id);

CREATE TABLE IF NOT EXISTS feishu_chat_pending_selection (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    open_id TEXT NOT NULL,
    feishu_chat_id TEXT NOT NULL,
    feishu_root_id TEXT NOT NULL DEFAULT '',
    feishu_message_id TEXT NOT NULL,
    original_content TEXT NOT NULL,
    candidate_project_ids UUID[] NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    selected_project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 minutes',
    consumed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_feishu_chat_pending_selection_lookup
    ON feishu_chat_pending_selection(user_id, feishu_chat_id, feishu_root_id, status, expires_at);
