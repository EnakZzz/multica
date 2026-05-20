-- name: CreatePlan :one
INSERT INTO plan (
    workspace_id, title, prompt, status, planner_agent_id, project_id, created_by
) VALUES (
    $1, $2, $3, 'planning', $4, sqlc.narg('project_id'), $5
) RETURNING *;

-- name: CreatePlanForIssue :one
INSERT INTO plan (
    workspace_id, title, prompt, status, planner_agent_id, project_id,
    parent_title, parent_description, parent_issue_id, created_by
) VALUES (
    $1, $2, $3, 'planning', $4, sqlc.narg('project_id'),
    $5, $6, $7, $8
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

-- name: GetPlan :one
SELECT * FROM plan
WHERE id = $1;

-- name: GetPlanByTask :one
SELECT * FROM plan
WHERE task_id = $1;

-- name: UpdatePlanDraft :one
UPDATE plan
SET
    title = COALESCE(sqlc.narg('title')::text, title),
    parent_title = sqlc.narg('parent_title')::text,
    parent_description = sqlc.narg('parent_description')::text,
    spec = sqlc.arg('spec')::jsonb,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkPlanSpecReview :one
UPDATE plan
SET
    status = 'spec_review',
    title = sqlc.arg('title'),
    spec = sqlc.arg('spec')::jsonb,
    error = NULL,
    spec_approved_at = NULL,
    spec_approved_by = NULL,
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: ApprovePlanSpec :one
UPDATE plan
SET
    status = 'planning',
    task_id = sqlc.arg('task_id'),
    spec = sqlc.arg('spec')::jsonb,
    spec_approved_at = now(),
    spec_approved_by = sqlc.arg('spec_approved_by'),
    error = NULL,
    updated_at = now()
WHERE id = sqlc.arg('id')
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
SET
    status = 'planning',
    task_id = $2,
    error = NULL,
    spec_approved_at = NULL,
    spec_approved_by = NULL,
    updated_at = now()
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
    plan_id, position, title, description,
    acceptance_criteria, suggested_test_commands, context_resources, risk_notes,
    execution_kind, confirmation_question, confirmation_reason, required_evidence,
    requires_git_commit, branch_name,
    node_type,
    recommended_agent_id,
    match_score, match_reason, missing_capability, depends_on_positions, selected
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14,
    $20,
    sqlc.narg('recommended_agent_id'),
    $15, $16, $17, $18, $19
) RETURNING *;

-- name: ListPlanItems :many
SELECT * FROM plan_item
WHERE plan_id = $1
ORDER BY position ASC, created_at ASC;

-- name: GetPlanItemByGeneratedIssue :one
SELECT * FROM plan_item
WHERE generated_issue_id = $1
LIMIT 1;

-- name: UpdatePlanItemGeneratedIssue :one
UPDATE plan_item
SET generated_issue_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;
