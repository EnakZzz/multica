package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListIssueDependenciesReturnsBothDirectionsAndEmpty(t *testing.T) {
	prereqID := createTestIssue(t, "Dependency prereq", "todo", "medium")
	targetID := createTestIssue(t, "Dependency target", "todo", "medium")
	downstreamID := createTestIssue(t, "Dependency downstream", "todo", "medium")
	emptyID := createTestIssue(t, "Dependency empty", "todo", "medium")
	t.Cleanup(func() {
		for _, id := range []string{emptyID, downstreamID, targetID, prereqID} {
			deleteTestIssue(t, id)
		}
	})

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by'), ($3, $4, 'blocked_by')
	`, targetID, prereqID, downstreamID, targetID); err != nil {
		t.Fatalf("seed dependencies: %v", err)
	}

	resp := fetchIssueDependencies(t, targetID)
	if len(resp.BlockedBy) != 1 {
		t.Fatalf("expected one blocked_by dependency, got %#v", resp.BlockedBy)
	}
	if resp.BlockedBy[0].IssueID != prereqID || resp.BlockedBy[0].Identifier == "" {
		t.Fatalf("blocked_by summary = %#v, want prereq %s with identifier", resp.BlockedBy[0], prereqID)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("expected one blocks dependency, got %#v", resp.Blocks)
	}
	if resp.Blocks[0].IssueID != downstreamID || resp.Blocks[0].Identifier == "" {
		t.Fatalf("blocks summary = %#v, want downstream %s with identifier", resp.Blocks[0], downstreamID)
	}

	empty := fetchIssueDependencies(t, emptyID)
	if len(empty.BlockedBy) != 0 || len(empty.Blocks) != 0 {
		t.Fatalf("expected empty arrays for issue without dependencies, got %#v", empty)
	}
}

func TestListIssueDependenciesExcludesOtherWorkspaceIssues(t *testing.T) {
	targetID := createTestIssue(t, "Dependency workspace target", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, targetID) })

	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Other Dependency Workspace', 'other-dependency-workspace', '', 'OTH')
		RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	var foreignIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type)
		VALUES ($1, 'Foreign dependency', 'todo', 'medium', $2, 'member')
		RETURNING id
	`, otherWorkspaceID, testUserID).Scan(&foreignIssueID); err != nil {
		t.Fatalf("create foreign issue: %v", err)
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
		VALUES ($1, $2, 'blocked_by')
	`, targetID, foreignIssueID); err != nil {
		t.Fatalf("seed cross-workspace dependency: %v", err)
	}

	resp := fetchIssueDependencies(t, targetID)
	if len(resp.BlockedBy) != 0 || len(resp.Blocks) != 0 {
		t.Fatalf("expected cross-workspace dependency to be hidden, got %#v", resp)
	}
}

type issueDependenciesTestResponse struct {
	BlockedBy []IssueDependencySummary `json:"blocked_by"`
	Blocks    []IssueDependencySummary `json:"blocks"`
}

func fetchIssueDependencies(t *testing.T, issueID string) issueDependenciesTestResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/issues/"+issueID+"/dependencies", nil)
	req = withURLParam(req, "id", issueID)
	testHandler.ListIssueDependencies(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListIssueDependencies: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp issueDependenciesTestResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode ListIssueDependencies response: %v", err)
	}
	if resp.BlockedBy == nil || resp.Blocks == nil {
		t.Fatalf("expected dependency arrays to be present, got %#v", resp)
	}
	return resp
}
