ALTER TABLE plan_item
    DROP COLUMN IF EXISTS risk_notes,
    DROP COLUMN IF EXISTS context_resources,
    DROP COLUMN IF EXISTS suggested_test_commands,
    DROP COLUMN IF EXISTS acceptance_criteria;
