CREATE TABLE IF NOT EXISTS feishu_chat_session_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    feishu_chat_id TEXT NOT NULL,
    feishu_root_id TEXT NOT NULL DEFAULT '',
    last_message_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id, feishu_chat_id, feishu_root_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_chat_session_binding_session
    ON feishu_chat_session_binding(chat_session_id);

CREATE INDEX IF NOT EXISTS idx_feishu_chat_session_binding_lookup
    ON feishu_chat_session_binding(workspace_id, user_id, feishu_chat_id, feishu_root_id);
