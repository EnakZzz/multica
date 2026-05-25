ALTER TABLE plan
ADD COLUMN IF NOT EXISTS committed_spec JSONB;

UPDATE plan
SET committed_spec = spec
WHERE status = 'committed'
  AND committed_spec IS NULL;
