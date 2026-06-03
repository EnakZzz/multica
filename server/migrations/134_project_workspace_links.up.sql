-- Allow a project owned by one workspace to be used from other workspaces.
CREATE TABLE project_workspace_link (
    project_id UUID NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, workspace_id)
);

CREATE INDEX idx_project_workspace_link_workspace
    ON project_workspace_link(workspace_id, project_id);
