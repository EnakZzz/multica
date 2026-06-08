-- name: ListPipelines :many
SELECT * FROM pipeline
WHERE workspace_id = $1 AND archived_at IS NULL
ORDER BY is_system DESC, created_at DESC;

-- name: GetPipelineInWorkspace :one
SELECT * FROM pipeline
WHERE id = $1 AND workspace_id = $2;

-- name: GetSystemPipelineByKey :one
SELECT * FROM pipeline
WHERE workspace_id = $1 AND system_key = $2 AND is_system = true;

-- name: CreatePipeline :one
INSERT INTO pipeline (
    workspace_id, name, description, default_project_id, created_by
) VALUES (
    $1, $2, $3, sqlc.narg('default_project_id'), $4
) RETURNING *;

-- name: CreateSystemPipeline :one
INSERT INTO pipeline (
    workspace_id, name, description, default_project_id, created_by, is_system, system_key
) VALUES (
    $1, $2, $3, sqlc.narg('default_project_id'), $4, true, $5
) RETURNING *;

-- name: UpdatePipeline :one
UPDATE pipeline SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    default_project_id = sqlc.narg('default_project_id'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSystemPipelineMetadata :one
UPDATE pipeline SET
    name = $2,
    description = $3,
    archived_at = NULL,
    updated_at = now()
WHERE id = $1 AND is_system = true
RETURNING *;

-- name: ArchivePipeline :one
UPDATE pipeline
SET archived_at = now(), updated_at = now()
WHERE id = $1 AND archived_at IS NULL
RETURNING *;

-- name: ListPipelineRoles :many
SELECT * FROM pipeline_role
WHERE pipeline_id = $1
ORDER BY position ASC, created_at ASC;

-- name: DeletePipelineRoles :exec
DELETE FROM pipeline_role WHERE pipeline_id = $1;

-- name: CreatePipelineRole :one
INSERT INTO pipeline_role (
    pipeline_id, key, name, description, agent_id, required_skill_ids, position
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListPipelineStages :many
SELECT * FROM pipeline_stage
WHERE pipeline_id = $1
ORDER BY position ASC, created_at ASC;

-- name: DeletePipelineStages :exec
DELETE FROM pipeline_stage WHERE pipeline_id = $1;

-- name: CreatePipelineStage :one
INSERT INTO pipeline_stage (
    pipeline_id, key, title, description, role_key, node_type, agent_id, depends_on_stage_keys, position, position_x, position_y, repo_keys, harness_strategy, execution_routing
) VALUES (
    $1, $2, $3, $4, $5, $6, sqlc.narg('agent_id'), $7, $8, $9, $10, $11, sqlc.arg('harness_strategy')::jsonb, sqlc.arg('execution_routing')::jsonb
) RETURNING *;

-- name: CreatePipelineRun :one
INSERT INTO pipeline_run (
    pipeline_id, workspace_id, project_id, parent_issue_id, status, created_by
) VALUES (
    $1, $2, sqlc.narg('project_id'), $3, $4, $5
) RETURNING *;

-- name: CreatePipelineRunStage :one
INSERT INTO pipeline_run_stage (
    pipeline_run_id, pipeline_stage_id, stage_key, issue_id
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: ListPipelineRunStages :many
SELECT * FROM pipeline_run_stage
WHERE pipeline_run_id = $1
ORDER BY created_at ASC;

-- name: GetPipelineRunStageForIssue :one
SELECT
    prs.id,
    prs.pipeline_run_id,
    prs.pipeline_stage_id,
    prs.stage_key,
    prs.issue_id,
    prs.created_at,
    ps.node_type
FROM pipeline_run_stage prs
JOIN pipeline_stage ps ON ps.id = prs.pipeline_stage_id
WHERE prs.issue_id = $1
LIMIT 1;
