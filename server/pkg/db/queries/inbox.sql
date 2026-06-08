-- name: ListInboxItems :many
SELECT i.*,
       iss.status as issue_status
FROM inbox_item i
LEFT JOIN issue iss ON iss.id = i.issue_id
WHERE i.workspace_id = $1 AND i.recipient_type = $2 AND i.recipient_id = $3 AND i.archived = false
  AND (
    i.issue_id IS NULL
    OR i.feishu_delivery_status IN ('not_applicable', 'failed')
  )
ORDER BY i.created_at DESC;

-- name: GetInboxItem :one
SELECT * FROM inbox_item
WHERE id = $1;

-- name: GetInboxItemInWorkspace :one
SELECT * FROM inbox_item
WHERE id = $1 AND workspace_id = $2;

-- name: CreateInboxItem :one
INSERT INTO inbox_item (
    workspace_id, recipient_type, recipient_id,
    type, severity, issue_id, title, body,
    actor_type, actor_id, details, feishu_delivery_status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: MarkInboxFeishuPending :exec
UPDATE inbox_item
SET feishu_delivery_status = 'pending',
    feishu_delivery_attempts = feishu_delivery_attempts + 1,
    feishu_delivery_last_error = NULL
WHERE id = $1;

-- name: MarkInboxFeishuSent :exec
UPDATE inbox_item
SET feishu_delivery_status = 'sent',
    feishu_delivered_at = now(),
    feishu_delivery_last_error = NULL
WHERE id = $1;

-- name: MarkInboxFeishuFailed :exec
UPDATE inbox_item
SET feishu_delivery_status = 'failed',
    feishu_delivery_last_error = $2
WHERE id = $1;

-- name: MarkInboxRead :one
UPDATE inbox_item SET read = true
WHERE id = $1
RETURNING *;

-- name: ArchiveInboxItem :one
UPDATE inbox_item SET archived = true
WHERE id = $1
RETURNING *;

-- name: ArchiveInboxByIssue :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = $2 AND recipient_id = $3 AND issue_id = $4 AND archived = false;

-- name: ArchiveInboxByIssueAndType :many
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND issue_id = $2 AND type = $3 AND archived = false
RETURNING recipient_type, recipient_id;

-- name: CountUnreadInbox :one
SELECT count(*) FROM inbox_item
WHERE workspace_id = $1 AND recipient_type = $2 AND recipient_id = $3 AND read = false AND archived = false;

-- name: MarkAllInboxRead :execrows
UPDATE inbox_item SET read = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND archived = false AND read = false;

-- name: ArchiveAllInbox :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND archived = false;

-- name: ArchiveAllReadInbox :execrows
UPDATE inbox_item SET archived = true
WHERE workspace_id = $1 AND recipient_type = 'member' AND recipient_id = $2 AND read = true AND archived = false;

-- name: ArchiveCompletedInbox :execrows
UPDATE inbox_item i SET archived = true
WHERE i.workspace_id = $1 AND i.recipient_type = 'member' AND i.recipient_id = $2 AND i.archived = false
  AND i.issue_id IN (SELECT id FROM issue WHERE status IN ('done', 'cancelled'));
