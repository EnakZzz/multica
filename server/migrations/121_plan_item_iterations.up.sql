ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS iteration_index integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS iteration_title text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS iteration_branch_name text NOT NULL DEFAULT '';
