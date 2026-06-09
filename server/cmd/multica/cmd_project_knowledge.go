package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var projectKnowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Search project knowledge",
}

var projectKnowledgeSearchCmd = &cobra.Command{
	Use:   "search <project-id>",
	Short: "Search project wiki and memory",
	Args:  exactArgs(1),
	RunE:  runProjectKnowledgeSearch,
}

func init() {
	projectCmd.AddCommand(projectKnowledgeCmd)
	projectKnowledgeCmd.AddCommand(projectKnowledgeSearchCmd)

	projectKnowledgeSearchCmd.Flags().String("query", "", "Search query (required)")
	projectKnowledgeSearchCmd.Flags().Int32("limit", 5, "Maximum number of results")
	projectKnowledgeSearchCmd.Flags().String("output", "json", "Output format: table or json")
}

func runProjectKnowledgeSearch(cmd *cobra.Command, args []string) error {
	query, _ := cmd.Flags().GetString("query")
	if query == "" {
		return fmt.Errorf("--query is required")
	}
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
	limit, _ := cmd.Flags().GetInt32("limit")
	body := map[string]any{
		"query": query,
		"limit": limit,
	}
	var result map[string]any
	if err := client.PostJSON(ctx, "/api/projects/"+url.PathEscape(projectRef.ID)+"/knowledge/search", body, &result); err != nil {
		return fmt.Errorf("search project knowledge: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		results, _ := result["results"].([]any)
		headers := []string{"TYPE", "TITLE", "SCORE", "SUMMARY"}
		rows := make([][]string, 0, len(results))
		for _, raw := range results {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			rows = append(rows, []string{
				strVal(item, "target_type"),
				strVal(item, "title"),
				fmt.Sprint(item["score"]),
				strVal(item, "summary"),
			})
		}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}
	return cli.PrintJSON(os.Stdout, result)
}
