package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProjectWorkspaceLinksExposeProjectToTargetWorkspace(t *testing.T) {
	ctx := context.Background()
	targetWorkspaceID := createProjectLinkTestWorkspace(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Shared project fixture",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, project.ID)
	})

	w = httptest.NewRecorder()
	testHandler.ListProjects(w, withWorkspaceID(newRequest("GET", "/api/projects", nil), targetWorkspaceID))
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjects before link: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), project.ID) {
		t.Fatal("target workspace saw project before it was linked")
	}

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+project.ID+"/workspace-links", map[string]any{
		"workspace_ids": []string{targetWorkspaceID},
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProjectWorkspaceLinks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProjectWorkspaceLinks: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.ListProjects(w, withWorkspaceID(newRequest("GET", "/api/projects", nil), targetWorkspaceID))
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjects after link: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), project.ID) {
		t.Fatal("target workspace did not see linked project")
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("GET", "/api/projects/"+project.ID, nil), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.GetProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProject from target workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("POST", "/api/issues", map[string]any{
		"title":      "Issue using linked project",
		"project_id": project.ID,
	}), targetWorkspaceID)
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue with linked project: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectWorkspaceLinksDoNotGrantResourceMutation(t *testing.T) {
	ctx := context.Background()
	targetWorkspaceID := createProjectLinkTestWorkspace(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Shared resource mutation fixture",
		"resources": []map[string]any{
			{
				"resource_type": "git_repo",
				"resource_ref":  map[string]any{"url": "https://github.com/example/shared-resource.git"},
			},
		},
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, project.ID)
	})

	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+project.ID+"/workspace-links", map[string]any{
		"workspace_ids": []string{targetWorkspaceID},
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProjectWorkspaceLinks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProjectWorkspaceLinks: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("GET", "/api/projects/"+project.ID+"/resources", nil), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.ListProjectResources(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjectResources from target workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("POST", "/api/projects/"+project.ID+"/resources", map[string]any{
		"resource_type": "git_repo",
		"resource_ref":  map[string]any{"url": "https://github.com/example/target-mutation.git"},
	}), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.CreateProjectResource(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("CreateProjectResource from target workspace: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectWorkspaceLinksExposeKnowledgeAndVisualBoardToTargetWorkspace(t *testing.T) {
	targetWorkspaceID := createProjectLinkTestWorkspace(t)
	project := createProjectVisualTestProject(t, "Shared knowledge visual fixture")
	createProjectVisualTestWikiPage(t, project.ID, "design/core-loop", "Core Loop", "Shared wiki body")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects/"+project.ID+"/memory", map[string]any{
		"kind":    "decision",
		"title":   "Shared memory",
		"summary": "Memory visible through a project workspace link.",
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.CreateProjectMemoryItem(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProjectMemoryItem: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	createProjectVisualTestNode(t, project.ID, "scene", "Shared visual node", "draft", "")

	linkProjectToWorkspace(t, project.ID, targetWorkspaceID)

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("GET", "/api/projects/"+project.ID+"/wiki/pages", nil), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.ListProjectWikiPages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjectWikiPages from target workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Core Loop") {
		t.Fatalf("linked workspace wiki response missing owner page: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("GET", "/api/projects/"+project.ID+"/memory", nil), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.ListProjectMemoryItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjectMemoryItems from target workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Shared memory") {
		t.Fatalf("linked workspace memory response missing owner item: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceID(newRequest("GET", "/api/projects/"+project.ID+"/visual-board", nil), targetWorkspaceID)
	req = withURLParam(req, "id", project.ID)
	testHandler.GetProjectVisualBoard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectVisualBoard from target workspace: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Shared visual node") {
		t.Fatalf("linked workspace visual board response missing owner node: %s", w.Body.String())
	}
}

func withWorkspaceID(req *http.Request, workspaceID string) *http.Request {
	req.Header.Set("X-Workspace-ID", workspaceID)
	req.Header.Del("X-Workspace-Slug")
	return req
}

func linkProjectToWorkspace(t *testing.T, projectID, workspaceID string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("PUT", "/api/projects/"+projectID+"/workspace-links", map[string]any{
		"workspace_ids": []string{workspaceID},
	})
	req = withURLParam(req, "id", projectID)
	testHandler.UpdateProjectWorkspaceLinks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProjectWorkspaceLinks: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func createProjectLinkTestWorkspace(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	slug := "project-link-" + strings.ReplaceAll(id, "-", "")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workspace (id, name, slug, issue_prefix)
		VALUES ($1, $2, $3, $4)
	`, id, "Project Link Test", slug, "PLT"); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, id, testUserID); err != nil {
		t.Fatalf("create workspace member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, id)
	})
	return id
}
