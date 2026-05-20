CREATE TABLE pipeline (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    default_project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    archived_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_pipeline_workspace_active_name
    ON pipeline(workspace_id, lower(name))
    WHERE archived_at IS NULL;

CREATE INDEX idx_pipeline_workspace_created_at
    ON pipeline(workspace_id, created_at DESC)
    WHERE archived_at IS NULL;

CREATE TABLE pipeline_role (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_id UUID NOT NULL REFERENCES pipeline(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    required_skill_ids UUID[] NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pipeline_id, key)
);

CREATE INDEX idx_pipeline_role_pipeline_position
    ON pipeline_role(pipeline_id, position ASC);

CREATE TABLE pipeline_stage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_id UUID NOT NULL REFERENCES pipeline(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    role_key TEXT NOT NULL,
    depends_on_stage_keys TEXT[] NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pipeline_id, key)
);

CREATE INDEX idx_pipeline_stage_pipeline_position
    ON pipeline_stage(pipeline_id, position ASC);

CREATE TABLE pipeline_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_id UUID NOT NULL REFERENCES pipeline(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    parent_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'completed'
        CHECK (status IN ('completed', 'failed')),
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_pipeline_run_pipeline_created_at
    ON pipeline_run(pipeline_id, created_at DESC);

CREATE TABLE pipeline_run_stage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_run_id UUID NOT NULL REFERENCES pipeline_run(id) ON DELETE CASCADE,
    pipeline_stage_id UUID REFERENCES pipeline_stage(id) ON DELETE SET NULL,
    stage_key TEXT NOT NULL,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(pipeline_run_id, stage_key)
);

CREATE INDEX idx_pipeline_run_stage_run
    ON pipeline_run_stage(pipeline_run_id);
