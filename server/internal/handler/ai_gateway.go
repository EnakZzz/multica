package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	aiGatewayTokenPrefix = "mvk_"
	aiGatewayDefaultURL  = "https://api.openai.com/v1"
)

type aiGatewayKeyResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"token_prefix"`
	Status     string  `json:"status"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

type createAIGatewayKeyRequest struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

type createAIGatewayKeyResponse struct {
	aiGatewayKeyResponse
	Token string `json:"token"`
}

type aiGatewayProviderPresetResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	BaseURL        string   `json:"base_url"`
	APIKeyEnv      string   `json:"api_key_env"`
	Model          string   `json:"model"`
	UpstreamAPI    string   `json:"upstream_api"`
	EndpointTypes  []string `json:"endpoint_types"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type aiGatewayRouteResponse struct {
	ID        string                    `json:"id"`
	Alias     string                    `json:"alias"`
	Strategy  string                    `json:"strategy"`
	Enabled   bool                      `json:"enabled"`
	Targets   []aiGatewayTargetResponse `json:"targets"`
	CreatedAt string                    `json:"created_at"`
	UpdatedAt string                    `json:"updated_at"`
}

type aiGatewayTargetResponse struct {
	ID                          string `json:"id"`
	Provider                    string `json:"provider"`
	BaseURL                     string `json:"base_url"`
	APIKeyEnv                   string `json:"api_key_env"`
	Model                       string `json:"model"`
	UpstreamAPI                 string `json:"upstream_api"`
	ReasoningEffort             string `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string `json:"organization_env,omitempty"`
	ProjectEnv                  string `json:"project_env,omitempty"`
	TimeoutSeconds              int    `json:"timeout_seconds"`
	Weight                      int    `json:"weight"`
	Priority                    int    `json:"priority"`
	Enabled                     bool   `json:"enabled"`
	InputPricePerMillionMicros  int64  `json:"input_price_per_million_micros"`
	OutputPricePerMillionMicros int64  `json:"output_price_per_million_micros"`
}

type saveAIGatewayRouteRequest struct {
	Alias    string                         `json:"alias"`
	Strategy string                         `json:"strategy"`
	Enabled  *bool                          `json:"enabled"`
	Targets  []saveAIGatewayRouteTargetForm `json:"targets"`
}

type saveAIGatewayRouteTargetForm struct {
	ID                          string `json:"id,omitempty"`
	Provider                    string `json:"provider"`
	BaseURL                     string `json:"base_url"`
	APIKeyEnv                   string `json:"api_key_env"`
	Model                       string `json:"model"`
	UpstreamAPI                 string `json:"upstream_api"`
	ReasoningEffort             string `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string `json:"organization_env,omitempty"`
	ProjectEnv                  string `json:"project_env,omitempty"`
	TimeoutSeconds              int    `json:"timeout_seconds,omitempty"`
	Weight                      int    `json:"weight,omitempty"`
	Priority                    int    `json:"priority,omitempty"`
	Enabled                     *bool  `json:"enabled,omitempty"`
	InputPricePerMillionMicros  int64  `json:"input_price_per_million_micros,omitempty"`
	OutputPricePerMillionMicros int64  `json:"output_price_per_million_micros,omitempty"`
}

type aiGatewayProbeRequest struct {
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	Model     string `json:"model,omitempty"`
}

type aiGatewayProbeResponse struct {
	BaseURL           string                    `json:"base_url"`
	Authenticated     bool                      `json:"authenticated"`
	ModelsEndpoint    aiGatewayProbeEndpoint    `json:"models_endpoint"`
	Responses         aiGatewayProbeEndpoint    `json:"responses"`
	ChatCompletions   aiGatewayProbeEndpoint    `json:"chat_completions"`
	AnthropicMessages aiGatewayProbeEndpoint    `json:"anthropic_messages"`
	Models            []aiGatewayProbeModelInfo `json:"models"`
}

type aiGatewayProbeEndpoint struct {
	Status    int    `json:"status"`
	OK        bool   `json:"ok"`
	Supported bool   `json:"supported"`
	Error     string `json:"error,omitempty"`
}

type aiGatewayProbeModelInfo struct {
	ID            string   `json:"id"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	EndpointTypes []string `json:"supported_endpoint_types,omitempty"`
}

type aiGatewayKeyRow struct {
	ID         pgtype.UUID
	Name       string
	Prefix     string
	Status     string
	ExpiresAt  pgtype.Timestamptz
	LastUsedAt pgtype.Timestamptz
	RevokedAt  pgtype.Timestamptz
	CreatedAt  pgtype.Timestamptz
}

func aiGatewayKeyToResponse(row aiGatewayKeyRow) aiGatewayKeyResponse {
	resp := aiGatewayKeyResponse{
		ID:         uuidToString(row.ID),
		Name:       row.Name,
		Prefix:     row.Prefix,
		Status:     row.Status,
		ExpiresAt:  timestampToPtr(row.ExpiresAt),
		LastUsedAt: timestampToPtr(row.LastUsedAt),
		CreatedAt:  timestampToString(row.CreatedAt),
	}
	if row.RevokedAt.Valid {
		resp.RevokedAt = timestampToPtr(row.RevokedAt)
	}
	return resp
}

func generateAIGatewayToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate ai gateway token: %w", err)
	}
	return aiGatewayTokenPrefix + hex.EncodeToString(b), nil
}

func (h *Handler) CreateAIGatewayKey(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return
	}

	var req createAIGatewayKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	rawToken, err := generateAIGatewayToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}

	var expiresAt any
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		expiresAt = time.Now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
	}

	var row aiGatewayKeyRow
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO ai_gateway_virtual_key (
			workspace_id, created_by, name, token_hash, token_prefix, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, token_prefix, status, expires_at, last_used_at, revoked_at, created_at
	`, parseUUID(workspaceID), parseUUID(userID), req.Name, auth.HashToken(rawToken), prefix, expiresAt).Scan(
		&row.ID, &row.Name, &row.Prefix, &row.Status,
		&row.ExpiresAt, &row.LastUsedAt, &row.RevokedAt, &row.CreatedAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create AI gateway key")
		return
	}

	writeJSON(w, http.StatusCreated, createAIGatewayKeyResponse{
		aiGatewayKeyResponse: aiGatewayKeyToResponse(row),
		Token:                rawToken,
	})
}

func (h *Handler) ListAIGatewayKeys(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT id, name, token_prefix, status, expires_at, last_used_at, revoked_at, created_at
		FROM ai_gateway_virtual_key
		WHERE workspace_id = $1
		ORDER BY created_at DESC
	`, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway keys")
		return
	}
	defer rows.Close()

	resp := []aiGatewayKeyResponse{}
	for rows.Next() {
		var row aiGatewayKeyRow
		if err := rows.Scan(
			&row.ID, &row.Name, &row.Prefix, &row.Status,
			&row.ExpiresAt, &row.LastUsedAt, &row.RevokedAt, &row.CreatedAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list AI gateway keys")
			return
		}
		resp = append(resp, aiGatewayKeyToResponse(row))
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway keys")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) RevokeAIGatewayKey(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return
	}
	keyID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "key id")
	if !ok {
		return
	}

	_, err := h.DB.Exec(r.Context(), `
		UPDATE ai_gateway_virtual_key
		SET status = 'revoked', revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1 AND workspace_id = $2
	`, keyID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke AI gateway key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var aiGatewayProviderPresets = []aiGatewayProviderPresetResponse{
	{
		ID:             "openai-responses",
		Name:           "OpenAI Responses",
		Provider:       "openai",
		BaseURL:        "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		Model:          "gpt-5-codex",
		UpstreamAPI:    "responses",
		EndpointTypes:  []string{"responses"},
		TimeoutSeconds: 300,
	},
	{
		ID:             "claude-local-chat",
		Name:           "Claude Local Chat Completions",
		Provider:       "claude-local",
		BaseURL:        "http://localhost:3000/v1",
		APIKeyEnv:      "ANTHROPIC_AUTH_TOKEN",
		Model:          "claude-sonnet-4-6",
		UpstreamAPI:    "chat_completions",
		EndpointTypes:  []string{"chat_completions"},
		TimeoutSeconds: 3000,
	},
	{
		ID:             "openrouter-chat",
		Name:           "OpenRouter Chat Completions",
		Provider:       "openrouter",
		BaseURL:        "https://openrouter.ai/api/v1",
		APIKeyEnv:      "OPENROUTER_API_KEY",
		Model:          "anthropic/claude-sonnet",
		UpstreamAPI:    "chat_completions",
		EndpointTypes:  []string{"chat_completions"},
		TimeoutSeconds: 300,
	},
	{
		ID:             "openai-wildcard",
		Name:           "OpenAI Direct Pass-through",
		Provider:       "openai",
		BaseURL:        "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		Model:          "",
		UpstreamAPI:    "responses",
		EndpointTypes:  []string{"responses"},
		TimeoutSeconds: 300,
	},
}

func (h *Handler) ListAIGatewayProviderPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, aiGatewayProviderPresets)
}

func (h *Handler) ListAIGatewayRoutes(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID, ok := requireAIGatewayWorkspace(w, r)
	if !ok {
		return
	}
	routes, err := h.loadAIGatewayRoutesFromDB(r.Context(), workspaceID, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway routes")
		return
	}
	writeJSON(w, http.StatusOK, aiGatewayRoutesToResponse(routes))
}

func (h *Handler) CreateAIGatewayRoute(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID, ok := requireAIGatewayWorkspace(w, r)
	if !ok {
		return
	}
	var req saveAIGatewayRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	route, err := normalizeAIGatewayRouteRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var routeID pgtype.UUID
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO ai_gateway_route (workspace_id, alias, strategy, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, parseUUID(workspaceID), route.Alias, route.Strategy, route.Enabled).Scan(&routeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create AI gateway route")
		return
	}
	for i, target := range route.Targets {
		if err := insertAIGatewayRouteTarget(r.Context(), h.DB, routeID, target, i); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create AI gateway route target")
			return
		}
	}
	routes, err := h.loadAIGatewayRoutesFromDB(r.Context(), workspaceID, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load AI gateway route")
		return
	}
	for _, item := range aiGatewayRoutesToResponse(routes) {
		if item.ID == uuidToString(routeID) {
			writeJSON(w, http.StatusCreated, item)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) UpdateAIGatewayRoute(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID, ok := requireAIGatewayWorkspace(w, r)
	if !ok {
		return
	}
	routeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "route id")
	if !ok {
		return
	}
	var req saveAIGatewayRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	route, err := normalizeAIGatewayRouteRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE ai_gateway_route
		SET alias = $3, strategy = $4, enabled = $5, updated_at = now()
		WHERE id = $1 AND workspace_id = $2
	`, routeID, parseUUID(workspaceID), route.Alias, route.Strategy, route.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update AI gateway route")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "AI gateway route not found")
		return
	}
	if _, err := h.DB.Exec(r.Context(), `DELETE FROM ai_gateway_route_target WHERE route_id = $1`, routeID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update AI gateway route targets")
		return
	}
	for i, target := range route.Targets {
		if err := insertAIGatewayRouteTarget(r.Context(), h.DB, routeID, target, i); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update AI gateway route target")
			return
		}
	}
	routes, err := h.loadAIGatewayRoutesFromDB(r.Context(), workspaceID, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load AI gateway route")
		return
	}
	for _, item := range aiGatewayRoutesToResponse(routes) {
		if item.ID == uuidToString(routeID) {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) DeleteAIGatewayRoute(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID, ok := requireAIGatewayWorkspace(w, r)
	if !ok {
		return
	}
	routeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "route id")
	if !ok {
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		DELETE FROM ai_gateway_route
		WHERE id = $1 AND workspace_id = $2
	`, routeID, parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete AI gateway route")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ProbeAIGatewayProvider(w http.ResponseWriter, r *http.Request) {
	var req aiGatewayProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKeyEnv = strings.TrimSpace(req.APIKeyEnv)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Model = strings.TrimSpace(req.Model)
	if req.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	apiKey := req.APIKey
	if apiKey == "" && req.APIKeyEnv != "" {
		apiKey = os.Getenv(req.APIKeyEnv)
	}
	if apiKey == "" {
		apiKey = "invalid-token"
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp := aiGatewayProbeResponse{BaseURL: req.BaseURL}
	resp.ModelsEndpoint, resp.Models = probeAIGatewayModels(r.Context(), client, req.BaseURL, apiKey)
	resp.Authenticated = resp.ModelsEndpoint.OK
	resp.Responses = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/responses", apiKey, req.Model, "responses")
	resp.ChatCompletions = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/chat/completions", apiKey, req.Model, "chat_completions")
	resp.AnthropicMessages = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/messages", apiKey, req.Model, "anthropic_messages")
	writeJSON(w, http.StatusOK, resp)
}

func probeAIGatewayModels(ctx context.Context, client *http.Client, baseURL, apiKey string) (aiGatewayProbeEndpoint, []aiGatewayProbeModelInfo) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return aiGatewayProbeEndpoint{Error: err.Error()}, nil
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(httpReq)
	if err != nil {
		return aiGatewayProbeEndpoint{Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	endpoint := aiGatewayProbeEndpoint{
		Status:    resp.StatusCode,
		OK:        resp.StatusCode >= 200 && resp.StatusCode < 300,
		Supported: resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed,
	}
	if !endpoint.OK {
		endpoint.Error = strings.TrimSpace(string(data))
		return endpoint, nil
	}
	var envelope struct {
		Data []struct {
			ID            string   `json:"id"`
			OwnedBy       string   `json:"owned_by"`
			EndpointTypes []string `json:"supported_endpoint_types"`
		} `json:"data"`
	}
	_ = json.Unmarshal(data, &envelope)
	models := make([]aiGatewayProbeModelInfo, 0, len(envelope.Data))
	for _, model := range envelope.Data {
		models = append(models, aiGatewayProbeModelInfo{
			ID:            model.ID,
			OwnedBy:       model.OwnedBy,
			EndpointTypes: model.EndpointTypes,
		})
	}
	return endpoint, models
}

func probeAIGatewayJSONEndpoint(ctx context.Context, client *http.Client, url, apiKey, model, kind string) aiGatewayProbeEndpoint {
	var body string
	switch kind {
	case "responses":
		if model == "" {
			body = `{"model":"probe","input":"ping","max_output_tokens":1}`
		} else {
			body = fmt.Sprintf(`{"model":%q,"input":"Reply OK","max_output_tokens":1}`, model)
		}
	case "chat_completions":
		if model == "" {
			body = `{"model":"probe","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`
		} else {
			body = fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply OK"}],"max_tokens":1}`, model)
		}
	default:
		if model == "" {
			body = `{"model":"probe","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`
		} else {
			body = fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply OK"}],"max_tokens":1}`, model)
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return aiGatewayProbeEndpoint{Error: err.Error()}
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return aiGatewayProbeEndpoint{Error: err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	endpoint := aiGatewayProbeEndpoint{
		Status:    resp.StatusCode,
		OK:        resp.StatusCode >= 200 && resp.StatusCode < 300,
		Supported: resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed,
	}
	if !endpoint.OK {
		endpoint.Error = strings.TrimSpace(string(data))
	}
	return endpoint
}

type aiGatewayUsageResponse struct {
	ID               string `json:"id"`
	KeyPrefix        string `json:"key_prefix,omitempty"`
	KeyName          string `json:"key_name,omitempty"`
	CallerID         string `json:"caller_id,omitempty"`
	RequestID        string `json:"request_id"`
	Endpoint         string `json:"endpoint"`
	ModelAlias       string `json:"model_alias"`
	UpstreamProvider string `json:"upstream_provider"`
	UpstreamModel    string `json:"upstream_model"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	StatusCode       int32  `json:"status_code"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	TotalCostMicros  int64  `json:"total_cost_micros"`
	LatencyMs        int64  `json:"latency_ms"`
	Error            string `json:"error,omitempty"`
	CreatedAt        string `json:"created_at"`
}

type aiGatewayUsageSummaryResponse struct {
	CallerID         string `json:"caller_id"`
	KeyName          string `json:"key_name,omitempty"`
	KeyPrefix        string `json:"key_prefix,omitempty"`
	CreatedByName    string `json:"created_by_name,omitempty"`
	CreatedByEmail   string `json:"created_by_email,omitempty"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	ErrorCount       int64  `json:"error_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	TotalCostMicros  int64  `json:"total_cost_micros"`
	AverageLatencyMs int64  `json:"average_latency_ms"`
	LastRequestAt    string `json:"last_request_at"`
}

func (h *Handler) ListAIGatewayUsage(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return
	}
	limit := int32(100)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = int32(v)
	}
	offset := int32(0)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 || v > 100000 {
			writeError(w, http.StatusBadRequest, "offset must be between 0 and 100000")
			return
		}
		offset = int32(v)
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT
			u.id,
			COALESCE(k.token_prefix, ''),
			COALESCE(k.name, ''),
			COALESCE(u.caller_id, ''),
			u.request_id,
			u.endpoint,
			u.model_alias,
			u.upstream_provider,
			u.upstream_model,
			COALESCE(u.reasoning_effort, ''),
			u.status_code,
			u.prompt_tokens,
			u.completion_tokens,
			u.total_tokens,
			u.total_cost_micros,
			u.latency_ms,
			COALESCE(u.error, ''),
			u.created_at
		FROM ai_gateway_usage u
		LEFT JOIN ai_gateway_virtual_key k ON k.id = u.virtual_key_id
		WHERE u.workspace_id = $1
		  AND u.created_at >= now() - interval '1 day'
		ORDER BY u.created_at DESC
		LIMIT $2 OFFSET $3
	`, parseUUID(workspaceID), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage")
		return
	}
	defer rows.Close()

	resp := []aiGatewayUsageResponse{}
	for rows.Next() {
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		var item aiGatewayUsageResponse
		if err := rows.Scan(
			&id,
			&item.KeyPrefix,
			&item.KeyName,
			&item.CallerID,
			&item.RequestID,
			&item.Endpoint,
			&item.ModelAlias,
			&item.UpstreamProvider,
			&item.UpstreamModel,
			&item.ReasoningEffort,
			&item.StatusCode,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.TotalTokens,
			&item.TotalCostMicros,
			&item.LatencyMs,
			&item.Error,
			&createdAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage")
			return
		}
		item.ID = uuidToString(id)
		item.CreatedAt = timestampToString(createdAt)
		resp = append(resp, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListAIGatewayUsageSummary(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return
	}
	days := int32(30)
	if raw := r.URL.Query().Get("days"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 365 {
			writeError(w, http.StatusBadRequest, "days must be between 1 and 365")
			return
		}
		days = int32(v)
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT
			COALESCE(NULLIF(u.caller_id, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown') AS caller_id,
			COALESCE(k.name, ''),
			COALESCE(k.token_prefix, ''),
			COALESCE(creator.name, ''),
			COALESCE(creator.email, ''),
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE u.status_code < 400)::bigint,
			COUNT(*) FILTER (WHERE u.status_code >= 400)::bigint,
			COALESCE(SUM(u.prompt_tokens), 0)::bigint,
			COALESCE(SUM(u.completion_tokens), 0)::bigint,
			COALESCE(SUM(u.total_tokens), 0)::bigint,
			COALESCE(SUM(u.total_cost_micros), 0)::bigint,
			COALESCE(ROUND(AVG(u.latency_ms)), 0)::bigint,
			MAX(u.created_at)
		FROM ai_gateway_usage u
		LEFT JOIN ai_gateway_virtual_key k ON k.id = u.virtual_key_id
		LEFT JOIN "user" creator ON creator.id = k.created_by
		WHERE u.workspace_id = $1
		  AND u.created_at >= now() - ($2::int * interval '1 day')
		GROUP BY
			COALESCE(NULLIF(u.caller_id, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown'),
			k.name,
			k.token_prefix,
			creator.name,
			creator.email
		ORDER BY COALESCE(SUM(u.total_tokens), 0) DESC, COUNT(*) DESC
		LIMIT 100
	`, parseUUID(workspaceID), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage summary")
		return
	}
	defer rows.Close()

	resp := []aiGatewayUsageSummaryResponse{}
	for rows.Next() {
		var item aiGatewayUsageSummaryResponse
		var lastRequestAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.CallerID,
			&item.KeyName,
			&item.KeyPrefix,
			&item.CreatedByName,
			&item.CreatedByEmail,
			&item.RequestCount,
			&item.SuccessCount,
			&item.ErrorCount,
			&item.PromptTokens,
			&item.CompletionTokens,
			&item.TotalTokens,
			&item.TotalCostMicros,
			&item.AverageLatencyMs,
			&lastRequestAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage summary")
			return
		}
		item.LastRequestAt = timestampToString(lastRequestAt)
		resp = append(resp, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage summary")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

type aiGatewayVirtualKey struct {
	ID          string
	WorkspaceID string
}

func (h *Handler) resolveAIGatewayVirtualKey(ctx context.Context, token string) (aiGatewayVirtualKey, bool, error) {
	if h.DB == nil {
		return aiGatewayVirtualKey{}, false, errors.New("database unavailable")
	}
	if !strings.HasPrefix(token, aiGatewayTokenPrefix) {
		return aiGatewayVirtualKey{}, false, nil
	}
	hash := auth.HashToken(token)
	var id pgtype.UUID
	var workspaceID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT id, workspace_id
		FROM ai_gateway_virtual_key
		WHERE token_hash = $1
		  AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > now())
	`, hash).Scan(&id, &workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiGatewayVirtualKey{}, false, nil
	}
	if err != nil {
		return aiGatewayVirtualKey{}, false, err
	}
	key := aiGatewayVirtualKey{
		ID:          uuidToString(id),
		WorkspaceID: uuidToString(workspaceID),
	}
	go h.touchAIGatewayKey(context.Background(), key.ID)
	return key, true, nil
}

func (h *Handler) touchAIGatewayKey(ctx context.Context, keyID string) {
	if h.DB == nil {
		return
	}
	_, _ = h.DB.Exec(ctx, `UPDATE ai_gateway_virtual_key SET last_used_at = now() WHERE id = $1`, parseUUID(keyID))
}

type aiGatewayRoute struct {
	ID        string            `json:"id,omitempty"`
	Alias     string            `json:"alias"`
	Strategy  string            `json:"strategy,omitempty"`
	Enabled   bool              `json:"enabled,omitempty"`
	Targets   []aiGatewayTarget `json:"targets"`
	CreatedAt string            `json:"created_at,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

type aiGatewayTarget struct {
	ID                          string `json:"id,omitempty"`
	Provider                    string `json:"provider"`
	BaseURL                     string `json:"base_url"`
	APIKeyEnv                   string `json:"api_key_env"`
	Model                       string `json:"model"`
	UpstreamAPI                 string `json:"upstream_api,omitempty"`
	ReasoningEffort             string `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string `json:"organization_env,omitempty"`
	ProjectEnv                  string `json:"project_env,omitempty"`
	TimeoutSeconds              int    `json:"timeout_seconds,omitempty"`
	Weight                      int    `json:"weight,omitempty"`
	Priority                    int    `json:"priority,omitempty"`
	Enabled                     bool   `json:"enabled,omitempty"`
	InputPricePerMillionMicros  int64  `json:"input_price_per_million_micros,omitempty"`
	OutputPricePerMillionMicros int64  `json:"output_price_per_million_micros,omitempty"`
}

type aiGatewayRouteConfig struct {
	Models []aiGatewayRoute `json:"models"`
}

func loadAIGatewayRoutesFromEnv() ([]aiGatewayRoute, error) {
	raw := strings.TrimSpace(os.Getenv("AI_GATEWAY_ROUTES"))
	if raw == "" {
		defaultModel := strings.TrimSpace(os.Getenv("AI_GATEWAY_DEFAULT_MODEL"))
		if defaultModel == "" || os.Getenv("OPENAI_API_KEY") == "" {
			return nil, nil
		}
		alias := strings.TrimSpace(os.Getenv("AI_GATEWAY_DEFAULT_ALIAS"))
		if alias == "" {
			alias = "team-agent"
		}
		return []aiGatewayRoute{{
			Alias:   alias,
			Enabled: true,
			Targets: []aiGatewayTarget{{
				Provider:       "openai",
				BaseURL:        aiGatewayDefaultURL,
				APIKeyEnv:      "OPENAI_API_KEY",
				Model:          defaultModel,
				UpstreamAPI:    "responses",
				TimeoutSeconds: 300,
				Weight:         1,
				Enabled:        true,
			}},
		}}, nil
	}

	var cfg aiGatewayRouteConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err == nil && len(cfg.Models) > 0 {
		return normalizeAIGatewayRoutes(cfg.Models)
	}
	var routes []aiGatewayRoute
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, fmt.Errorf("parse AI_GATEWAY_ROUTES: %w", err)
	}
	return normalizeAIGatewayRoutes(routes)
}

func normalizeAIGatewayRoutes(routes []aiGatewayRoute) ([]aiGatewayRoute, error) {
	for i := range routes {
		routes[i].Alias = strings.TrimSpace(routes[i].Alias)
		if routes[i].Alias == "" {
			return nil, fmt.Errorf("AI gateway route at index %d is missing alias", i)
		}
		if routes[i].Strategy == "" {
			routes[i].Strategy = "fallback"
		}
		if routes[i].Strategy != "fallback" && routes[i].Strategy != "single" && routes[i].Strategy != "round_robin" && routes[i].Strategy != "weighted" {
			return nil, fmt.Errorf("AI gateway route %q has unsupported strategy %q", routes[i].Alias, routes[i].Strategy)
		}
		if !routes[i].Enabled {
			routes[i].Enabled = true
		}
		if len(routes[i].Targets) == 0 {
			return nil, fmt.Errorf("AI gateway route %q has no targets", routes[i].Alias)
		}
		for j := range routes[i].Targets {
			t := &routes[i].Targets[j]
			t.Provider = strings.TrimSpace(t.Provider)
			if t.Provider == "" {
				t.Provider = "openai-compatible"
			}
			t.BaseURL = strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
			if t.BaseURL == "" {
				t.BaseURL = aiGatewayDefaultURL
			}
			t.UpstreamAPI = strings.TrimSpace(t.UpstreamAPI)
			if t.UpstreamAPI == "" {
				t.UpstreamAPI = "responses"
			}
			if t.UpstreamAPI != "responses" && t.UpstreamAPI != "chat_completions" {
				return nil, fmt.Errorf("AI gateway route %q target %d has unsupported upstream_api %q", routes[i].Alias, j, t.UpstreamAPI)
			}
			t.APIKeyEnv = strings.TrimSpace(t.APIKeyEnv)
			if t.APIKeyEnv == "" {
				return nil, fmt.Errorf("AI gateway route %q target %d is missing api_key_env", routes[i].Alias, j)
			}
			t.Model = strings.TrimSpace(t.Model)
			if t.Model == "" && routes[i].Alias != "*" {
				return nil, fmt.Errorf("AI gateway route %q target %d is missing model", routes[i].Alias, j)
			}
			if t.TimeoutSeconds <= 0 {
				t.TimeoutSeconds = 60
			}
			if t.Weight <= 0 {
				t.Weight = 1
			}
			if !t.Enabled {
				t.Enabled = true
			}
		}
	}
	return routes, nil
}

func findAIGatewayRoute(routes []aiGatewayRoute, model string) (aiGatewayRoute, bool) {
	for _, route := range routes {
		if route.Alias == model {
			return route, true
		}
	}
	for _, route := range routes {
		if route.Alias == "*" {
			return route, true
		}
	}
	return aiGatewayRoute{}, false
}

func requireAIGatewayWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r == nil {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return "", false
	}
	workspaceID := middleware.WorkspaceIDFromContext(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id or workspace_slug is required")
		return "", false
	}
	return workspaceID, true
}

func normalizeAIGatewayRouteRequest(req saveAIGatewayRouteRequest) (aiGatewayRoute, error) {
	route := aiGatewayRoute{
		Alias:    strings.TrimSpace(req.Alias),
		Strategy: strings.TrimSpace(req.Strategy),
		Enabled:  true,
		Targets:  make([]aiGatewayTarget, 0, len(req.Targets)),
	}
	if route.Alias == "" {
		return aiGatewayRoute{}, errors.New("alias is required")
	}
	if route.Strategy == "" {
		route.Strategy = "fallback"
	}
	if route.Strategy != "fallback" && route.Strategy != "single" && route.Strategy != "round_robin" && route.Strategy != "weighted" {
		return aiGatewayRoute{}, fmt.Errorf("unsupported strategy %q", route.Strategy)
	}
	if req.Enabled != nil {
		route.Enabled = *req.Enabled
	}
	if len(req.Targets) == 0 {
		return aiGatewayRoute{}, errors.New("at least one target is required")
	}
	for i, raw := range req.Targets {
		target := aiGatewayTarget{
			ID:                          strings.TrimSpace(raw.ID),
			Provider:                    strings.TrimSpace(raw.Provider),
			BaseURL:                     strings.TrimRight(strings.TrimSpace(raw.BaseURL), "/"),
			APIKeyEnv:                   strings.TrimSpace(raw.APIKeyEnv),
			Model:                       strings.TrimSpace(raw.Model),
			UpstreamAPI:                 strings.TrimSpace(raw.UpstreamAPI),
			ReasoningEffort:             strings.TrimSpace(raw.ReasoningEffort),
			OrganizationEnv:             strings.TrimSpace(raw.OrganizationEnv),
			ProjectEnv:                  strings.TrimSpace(raw.ProjectEnv),
			TimeoutSeconds:              raw.TimeoutSeconds,
			Weight:                      raw.Weight,
			Priority:                    raw.Priority,
			Enabled:                     true,
			InputPricePerMillionMicros:  raw.InputPricePerMillionMicros,
			OutputPricePerMillionMicros: raw.OutputPricePerMillionMicros,
		}
		if target.Provider == "" {
			target.Provider = "openai-compatible"
		}
		if target.BaseURL == "" {
			return aiGatewayRoute{}, fmt.Errorf("target %d base_url is required", i)
		}
		if target.APIKeyEnv == "" {
			return aiGatewayRoute{}, fmt.Errorf("target %d api_key_env is required", i)
		}
		if target.Model == "" && route.Alias != "*" {
			return aiGatewayRoute{}, fmt.Errorf("target %d model is required", i)
		}
		if target.UpstreamAPI == "" {
			target.UpstreamAPI = "responses"
		}
		if target.UpstreamAPI != "responses" && target.UpstreamAPI != "chat_completions" {
			return aiGatewayRoute{}, fmt.Errorf("target %d upstream_api is unsupported", i)
		}
		if target.ReasoningEffort != "" && !isAIGatewayReasoningEffort(target.ReasoningEffort) {
			return aiGatewayRoute{}, fmt.Errorf("target %d reasoning_effort is unsupported", i)
		}
		if target.TimeoutSeconds <= 0 {
			target.TimeoutSeconds = 60
		}
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if raw.Enabled != nil {
			target.Enabled = *raw.Enabled
		}
		if target.InputPricePerMillionMicros < 0 || target.OutputPricePerMillionMicros < 0 {
			return aiGatewayRoute{}, fmt.Errorf("target %d price must be non-negative", i)
		}
		route.Targets = append(route.Targets, target)
	}
	return route, nil
}

func insertAIGatewayRouteTarget(ctx context.Context, db dbExecutor, routeID pgtype.UUID, target aiGatewayTarget, fallbackPriority int) error {
	priority := target.Priority
	if priority == 0 {
		priority = fallbackPriority
	}
	_, err := db.Exec(ctx, `
		INSERT INTO ai_gateway_route_target (
			route_id, provider, base_url, api_key_env, model, upstream_api,
			reasoning_effort, organization_env, project_env, timeout_seconds, weight, priority, enabled,
			input_price_per_million_micros, output_price_per_million_micros
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, $13, $14, $15)
	`, routeID, target.Provider, target.BaseURL, target.APIKeyEnv, target.Model, target.UpstreamAPI,
		target.ReasoningEffort, target.OrganizationEnv, target.ProjectEnv, target.TimeoutSeconds, target.Weight, priority, target.Enabled,
		target.InputPricePerMillionMicros, target.OutputPricePerMillionMicros)
	return err
}

func (h *Handler) loadAIGatewayRoutes(ctx context.Context, workspaceID string) ([]aiGatewayRoute, error) {
	if h.DB != nil && workspaceID != "" {
		routes, err := h.loadAIGatewayRoutesFromDB(ctx, workspaceID, false)
		if err != nil {
			if !isAIGatewayRouteTableMissing(err) {
				return nil, err
			}
			return loadAIGatewayRoutesFromEnv()
		}
		if len(routes) > 0 {
			return routes, nil
		}
	}
	return loadAIGatewayRoutesFromEnv()
}

func isAIGatewayRouteTableMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), `relation "ai_gateway_route" does not exist`)
}

func (h *Handler) loadAIGatewayRoutesFromDB(ctx context.Context, workspaceID string, includeDisabled bool) ([]aiGatewayRoute, error) {
	if h.DB == nil {
		return nil, nil
	}
	enabledClause := ""
	if !includeDisabled {
		enabledClause = "AND r.enabled = true AND t.enabled = true"
	}
	rows, err := h.DB.Query(ctx, `
		SELECT
			r.id, r.alias, r.strategy, r.enabled, r.created_at, r.updated_at,
			t.id, t.provider, t.base_url, t.api_key_env, t.model, t.upstream_api,
			COALESCE(t.reasoning_effort, ''), COALESCE(t.organization_env, ''), COALESCE(t.project_env, ''),
			t.timeout_seconds, t.weight, t.priority, t.enabled,
			t.input_price_per_million_micros, t.output_price_per_million_micros
		FROM ai_gateway_route r
		JOIN ai_gateway_route_target t ON t.route_id = r.id
		WHERE r.workspace_id = $1 `+enabledClause+`
		ORDER BY r.alias ASC, t.priority ASC, t.created_at ASC
	`, parseUUID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]int{}
	routes := []aiGatewayRoute{}
	for rows.Next() {
		var routeID, targetID pgtype.UUID
		var createdAt, updatedAt pgtype.Timestamptz
		var route aiGatewayRoute
		var target aiGatewayTarget
		if err := rows.Scan(
			&routeID, &route.Alias, &route.Strategy, &route.Enabled, &createdAt, &updatedAt,
			&targetID, &target.Provider, &target.BaseURL, &target.APIKeyEnv, &target.Model, &target.UpstreamAPI,
			&target.ReasoningEffort, &target.OrganizationEnv, &target.ProjectEnv, &target.TimeoutSeconds, &target.Weight, &target.Priority, &target.Enabled,
			&target.InputPricePerMillionMicros, &target.OutputPricePerMillionMicros,
		); err != nil {
			return nil, err
		}
		route.ID = uuidToString(routeID)
		route.CreatedAt = timestampToString(createdAt)
		route.UpdatedAt = timestampToString(updatedAt)
		target.ID = uuidToString(targetID)
		if idx, ok := byID[route.ID]; ok {
			routes[idx].Targets = append(routes[idx].Targets, target)
		} else {
			route.Targets = []aiGatewayTarget{target}
			byID[route.ID] = len(routes)
			routes = append(routes, route)
		}
	}
	return routes, rows.Err()
}

func aiGatewayRoutesToResponse(routes []aiGatewayRoute) []aiGatewayRouteResponse {
	resp := make([]aiGatewayRouteResponse, 0, len(routes))
	for _, route := range routes {
		item := aiGatewayRouteResponse{
			ID:        route.ID,
			Alias:     route.Alias,
			Strategy:  route.Strategy,
			Enabled:   route.Enabled,
			CreatedAt: route.CreatedAt,
			UpdatedAt: route.UpdatedAt,
			Targets:   make([]aiGatewayTargetResponse, 0, len(route.Targets)),
		}
		for _, target := range route.Targets {
			item.Targets = append(item.Targets, aiGatewayTargetResponse{
				ID:                          target.ID,
				Provider:                    target.Provider,
				BaseURL:                     target.BaseURL,
				APIKeyEnv:                   target.APIKeyEnv,
				Model:                       target.Model,
				UpstreamAPI:                 target.UpstreamAPI,
				ReasoningEffort:             target.ReasoningEffort,
				OrganizationEnv:             target.OrganizationEnv,
				ProjectEnv:                  target.ProjectEnv,
				TimeoutSeconds:              target.TimeoutSeconds,
				Weight:                      target.Weight,
				Priority:                    target.Priority,
				Enabled:                     target.Enabled,
				InputPricePerMillionMicros:  target.InputPricePerMillionMicros,
				OutputPricePerMillionMicros: target.OutputPricePerMillionMicros,
			})
		}
		resp = append(resp, item)
	}
	return resp
}

func (h *Handler) AIGatewayModels(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireAIGatewayKey(w, r)
	if !ok {
		return
	}
	routes, err := h.loadAIGatewayRoutes(r.Context(), key.WorkspaceID)
	if err != nil {
		writeAIGatewayError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := make([]map[string]any, 0, len(routes))
	seen := map[string]bool{}
	for _, route := range routes {
		if route.Alias == "*" {
			for _, target := range route.Targets {
				model := strings.TrimSpace(target.Model)
				if model == "" || seen[model] {
					continue
				}
				seen[model] = true
				data = append(data, aiGatewayModelListItem(model))
			}
			if routeHasCodexModelTemplate(route) {
				for _, model := range aiGatewayCodexModels() {
					if seen[model] {
						continue
					}
					seen[model] = true
					data = append(data, aiGatewayModelListItem(model))
				}
			}
			continue
		}
		if seen[route.Alias] {
			continue
		}
		seen[route.Alias] = true
		data = append(data, aiGatewayModelListItem(route.Alias))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func aiGatewayModelListItem(model string) map[string]any {
	return map[string]any{
		"id":       model,
		"object":   "model",
		"created":  0,
		"owned_by": "multica",
	}
}

func aiGatewayCodexModels() []string {
	models := agent.CodexStaticModels()
	out := make([]string, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func isAIGatewayCodexModel(model string) bool {
	for _, id := range aiGatewayCodexModels() {
		if id == model {
			return true
		}
	}
	return false
}

func routeHasCodexModelTemplate(route aiGatewayRoute) bool {
	for _, target := range route.Targets {
		if target.Enabled || target.ID == "" {
			if targetCanProxyCodexModel(target) {
				return true
			}
		}
	}
	return false
}

func targetCanProxyCodexModel(target aiGatewayTarget) bool {
	upstreamAPI := strings.TrimSpace(target.UpstreamAPI)
	if upstreamAPI != "" && upstreamAPI != "responses" {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(target.Provider))
	baseURL := strings.ToLower(strings.TrimSpace(target.BaseURL))
	return strings.Contains(provider, "openai") || strings.Contains(baseURL, "api.openai.com")
}

func (h *Handler) AIGatewayResponses(w http.ResponseWriter, r *http.Request) {
	h.proxyAIGateway(w, r, "/responses")
}

func (h *Handler) AIGatewayChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.proxyAIGateway(w, r, "/chat/completions")
}

func (h *Handler) requireAIGatewayKey(w http.ResponseWriter, r *http.Request) (aiGatewayVirtualKey, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeAIGatewayError(w, http.StatusUnauthorized, "missing authorization")
		return aiGatewayVirtualKey{}, false
	}
	key, ok, err := h.resolveAIGatewayVirtualKey(r.Context(), token)
	if err != nil {
		writeAIGatewayError(w, http.StatusServiceUnavailable, "AI gateway auth unavailable")
		return aiGatewayVirtualKey{}, false
	}
	if !ok {
		writeAIGatewayError(w, http.StatusUnauthorized, "invalid token")
		return aiGatewayVirtualKey{}, false
	}
	return key, true
}

func bearerToken(header string) string {
	if header == "" {
		return ""
	}
	if token, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(token)
	}
	return ""
}

func (h *Handler) proxyAIGateway(w http.ResponseWriter, r *http.Request, endpoint string) {
	key, ok := h.requireAIGatewayKey(w, r)
	if !ok {
		return
	}
	if r.Body == nil {
		writeAIGatewayError(w, http.StatusBadRequest, "missing request body")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		writeAIGatewayError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeAIGatewayError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		writeAIGatewayError(w, http.StatusBadRequest, "model is required")
		return
	}

	routes, err := h.loadAIGatewayRoutes(r.Context(), key.WorkspaceID)
	if err != nil {
		writeAIGatewayError(w, http.StatusInternalServerError, err.Error())
		return
	}
	route, found := findAIGatewayRoute(routes, model)
	if !found {
		writeAIGatewayError(w, http.StatusNotFound, "model is not configured")
		return
	}

	stream, _ := payload["stream"].(bool)
	requestID := uuid.NewString()
	callerID := aiGatewayCallerID(r)
	var lastErr string
	var lastStatus int
	targets := selectAIGatewayTargets(route, model)
	if len(targets) == 0 {
		writeAIGatewayError(w, http.StatusNotFound, "model is not configured")
		return
	}
	for _, target := range targets {
		apiKey := os.Getenv(target.APIKeyEnv)
		if apiKey == "" {
			lastErr = "upstream API key env is not set"
			continue
		}
		targetModel := target.Model
		if targetModel == "" {
			targetModel = model
		}
		upstreamEndpoint, upstreamBody, err := buildAIGatewayUpstreamRequest(endpoint, payload, target, targetModel)
		if err != nil {
			writeAIGatewayError(w, http.StatusBadRequest, err.Error())
			return
		}
		reasoningEffort := extractAIGatewayReasoningEffort(upstreamBody, upstreamEndpoint)
		forwardTarget := target
		if reasoningEffort != "" {
			forwardTarget.ReasoningEffort = reasoningEffort
		}
		status, retry, errText := h.forwardAIGatewayRequest(w, r, aiGatewayForwardRequest{
			Key:              key,
			RequestID:        requestID,
			CallerID:         callerID,
			Endpoint:         endpoint,
			UpstreamEndpoint: upstreamEndpoint,
			ModelAlias:       model,
			Target:           forwardTarget,
			TargetModel:      targetModel,
			APIKey:           apiKey,
			Body:             upstreamBody,
			Stream:           stream,
		})
		if !retry {
			return
		}
		lastStatus = status
		lastErr = errText
		if status > 0 && status < http.StatusInternalServerError && status != http.StatusTooManyRequests {
			break
		}
	}
	if lastErr == "" {
		lastErr = "no usable upstream target configured"
	}
	finalStatus := http.StatusBadGateway
	if lastStatus == http.StatusTooManyRequests {
		finalStatus = http.StatusTooManyRequests
	}
	writeAIGatewayError(w, finalStatus, lastErr)
}

func patchedAIGatewayBody(payload map[string]any, model string, endpoint string, target aiGatewayTarget) ([]byte, error) {
	copyPayload := make(map[string]any, len(payload))
	for k, v := range payload {
		copyPayload[k] = v
	}
	copyPayload["model"] = model
	applyAIGatewayReasoningEffort(copyPayload, endpoint, target.ReasoningEffort)
	return json.Marshal(copyPayload)
}

func selectAIGatewayTargets(route aiGatewayRoute, requestedModel string) []aiGatewayTarget {
	targets := make([]aiGatewayTarget, 0, len(route.Targets))
	codexTemplateTargets := make([]aiGatewayTarget, 0, len(route.Targets))
	for _, target := range route.Targets {
		if target.Enabled || target.ID == "" {
			if route.Alias == "*" && target.Model != "" && target.Model != requestedModel {
				if isAIGatewayCodexModel(requestedModel) && targetCanProxyCodexModel(target) {
					target.Model = requestedModel
					codexTemplateTargets = append(codexTemplateTargets, target)
				}
				continue
			}
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 && len(codexTemplateTargets) > 0 {
		targets = codexTemplateTargets
	}
	if len(targets) <= 1 {
		return targets
	}
	switch route.Strategy {
	case "single":
		return targets[:1]
	case "round_robin":
		idx := int(time.Now().UnixNano() % int64(len(targets)))
		return append(targets[idx:], targets[:idx]...)
	case "weighted":
		total := 0
		for _, target := range targets {
			weight := target.Weight
			if weight <= 0 {
				weight = 1
			}
			total += weight
		}
		if total <= 0 {
			return targets
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
		if err != nil {
			return targets
		}
		pick := int(n.Int64())
		acc := 0
		for i, target := range targets {
			weight := target.Weight
			if weight <= 0 {
				weight = 1
			}
			acc += weight
			if pick < acc {
				return append(targets[i:], targets[:i]...)
			}
		}
	}
	return targets
}

func buildAIGatewayUpstreamRequest(endpoint string, payload map[string]any, target aiGatewayTarget, targetModel string) (string, []byte, error) {
	if target.UpstreamAPI == "chat_completions" && endpoint == "/responses" {
		body, err := responsesPayloadToChatCompletions(payload, targetModel, target)
		return "/chat/completions", body, err
	}
	body, err := patchedAIGatewayBody(payload, targetModel, endpoint, target)
	return endpoint, body, err
}

func responsesPayloadToChatCompletions(payload map[string]any, model string, target aiGatewayTarget) ([]byte, error) {
	messages := []map[string]any{}
	if rawInput, ok := payload["input"]; ok {
		switch input := rawInput.(type) {
		case string:
			messages = append(messages, map[string]any{"role": "user", "content": input})
		case []any:
			for _, item := range input {
				if msg, ok := responseInputItemToChatMessage(item); ok {
					messages = append(messages, msg)
				}
			}
		default:
			return nil, errors.New("unsupported responses input shape for chat_completions upstream")
		}
	}
	if len(messages) == 0 {
		if rawMessages, ok := payload["messages"].([]any); ok {
			for _, item := range rawMessages {
				if msg, ok := responseInputItemToChatMessage(item); ok {
					messages = append(messages, msg)
				}
			}
		}
	}
	if len(messages) == 0 {
		return nil, errors.New("responses input is required for chat_completions upstream")
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	copyIfPresent(body, payload, "stream")
	copyIfPresent(body, payload, "temperature")
	copyIfPresent(body, payload, "top_p")
	copyIfPresent(body, payload, "tools")
	copyIfPresent(body, payload, "tool_choice")
	copyIfPresent(body, payload, "parallel_tool_calls")
	if maxOutput, ok := payload["max_output_tokens"]; ok {
		body["max_tokens"] = maxOutput
	} else {
		copyIfPresent(body, payload, "max_tokens")
	}
	applyAIGatewayReasoningEffort(body, "/chat/completions", target.ReasoningEffort)
	return json.Marshal(body)
}

func applyAIGatewayReasoningEffort(body map[string]any, endpoint string, effort string) {
	effort = strings.TrimSpace(effort)
	if body == nil || effort == "" {
		return
	}
	if endpoint == "/chat/completions" {
		body["reasoning_effort"] = effort
		return
	}
	reasoning := map[string]any{}
	if existing, ok := body["reasoning"].(map[string]any); ok {
		for k, v := range existing {
			reasoning[k] = v
		}
	}
	reasoning["effort"] = effort
	body["reasoning"] = reasoning
}

func extractAIGatewayReasoningEffort(body []byte, endpoint string) string {
	var payload map[string]any
	if len(body) == 0 || json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if endpoint == "/chat/completions" {
		if effort, ok := payload["reasoning_effort"].(string); ok && isAIGatewayReasoningEffort(effort) {
			return effort
		}
	}
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok && isAIGatewayReasoningEffort(effort) {
			return effort
		}
	}
	if effort, ok := payload["reasoning_effort"].(string); ok && isAIGatewayReasoningEffort(effort) {
		return effort
	}
	return ""
}

func isAIGatewayReasoningEffort(effort string) bool {
	switch effort {
	case "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func responseInputItemToChatMessage(item any) (map[string]any, bool) {
	obj, ok := item.(map[string]any)
	if !ok {
		return nil, false
	}
	role, _ := obj["role"].(string)
	if role == "" {
		if typ, _ := obj["type"].(string); typ == "message" {
			role, _ = obj["role"].(string)
		}
	}
	if role == "" {
		return nil, false
	}
	content := normalizeAIGatewayContent(obj["content"])
	msg := map[string]any{"role": role, "content": content}
	if name, _ := obj["name"].(string); name != "" {
		msg["name"] = name
	}
	if toolCallID, _ := obj["tool_call_id"].(string); toolCallID != "" {
		msg["tool_call_id"] = toolCallID
	}
	if toolCalls, ok := obj["tool_calls"]; ok {
		msg["tool_calls"] = toolCalls
	}
	return msg, true
}

func normalizeAIGatewayContent(value any) any {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		parts := make([]any, 0, len(content))
		for _, raw := range content {
			obj, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := obj["type"].(string)
			switch typ {
			case "input_text", "output_text":
				parts = append(parts, map[string]any{"type": "text", "text": stringValue(obj["text"])})
			case "text", "image_url":
				parts = append(parts, obj)
			default:
				if text := stringValue(obj["text"]); text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": text})
				}
			}
		}
		if len(parts) == 1 {
			if part, ok := parts[0].(map[string]any); ok && part["type"] == "text" {
				return part["text"]
			}
		}
		return parts
	default:
		return ""
	}
}

func copyIfPresent(dst map[string]any, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

type aiGatewayForwardRequest struct {
	Key              aiGatewayVirtualKey
	RequestID        string
	CallerID         string
	Endpoint         string
	UpstreamEndpoint string
	ModelAlias       string
	Target           aiGatewayTarget
	TargetModel      string
	APIKey           string
	Body             []byte
	Stream           bool
}

func (h *Handler) forwardAIGatewayRequest(w http.ResponseWriter, r *http.Request, req aiGatewayForwardRequest) (status int, retry bool, errorText string) {
	ctx := r.Context()
	timeout := 60 * time.Second
	if req.Target.TimeoutSeconds > 0 {
		timeout = time.Duration(req.Target.TimeoutSeconds) * time.Second
	}
	client := newAIGatewayHTTPClient(timeout, req.Stream)
	upstreamEndpoint := req.UpstreamEndpoint
	if upstreamEndpoint == "" {
		upstreamEndpoint = req.Endpoint
	}
	upstreamURL := req.Target.BaseURL + upstreamEndpoint
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(req.Body))
	if err != nil {
		h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
			Key:         req.Key,
			RequestID:   req.RequestID,
			CallerID:    req.CallerID,
			Endpoint:    req.Endpoint,
			ModelAlias:  req.ModelAlias,
			Target:      req.Target,
			TargetModel: req.TargetModel,
			StatusCode:  http.StatusBadGateway,
			Error:       err.Error(),
		})
		return http.StatusBadGateway, true, err.Error()
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept-Encoding", "identity")
	upstreamReq.Header.Set("Accept", r.Header.Get("Accept"))
	if upstreamReq.Header.Get("Accept") == "" {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	if org := envValue(req.Target.OrganizationEnv); org != "" {
		upstreamReq.Header.Set("OpenAI-Organization", org)
	}
	if project := envValue(req.Target.ProjectEnv); project != "" {
		upstreamReq.Header.Set("OpenAI-Project", project)
	}

	start := time.Now()
	resp, err := client.Do(upstreamReq)
	latency := time.Since(start)
	if err != nil {
		h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
			Key:         req.Key,
			RequestID:   req.RequestID,
			CallerID:    req.CallerID,
			Endpoint:    req.Endpoint,
			ModelAlias:  req.ModelAlias,
			Target:      req.Target,
			TargetModel: req.TargetModel,
			StatusCode:  http.StatusBadGateway,
			LatencyMs:   latency.Milliseconds(),
			Error:       err.Error(),
		})
		return http.StatusBadGateway, true, err.Error()
	}
	defer resp.Body.Close()

	if shouldRetryAIGatewayStatus(resp.StatusCode) {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		errText := strings.TrimSpace(string(errorBody))
		if errText == "" {
			errText = resp.Status
		}
		h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
			Key:         req.Key,
			RequestID:   req.RequestID,
			CallerID:    req.CallerID,
			Endpoint:    req.Endpoint,
			ModelAlias:  req.ModelAlias,
			Target:      req.Target,
			TargetModel: req.TargetModel,
			StatusCode:  resp.StatusCode,
			LatencyMs:   latency.Milliseconds(),
			Error:       errText,
		})
		return resp.StatusCode, true, errText
	}

	copyAIGatewayHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Multica-AI-Gateway-Request-ID", req.RequestID)

	if req.Stream {
		if req.Target.UpstreamAPI == "chat_completions" && req.Endpoint == "/responses" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(resp.StatusCode)
			n, usage, copyErr := copyChatCompletionStreamAsResponses(w, resp.Body, req)
			errText := ""
			if copyErr != nil {
				errText = copyErr.Error()
			}
			totalLatency := time.Since(start)
			h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
				Key:              req.Key,
				RequestID:        req.RequestID,
				CallerID:         req.CallerID,
				Endpoint:         req.Endpoint,
				ModelAlias:       req.ModelAlias,
				Target:           req.Target,
				TargetModel:      req.TargetModel,
				StatusCode:       resp.StatusCode,
				PromptTokens:     usage.PromptTokens,
				CompletionTokens: usage.CompletionTokens,
				TotalTokens:      usage.TotalTokens,
				LatencyMs:        totalLatency.Milliseconds(),
				Error:            errText,
				Bytes:            n,
			})
			return resp.StatusCode, false, errText
		}
		w.WriteHeader(resp.StatusCode)
		n, usage, copyErr := copyAIGatewayStream(w, resp.Body)
		errText := ""
		if copyErr != nil {
			errText = copyErr.Error()
		}
		totalLatency := time.Since(start)
		h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
			Key:              req.Key,
			RequestID:        req.RequestID,
			CallerID:         req.CallerID,
			Endpoint:         req.Endpoint,
			ModelAlias:       req.ModelAlias,
			Target:           req.Target,
			TargetModel:      req.TargetModel,
			StatusCode:       resp.StatusCode,
			LatencyMs:        totalLatency.Milliseconds(),
			Error:            errText,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			Bytes:            n,
		})
		return resp.StatusCode, false, errText
	}

	data, readErr := io.ReadAll(resp.Body)
	errText := ""
	if readErr != nil {
		errText = readErr.Error()
	}
	usage := parseAIGatewayUsage(data)
	if resp.StatusCode >= 400 && errText == "" {
		errText = strings.TrimSpace(string(data))
	}
	if resp.StatusCode < 400 && req.Target.UpstreamAPI == "chat_completions" && req.Endpoint == "/responses" {
		converted, err := chatCompletionToResponses(data, req)
		if err != nil {
			errText = err.Error()
			resp.StatusCode = http.StatusBadGateway
			data = []byte(`{"error":{"message":"failed to convert chat completion response","type":"multica_ai_gateway_error"}}`)
		} else {
			data = converted
			w.Header().Set("Content-Type", "application/json")
		}
	}
	h.recordAIGatewayUsage(context.Background(), aiGatewayUsageRecord{
		Key:              req.Key,
		RequestID:        req.RequestID,
		CallerID:         req.CallerID,
		Endpoint:         req.Endpoint,
		ModelAlias:       req.ModelAlias,
		Target:           req.Target,
		TargetModel:      req.TargetModel,
		StatusCode:       resp.StatusCode,
		LatencyMs:        time.Since(start).Milliseconds(),
		Error:            errText,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	})
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
	return resp.StatusCode, false, errText
}

func newAIGatewayHTTPClient(timeout time.Duration, stream bool) *http.Client {
	if !stream {
		return &http.Client{Timeout: timeout}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	return &http.Client{Transport: transport}
}

func envValue(name string) string {
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}

func shouldRetryAIGatewayStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func copyAIGatewayHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func copyAIGatewayStream(w http.ResponseWriter, body io.Reader) (int64, aiGatewayUsageTokens, error) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var written int64
	var usageParser aiGatewaySSEUsageParser
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			m, writeErr := w.Write(buf[:n])
			written += int64(m)
			usageParser.Feed(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
			if writeErr != nil {
				usageParser.Finish()
				return written, usageParser.Usage(), writeErr
			}
		}
		if readErr != nil {
			usageParser.Finish()
			if errors.Is(readErr, io.EOF) {
				return written, usageParser.Usage(), nil
			}
			if errors.Is(readErr, context.Canceled) && usageParser.Completed() {
				return written, usageParser.Usage(), nil
			}
			return written, usageParser.Usage(), readErr
		}
	}
}

func chatCompletionToResponses(data []byte, req aiGatewayForwardRequest) ([]byte, error) {
	var chat struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   any    `json:"content"`
				ToolCalls any    `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage any `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(data, &chat); err != nil {
		return nil, err
	}
	if chat.ID == "" {
		chat.ID = "resp_" + req.RequestID
	}
	text := ""
	var toolCalls any
	if len(chat.Choices) > 0 {
		switch content := chat.Choices[0].Message.Content.(type) {
		case string:
			text = content
		case []any:
			parts := make([]string, 0, len(content))
			for _, raw := range content {
				if obj, ok := raw.(map[string]any); ok {
					if obj["type"] == "text" {
						parts = append(parts, stringValue(obj["text"]))
					}
				}
			}
			text = strings.Join(parts, "")
		}
		toolCalls = chat.Choices[0].Message.ToolCalls
	}
	output := []any{
		map[string]any{
			"id":     "msg_" + req.RequestID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{
					"type":        "output_text",
					"text":        text,
					"annotations": []any{},
				},
			},
		},
	}
	if toolCalls != nil {
		if calls, ok := toolCalls.([]any); ok && len(calls) > 0 {
			for _, call := range calls {
				output = append(output, map[string]any{
					"type": "function_call",
					"call": call,
				})
			}
		}
	}
	resp := map[string]any{
		"id":                  chat.ID,
		"object":              "response",
		"created_at":          time.Now().Unix(),
		"status":              "completed",
		"model":               req.TargetModel,
		"output":              output,
		"parallel_tool_calls": true,
	}
	if chat.Usage != nil {
		resp["usage"] = chat.Usage
	}
	return json.Marshal(resp)
}

func copyChatCompletionStreamAsResponses(w http.ResponseWriter, body io.Reader, req aiGatewayForwardRequest) (int64, aiGatewayUsageTokens, error) {
	flusher, _ := w.(http.Flusher)
	responseID := "resp_" + req.RequestID
	itemID := "msg_" + req.RequestID
	var written int64
	writeEvent := func(event string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		n, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		written += int64(n)
		if flusher != nil {
			flusher.Flush()
		}
		return err
	}
	if err := writeEvent("response.created", map[string]any{"response": minimalStreamingResponse(responseID, req.TargetModel, "in_progress", "")}); err != nil {
		return written, aiGatewayUsageTokens{}, err
	}
	if err := writeEvent("response.output_item.added", map[string]any{"response_id": responseID, "output_index": 0, "item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "content": []any{}}}); err != nil {
		return written, aiGatewayUsageTokens{}, err
	}
	if err := writeEvent("response.content_part.added", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}}); err != nil {
		return written, aiGatewayUsageTokens{}, err
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	var usage aiGatewayUsageTokens
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "[DONE]" {
			break
		}
		if chunkUsage := parseAIGatewayUsage([]byte(raw)); chunkUsage.TotalTokens > 0 {
			usage = chunkUsage
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content any `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chatDeltaToText(chunk.Choices[0].Delta.Content)
		if delta == "" {
			continue
		}
		full.WriteString(delta)
		if err := writeEvent("response.output_text.delta", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "delta": delta}); err != nil {
			return written, usage, err
		}
	}
	if err := scanner.Err(); err != nil {
		return written, usage, err
	}
	text := full.String()
	if err := writeEvent("response.output_text.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "text": text}); err != nil {
		return written, usage, err
	}
	if err := writeEvent("response.content_part.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text}}); err != nil {
		return written, usage, err
	}
	if err := writeEvent("response.output_item.done", map[string]any{"response_id": responseID, "output_index": 0, "item": map[string]any{"id": itemID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}}); err != nil {
		return written, usage, err
	}
	completed := minimalStreamingResponse(responseID, req.TargetModel, "completed", text)
	if usage.TotalTokens > 0 {
		completed["usage"] = usage.responsesUsage()
	}
	if err := writeEvent("response.completed", map[string]any{"response": completed}); err != nil {
		return written, usage, err
	}
	n, err := fmt.Fprint(w, "data: [DONE]\n\n")
	written += int64(n)
	if flusher != nil {
		flusher.Flush()
	}
	return written, usage, err
}

func minimalStreamingResponse(id, model, status, text string) map[string]any {
	return map[string]any{
		"id":         id,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     status,
		"model":      model,
		"output": []any{
			map[string]any{
				"id":     strings.Replace(id, "resp_", "msg_", 1),
				"type":   "message",
				"status": status,
				"role":   "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
				},
			},
		},
	}
}

func chatDeltaToText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var b strings.Builder
		for _, raw := range value {
			if obj, ok := raw.(map[string]any); ok && obj["type"] == "text" {
				b.WriteString(stringValue(obj["text"]))
			}
		}
		return b.String()
	default:
		return ""
	}
}

func aiGatewayCallerID(r *http.Request) string {
	for _, header := range []string{"X-Multica-Caller", "X-Multica-User", "X-Codex-User"} {
		if value := sanitizeAIGatewayCallerID(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

func sanitizeAIGatewayCallerID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return -1
		default:
			return r
		}
	}, value)
	if len(value) > 160 {
		return value[:160]
	}
	return value
}

type aiGatewayUsageTokens struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

func (u aiGatewayUsageTokens) responsesUsage() map[string]int64 {
	return map[string]int64{
		"input_tokens":  u.PromptTokens,
		"output_tokens": u.CompletionTokens,
		"total_tokens":  u.TotalTokens,
	}
}

type aiGatewayUsageJSON struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func parseAIGatewayUsage(data []byte) aiGatewayUsageTokens {
	var envelope struct {
		Usage    aiGatewayUsageJSON `json:"usage"`
		Response struct {
			Usage aiGatewayUsageJSON `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return aiGatewayUsageTokens{}
	}
	usage := envelope.Usage
	if usage.TotalTokens == 0 && usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = envelope.Response.Usage
	}
	prompt := usage.PromptTokens
	if prompt == 0 {
		prompt = usage.InputTokens
	}
	completion := usage.CompletionTokens
	if completion == 0 {
		completion = usage.OutputTokens
	}
	total := usage.TotalTokens
	if total == 0 {
		total = prompt + completion
	}
	return aiGatewayUsageTokens{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	}
}

type aiGatewaySSEUsageParser struct {
	buffer    string
	usage     aiGatewayUsageTokens
	completed bool
}

func (p *aiGatewaySSEUsageParser) Feed(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	p.buffer += string(chunk)
	p.buffer = strings.ReplaceAll(p.buffer, "\r\n", "\n")
	for {
		idx := strings.Index(p.buffer, "\n\n")
		if idx < 0 {
			if len(p.buffer) > 1<<20 {
				p.buffer = p.buffer[len(p.buffer)-(1<<20):]
			}
			return
		}
		frame := p.buffer[:idx]
		p.buffer = p.buffer[idx+2:]
		p.parseFrame(frame)
	}
}

func (p *aiGatewaySSEUsageParser) Finish() {
	if strings.TrimSpace(p.buffer) == "" {
		p.buffer = ""
		return
	}
	p.parseFrame(p.buffer)
	p.buffer = ""
}

func (p *aiGatewaySSEUsageParser) Usage() aiGatewayUsageTokens {
	return p.usage
}

func (p *aiGatewaySSEUsageParser) Completed() bool {
	return p.completed
}

func (p *aiGatewaySSEUsageParser) parseFrame(frame string) {
	event := ""
	var dataLines []string
	for _, line := range strings.Split(frame, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return
	}
	raw := strings.Join(dataLines, "\n")
	if raw == "[DONE]" {
		return
	}
	if event == "response.completed" || isAIGatewayResponseCompletedEvent(raw) {
		p.completed = true
	}
	if usage := parseAIGatewayUsage([]byte(raw)); usage.TotalTokens > 0 {
		p.usage = usage
	}
}

func isAIGatewayResponseCompletedEvent(raw string) bool {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return false
	}
	return event.Type == "response.completed" || event.Response.Status == "completed"
}

type aiGatewayUsageRecord struct {
	Key              aiGatewayVirtualKey
	RequestID        string
	CallerID         string
	Endpoint         string
	ModelAlias       string
	Target           aiGatewayTarget
	TargetModel      string
	StatusCode       int
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	InputCostMicros  int64
	OutputCostMicros int64
	TotalCostMicros  int64
	LatencyMs        int64
	Error            string
	Bytes            int64
}

func (h *Handler) recordAIGatewayUsage(ctx context.Context, record aiGatewayUsageRecord) {
	if h.DB == nil || record.Key.ID == "" || record.Key.WorkspaceID == "" {
		return
	}
	errText := record.Error
	if len(errText) > 2048 {
		errText = errText[:2048]
	}
	inputCost := record.InputCostMicros
	if inputCost == 0 && record.PromptTokens > 0 && record.Target.InputPricePerMillionMicros > 0 {
		inputCost = record.PromptTokens * record.Target.InputPricePerMillionMicros / 1_000_000
	}
	outputCost := record.OutputCostMicros
	if outputCost == 0 && record.CompletionTokens > 0 && record.Target.OutputPricePerMillionMicros > 0 {
		outputCost = record.CompletionTokens * record.Target.OutputPricePerMillionMicros / 1_000_000
	}
	totalCost := record.TotalCostMicros
	if totalCost == 0 {
		totalCost = inputCost + outputCost
	}
	reasoningEffort := strings.TrimSpace(record.Target.ReasoningEffort)
	_, err := h.DB.Exec(ctx, `
		INSERT INTO ai_gateway_usage (
			virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
			upstream_provider, upstream_model, reasoning_effort, status_code, prompt_tokens,
			completion_tokens, total_tokens, latency_ms, error,
			input_cost_micros, output_cost_micros, total_cost_micros
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, NULLIF($15, ''), $16, $17, $18)
	`,
		parseUUID(record.Key.ID),
		parseUUID(record.Key.WorkspaceID),
		record.RequestID,
		record.CallerID,
		record.Endpoint,
		record.ModelAlias,
		record.Target.Provider,
		record.TargetModel,
		reasoningEffort,
		int32(record.StatusCode),
		record.PromptTokens,
		record.CompletionTokens,
		record.TotalTokens,
		record.LatencyMs,
		errText,
		inputCost,
		outputCost,
		totalCost,
	)
	if err != nil && (strings.Contains(err.Error(), "reasoning_effort") || strings.Contains(err.Error(), "input_cost_micros")) {
		_, _ = h.DB.Exec(ctx, `
			INSERT INTO ai_gateway_usage (
				virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
				upstream_provider, upstream_model, status_code, prompt_tokens,
				completion_tokens, total_tokens, latency_ms, error
			)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, ''))
		`,
			parseUUID(record.Key.ID),
			parseUUID(record.Key.WorkspaceID),
			record.RequestID,
			record.CallerID,
			record.Endpoint,
			record.ModelAlias,
			record.Target.Provider,
			record.TargetModel,
			int32(record.StatusCode),
			record.PromptTokens,
			record.CompletionTokens,
			record.TotalTokens,
			record.LatencyMs,
			errText,
		)
	}
}

func writeAIGatewayError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"message": msg,
			"type":    "multica_ai_gateway_error",
		},
	})
}
