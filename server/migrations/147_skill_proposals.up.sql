CREATE TABLE skill_proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    source_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    source_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    operation TEXT NOT NULL CHECK (operation IN ('insert', 'update', 'delete')),
    target_skill_id UUID REFERENCES skill(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'rejected', 'applied')),
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    rationale TEXT NOT NULL DEFAULT '',
    risk_level TEXT NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high')),
    proposed_name TEXT NOT NULL DEFAULT '',
    proposed_description TEXT NOT NULL DEFAULT '',
    proposed_content TEXT NOT NULL DEFAULT '',
    proposed_files JSONB NOT NULL DEFAULT '[]'::jsonb,
    base_content_hash TEXT NOT NULL DEFAULT '',
    diff TEXT NOT NULL DEFAULT '',
    evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    curator_model TEXT NOT NULL DEFAULT 'rule-v1',
    curator_prompt_hash TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    reviewed_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    rejected_reason TEXT NOT NULL DEFAULT '',
    applied_skill_id UUID REFERENCES skill(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    applied_at TIMESTAMPTZ
);

CREATE INDEX idx_skill_proposal_workspace_status
    ON skill_proposal(workspace_id, status, created_at DESC);

CREATE INDEX idx_skill_proposal_target
    ON skill_proposal(target_skill_id, created_at DESC)
    WHERE target_skill_id IS NOT NULL;

CREATE UNIQUE INDEX idx_skill_proposal_pending_dedupe
    ON skill_proposal(workspace_id, operation, COALESCE(target_skill_id, '00000000-0000-0000-0000-000000000000'::uuid), md5(proposed_content))
    WHERE status = 'pending';
