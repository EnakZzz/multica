package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultSkillCuratorSleepBatch    = 50
	defaultSkillCuratorLookbackHours = 24 * 14
	maxSkillCuratorTokenDelta        = 260
)

type skillCuratorSleepConfig struct {
	Enabled       bool
	RunAtHour     int
	RunAtMinute   int
	RunOnStart    bool
	BatchSize     int32
	LookbackHours float64
	BaseURL       string
	Model         string
	APIKey        string
}

type skillGateResult struct {
	Status      string
	Confidence  string
	Reason      string
	ScoreBefore pgtype.Float8
	ScoreAfter  pgtype.Float8
}

func envSkillCuratorSleepConfig() skillCuratorSleepConfig {
	cfg := skillCuratorSleepConfig{
		Enabled:       envBool("MULTICA_SKILL_CURATOR_SLEEP_ENABLED", false),
		RunAtHour:     envInt("MULTICA_SKILL_CURATOR_SLEEP_RUN_AT_HOUR", 2),
		RunAtMinute:   envInt("MULTICA_SKILL_CURATOR_SLEEP_RUN_AT_MINUTE", 0),
		RunOnStart:    envBool("MULTICA_SKILL_CURATOR_SLEEP_RUN_ON_START", false),
		BatchSize:     int32(envInt("MULTICA_SKILL_CURATOR_SLEEP_BATCH_SIZE", defaultSkillCuratorSleepBatch)),
		LookbackHours: float64(envInt("MULTICA_SKILL_CURATOR_SLEEP_LOOKBACK_HOURS", defaultSkillCuratorLookbackHours)),
		Model:         strings.TrimSpace(os.Getenv("MULTICA_SKILL_CURATOR_MODEL")),
		APIKey:        strings.TrimSpace(os.Getenv("AI_GATEWAY_VIRTUAL_KEY")),
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_SKILL_CURATOR_BASE_URL")), "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AI_GATEWAY_UPSTREAM_URL")), "/")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:9111/v1"
	} else if !strings.HasSuffix(cfg.BaseURL, "/v1") {
		cfg.BaseURL += "/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4.1-mini"
	}
	if cfg.RunAtHour < 0 || cfg.RunAtHour > 23 {
		cfg.RunAtHour = 2
	}
	if cfg.RunAtMinute < 0 || cfg.RunAtMinute > 59 {
		cfg.RunAtMinute = 0
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > 200 {
		cfg.BatchSize = defaultSkillCuratorSleepBatch
	}
	if cfg.LookbackHours <= 0 {
		cfg.LookbackHours = defaultSkillCuratorLookbackHours
	}
	return cfg
}

func runSkillCuratorSleepWorker(ctx context.Context, queries *db.Queries, cfg skillCuratorSleepConfig) {
	if !cfg.Enabled {
		slog.Info("skill curator sleep worker disabled")
		return
	}
	slog.Info("skill curator sleep worker enabled", "run_at", cfg.runAtLabel(), "batch_size", cfg.BatchSize, "model", cfg.Model)
	if cfg.RunOnStart {
		processSkillCuratorSleepBatch(ctx, queries, cfg)
	}
	for {
		next := nextSkillCuratorRunAt(time.Now(), cfg)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			processSkillCuratorSleepBatch(ctx, queries, cfg)
		}
	}
}

func (cfg skillCuratorSleepConfig) runAtLabel() string {
	return fmt.Sprintf("%02d:%02d", cfg.RunAtHour, cfg.RunAtMinute)
}

func nextSkillCuratorRunAt(now time.Time, cfg skillCuratorSleepConfig) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), cfg.RunAtHour, cfg.RunAtMinute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func processSkillCuratorSleepBatch(ctx context.Context, queries *db.Queries, cfg skillCuratorSleepConfig) {
	tasks, err := queries.ListRecentCompletedTasksForSkillCuration(ctx, db.ListRecentCompletedTasksForSkillCurationParams{
		Column1: cfg.LookbackHours,
		Limit:   cfg.BatchSize,
	})
	if err != nil {
		slog.Warn("skill curator sleep: list tasks failed", "error", err)
		return
	}
	created := 0
	for _, task := range tasks {
		if stageSleepProposalForTask(ctx, queries, cfg, task) {
			created++
		}
	}
	if created > 0 {
		slog.Info("skill curator sleep: proposals staged", "created", created, "scanned", len(tasks))
	}
}

func stageSleepProposalForTask(ctx context.Context, queries *db.Queries, cfg skillCuratorSleepConfig, task db.ListRecentCompletedTasksForSkillCurationRow) bool {
	comments, err := queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     task.IssueID,
		WorkspaceID: task.IssueWorkspaceID,
		Limit:       200,
	})
	if err != nil {
		return false
	}
	signalText := collectCuratorSignalText(task.IssueTitle+" "+task.IssueDescription.String, comments, string(task.Result), task.Error.String)
	if signalText == "" {
		return false
	}
	skills, err := queries.ListAgentSkills(ctx, task.AgentID)
	if err != nil || len(skills) == 0 {
		return false
	}
	target := firstEditableSkill(skills)
	if !target.ID.Valid {
		return false
	}

	note := sleepCuratorNote(task, signalText)
	proposed := appendBoundedCuratorNote(target.Content, note)
	tokenDelta := estimateTokenDelta(target.Content, proposed)
	if tokenDelta > maxSkillCuratorTokenDelta {
		return false
	}
	diff := simpleSkillDiff(target.Content, proposed)
	rejectedSimilar, err := queries.CountRejectedSimilarSkillProposals(ctx, db.CountRejectedSimilarSkillProposalsParams{
		WorkspaceID:   target.WorkspaceID,
		Operation:     "update",
		TargetSkillID: target.ID,
		Diff:          diff,
	})
	if err != nil || rejectedSimilar > 0 {
		return false
	}
	editOps := []map[string]any{
		{"op": "add", "path": "SKILL.md", "section": "Curator Proposal Notes", "content": note},
	}
	evidence := []map[string]any{
		{"type": "task", "id": curatorUUIDString(task.ID), "outcome": task.Status},
		{"type": "issue", "id": curatorUUIDString(task.IssueID), "title": task.IssueTitle},
		{"type": "skill", "id": curatorUUIDString(target.ID), "name": target.Name},
		{"type": "sleep_replay", "lookback_hours": cfg.LookbackHours},
	}
	gate := gateSleepProposal(ctx, cfg, target.Content, proposed, signalText)
	if gate.Status != "passed" {
		return false
	}

	_, err = queries.CreateSkillProposal(ctx, db.CreateSkillProposalParams{
		WorkspaceID:           target.WorkspaceID,
		ProjectID:             task.IssueProjectID,
		SourceTaskID:          task.ID,
		SourceIssueID:         task.IssueID,
		Operation:             "update",
		TargetSkillID:         target.ID,
		Title:                 "Sleep curator proposal: " + truncateForProposal(task.IssueTitle, 80),
		Summary:               "Offline replay found reusable skill guidance in a completed task.",
		Rationale:             signalText,
		RiskLevel:             "low",
		ProposedName:          target.Name,
		ProposedDescription:   target.Description,
		ProposedContent:       proposed,
		ProposedFiles:         []byte("[]"),
		BaseContentHash:       sha256Hex(target.Content),
		Diff:                  diff,
		EvidenceRefs:          mustJSON(evidence),
		EditOps:               mustJSON(editOps),
		ValidationStatus:      gate.Status,
		ValidationScoreBefore: gate.ScoreBefore,
		ValidationScoreAfter:  gate.ScoreAfter,
		RejectedSimilarCount:  rejectedSimilar,
		TokenDelta:            tokenDelta,
		GateReason:            gate.Reason,
		Confidence:            gate.Confidence,
		CuratorModel:          "skillopt-sleep-v1/" + cfg.Model,
		CuratorPromptHash:     "",
	})
	if err != nil {
		if !isCuratorUniqueViolation(err) {
			slog.Warn("skill curator sleep: create proposal failed", "task_id", curatorUUIDString(task.ID), "error", err)
		}
		return false
	}
	return true
}

func sleepCuratorNote(task db.ListRecentCompletedTasksForSkillCurationRow, signalText string) string {
	return fmt.Sprintf(`

## Curator Proposal Notes

- Source task: %s
- Outcome: %s
- Issue: %s
- Evidence:
%s
`, curatorUUIDString(task.ID), task.Status, task.IssueTitle, indentProposalEvidence(signalText))
}

func appendBoundedCuratorNote(content, note string) string {
	return strings.TrimRight(content, "\n") + note
}

func gateSleepProposal(ctx context.Context, cfg skillCuratorSleepConfig, current, proposed, evidence string) skillGateResult {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return skillGateResult{
			Status:     "skipped",
			Confidence: "low",
			Reason:     "AI Gateway key is not configured; sleep curator did not run validation.",
		}
	}
	result, err := callSkillCuratorJudge(ctx, cfg, current, proposed, evidence)
	if err != nil {
		slog.Debug("skill curator sleep: judge failed", "error", err)
		return skillGateResult{Status: "failed", Confidence: "low", Reason: "judge failed: " + truncateForProposal(err.Error(), 300)}
	}
	if result.Status == "" {
		result.Status = "failed"
	}
	if result.Confidence == "" || result.Confidence == "high" {
		result.Confidence = "medium"
	}
	if result.Reason == "" {
		result.Reason = "LLM judge accepted a bounded skill edit."
	}
	return result
}

func callSkillCuratorJudge(ctx context.Context, cfg skillCuratorSleepConfig, current, proposed, evidence string) (skillGateResult, error) {
	endpoint := cfg.BaseURL
	if !strings.HasSuffix(endpoint, "/responses") {
		endpoint += "/responses"
	}
	prompt := strings.Join([]string{
		"You are gating a proposed SKILL.md edit. Return compact JSON only.",
		"Pass only if the edit is small, grounded in evidence, and improves future tasks.",
		"Never return high confidence unless an explicit validation set and scores are present.",
		`Schema: {"status":"passed|failed","confidence":"low|medium","reason":"...","score_before":number|null,"score_after":number|null}`,
		"Evidence:\n" + truncateForProposal(evidence, 1800),
		"Current SKILL.md:\n" + truncateForProposal(current, 5000),
		"Proposed SKILL.md:\n" + truncateForProposal(proposed, 6500),
	}, "\n\n")
	body := map[string]any{
		"model": cfg.Model,
		"input": prompt,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return skillGateResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return skillGateResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return skillGateResult{}, fmt.Errorf("judge status %d", resp.StatusCode)
	}
	var decoded struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return skillGateResult{}, err
	}
	text := strings.TrimSpace(decoded.OutputText)
	if text == "" {
		for _, out := range decoded.Output {
			for _, content := range out.Content {
				if strings.TrimSpace(content.Text) != "" {
					text = strings.TrimSpace(content.Text)
					break
				}
			}
			if text != "" {
				break
			}
		}
	}
	var raw struct {
		Status      string   `json:"status"`
		Confidence  string   `json:"confidence"`
		Reason      string   `json:"reason"`
		ScoreBefore *float64 `json:"score_before"`
		ScoreAfter  *float64 `json:"score_after"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &raw); err != nil {
		return skillGateResult{}, err
	}
	result := skillGateResult{
		Status:     normalizeSleepValidationStatus(raw.Status),
		Confidence: normalizeSleepConfidence(raw.Confidence),
		Reason:     raw.Reason,
	}
	if raw.ScoreBefore != nil {
		result.ScoreBefore = pgtype.Float8{Float64: *raw.ScoreBefore, Valid: true}
	}
	if raw.ScoreAfter != nil {
		result.ScoreAfter = pgtype.Float8{Float64: *raw.ScoreAfter, Valid: true}
	}
	return result, nil
}

func normalizeSleepValidationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed", "failed", "skipped":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "failed"
	}
}

func normalizeSleepConfidence(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "low", "medium":
		return strings.ToLower(strings.TrimSpace(confidence))
	default:
		return "low"
	}
}

func extractJSONObject(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
