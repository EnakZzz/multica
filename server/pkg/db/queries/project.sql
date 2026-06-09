-- name: ListProjects :many
SELECT * FROM project
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR priority = sqlc.narg('priority'))
ORDER BY created_at DESC;

-- name: ListAccessibleProjects :many
SELECT DISTINCT p.* FROM project p
LEFT JOIN project_workspace_link pwl ON pwl.project_id = p.id
WHERE (p.workspace_id = sqlc.arg('workspace_id') OR pwl.workspace_id = sqlc.arg('workspace_id'))
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR p.priority = sqlc.narg('priority'))
ORDER BY p.created_at DESC;

-- name: ListProjectsForUserWorkspaces :many
SELECT DISTINCT p.* FROM project p
JOIN member m ON m.workspace_id = p.workspace_id
WHERE m.user_id = $1
  AND (sqlc.narg('status')::text IS NULL OR p.status = sqlc.narg('status'))
ORDER BY p.updated_at DESC, p.created_at DESC;

-- name: GetProject :one
SELECT * FROM project
WHERE id = $1;

-- name: GetProjectInWorkspace :one
SELECT * FROM project
WHERE id = $1 AND workspace_id = $2;

-- name: GetProjectAccessibleInWorkspace :one
SELECT p.* FROM project p
LEFT JOIN project_workspace_link pwl ON pwl.project_id = p.id
WHERE p.id = $1
  AND (p.workspace_id = $2 OR pwl.workspace_id = $2)
LIMIT 1;

-- name: ListProjectWorkspaceLinks :many
SELECT pwl.workspace_id, w.name, w.slug, pwl.created_at
FROM project_workspace_link pwl
JOIN workspace w ON w.id = pwl.workspace_id
WHERE pwl.project_id = $1
ORDER BY w.name ASC;

-- name: CreateProjectWorkspaceLink :one
INSERT INTO project_workspace_link (project_id, workspace_id)
VALUES ($1, $2)
RETURNING *;

-- name: DeleteProjectWorkspaceLinks :exec
DELETE FROM project_workspace_link
WHERE project_id = $1;

-- name: CreateProject :one
INSERT INTO project (
    workspace_id, title, description, icon, status,
    lead_type, lead_id, priority
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: UpdateProject :one
UPDATE project SET
    title = COALESCE(sqlc.narg('title'), title),
    description = sqlc.narg('description'),
    icon = sqlc.narg('icon'),
    status = COALESCE(sqlc.narg('status'), status),
    priority = COALESCE(sqlc.narg('priority'), priority),
    lead_type = sqlc.narg('lead_type'),
    lead_id = sqlc.narg('lead_id'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
DELETE FROM project WHERE id = $1;

-- name: CountIssuesByProject :one
SELECT count(*) FROM issue
WHERE project_id = $1;

-- name: GetProjectIssueStats :many
SELECT project_id,
       count(*)::bigint AS total_count,
       count(*) FILTER (WHERE status IN ('done', 'cancelled'))::bigint AS done_count
FROM issue
WHERE project_id = ANY(sqlc.arg('project_ids')::uuid[])
GROUP BY project_id;
