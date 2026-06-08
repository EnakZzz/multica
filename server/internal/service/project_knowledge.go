package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const (
	KnowledgeTargetWikiPage   = "wiki_page"
	KnowledgeTargetMemoryItem = "memory_item"
	DefaultEmbeddingModel     = "text-embedding-3-small"
	DefaultEmbeddingDimension = 1536
)

var ErrEmbeddingNotConfigured = errors.New("project knowledge embeddings are not configured")

type Embedder interface {
	Model() string
	Dimensions() int
	Embed(ctx context.Context, input string) ([]float32, error)
}

type ProjectKnowledgeService struct {
	Queries  *db.Queries
	Tx       TxStarter
	Embedder Embedder
}

func NewProjectKnowledgeService(q *db.Queries, tx TxStarter, embedder Embedder) *ProjectKnowledgeService {
	if isNilEmbedder(embedder) {
		if envEmbedder := NewOpenAICompatibleEmbedderFromEnv(); envEmbedder != nil {
			embedder = envEmbedder
		} else {
			embedder = nil
		}
	}
	return &ProjectKnowledgeService{Queries: q, Tx: tx, Embedder: embedder}
}

func isNilEmbedder(embedder Embedder) bool {
	if embedder == nil {
		return true
	}
	value := reflect.ValueOf(embedder)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type OpenAICompatibleEmbedder struct {
	BaseURL     string
	APIKey      string
	ModelName   string
	DimensionsN int
	Client      *http.Client
}

func NewOpenAICompatibleEmbedderFromEnv() *OpenAICompatibleEmbedder {
	apiKey := strings.TrimSpace(os.Getenv("AI_GATEWAY_VIRTUAL_KEY"))
	if apiKey == "" {
		return nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_EMBEDDINGS_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("AI_GATEWAY_UPSTREAM_URL")), "/")
	}
	if baseURL == "" {
		baseURL = "http://localhost:9111/v1"
	} else if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	model := strings.TrimSpace(os.Getenv("MULTICA_EMBEDDINGS_MODEL"))
	if model == "" {
		model = DefaultEmbeddingModel
	}
	dims := DefaultEmbeddingDimension
	if raw := strings.TrimSpace(os.Getenv("MULTICA_EMBEDDINGS_DIMENSIONS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			dims = parsed
		}
	}
	return &OpenAICompatibleEmbedder{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		ModelName:   model,
		DimensionsN: dims,
		Client:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *OpenAICompatibleEmbedder) Model() string {
	if e == nil {
		return ""
	}
	return e.ModelName
}

func (e *OpenAICompatibleEmbedder) Dimensions() int {
	if e == nil {
		return 0
	}
	return e.DimensionsN
}

func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, input string) ([]float32, error) {
	if e == nil || strings.TrimSpace(e.APIKey) == "" {
		return nil, ErrEmbeddingNotConfigured
	}
	endpoint := e.BaseURL
	if !strings.HasSuffix(endpoint, "/embeddings") {
		endpoint += "/embeddings"
	}
	body := map[string]any{
		"model": e.ModelName,
		"input": input,
	}
	if e.DimensionsN > 0 {
		body["dimensions"] = e.DimensionsN
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request failed with status %d", resp.StatusCode)
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding response did not include a vector")
	}
	return decoded.Data[0].Embedding, nil
}

func (s *ProjectKnowledgeService) EmbeddingsConfigured() bool {
	return s != nil && !isNilEmbedder(s.Embedder)
}

type WikiPage struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	ProjectID   string          `json:"project_id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	SourceRefs  json.RawMessage `json:"source_refs"`
	Status      string          `json:"status"`
	UpdatedBy   *string         `json:"updated_by"`
	ReviewedAt  *string         `json:"reviewed_at"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type MemoryItem struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	ProjectID   string          `json:"project_id"`
	IssueID     *string         `json:"issue_id"`
	TaskID      *string         `json:"task_id"`
	CommentID   *string         `json:"comment_id"`
	Kind        string          `json:"kind"`
	Outcome     string          `json:"outcome"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Symptom     string          `json:"symptom"`
	Cause       string          `json:"cause"`
	FixPath     string          `json:"fix_path"`
	Commands    json.RawMessage `json:"commands"`
	RepoRefs    json.RawMessage `json:"repo_refs"`
	Tags        []string        `json:"tags"`
	Confidence  int32           `json:"confidence"`
	ExpiresAt   *string         `json:"expires_at"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type KnowledgeSearchResult struct {
	TargetType   string          `json:"target_type"`
	Score        float64         `json:"score"`
	MatchType    string          `json:"match_type"`
	VectorScore  *float64        `json:"vector_score,omitempty"`
	KeywordScore *float64        `json:"keyword_score,omitempty"`
	WikiPage     *WikiPage       `json:"wiki_page,omitempty"`
	MemoryItem   *MemoryItem     `json:"memory_item,omitempty"`
	Snippet      string          `json:"snippet"`
	SourceRefs   json.RawMessage `json:"source_refs,omitempty"`
}

type KnowledgeSearchResultSet struct {
	Results        []KnowledgeSearchResult `json:"results"`
	SearchMode     string                  `json:"search_mode"`
	EmbeddingModel string                  `json:"embedding_model,omitempty"`
	FallbackUsed   bool                    `json:"fallback_used"`
}

type RelevantKnowledge struct {
	TargetType   string   `json:"target_type"`
	ID           string   `json:"id"`
	Slug         string   `json:"slug,omitempty"`
	Kind         string   `json:"kind"`
	Outcome      string   `json:"outcome"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	IssueID      string   `json:"issue_id,omitempty"`
	TaskID       string   `json:"task_id,omitempty"`
	CommentID    string   `json:"comment_id,omitempty"`
	Confidence   int32    `json:"confidence"`
	Score        float64  `json:"score"`
	MatchType    string   `json:"match_type,omitempty"`
	VectorScore  *float64 `json:"vector_score,omitempty"`
	KeywordScore *float64 `json:"keyword_score,omitempty"`
	Snippet      string   `json:"snippet,omitempty"`
	SourceReason string   `json:"source_reason,omitempty"`
}

type EmbeddingBackfillResult struct {
	Queued  int `json:"queued"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

type EmbeddingJobProcessResult struct {
	Processed int
	Succeeded int
	Failed    int
}

type RetrievalLog struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspace_id"`
	ProjectID         string          `json:"project_id"`
	IssueID           *string         `json:"issue_id"`
	TaskID            *string         `json:"task_id"`
	QueryText         string          `json:"query_text"`
	ReturnedItems     json.RawMessage `json:"returned_items"`
	SearchMode        string          `json:"search_mode"`
	QueryContext      json.RawMessage `json:"query_context"`
	Candidates        json.RawMessage `json:"candidates"`
	SelectedItems     json.RawMessage `json:"selected_items"`
	InjectedText      string          `json:"injected_text"`
	TokenBudget       *int32          `json:"token_budget"`
	InjectedItemCount int32           `json:"injected_item_count"`
	PromptSectionHash *string         `json:"prompt_section_hash"`
	Status            string          `json:"status"`
	Error             *string         `json:"error"`
	TaskOutcome       *string         `json:"task_outcome"`
	Helpfulness       *int32          `json:"helpfulness"`
	Feedback          *string         `json:"feedback"`
	FeedbackNote      *string         `json:"feedback_note"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type RetrievalLogInput struct {
	WorkspaceID       pgtype.UUID
	ProjectID         pgtype.UUID
	IssueID           pgtype.UUID
	TaskID            pgtype.UUID
	QueryText         string
	SearchMode        string
	QueryContext      any
	Candidates        []KnowledgeSearchResult
	SelectedItems     []RelevantKnowledge
	InjectedText      string
	TokenBudget       int32
	InjectedItemCount int32
	Status            string
	Error             string
}

type CreateWikiPageInput struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	Slug        string
	Title       string
	Body        string
	SourceRefs  json.RawMessage
	Status      string
	UpdatedBy   pgtype.UUID
	ReviewedAt  pgtype.Timestamptz
}

type UpdateWikiPageInput struct {
	ID         pgtype.UUID
	ProjectID  pgtype.UUID
	Title      *string
	Body       *string
	SourceRefs *json.RawMessage
	Status     *string
	UpdatedBy  pgtype.UUID
	ReviewedAt pgtype.Timestamptz
}

type CreateMemoryItemInput struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	IssueID     pgtype.UUID
	TaskID      pgtype.UUID
	CommentID   pgtype.UUID
	Kind        string
	Outcome     string
	Title       string
	Summary     string
	Symptom     string
	Cause       string
	FixPath     string
	Commands    json.RawMessage
	RepoRefs    json.RawMessage
	Tags        []string
	Confidence  int32
	ExpiresAt   pgtype.Timestamptz
}

type UpdateMemoryItemInput struct {
	ID         pgtype.UUID
	ProjectID  pgtype.UUID
	Kind       *string
	Outcome    *string
	Title      *string
	Summary    *string
	Symptom    *string
	Cause      *string
	FixPath    *string
	Commands   *json.RawMessage
	RepoRefs   *json.RawMessage
	Tags       []string
	TagsSet    bool
	Confidence *int32
	ExpiresAt  pgtype.Timestamptz
}

func (s *ProjectKnowledgeService) ListWikiPages(ctx context.Context, workspaceID, projectID pgtype.UUID) ([]WikiPage, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at, created_at, updated_at
		FROM project_wiki_page
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY updated_at DESC
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WikiPage{}
	for rows.Next() {
		page, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, page)
	}
	return out, rows.Err()
}

func (s *ProjectKnowledgeService) ListCanonicalWikiPages(ctx context.Context, workspaceID, projectID pgtype.UUID, limit int32) ([]WikiPage, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at, created_at, updated_at
		FROM project_wiki_page
		WHERE workspace_id = $1 AND project_id = $2 AND status != 'archived'
		ORDER BY CASE status WHEN 'reviewed' THEN 0 WHEN 'draft' THEN 1 ELSE 2 END, updated_at DESC
		LIMIT $3
	`, workspaceID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WikiPage{}
	for rows.Next() {
		page, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, page)
	}
	return out, rows.Err()
}

func (s *ProjectKnowledgeService) CreateWikiPage(ctx context.Context, in CreateWikiPageInput) (WikiPage, error) {
	if in.Status == "" {
		in.Status = "draft"
	}
	if len(in.SourceRefs) == 0 {
		in.SourceRefs = json.RawMessage("[]")
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return WikiPage{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO project_wiki_page (workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
		RETURNING id, workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at, created_at, updated_at
	`, in.WorkspaceID, in.ProjectID, in.Slug, in.Title, in.Body, string(in.SourceRefs), in.Status, in.UpdatedBy, in.ReviewedAt)
	page, err := scanWikiPage(row)
	if err != nil {
		tx.Rollback(ctx)
		return WikiPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WikiPage{}, err
	}
	s.enqueueWikiPageEmbeddingBestEffort(ctx, in.WorkspaceID, in.ProjectID, util.MustParseUUID(page.ID), page)
	return page, nil
}

func (s *ProjectKnowledgeService) UpdateWikiPage(ctx context.Context, in UpdateWikiPageInput) (WikiPage, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return WikiPage{}, err
	}
	row := tx.QueryRow(ctx, `
		UPDATE project_wiki_page SET
			title = COALESCE($3, title),
			body = COALESCE($4, body),
			source_refs = COALESCE($5::jsonb, source_refs),
			status = COALESCE($6, status),
			updated_by = COALESCE($7, updated_by),
			reviewed_at = COALESCE($8, reviewed_at),
			updated_at = now()
		WHERE id = $1 AND project_id = $2
		RETURNING id, workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at, created_at, updated_at
	`, in.ID, in.ProjectID, in.Title, in.Body, rawJSONPtrToString(in.SourceRefs), in.Status, in.UpdatedBy, in.ReviewedAt)
	page, err := scanWikiPage(row)
	if err != nil {
		tx.Rollback(ctx)
		return WikiPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WikiPage{}, err
	}
	s.enqueueWikiPageEmbeddingBestEffort(ctx, util.MustParseUUID(page.WorkspaceID), in.ProjectID, in.ID, page)
	return page, nil
}

func (s *ProjectKnowledgeService) DeleteWikiPage(ctx context.Context, projectID, id pgtype.UUID) error {
	return s.execTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM project_memory_embedding
			WHERE target_type = $1 AND target_id = $2 AND project_id = $3
		`, KnowledgeTargetWikiPage, id, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM project_knowledge_embedding_job
			WHERE target_type = $1 AND target_id = $2 AND project_id = $3
		`, KnowledgeTargetWikiPage, id, projectID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM project_wiki_page WHERE id = $1 AND project_id = $2`, id, projectID)
		return err
	})
}

func (s *ProjectKnowledgeService) ListMemoryItems(ctx context.Context, workspaceID, projectID pgtype.UUID, limit int32) ([]MemoryItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
		       commands, repo_refs, array_to_json(tags)::jsonb, confidence, expires_at, created_at, updated_at
		FROM project_memory_item
		WHERE workspace_id = $1 AND project_id = $2 AND (expires_at IS NULL OR expires_at > now())
		ORDER BY updated_at DESC
		LIMIT $3
	`, workspaceID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		item, err := scanMemoryItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ProjectKnowledgeService) CreateMemoryItem(ctx context.Context, in CreateMemoryItemInput) (MemoryItem, error) {
	if len(in.Commands) == 0 {
		in.Commands = json.RawMessage("[]")
	}
	if len(in.RepoRefs) == 0 {
		in.RepoRefs = json.RawMessage("[]")
	}
	if in.Confidence == 0 {
		in.Confidence = 60
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return MemoryItem{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO project_memory_item (
			workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
			commands, repo_refs, tags, confidence, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14::jsonb, $15::text[], $16, $17
		)
		RETURNING id, workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
		          commands, repo_refs, array_to_json(tags)::jsonb, confidence, expires_at, created_at, updated_at
	`, in.WorkspaceID, in.ProjectID, in.IssueID, in.TaskID, in.CommentID, in.Kind, in.Outcome, in.Title, in.Summary, in.Symptom, in.Cause, in.FixPath, string(in.Commands), string(in.RepoRefs), in.Tags, in.Confidence, in.ExpiresAt)
	item, err := scanMemoryItem(row)
	if err != nil {
		tx.Rollback(ctx)
		return MemoryItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MemoryItem{}, err
	}
	s.enqueueMemoryItemEmbeddingBestEffort(ctx, in.WorkspaceID, in.ProjectID, util.MustParseUUID(item.ID), item)
	return item, nil
}

func (s *ProjectKnowledgeService) UpdateMemoryItem(ctx context.Context, in UpdateMemoryItemInput) (MemoryItem, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return MemoryItem{}, err
	}
	var tags any
	if in.TagsSet {
		tags = in.Tags
	}
	row := tx.QueryRow(ctx, `
		UPDATE project_memory_item SET
			kind = COALESCE($3, kind),
			outcome = COALESCE($4, outcome),
			title = COALESCE($5, title),
			summary = COALESCE($6, summary),
			symptom = COALESCE($7, symptom),
			cause = COALESCE($8, cause),
			fix_path = COALESCE($9, fix_path),
			commands = COALESCE($10::jsonb, commands),
			repo_refs = COALESCE($11::jsonb, repo_refs),
			tags = COALESCE($12::text[], tags),
			confidence = COALESCE($13, confidence),
			expires_at = COALESCE($14, expires_at),
			updated_at = now()
		WHERE id = $1 AND project_id = $2
		RETURNING id, workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
		          commands, repo_refs, array_to_json(tags)::jsonb, confidence, expires_at, created_at, updated_at
	`, in.ID, in.ProjectID, in.Kind, in.Outcome, in.Title, in.Summary, in.Symptom, in.Cause, in.FixPath, rawJSONPtrToString(in.Commands), rawJSONPtrToString(in.RepoRefs), tags, in.Confidence, in.ExpiresAt)
	item, err := scanMemoryItem(row)
	if err != nil {
		tx.Rollback(ctx)
		return MemoryItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MemoryItem{}, err
	}
	s.enqueueMemoryItemEmbeddingBestEffort(ctx, util.MustParseUUID(item.WorkspaceID), in.ProjectID, in.ID, item)
	return item, nil
}

func (s *ProjectKnowledgeService) DeleteMemoryItem(ctx context.Context, projectID, id pgtype.UUID) error {
	return s.execTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			DELETE FROM project_memory_embedding
			WHERE target_type = $1 AND target_id = $2 AND project_id = $3
		`, KnowledgeTargetMemoryItem, id, projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM project_knowledge_embedding_job
			WHERE target_type = $1 AND target_id = $2 AND project_id = $3
		`, KnowledgeTargetMemoryItem, id, projectID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM project_memory_item WHERE id = $1 AND project_id = $2`, id, projectID)
		return err
	})
}

func (s *ProjectKnowledgeService) Search(ctx context.Context, workspaceID, projectID pgtype.UUID, query string, limit int32) ([]KnowledgeSearchResult, error) {
	set, err := s.SearchWithMode(ctx, workspaceID, projectID, query, limit)
	if err != nil {
		return nil, err
	}
	return set.Results, nil
}

func (s *ProjectKnowledgeService) SearchWithMode(ctx context.Context, workspaceID, projectID pgtype.UUID, query string, limit int32) (KnowledgeSearchResultSet, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	candidateLimit := limit * 8
	if candidateLimit < 20 {
		candidateLimit = 20
	}
	if candidateLimit > 100 {
		candidateLimit = 100
	}

	tsQuery := knowledgeSearchTSQuery(query)
	var vectorLiteral *string
	var embeddingModel string
	if s.EmbeddingsConfigured() {
		vector, err := s.Embedder.Embed(ctx, query)
		if err != nil {
			slog.Debug("project knowledge vector stream skipped", "project_id", util.UUIDToString(projectID), "error", err)
		} else if len(vector) != DefaultEmbeddingDimension {
			slog.Warn("project knowledge query embedding dimension mismatch", "project_id", util.UUIDToString(projectID), "got", len(vector), "want", DefaultEmbeddingDimension)
		} else {
			literal := embeddingToVectorLiteral(vector)
			vectorLiteral = &literal
			embeddingModel = s.Embedder.Model()
		}
	}
	if vectorLiteral == nil && tsQuery == "" {
		if !s.EmbeddingsConfigured() {
			return KnowledgeSearchResultSet{Results: []KnowledgeSearchResult{}, SearchMode: "none"}, ErrEmbeddingNotConfigured
		}
		return KnowledgeSearchResultSet{Results: []KnowledgeSearchResult{}, SearchMode: "none", EmbeddingModel: embeddingModel}, nil
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return KnowledgeSearchResultSet{}, err
	}
	defer tx.Rollback(ctx)

	if vectorLiteral == nil {
		results, err := s.searchKeywordOnly(ctx, tx, workspaceID, projectID, tsQuery, limit, candidateLimit)
		return KnowledgeSearchResultSet{Results: results, SearchMode: "keyword"}, err
	}
	if tsQuery == "" {
		results, err := s.searchVectorOnly(ctx, tx, workspaceID, projectID, *vectorLiteral, embeddingModel, limit, candidateLimit)
		return KnowledgeSearchResultSet{Results: results, SearchMode: "vector", EmbeddingModel: embeddingModel}, err
	}
	results, err := s.searchHybrid(ctx, tx, workspaceID, projectID, *vectorLiteral, embeddingModel, tsQuery, limit, candidateLimit)
	return KnowledgeSearchResultSet{Results: results, SearchMode: "hybrid", EmbeddingModel: embeddingModel}, err
}

func (s *ProjectKnowledgeService) searchHybrid(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, vectorLiteral, embeddingModel, tsQuery string, limit, candidateLimit int32) ([]KnowledgeSearchResult, error) {
	rows, err := tx.Query(ctx, `
		WITH vector_ranked AS (
			SELECT target_type, target_id,
			       1 - (embedding <=> $3::vector) AS vector_score,
			       row_number() OVER (ORDER BY embedding <=> $3::vector) AS vector_rank
			FROM project_memory_embedding
			WHERE workspace_id = $1 AND project_id = $2 AND embedding_model = $4
			ORDER BY embedding <=> $3::vector
			LIMIT $6
		),
		keyword_query AS (
			SELECT to_tsquery('simple', $5) AS q
		),
		keyword_ranked AS (
			SELECT 'memory_item'::text AS target_type, m.id AS target_id,
			       ts_rank_cd(m.search_document, k.q) AS keyword_score,
			       row_number() OVER (ORDER BY ts_rank_cd(m.search_document, k.q) DESC, m.updated_at DESC) AS keyword_rank
			FROM project_memory_item m, keyword_query k
			WHERE m.workspace_id = $1 AND m.project_id = $2
			  AND (m.expires_at IS NULL OR m.expires_at > now())
			  AND m.search_document @@ k.q
			UNION ALL
			SELECT 'wiki_page'::text AS target_type, w.id AS target_id,
			       ts_rank_cd(w.search_document, k.q) AS keyword_score,
			       row_number() OVER (ORDER BY ts_rank_cd(w.search_document, k.q) DESC, w.updated_at DESC) AS keyword_rank
			FROM project_wiki_page w, keyword_query k
			WHERE w.workspace_id = $1 AND w.project_id = $2
			  AND w.status != 'archived'
			  AND w.search_document @@ k.q
			LIMIT $6
		),
		combined AS (
			SELECT target_type, target_id,
			       max(vector_score) AS vector_score,
			       NULL::double precision AS keyword_score,
			       min(vector_rank) AS vector_rank,
			       NULL::bigint AS keyword_rank
			FROM vector_ranked
			GROUP BY target_type, target_id
			UNION ALL
			SELECT target_type, target_id,
			       NULL::double precision AS vector_score,
			       max(keyword_score) AS keyword_score,
			       NULL::bigint AS vector_rank,
			       min(keyword_rank) AS keyword_rank
			FROM keyword_ranked
			GROUP BY target_type, target_id
		),
		fused AS (
			SELECT target_type, target_id,
			       coalesce(max(vector_score), 0) AS vector_score,
			       coalesce(max(keyword_score), 0) AS keyword_score,
			       coalesce(1.0::double precision / (60.0 + (min(vector_rank) FILTER (WHERE vector_rank IS NOT NULL))::double precision), 0.0) +
			       coalesce(1.0::double precision / (60.0 + (min(keyword_rank) FILTER (WHERE keyword_rank IS NOT NULL))::double precision), 0.0) AS score,
			       CASE
			           WHEN min(vector_rank) IS NOT NULL AND min(keyword_rank) IS NOT NULL THEN 'hybrid'
			           WHEN min(vector_rank) IS NOT NULL THEN 'vector'
			           ELSE 'keyword'
			       END AS match_type
			FROM combined
			GROUP BY target_type, target_id
		),
		memory_rows AS (
			SELECT f.target_type, f.score, nullif(f.vector_score, 0) AS vector_score, nullif(f.keyword_score, 0) AS keyword_score, f.match_type,
			       m.id, m.workspace_id, m.project_id, m.issue_id, m.task_id, m.comment_id, m.kind, m.outcome, m.title, m.summary, m.symptom, m.cause, m.fix_path,
			       m.commands, m.repo_refs, array_to_json(m.tags)::jsonb AS tags_json, m.confidence, m.expires_at, m.created_at, m.updated_at,
			       NULL::text AS slug, NULL::text AS body, NULL::jsonb AS source_refs, NULL::text AS status, NULL::uuid AS updated_by, NULL::timestamptz AS reviewed_at
			FROM fused f
			JOIN project_memory_item m ON f.target_type = 'memory_item' AND m.id = f.target_id
			WHERE m.expires_at IS NULL OR m.expires_at > now()
		),
		wiki_rows AS (
			SELECT f.target_type, f.score, nullif(f.vector_score, 0) AS vector_score, nullif(f.keyword_score, 0) AS keyword_score, f.match_type,
			       w.id, w.workspace_id, w.project_id, NULL::uuid AS issue_id, NULL::uuid AS task_id, NULL::uuid AS comment_id,
			       'wiki_page'::text AS kind, w.status AS outcome, w.title, left(w.body, 800) AS summary, ''::text AS symptom, ''::text AS cause, ''::text AS fix_path,
			       '[]'::jsonb AS commands, '[]'::jsonb AS repo_refs, '[]'::jsonb AS tags_json, 90::int AS confidence, NULL::timestamptz AS expires_at, w.created_at, w.updated_at,
			       w.slug, w.body, w.source_refs, w.status, w.updated_by, w.reviewed_at
			FROM fused f
			JOIN project_wiki_page w ON f.target_type = 'wiki_page' AND w.id = f.target_id
			WHERE w.status != 'archived'
		)
		SELECT * FROM memory_rows
		UNION ALL
		SELECT * FROM wiki_rows
		ORDER BY score DESC
		LIMIT $7
	`, workspaceID, projectID, vectorLiteral, embeddingModel, tsQuery, candidateLimit, limit)
	if err != nil {
		return nil, err
	}
	return scanKnowledgeSearchResults(rows)
}

func (s *ProjectKnowledgeService) searchVectorOnly(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, vectorLiteral, embeddingModel string, limit, candidateLimit int32) ([]KnowledgeSearchResult, error) {
	rows, err := tx.Query(ctx, `
		WITH fused AS (
			SELECT target_type, target_id,
			       1 - (embedding <=> $3::vector) AS score,
			       1 - (embedding <=> $3::vector) AS vector_score,
			       NULL::double precision AS keyword_score,
			       'vector'::text AS match_type
			FROM project_memory_embedding
			WHERE workspace_id = $1 AND project_id = $2 AND embedding_model = $4
			ORDER BY embedding <=> $3::vector
			LIMIT $5
		),
		memory_rows AS (
			SELECT f.target_type, f.score, f.vector_score, f.keyword_score, f.match_type,
			       m.id, m.workspace_id, m.project_id, m.issue_id, m.task_id, m.comment_id, m.kind, m.outcome, m.title, m.summary, m.symptom, m.cause, m.fix_path,
			       m.commands, m.repo_refs, array_to_json(m.tags)::jsonb AS tags_json, m.confidence, m.expires_at, m.created_at, m.updated_at,
			       NULL::text AS slug, NULL::text AS body, NULL::jsonb AS source_refs, NULL::text AS status, NULL::uuid AS updated_by, NULL::timestamptz AS reviewed_at
			FROM fused f
			JOIN project_memory_item m ON f.target_type = 'memory_item' AND m.id = f.target_id
			WHERE m.expires_at IS NULL OR m.expires_at > now()
		),
		wiki_rows AS (
			SELECT f.target_type, f.score, f.vector_score, f.keyword_score, f.match_type,
			       w.id, w.workspace_id, w.project_id, NULL::uuid AS issue_id, NULL::uuid AS task_id, NULL::uuid AS comment_id,
			       'wiki_page'::text AS kind, w.status AS outcome, w.title, left(w.body, 800) AS summary, ''::text AS symptom, ''::text AS cause, ''::text AS fix_path,
			       '[]'::jsonb AS commands, '[]'::jsonb AS repo_refs, '[]'::jsonb AS tags_json, 90::int AS confidence, NULL::timestamptz AS expires_at, w.created_at, w.updated_at,
			       w.slug, w.body, w.source_refs, w.status, w.updated_by, w.reviewed_at
			FROM fused f
			JOIN project_wiki_page w ON f.target_type = 'wiki_page' AND w.id = f.target_id
			WHERE w.status != 'archived'
		)
		SELECT * FROM memory_rows
		UNION ALL
		SELECT * FROM wiki_rows
		ORDER BY score DESC
		LIMIT $6
	`, workspaceID, projectID, vectorLiteral, embeddingModel, candidateLimit, limit)
	if err != nil {
		return nil, err
	}
	return scanKnowledgeSearchResults(rows)
}

func (s *ProjectKnowledgeService) searchKeywordOnly(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, tsQuery string, limit, candidateLimit int32) ([]KnowledgeSearchResult, error) {
	rows, err := tx.Query(ctx, `
		WITH keyword_query AS (
			SELECT to_tsquery('simple', $3) AS q
		),
		fused AS (
			SELECT 'memory_item'::text AS target_type, m.id AS target_id,
			       ts_rank_cd(m.search_document, k.q) AS score,
			       NULL::double precision AS vector_score,
			       ts_rank_cd(m.search_document, k.q) AS keyword_score,
			       'keyword'::text AS match_type,
			       m.updated_at
			FROM project_memory_item m, keyword_query k
			WHERE m.workspace_id = $1 AND m.project_id = $2
			  AND (m.expires_at IS NULL OR m.expires_at > now())
			  AND m.search_document @@ k.q
			UNION ALL
			SELECT 'wiki_page'::text AS target_type, w.id AS target_id,
			       ts_rank_cd(w.search_document, k.q) AS score,
			       NULL::double precision AS vector_score,
			       ts_rank_cd(w.search_document, k.q) AS keyword_score,
			       'keyword'::text AS match_type,
			       w.updated_at
			FROM project_wiki_page w, keyword_query k
			WHERE w.workspace_id = $1 AND w.project_id = $2
			  AND w.status != 'archived'
			  AND w.search_document @@ k.q
			ORDER BY score DESC, updated_at DESC
			LIMIT $4
		),
		memory_rows AS (
			SELECT f.target_type, f.score, f.vector_score, f.keyword_score, f.match_type,
			       m.id, m.workspace_id, m.project_id, m.issue_id, m.task_id, m.comment_id, m.kind, m.outcome, m.title, m.summary, m.symptom, m.cause, m.fix_path,
			       m.commands, m.repo_refs, array_to_json(m.tags)::jsonb AS tags_json, m.confidence, m.expires_at, m.created_at, m.updated_at,
			       NULL::text AS slug, NULL::text AS body, NULL::jsonb AS source_refs, NULL::text AS status, NULL::uuid AS updated_by, NULL::timestamptz AS reviewed_at
			FROM fused f
			JOIN project_memory_item m ON f.target_type = 'memory_item' AND m.id = f.target_id
		),
		wiki_rows AS (
			SELECT f.target_type, f.score, f.vector_score, f.keyword_score, f.match_type,
			       w.id, w.workspace_id, w.project_id, NULL::uuid AS issue_id, NULL::uuid AS task_id, NULL::uuid AS comment_id,
			       'wiki_page'::text AS kind, w.status AS outcome, w.title, left(w.body, 800) AS summary, ''::text AS symptom, ''::text AS cause, ''::text AS fix_path,
			       '[]'::jsonb AS commands, '[]'::jsonb AS repo_refs, '[]'::jsonb AS tags_json, 90::int AS confidence, NULL::timestamptz AS expires_at, w.created_at, w.updated_at,
			       w.slug, w.body, w.source_refs, w.status, w.updated_by, w.reviewed_at
			FROM fused f
			JOIN project_wiki_page w ON f.target_type = 'wiki_page' AND w.id = f.target_id
		)
		SELECT * FROM memory_rows
		UNION ALL
		SELECT * FROM wiki_rows
		ORDER BY score DESC
		LIMIT $5
	`, workspaceID, projectID, tsQuery, candidateLimit, limit)
	if err != nil {
		return nil, err
	}
	return scanKnowledgeSearchResults(rows)
}

func (s *ProjectKnowledgeService) RelevantMemoryForIssue(ctx context.Context, issue db.Issue, limit int32) ([]KnowledgeSearchResult, error) {
	if !issue.ProjectID.Valid {
		return []KnowledgeSearchResult{}, nil
	}
	query := strings.Join([]string{issue.Title, issue.Description.String}, "\n\n")
	return s.Search(ctx, issue.WorkspaceID, issue.ProjectID, query, limit)
}

func (s *ProjectKnowledgeService) CanonicalWikiContextForIssue(ctx context.Context, issue db.Issue, limit int32) ([]KnowledgeSearchResult, error) {
	if !issue.ProjectID.Valid {
		return []KnowledgeSearchResult{}, nil
	}
	query := strings.Join([]string{issue.Title, issue.Description.String}, "\n\n")
	return s.CanonicalWikiContext(ctx, issue.WorkspaceID, issue.ProjectID, query, limit)
}

func (s *ProjectKnowledgeService) CanonicalWikiContext(ctx context.Context, workspaceID, projectID pgtype.UUID, query string, limit int32) ([]KnowledgeSearchResult, error) {
	set, err := s.CanonicalWikiContextWithMode(ctx, workspaceID, projectID, query, limit)
	if err != nil {
		return nil, err
	}
	return set.Results, nil
}

func (s *ProjectKnowledgeService) CanonicalWikiContextWithMode(ctx context.Context, workspaceID, projectID pgtype.UUID, query string, limit int32) (KnowledgeSearchResultSet, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	set, err := s.SearchWithMode(ctx, workspaceID, projectID, query, limit*3)
	if err == nil {
		wiki := canonicalWikiResultsFromSearch(set.Results, int(limit))
		if len(wiki) > 0 {
			set.Results = wiki
			return set, nil
		}
	} else if !errors.Is(err, ErrEmbeddingNotConfigured) {
		return KnowledgeSearchResultSet{}, err
	}

	pages, fallbackErr := s.ListCanonicalWikiPages(ctx, workspaceID, projectID, limit)
	if fallbackErr != nil {
		if err != nil {
			return KnowledgeSearchResultSet{}, err
		}
		return KnowledgeSearchResultSet{}, fallbackErr
	}
	fallbackResults := wikiPagesToSearchResults(pages)
	mode := "fallback"
	if len(fallbackResults) == 0 {
		mode = "none"
	}
	return KnowledgeSearchResultSet{
		Results:      fallbackResults,
		SearchMode:   mode,
		FallbackUsed: true,
	}, nil
}

func canonicalWikiResultsFromSearch(results []KnowledgeSearchResult, limit int) []KnowledgeSearchResult {
	if limit <= 0 {
		limit = len(results)
	}
	wiki := make([]KnowledgeSearchResult, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		if result.WikiPage == nil || result.WikiPage.Status == "archived" {
			continue
		}
		if _, ok := seen[result.WikiPage.ID]; ok {
			continue
		}
		seen[result.WikiPage.ID] = struct{}{}
		wiki = append(wiki, result)
	}
	sort.SliceStable(wiki, func(i, j int) bool {
		ri := wikiStatusRank(wiki[i].WikiPage.Status)
		rj := wikiStatusRank(wiki[j].WikiPage.Status)
		if ri != rj {
			return ri < rj
		}
		if wiki[i].Score != wiki[j].Score {
			return wiki[i].Score > wiki[j].Score
		}
		return wiki[i].WikiPage.UpdatedAt > wiki[j].WikiPage.UpdatedAt
	})
	if len(wiki) > limit {
		wiki = wiki[:limit]
	}
	return wiki
}

func wikiStatusRank(status string) int {
	switch strings.TrimSpace(status) {
	case "reviewed":
		return 0
	case "draft":
		return 1
	default:
		return 2
	}
}

func wikiPagesToSearchResults(pages []WikiPage) []KnowledgeSearchResult {
	out := make([]KnowledgeSearchResult, 0, len(pages))
	for i, page := range pages {
		page := page
		score := 1.0 - float64(i)*0.01
		if score < 0 {
			score = 0
		}
		out = append(out, KnowledgeSearchResult{
			TargetType: KnowledgeTargetWikiPage,
			Score:      score,
			MatchType:  "canonical_fallback",
			WikiPage:   &page,
			Snippet:    truncateForSummary(page.Body, 500),
			SourceRefs: page.SourceRefs,
		})
	}
	return out
}

func (s *ProjectKnowledgeService) LogRetrieval(ctx context.Context, in RetrievalLogInput) {
	if s == nil || s.Tx == nil {
		return
	}
	returned := []byte("[]")
	if in.Candidates != nil {
		returned, _ = json.Marshal(in.Candidates)
	}
	candidates := returned
	selected := []byte("[]")
	if in.SelectedItems != nil {
		selected, _ = json.Marshal(in.SelectedItems)
	}
	queryContext := []byte("{}")
	if in.QueryContext != nil {
		queryContext, _ = json.Marshal(in.QueryContext)
	}
	if in.Status == "" {
		in.Status = "injected"
	}
	if in.SearchMode == "" {
		in.SearchMode = "none"
	}
	if len(returned) == 0 || string(returned) == "null" {
		returned = []byte("[]")
	}
	if len(candidates) == 0 || string(candidates) == "null" {
		candidates = []byte("[]")
	}
	if len(selected) == 0 || string(selected) == "null" {
		selected = []byte("[]")
	}
	if len(queryContext) == 0 || string(queryContext) == "null" {
		queryContext = []byte("{}")
	}
	var promptHash any
	if strings.TrimSpace(in.InjectedText) != "" {
		sum := sha256.Sum256([]byte(in.InjectedText))
		promptHash = hex.EncodeToString(sum[:])
	}
	var tokenBudget any
	if in.TokenBudget > 0 {
		tokenBudget = in.TokenBudget
	}
	if execErr := s.execTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO project_knowledge_retrieval_log (
				workspace_id, project_id, issue_id, task_id, query_text, returned_items,
				search_mode, query_context, candidates, selected_items, injected_text,
				token_budget, injected_item_count, prompt_section_hash, status, error
			)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8::jsonb, $9::jsonb, $10::jsonb, $11, $12, $13, $14, $15, $16)
		`, in.WorkspaceID, in.ProjectID, in.IssueID, in.TaskID, in.QueryText, string(returned),
			in.SearchMode, string(queryContext), string(candidates), string(selected), in.InjectedText,
			tokenBudget, in.InjectedItemCount, promptHash, in.Status, nullableString(in.Error))
		return err
	}); execErr != nil {
		slog.Debug("failed to write project knowledge retrieval log", "error", execErr)
	}
}

func (s *ProjectKnowledgeService) ListRetrievalLogs(ctx context.Context, workspaceID, projectID pgtype.UUID, limit int32) ([]RetrievalLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, retrievalLogSelectSQL+`
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, workspaceID, projectID, limit)
	if err != nil {
		return nil, err
	}
	return scanRetrievalLogs(rows)
}

func (s *ProjectKnowledgeService) ListRetrievalLogsForIssue(ctx context.Context, workspaceID, issueID pgtype.UUID, limit int32) ([]RetrievalLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, retrievalLogSelectSQL+`
		WHERE workspace_id = $1 AND issue_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, workspaceID, issueID, limit)
	if err != nil {
		return nil, err
	}
	return scanRetrievalLogs(rows)
}

func (s *ProjectKnowledgeService) ListRetrievalLogsForTask(ctx context.Context, workspaceID, taskID pgtype.UUID, limit int32) ([]RetrievalLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, retrievalLogSelectSQL+`
		WHERE workspace_id = $1 AND task_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, workspaceID, taskID, limit)
	if err != nil {
		return nil, err
	}
	return scanRetrievalLogs(rows)
}

func (s *ProjectKnowledgeService) UpdateRetrievalFeedback(ctx context.Context, workspaceID, projectID, logID pgtype.UUID, feedback, note string, helpfulness *int32) (RetrievalLog, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return RetrievalLog{}, err
	}
	var helpfulnessValue any
	if helpfulness != nil {
		helpfulnessValue = *helpfulness
	}
	row := tx.QueryRow(ctx, retrievalLogSelectSQL+`
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, logID, workspaceID, projectID)
	existing, err := scanRetrievalLog(row)
	if err != nil {
		tx.Rollback(ctx)
		return RetrievalLog{}, err
	}
	row = tx.QueryRow(ctx, `
		UPDATE project_knowledge_retrieval_log
		SET feedback = $4,
		    feedback_note = $5,
		    helpfulness = COALESCE($6, helpfulness),
		    updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
		RETURNING id, workspace_id, project_id, issue_id, task_id, query_text, returned_items,
		          search_mode, query_context, candidates, selected_items, injected_text,
		          token_budget, injected_item_count, prompt_section_hash, status, error, task_outcome,
		          helpfulness, feedback, feedback_note, created_at, updated_at
	`, logID, workspaceID, projectID, nullableString(feedback), nullableString(note), helpfulnessValue)
	updated, err := scanRetrievalLog(row)
	if err != nil {
		tx.Rollback(ctx)
		return existing, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RetrievalLog{}, err
	}
	return updated, nil
}

func (s *ProjectKnowledgeService) UpdateRetrievalOutcomeForTask(ctx context.Context, taskID pgtype.UUID, outcome string) {
	if s == nil || !taskID.Valid {
		return
	}
	if err := s.execTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE project_knowledge_retrieval_log
			SET task_outcome = $2, updated_at = now()
			WHERE task_id = $1 AND task_outcome IS NULL
		`, taskID, outcome)
		return err
	}); err != nil {
		slog.Debug("failed to update project knowledge retrieval outcome", "task_id", util.UUIDToString(taskID), "error", err)
	}
}

func (s *ProjectKnowledgeService) CaptureTaskCompleted(ctx context.Context, task db.AgentTaskQueue, result []byte) {
	if s == nil || !task.IssueID.Valid {
		return
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil || !issue.ProjectID.Valid {
		return
	}
	output := taskCompletedOutput(result)
	if strings.TrimSpace(output) == "" {
		return
	}
	title := "Successful fix: " + issue.Title
	summary := truncateForSummary(redact.Text(output), 900)
	var payload struct {
		BranchName      string `json:"branch_name"`
		BranchCommitSHA string `json:"branch_commit_sha"`
		Comment         string `json:"comment"`
		Output          string `json:"output"`
	}
	_ = json.Unmarshal(result, &payload)
	repoRefs := []map[string]string{}
	if strings.TrimSpace(payload.BranchName) != "" {
		repoRefs = append(repoRefs, map[string]string{"branch": strings.TrimSpace(payload.BranchName), "commit": strings.TrimSpace(payload.BranchCommitSHA)})
	}
	repoRefsJSON, _ := json.Marshal(repoRefs)
	_, err = s.CreateMemoryItem(ctx, CreateMemoryItemInput{
		WorkspaceID: issue.WorkspaceID,
		ProjectID:   issue.ProjectID,
		IssueID:     issue.ID,
		TaskID:      task.ID,
		Kind:        "successful_fix",
		Outcome:     "completed",
		Title:       title,
		Summary:     summary,
		FixPath:     summary,
		RepoRefs:    repoRefsJSON,
		Tags:        []string{"auto_capture", "task_completed"},
		Confidence:  70,
	})
	if err != nil {
		slog.Debug("failed to auto-capture successful project memory", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *ProjectKnowledgeService) CaptureTaskFailed(ctx context.Context, task db.AgentTaskQueue, errMsg, failureReason string) {
	if s == nil || !task.IssueID.Valid {
		return
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil || !issue.ProjectID.Valid {
		return
	}
	symptom := strings.TrimSpace(errMsg)
	if symptom == "" {
		symptom = strings.TrimSpace(failureReason)
	}
	if symptom == "" {
		return
	}
	_, err = s.CreateMemoryItem(ctx, CreateMemoryItemInput{
		WorkspaceID: issue.WorkspaceID,
		ProjectID:   issue.ProjectID,
		IssueID:     issue.ID,
		TaskID:      task.ID,
		Kind:        "failure_recovery",
		Outcome:     "failed",
		Title:       "Failed task: " + issue.Title,
		Summary:     truncateForSummary(redact.Text(symptom), 900),
		Symptom:     truncateForSummary(redact.Text(symptom), 900),
		Cause:       failureReason,
		Tags:        []string{"auto_capture", "task_failed", failureReason},
		Confidence:  55,
	})
	if err != nil {
		slog.Debug("failed to auto-capture failed project memory", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *ProjectKnowledgeService) CaptureReviewFinding(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, nodeType string, review reviewGateResult) {
	if s == nil || !issue.ProjectID.Valid || review.Status != reviewGateStatusFail {
		return
	}
	summary := strings.TrimSpace(review.Summary)
	if summary == "" && len(review.Findings) > 0 {
		summary = review.Findings[0].Title
	}
	if summary == "" {
		return
	}
	findingsJSON, _ := json.Marshal(review.Findings)
	_, err := s.CreateMemoryItem(ctx, CreateMemoryItemInput{
		WorkspaceID: issue.WorkspaceID,
		ProjectID:   issue.ProjectID,
		IssueID:     issue.ID,
		TaskID:      task.ID,
		Kind:        "review_finding",
		Outcome:     "fail",
		Title:       fmt.Sprintf("%s finding: %s", nodeType, issue.Title),
		Summary:     truncateForSummary(redact.Text(summary), 900),
		Symptom:     truncateForSummary(redact.Text(summary), 900),
		Cause:       string(findingsJSON),
		Tags:        []string{"auto_capture", nodeType, "review_fail"},
		Confidence:  75,
	})
	if err != nil {
		slog.Debug("failed to auto-capture review finding", "task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *ProjectKnowledgeService) ToRelevant(results []KnowledgeSearchResult, limit int) []RelevantKnowledge {
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}
	out := make([]RelevantKnowledge, 0, limit)
	for _, r := range results[:limit] {
		if r.MemoryItem != nil {
			m := r.MemoryItem
			out = append(out, RelevantKnowledge{
				TargetType:   r.TargetType,
				ID:           m.ID,
				Kind:         m.Kind,
				Outcome:      m.Outcome,
				Title:        m.Title,
				Summary:      m.Summary,
				IssueID:      ptrString(m.IssueID),
				TaskID:       ptrString(m.TaskID),
				CommentID:    ptrString(m.CommentID),
				Confidence:   m.Confidence,
				Score:        r.Score,
				MatchType:    r.MatchType,
				VectorScore:  r.VectorScore,
				KeywordScore: r.KeywordScore,
				Snippet:      r.Snippet,
				SourceReason: knowledgeSourceReason(r),
			})
			continue
		}
		if r.WikiPage != nil {
			w := r.WikiPage
			out = append(out, RelevantKnowledge{
				TargetType:   r.TargetType,
				ID:           w.ID,
				Slug:         w.Slug,
				Kind:         "wiki_page",
				Outcome:      w.Status,
				Title:        w.Title,
				Summary:      truncateForSummary(w.Body, 700),
				Confidence:   90,
				Score:        r.Score,
				MatchType:    r.MatchType,
				VectorScore:  r.VectorScore,
				KeywordScore: r.KeywordScore,
				Snippet:      r.Snippet,
				SourceReason: knowledgeSourceReason(r),
			})
		}
	}
	return out
}

func knowledgeSourceReason(r KnowledgeSearchResult) string {
	switch r.MatchType {
	case "hybrid":
		return "Matched by both semantic vector similarity and keyword relevance."
	case "vector":
		return "Matched by semantic vector similarity."
	case "keyword":
		return "Matched by keyword search."
	case "canonical_fallback":
		return "Selected from canonical Project Wiki fallback pages."
	default:
		if r.WikiPage != nil {
			return "Selected from Project Wiki context."
		}
		return "Selected from project memory context."
	}
}

func (s *ProjectKnowledgeService) InjectedText(results []KnowledgeSearchResult, limit int, maxChars int) string {
	relevant := s.ToRelevant(results, limit)
	if len(relevant) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range relevant {
		if maxChars > 0 && b.Len() >= maxChars {
			break
		}
		fmt.Fprintf(&b, "- kind=%s outcome=%s confidence=%d source=%s", item.Kind, item.Outcome, item.Confidence, item.ID)
		if item.Slug != "" {
			fmt.Fprintf(&b, " slug=%s", item.Slug)
		}
		if item.IssueID != "" {
			fmt.Fprintf(&b, " issue=%s", item.IssueID)
		}
		if item.TaskID != "" {
			fmt.Fprintf(&b, " task=%s", item.TaskID)
		}
		b.WriteString("\n")
		if item.Title != "" {
			fmt.Fprintf(&b, "  title: %s\n", truncateForSummary(item.Title, 180))
		}
		if item.Summary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", truncateForSummary(item.Summary, 500))
		}
	}
	out := b.String()
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars]
	}
	return out
}

func (s *ProjectKnowledgeService) enqueueWikiPageEmbeddingBestEffort(ctx context.Context, workspaceID, projectID, id pgtype.UUID, page WikiPage) {
	text := wikiPageEmbeddingText(page)
	s.enqueueEmbeddingJobBestEffort(ctx, workspaceID, projectID, KnowledgeTargetWikiPage, id, text)
}

func (s *ProjectKnowledgeService) enqueueMemoryItemEmbeddingBestEffort(ctx context.Context, workspaceID, projectID, id pgtype.UUID, item MemoryItem) {
	text := memoryItemEmbeddingText(item)
	s.enqueueEmbeddingJobBestEffort(ctx, workspaceID, projectID, KnowledgeTargetMemoryItem, id, text)
}

func (s *ProjectKnowledgeService) enqueueEmbeddingJobBestEffort(ctx context.Context, workspaceID, projectID pgtype.UUID, targetType string, targetID pgtype.UUID, text string) {
	if _, err := s.enqueueEmbeddingJob(ctx, workspaceID, projectID, targetType, targetID, text); err != nil {
		slog.Debug("project knowledge embedding job enqueue skipped", "target_type", targetType, "target_id", util.UUIDToString(targetID), "error", err)
	}
}

func (s *ProjectKnowledgeService) enqueueEmbeddingJob(ctx context.Context, workspaceID, projectID pgtype.UUID, targetType string, targetID pgtype.UUID, text string) (bool, error) {
	if !s.EmbeddingsConfigured() || strings.TrimSpace(text) == "" {
		return false, ErrEmbeddingNotConfigured
	}
	contentHash := knowledgeContentHash(text)
	inserted := false
	err := s.execTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			WITH current_embedding AS (
				SELECT 1
				FROM project_memory_embedding
				WHERE target_type = $3
				  AND target_id = $4
				  AND embedding_model = $5
				  AND content_hash = $6
			)
			INSERT INTO project_knowledge_embedding_job (
				workspace_id, project_id, target_type, target_id, embedding_model, content_hash,
				status, next_attempt_at, last_error, embedded_at, updated_at
			)
			SELECT $1, $2, $3, $4, $5, $6, 'queued', now(), NULL, NULL, now()
			WHERE NOT EXISTS (SELECT 1 FROM current_embedding)
			ON CONFLICT (target_type, target_id, embedding_model) DO UPDATE SET
				workspace_id = EXCLUDED.workspace_id,
				project_id = EXCLUDED.project_id,
				content_hash = EXCLUDED.content_hash,
				status = 'queued',
				next_attempt_at = now(),
				last_error = NULL,
				embedded_at = NULL,
				updated_at = now()
			WHERE project_knowledge_embedding_job.content_hash IS DISTINCT FROM EXCLUDED.content_hash
			   OR project_knowledge_embedding_job.status IN ('failed', 'running')
		`, workspaceID, projectID, targetType, targetID, s.Embedder.Model(), contentHash)
		if err != nil {
			return err
		}
		inserted = tag.RowsAffected() > 0
		return nil
	})
	return inserted, err
}

func (s *ProjectKnowledgeService) ProcessEmbeddingJobs(ctx context.Context, limit int32) (EmbeddingJobProcessResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	out := EmbeddingJobProcessResult{}
	if !s.EmbeddingsConfigured() {
		return out, ErrEmbeddingNotConfigured
	}
	jobs, err := s.claimEmbeddingJobs(ctx, limit)
	if err != nil {
		return out, err
	}
	for _, job := range jobs {
		out.Processed++
		if err := s.processEmbeddingJob(ctx, job); err != nil {
			out.Failed++
			s.markEmbeddingJobFailed(ctx, job.ID, job.AttemptCount, err)
			continue
		}
		out.Succeeded++
	}
	return out, nil
}

func (s *ProjectKnowledgeService) BackfillKnowledgeEmbeddings(ctx context.Context, workspaceID, projectID pgtype.UUID, targetType string) (EmbeddingBackfillResult, error) {
	out := EmbeddingBackfillResult{}
	if !s.EmbeddingsConfigured() {
		return out, ErrEmbeddingNotConfigured
	}
	targetType = strings.TrimSpace(targetType)
	if targetType != "" && targetType != KnowledgeTargetWikiPage && targetType != KnowledgeTargetMemoryItem {
		return out, fmt.Errorf("unsupported knowledge target type %q", targetType)
	}
	if targetType == "" || targetType == KnowledgeTargetWikiPage {
		pages, err := s.ListWikiPages(ctx, workspaceID, projectID)
		if err != nil {
			return out, err
		}
		for _, page := range pages {
			if page.Status == "archived" {
				continue
			}
			queued, err := s.enqueueEmbeddingJob(ctx, workspaceID, projectID, KnowledgeTargetWikiPage, util.MustParseUUID(page.ID), wikiPageEmbeddingText(page))
			updateBackfillCounts(&out, queued, err)
		}
	}

	if targetType == "" || targetType == KnowledgeTargetMemoryItem {
		items, err := s.listAllMemoryItemsForEmbedding(ctx, workspaceID, projectID)
		if err != nil {
			return out, err
		}
		for _, item := range items {
			queued, err := s.enqueueEmbeddingJob(ctx, workspaceID, projectID, KnowledgeTargetMemoryItem, util.MustParseUUID(item.ID), memoryItemEmbeddingText(item))
			updateBackfillCounts(&out, queued, err)
		}
	}
	return out, nil
}

func (s *ProjectKnowledgeService) listAllMemoryItemsForEmbedding(ctx context.Context, workspaceID, projectID pgtype.UUID) ([]MemoryItem, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
		       commands, repo_refs, array_to_json(tags)::jsonb, confidence, expires_at, created_at, updated_at
		FROM project_memory_item
		WHERE workspace_id = $1 AND project_id = $2 AND (expires_at IS NULL OR expires_at > now())
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MemoryItem{}
	for rows.Next() {
		item, err := scanMemoryItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type embeddingJob struct {
	ID             pgtype.UUID
	WorkspaceID    pgtype.UUID
	ProjectID      pgtype.UUID
	TargetType     string
	TargetID       pgtype.UUID
	EmbeddingModel string
	ContentHash    string
	AttemptCount   int32
}

func (s *ProjectKnowledgeService) claimEmbeddingJobs(ctx context.Context, limit int32) ([]embeddingJob, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT id
			FROM project_knowledge_embedding_job
			WHERE status IN ('queued', 'failed')
			  AND next_attempt_at <= now()
			  AND embedding_model = $2
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE project_knowledge_embedding_job j
		SET status = 'running',
		    attempt_count = j.attempt_count + 1,
		    updated_at = now()
		FROM due
		WHERE j.id = due.id
		RETURNING j.id, j.workspace_id, j.project_id, j.target_type, j.target_id, j.embedding_model, j.content_hash, j.attempt_count
	`, limit, s.Embedder.Model())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []embeddingJob{}
	for rows.Next() {
		var job embeddingJob
		if err := rows.Scan(&job.ID, &job.WorkspaceID, &job.ProjectID, &job.TargetType, &job.TargetID, &job.EmbeddingModel, &job.ContentHash, &job.AttemptCount); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *ProjectKnowledgeService) processEmbeddingJob(ctx context.Context, job embeddingJob) error {
	text, err := s.embeddingTextForJob(ctx, job)
	if err != nil {
		return err
	}
	if hash := knowledgeContentHash(text); hash != job.ContentHash {
		_, err := s.enqueueEmbeddingJob(ctx, job.WorkspaceID, job.ProjectID, job.TargetType, job.TargetID, text)
		return err
	}
	vector, err := s.Embedder.Embed(ctx, text)
	if err != nil {
		return err
	}
	if len(vector) != DefaultEmbeddingDimension {
		return fmt.Errorf("embedding dimension mismatch: got %d want %d", len(vector), DefaultEmbeddingDimension)
	}
	return s.execTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_memory_embedding (workspace_id, project_id, target_type, target_id, embedding, embedding_model, content_hash)
			VALUES ($1, $2, $3, $4, $5::vector, $6, $7)
			ON CONFLICT (target_type, target_id, embedding_model) DO UPDATE SET
				workspace_id = EXCLUDED.workspace_id,
				project_id = EXCLUDED.project_id,
				embedding = EXCLUDED.embedding,
				content_hash = EXCLUDED.content_hash,
				embedded_at = now()
		`, job.WorkspaceID, job.ProjectID, job.TargetType, job.TargetID, embeddingToVectorLiteral(vector), job.EmbeddingModel, job.ContentHash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE project_knowledge_embedding_job
			SET status = 'succeeded',
			    last_error = NULL,
			    embedded_at = now(),
			    updated_at = now()
			WHERE id = $1
		`, job.ID)
		return err
	})
}

func (s *ProjectKnowledgeService) embeddingTextForJob(ctx context.Context, job embeddingJob) (string, error) {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	switch job.TargetType {
	case KnowledgeTargetWikiPage:
		row := tx.QueryRow(ctx, `
			SELECT id, workspace_id, project_id, slug, title, body, source_refs, status, updated_by, reviewed_at, created_at, updated_at
			FROM project_wiki_page
			WHERE id = $1 AND workspace_id = $2 AND project_id = $3 AND status != 'archived'
		`, job.TargetID, job.WorkspaceID, job.ProjectID)
		page, err := scanWikiPage(row)
		if err != nil {
			return "", err
		}
		return wikiPageEmbeddingText(page), nil
	case KnowledgeTargetMemoryItem:
		row := tx.QueryRow(ctx, `
			SELECT id, workspace_id, project_id, issue_id, task_id, comment_id, kind, outcome, title, summary, symptom, cause, fix_path,
			       commands, repo_refs, array_to_json(tags)::jsonb, confidence, expires_at, created_at, updated_at
			FROM project_memory_item
			WHERE id = $1 AND workspace_id = $2 AND project_id = $3 AND (expires_at IS NULL OR expires_at > now())
		`, job.TargetID, job.WorkspaceID, job.ProjectID)
		item, err := scanMemoryItem(row)
		if err != nil {
			return "", err
		}
		return memoryItemEmbeddingText(item), nil
	default:
		return "", fmt.Errorf("unsupported knowledge target type %q", job.TargetType)
	}
}

func (s *ProjectKnowledgeService) markEmbeddingJobFailed(ctx context.Context, id pgtype.UUID, attemptCount int32, jobErr error) {
	delay := embeddingRetryDelay(attemptCount)
	if err := s.execTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE project_knowledge_embedding_job
			SET status = 'failed',
			    next_attempt_at = now() + ($2::double precision * interval '1 second'),
			    last_error = $3,
			    updated_at = now()
			WHERE id = $1
		`, id, delay.Seconds(), truncateForSummary(jobErr.Error(), 1000))
		return err
	}); err != nil {
		slog.Debug("failed to mark project knowledge embedding job failed", "job_id", util.UUIDToString(id), "error", err)
	}
}

func updateBackfillCounts(out *EmbeddingBackfillResult, queued bool, err error) {
	if err != nil {
		out.Failed++
		return
	}
	if queued {
		out.Queued++
	} else {
		out.Skipped++
	}
}

func wikiPageEmbeddingText(page WikiPage) string {
	return strings.Join([]string{page.Title, page.Body}, "\n\n")
}

func memoryItemEmbeddingText(item MemoryItem) string {
	return strings.Join([]string{item.Title, item.Summary, item.Symptom, item.Cause, item.FixPath}, "\n\n")
}

func knowledgeContentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func embeddingRetryDelay(attemptCount int32) time.Duration {
	if attemptCount < 1 {
		attemptCount = 1
	}
	if attemptCount > 6 {
		attemptCount = 6
	}
	return time.Duration(1<<uint(attemptCount-1)) * time.Minute
}

func (s *ProjectKnowledgeService) execTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.Tx.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type scanner interface {
	Scan(dest ...any) error
}

const retrievalLogSelectSQL = `
	SELECT id, workspace_id, project_id, issue_id, task_id, query_text, returned_items,
	       search_mode, query_context, candidates, selected_items, injected_text,
	       token_budget, injected_item_count, prompt_section_hash, status, error, task_outcome,
	       helpfulness, feedback, feedback_note, created_at, updated_at
	FROM project_knowledge_retrieval_log
`

func scanWikiPage(row scanner) (WikiPage, error) {
	var page WikiPage
	var id, workspaceID, projectID, updatedBy pgtype.UUID
	var sourceRefs []byte
	var reviewedAt, createdAt, updatedAt pgtype.Timestamptz
	if err := row.Scan(&id, &workspaceID, &projectID, &page.Slug, &page.Title, &page.Body, &sourceRefs, &page.Status, &updatedBy, &reviewedAt, &createdAt, &updatedAt); err != nil {
		return WikiPage{}, err
	}
	page.ID = util.UUIDToString(id)
	page.WorkspaceID = util.UUIDToString(workspaceID)
	page.ProjectID = util.UUIDToString(projectID)
	page.SourceRefs = jsonOrDefault(sourceRefs)
	page.UpdatedBy = util.UUIDToPtr(updatedBy)
	page.ReviewedAt = util.TimestampToPtr(reviewedAt)
	page.CreatedAt = util.TimestampToString(createdAt)
	page.UpdatedAt = util.TimestampToString(updatedAt)
	return page, nil
}

func scanMemoryItem(row scanner) (MemoryItem, error) {
	var item MemoryItem
	var id, workspaceID, projectID, issueID, taskID, commentID pgtype.UUID
	var commands, repoRefs, tagsJSON []byte
	var expiresAt, createdAt, updatedAt pgtype.Timestamptz
	if err := row.Scan(&id, &workspaceID, &projectID, &issueID, &taskID, &commentID, &item.Kind, &item.Outcome, &item.Title, &item.Summary, &item.Symptom, &item.Cause, &item.FixPath, &commands, &repoRefs, &tagsJSON, &item.Confidence, &expiresAt, &createdAt, &updatedAt); err != nil {
		return MemoryItem{}, err
	}
	item.ID = util.UUIDToString(id)
	item.WorkspaceID = util.UUIDToString(workspaceID)
	item.ProjectID = util.UUIDToString(projectID)
	item.IssueID = util.UUIDToPtr(issueID)
	item.TaskID = util.UUIDToPtr(taskID)
	item.CommentID = util.UUIDToPtr(commentID)
	item.Commands = jsonOrDefault(commands)
	item.RepoRefs = jsonOrDefault(repoRefs)
	_ = json.Unmarshal(tagsJSON, &item.Tags)
	if item.Tags == nil {
		item.Tags = []string{}
	}
	item.ExpiresAt = util.TimestampToPtr(expiresAt)
	item.CreatedAt = util.TimestampToString(createdAt)
	item.UpdatedAt = util.TimestampToString(updatedAt)
	return item, nil
}

func scanRetrievalLogs(rows pgx.Rows) ([]RetrievalLog, error) {
	defer rows.Close()
	logs := []RetrievalLog{}
	for rows.Next() {
		log, err := scanRetrievalLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func scanRetrievalLog(row scanner) (RetrievalLog, error) {
	var log RetrievalLog
	var id, workspaceID, projectID, issueID, taskID pgtype.UUID
	var returnedItems, queryContext, candidates, selectedItems []byte
	var tokenBudget, helpfulness pgtype.Int4
	var promptHash, errText, taskOutcome, feedback, feedbackNote pgtype.Text
	var createdAt, updatedAt pgtype.Timestamptz
	if err := row.Scan(
		&id, &workspaceID, &projectID, &issueID, &taskID, &log.QueryText, &returnedItems,
		&log.SearchMode, &queryContext, &candidates, &selectedItems, &log.InjectedText,
		&tokenBudget, &log.InjectedItemCount, &promptHash, &log.Status, &errText, &taskOutcome,
		&helpfulness, &feedback, &feedbackNote, &createdAt, &updatedAt,
	); err != nil {
		return RetrievalLog{}, err
	}
	log.ID = util.UUIDToString(id)
	log.WorkspaceID = util.UUIDToString(workspaceID)
	log.ProjectID = util.UUIDToString(projectID)
	log.IssueID = util.UUIDToPtr(issueID)
	log.TaskID = util.UUIDToPtr(taskID)
	log.ReturnedItems = jsonOrDefault(returnedItems)
	log.QueryContext = jsonObjectOrDefault(queryContext)
	log.Candidates = jsonOrDefault(candidates)
	log.SelectedItems = jsonOrDefault(selectedItems)
	log.TokenBudget = int4ToPtr(tokenBudget)
	log.PromptSectionHash = textToPtr(promptHash)
	log.Error = textToPtr(errText)
	log.TaskOutcome = textToPtr(taskOutcome)
	log.Helpfulness = int4ToPtr(helpfulness)
	log.Feedback = textToPtr(feedback)
	log.FeedbackNote = textToPtr(feedbackNote)
	log.CreatedAt = util.TimestampToString(createdAt)
	log.UpdatedAt = util.TimestampToString(updatedAt)
	return log, nil
}

func scanKnowledgeSearchResults(rows pgx.Rows) ([]KnowledgeSearchResult, error) {
	defer rows.Close()
	results := []KnowledgeSearchResult{}
	for rows.Next() {
		result, err := scanKnowledgeSearchResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func scanKnowledgeSearchResult(row scanner) (KnowledgeSearchResult, error) {
	var targetType string
	var score float64
	var vectorScore, keywordScore pgtype.Float8
	var matchType string
	var id, workspaceID, projectID, issueID, taskID, commentID, updatedBy pgtype.UUID
	var kind, outcome, title, summary, symptom, cause, fixPath string
	var commands, repoRefs, tagsJSON []byte
	var confidence int32
	var expiresAt, createdAt, updatedAt, reviewedAt pgtype.Timestamptz
	var slug, body, status pgtype.Text
	var sourceRefs []byte
	if err := row.Scan(
		&targetType, &score, &vectorScore, &keywordScore, &matchType,
		&id, &workspaceID, &projectID, &issueID, &taskID, &commentID, &kind, &outcome, &title, &summary, &symptom, &cause, &fixPath,
		&commands, &repoRefs, &tagsJSON, &confidence, &expiresAt, &createdAt, &updatedAt,
		&slug, &body, &sourceRefs, &status, &updatedBy, &reviewedAt,
	); err != nil {
		return KnowledgeSearchResult{}, err
	}
	result := KnowledgeSearchResult{TargetType: targetType, Score: score, MatchType: matchType}
	if vectorScore.Valid {
		result.VectorScore = &vectorScore.Float64
	}
	if keywordScore.Valid {
		result.KeywordScore = &keywordScore.Float64
	}
	if targetType == KnowledgeTargetWikiPage {
		page := WikiPage{
			ID:          util.UUIDToString(id),
			WorkspaceID: util.UUIDToString(workspaceID),
			ProjectID:   util.UUIDToString(projectID),
			Slug:        slug.String,
			Title:       title,
			Body:        body.String,
			SourceRefs:  jsonOrDefault(sourceRefs),
			Status:      status.String,
			UpdatedBy:   util.UUIDToPtr(updatedBy),
			ReviewedAt:  util.TimestampToPtr(reviewedAt),
			CreatedAt:   util.TimestampToString(createdAt),
			UpdatedAt:   util.TimestampToString(updatedAt),
		}
		result.WikiPage = &page
		result.Snippet = truncateForSummary(page.Body, 500)
		result.SourceRefs = page.SourceRefs
		return result, nil
	}
	item := MemoryItem{
		ID:          util.UUIDToString(id),
		WorkspaceID: util.UUIDToString(workspaceID),
		ProjectID:   util.UUIDToString(projectID),
		IssueID:     util.UUIDToPtr(issueID),
		TaskID:      util.UUIDToPtr(taskID),
		CommentID:   util.UUIDToPtr(commentID),
		Kind:        kind,
		Outcome:     outcome,
		Title:       title,
		Summary:     summary,
		Symptom:     symptom,
		Cause:       cause,
		FixPath:     fixPath,
		Commands:    jsonOrDefault(commands),
		RepoRefs:    jsonOrDefault(repoRefs),
		Confidence:  confidence,
		ExpiresAt:   util.TimestampToPtr(expiresAt),
		CreatedAt:   util.TimestampToString(createdAt),
		UpdatedAt:   util.TimestampToString(updatedAt),
	}
	_ = json.Unmarshal(tagsJSON, &item.Tags)
	if item.Tags == nil {
		item.Tags = []string{}
	}
	result.MemoryItem = &item
	result.Snippet = truncateForSummary(item.Summary, 500)
	return result, nil
}

func rawJSONPtrToString(v *json.RawMessage) *string {
	if v == nil || len(*v) == 0 {
		return nil
	}
	s := string(*v)
	return &s
}

func jsonOrDefault(v []byte) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage("[]")
	}
	return json.RawMessage(v)
}

func jsonObjectOrDefault(v []byte) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(v)
}

func textToPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func int4ToPtr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func knowledgeSearchTSQuery(input string) string {
	terms := knowledgeSearchTerms(input)
	if len(terms) == 0 {
		return ""
	}
	var b strings.Builder
	for i, term := range terms {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(term)
		b.WriteString(":*")
	}
	return b.String()
}

func knowledgeSearchTerms(input string) []string {
	input = strings.ToLower(input)
	seen := map[string]struct{}{}
	terms := make([]string, 0, 48)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		value := string(token)
		add(value)
		if containsNonASCII(token) && len(token) > 1 {
			for i := 0; i < len(token)-1; i++ {
				add(string(token[i : i+2]))
			}
		}
		token = token[:0]
	}
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			token = append(token, r)
			continue
		}
		flush()
		if len(terms) >= 48 {
			break
		}
	}
	flush()
	if len(terms) > 48 {
		return terms[:48]
	}
	return terms
}

func containsNonASCII(value []rune) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

func embeddingToVectorLiteral(values []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
