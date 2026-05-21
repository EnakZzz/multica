package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type IssueUnitTestFields struct {
	IssueID                pgtype.UUID        `json:"issue_id"`
	UnitTestChecklist      []byte             `json:"unit_test_checklist"`
	UnitTestStatus         string             `json:"unit_test_status"`
	UnitTestIterationCount int32              `json:"unit_test_iteration_count"`
	UnitTestLastTaskID     pgtype.UUID        `json:"unit_test_last_task_id"`
	UnitTestUpdatedAt      pgtype.Timestamptz `json:"unit_test_updated_at"`
}

type ListIssueUnitTestFieldsParams struct {
	IssueIds    []pgtype.UUID `json:"issue_ids"`
	WorkspaceID pgtype.UUID   `json:"workspace_id"`
}

func (q *Queries) ListIssueUnitTestFields(ctx context.Context, arg ListIssueUnitTestFieldsParams) ([]IssueUnitTestFields, error) {
	rows, err := q.db.Query(ctx, `
SELECT id, unit_test_checklist, unit_test_status, unit_test_iteration_count, unit_test_last_task_id, unit_test_updated_at
FROM issue
WHERE workspace_id = $1
  AND id = ANY($2::uuid[])
`, arg.WorkspaceID, arg.IssueIds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []IssueUnitTestFields{}
	for rows.Next() {
		var i IssueUnitTestFields
		if err := rows.Scan(
			&i.IssueID,
			&i.UnitTestChecklist,
			&i.UnitTestStatus,
			&i.UnitTestIterationCount,
			&i.UnitTestLastTaskID,
			&i.UnitTestUpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (q *Queries) GetIssueUnitTestFields(ctx context.Context, issueID pgtype.UUID) (IssueUnitTestFields, error) {
	row := q.db.QueryRow(ctx, `
SELECT id, unit_test_checklist, unit_test_status, unit_test_iteration_count, unit_test_last_task_id, unit_test_updated_at
FROM issue
WHERE id = $1
`, issueID)
	var i IssueUnitTestFields
	err := row.Scan(
		&i.IssueID,
		&i.UnitTestChecklist,
		&i.UnitTestStatus,
		&i.UnitTestIterationCount,
		&i.UnitTestLastTaskID,
		&i.UnitTestUpdatedAt,
	)
	return i, err
}

type UpdateIssueUnitTestFieldsParams struct {
	ID                     pgtype.UUID `json:"id"`
	UnitTestChecklist      []byte      `json:"unit_test_checklist"`
	UnitTestStatus         string      `json:"unit_test_status"`
	UnitTestIterationCount int32       `json:"unit_test_iteration_count"`
	UnitTestLastTaskID     pgtype.UUID `json:"unit_test_last_task_id"`
}

func (q *Queries) UpdateIssueUnitTestFields(ctx context.Context, arg UpdateIssueUnitTestFieldsParams) (IssueUnitTestFields, error) {
	row := q.db.QueryRow(ctx, `
UPDATE issue SET
    unit_test_checklist = $2::jsonb,
    unit_test_status = $3,
    unit_test_iteration_count = $4,
    unit_test_last_task_id = $5,
    unit_test_updated_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING id, unit_test_checklist, unit_test_status, unit_test_iteration_count, unit_test_last_task_id, unit_test_updated_at
`, arg.ID, arg.UnitTestChecklist, arg.UnitTestStatus, arg.UnitTestIterationCount, arg.UnitTestLastTaskID)
	var i IssueUnitTestFields
	err := row.Scan(
		&i.IssueID,
		&i.UnitTestChecklist,
		&i.UnitTestStatus,
		&i.UnitTestIterationCount,
		&i.UnitTestLastTaskID,
		&i.UnitTestUpdatedAt,
	)
	return i, err
}

type CreateIssueWithOriginAndUnitTestsManualParams struct {
	WorkspaceID       pgtype.UUID        `json:"workspace_id"`
	Title             string             `json:"title"`
	Description       pgtype.Text        `json:"description"`
	Status            string             `json:"status"`
	Priority          string             `json:"priority"`
	AssigneeType      pgtype.Text        `json:"assignee_type"`
	AssigneeID        pgtype.UUID        `json:"assignee_id"`
	CreatorType       string             `json:"creator_type"`
	CreatorID         pgtype.UUID        `json:"creator_id"`
	ParentIssueID     pgtype.UUID        `json:"parent_issue_id"`
	Position          float64            `json:"position"`
	StartDate         pgtype.Timestamptz `json:"start_date"`
	DueDate           pgtype.Timestamptz `json:"due_date"`
	Number            int32              `json:"number"`
	ProjectID         pgtype.UUID        `json:"project_id"`
	OriginType        pgtype.Text        `json:"origin_type"`
	OriginID          pgtype.UUID        `json:"origin_id"`
	UnitTestChecklist []byte             `json:"unit_test_checklist"`
}

func (q *Queries) CreateIssueWithOriginAndUnitTestsManual(ctx context.Context, arg CreateIssueWithOriginAndUnitTestsManualParams) (Issue, error) {
	row := q.db.QueryRow(ctx, `
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    assignee_type, assignee_id, creator_type, creator_id,
    parent_issue_id, position, start_date, due_date, number, project_id,
    origin_type, origin_id, unit_test_checklist, unit_test_status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
    $16, $17, $18::jsonb,
    CASE WHEN $18::jsonb = '[]'::jsonb THEN 'not_required' ELSE 'pending' END
) RETURNING id, workspace_id, title, description, status, priority, assignee_type, assignee_id, creator_type, creator_id, parent_issue_id, acceptance_criteria, context_refs, position, due_date, created_at, updated_at, number, project_id, origin_type, origin_id, first_executed_at, start_date
`,
		arg.WorkspaceID,
		arg.Title,
		arg.Description,
		arg.Status,
		arg.Priority,
		arg.AssigneeType,
		arg.AssigneeID,
		arg.CreatorType,
		arg.CreatorID,
		arg.ParentIssueID,
		arg.Position,
		arg.StartDate,
		arg.DueDate,
		arg.Number,
		arg.ProjectID,
		arg.OriginType,
		arg.OriginID,
		arg.UnitTestChecklist,
	)
	var i Issue
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.Title,
		&i.Description,
		&i.Status,
		&i.Priority,
		&i.AssigneeType,
		&i.AssigneeID,
		&i.CreatorType,
		&i.CreatorID,
		&i.ParentIssueID,
		&i.AcceptanceCriteria,
		&i.ContextRefs,
		&i.Position,
		&i.DueDate,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.Number,
		&i.ProjectID,
		&i.OriginType,
		&i.OriginID,
		&i.FirstExecutedAt,
		&i.StartDate,
	)
	return i, err
}
