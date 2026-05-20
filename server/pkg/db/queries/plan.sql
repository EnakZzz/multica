-- name: CreatePlan :one
INSERT INTO plan (
    workspace_id, title, prompt, status, planner_agent_id, project_id, created_by
) VALUES (
    $1, $2, $3, 'planning', $4, sqlc.narg('project_id'), $5
) RETURNING *;

-- name: SetPlanTask :one
UPDATE plan
SET task_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListPlans :many
SELECT * FROM plan
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetPlanInWorkspace :one
SELECT * FROM plan
WHERE id = $1 AND workspace_id = $2;

-- name: GetPlanByTask :one
SELECT * FROM plan
WHERE task_id = $1;

-- name: UpdatePlanDraft :one
UPDATE plan
SET
    title = COALESCE(sqlc.narg('title'), title),
    parent_title = sqlc.narg('parent_title'),
    parent_description = sqlc.narg('parent_description'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkPlanReady :one
UPDATE plan
SET
    status = 'ready',
    title = $2,
    parent_title = $3,
    parent_description = $4,
    error = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkPlanFailed :one
UPDATE plan
SET status = 'failed', error = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkPlanPlanning :one
UPDATE plan
SET status = 'planning', task_id = $2, error = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkPlanCommitted :one
UPDATE plan
SET status = 'committed', parent_issue_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeletePlanItems :exec
DELETE FROM plan_item WHERE plan_id = $1;

-- name: CreatePlanItem :one
INSERT INTO plan_item (
    plan_id, position, title, description, recommended_agent_id,
    match_score, match_reason, missing_capability, depends_on_positions, selected
) VALUES (
    $1, $2, $3, $4, sqlc.narg('recommended_agent_id'),
    $5, $6, $7, $8, $9
) RETURNING *;

-- name: ListPlanItems :many
SELECT * FROM plan_item
WHERE plan_id = $1
ORDER BY position ASC, created_at ASC;

-- name: UpdatePlanItemGeneratedIssue :one
UPDATE plan_item
SET generated_issue_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;
