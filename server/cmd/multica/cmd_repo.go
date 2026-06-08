package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/multica-ai/multica/server/internal/cli"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Work with repositories",
}

var repoCheckoutCmd = &cobra.Command{
	Use:   "checkout <url>",
	Short: "Check out a repository into the working directory",
	Long:  "Creates a git worktree from the daemon's bare clone cache. Used by agents to check out repos on demand.",
	Args:  exactArgs(1),
	RunE:  runRepoCheckout,
}

var repoPublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Push the current generated work branch",
	Long:  "Pushes the current generated work branch to origin and records branch metadata for the daemon task result.",
	Args:  exactArgs(0),
	RunE:  runRepoPublish,
}

var repoIntegrateCmd = &cobra.Command{
	Use:   "integrate",
	Short: "Integrate a confirmed work branch",
	Long:  "Creates or updates a GitHub Pull Request or GitLab Merge Request by default. Direct merge is only attempted when --strategy direct is explicitly provided.",
	Args:  exactArgs(0),
	RunE:  runRepoIntegrate,
}

var repoSyncContextCmd = &cobra.Command{
	Use:   "sync-context",
	Short: "Write Multica skills and pipelines into the current git checkout",
	Long:  "Writes selected Multica skills and pipeline YAML files under .multica/ so local agents and human testers use the same definitions as Multica.",
	Args:  exactArgs(0),
	RunE:  runRepoSyncContext,
}

var repoImportContextCmd = &cobra.Command{
	Use:   "import-context",
	Short: "Import Multica pipeline YAML files from the current git checkout",
	Long:  "Scans .multica/pipelines/*.yaml in the current git checkout and imports them into Multica, updating an existing pipeline with the same name when present.",
	Args:  exactArgs(0),
	RunE:  runRepoImportContext,
}

var repoCheckoutRef string
var repoIntegrateSource string
var repoIntegrateTarget string
var repoIntegrateStrategy string
var repoIntegrateTestResult string
var repoIntegrateOutput string
var repoIntegrateAllowNonDefaultTarget bool

func init() {
	repoCheckoutCmd.Flags().StringVar(&repoCheckoutRef, "ref", "", "branch, tag, or commit to check out instead of the remote default branch")
	repoIntegrateCmd.Flags().StringVar(&repoIntegrateSource, "source", "", "Source branch to integrate")
	repoIntegrateCmd.Flags().StringVar(&repoIntegrateTarget, "target", "", "Target branch to integrate into")
	repoIntegrateCmd.Flags().StringVar(&repoIntegrateStrategy, "strategy", "pr-first", "Integration strategy: pr-first or direct")
	repoIntegrateCmd.Flags().StringVar(&repoIntegrateTestResult, "test-result", "", "Verification result to include in integration output")
	repoIntegrateCmd.Flags().StringVar(&repoIntegrateOutput, "output", "json", "Output format: json")
	repoIntegrateCmd.Flags().BoolVar(&repoIntegrateAllowNonDefaultTarget, "allow-non-default-target", false, "Allow integrating into a target branch other than the project default/main when the issue explicitly authorizes it")
	repoSyncContextCmd.Flags().String("agent-id", "", "Agent ID whose project-context assigned skills should be written (defaults to MULTICA_AGENT_ID in agent tasks)")
	repoSyncContextCmd.Flags().StringSlice("skill-id", nil, "Project-specific skill ID to write (repeatable or comma-separated)")
	repoSyncContextCmd.Flags().String("project-id", "", "Project ID whose project resources should be written (defaults to MULTICA_PROJECT_ID in agent tasks)")
	repoSyncContextCmd.Flags().Bool("all-skills", false, "Write every workspace skill, including generic agent skills")
	repoSyncContextCmd.Flags().StringSlice("pipeline-id", nil, "Pipeline ID to write as YAML (repeatable or comma-separated)")
	repoSyncContextCmd.Flags().Bool("all-pipelines", false, "Write every workspace pipeline as YAML")
	repoSyncContextCmd.Flags().String("dir", ".multica", "Directory within the git root for synced context files")
	repoSyncContextCmd.Flags().String("output", "table", "Output format: table or json")
	repoImportContextCmd.Flags().String("project-id", "", "Project ID whose project resources should be imported (defaults to MULTICA_PROJECT_ID in agent tasks)")
	repoImportContextCmd.Flags().StringSlice("role", nil, "Manifest role binding as role_key=agent_id; omit a role to leave its pipeline nodes unassigned")
	repoImportContextCmd.Flags().String("dir", ".multica", "Directory within the git root containing context files")
	repoImportContextCmd.Flags().String("output", "table", "Output format: table or json")
	repoCmd.AddCommand(repoCheckoutCmd)
	repoCmd.AddCommand(repoPublishCmd)
	repoCmd.AddCommand(repoIntegrateCmd)
	repoCmd.AddCommand(repoSyncContextCmd)
	repoCmd.AddCommand(repoImportContextCmd)
}

func runRepoCheckout(cmd *cobra.Command, args []string) error {
	repoURL := args[0]

	daemonPort := os.Getenv("MULTICA_DAEMON_PORT")
	if daemonPort == "" {
		return fmt.Errorf("MULTICA_DAEMON_PORT not set (this command is intended to be run by an agent inside a daemon task)")
	}

	workspaceID := os.Getenv("MULTICA_WORKSPACE_ID")
	projectID := os.Getenv("MULTICA_PROJECT_ID")
	agentName := os.Getenv("MULTICA_AGENT_NAME")
	issueIdentifier := os.Getenv("MULTICA_ISSUE_IDENTIFIER")
	taskID := os.Getenv("MULTICA_TASK_ID")
	branchName := os.Getenv("MULTICA_PLAN_BRANCH_NAME")
	requiresIsolatedWorktree := os.Getenv("MULTICA_PLAN_REQUIRES_ISOLATED_WORKTREE")
	branchPolicy := os.Getenv("MULTICA_PLAN_BRANCH_POLICY")
	mergePolicy := os.Getenv("MULTICA_PLAN_MERGE_POLICY")
	checkoutRef := repoCheckoutRef
	if checkoutRef == "" {
		checkoutRef = os.Getenv("MULTICA_REPO_CHECKOUT_REF")
	}

	// Use current working directory as the checkout target.
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	reqBody := map[string]string{
		"url":                        repoURL,
		"workspace_id":               workspaceID,
		"workdir":                    workDir,
		"ref":                        checkoutRef,
		"agent_name":                 agentName,
		"issue_identifier":           issueIdentifier,
		"branch_name":                branchName,
		"requires_isolated_worktree": requiresIsolatedWorktree,
		"branch_policy":              branchPolicy,
		"merge_policy":               mergePolicy,
		"task_id":                    taskID,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Post(
		fmt.Sprintf("http://127.0.0.1:%s/repo/checkout", daemonPort),
		"application/json",
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checkout failed: %s", string(body))
	}

	var result struct {
		Path       string `json:"path"`
		BranchName string `json:"branch_name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", result.Path)
	fmt.Fprintf(os.Stderr, "Checked out %s → %s (branch: %s)\n", repoURL, result.Path, result.BranchName)
	if err := autoImportRepoContext(cmd, result.Path, projectID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to import Multica repo context: %v\n", err)
	}

	return nil
}

var allowedPublishBranchPrefixes = []string{"agent/", "feature/", "fix/", "chore/", "docs/", "refactor/", "test/", "ci/"}

func runRepoPublish(cmd *cobra.Command, args []string) error {
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not inside a git worktree; run `multica repo checkout <url>` first")
	}
	root = strings.TrimSpace(root)

	branch, err := gitOutput(root, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("detect current branch: %w", err)
	}
	branch = strings.TrimSpace(branch)
	publishBranch := strings.TrimSpace(os.Getenv("MULTICA_PUBLISH_BRANCH_NAME"))
	pushBranch, pushRef, err := resolveRepoPublishTarget(branch, publishBranch)
	if err != nil {
		return err
	}

	commit, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("detect commit: %w", err)
	}
	commit = strings.TrimSpace(commit)

	remote, _ := gitOutput(root, "remote", "get-url", "origin")
	remote = strings.TrimSpace(remote)

	push := exec.Command("git", "-C", root, "push", "-u", "origin", pushRef)
	push.Stdout = os.Stdout
	push.Stderr = os.Stderr
	if err := push.Run(); err != nil {
		return fmt.Errorf("git push origin %s: %w", pushRef, err)
	}

	meta := repoPublishMetadata{
		BranchName: pushBranch,
		CommitSHA:  commit,
		PushedAt:   time.Now().UTC().Format(time.RFC3339),
		Remote:     remote,
		TaskID:     os.Getenv("MULTICA_TASK_ID"),
	}
	if err := writeRepoPublishMetadata(root, meta); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Pushed branch %s (%s)\n", pushBranch, shortCommit(commit))
	return nil
}

func resolveRepoPublishTarget(currentBranch, publishBranch string) (string, string, error) {
	currentBranch = strings.TrimSpace(currentBranch)
	publishBranch = strings.TrimSpace(publishBranch)
	if publishBranch != "" {
		if err := validateAgentPublishBranch(publishBranch); err != nil {
			return "", "", err
		}
		return publishBranch, "HEAD:refs/heads/" + publishBranch, nil
	}
	if err := validateAgentPublishBranch(currentBranch); err != nil {
		return "", "", err
	}
	return currentBranch, currentBranch, nil
}

type repoPublishMetadata struct {
	BranchName string `json:"branch_name"`
	CommitSHA  string `json:"commit_sha"`
	PushedAt   string `json:"pushed_at"`
	Remote     string `json:"remote,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}

func validateAgentPublishBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("cannot publish detached HEAD; run `multica repo checkout <url>` first")
	}
	if branch == "main" || branch == "master" {
		return fmt.Errorf("Agents must push to generated work branches, not protected branches. Current branch %s is protected.", branch)
	}
	for _, prefix := range allowedPublishBranchPrefixes {
		if strings.HasPrefix(branch, prefix) {
			return nil
		}
	}
	return fmt.Errorf("Agents must push to generated work branches (%s). Current branch %s is not allowed.", strings.Join(allowedPublishBranchPrefixes, ", "), branch)
}

func writeRepoPublishMetadata(root string, meta repoPublishMetadata) error {
	dir := filepath.Join(root, ".multica")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create publish metadata dir: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode publish metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repo-output.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write publish metadata: %w", err)
	}
	return nil
}

type repoIntegrateResult struct {
	SourceBranch  string   `json:"source_branch"`
	TargetBranch  string   `json:"target_branch"`
	Mode          string   `json:"mode"`
	PRURL         string   `json:"pr_url,omitempty"`
	MergeCommit   string   `json:"merge_commit,omitempty"`
	TestResult    string   `json:"test_result,omitempty"`
	Status        string   `json:"status"`
	Error         string   `json:"error,omitempty"`
	PRLinkError   string   `json:"pull_request_link_error,omitempty"`
	ConflictFiles []string `json:"conflict_files,omitempty"`
}

func runRepoIntegrate(cmd *cobra.Command, _ []string) error {
	if strings.TrimSpace(repoIntegrateOutput) != "json" {
		return fmt.Errorf("unsupported output %q; only json is supported", repoIntegrateOutput)
	}
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return writeRepoIntegrateResult(repoIntegrateResult{
			SourceBranch: strings.TrimSpace(repoIntegrateSource),
			TargetBranch: defaultRepoIntegrateTarget(repoIntegrateTarget),
			Mode:         strings.TrimSpace(repoIntegrateStrategy),
			TestResult:   strings.TrimSpace(repoIntegrateTestResult),
			Status:       "failed",
			Error:        "not inside a git worktree; run this from the repository checkout",
		})
	}
	root = strings.TrimSpace(root)
	result := integrateRepo(root, repoIntegrateOptions{
		SourceBranch:          strings.TrimSpace(repoIntegrateSource),
		TargetBranch:          strings.TrimSpace(repoIntegrateTarget),
		TargetExplicit:        cmd.Flags().Changed("target"),
		AllowNonDefaultTarget: repoIntegrateAllowNonDefaultTarget || truthyEnv("MULTICA_ALLOW_NON_DEFAULT_MERGE_TARGET"),
		NodeType:              strings.TrimSpace(os.Getenv("MULTICA_PLAN_ITEM_NODE_TYPE")),
		Strategy:              strings.TrimSpace(repoIntegrateStrategy),
		TestResult:            strings.TrimSpace(repoIntegrateTestResult),
	})
	result = linkRepoIntegratePullRequest(cmd, root, result)
	return writeRepoIntegrateResult(result)
}

type repoIntegrateOptions struct {
	SourceBranch          string
	TargetBranch          string
	TargetExplicit        bool
	AllowNonDefaultTarget bool
	NodeType              string
	Strategy              string
	TestResult            string
}

func integrateRepo(root string, opts repoIntegrateOptions) repoIntegrateResult {
	source := resolveRepoIntegrateSource(root, opts.SourceBranch)
	target := defaultRepoIntegrateTarget(opts.TargetBranch)
	defaultTarget := defaultRepoIntegrateTarget("")
	mode := strings.TrimSpace(opts.Strategy)
	if mode == "" {
		mode = "pr-first"
	}
	result := repoIntegrateResult{
		SourceBranch: source,
		TargetBranch: target,
		Mode:         mode,
		TestResult:   strings.TrimSpace(opts.TestResult),
		Status:       "failed",
	}
	if source == "" {
		result.Error = "source branch is required; pass --source or run after multica repo publish/check out a planned branch"
		return result
	}
	if target == "" {
		result.TargetBranch = "main"
		target = "main"
	}
	if isMergeNodeIntegrate(opts.NodeType) && opts.TargetExplicit && !opts.AllowNonDefaultTarget && isFeatureBranch(source) && isFeatureBranch(target) {
		result.Error = fmt.Sprintf("merge node cannot integrate iteration branch %q into feature target %q by default; omit --target to use the project default branch %q, or pass --allow-non-default-target only when the issue explicitly authorizes this feature target", source, target, defaultTarget)
		return result
	}
	if opts.TargetExplicit && !opts.AllowNonDefaultTarget && target != defaultTarget {
		result.Error = fmt.Sprintf("target branch %q is not the project default branch %q; pass --allow-non-default-target only when the issue explicitly authorizes integrating into this branch", target, defaultTarget)
		return result
	}
	switch mode {
	case "pr-first":
		return integrateRepoPRFirst(root, result)
	case "direct":
		return integrateRepoDirect(root, result)
	default:
		result.Error = "strategy must be pr-first or direct"
		return result
	}
}

type linkIssuePullRequestRequest struct {
	RepoOwner string  `json:"repo_owner"`
	RepoName  string  `json:"repo_name"`
	Number    int32   `json:"number"`
	Title     string  `json:"title"`
	State     string  `json:"state"`
	HTMLURL   string  `json:"html_url"`
	Branch    *string `json:"branch,omitempty"`
	HeadSHA   *string `json:"head_sha,omitempty"`
}

func linkRepoIntegratePullRequest(cmd *cobra.Command, root string, result repoIntegrateResult) repoIntegrateResult {
	if result.PRURL == "" || result.Status != "pr_created" {
		return result
	}
	issueID := strings.TrimSpace(os.Getenv("MULTICA_ISSUE_ID"))
	if issueID == "" {
		issueID = strings.TrimSpace(os.Getenv("MULTICA_ISSUE_IDENTIFIER"))
	}
	if issueID == "" {
		return result
	}
	owner, repo := repoOwnerNameForPullRequest(root, result.PRURL)
	if owner == "" || repo == "" {
		result.PRLinkError = "could not determine repository owner/name for pull request link"
		return result
	}
	number := parsePullRequestNumber(result.PRURL)
	if number <= 0 {
		result.PRLinkError = "could not determine pull request number from URL"
		return result
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		result.PRLinkError = err.Error()
		return result
	}
	title := fmt.Sprintf("Integrate %s into %s", result.SourceBranch, result.TargetBranch)
	headSHA := strings.TrimSpace(gitOutputOrEmpty(root, "rev-parse", result.SourceBranch))
	req := linkIssuePullRequestRequest{
		RepoOwner: owner,
		RepoName:  repo,
		Number:    int32(number),
		Title:     title,
		State:     "open",
		HTMLURL:   result.PRURL,
	}
	if result.SourceBranch != "" {
		req.Branch = &result.SourceBranch
	}
	if headSHA != "" {
		req.HeadSHA = &headSHA
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.PostJSON(ctx, "/api/issues/"+url.PathEscape(issueID)+"/pull-requests", req, nil); err != nil {
		result.PRLinkError = err.Error()
	}
	return result
}

func repoOwnerNameForPullRequest(root, prURL string) (string, string) {
	if remoteURL, err := gitOutput(root, "remote", "get-url", "origin"); err == nil {
		if remote, ok := parseGitRemoteURL(remoteURL); ok {
			if owner, repo := splitRepoPath(remote.Path); owner != "" && repo != "" {
				return owner, repo
			}
		}
	}
	if u, err := url.Parse(strings.TrimSpace(prURL)); err == nil {
		path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
		if idx := strings.Index(path, "/-/merge_requests/"); idx >= 0 {
			return splitRepoPath(path[:idx])
		}
		if idx := strings.Index(path, "/pull/"); idx >= 0 {
			return splitRepoPath(path[:idx])
		}
	}
	return "", ""
}

func splitRepoPath(path string) (string, string) {
	path = strings.Trim(strings.TrimSuffix(strings.TrimSpace(path), ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", ""
	}
	repo := parts[len(parts)-1]
	owner := strings.Join(parts[:len(parts)-1], "/")
	return owner, repo
}

var pullRequestNumberRe = regexp.MustCompile(`/(?:pull|merge_requests)/([0-9]+)(?:[/?#]|$)`)

func parsePullRequestNumber(rawURL string) int {
	m := pullRequestNumberRe.FindStringSubmatch(strings.TrimSpace(rawURL))
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func gitOutputOrEmpty(root string, args ...string) string {
	out, err := gitOutput(root, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func integrateRepoPRFirst(root string, result repoIntegrateResult) repoIntegrateResult {
	if result.SourceBranch == result.TargetBranch {
		result.Error = "source branch and target branch must differ"
		return result
	}
	if err := runCommand(root, "git", "push", "-u", "origin", result.SourceBranch); err != nil {
		result.Error = fmt.Sprintf("push source branch before PR: %v", err)
		return result
	}
	if remoteURL, err := gitOutput(root, "remote", "get-url", "origin"); err == nil {
		if remote, ok := parseGitRemoteURL(remoteURL); ok && shouldUseGitLabMergeRequest(remote) {
			return integrateRepoGitLabMR(result, remote)
		}
	}
	return integrateRepoGitHubPR(root, result)
}

func integrateRepoGitHubPR(root string, result repoIntegrateResult) repoIntegrateResult {
	if _, err := exec.LookPath("gh"); err != nil {
		result.Error = "GitHub CLI `gh` is not available; PR-first integration cannot create or update a PR and will not direct-merge automatically"
		return result
	}
	if prURL, err := commandOutput(root, "gh", "pr", "view", result.SourceBranch, "--json", "url", "--jq", ".url"); err == nil && strings.TrimSpace(prURL) != "" {
		result.PRURL = strings.TrimSpace(prURL)
		result.Status = "pr_created"
		return result
	}
	title := fmt.Sprintf("Integrate %s into %s", result.SourceBranch, result.TargetBranch)
	body := strings.TrimSpace(result.TestResult)
	if body == "" {
		body = "Created by `multica repo integrate --strategy pr-first`."
	} else {
		body = "Test result: " + body
	}
	prURL, err := commandOutput(root, "gh", "pr", "create", "--head", result.SourceBranch, "--base", result.TargetBranch, "--title", title, "--body", body)
	if err != nil {
		result.Error = fmt.Sprintf("create PR: %v", err)
		return result
	}
	result.PRURL = strings.TrimSpace(prURL)
	result.Status = "pr_created"
	return result
}

type gitRemoteURL struct {
	Host string
	Path string
}

func parseGitRemoteURL(raw string) (gitRemoteURL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gitRemoteURL{}, false
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		host := strings.TrimSpace(u.Hostname())
		path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
		if host != "" && path != "" {
			return gitRemoteURL{Host: host, Path: path}, true
		}
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		rest := raw[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			host := strings.TrimSpace(rest[:colon])
			path := strings.Trim(strings.TrimSuffix(rest[colon+1:], ".git"), "/")
			if host != "" && path != "" {
				return gitRemoteURL{Host: host, Path: path}, true
			}
		}
	}
	return gitRemoteURL{}, false
}

func shouldUseGitLabMergeRequest(remote gitRemoteURL) bool {
	host := strings.ToLower(strings.TrimSpace(remote.Host))
	if host == "" {
		return false
	}
	if isGitLabHost(host) {
		return true
	}
	for _, key := range []string{"GITLAB_BASE_URL", "CI_SERVER_URL"} {
		if envHost := hostFromURL(os.Getenv(key)); envHost != "" && strings.EqualFold(envHost, remote.Host) {
			return true
		}
	}
	if gitLabAuthHeaderValue() != "" && !isGitHubHost(host) {
		return true
	}
	return false
}

func isGitLabHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.Contains(host, "gitlab") || host == "sc-sh.happyelements.net"
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Hostname())
}

func integrateRepoGitLabMR(result repoIntegrateResult, remote gitRemoteURL) repoIntegrateResult {
	if gitLabAuthHeaderValue() == "" {
		result.Error = "GitLab remote detected but no GitLab token is configured; set GITLAB_TOKEN, GLAB_TOKEN, GITLAB_PRIVATE_TOKEN, or CI_JOB_TOKEN so PR-first can create or reuse a Merge Request"
		return result
	}
	if mrURL, err := findOpenGitLabMergeRequest(remote, result.SourceBranch, result.TargetBranch); err == nil && mrURL != "" {
		result.PRURL = mrURL
		result.Status = "pr_created"
		return result
	} else if err != nil {
		result.Error = fmt.Sprintf("query GitLab merge requests: %v", err)
		return result
	}
	mrURL, err := createGitLabMergeRequest(remote, result)
	if err != nil {
		result.Error = fmt.Sprintf("create GitLab merge request: %v", err)
		return result
	}
	result.PRURL = mrURL
	result.Status = "pr_created"
	return result
}

func gitLabAPIBase(remote gitRemoteURL) string {
	for _, key := range []string{"GITLAB_BASE_URL", "CI_SERVER_URL"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Hostname(), remote.Host) {
			return strings.TrimRight(raw, "/")
		}
	}
	return "https://" + remote.Host
}

func gitLabAuthHeaderValue() string {
	for _, key := range []string{"GITLAB_TOKEN", "GLAB_TOKEN", "GITLAB_PRIVATE_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return "PRIVATE-TOKEN: " + token
		}
	}
	if token := strings.TrimSpace(os.Getenv("CI_JOB_TOKEN")); token != "" {
		return "JOB-TOKEN: " + token
	}
	return ""
}

func newGitLabRequest(method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return nil, err
	}
	header := gitLabAuthHeaderValue()
	if header == "" {
		return nil, fmt.Errorf("GitLab token is not configured")
	}
	parts := strings.SplitN(header, ": ", 2)
	req.Header.Set(parts[0], parts[1])
	req.Header.Set("Accept", "application/json")
	return req, nil
}

type gitLabMergeRequestResponse struct {
	WebURL string `json:"web_url"`
}

func findOpenGitLabMergeRequest(remote gitRemoteURL, sourceBranch, targetBranch string) (string, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests?state=opened&source_branch=%s&target_branch=%s",
		gitLabAPIBase(remote),
		url.PathEscape(remote.Path),
		url.QueryEscape(sourceBranch),
		url.QueryEscape(targetBranch),
	)
	req, err := newGitLabRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("GitLab API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var mrs []gitLabMergeRequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&mrs); err != nil {
		return "", err
	}
	if len(mrs) == 0 {
		return "", nil
	}
	return strings.TrimSpace(mrs[0].WebURL), nil
}

func createGitLabMergeRequest(remote gitRemoteURL, result repoIntegrateResult) (string, error) {
	body := strings.TrimSpace(result.TestResult)
	if body == "" {
		body = "Created by `multica repo integrate --strategy pr-first`."
	} else {
		body = "Test result: " + body
	}
	payload := map[string]string{
		"source_branch": result.SourceBranch,
		"target_branch": result.TargetBranch,
		"title":         fmt.Sprintf("Integrate %s into %s", result.SourceBranch, result.TargetBranch),
		"description":   body,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests", gitLabAPIBase(remote), url.PathEscape(remote.Path))
	req, err := newGitLabRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("GitLab API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var mr gitLabMergeRequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return "", err
	}
	if strings.TrimSpace(mr.WebURL) == "" {
		return "", fmt.Errorf("GitLab API response did not include web_url")
	}
	return strings.TrimSpace(mr.WebURL), nil
}

func integrateRepoDirect(root string, result repoIntegrateResult) repoIntegrateResult {
	if result.SourceBranch == result.TargetBranch {
		result.Error = "source branch and target branch must differ"
		return result
	}
	_ = runCommand(root, "git", "fetch", "origin", result.TargetBranch, result.SourceBranch)
	if err := runCommand(root, "git", "checkout", result.TargetBranch); err != nil {
		if checkoutErr := runCommand(root, "git", "checkout", "-B", result.TargetBranch, "origin/"+result.TargetBranch); checkoutErr != nil {
			result.Error = fmt.Sprintf("checkout target branch: %v; fallback: %v", err, checkoutErr)
			return result
		}
	}
	sourceRef := result.SourceBranch
	if _, err := gitOutput(root, "rev-parse", "--verify", "origin/"+result.SourceBranch); err == nil {
		sourceRef = "origin/" + result.SourceBranch
	}
	if err := runCommand(root, "git", "merge", "--no-ff", sourceRef, "-m", fmt.Sprintf("Merge %s into %s", result.SourceBranch, result.TargetBranch)); err != nil {
		result.ConflictFiles = conflictedGitFiles(root)
		_ = runCommand(root, "git", "merge", "--abort")
		result.Error = fmt.Sprintf("merge source branch: %v", err)
		return result
	}
	commit, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		result.Error = fmt.Sprintf("read merge commit: %v", err)
		return result
	}
	if err := runCommand(root, "git", "push", "origin", result.TargetBranch); err != nil {
		result.MergeCommit = strings.TrimSpace(commit)
		result.Error = fmt.Sprintf("push target branch: %v", err)
		return result
	}
	result.MergeCommit = strings.TrimSpace(commit)
	result.Status = "merged"
	return result
}

func resolveRepoIntegrateSource(root, explicit string) string {
	if source := strings.TrimSpace(explicit); source != "" {
		return source
	}
	for _, key := range []string{"MULTICA_PLAN_BRANCH_NAME", "MULTICA_PUBLISH_BRANCH_NAME"} {
		if source := strings.TrimSpace(os.Getenv(key)); source != "" {
			return source
		}
	}
	if meta, err := readRepoPublishMetadata(root); err == nil && strings.TrimSpace(meta.BranchName) != "" {
		return strings.TrimSpace(meta.BranchName)
	}
	branch, err := gitOutput(root, "branch", "--show-current")
	if err == nil {
		return strings.TrimSpace(branch)
	}
	return ""
}

func defaultRepoIntegrateTarget(explicit string) string {
	if target := strings.TrimSpace(explicit); target != "" {
		return target
	}
	for _, key := range []string{"MULTICA_DEFAULT_BRANCH_HINT"} {
		if target := strings.TrimSpace(os.Getenv(key)); target != "" {
			return target
		}
	}
	return "main"
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func isMergeNodeIntegrate(nodeType string) bool {
	return strings.EqualFold(strings.TrimSpace(nodeType), "merge")
}

func isFeatureBranch(branch string) bool {
	return strings.HasPrefix(strings.TrimSpace(branch), "feature/")
}

func readRepoPublishMetadata(root string) (repoPublishMetadata, error) {
	var meta repoPublishMetadata
	data, err := os.ReadFile(filepath.Join(root, ".multica", "repo-output.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func conflictedGitFiles(root string) []string {
	out, err := gitOutput(root, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return []string{}
	}
	lines := strings.Split(out, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func writeRepoIntegrateResult(result repoIntegrateResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

type repoContextSyncOptions struct {
	BaseDir      string
	AgentID      string
	SkillIDs     []string
	ProjectID    string
	AllSkills    bool
	PipelineIDs  []string
	AllPipelines bool
}

type repoContextSyncItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type repoContextSyncResult struct {
	Root  string                `json:"root"`
	Dir   string                `json:"dir"`
	Items []repoContextSyncItem `json:"items"`
}

type repoContextImportOptions struct {
	BaseDir      string
	ProjectID    string
	RoleBindings map[string]string
	SourcePolicy repoContextSourcePolicy
	ExpectedRef  string
}

type repoContextImportResult struct {
	Root  string                `json:"root"`
	Dir   string                `json:"dir"`
	Items []repoContextSyncItem `json:"items"`
}

type repoContextSourcePolicy string

const (
	repoContextSourcePolicyManual    repoContextSourcePolicy = "manual"
	repoContextSourcePolicyCanonical repoContextSourcePolicy = "canonical"
)

type repoContextSourceSkip struct {
	Reason string
}

func (e repoContextSourceSkip) Error() string {
	return e.Reason
}

func runRepoSyncContext(cmd *cobra.Command, _ []string) error {
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not inside a git worktree; run this from the project checkout that should receive Multica context files")
	}
	root = strings.TrimSpace(root)

	agentID, _ := cmd.Flags().GetString("agent-id")
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = strings.TrimSpace(os.Getenv("MULTICA_AGENT_ID"))
	}
	pipelineIDs, _ := cmd.Flags().GetStringSlice("pipeline-id")
	skillIDs, _ := cmd.Flags().GetStringSlice("skill-id")
	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("MULTICA_PROJECT_ID"))
	}
	baseDir, _ := cmd.Flags().GetString("dir")
	allSkills, _ := cmd.Flags().GetBool("all-skills")
	allPipelines, _ := cmd.Flags().GetBool("all-pipelines")
	if !allSkills && agentID == "" && len(skillIDs) == 0 && !allPipelines && len(pipelineIDs) == 0 && projectID == "" {
		return fmt.Errorf("nothing to sync; pass --agent-id, --skill-id, --project-id, --all-skills, --pipeline-id, or --all-pipelines")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := syncRepoContext(ctx, client, root, repoContextSyncOptions{
		BaseDir:      baseDir,
		AgentID:      agentID,
		SkillIDs:     skillIDs,
		ProjectID:    projectID,
		AllSkills:    allSkills,
		PipelineIDs:  pipelineIDs,
		AllPipelines: allPipelines,
	})
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	headers := []string{"TYPE", "ID", "NAME", "PATH"}
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		rows = append(rows, []string{item.Type, item.ID, item.Name, item.Path})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	fmt.Fprintf(os.Stderr, "Synced %d Multica context file set(s) into %s.\n", len(result.Items), filepath.Join(root, result.Dir))
	return nil
}

func runRepoImportContext(cmd *cobra.Command, _ []string) error {
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not inside a git worktree; run this from the project checkout that contains Multica context files")
	}
	root = strings.TrimSpace(root)

	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("MULTICA_PROJECT_ID"))
	}
	baseDir, _ := cmd.Flags().GetString("dir")
	roleBindingFlags, _ := cmd.Flags().GetStringSlice("role")
	roleBindings, err := parseRepoContextRoleBindings(roleBindingFlags)
	if err != nil {
		return err
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := importRepoContext(ctx, client, root, repoContextImportOptions{
		BaseDir:      baseDir,
		ProjectID:    projectID,
		RoleBindings: roleBindings,
		SourcePolicy: repoContextSourcePolicyManual,
	})
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	headers := []string{"TYPE", "ID", "NAME", "PATH"}
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		rows = append(rows, []string{item.Type, item.ID, item.Name, item.Path})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	fmt.Fprintf(os.Stderr, "Imported %d Multica context file(s) from %s.\n", len(result.Items), filepath.Join(root, result.Dir))
	return nil
}

func autoImportRepoContext(cmd *cobra.Command, root, projectID string) error {
	files, err := pipelineYAMLFiles(filepath.Join(root, ".multica", "pipelines"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		if _, err := os.Stat(filepath.Join(root, ".multica", "project.yaml")); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return fmt.Errorf("stat project manifest: %w", err)
		}
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := importRepoContext(ctx, client, root, repoContextImportOptions{
		BaseDir:      ".multica",
		ProjectID:    strings.TrimSpace(projectID),
		RoleBindings: repoContextRoleBindingsFromEnv(),
		SourcePolicy: repoContextSourcePolicyCanonical,
		ExpectedRef:  repoCheckoutRef,
	})
	if err != nil {
		var skip repoContextSourceSkip
		if errors.As(err, &skip) {
			fmt.Fprintf(os.Stderr, "Skipping Multica repo context import: %s\n", skip.Reason)
			return nil
		}
		return err
	}
	if len(result.Items) > 0 {
		fmt.Fprintf(os.Stderr, "Imported %d Multica pipeline(s) from %s.\n", len(result.Items), filepath.Join(root, result.Dir, "pipelines"))
	}
	return nil
}

func syncRepoContext(ctx context.Context, client *cli.APIClient, root string, opts repoContextSyncOptions) (repoContextSyncResult, error) {
	baseDir := strings.TrimSpace(opts.BaseDir)
	if baseDir == "" {
		baseDir = ".multica"
	}
	cleanBaseDir, err := cleanRepoContextPath(baseDir)
	if err != nil {
		return repoContextSyncResult{}, fmt.Errorf("invalid --dir: %w", err)
	}
	targetDir := filepath.Join(root, filepath.FromSlash(cleanBaseDir))
	result := repoContextSyncResult{
		Root: filepath.ToSlash(root),
		Dir:  cleanBaseDir,
	}

	skills, err := repoContextSkills(ctx, client, opts)
	if err != nil {
		return result, err
	}
	for _, skill := range skills {
		id := strVal(skill, "id")
		name := strVal(skill, "name")
		skillDir := filepath.Join(targetDir, "skills", localSkillFolderName(name, id))
		if err := writeSkillBundle(skillDir, skill); err != nil {
			return result, fmt.Errorf("write skill %s: %w", id, err)
		}
		result.Items = append(result.Items, repoContextSyncItem{
			Type: "skill",
			ID:   id,
			Name: name,
			Path: filepath.ToSlash(skillDir),
		})
	}

	pipelines, err := repoContextPipelines(ctx, client, opts)
	if err != nil {
		return result, err
	}
	pipelineDir := filepath.Join(targetDir, "pipelines")
	syncedPipelinePaths := make([]string, 0, len(pipelines))
	for _, pipeline := range pipelines {
		id := strVal(pipeline, "id")
		name := strVal(pipeline, "name")
		data, err := pipelineYAMLContent(pipeline)
		if err != nil {
			return result, fmt.Errorf("render pipeline %s: %w", id, err)
		}
		if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
			return result, fmt.Errorf("create pipeline dir: %w", err)
		}
		path := filepath.Join(pipelineDir, localPipelineFileName(name, id))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return result, fmt.Errorf("write pipeline %s: %w", id, err)
		}
		result.Items = append(result.Items, repoContextSyncItem{
			Type: "pipeline",
			ID:   id,
			Name: name,
			Path: filepath.ToSlash(path),
		})
		syncedPipelinePaths = append(syncedPipelinePaths, filepath.ToSlash(filepath.Join("pipelines", filepath.Base(path))))
	}
	if len(syncedPipelinePaths) > 0 || strings.TrimSpace(opts.ProjectID) != "" {
		resources, err := repoContextProjectResources(ctx, client, opts.ProjectID)
		if err != nil {
			return result, err
		}
		manifest, err := loadRepoProjectManifest(root, cleanBaseDir)
		if err != nil {
			return result, err
		}
		if err := validateRepoContextSource(root, cleanBaseDir, manifest, repoContextSourcePolicyManual, ""); err != nil {
			return result, err
		}
		source := repoContextCanonicalSource(root, cleanBaseDir, resources)
		manifestPath, err := writeRepoProjectManifest(targetDir, syncedPipelinePaths, resources, source)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, repoContextSyncItem{
			Type: "project_manifest",
			Name: "project.yaml",
			Path: filepath.ToSlash(manifestPath),
		})
	}

	return result, nil
}

func repoContextSkills(ctx context.Context, client *cli.APIClient, opts repoContextSyncOptions) ([]map[string]any, error) {
	if opts.AllSkills {
		var summaries []map[string]any
		if err := client.GetJSON(ctx, "/api/skills", &summaries); err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		return fetchSkillDetails(ctx, client, summaries)
	}

	skills := []map[string]any{}
	seen := map[string]bool{}
	explicit, err := fetchSkillDetailsByIDs(ctx, client, opts.SkillIDs)
	if err != nil {
		return nil, err
	}
	skills = appendUniqueSkills(skills, seen, explicit)

	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return skills, nil
	}
	var assigned []map[string]any
	if err := client.GetJSON(ctx, "/api/agents/"+agentID+"/skills", &assigned); err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	details, err := fetchSkillDetails(ctx, client, assigned)
	if err != nil {
		return nil, err
	}
	for _, skill := range details {
		if skillIsRepoContext(skill) {
			skills = appendUniqueSkills(skills, seen, []map[string]any{skill})
		}
	}
	return skills, nil
}

func repoContextProjectResources(ctx context.Context, client *cli.APIClient, projectID string) (repoProjectResources, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return repoProjectResources{}, nil
	}
	resources, err := listProjectResourceMaps(ctx, client, projectID)
	if err != nil {
		return repoProjectResources{}, err
	}
	repos := map[string]repoProjectRepoResource{}
	for _, resource := range resources {
		resourceType := strVal(resource, "resource_type")
		if resourceType != "git_repo" && resourceType != "github_repo" {
			continue
		}
		key := strings.TrimSpace(strVal(resource, "label"))
		url := repoResourceURL(resource)
		if key == "" || url == "" {
			continue
		}
		ref, _ := resource["resource_ref"].(map[string]any)
		repos[key] = repoProjectRepoResource{
			URL:               url,
			DefaultBranchHint: strings.TrimSpace(strVal(ref, "default_branch_hint")),
		}
	}
	return repoProjectResources{Repos: repos}, nil
}

func writeRepoProjectManifest(targetDir string, syncedPipelinePaths []string, resources repoProjectResources, source repoProjectSource) (string, error) {
	path := filepath.Join(targetDir, "project.yaml")
	var manifest repoProjectManifest
	data, err := os.ReadFile(path)
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return "", fmt.Errorf("parse existing project manifest: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read project manifest: %w", err)
	}
	if manifest.Version == 0 {
		manifest.Version = 1
	}
	if manifest.Multica.Source.empty() && !source.empty() {
		manifest.Multica.Source = source
	}

	byPath := map[string]repoProjectPipeline{}
	order := make([]string, 0, len(manifest.Pipelines)+len(syncedPipelinePaths))
	for _, pipeline := range manifest.Pipelines {
		clean := strings.TrimSpace(filepath.ToSlash(pipeline.Path))
		if clean == "" {
			continue
		}
		if _, ok := byPath[clean]; !ok {
			order = append(order, clean)
		}
		pipeline.Path = clean
		byPath[clean] = pipeline
	}
	for _, rawPath := range syncedPipelinePaths {
		clean := strings.TrimSpace(filepath.ToSlash(rawPath))
		if clean == "" {
			continue
		}
		if _, ok := byPath[clean]; !ok {
			order = append(order, clean)
		}
		entry := byPath[clean]
		entry.Path = clean
		byPath[clean] = entry
	}
	manifest.Pipelines = make([]repoProjectPipeline, 0, len(order))
	for _, clean := range order {
		manifest.Pipelines = append(manifest.Pipelines, byPath[clean])
	}
	if len(resources.Repos) > 0 {
		if manifest.Resources.Repos == nil {
			manifest.Resources.Repos = map[string]repoProjectRepoResource{}
		}
		for key, repo := range resources.Repos {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(repo.URL) == "" {
				continue
			}
			manifest.Resources.Repos[key] = repo
		}
	}

	out, err := yaml.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("render project manifest: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create project manifest dir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write project manifest: %w", err)
	}
	return path, nil
}

func fetchSkillDetails(ctx context.Context, client *cli.APIClient, summaries []map[string]any) ([]map[string]any, error) {
	skills := make([]map[string]any, 0, len(summaries))
	seen := map[string]bool{}
	for _, summary := range summaries {
		id := strVal(summary, "id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var skill map[string]any
		if err := client.GetJSON(ctx, "/api/skills/"+id, &skill); err != nil {
			return nil, fmt.Errorf("get skill %s: %w", id, err)
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func fetchSkillDetailsByIDs(ctx context.Context, client *cli.APIClient, ids []string) ([]map[string]any, error) {
	summaries := make([]map[string]any, 0, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		summaries = append(summaries, map[string]any{"id": id})
	}
	return fetchSkillDetails(ctx, client, summaries)
}

func appendUniqueSkills(dst []map[string]any, seen map[string]bool, items []map[string]any) []map[string]any {
	for _, item := range items {
		id := strVal(item, "id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		dst = append(dst, item)
	}
	return dst
}

func skillIsRepoContext(skill map[string]any) bool {
	config := skillConfigMap(skill)
	if boolConfig(config, "repo_context") || boolConfig(config, "project_context") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", config["scope"]))) {
	case "project", "repo", "repository":
		return true
	default:
		return false
	}
}

func skillConfigMap(skill map[string]any) map[string]any {
	raw := skill["config"]
	switch v := raw.(type) {
	case map[string]any:
		return v
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = value
		}
		return out
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return out
		}
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(v, &out); err == nil {
			return out
		}
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(v, &out); err == nil {
			return out
		}
	}
	return map[string]any{}
}

func boolConfig(config map[string]any, key string) bool {
	switch v := config[key].(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && parsed
	default:
		return false
	}
}

func repoContextPipelines(ctx context.Context, client *cli.APIClient, opts repoContextSyncOptions) ([]map[string]any, error) {
	if opts.AllPipelines {
		return listPipelineMaps(ctx, client)
	}
	pipelines := make([]map[string]any, 0, len(opts.PipelineIDs))
	seen := map[string]bool{}
	for _, rawID := range opts.PipelineIDs {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var pipeline map[string]any
		if err := client.GetJSON(ctx, "/api/pipelines/"+id, &pipeline); err != nil {
			return nil, fmt.Errorf("get pipeline %s: %w", id, err)
		}
		pipelines = append(pipelines, pipeline)
	}
	return pipelines, nil
}

func listPipelineMaps(ctx context.Context, client *cli.APIClient) ([]map[string]any, error) {
	var resp map[string]any
	if err := client.GetJSON(ctx, "/api/pipelines", &resp); err != nil {
		return nil, fmt.Errorf("list pipelines: %w", err)
	}
	rawPipelines, _ := resp["pipelines"].([]any)
	pipelines := make([]map[string]any, 0, len(rawPipelines))
	for _, raw := range rawPipelines {
		pipeline, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		pipelines = append(pipelines, pipeline)
	}
	return pipelines, nil
}

func listAutopilotMaps(ctx context.Context, client *cli.APIClient) ([]map[string]any, error) {
	var resp map[string]any
	if err := client.GetJSON(ctx, "/api/autopilots", &resp); err != nil {
		return nil, fmt.Errorf("list automations: %w", err)
	}
	rawAutopilots, _ := resp["autopilots"].([]any)
	autopilots := make([]map[string]any, 0, len(rawAutopilots))
	for _, raw := range rawAutopilots {
		autopilot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		autopilots = append(autopilots, autopilot)
	}
	return autopilots, nil
}

func importRepoContext(ctx context.Context, client *cli.APIClient, root string, opts repoContextImportOptions) (repoContextImportResult, error) {
	baseDir := strings.TrimSpace(opts.BaseDir)
	if baseDir == "" {
		baseDir = ".multica"
	}
	cleanBaseDir, err := cleanRepoContextPath(baseDir)
	if err != nil {
		return repoContextImportResult{}, fmt.Errorf("invalid --dir: %w", err)
	}
	result := repoContextImportResult{
		Root: filepath.ToSlash(root),
		Dir:  cleanBaseDir,
	}

	manifest, err := loadRepoProjectManifest(root, cleanBaseDir)
	if err != nil {
		return result, err
	}
	if err := validateRepoContextSource(root, cleanBaseDir, manifest, opts.SourcePolicy, opts.ExpectedRef); err != nil {
		return result, err
	}
	pipelineImports, err := repoContextPipelineImports(root, cleanBaseDir, manifest)
	if err != nil {
		return result, err
	}
	if len(pipelineImports) == 0 && (manifest == nil || len(manifest.Automations) == 0) {
		return result, nil
	}

	existingPipelines, err := listPipelineMaps(ctx, client)
	if err != nil {
		return result, err
	}
	existingByName := map[string]string{}
	for _, pipeline := range existingPipelines {
		name := strings.ToLower(strings.TrimSpace(strVal(pipeline, "name")))
		id := strVal(pipeline, "id")
		if name != "" && id != "" {
			existingByName[name] = id
		}
	}
	if manifest != nil && len(manifest.Resources.Repos) > 0 {
		items, err := importRepoProjectResources(ctx, client, strings.TrimSpace(opts.ProjectID), manifest)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, items...)
	}

	importedByName := map[string]string{}
	for _, spec := range pipelineImports {
		data, err := os.ReadFile(spec.Path)
		if err != nil {
			return result, fmt.Errorf("read pipeline yaml %s: %w", spec.Path, err)
		}
		content, name, err := pipelineYAMLWithRoles(
			data,
			spec.RoleBindings,
			opts.RoleBindings,
		)
		if err != nil {
			return result, fmt.Errorf("parse pipeline yaml %s: %w", spec.Path, err)
		}
		payload := map[string]any{"content": content}
		if existingID := existingByName[strings.ToLower(name)]; existingID != "" {
			payload["pipeline_id"] = existingID
		}
		var pipeline map[string]any
		if err := client.PostJSON(ctx, "/api/pipelines/import", payload, &pipeline); err != nil {
			return result, fmt.Errorf("import pipeline yaml %s: %w", spec.Path, err)
		}
		id := strVal(pipeline, "id")
		importedName := strVal(pipeline, "name")
		if importedName == "" {
			importedName = name
		}
		if id != "" && importedName != "" {
			existingByName[strings.ToLower(importedName)] = id
			importedByName[strings.ToLower(importedName)] = id
		}
		result.Items = append(result.Items, repoContextSyncItem{
			Type: "pipeline",
			ID:   id,
			Name: importedName,
			Path: filepath.ToSlash(spec.Path),
		})
	}
	if manifest != nil && len(manifest.Automations) > 0 {
		items, err := importRepoProjectAutomations(ctx, client, strings.TrimSpace(opts.ProjectID), manifest, importedByName, opts.RoleBindings)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, items...)
	}
	return result, nil
}

type repoProjectManifest struct {
	Version     int                     `yaml:"version"`
	Name        string                  `yaml:"name,omitempty"`
	Multica     repoProjectMultica      `yaml:"multica,omitempty"`
	Resources   repoProjectResources    `yaml:"resources,omitempty"`
	Roles       []repoProjectRole       `yaml:"roles,omitempty"`
	Pipelines   []repoProjectPipeline   `yaml:"pipelines,omitempty"`
	Automations []repoProjectAutomation `yaml:"automations,omitempty"`
}

type repoProjectMultica struct {
	Source repoProjectSource `yaml:"source,omitempty"`
}

func (m repoProjectMultica) IsZero() bool {
	return m.Source.empty()
}

type repoProjectSource struct {
	Repo   string `yaml:"repo,omitempty"`
	Branch string `yaml:"branch,omitempty"`
	Path   string `yaml:"path,omitempty"`
}

func (s repoProjectSource) IsZero() bool {
	return s.empty()
}

func (s repoProjectSource) empty() bool {
	return strings.TrimSpace(s.Repo) == "" && strings.TrimSpace(s.Branch) == "" && strings.TrimSpace(s.Path) == ""
}

type repoProjectResources struct {
	Repos map[string]repoProjectRepoResource `yaml:"repos,omitempty"`
}

type repoProjectRepoResource struct {
	URL               string `yaml:"url"`
	DefaultBranchHint string `yaml:"default_branch_hint,omitempty"`
}

type repoProjectRole struct {
	Key         string `yaml:"key"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

type repoProjectPipeline struct {
	Path         string            `yaml:"path"`
	RoleBindings map[string]string `yaml:"role_bindings,omitempty"`
}

type repoProjectAutomation struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description,omitempty"`
	ProjectID    string `yaml:"project_id,omitempty"`
	Pipeline     string `yaml:"pipeline"`
	Cron         string `yaml:"cron,omitempty"`
	Timezone     string `yaml:"timezone,omitempty"`
	AssigneeRole string `yaml:"assignee_role,omitempty"`
	Status       string `yaml:"status,omitempty"`
	Prompt       string `yaml:"prompt,omitempty"`
}

type repoPipelineImportSpec struct {
	Path         string
	RoleBindings map[string]string
}

func loadRepoProjectManifest(root, cleanBaseDir string) (*repoProjectManifest, error) {
	path := filepath.Join(root, filepath.FromSlash(cleanBaseDir), "project.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project manifest %s: %w", path, err)
	}
	var manifest repoProjectManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse project manifest %s: %w", path, err)
	}
	if manifest.Version == 0 {
		manifest.Version = 1
	}
	if manifest.Version != 1 {
		return nil, fmt.Errorf("unsupported project manifest version %d", manifest.Version)
	}
	return &manifest, nil
}

func validateRepoContextSource(root, cleanBaseDir string, manifest *repoProjectManifest, policy repoContextSourcePolicy, expectedRef string) error {
	if manifest == nil || manifest.Multica.Source.empty() {
		return nil
	}
	source := manifest.Multica.Source
	sourceRepo := strings.TrimSpace(source.Repo)
	sourcePath := strings.TrimSpace(filepath.ToSlash(source.Path))
	sourceBranch := strings.TrimSpace(source.Branch)
	if sourcePath != "" && strings.Trim(sourcePath, "/") != strings.Trim(cleanBaseDir, "/") {
		return fmt.Errorf("project manifest source path %q does not match import dir %q", sourcePath, cleanBaseDir)
	}
	if sourceRepo == "" {
		return fmt.Errorf("project manifest multica.source.repo is required when a source is declared")
	}
	repo, ok := manifest.Resources.Repos[sourceRepo]
	if !ok || strings.TrimSpace(repo.URL) == "" {
		return fmt.Errorf("project manifest multica.source.repo %q is not defined under resources.repos", sourceRepo)
	}
	origin, err := gitOutput(root, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("detect current repo origin for Multica context source: %w", err)
	}
	origin = strings.TrimSpace(origin)
	if !sameGitRemoteURL(origin, repo.URL) {
		return fmt.Errorf("current checkout origin %q is not the canonical Multica context source %q (%s)", origin, sourceRepo, strings.TrimSpace(repo.URL))
	}
	if policy != repoContextSourcePolicyCanonical || sourceBranch == "" {
		return nil
	}
	if refMatchesBranch(expectedRef, sourceBranch) {
		return nil
	}
	if strings.TrimSpace(expectedRef) == "" {
		return nil
	}
	return repoContextSourceSkip{Reason: fmt.Sprintf("checkout ref %q is not canonical source branch %q", expectedRef, sourceBranch)}
}

func repoContextCanonicalSource(root, cleanBaseDir string, resources repoProjectResources) repoProjectSource {
	origin, err := gitOutput(root, "remote", "get-url", "origin")
	if err != nil {
		return repoProjectSource{}
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return repoProjectSource{}
	}
	keys := make([]string, 0, len(resources.Repos))
	for key := range resources.Repos {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		repo := resources.Repos[key]
		if !sameGitRemoteURL(origin, repo.URL) {
			continue
		}
		branch := strings.TrimSpace(repo.DefaultBranchHint)
		if branch == "" {
			branch = gitDefaultBranch(root)
		}
		return repoProjectSource{
			Repo:   strings.TrimSpace(key),
			Branch: branch,
			Path:   cleanBaseDir,
		}
	}
	return repoProjectSource{}
}

func sameGitRemoteURL(a, b string) bool {
	return normalizeGitRemoteURL(a) == normalizeGitRemoteURL(b)
}

func normalizeGitRemoteURL(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.TrimPrefix(value, "git+")
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(value, "git@") {
		value = strings.TrimPrefix(value, "git@")
		value = strings.Replace(value, ":", "/", 1)
		value = "https://" + value
	}
	if strings.HasPrefix(value, "ssh://git@") {
		value = strings.TrimPrefix(value, "ssh://git@")
		value = strings.Replace(value, ":", "/", 1)
		value = "https://" + value
	}
	return value
}

func refMatchesBranch(ref, branch string) bool {
	ref = strings.TrimSpace(ref)
	branch = strings.TrimSpace(branch)
	if ref == "" || branch == "" {
		return true
	}
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "origin/")
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "origin/")
	return ref == branch
}

func gitDefaultBranch(root string) string {
	ref, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		ref = strings.TrimSpace(ref)
		ref = strings.TrimPrefix(ref, "origin/")
		if ref != "" {
			return ref
		}
	}
	branch, err := gitOutput(root, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(branch)
}

func repoContextPipelineImports(root, cleanBaseDir string, manifest *repoProjectManifest) ([]repoPipelineImportSpec, error) {
	if manifest == nil || len(manifest.Pipelines) == 0 {
		files, err := pipelineYAMLFiles(filepath.Join(root, filepath.FromSlash(cleanBaseDir), "pipelines"))
		if err != nil {
			return nil, err
		}
		imports := make([]repoPipelineImportSpec, 0, len(files))
		for _, path := range files {
			imports = append(imports, repoPipelineImportSpec{Path: path})
		}
		return imports, nil
	}

	imports := make([]repoPipelineImportSpec, 0, len(manifest.Pipelines))
	for _, pipeline := range manifest.Pipelines {
		path, err := repoManifestFilePath(root, cleanBaseDir, pipeline.Path)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", pipeline.Path, err)
		}
		imports = append(imports, repoPipelineImportSpec{
			Path:         path,
			RoleBindings: normalizedStringMap(pipeline.RoleBindings),
		})
	}
	return imports, nil
}

func repoManifestFilePath(root, cleanBaseDir, raw string) (string, error) {
	raw = strings.TrimSpace(filepath.ToSlash(raw))
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	basePrefix := strings.TrimSuffix(cleanBaseDir, "/") + "/"
	if strings.HasPrefix(raw, basePrefix) {
		raw = strings.TrimPrefix(raw, basePrefix)
	}
	clean, err := cleanRepoContextPath(raw)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(cleanBaseDir), filepath.FromSlash(clean)), nil
}

func importRepoProjectResources(ctx context.Context, client *cli.APIClient, projectID string, manifest *repoProjectManifest) ([]repoContextSyncItem, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("project manifest resources require --project-id or MULTICA_PROJECT_ID")
	}
	existingResources, err := listProjectResourceMaps(ctx, client, projectID)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, resource := range existingResources {
		label := strings.TrimSpace(strVal(resource, "label"))
		url := repoResourceURL(resource)
		if label != "" {
			existing["label:"+label] = true
		}
		if url != "" {
			existing["url:"+url] = true
		}
	}

	keys := make([]string, 0, len(manifest.Resources.Repos))
	for key := range manifest.Resources.Repos {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]repoContextSyncItem, 0, len(keys))
	for _, key := range keys {
		alias := strings.TrimSpace(key)
		repo := manifest.Resources.Repos[key]
		url := strings.TrimSpace(repo.URL)
		if alias == "" || url == "" {
			return nil, fmt.Errorf("project manifest repo %q requires url", key)
		}
		if existing["label:"+alias] || existing["url:"+url] {
			items = append(items, repoContextSyncItem{
				Type: "project_resource",
				Name: alias,
				Path: url,
			})
			continue
		}
		ref := map[string]any{"url": url}
		if branch := strings.TrimSpace(repo.DefaultBranchHint); branch != "" {
			ref["default_branch_hint"] = branch
		}
		payload := map[string]any{
			"resource_type": "git_repo",
			"resource_ref":  ref,
			"label":         alias,
		}
		var created map[string]any
		if err := client.PostJSON(ctx, "/api/projects/"+projectID+"/resources", payload, &created); err != nil {
			return nil, fmt.Errorf("create project repo resource %q: %w", alias, err)
		}
		items = append(items, repoContextSyncItem{
			Type: "project_resource",
			ID:   strVal(created, "id"),
			Name: alias,
			Path: url,
		})
		existing["label:"+alias] = true
		existing["url:"+url] = true
	}
	return items, nil
}

func listProjectResourceMaps(ctx context.Context, client *cli.APIClient, projectID string) ([]map[string]any, error) {
	var resp map[string]any
	if err := client.GetJSON(ctx, "/api/projects/"+projectID+"/resources", &resp); err != nil {
		return nil, fmt.Errorf("list project resources: %w", err)
	}
	rawResources, _ := resp["resources"].([]any)
	resources := make([]map[string]any, 0, len(rawResources))
	for _, raw := range rawResources {
		resource, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func repoResourceURL(resource map[string]any) string {
	ref, _ := resource["resource_ref"].(map[string]any)
	return strings.TrimSpace(strVal(ref, "url"))
}

func importRepoProjectAutomations(ctx context.Context, client *cli.APIClient, defaultProjectID string, manifest *repoProjectManifest, importedPipelineIDs map[string]string, roleAgentIDs map[string]string) ([]repoContextSyncItem, error) {
	items := make([]repoContextSyncItem, 0, len(manifest.Automations))
	var existingByName map[string]string
	loadExisting := func() error {
		if existingByName != nil {
			return nil
		}
		autopilots, err := listAutopilotMaps(ctx, client)
		if err != nil {
			return err
		}
		existingByName = map[string]string{}
		for _, autopilot := range autopilots {
			name := strings.ToLower(strings.TrimSpace(strVal(autopilot, "title")))
			id := strVal(autopilot, "id")
			if name != "" && id != "" {
				existingByName[name] = id
			}
		}
		return nil
	}
	for _, automation := range manifest.Automations {
		name := strings.TrimSpace(automation.Name)
		if name == "" {
			return nil, fmt.Errorf("automation name is required")
		}
		roleKey := strings.TrimSpace(automation.AssigneeRole)
		assigneeID := ""
		if roleKey != "" {
			assigneeID = strings.TrimSpace(roleAgentIDs[roleKey])
		}
		if assigneeID == "" {
			items = append(items, repoContextSyncItem{
				Type: "automation_pending",
				Name: name,
				Path: "unbound role: " + roleKey,
			})
			continue
		}

		description := repoAutomationDescription(automation, importedPipelineIDs)
		status := strings.ToLower(strings.TrimSpace(automation.Status))
		if status == "" {
			status = "active"
		}
		projectID := strings.TrimSpace(automation.ProjectID)
		if projectID == "" {
			projectID = strings.TrimSpace(defaultProjectID)
		}
		if err := loadExisting(); err != nil {
			return nil, err
		}
		if existingID := existingByName[strings.ToLower(name)]; existingID != "" {
			body := map[string]any{
				"title":          name,
				"description":    nullableString(description),
				"assignee_id":    assigneeID,
				"status":         status,
				"execution_mode": "run_only",
			}
			if projectID != "" {
				body["project_id"] = projectID
			}
			var updated map[string]any
			if err := client.PatchJSON(ctx, "/api/autopilots/"+existingID, body, &updated); err != nil {
				return nil, fmt.Errorf("update automation %q: %w", name, err)
			}
			items = append(items, repoContextSyncItem{
				Type: "automation",
				ID:   existingID,
				Name: name,
				Path: strings.TrimSpace(automation.Cron),
			})
			continue
		}
		body := map[string]any{
			"title":          name,
			"description":    nullableString(description),
			"assignee_id":    assigneeID,
			"execution_mode": "run_only",
		}
		if projectID != "" {
			body["project_id"] = projectID
		}
		var autopilot map[string]any
		if err := client.PostJSON(ctx, "/api/autopilots", body, &autopilot); err != nil {
			return nil, fmt.Errorf("create automation %q: %w", name, err)
		}
		autopilotID := strVal(autopilot, "id")
		if status != "" && status != "active" && autopilotID != "" {
			var updated map[string]any
			if err := client.PatchJSON(ctx, "/api/autopilots/"+autopilotID, map[string]any{"status": status}, &updated); err != nil {
				return nil, fmt.Errorf("pause automation %q: %w", name, err)
			}
		}
		if autopilotID != "" {
			existingByName[strings.ToLower(name)] = autopilotID
		}
		if cron := strings.TrimSpace(automation.Cron); cron != "" && autopilotID != "" {
			trigger := map[string]any{
				"kind":            "schedule",
				"cron_expression": cron,
				"timezone":        strings.TrimSpace(automation.Timezone),
				"label":           name,
			}
			var triggerResp map[string]any
			if err := client.PostJSON(ctx, "/api/autopilots/"+autopilotID+"/triggers", trigger, &triggerResp); err != nil {
				return nil, fmt.Errorf("create automation schedule %q: %w", name, err)
			}
		}
		items = append(items, repoContextSyncItem{
			Type: "automation",
			ID:   autopilotID,
			Name: name,
			Path: strings.TrimSpace(automation.Cron),
		})
	}
	return items, nil
}

func repoAutomationDescription(automation repoProjectAutomation, importedPipelineIDs map[string]string) string {
	prompt := strings.TrimSpace(automation.Prompt)
	pipelineName := strings.TrimSpace(automation.Pipeline)
	pipelineID := importedPipelineIDs[strings.ToLower(pipelineName)]
	var b strings.Builder
	if prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	if pipelineName != "" {
		fmt.Fprintf(&b, "This automation is attached to Multica pipeline %q.", pipelineName)
		if pipelineID != "" {
			fmt.Fprintf(&b, " Pipeline ID: %s.", pipelineID)
		}
		b.WriteString("\n")
	}
	if pipelineID != "" {
		fmt.Fprintf(&b, "When the schedule fires and the task should generate executable pipeline issues, trigger `POST /api/pipelines/%s/run` through the Multica API.\n", pipelineID)
	}
	if text := strings.TrimSpace(automation.Description); text != "" {
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func normalizedStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func parseRepoContextRoleBindings(values []string) (map[string]string, error) {
	bindings := map[string]string{}
	for _, raw := range values {
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key, value, ok := strings.Cut(item, "=")
			if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("invalid --role %q; expected role_key=agent_id", item)
			}
			bindings[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return bindings, nil
}

func repoContextRoleBindingsFromEnv() map[string]string {
	raw := strings.TrimSpace(os.Getenv("MULTICA_PROJECT_ROLE_BINDINGS"))
	if raw == "" {
		return map[string]string{}
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		return normalizedStringMap(values)
	}
	bindings, err := parseRepoContextRoleBindings([]string{raw})
	if err != nil {
		return map[string]string{}
	}
	return bindings
}

func pipelineYAMLFiles(dir string) ([]string, error) {
	matches := []string{}
	for _, pattern := range []string{"*.yaml", "*.yml"} {
		found, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return nil, fmt.Errorf("scan pipeline yaml: %w", err)
		}
		matches = append(matches, found...)
	}
	sort.Strings(matches)
	return matches, nil
}

func pipelineYAMLWithProjectDefault(data []byte, _ string) (content string, name string, err error) {
	return pipelineYAMLWithRoles(data, nil, nil)
}

func pipelineYAMLWithRoles(data []byte, nodeRoleBindings, roleAgentIDs map[string]string) (content string, name string, err error) {
	var doc repoImportPipelineYAMLDefinition
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", "", err
	}
	name = strings.TrimSpace(doc.Name)
	mutated := false
	if len(nodeRoleBindings) > 0 {
		normalizedRoleAgents := normalizedStringMap(roleAgentIDs)
		for index, node := range doc.Nodes {
			key := strings.TrimSpace(fmt.Sprintf("%v", node["key"]))
			if key == "" {
				continue
			}
			roleKey := strings.TrimSpace(nodeRoleBindings[key])
			if roleKey == "" {
				continue
			}
			agentID := strings.TrimSpace(normalizedRoleAgents[roleKey])
			if agentID == "" {
				delete(node, "agent_id")
				delete(node, "agent")
			} else {
				node["agent_id"] = agentID
			}
			doc.Nodes[index] = node
			mutated = true
		}
	}
	if !mutated {
		return string(data), name, nil
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", "", err
	}
	return string(out), name, nil
}

type repoImportPipelineYAMLDefinition struct {
	Version     int              `yaml:"version"`
	Name        string           `yaml:"name"`
	Description string           `yaml:"description,omitempty"`
	Nodes       []map[string]any `yaml:"nodes"`
}

type repoPipelineYAML struct {
	Version     int                    `yaml:"version"`
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description,omitempty"`
	Nodes       []repoPipelineYAMLNode `yaml:"nodes"`
}

type repoPipelineYAMLNode struct {
	Key         string                   `yaml:"key"`
	Type        string                   `yaml:"type,omitempty"`
	Title       string                   `yaml:"title"`
	Description string                   `yaml:"description,omitempty"`
	AgentID     string                   `yaml:"agent_id,omitempty"`
	Repo        string                   `yaml:"repo,omitempty"`
	Repos       []string                 `yaml:"repos,omitempty"`
	DependsOn   []string                 `yaml:"depends_on,omitempty"`
	Position    repoPipelineYAMLPosition `yaml:"position"`
}

type repoPipelineYAMLPosition struct {
	X int32 `yaml:"x"`
	Y int32 `yaml:"y"`
}

func pipelineYAMLContent(pipeline map[string]any) ([]byte, error) {
	doc := repoPipelineYAML{
		Version:     1,
		Name:        strVal(pipeline, "name"),
		Description: strVal(pipeline, "description"),
	}
	rawNodes, _ := pipeline["nodes"].([]any)
	doc.Nodes = make([]repoPipelineYAMLNode, 0, len(rawNodes))
	for _, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		doc.Nodes = append(doc.Nodes, repoPipelineYAMLNode{
			Key:         strVal(node, "key"),
			Type:        strVal(node, "type"),
			Title:       strVal(node, "title"),
			Description: strVal(node, "description"),
			AgentID:     strVal(node, "agent_id"),
			Repo:        strVal(node, "repo"),
			Repos:       stringSliceVal(node["repos"]),
			DependsOn:   stringSliceVal(node["depends_on_node_keys"]),
			Position: repoPipelineYAMLPosition{
				X: int32Val(node["position_x"]),
				Y: int32Val(node["position_y"]),
			},
		})
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func localPipelineFileName(name, id string) string {
	return localSkillFolderName(name, id) + ".yaml"
}

func cleanRepoContextPath(raw string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw)))
	if cleaned == "." || filepath.IsAbs(cleaned) || filepath.VolumeName(cleaned) != "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid repo context path %q", raw)
	}
	return filepath.ToSlash(cleaned), nil
}

func stringSliceVal(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if v := strings.TrimSpace(fmt.Sprintf("%v", item)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func int32Val(raw any) int32 {
	switch v := raw.(type) {
	case int:
		return int32(v)
	case int32:
		return v
	case int64:
		return int32(v)
	case float64:
		return int32(v)
	case json.Number:
		i, _ := v.Int64()
		return int32(i)
	default:
		return 0
	}
}

func gitOutput(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func commandOutput(workDir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func runCommand(workDir, name string, args ...string) error {
	_, err := commandOutput(workDir, name, args...)
	return err
}

func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
