ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS execution_kind text NOT NULL DEFAULT 'agent_task',
    ADD COLUMN IF NOT EXISTS confirmation_question text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS confirmation_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS required_evidence text[] NOT NULL DEFAULT '{}';

ALTER TABLE plan_item
    DROP CONSTRAINT IF EXISTS plan_item_execution_kind_check;

ALTER TABLE plan_item
    ADD CONSTRAINT plan_item_execution_kind_check
    CHECK (execution_kind IN ('agent_task', 'human_confirmation'));
