ALTER TABLE issue
    ADD COLUMN IF NOT EXISTS unit_test_checklist jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS unit_test_status text NOT NULL DEFAULT 'not_required',
    ADD COLUMN IF NOT EXISTS unit_test_iteration_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unit_test_last_task_id uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS unit_test_updated_at timestamptz;

ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS unit_test_checklist jsonb NOT NULL DEFAULT '[]'::jsonb;
