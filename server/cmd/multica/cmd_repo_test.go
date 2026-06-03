package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestValidateAgentPublishBranch(t *testing.T) {
	t.Parallel()

	if err := validateAgentPublishBranch("agent/backend-engineer/LOC-18-b4fa2df8"); err != nil {
		t.Fatalf("expected agent branch to be allowed, got %v", err)
	}
	if err := validateAgentPublishBranch("feature/plan-branch-contract"); err != nil {
		t.Fatalf("expected feature branch to be allowed, got %v", err)
	}

	for _, branch := range []string{"main", "master"} {
		t.Run(branch, func(t *testing.T) {
			err := validateAgentPublishBranch(branch)
			if err == nil {
				t.Fatal("expected protected branch to be rejected")
			}
			if !strings.Contains(err.Error(), "generated work branches") {
				t.Fatalf("expected readable branch-only error, got %v", err)
			}
			if !strings.Contains(err.Error(), "protected") {
				t.Fatalf("expected protected-branch wording, got %v", err)
			}
		})
	}

	err := validateAgentPublishBranch("mainline/demo")
	if err == nil {
		t.Fatal("expected non-work branch to be rejected")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected non-agent wording, got %v", err)
	}
}

func TestValidateAgentPublishBranchDetachedHead(t *testing.T) {
	t.Parallel()

	err := validateAgentPublishBranch("")
	if err == nil {
		t.Fatal("expected detached HEAD to be rejected")
	}
	if !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("expected detached HEAD wording, got %v", err)
	}
}

func TestResolveRepoPublishTargetUsesPlannedBranch(t *testing.T) {
	t.Parallel()

	branch, ref, err := resolveRepoPublishTarget("agent/backend/LOC-1-deadbeef", "feature/lost-pet-app-shell")
	if err != nil {
		t.Fatalf("resolveRepoPublishTarget(): %v", err)
	}
	if branch != "feature/lost-pet-app-shell" {
		t.Fatalf("branch = %q, want planned branch", branch)
	}
	if ref != "HEAD:refs/heads/feature/lost-pet-app-shell" {
		t.Fatalf("ref = %q, want HEAD push to planned branch", ref)
	}
}

func TestResolveRepoPublishTargetAllowsDetachedHeadWithPlannedBranch(t *testing.T) {
	t.Parallel()

	branch, ref, err := resolveRepoPublishTarget("", "feature/lost-pet-app-shell")
	if err != nil {
		t.Fatalf("resolveRepoPublishTarget(): %v", err)
	}
	if branch != "feature/lost-pet-app-shell" || ref != "HEAD:refs/heads/feature/lost-pet-app-shell" {
		t.Fatalf("target = (%q, %q), want planned branch HEAD ref", branch, ref)
	}
}

func TestParseGitRemoteURLSupportsSelfManagedGitLabForms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		host string
		path string
	}{
		{"git@sc-sh.happyelements.net:group/sub/repo.git", "sc-sh.happyelements.net", "group/sub/repo"},
		{"https://sc-sh.happyelements.net/group/sub/repo.git", "sc-sh.happyelements.net", "group/sub/repo"},
		{"ssh://git@sc-sh.happyelements.net:2222/group/sub/repo.git", "sc-sh.happyelements.net", "group/sub/repo"},
	}
	for _, tt := range cases {
		t.Run(tt.raw, func(t *testing.T) {
			got, ok := parseGitRemoteURL(tt.raw)
			if !ok {
				t.Fatalf("parseGitRemoteURL(%q) failed", tt.raw)
			}
			if got.Host != tt.host || got.Path != tt.path {
				t.Fatalf("parseGitRemoteURL(%q) = %#v, want host=%q path=%q", tt.raw, got, tt.host, tt.path)
			}
		})
	}
}

func TestShouldUseGitLabMergeRequestDetectsCompanyGitLab(t *testing.T) {
	t.Parallel()

	if !shouldUseGitLabMergeRequest(gitRemoteURL{Host: "sc-sh.happyelements.net", Path: "group/sub/repo"}) {
		t.Fatal("expected company GitLab host to use GitLab merge requests")
	}
	if shouldUseGitLabMergeRequest(gitRemoteURL{Host: "github.com", Path: "group/repo"}) {
		t.Fatal("GitHub host must not use GitLab merge requests")
	}
}

func TestGitLabMergeRequestCreatesMR(t *testing.T) {
	var sawCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "token-1" {
			t.Fatalf("PRIVATE-TOKEN header = %q", got)
		}
		escapedPath := r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(escapedPath, "/api/v4/projects/group%2Fsub%2Frepo/merge_requests"):
			if got := r.URL.Query().Get("source_branch"); got != "feature/work" {
				t.Fatalf("source_branch = %q", got)
			}
			if got := r.URL.Query().Get("target_branch"); got != "main" {
				t.Fatalf("target_branch = %q", got)
			}
			w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && escapedPath == "/api/v4/projects/group%2Fsub%2Frepo/merge_requests":
			sawCreate = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["source_branch"] != "feature/work" || body["target_branch"] != "main" {
				t.Fatalf("unexpected create body: %#v", body)
			}
			if !strings.Contains(body["description"], "go test ./... passed") {
				t.Fatalf("description missing test result: %#v", body)
			}
			w.Write([]byte(`{"web_url":"https://sc-sh.happyelements.net/group/sub/repo/-/merge_requests/7"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	t.Setenv("GITLAB_BASE_URL", srv.URL)
	t.Setenv("GITLAB_TOKEN", "token-1")

	remote := gitRemoteURL{Host: hostFromURL(srv.URL), Path: "group/sub/repo"}
	got := integrateRepoGitLabMR(repoIntegrateResult{
		SourceBranch: "feature/work",
		TargetBranch: "main",
		Mode:         "pr-first",
		TestResult:   "go test ./... passed",
		Status:       "failed",
	}, remote)

	if got.Status != "pr_created" || got.PRURL != "https://sc-sh.happyelements.net/group/sub/repo/-/merge_requests/7" {
		t.Fatalf("integrateRepoGitLabMR() = %#v", got)
	}
	if !sawCreate {
		t.Fatalf("expected merge request create call")
	}
}

func TestDefaultRepoIntegrateTargetIgnoresLegacyTargetEnv(t *testing.T) {
	t.Setenv("MULTICA_TARGET_BRANCH", "feature/old-target")
	t.Setenv("MULTICA_DEFAULT_BRANCH_HINT", "develop")

	if got := defaultRepoIntegrateTarget(""); got != "develop" {
		t.Fatalf("defaultRepoIntegrateTarget() = %q, want develop", got)
	}
}

func TestIntegrateRepoRejectsNonDefaultTargetWithoutAuthorization(t *testing.T) {
	t.Setenv("MULTICA_DEFAULT_BRANCH_HINT", "main")

	got := integrateRepo("", repoIntegrateOptions{
		SourceBranch:   "feature/work",
		TargetBranch:   "feature/app-shell",
		TargetExplicit: true,
		Strategy:       "pr-first",
	})

	if got.Status != "failed" {
		t.Fatalf("Status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "not the project default branch") {
		t.Fatalf("Error = %q, want non-default target guard", got.Error)
	}
}

func TestIntegrateRepoMergeNodeRejectsFeatureToFeatureTargetWithoutAuthorization(t *testing.T) {
	t.Setenv("MULTICA_DEFAULT_BRANCH_HINT", "main")

	got := integrateRepo("", repoIntegrateOptions{
		SourceBranch:   "feature/work",
		TargetBranch:   "feature/app-shell",
		TargetExplicit: true,
		NodeType:       "merge",
		Strategy:       "pr-first",
	})

	if got.Status != "failed" {
		t.Fatalf("Status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "cannot integrate iteration branch") {
		t.Fatalf("Error = %q, want merge node feature target guard", got.Error)
	}
}

func TestIntegrateRepoAllowsExplicitNonDefaultTargetWithAuthorization(t *testing.T) {
	t.Setenv("MULTICA_DEFAULT_BRANCH_HINT", "main")

	got := integrateRepo("", repoIntegrateOptions{
		SourceBranch:          "feature/work",
		TargetBranch:          "feature/app-shell",
		TargetExplicit:        true,
		AllowNonDefaultTarget: true,
		NodeType:              "merge",
		Strategy:              "unknown",
	})

	if got.Error != "strategy must be pr-first or direct" {
		t.Fatalf("Error = %q, want strategy validation after target authorization", got.Error)
	}
}

func TestSyncRepoContextWritesSkillsAndPipelines(t *testing.T) {
	const (
		agentID        = "agent-123"
		skillID        = "019e20dd-33be-70a2-9657-67c679efc905"
		genericSkillID = "019e20dd-33be-70a2-9657-67c679efc906"
		pipelineID     = "019e2205-a0b9-74a2-9faf-8f2cd77b7627"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agents/" + agentID + "/skills":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": skillID, "name": "Review Helper"},
				{"id": genericSkillID, "name": "Generic Worker Skill"},
			})
		case "/api/skills/" + skillID:
			json.NewEncoder(w).Encode(map[string]any{
				"id":      skillID,
				"name":    "Review Helper",
				"content": "# Review Helper\n",
				"config":  map[string]any{"repo_context": true},
				"files": []map[string]any{
					{"path": "docs/example.md", "content": "example"},
				},
			})
		case "/api/skills/" + genericSkillID:
			json.NewEncoder(w).Encode(map[string]any{
				"id":      genericSkillID,
				"name":    "Generic Worker Skill",
				"content": "# Generic Worker Skill\n",
				"config":  map[string]any{"scope": "agent"},
			})
		case "/api/pipelines/" + pipelineID:
			json.NewEncoder(w).Encode(map[string]any{
				"id":          pipelineID,
				"name":        "Release Flow",
				"description": "Ship a local-testable release.",
				"nodes": []map[string]any{
					{
						"key":                  "design",
						"type":                 "issue",
						"title":                "Design",
						"description":          "Plan the change.",
						"agent_id":             skillID,
						"depends_on_node_keys": []string{},
						"position_x":           120,
						"position_y":           80,
					},
					{
						"key":                  "implement",
						"type":                 "issue",
						"title":                "Implement",
						"description":          "Write the code.",
						"agent_id":             skillID,
						"depends_on_node_keys": []string{"design"},
						"position_x":           420,
						"position_y":           80,
					},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := syncRepoContext(context.Background(), client, root, repoContextSyncOptions{
		BaseDir:     ".multica",
		AgentID:     agentID,
		PipelineIDs: []string{pipelineID},
	})
	if err != nil {
		t.Fatalf("syncRepoContext(): %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("syncRepoContext() wrote %d items, want 3", len(result.Items))
	}

	skillPath := filepath.Join(root, ".multica", "skills", "review-helper-019e20dd", "SKILL.md")
	skillBody, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read synced skill: %v", err)
	}
	if got, want := string(skillBody), "# Review Helper\n"; got != want {
		t.Fatalf("synced skill = %q, want %q", got, want)
	}
	extraPath := filepath.Join(root, ".multica", "skills", "review-helper-019e20dd", "docs", "example.md")
	extraBody, err := os.ReadFile(extraPath)
	if err != nil {
		t.Fatalf("read synced skill file: %v", err)
	}
	if got, want := string(extraBody), "example"; got != want {
		t.Fatalf("synced skill file = %q, want %q", got, want)
	}
	genericPath := filepath.Join(root, ".multica", "skills", "generic-worker-skill-019e20dd", "SKILL.md")
	if _, err := os.Stat(genericPath); !os.IsNotExist(err) {
		t.Fatalf("generic agent skill should not be synced by default, stat err = %v", err)
	}

	pipelinePath := filepath.Join(root, ".multica", "pipelines", "release-flow-019e2205.yaml")
	pipelineBody, err := os.ReadFile(pipelinePath)
	if err != nil {
		t.Fatalf("read synced pipeline: %v", err)
	}
	pipelineYAML := string(pipelineBody)
	for _, want := range []string{
		"version: 1",
		"name: Release Flow",
		"key: design",
		"key: implement",
		"depends_on:",
		"- design",
	} {
		if !strings.Contains(pipelineYAML, want) {
			t.Fatalf("synced pipeline YAML missing %q:\n%s", want, pipelineYAML)
		}
	}
	manifestPath := filepath.Join(root, ".multica", "project.yaml")
	manifestBody, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read project manifest: %v", err)
	}
	if !strings.Contains(string(manifestBody), "path: pipelines/release-flow-019e2205.yaml") {
		t.Fatalf("project manifest missing synced pipeline path:\n%s", string(manifestBody))
	}
}

func TestSyncRepoContextWritesExplicitSkillID(t *testing.T) {
	const skillID = "019e20dd-33be-70a2-9657-67c679efc907"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/skills/" + skillID:
			json.NewEncoder(w).Encode(map[string]any{
				"id":      skillID,
				"name":    "Local Project Skill",
				"content": "# Local Project Skill\n",
				"config":  map[string]any{"scope": "agent"},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := syncRepoContext(context.Background(), client, root, repoContextSyncOptions{
		BaseDir:  ".multica",
		SkillIDs: []string{skillID},
	})
	if err != nil {
		t.Fatalf("syncRepoContext(): %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("syncRepoContext() wrote %d items, want 1", len(result.Items))
	}

	skillPath := filepath.Join(root, ".multica", "skills", "local-project-skill-019e20dd", "SKILL.md")
	skillBody, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read explicit synced skill: %v", err)
	}
	if got, want := string(skillBody), "# Local Project Skill\n"; got != want {
		t.Fatalf("explicit synced skill = %q, want %q", got, want)
	}
}

func TestSyncRepoContextWritesProjectManifestResources(t *testing.T) {
	const (
		projectID = "project-123"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects/" + projectID + "/resources":
			json.NewEncoder(w).Encode(map[string]any{
				"resources": []map[string]any{
					{
						"id":            "resource-1",
						"resource_type": "git_repo",
						"resource_ref":  map[string]any{"url": "https://github.com/acme/project.git", "default_branch_hint": "main"},
						"label":         "app",
					},
				},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	initRepoContextGit(t, root, "feature/context", "https://github.com/acme/project.git")
	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := syncRepoContext(context.Background(), client, root, repoContextSyncOptions{
		BaseDir:   ".multica",
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("syncRepoContext(): %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("syncRepoContext() wrote %d items, want 1", len(result.Items))
	}
	manifestBody, err := os.ReadFile(filepath.Join(root, ".multica", "project.yaml"))
	if err != nil {
		t.Fatalf("read project manifest: %v", err)
	}
	if !strings.Contains(string(manifestBody), "app:") || !strings.Contains(string(manifestBody), "https://github.com/acme/project.git") {
		t.Fatalf("project manifest missing repo resources:\n%s", string(manifestBody))
	}
	for _, want := range []string{
		"multica:",
		"source:",
		"repo: app",
		"branch: main",
		"path: .multica",
	} {
		if !strings.Contains(string(manifestBody), want) {
			t.Fatalf("project manifest missing source field %q:\n%s", want, string(manifestBody))
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".multica", "pipelines")); !os.IsNotExist(err) {
		t.Fatalf("project resource sync should not infer pipelines, stat err = %v", err)
	}
}

func TestImportRepoContextImportsPipelineYAML(t *testing.T) {
	const (
		projectID  = "project-123"
		pipelineID = "019e2205-a0b9-74a2-9faf-8f2cd77b7630"
	)
	var importedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipelines":
			json.NewEncoder(w).Encode(map[string]any{"pipelines": []map[string]any{}})
		case "/api/pipelines/import":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode import payload: %v", err)
			}
			if _, ok := payload["pipeline_id"]; ok {
				t.Fatalf("new repo pipeline should not include pipeline_id: %#v", payload)
			}
			importedContent, _ = payload["content"].(string)
			json.NewEncoder(w).Encode(map[string]any{
				"id":    pipelineID,
				"name":  "Repo Flow",
				"nodes": []map[string]any{},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	pipelineDir := filepath.Join(root, ".multica", "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelineDir, "repo-flow.yaml"), []byte("version: 1\nname: Repo Flow\nnodes:\n  - key: build\n    title: Build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{
		BaseDir:   ".multica",
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("importRepoContext(): %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != pipelineID {
		t.Fatalf("importRepoContext() items = %#v, want imported pipeline", result.Items)
	}
	if strings.Contains(importedContent, "default_project_id") {
		t.Fatalf("imported YAML should not receive a pipeline project id:\n%s", importedContent)
	}
}

func TestImportRepoContextAppliesManifestRoleBindingsAndAllowsUnboundRoles(t *testing.T) {
	const (
		projectID  = "project-123"
		pipelineID = "019e2205-a0b9-74a2-9faf-8f2cd77b7632"
	)
	var importedContent string
	sawAutopilot := false
	var createdResource map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipelines":
			json.NewEncoder(w).Encode(map[string]any{"pipelines": []map[string]any{}})
		case "/api/projects/" + projectID + "/resources":
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(map[string]any{"resources": []map[string]any{}})
				return
			}
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected resources method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&createdResource); err != nil {
				t.Fatalf("decode resource payload: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "resource-1"})
		case "/api/pipelines/import":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode import payload: %v", err)
			}
			importedContent, _ = payload["content"].(string)
			json.NewEncoder(w).Encode(map[string]any{
				"id":    pipelineID,
				"name":  "Manifest Flow",
				"nodes": []map[string]any{},
			})
		case "/api/autopilots":
			sawAutopilot = true
			http.Error(w, "automation should be pending while role is unbound", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	baseDir := filepath.Join(root, ".multica")
	pipelineDir := filepath.Join(baseDir, "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
resources:
  repos:
    app:
      url: https://github.com/acme/app.git
roles:
  - key: developer
    name: Developer
  - key: reviewer
    name: Reviewer
pipelines:
  - path: pipelines/manifest-flow.yaml
    role_bindings:
      build: developer
      review: reviewer
automations:
  - name: Manifest schedule
    pipeline: Manifest Flow
    assignee_role: reviewer
    cron: "0 9 * * 1"
`
	if err := os.WriteFile(filepath.Join(baseDir, "project.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline := `version: 1
name: Manifest Flow
nodes:
  - key: build
    title: Build
  - key: review
    title: Review
    agent_id: stale-reviewer
`
	if err := os.WriteFile(filepath.Join(pipelineDir, "manifest-flow.yaml"), []byte(pipeline), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{
		BaseDir:      ".multica",
		ProjectID:    projectID,
		RoleBindings: map[string]string{"developer": "agent-dev"},
	})
	if err != nil {
		t.Fatalf("importRepoContext(): %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("importRepoContext() items = %#v, want resource + pipeline + pending automation", result.Items)
	}
	if result.Items[2].Type != "automation_pending" {
		t.Fatalf("third item = %#v, want pending automation", result.Items[2])
	}
	if sawAutopilot {
		t.Fatal("unbound automation should not call autopilot API")
	}
	if createdResource["label"] != "app" {
		t.Fatalf("manifest repo resource was not created with alias label: %#v", createdResource)
	}
	if !strings.Contains(importedContent, "agent_id: agent-dev") {
		t.Fatalf("bound role did not set agent_id:\n%s", importedContent)
	}
	if strings.Contains(importedContent, "stale-reviewer") {
		t.Fatalf("unbound role should clear stale agent_id:\n%s", importedContent)
	}
	if strings.Contains(importedContent, "default_project_id") {
		t.Fatalf("imported YAML should not receive a pipeline project id:\n%s", importedContent)
	}
}

func TestImportRepoContextCreatesManifestAutomationWhenRoleIsBound(t *testing.T) {
	const (
		projectID   = "project-123"
		pipelineID  = "019e2205-a0b9-74a2-9faf-8f2cd77b7633"
		autopilotID = "019e2205-a0b9-74a2-9faf-8f2cd77b7634"
		agentID     = "019e2205-a0b9-74a2-9faf-8f2cd77b7635"
	)
	var autopilotBody map[string]any
	var statusPatch map[string]any
	var triggerBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipelines":
			json.NewEncoder(w).Encode(map[string]any{"pipelines": []map[string]any{}})
		case "/api/pipelines/import":
			json.NewEncoder(w).Encode(map[string]any{
				"id":    pipelineID,
				"name":  "Scheduled Flow",
				"nodes": []map[string]any{},
			})
		case "/api/autopilots":
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(map[string]any{"autopilots": []map[string]any{}})
				return
			}
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected /api/autopilots method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&autopilotBody); err != nil {
				t.Fatalf("decode autopilot body: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id":             autopilotID,
				"title":          "Scheduled Flow watch",
				"assignee_id":    agentID,
				"status":         "active",
				"execution_mode": "run_only",
			})
		case "/api/autopilots/" + autopilotID:
			if r.Method != http.MethodPatch {
				t.Fatalf("unexpected autopilot update method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&statusPatch); err != nil {
				t.Fatalf("decode status patch: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id":             autopilotID,
				"title":          "Scheduled Flow watch",
				"assignee_id":    agentID,
				"status":         "paused",
				"execution_mode": "run_only",
			})
		case "/api/autopilots/" + autopilotID + "/triggers":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected trigger method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&triggerBody); err != nil {
				t.Fatalf("decode trigger body: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "trigger-1"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	baseDir := filepath.Join(root, ".multica")
	pipelineDir := filepath.Join(baseDir, "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
roles:
  - key: upstream
    name: Upstream Analyst
pipelines:
  - path: pipelines/scheduled-flow.yaml
automations:
  - name: Scheduled Flow watch
    pipeline: Scheduled Flow
    assignee_role: upstream
    status: paused
    cron: "0 9 * * 1"
    timezone: Asia/Shanghai
    prompt: Run the scheduled flow.
`
	if err := os.WriteFile(filepath.Join(baseDir, "project.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelineDir, "scheduled-flow.yaml"), []byte("version: 1\nname: Scheduled Flow\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{
		BaseDir:      ".multica",
		ProjectID:    projectID,
		RoleBindings: map[string]string{"upstream": agentID},
	})
	if err != nil {
		t.Fatalf("importRepoContext(): %v", err)
	}
	if len(result.Items) != 2 || result.Items[1].Type != "automation" {
		t.Fatalf("importRepoContext() items = %#v, want pipeline + automation", result.Items)
	}
	if autopilotBody["assignee_id"] != agentID {
		t.Fatalf("assignee_id = %#v, want %s", autopilotBody["assignee_id"], agentID)
	}
	if autopilotBody["execution_mode"] != "run_only" {
		t.Fatalf("execution_mode = %#v, want run_only", autopilotBody["execution_mode"])
	}
	if autopilotBody["project_id"] != projectID {
		t.Fatalf("project_id = %#v, want %s", autopilotBody["project_id"], projectID)
	}
	description, _ := autopilotBody["description"].(string)
	if !strings.Contains(description, pipelineID) || !strings.Contains(description, "Run the scheduled flow.") {
		t.Fatalf("automation description did not include prompt and pipeline id:\n%s", description)
	}
	if statusPatch["status"] != "paused" {
		t.Fatalf("status patch = %#v, want paused", statusPatch)
	}
	if triggerBody["cron_expression"] != "0 9 * * 1" || triggerBody["timezone"] != "Asia/Shanghai" {
		t.Fatalf("trigger body = %#v, want cron/timezone", triggerBody)
	}
}

func TestImportRepoContextUpdatesExistingPipelineByName(t *testing.T) {
	const pipelineID = "019e2205-a0b9-74a2-9faf-8f2cd77b7631"
	var importedPipelineID any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipelines":
			json.NewEncoder(w).Encode(map[string]any{
				"pipelines": []map[string]any{
					{"id": pipelineID, "name": "Repo Flow", "nodes": []map[string]any{}},
				},
			})
		case "/api/pipelines/import":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode import payload: %v", err)
			}
			importedPipelineID = payload["pipeline_id"]
			json.NewEncoder(w).Encode(map[string]any{
				"id":    pipelineID,
				"name":  "Repo Flow",
				"nodes": []map[string]any{},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	pipelineDir := filepath.Join(root, ".multica", "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelineDir, "repo-flow.yaml"), []byte("version: 1\nname: Repo Flow\nnodes:\n  - key: build\n    title: Build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	if _, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{BaseDir: ".multica"}); err != nil {
		t.Fatalf("importRepoContext(): %v", err)
	}
	if importedPipelineID != pipelineID {
		t.Fatalf("pipeline_id = %#v, want %s", importedPipelineID, pipelineID)
	}
}

func TestImportRepoContextRejectsNonCanonicalSourceRepo(t *testing.T) {
	root := t.TempDir()
	initRepoContextGit(t, root, "feature/demo", "https://github.com/acme/other.git")
	baseDir := filepath.Join(root, ".multica")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
multica:
  source:
    repo: app
    branch: main
    path: .multica
resources:
  repos:
    app:
      url: https://github.com/acme/app.git
`
	if err := os.WriteFile(filepath.Join(baseDir, "project.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient("http://127.0.0.1:1", "ws-1", "test-token")
	_, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{
		BaseDir:      ".multica",
		SourcePolicy: repoContextSourcePolicyManual,
	})
	if err == nil {
		t.Fatal("expected non-canonical repo import to fail")
	}
	if !strings.Contains(err.Error(), "not the canonical Multica context source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportRepoContextAllowsCanonicalSourceCurrentBranch(t *testing.T) {
	const pipelineID = "019e2205-a0b9-74a2-9faf-8f2cd77b7636"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pipelines":
			json.NewEncoder(w).Encode(map[string]any{"pipelines": []map[string]any{}})
		case "/api/projects/project-123/resources":
			json.NewEncoder(w).Encode(map[string]any{"resources": []map[string]any{
				{
					"id":            "resource-1",
					"resource_type": "git_repo",
					"resource_ref":  map[string]any{"url": "https://github.com/acme/app.git"},
					"label":         "app",
				},
			}})
		case "/api/pipelines/import":
			json.NewEncoder(w).Encode(map[string]any{
				"id":    pipelineID,
				"name":  "Canonical Flow",
				"nodes": []map[string]any{},
			})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	initRepoContextGit(t, root, "feature/demo", "https://github.com/acme/app.git")
	baseDir := filepath.Join(root, ".multica")
	pipelineDir := filepath.Join(baseDir, "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
multica:
  source:
    repo: app
    branch: main
    path: .multica
resources:
  repos:
    app:
      url: git@github.com:acme/app.git
pipelines:
  - path: pipelines/canonical-flow.yaml
`
	if err := os.WriteFile(filepath.Join(baseDir, "project.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelineDir, "canonical-flow.yaml"), []byte("version: 1\nname: Canonical Flow\nnodes: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := cli.NewAPIClient(srv.URL, "ws-1", "test-token")
	result, err := importRepoContext(context.Background(), client, root, repoContextImportOptions{
		BaseDir:      ".multica",
		ProjectID:    "project-123",
		SourcePolicy: repoContextSourcePolicyManual,
	})
	if err != nil {
		t.Fatalf("importRepoContext(): %v", err)
	}
	last := result.Items[len(result.Items)-1]
	if len(result.Items) != 2 || last.Type != "pipeline" || last.ID != pipelineID {
		t.Fatalf("importRepoContext() items = %#v, want imported pipeline", result.Items)
	}
}

func TestValidateRepoContextSourceCanonicalPolicySkipsNonDefaultRef(t *testing.T) {
	root := t.TempDir()
	initRepoContextGit(t, root, "agent/dev/demo", "https://github.com/acme/app.git")
	manifest := &repoProjectManifest{
		Multica: repoProjectMultica{Source: repoProjectSource{Repo: "app", Branch: "main", Path: ".multica"}},
		Resources: repoProjectResources{Repos: map[string]repoProjectRepoResource{
			"app": {URL: "https://github.com/acme/app.git"},
		}},
	}

	err := validateRepoContextSource(root, ".multica", manifest, repoContextSourcePolicyCanonical, "feature/demo")
	var skip repoContextSourceSkip
	if !errors.As(err, &skip) {
		t.Fatalf("expected source skip, got %v", err)
	}
	if !strings.Contains(skip.Reason, "not canonical source branch") {
		t.Fatalf("unexpected skip reason: %s", skip.Reason)
	}
}

func TestCleanRepoContextPathRejectsEscapes(t *testing.T) {
	for _, raw := range []string{"", "../.multica", "docs/../../.multica", filepath.Join(t.TempDir(), ".multica")} {
		t.Run(raw, func(t *testing.T) {
			if got, err := cleanRepoContextPath(raw); err == nil {
				t.Fatalf("cleanRepoContextPath(%q): expected error, got %q", raw, got)
			}
		})
	}
}

func initRepoContextGit(t *testing.T, root, branch, origin string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-b", branch, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", root, "remote", "add", "origin", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, string(out))
	}
}
