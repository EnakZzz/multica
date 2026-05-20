ALTER TABLE plan_item
ADD COLUMN IF NOT EXISTS depends_on_positions INTEGER[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_issue_dependency_issue ON issue_dependency(issue_id);
CREATE INDEX IF NOT EXISTS idx_issue_dependency_depends_on ON issue_dependency(depends_on_issue_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_dependency_unique
    ON issue_dependency(issue_id, depends_on_issue_id, type);
