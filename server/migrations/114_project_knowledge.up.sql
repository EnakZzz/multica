CREATE EXTENSION IF NOT EXISTS vector;

CREATE OR REPLACE FUNCTION project_knowledge_fts_text(input text)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
AS $$
DECLARE
    token text;
    i integer;
    normalized text;
    output text := '';
BEGIN
    normalized := lower(coalesce(input, ''));
    normalized := translate(normalized, '，。！？；：「」『』（）【】、《》“”‘’…', '                         ');

    FOR token IN
        SELECT unnest(regexp_split_to_array(normalized, '[[:space:][:punct:]]+'))
    LOOP
        token := trim(token);
        IF token = '' THEN
            CONTINUE;
        END IF;

        output := output || ' ' || token;
        IF char_length(token) > 1 AND octet_length(token) > char_length(token) THEN
            FOR i IN 1..(char_length(token) - 1) LOOP
                output := output || ' ' || substr(token, i, 2);
            END LOOP;
        END IF;
    END LOOP;

    RETURN output;
END;
$$;

CREATE TABLE project_wiki_page (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    slug text NOT NULL,
    title text NOT NULL,
    body text NOT NULL DEFAULT '',
    source_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'reviewed', 'archived')),
    updated_by uuid REFERENCES "user"(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    search_document tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', project_knowledge_fts_text(slug || ' ' || title)), 'A') ||
        setweight(to_tsvector('simple', project_knowledge_fts_text(body)), 'B')
    ) STORED,
    UNIQUE (project_id, slug)
);

CREATE INDEX idx_project_wiki_page_project ON project_wiki_page(project_id, status, updated_at DESC);
CREATE INDEX idx_project_wiki_page_search ON project_wiki_page USING gin(search_document);

CREATE TABLE project_memory_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
    task_id uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    comment_id uuid REFERENCES comment(id) ON DELETE SET NULL,
    kind text NOT NULL,
    outcome text NOT NULL DEFAULT '',
    title text NOT NULL,
    summary text NOT NULL DEFAULT '',
    symptom text NOT NULL DEFAULT '',
    cause text NOT NULL DEFAULT '',
    fix_path text NOT NULL DEFAULT '',
    commands jsonb NOT NULL DEFAULT '[]'::jsonb,
    repo_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    confidence integer NOT NULL DEFAULT 60 CHECK (confidence >= 0 AND confidence <= 100),
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    search_document tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', project_knowledge_fts_text(kind || ' ' || outcome || ' ' || title || ' ' || array_to_string(tags, ' '))), 'A') ||
        setweight(to_tsvector('simple', project_knowledge_fts_text(summary || ' ' || symptom)), 'B') ||
        setweight(to_tsvector('simple', project_knowledge_fts_text(cause || ' ' || fix_path)), 'C')
    ) STORED
);

CREATE INDEX idx_project_memory_item_project ON project_memory_item(project_id, kind, updated_at DESC);
CREATE INDEX idx_project_memory_item_issue ON project_memory_item(issue_id) WHERE issue_id IS NOT NULL;
CREATE INDEX idx_project_memory_item_task ON project_memory_item(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_project_memory_item_tags ON project_memory_item USING gin(tags);
CREATE INDEX idx_project_memory_item_search ON project_memory_item USING gin(search_document);

CREATE TABLE project_memory_embedding (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    target_type text NOT NULL CHECK (target_type IN ('wiki_page', 'memory_item')),
    target_id uuid NOT NULL,
    embedding vector(1536) NOT NULL,
    embedding_model text NOT NULL,
    content_hash text NOT NULL,
    embedded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (target_type, target_id, embedding_model)
);

CREATE INDEX idx_project_memory_embedding_target ON project_memory_embedding(target_type, target_id);
CREATE INDEX idx_project_memory_embedding_project ON project_memory_embedding(project_id, embedding_model);
CREATE INDEX idx_project_memory_embedding_hnsw
    ON project_memory_embedding USING hnsw (embedding vector_cosine_ops);

CREATE TABLE project_knowledge_retrieval_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
    task_id uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    query_text text NOT NULL DEFAULT '',
    returned_items jsonb NOT NULL DEFAULT '[]'::jsonb,
    search_mode text NOT NULL DEFAULT 'hybrid',
    query_context jsonb NOT NULL DEFAULT '{}'::jsonb,
    candidates jsonb NOT NULL DEFAULT '[]'::jsonb,
    selected_items jsonb NOT NULL DEFAULT '[]'::jsonb,
    injected_text text NOT NULL DEFAULT '',
    token_budget integer,
    injected_item_count integer NOT NULL DEFAULT 0,
    prompt_section_hash text,
    status text NOT NULL DEFAULT 'injected',
    error text,
    task_outcome text,
    helpfulness integer CHECK (helpfulness IS NULL OR (helpfulness >= 0 AND helpfulness <= 100)),
    feedback text CHECK (feedback IS NULL OR feedback IN ('useful', 'noisy', 'wrong', 'stale')),
    feedback_note text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_project_knowledge_retrieval_log_task ON project_knowledge_retrieval_log(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_project_knowledge_retrieval_log_project ON project_knowledge_retrieval_log(project_id, created_at DESC);
