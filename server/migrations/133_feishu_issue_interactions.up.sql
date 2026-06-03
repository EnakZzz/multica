CREATE TABLE IF NOT EXISTS feishu_user_identity (
    user_id UUID PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    open_id TEXT NOT NULL,
    union_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_user_identity_open_id ON feishu_user_identity(open_id);
CREATE INDEX IF NOT EXISTS idx_feishu_user_identity_email ON feishu_user_identity(lower(email));

CREATE TABLE IF NOT EXISTS feishu_message_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    inbox_item_id UUID NOT NULL REFERENCES inbox_item(id) ON DELETE CASCADE,
    recipient_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    receive_id_type TEXT NOT NULL,
    receive_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    root_id TEXT,
    chat_id TEXT,
    card_action_value JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_message_binding_message_id ON feishu_message_binding(message_id);
CREATE INDEX IF NOT EXISTS idx_feishu_message_binding_root_id ON feishu_message_binding(root_id) WHERE root_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_feishu_message_binding_issue ON feishu_message_binding(workspace_id, issue_id);

CREATE TABLE IF NOT EXISTS feishu_event_delivery (
    event_id TEXT PRIMARY KEY,
    handled_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
