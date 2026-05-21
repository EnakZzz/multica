ALTER TABLE plan_item
    DROP COLUMN IF EXISTS unit_test_checklist;

ALTER TABLE issue
    DROP COLUMN IF EXISTS unit_test_updated_at,
    DROP COLUMN IF EXISTS unit_test_last_task_id,
    DROP COLUMN IF EXISTS unit_test_iteration_count,
    DROP COLUMN IF EXISTS unit_test_status,
    DROP COLUMN IF EXISTS unit_test_checklist;
