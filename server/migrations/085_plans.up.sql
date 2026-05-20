CREATE TABLE plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'planning'
        CHECK (status IN ('planning', 'ready', 'failed', 'committed')),
    planner_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    project_id UUID REFERENCES project(id) ON DELETE SET NULL,
    parent_title TEXT,
    parent_description TEXT,
    parent_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    error TEXT,
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plan_workspace_created_at ON plan(workspace_id, created_at DESC);
CREATE INDEX idx_plan_task_id ON plan(task_id) WHERE task_id IS NOT NULL;

CREATE TABLE plan_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    recommended_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    match_score INTEGER NOT NULL DEFAULT 0 CHECK (match_score >= 0 AND match_score <= 100),
    match_reason TEXT NOT NULL DEFAULT '',
    missing_capability TEXT NOT NULL DEFAULT '',
    depends_on_positions INTEGER[] NOT NULL DEFAULT '{}',
    selected BOOLEAN NOT NULL DEFAULT TRUE,
    generated_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_plan_item_plan_position ON plan_item(plan_id, position ASC);
CREATE INDEX idx_plan_item_generated_issue ON plan_item(generated_issue_id) WHERE generated_issue_id IS NOT NULL;
