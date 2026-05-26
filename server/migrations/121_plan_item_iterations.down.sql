ALTER TABLE plan_item
    DROP COLUMN IF EXISTS iteration_branch_name,
    DROP COLUMN IF EXISTS iteration_title,
    DROP COLUMN IF EXISTS iteration_index;
