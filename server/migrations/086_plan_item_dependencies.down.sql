DROP INDEX IF EXISTS idx_issue_dependency_unique;
DROP INDEX IF EXISTS idx_issue_dependency_depends_on;
DROP INDEX IF EXISTS idx_issue_dependency_issue;

ALTER TABLE plan_item
DROP COLUMN IF EXISTS depends_on_positions;
