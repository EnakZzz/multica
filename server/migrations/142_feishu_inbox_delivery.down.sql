DROP INDEX IF EXISTS idx_inbox_item_feishu_delivery_retry;
ALTER TABLE inbox_item DROP CONSTRAINT IF EXISTS inbox_item_feishu_delivery_status_check;
ALTER TABLE inbox_item DROP COLUMN IF EXISTS feishu_delivery_last_error;
ALTER TABLE inbox_item DROP COLUMN IF EXISTS feishu_delivery_attempts;
ALTER TABLE inbox_item DROP COLUMN IF EXISTS feishu_delivered_at;
ALTER TABLE inbox_item DROP COLUMN IF EXISTS feishu_delivery_status;
