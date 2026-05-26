CREATE TABLE project_visual_board (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    viewport jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id)
);

CREATE TABLE project_visual_node (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id uuid NOT NULL REFERENCES project_visual_board(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    type text NOT NULL CHECK (type IN ('character', 'scene', 'ui_element', 'prop', 'reference', 'gameplay_note', 'generated_variant')),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'adopted', 'rejected', 'generating', 'failed')),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    prompt text NOT NULL DEFAULT '',
    position_x double precision NOT NULL DEFAULT 0,
    position_y double precision NOT NULL DEFAULT 0,
    source_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    reference_attachment_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    result_attachment_id uuid REFERENCES attachment(id) ON DELETE SET NULL,
    result_note text NOT NULL DEFAULT '',
    generation_agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    generation_task_id uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    generation_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_visual_edge (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id uuid NOT NULL REFERENCES project_visual_board(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    source_node_id uuid NOT NULL REFERENCES project_visual_node(id) ON DELETE CASCADE,
    target_node_id uuid NOT NULL REFERENCES project_visual_node(id) ON DELETE CASCADE,
    relation text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_project_visual_board_project ON project_visual_board(project_id);
CREATE INDEX idx_project_visual_node_board ON project_visual_node(board_id, type, status);
CREATE INDEX idx_project_visual_node_project ON project_visual_node(project_id, updated_at DESC);
CREATE INDEX idx_project_visual_edge_board ON project_visual_edge(board_id);
