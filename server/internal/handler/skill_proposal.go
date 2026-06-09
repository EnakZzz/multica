package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type SkillProposalResponse struct {
	ID                    string   `json:"id"`
	WorkspaceID           string   `json:"workspace_id"`
	ProjectID             *string  `json:"project_id"`
	SourceTaskID          *string  `json:"source_task_id"`
	SourceIssueID         *string  `json:"source_issue_id"`
	Operation             string   `json:"operation"`
	TargetSkillID         *string  `json:"target_skill_id"`
	Status                string   `json:"status"`
	Title                 string   `json:"title"`
	Summary               string   `json:"summary"`
	Rationale             string   `json:"rationale"`
	RiskLevel             string   `json:"risk_level"`
	ProposedName          string   `json:"proposed_name"`
	ProposedDescription   string   `json:"proposed_description"`
	ProposedContent       string   `json:"proposed_content"`
	ProposedFiles         any      `json:"proposed_files"`
	BaseContentHash       string   `json:"base_content_hash"`
	Diff                  string   `json:"diff"`
	EvidenceRefs          any      `json:"evidence_refs"`
	EditOps               any      `json:"edit_ops"`
	ValidationStatus      string   `json:"validation_status"`
	ValidationScoreBefore *float64 `json:"validation_score_before"`
	ValidationScoreAfter  *float64 `json:"validation_score_after"`
	RejectedSimilarCount  int32    `json:"rejected_similar_count"`
	TokenDelta            int32    `json:"token_delta"`
	GateReason            string   `json:"gate_reason"`
	Confidence            string   `json:"confidence"`
	CuratorModel          string   `json:"curator_model"`
	CuratorPromptHash     string   `json:"curator_prompt_hash"`
	CreatedBy             *string  `json:"created_by"`
	ReviewedBy            *string  `json:"reviewed_by"`
	RejectedReason        string   `json:"rejected_reason"`
	AppliedSkillID        *string  `json:"applied_skill_id"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
	ReviewedAt            *string  `json:"reviewed_at"`
	AppliedAt             *string  `json:"applied_at"`
}

type RejectSkillProposalRequest struct {
	Reason string `json:"reason"`
}

type skillProposalInput struct {
	WorkspaceID           pgtype.UUID
	ProjectID             pgtype.UUID
	SourceTaskID          pgtype.UUID
	SourceIssueID         pgtype.UUID
	Operation             string
	TargetSkillID         pgtype.UUID
	Title                 string
	Summary               string
	Rationale             string
	RiskLevel             string
	ProposedName          string
	ProposedDescription   string
	ProposedContent       string
	ProposedFiles         any
	BaseContentHash       string
	Diff                  string
	EvidenceRefs          any
	EditOps               any
	ValidationStatus      string
	ValidationScoreBefore pgtype.Float8
	ValidationScoreAfter  pgtype.Float8
	RejectedSimilarCount  int32
	TokenDelta            int32
	GateReason            string
	Confidence            string
	CuratorModel          string
	CuratorPromptHash     string
	CreatedBy             pgtype.UUID
}

func skillProposalToResponse(p db.SkillProposal) SkillProposalResponse {
	return SkillProposalResponse{
		ID:                    uuidToString(p.ID),
		WorkspaceID:           uuidToString(p.WorkspaceID),
		ProjectID:             uuidToPtr(p.ProjectID),
		SourceTaskID:          uuidToPtr(p.SourceTaskID),
		SourceIssueID:         uuidToPtr(p.SourceIssueID),
		Operation:             p.Operation,
		TargetSkillID:         uuidToPtr(p.TargetSkillID),
		Status:                p.Status,
		Title:                 p.Title,
		Summary:               p.Summary,
		Rationale:             p.Rationale,
		RiskLevel:             p.RiskLevel,
		ProposedName:          p.ProposedName,
		ProposedDescription:   p.ProposedDescription,
		ProposedContent:       p.ProposedContent,
		ProposedFiles:         decodeJSONWithDefault(p.ProposedFiles, []any{}),
		BaseContentHash:       p.BaseContentHash,
		Diff:                  p.Diff,
		EvidenceRefs:          decodeJSONWithDefault(p.EvidenceRefs, []any{}),
		EditOps:               decodeJSONWithDefault(p.EditOps, []any{}),
		ValidationStatus:      p.ValidationStatus,
		ValidationScoreBefore: float8ToPtr(p.ValidationScoreBefore),
		ValidationScoreAfter:  float8ToPtr(p.ValidationScoreAfter),
		RejectedSimilarCount:  p.RejectedSimilarCount,
		TokenDelta:            p.TokenDelta,
		GateReason:            p.GateReason,
		Confidence:            p.Confidence,
		CuratorModel:          p.CuratorModel,
		CuratorPromptHash:     p.CuratorPromptHash,
		CreatedBy:             uuidToPtr(p.CreatedBy),
		ReviewedBy:            uuidToPtr(p.ReviewedBy),
		RejectedReason:        p.RejectedReason,
		AppliedSkillID:        uuidToPtr(p.AppliedSkillID),
		CreatedAt:             timestampToString(p.CreatedAt),
		UpdatedAt:             timestampToString(p.UpdatedAt),
		ReviewedAt:            timestampToPtr(p.ReviewedAt),
		AppliedAt:             timestampToPtr(p.AppliedAt),
	}
}

func float8ToPtr(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func decodeJSONWithDefault(raw []byte, fallback any) any {
	if len(raw) == 0 {
		return fallback
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return fallback
	}
	return out
}

func skillContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func marshalProposalJSON(v any, fallback string) []byte {
	if v == nil {
		return []byte(fallback)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(fallback)
	}
	return b
}

func normalizeProposalOperation(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "insert", "update", "delete":
		return strings.ToLower(strings.TrimSpace(op))
	default:
		return ""
	}
}

func normalizeProposalRisk(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "medium", "high":
		return strings.ToLower(strings.TrimSpace(risk))
	default:
		return "low"
	}
}

func (h *Handler) createSkillProposal(ctx context.Context, q *db.Queries, input skillProposalInput) (db.SkillProposal, error) {
	if q == nil {
		q = h.Queries
	}
	return q.CreateSkillProposal(ctx, db.CreateSkillProposalParams{
		WorkspaceID:           input.WorkspaceID,
		ProjectID:             input.ProjectID,
		SourceTaskID:          input.SourceTaskID,
		SourceIssueID:         input.SourceIssueID,
		Operation:             normalizeProposalOperation(input.Operation),
		TargetSkillID:         input.TargetSkillID,
		Title:                 sanitizeNullBytes(input.Title),
		Summary:               sanitizeNullBytes(input.Summary),
		Rationale:             sanitizeNullBytes(input.Rationale),
		RiskLevel:             normalizeProposalRisk(input.RiskLevel),
		ProposedName:          sanitizeNullBytes(input.ProposedName),
		ProposedDescription:   sanitizeNullBytes(input.ProposedDescription),
		ProposedContent:       sanitizeNullBytes(input.ProposedContent),
		ProposedFiles:         marshalProposalJSON(input.ProposedFiles, "[]"),
		BaseContentHash:       input.BaseContentHash,
		Diff:                  sanitizeNullBytes(input.Diff),
		EvidenceRefs:          marshalProposalJSON(input.EvidenceRefs, "[]"),
		EditOps:               marshalProposalJSON(input.EditOps, "[]"),
		ValidationStatus:      normalizeValidationStatus(input.ValidationStatus),
		ValidationScoreBefore: input.ValidationScoreBefore,
		ValidationScoreAfter:  input.ValidationScoreAfter,
		RejectedSimilarCount:  input.RejectedSimilarCount,
		TokenDelta:            input.TokenDelta,
		GateReason:            sanitizeNullBytes(input.GateReason),
		Confidence:            normalizeProposalConfidence(input.Confidence),
		CuratorModel:          sanitizeNullBytes(input.CuratorModel),
		CuratorPromptHash:     sanitizeNullBytes(input.CuratorPromptHash),
		CreatedBy:             input.CreatedBy,
	})
}

func normalizeValidationStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "skipped", "passed", "failed":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "not_run"
	}
}

func normalizeProposalConfidence(confidence string) string {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(confidence))
	default:
		return "unknown"
	}
}

func (h *Handler) ListSkillProposals(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	var statusText pgtype.Text
	if status != "" {
		statusText = pgtype.Text{String: status, Valid: true}
	}
	limit := int32(100)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}

	items, err := h.Queries.ListSkillProposalsByWorkspace(r.Context(), db.ListSkillProposalsByWorkspaceParams{
		WorkspaceID: workspaceUUID,
		Limit:       limit,
		Status:      statusText,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skill proposals")
		return
	}
	resp := make([]SkillProposalResponse, len(items))
	for i, item := range items {
		resp[i] = skillProposalToResponse(item)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetSkillProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "proposal_id")
	if !ok {
		return
	}
	proposal, err := h.Queries.GetSkillProposalInWorkspace(r.Context(), db.GetSkillProposalInWorkspaceParams{
		ID:          id,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "skill proposal not found")
		return
	}
	writeJSON(w, http.StatusOK, skillProposalToResponse(proposal))
}

func (h *Handler) RejectSkillProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "proposal_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetSkillProposalInWorkspace(r.Context(), db.GetSkillProposalInWorkspaceParams{
		ID:          id,
		WorkspaceID: workspaceUUID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "skill proposal not found")
		return
	}
	var req RejectSkillProposalRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	proposal, err := h.Queries.RejectSkillProposal(r.Context(), db.RejectSkillProposalParams{
		ID:             id,
		ReviewedBy:     parseUUID(userID),
		RejectedReason: sanitizeNullBytes(req.Reason),
	})
	if err != nil {
		writeError(w, http.StatusConflict, "skill proposal is not pending")
		return
	}
	writeJSON(w, http.StatusOK, skillProposalToResponse(proposal))
}

func (h *Handler) ApplySkillProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "proposal_id")
	if !ok {
		return
	}

	proposal, err := h.Queries.GetSkillProposalInWorkspace(r.Context(), db.GetSkillProposalInWorkspaceParams{
		ID:          id,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "skill proposal not found")
		return
	}
	if proposal.Status != "pending" {
		writeError(w, http.StatusConflict, "skill proposal is not pending")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	var appliedSkill db.Skill
	switch proposal.Operation {
	case "insert":
		appliedSkill, err = qtx.CreateSkill(r.Context(), db.CreateSkillParams{
			WorkspaceID: workspaceUUID,
			Name:        sanitizeNullBytes(proposal.ProposedName),
			Description: sanitizeNullBytes(proposal.ProposedDescription),
			Content:     sanitizeNullBytes(proposal.ProposedContent),
			Config:      []byte(`{"curation":{"source":"proposal"}}`),
			CreatedBy:   parseUUID(userID),
		})
		if err == nil && proposalHasFiles(proposal.ProposedFiles) {
			err = upsertProposalFiles(r.Context(), qtx, appliedSkill.ID, proposal.ProposedFiles)
		}
	case "update":
		if !proposal.TargetSkillID.Valid {
			writeError(w, http.StatusBadRequest, "target_skill_id is required")
			return
		}
		current, getErr := qtx.GetSkillInWorkspace(r.Context(), db.GetSkillInWorkspaceParams{
			ID:          proposal.TargetSkillID,
			WorkspaceID: workspaceUUID,
		})
		if getErr != nil {
			writeError(w, http.StatusNotFound, "target skill not found")
			return
		}
		if proposal.BaseContentHash != "" && skillContentHash(current.Content) != proposal.BaseContentHash {
			writeError(w, http.StatusConflict, "target skill changed since proposal was created")
			return
		}
		appliedSkill, err = qtx.UpdateSkill(r.Context(), db.UpdateSkillParams{
			ID:          proposal.TargetSkillID,
			Name:        pgtype.Text{String: sanitizeNullBytes(proposal.ProposedName), Valid: proposal.ProposedName != ""},
			Description: pgtype.Text{String: sanitizeNullBytes(proposal.ProposedDescription), Valid: true},
			Content:     pgtype.Text{String: sanitizeNullBytes(proposal.ProposedContent), Valid: true},
		})
		if err == nil && proposalHasFiles(proposal.ProposedFiles) {
			err = upsertProposalFiles(r.Context(), qtx, appliedSkill.ID, proposal.ProposedFiles)
		}
	case "delete":
		if !proposal.TargetSkillID.Valid {
			writeError(w, http.StatusBadRequest, "target_skill_id is required")
			return
		}
		current, getErr := qtx.GetSkillInWorkspace(r.Context(), db.GetSkillInWorkspaceParams{
			ID:          proposal.TargetSkillID,
			WorkspaceID: workspaceUUID,
		})
		if getErr != nil {
			writeError(w, http.StatusNotFound, "target skill not found")
			return
		}
		config := decodeSkillConfig(current.Config)
		configMap, _ := config.(map[string]any)
		if configMap == nil {
			configMap = map[string]any{}
		}
		configMap["deprecated"] = true
		configMap["deprecated_by_proposal_id"] = uuidToString(proposal.ID)
		configBytes, _ := json.Marshal(configMap)
		appliedSkill, err = qtx.UpdateSkill(r.Context(), db.UpdateSkillParams{
			ID:     proposal.TargetSkillID,
			Config: configBytes,
		})
	default:
		writeError(w, http.StatusBadRequest, "unsupported proposal operation")
		return
	}
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "skill proposal conflicts with existing skill data")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to apply skill proposal: "+err.Error())
		return
	}

	applied, err := qtx.MarkSkillProposalApplied(r.Context(), db.MarkSkillProposalAppliedParams{
		ID:             proposal.ID,
		ReviewedBy:     parseUUID(userID),
		AppliedSkillID: appliedSkill.ID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "skill proposal is not pending")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publish(protocol.EventSkillUpdated, workspaceID, actorType, actorID, map[string]any{"skill": skillToResponse(appliedSkill)})
	writeJSON(w, http.StatusOK, skillProposalToResponse(applied))
}

func upsertProposalFiles(ctx context.Context, qtx *db.Queries, skillID pgtype.UUID, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var files []CreateSkillFileRequest
	if err := json.Unmarshal(raw, &files); err != nil || files == nil {
		return nil
	}
	if err := qtx.DeleteSkillFilesBySkill(ctx, skillID); err != nil {
		return err
	}
	for _, file := range files {
		if !validateFilePath(file.Path) || strings.EqualFold(file.Path, "SKILL.md") {
			continue
		}
		if _, err := qtx.UpsertSkillFile(ctx, db.UpsertSkillFileParams{
			SkillID: skillID,
			Path:    sanitizeNullBytes(file.Path),
			Content: sanitizeNullBytes(file.Content),
		}); err != nil {
			return err
		}
	}
	return nil
}

func proposalHasFiles(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var files []CreateSkillFileRequest
	return json.Unmarshal(raw, &files) == nil && len(files) > 0
}
