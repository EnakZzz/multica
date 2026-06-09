-- Review item unified human-in-the-loop queue

-- name: ListReviewItemsByWorkspace :many
SELECT * FROM review_item
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type')::text)
ORDER BY
  CASE WHEN status = 'pending' THEN 0 ELSE 1 END,
  created_at DESC
LIMIT $2;

-- name: GetReviewItemInWorkspace :one
SELECT * FROM review_item
WHERE id = $1 AND workspace_id = $2;

-- name: CreateReviewItem :one
INSERT INTO review_item (
    workspace_id,
    type,
    risk_level,
    title,
    summary,
    source_actor_type,
    source_actor_id,
    source_object_type,
    source_object_id,
    target_object_type,
    target_object_id,
    payload,
    diff,
    available_actions
) VALUES (
    $1, $2, $3, $4, $5,
    sqlc.narg('source_actor_type'),
    sqlc.narg('source_actor_id'),
    $6,
    sqlc.narg('source_object_id'),
    $7,
    sqlc.narg('target_object_id'),
    sqlc.arg('payload')::jsonb,
    $8,
    $9
)
RETURNING *;

-- name: UpsertPendingReviewItemBySource :one
INSERT INTO review_item (
    workspace_id,
    type,
    risk_level,
    title,
    summary,
    source_actor_type,
    source_actor_id,
    source_object_type,
    source_object_id,
    target_object_type,
    target_object_id,
    payload,
    diff,
    available_actions
) VALUES (
    $1, $2, $3, $4, $5,
    sqlc.narg('source_actor_type'),
    sqlc.narg('source_actor_id'),
    $6,
    sqlc.narg('source_object_id'),
    $7,
    sqlc.narg('target_object_id'),
    sqlc.arg('payload')::jsonb,
    $8,
    $9
)
ON CONFLICT (workspace_id, type, source_object_type, source_object_id)
WHERE status = 'pending' AND source_object_id IS NOT NULL
DO UPDATE SET
    risk_level = EXCLUDED.risk_level,
    title = EXCLUDED.title,
    summary = EXCLUDED.summary,
    source_actor_type = EXCLUDED.source_actor_type,
    source_actor_id = EXCLUDED.source_actor_id,
    target_object_type = EXCLUDED.target_object_type,
    target_object_id = EXCLUDED.target_object_id,
    payload = EXCLUDED.payload,
    diff = EXCLUDED.diff,
    available_actions = EXCLUDED.available_actions,
    updated_at = now()
RETURNING *;

-- name: MarkReviewItemReviewed :one
UPDATE review_item
SET
    status = $3,
    reviewer_id = $4,
    review_note = $5,
    reviewed_at = now(),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'pending'
RETURNING *;
