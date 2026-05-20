ALTER TABLE plan_item
    DROP COLUMN IF EXISTS branch_name,
    DROP COLUMN IF EXISTS requires_git_commit;
