ALTER TABLE inbox_item
  ADD COLUMN IF NOT EXISTS feishu_delivery_status TEXT NOT NULL DEFAULT 'not_applicable',
  ADD COLUMN IF NOT EXISTS feishu_delivered_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS feishu_delivery_attempts INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS feishu_delivery_last_error TEXT;

ALTER TABLE inbox_item
  DROP CONSTRAINT IF EXISTS inbox_item_feishu_delivery_status_check;

ALTER TABLE inbox_item
  ADD CONSTRAINT inbox_item_feishu_delivery_status_check
  CHECK (feishu_delivery_status IN ('not_applicable', 'pending', 'sent', 'failed'));

CREATE INDEX IF NOT EXISTS idx_inbox_item_feishu_delivery_retry
  ON inbox_item(feishu_delivery_status, feishu_delivery_attempts, created_at)
  WHERE issue_id IS NOT NULL AND archived = false;
