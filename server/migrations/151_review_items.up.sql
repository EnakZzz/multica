CREATE TABLE review_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('skill_review', 'issue_change_review', 'plan_review', 'artifact_review')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'changes_requested', 'rejected', 'superseded')),
    risk_level TEXT NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high')),
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    source_actor_type TEXT,
    source_actor_id UUID,
    source_object_type TEXT NOT NULL DEFAULT '',
    source_object_id UUID,
    target_object_type TEXT NOT NULL DEFAULT '',
    target_object_id UUID,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    diff TEXT NOT NULL DEFAULT '',
    available_actions TEXT[] NOT NULL DEFAULT '{}',
    reviewer_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    review_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

CREATE INDEX idx_review_item_workspace_status
    ON review_item(workspace_id, status, created_at DESC);

CREATE INDEX idx_review_item_workspace_type
    ON review_item(workspace_id, type, created_at DESC);

CREATE UNIQUE INDEX idx_review_item_pending_source
    ON review_item(workspace_id, type, source_object_type, source_object_id)
    WHERE status = 'pending' AND source_object_id IS NOT NULL;
