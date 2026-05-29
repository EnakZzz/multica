CREATE TABLE project_visual_node_generation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id uuid NOT NULL REFERENCES project_visual_board(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    node_id uuid NOT NULL REFERENCES project_visual_node(id) ON DELETE CASCADE,
    task_id uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
    attachment_id uuid REFERENCES attachment(id) ON DELETE SET NULL,
    note text NOT NULL DEFAULT '',
    note_zh text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    error_zh text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id)
);

CREATE INDEX idx_project_visual_node_generation_node
    ON project_visual_node_generation(node_id, created_at DESC);

CREATE INDEX idx_project_visual_node_generation_issue
    ON project_visual_node_generation(issue_id)
    WHERE issue_id IS NOT NULL;
