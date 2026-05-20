UPDATE plan
SET status = 'planning'
WHERE status = 'spec_review';

ALTER TABLE plan
DROP CONSTRAINT IF EXISTS plan_status_check;

ALTER TABLE plan
ADD CONSTRAINT plan_status_check
CHECK (status IN ('planning', 'ready', 'failed', 'committed'));

ALTER TABLE plan
DROP COLUMN IF EXISTS spec_approved_by,
DROP COLUMN IF EXISTS spec_approved_at,
DROP COLUMN IF EXISTS spec;
