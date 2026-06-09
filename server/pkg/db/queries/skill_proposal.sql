-- Skill proposal CRUD

-- name: ListSkillProposalsByWorkspace :many
SELECT * FROM skill_proposal
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC
LIMIT $2;

-- name: GetSkillProposalInWorkspace :one
SELECT * FROM skill_proposal
WHERE id = $1 AND workspace_id = $2;

-- name: CreateSkillProposal :one
INSERT INTO skill_proposal (
    workspace_id,
    project_id,
    source_task_id,
    source_issue_id,
    operation,
    target_skill_id,
    title,
    summary,
    rationale,
    risk_level,
    proposed_name,
    proposed_description,
    proposed_content,
    proposed_files,
    base_content_hash,
    diff,
    evidence_refs,
    edit_ops,
    validation_status,
    validation_score_before,
    validation_score_after,
    rejected_similar_count,
    token_delta,
    gate_reason,
    confidence,
    curator_model,
    curator_prompt_hash,
    created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28
)
RETURNING *;

-- name: ListRecentCompletedTasksForSkillCuration :many
SELECT t.*, i.workspace_id AS issue_workspace_id, i.project_id AS issue_project_id,
       i.title AS issue_title, i.description AS issue_description
FROM agent_task_queue t
JOIN issue i ON i.id = t.issue_id
WHERE t.status IN ('completed', 'failed')
  AND t.issue_id IS NOT NULL
  AND COALESCE(t.completed_at, t.started_at, t.created_at) >= now() - ($1::double precision * interval '1 hour')
ORDER BY COALESCE(t.completed_at, t.started_at, t.created_at) DESC
LIMIT $2;

-- name: CountRejectedSimilarSkillProposals :one
SELECT COUNT(*)::int
FROM skill_proposal
WHERE workspace_id = $1
  AND status = 'rejected'
  AND operation = $2
  AND COALESCE(target_skill_id, '00000000-0000-0000-0000-000000000000'::uuid)
      = COALESCE(sqlc.narg('target_skill_id')::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND md5(diff) = md5(sqlc.arg('diff')::text);

-- name: RejectSkillProposal :one
UPDATE skill_proposal SET
    status = 'rejected',
    reviewed_by = $2,
    rejected_reason = $3,
    reviewed_at = now(),
    updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: MarkSkillProposalApplied :one
UPDATE skill_proposal SET
    status = 'applied',
    reviewed_by = $2,
    applied_skill_id = $3,
    reviewed_at = COALESCE(reviewed_at, now()),
    applied_at = now(),
    updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;
