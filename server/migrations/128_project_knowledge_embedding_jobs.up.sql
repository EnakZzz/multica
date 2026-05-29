CREATE TABLE IF NOT EXISTS project_knowledge_embedding_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK (target_type IN ('wiki_page', 'memory_item')),
    target_id UUID NOT NULL,
    embedding_model TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    embedded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (target_type, target_id, embedding_model)
);

CREATE INDEX IF NOT EXISTS idx_project_knowledge_embedding_job_due
    ON project_knowledge_embedding_job (status, next_attempt_at)
    WHERE status IN ('queued', 'failed');

CREATE INDEX IF NOT EXISTS idx_project_knowledge_embedding_job_project
    ON project_knowledge_embedding_job (workspace_id, project_id, target_type);
