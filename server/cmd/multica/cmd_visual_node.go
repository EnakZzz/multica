package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var visualNodeCmd = &cobra.Command{
	Use:   "visual-node",
	Short: "Work with project visual nodes",
}

var visualNodeCompleteCmd = &cobra.Command{
	Use:   "complete <node-id>",
	Short: "Complete a visual node generation task",
	Args:  exactArgs(1),
	RunE:  runVisualNodeComplete,
}

func init() {
	visualNodeCmd.AddCommand(visualNodeCompleteCmd)
	visualNodeCompleteCmd.Flags().String("project", "", "Project ID that owns the node")
	visualNodeCompleteCmd.Flags().String("attachment", "", "Local generated image path to upload")
	visualNodeCompleteCmd.Flags().String("note", "", "Short generation note")
	visualNodeCompleteCmd.Flags().String("note-zh", "", "Short Chinese generation note for human display")
	visualNodeCompleteCmd.Flags().String("error", "", "Failure reason")
	visualNodeCompleteCmd.Flags().String("error-zh", "", "Chinese failure reason for human display")
}

func runVisualNodeComplete(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	projectID, _ := cmd.Flags().GetString("project")
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return fmt.Errorf("--project is required")
	}
	attachmentPath, _ := cmd.Flags().GetString("attachment")
	note, _ := cmd.Flags().GetString("note")
	noteZh, _ := cmd.Flags().GetString("note-zh")
	errorText, _ := cmd.Flags().GetString("error")
	errorZh, _ := cmd.Flags().GetString("error-zh")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	payload := map[string]any{
		"note":    strings.TrimSpace(note),
		"note_zh": strings.TrimSpace(noteZh),
	}
	if taskID := strings.TrimSpace(os.Getenv("MULTICA_TASK_ID")); taskID != "" {
		payload["task_id"] = taskID
	}
	if strings.TrimSpace(errorText) != "" {
		payload["error"] = strings.TrimSpace(errorText)
		payload["error_zh"] = strings.TrimSpace(errorZh)
	} else {
		if strings.TrimSpace(attachmentPath) == "" {
			return fmt.Errorf("--attachment is required unless --error is provided")
		}
		data, err := os.ReadFile(attachmentPath)
		if err != nil {
			return fmt.Errorf("read attachment: %w", err)
		}
		attachmentID, _, err := client.UploadFileWithURL(ctx, data, filepath.Base(attachmentPath))
		if err != nil {
			return fmt.Errorf("upload attachment: %w", err)
		}
		if attachmentID == "" {
			return fmt.Errorf("uploaded attachment did not return an id")
		}
		payload["attachment_id"] = attachmentID
	}

	path := fmt.Sprintf("/api/projects/%s/visual-nodes/%s/generation-result", projectID, args[0])
	var out map[string]any
	if err := client.PostJSON(ctx, path, payload, &out); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, out)
}
