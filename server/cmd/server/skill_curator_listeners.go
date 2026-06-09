package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var skillCuratorSignals = []string{
	"记住",
	"下次",
	"不要",
	"不是",
	"应该",
	"你漏了",
	"还是不行",
	"按这个流程",
	"默认",
	"wiki delta",
	"skill delta",
}

func registerSkillCuratorListeners(bus *events.Bus, queries *db.Queries) {
	ctx := context.Background()
	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		go runRuleSkillCurator(ctx, queries, e, "completed")
	})
	bus.Subscribe(protocol.EventTaskFailed, func(e events.Event) {
		go runRuleSkillCurator(ctx, queries, e, "failed")
	})
}

func runRuleSkillCurator(ctx context.Context, queries *db.Queries, e events.Event, outcome string) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	taskID, ok := payload["task_id"].(string)
	if !ok || taskID == "" {
		return
	}
	task, err := queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil || !task.IssueID.Valid {
		return
	}
	issue, err := queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return
	}
	comments, err := queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     task.IssueID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       200,
	})
	if err != nil {
		return
	}

	signalText := collectCuratorSignalText(issue.Title+" "+issue.Description.String, comments, string(task.Result), task.Error.String)
	if signalText == "" {
		return
	}
	skills, err := queries.ListAgentSkills(ctx, task.AgentID)
	if err != nil || len(skills) == 0 {
		return
	}
	target := firstEditableSkill(skills)
	if !target.ID.Valid {
		return
	}
	proposed := appendCuratorNote(target.Content, issue.Title, outcome, signalText, taskID)
	editOps := []map[string]any{
		{
			"op":      "add",
			"path":    "SKILL.md",
			"section": "Curator Proposal Notes",
			"content": strings.TrimPrefix(proposed, target.Content),
		},
	}
	evidence := []map[string]any{
		{"type": "task", "id": taskID, "outcome": outcome},
		{"type": "issue", "id": curatorUUIDString(task.IssueID), "title": issue.Title},
		{"type": "skill", "id": curatorUUIDString(target.ID), "name": target.Name},
	}
	diff := simpleSkillDiff(target.Content, proposed)
	_, err = queries.CreateSkillProposal(ctx, db.CreateSkillProposalParams{
		WorkspaceID:          issue.WorkspaceID,
		ProjectID:            issue.ProjectID,
		SourceTaskID:         task.ID,
		SourceIssueID:        task.IssueID,
		Operation:            "update",
		TargetSkillID:        target.ID,
		Title:                "Update skill from task evidence: " + truncateForProposal(issue.Title, 80),
		Summary:              "Rule curator detected reusable correction or workflow evidence in a terminal agent task.",
		Rationale:            signalText,
		RiskLevel:            "low",
		ProposedName:         target.Name,
		ProposedDescription:  target.Description,
		ProposedContent:      proposed,
		ProposedFiles:        []byte("[]"),
		BaseContentHash:      sha256Hex(target.Content),
		Diff:                 diff,
		EvidenceRefs:         mustJSON(evidence),
		EditOps:              mustJSON(editOps),
		ValidationStatus:     "skipped",
		RejectedSimilarCount: 0,
		TokenDelta:           estimateTokenDelta(target.Content, proposed),
		GateReason:           "Rule curator stages low-risk notes from explicit task evidence; no validation set was run.",
		Confidence:           "low",
		CuratorModel:         "rule-v1",
		CuratorPromptHash:    "",
	})
	if err != nil {
		if !isCuratorUniqueViolation(err) {
			slog.Warn("skill curator: failed to create proposal", "task_id", taskID, "error", err)
		}
		return
	}
	slog.Info("skill curator: created proposal", "task_id", taskID, "skill_id", curatorUUIDString(target.ID))
}

func isCuratorUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func firstEditableSkill(skills []db.Skill) db.Skill {
	for _, skill := range skills {
		if !skill.IsBuiltin {
			return skill
		}
	}
	return db.Skill{}
}

func collectCuratorSignalText(seed string, comments []db.Comment, result, failure string) string {
	lines := []string{}
	addIfSignal := func(label, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		lower := strings.ToLower(text)
		for _, signal := range skillCuratorSignals {
			if strings.Contains(lower, strings.ToLower(signal)) {
				lines = append(lines, fmt.Sprintf("%s: %s", label, truncateForProposal(text, 500)))
				return
			}
		}
	}
	addIfSignal("issue", seed)
	for _, c := range comments {
		label := c.AuthorType
		addIfSignal(label, c.Content)
		if c.DisplayContentZh.Valid {
			addIfSignal(label+"_zh", c.DisplayContentZh.String)
		}
	}
	addIfSignal("result", result)
	addIfSignal("failure", failure)
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "\n")
}

func appendCuratorNote(content, issueTitle, outcome, signalText, taskID string) string {
	note := fmt.Sprintf(`

## Curator Proposal Notes

- Source task: %s
- Outcome: %s
- Issue: %s
- Evidence:
%s
`, taskID, outcome, issueTitle, indentProposalEvidence(signalText))
	if strings.Contains(content, "## Curator Proposal Notes") {
		return content + note
	}
	return strings.TrimRight(content, "\n") + note
}

func indentProposalEvidence(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  - " + line
	}
	return strings.Join(lines, "\n")
}

func simpleSkillDiff(oldContent, newContent string) string {
	added := newContent
	if strings.HasPrefix(newContent, oldContent) {
		added = newContent[len(oldContent):]
	}
	return "--- current/SKILL.md\n+++ proposed/SKILL.md\n@@\n" +
		"- sha256:" + sha256Hex(oldContent) + "\n" +
		"+ sha256:" + sha256Hex(newContent) + "\n\n" +
		added
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func truncateForProposal(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}

func estimateTokenDelta(oldContent, newContent string) int32 {
	delta := len([]rune(newContent)) - len([]rune(oldContent))
	if delta < 0 {
		delta = -delta
	}
	return int32((delta + 3) / 4)
}

func curatorUUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	var b [16]byte
	copy(b[:], u.Bytes[:])
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}
