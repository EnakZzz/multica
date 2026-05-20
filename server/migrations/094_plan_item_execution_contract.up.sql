ALTER TABLE plan_item
    ADD COLUMN IF NOT EXISTS acceptance_criteria text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS suggested_test_commands text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS context_resources text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS risk_notes text[] NOT NULL DEFAULT '{}';
