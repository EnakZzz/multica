ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS requires_git_commit boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS branch_name text NOT NULL DEFAULT '';
