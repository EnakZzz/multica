ALTER TABLE plan_item
    DROP COLUMN IF EXISTS required_evidence,
    DROP COLUMN IF EXISTS confirmation_reason,
    DROP COLUMN IF EXISTS confirmation_question,
    DROP COLUMN IF EXISTS execution_kind;
