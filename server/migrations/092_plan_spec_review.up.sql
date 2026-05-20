ALTER TABLE plan
DROP CONSTRAINT IF EXISTS plan_status_check;

ALTER TABLE plan
ADD CONSTRAINT plan_status_check
CHECK (status IN ('planning', 'spec_review', 'ready', 'failed', 'committed'));

ALTER TABLE plan
ADD COLUMN IF NOT EXISTS spec JSONB NOT NULL DEFAULT '{}'::jsonb,
ADD COLUMN IF NOT EXISTS spec_approved_at TIMESTAMPTZ,
ADD COLUMN IF NOT EXISTS spec_approved_by UUID REFERENCES "user"(id) ON DELETE SET NULL;
