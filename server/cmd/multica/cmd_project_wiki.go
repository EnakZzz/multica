package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var projectWikiCmd = &cobra.Command{
	Use:   "wiki",
	Short: "Manage project wiki pages",
}

var projectWikiListCmd = &cobra.Command{
	Use:   "list <project-id>",
	Short: "List wiki pages for a project",
	Args:  exactArgs(1),
	RunE:  runProjectWikiList,
}

var projectWikiGetCmd = &cobra.Command{
	Use:   "get <project-id> <page-id-or-slug>",
	Short: "Get a project wiki page",
	Args:  exactArgs(2),
	RunE:  runProjectWikiGet,
}

var projectWikiUpsertCmd = &cobra.Command{
	Use:   "upsert <project-id>",
	Short: "Create or update a project wiki page by slug",
	Args:  exactArgs(1),
	RunE:  runProjectWikiUpsert,
}

func init() {
	projectCmd.AddCommand(projectWikiCmd)
	projectWikiCmd.AddCommand(projectWikiListCmd)
	projectWikiCmd.AddCommand(projectWikiGetCmd)
	projectWikiCmd.AddCommand(projectWikiUpsertCmd)

	projectWikiListCmd.Flags().String("output", "table", "Output format: table or json")
	projectWikiListCmd.Flags().Bool("full-id", false, "Show full UUIDs in table output")

	projectWikiGetCmd.Flags().String("output", "json", "Output format: table or json")

	projectWikiUpsertCmd.Flags().String("slug", "", "Wiki page slug (required)")
	projectWikiUpsertCmd.Flags().String("title", "", "Wiki page title (required when creating)")
	projectWikiUpsertCmd.Flags().String("body", "", "Markdown body")
	projectWikiUpsertCmd.Flags().String("body-file", "", "Read Markdown body from a file")
	projectWikiUpsertCmd.Flags().String("status", "draft", "Wiki page status: draft, reviewed, archived")
	projectWikiUpsertCmd.Flags().StringArray("source-ref", nil, "JSON source reference to store in source_refs (may be repeated)")
	projectWikiUpsertCmd.Flags().Bool("append", false, "Append body to an existing page instead of replacing it")
	projectWikiUpsertCmd.Flags().String("output", "json", "Output format: table or json")
}

func runProjectWikiList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	projectRef, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	pages, err := fetchProjectWikiPages(ctx, client, projectRef.ID)
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, pages)
	}

	fullID, _ := cmd.Flags().GetBool("full-id")
	headers := []string{"ID", "SLUG", "TITLE", "STATUS", "UPDATED"}
	rows := make([][]string, 0, len(pages))
	for _, page := range pages {
		updated := strVal(page, "updated_at")
		if len(updated) >= 10 {
			updated = updated[:10]
		}
		rows = append(rows, []string{
			displayID(strVal(page, "id"), fullID),
			strVal(page, "slug"),
			strVal(page, "title"),
			strVal(page, "status"),
			updated,
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

func runProjectWikiGet(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	projectRef, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	page, err := resolveProjectWikiPage(ctx, client, projectRef.ID, args[1])
	if err != nil {
		return err
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		headers := []string{"ID", "SLUG", "TITLE", "STATUS"}
		rows := [][]string{{
			strVal(page, "id"),
			strVal(page, "slug"),
			strVal(page, "title"),
			strVal(page, "status"),
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}
	return cli.PrintJSON(os.Stdout, page)
}

func runProjectWikiUpsert(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	projectRef, err := resolveProjectID(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}
	slug, _ := cmd.Flags().GetString("slug")
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return fmt.Errorf("--slug is required")
	}
	body, bodyProvided, err := wikiBodyFromFlags(cmd)
	if err != nil {
		return err
	}
	title, _ := cmd.Flags().GetString("title")
	title = strings.TrimSpace(title)
	status, _ := cmd.Flags().GetString("status")
	status = strings.TrimSpace(status)
	if status == "" {
		status = "draft"
	}
	sourceRefs, sourceRefsProvided, err := wikiSourceRefsFromFlags(cmd)
	if err != nil {
		return err
	}

	existing, err := findProjectWikiPageBySlug(ctx, client, projectRef.ID, slug)
	if err != nil {
		return err
	}
	appendBody, _ := cmd.Flags().GetBool("append")
	var result map[string]any
	if existing == nil {
		if title == "" {
			return fmt.Errorf("--title is required when creating a wiki page")
		}
		bodyPayload := map[string]any{
			"slug":        slug,
			"title":       title,
			"body":        body,
			"status":      status,
			"source_refs": sourceRefs,
		}
		if err := client.PostJSON(ctx, "/api/projects/"+url.PathEscape(projectRef.ID)+"/knowledge/wiki-pages", bodyPayload, &result); err != nil {
			return fmt.Errorf("create wiki page: %w", err)
		}
		return printProjectWikiMutationResult(cmd, result)
	}

	bodyPayload := map[string]any{}
	if title != "" {
		bodyPayload["title"] = title
	}
	if bodyProvided {
		if appendBody {
			current := strings.TrimRight(strVal(existing, "body"), "\n")
			next := strings.TrimSpace(body)
			if current != "" && next != "" {
				bodyPayload["body"] = current + "\n\n" + next
			} else if next != "" {
				bodyPayload["body"] = next
			} else {
				bodyPayload["body"] = current
			}
		} else {
			bodyPayload["body"] = body
		}
	}
	if cmd.Flags().Changed("status") {
		bodyPayload["status"] = status
	}
	if sourceRefsProvided {
		bodyPayload["source_refs"] = sourceRefs
	}
	if len(bodyPayload) == 0 {
		return fmt.Errorf("no fields to update; pass --title, --body, --body-file, --status, or --source-ref")
	}

	pageID := strVal(existing, "id")
	if err := client.PatchJSON(ctx, "/api/projects/"+url.PathEscape(projectRef.ID)+"/knowledge/wiki-pages/"+url.PathEscape(pageID), bodyPayload, &result); err != nil {
		return fmt.Errorf("update wiki page: %w", err)
	}
	return printProjectWikiMutationResult(cmd, result)
}

func fetchProjectWikiPages(ctx context.Context, client *cli.APIClient, projectID string) ([]map[string]any, error) {
	var result map[string]any
	if err := client.GetJSON(ctx, "/api/projects/"+url.PathEscape(projectID)+"/knowledge/wiki-pages", &result); err != nil {
		return nil, fmt.Errorf("list wiki pages: %w", err)
	}
	pagesRaw, _ := result["wiki_pages"].([]any)
	pages := make([]map[string]any, 0, len(pagesRaw))
	for _, raw := range pagesRaw {
		page, ok := raw.(map[string]any)
		if ok {
			pages = append(pages, page)
		}
	}
	return pages, nil
}

func resolveProjectWikiPage(ctx context.Context, client *cli.APIClient, projectID, pageRef string) (map[string]any, error) {
	pageRef = strings.TrimSpace(pageRef)
	pages, err := fetchProjectWikiPages(ctx, client, projectID)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if strVal(page, "id") == pageRef || strVal(page, "slug") == pageRef {
			return page, nil
		}
	}
	return nil, fmt.Errorf("wiki page %q not found", pageRef)
}

func findProjectWikiPageBySlug(ctx context.Context, client *cli.APIClient, projectID, slug string) (map[string]any, error) {
	pages, err := fetchProjectWikiPages(ctx, client, projectID)
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if strVal(page, "slug") == slug {
			return page, nil
		}
	}
	return nil, nil
}

func wikiBodyFromFlags(cmd *cobra.Command) (string, bool, error) {
	body, _ := cmd.Flags().GetString("body")
	bodyFile, _ := cmd.Flags().GetString("body-file")
	bodyFile = strings.TrimSpace(bodyFile)
	if strings.TrimSpace(body) != "" && bodyFile != "" {
		return "", false, fmt.Errorf("use either --body or --body-file, not both")
	}
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", false, fmt.Errorf("read --body-file: %w", err)
		}
		return string(data), true, nil
	}
	if cmd.Flags().Changed("body") {
		return body, true, nil
	}
	return "", false, nil
}

func wikiSourceRefsFromFlags(cmd *cobra.Command) ([]any, bool, error) {
	rawRefs, _ := cmd.Flags().GetStringArray("source-ref")
	if len(rawRefs) == 0 {
		return []any{}, false, nil
	}
	refs := make([]any, 0, len(rawRefs))
	for _, raw := range rawRefs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var ref any
		if err := json.Unmarshal([]byte(raw), &ref); err != nil {
			return nil, false, fmt.Errorf("--source-ref is not valid JSON: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, true, nil
}

func printProjectWikiMutationResult(cmd *cobra.Command, page map[string]any) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		headers := []string{"ID", "SLUG", "TITLE", "STATUS"}
		rows := [][]string{{
			strVal(page, "id"),
			strVal(page, "slug"),
			strVal(page, "title"),
			strVal(page, "status"),
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}
	return cli.PrintJSON(os.Stdout, page)
}
