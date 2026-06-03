package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/aigateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
)

const (
	aiGatewayTokenPrefix                = "mvk_"
	aiGatewayDefaultURL                 = "https://api.openai.com/v1"
	aiGatewayReportDefaultWorkspaceSlug = "local-agents"
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
	ID                          string                      `json:"id"`
	Provider                    string                      `json:"provider"`
	BaseURL                     string                      `json:"base_url"`
	AuthMode                    string                      `json:"auth_mode,omitempty"`
	APIKeyEnv                   string                      `json:"api_key_env"`
	CookieEnv                   string                      `json:"cookie_env,omitempty"`
	CustomHeaderEnvs            []aigateway.CustomHeaderEnv `json:"custom_header_envs,omitempty"`
	Model                       string                      `json:"model"`
	UpstreamAPI                 string                      `json:"upstream_api"`
	ReasoningEffort             string                      `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string                      `json:"organization_env,omitempty"`
	ProjectEnv                  string                      `json:"project_env,omitempty"`
	TimeoutSeconds              int                         `json:"timeout_seconds"`
	Weight                      int                         `json:"weight"`
	Priority                    int                         `json:"priority"`
	Enabled                     bool                        `json:"enabled"`
	InputPricePerMillionMicros  int64                       `json:"input_price_per_million_micros"`
	OutputPricePerMillionMicros int64                       `json:"output_price_per_million_micros"`
}

type saveAIGatewayRouteRequest struct {
	Alias    string                         `json:"alias"`
	Strategy string                         `json:"strategy"`
	Enabled  *bool                          `json:"enabled"`
	Targets  []saveAIGatewayRouteTargetForm `json:"targets"`
}

type saveAIGatewayRouteTargetForm struct {
	ID                          string                      `json:"id,omitempty"`
	Provider                    string                      `json:"provider"`
	BaseURL                     string                      `json:"base_url"`
	AuthMode                    string                      `json:"auth_mode,omitempty"`
	APIKeyEnv                   string                      `json:"api_key_env"`
	CookieEnv                   string                      `json:"cookie_env,omitempty"`
	CustomHeaderEnvs            []aigateway.CustomHeaderEnv `json:"custom_header_envs,omitempty"`
	Model                       string                      `json:"model"`
	UpstreamAPI                 string                      `json:"upstream_api"`
	ReasoningEffort             string                      `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string                      `json:"organization_env,omitempty"`
	ProjectEnv                  string                      `json:"project_env,omitempty"`
	TimeoutSeconds              int                         `json:"timeout_seconds,omitempty"`
	Weight                      int                         `json:"weight,omitempty"`
	Priority                    int                         `json:"priority,omitempty"`
	Enabled                     *bool                       `json:"enabled,omitempty"`
	InputPricePerMillionMicros  int64                       `json:"input_price_per_million_micros,omitempty"`
	OutputPricePerMillionMicros int64                       `json:"output_price_per_million_micros,omitempty"`
}

type aiGatewayProbeRequest struct {
	BaseURL          string                      `json:"base_url"`
	AuthMode         string                      `json:"auth_mode,omitempty"`
	APIKeyEnv        string                      `json:"api_key_env,omitempty"`
	APIKey           string                      `json:"api_key,omitempty"`
	CookieEnv        string                      `json:"cookie_env,omitempty"`
	CustomHeaderEnvs []aigateway.CustomHeaderEnv `json:"custom_header_envs,omitempty"`
	Model            string                      `json:"model,omitempty"`
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
	email, err := normalizeAIGatewayKeyEmail(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = email

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

func normalizeAIGatewayKeyEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", errors.New("email is required")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || addr.Name != "" {
		return "", errors.New("email must be a valid email address")
	}
	return email, nil
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
		ID:             "he-tokenapi-wildcard",
		Name:           "HE TokenAPI Pass-through",
		Provider:       "he-tokenapi",
		BaseURL:        "https://tokenapi.happyelements.net/v1",
		APIKeyEnv:      "HAPPYELEMENTS_TOKENAPI_API_KEY",
		Model:          "",
		UpstreamAPI:    "responses",
		EndpointTypes:  []string{"responses"},
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
	req.AuthMode = strings.TrimSpace(req.AuthMode)
	req.APIKeyEnv = strings.TrimSpace(req.APIKeyEnv)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.CookieEnv = strings.TrimSpace(req.CookieEnv)
	req.Model = strings.TrimSpace(req.Model)
	if req.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	target := aigateway.Target{
		BaseURL:          req.BaseURL,
		AuthMode:         req.AuthMode,
		APIKeyEnv:        req.APIKeyEnv,
		CookieEnv:        req.CookieEnv,
		CustomHeaderEnvs: req.CustomHeaderEnvs,
	}
	authHeaders := make(http.Header)
	if req.APIKey != "" {
		authHeaders.Set("Authorization", "Bearer "+req.APIKey)
	} else {
		auth, err := aigateway.ResolveUpstreamAuth(target)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		authHeaders = auth.Headers
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp := aiGatewayProbeResponse{BaseURL: req.BaseURL}
	resp.ModelsEndpoint, resp.Models = probeAIGatewayModels(r.Context(), client, req.BaseURL, authHeaders)
	resp.Authenticated = resp.ModelsEndpoint.OK
	resp.Responses = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/responses", authHeaders, req.Model, "responses")
	resp.ChatCompletions = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/chat/completions", authHeaders, req.Model, "chat_completions")
	resp.AnthropicMessages = probeAIGatewayJSONEndpoint(r.Context(), client, req.BaseURL+"/messages", authHeaders, req.Model, "anthropic_messages")
	writeJSON(w, http.StatusOK, resp)
}

func probeAIGatewayModels(ctx context.Context, client *http.Client, baseURL string, authHeaders http.Header) (aiGatewayProbeEndpoint, []aiGatewayProbeModelInfo) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return aiGatewayProbeEndpoint{Error: err.Error()}, nil
	}
	for key, values := range authHeaders {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
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

func probeAIGatewayJSONEndpoint(ctx context.Context, client *http.Client, url string, authHeaders http.Header, model, kind string) aiGatewayProbeEndpoint {
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
	for key, values := range authHeaders {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
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
	CallerID                string `json:"caller_id"`
	Model                   string `json:"model"`
	KeyName                 string `json:"key_name,omitempty"`
	KeyPrefix               string `json:"key_prefix,omitempty"`
	CreatedByName           string `json:"created_by_name,omitempty"`
	CreatedByEmail          string `json:"created_by_email,omitempty"`
	RequestCount            int64  `json:"request_count"`
	SuccessCount            int64  `json:"success_count"`
	ErrorCount              int64  `json:"error_count"`
	PromptTokens            int64  `json:"prompt_tokens"`
	CompletionTokens        int64  `json:"completion_tokens"`
	CachedInputTokens       int64  `json:"cached_input_tokens"`
	BillableInputTokens     int64  `json:"billable_input_tokens"`
	ReasoningTokens         int64  `json:"reasoning_tokens"`
	TotalTokens             int64  `json:"total_tokens"`
	InputCostMicros         int64  `json:"input_cost_micros"`
	CachedInputCostMicros   int64  `json:"cached_input_cost_micros"`
	OutputCostMicros        int64  `json:"output_cost_micros"`
	TotalCostMicros         int64  `json:"total_cost_micros"`
	LongContextRequestCount int64  `json:"long_context_request_count"`
	AverageLatencyMs        int64  `json:"average_latency_ms"`
	LastRequestAt           string `json:"last_request_at"`
}

type aiGatewayPublicUsageSummaryResponse struct {
	Email                   string `json:"email"`
	Model                   string `json:"model"`
	RequestCount            int64  `json:"request_count"`
	SuccessCount            int64  `json:"success_count"`
	ErrorCount              int64  `json:"error_count"`
	InputTokens             int64  `json:"input_tokens"`
	CachedInputTokens       int64  `json:"cached_input_tokens"`
	BillableInputTokens     int64  `json:"billable_input_tokens"`
	OutputTokens            int64  `json:"output_tokens"`
	ReasoningTokens         int64  `json:"reasoning_tokens"`
	TotalTokens             int64  `json:"total_tokens"`
	InputCostMicros         int64  `json:"input_cost_micros"`
	CachedInputCostMicros   int64  `json:"cached_input_cost_micros"`
	OutputCostMicros        int64  `json:"output_cost_micros"`
	TotalCostMicros         int64  `json:"total_cost_micros"`
	LongContextRequestCount int64  `json:"long_context_request_count"`
	AverageLatencyMs        int64  `json:"average_latency_ms"`
	LastRequestAt           string `json:"last_request_at"`
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
			COALESCE(u.auth_mode, ''),
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
	legacyColumns := false
	if err != nil && isAIGatewayAuthModeColumnError(err) {
		legacyColumns = true
		rows, err = h.DB.Query(r.Context(), `
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
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage")
		return
	}
	defer rows.Close()

	resp := []aiGatewayUsageResponse{}
	for rows.Next() {
		var id pgtype.UUID
		var createdAt pgtype.Timestamptz
		var authMode string
		var item aiGatewayUsageResponse
		if legacyColumns {
			authMode = aigateway.AuthModeAPIKey
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
		} else {
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
				&authMode,
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
		}
		item.ID = uuidToString(id)
		item.CreatedAt = timestampToString(createdAt)
		if item.TotalCostMicros == 0 && shouldEstimateAIGatewayUsageCost(authMode) {
			item.TotalCostMicros = aigateway.EstimateUsageCostMicros(item.UpstreamModel, item.PromptTokens, item.CompletionTokens)
		}
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
	days, ok := parseAIGatewayUsageSummaryDays(w, r)
	if !ok {
		return
	}
	resp, err := h.listAIGatewayUsageSummary(r.Context(), workspaceID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage summary")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListPublicAIGatewayUsageSummary(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil || h.Queries == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	workspaceSlug := strings.TrimSpace(r.URL.Query().Get("workspace_slug"))
	if workspaceSlug == "" {
		workspaceSlug = aiGatewayReportDefaultWorkspaceSlug
	}
	days := int32(30)
	if parsedDays, ok := parseAIGatewayUsageSummaryDays(w, r); ok {
		days = parsedDays
	} else {
		return
	}
	workspace, err := h.Queries.GetWorkspaceBySlug(r.Context(), workspaceSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	resp, err := h.listAIGatewayUsageSummary(r.Context(), uuidToString(workspace.ID), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list AI gateway usage summary")
		return
	}
	publicResp := make([]aiGatewayPublicUsageSummaryResponse, 0, len(resp))
	for _, item := range resp {
		email, err := normalizeAIGatewayKeyEmail(item.CallerID)
		if err != nil {
			continue
		}
		publicResp = append(publicResp, aiGatewayPublicUsageSummaryResponse{
			Email:                   email,
			Model:                   item.Model,
			RequestCount:            item.RequestCount,
			SuccessCount:            item.SuccessCount,
			ErrorCount:              item.ErrorCount,
			InputTokens:             item.PromptTokens,
			CachedInputTokens:       item.CachedInputTokens,
			BillableInputTokens:     item.BillableInputTokens,
			OutputTokens:            item.CompletionTokens,
			ReasoningTokens:         item.ReasoningTokens,
			TotalTokens:             item.TotalTokens,
			InputCostMicros:         item.InputCostMicros,
			CachedInputCostMicros:   item.CachedInputCostMicros,
			OutputCostMicros:        item.OutputCostMicros,
			TotalCostMicros:         item.TotalCostMicros,
			LongContextRequestCount: item.LongContextRequestCount,
			AverageLatencyMs:        item.AverageLatencyMs,
			LastRequestAt:           item.LastRequestAt,
		})
	}
	writeJSON(w, http.StatusOK, publicResp)
}

func parseAIGatewayUsageSummaryDays(w http.ResponseWriter, r *http.Request) (int32, bool) {
	days := int32(30)
	if raw := r.URL.Query().Get("days"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 365 {
			writeError(w, http.StatusBadRequest, "days must be between 1 and 365")
			return 0, false
		}
		days = int32(v)
	}
	return days, true
}

func aiGatewayUsageModelLabel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	return model
}

func shouldEstimateAIGatewayUsageCost(authMode string) bool {
	return strings.TrimSpace(authMode) != aigateway.AuthModeCustomHeadersCookie
}

func isAIGatewayAuthModeColumnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "auth_mode") ||
		strings.Contains(msg, "cookie_env") ||
		strings.Contains(msg, "custom_header_envs")
}

func (h *Handler) listAIGatewayUsageSummary(ctx context.Context, workspaceID string, days int32) ([]aiGatewayUsageSummaryResponse, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT
			COALESCE(NULLIF(u.caller_id, ''), NULLIF(creator.email, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown') AS caller_id,
			COALESCE(k.name, ''),
			COALESCE(k.token_prefix, ''),
			COALESCE(creator.name, ''),
			COALESCE(creator.email, ''),
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE u.status_code < 400)::bigint,
			COUNT(*) FILTER (WHERE u.status_code >= 400)::bigint,
			COALESCE(SUM(u.prompt_tokens), 0)::bigint,
			COALESCE(SUM(u.completion_tokens), 0)::bigint,
			COALESCE(SUM(u.cached_input_tokens), 0)::bigint,
			COALESCE(SUM(u.billable_input_tokens), 0)::bigint,
			COALESCE(SUM(u.reasoning_tokens), 0)::bigint,
			COALESCE(SUM(u.total_tokens), 0)::bigint,
			COALESCE(SUM(u.input_cost_micros), 0)::bigint,
			COALESCE(SUM(u.cached_input_cost_micros), 0)::bigint,
			COALESCE(SUM(u.output_cost_micros), 0)::bigint,
			COALESCE(SUM(u.total_cost_micros), 0)::bigint,
			COALESCE(SUM(u.prompt_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
			COALESCE(SUM(u.completion_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
			COALESCE(SUM(u.cached_input_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
			COALESCE(SUM(u.latency_ms), 0)::bigint,
			MAX(u.created_at),
			COALESCE(u.upstream_model, ''),
			COALESCE(u.auth_mode, ''),
			COUNT(*) FILTER (WHERE u.long_context)::bigint
		FROM ai_gateway_usage u
		LEFT JOIN ai_gateway_virtual_key k ON k.id = u.virtual_key_id
		LEFT JOIN "user" creator ON creator.id = k.created_by
		WHERE u.workspace_id = $1
		  AND u.created_at >= now() - ($2::int * interval '1 day')
		GROUP BY
			COALESCE(NULLIF(u.caller_id, ''), NULLIF(creator.email, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown'),
			k.name,
			k.token_prefix,
			creator.name,
			creator.email,
			COALESCE(u.upstream_model, ''),
			COALESCE(u.auth_mode, '')
		ORDER BY COALESCE(SUM(u.total_tokens), 0) DESC, COUNT(*) DESC
	`, parseUUID(workspaceID), days)
	legacyColumns := false
	if err != nil && isAIGatewayAuthModeColumnError(err) {
		legacyColumns = true
		rows, err = h.DB.Query(ctx, `
			SELECT
				COALESCE(NULLIF(u.caller_id, ''), NULLIF(creator.email, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown') AS caller_id,
				COALESCE(k.name, ''),
				COALESCE(k.token_prefix, ''),
				COALESCE(creator.name, ''),
				COALESCE(creator.email, ''),
				COUNT(*)::bigint,
				COUNT(*) FILTER (WHERE u.status_code < 400)::bigint,
				COUNT(*) FILTER (WHERE u.status_code >= 400)::bigint,
				COALESCE(SUM(u.prompt_tokens), 0)::bigint,
				COALESCE(SUM(u.completion_tokens), 0)::bigint,
				COALESCE(SUM(u.cached_input_tokens), 0)::bigint,
				COALESCE(SUM(u.billable_input_tokens), 0)::bigint,
				COALESCE(SUM(u.reasoning_tokens), 0)::bigint,
				COALESCE(SUM(u.total_tokens), 0)::bigint,
				COALESCE(SUM(u.input_cost_micros), 0)::bigint,
				COALESCE(SUM(u.cached_input_cost_micros), 0)::bigint,
				COALESCE(SUM(u.output_cost_micros), 0)::bigint,
				COALESCE(SUM(u.total_cost_micros), 0)::bigint,
				COALESCE(SUM(u.prompt_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
				COALESCE(SUM(u.completion_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
				COALESCE(SUM(u.cached_input_tokens) FILTER (WHERE u.total_cost_micros = 0), 0)::bigint,
				COALESCE(SUM(u.latency_ms), 0)::bigint,
				MAX(u.created_at),
				COALESCE(u.upstream_model, ''),
				COUNT(*) FILTER (WHERE u.long_context)::bigint
			FROM ai_gateway_usage u
			LEFT JOIN ai_gateway_virtual_key k ON k.id = u.virtual_key_id
			LEFT JOIN "user" creator ON creator.id = k.created_by
			WHERE u.workspace_id = $1
			  AND u.created_at >= now() - ($2::int * interval '1 day')
			GROUP BY
				COALESCE(NULLIF(u.caller_id, ''), NULLIF(creator.email, ''), NULLIF(k.name, ''), k.token_prefix, 'unknown'),
				k.name,
				k.token_prefix,
				creator.name,
				creator.email,
				COALESCE(u.upstream_model, '')
			ORDER BY COALESCE(SUM(u.total_tokens), 0) DESC, COUNT(*) DESC
		`, parseUUID(workspaceID), days)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type summaryAgg struct {
		item        aiGatewayUsageSummaryResponse
		latencySum  int64
		lastRequest pgtype.Timestamptz
	}
	summaryByCaller := map[string]*summaryAgg{}
	order := []string{}
	for rows.Next() {
		var row aiGatewayUsageSummaryResponse
		var lastRequestAt pgtype.Timestamptz
		var zeroCostPromptTokens, zeroCostCompletionTokens, zeroCostCachedInputTokens, latencySum int64
		var upstreamModel, authMode string
		if legacyColumns {
			authMode = aigateway.AuthModeAPIKey
			if err := rows.Scan(
				&row.CallerID,
				&row.KeyName,
				&row.KeyPrefix,
				&row.CreatedByName,
				&row.CreatedByEmail,
				&row.RequestCount,
				&row.SuccessCount,
				&row.ErrorCount,
				&row.PromptTokens,
				&row.CompletionTokens,
				&row.CachedInputTokens,
				&row.BillableInputTokens,
				&row.ReasoningTokens,
				&row.TotalTokens,
				&row.InputCostMicros,
				&row.CachedInputCostMicros,
				&row.OutputCostMicros,
				&row.TotalCostMicros,
				&zeroCostPromptTokens,
				&zeroCostCompletionTokens,
				&zeroCostCachedInputTokens,
				&latencySum,
				&lastRequestAt,
				&upstreamModel,
				&row.LongContextRequestCount,
			); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(
				&row.CallerID,
				&row.KeyName,
				&row.KeyPrefix,
				&row.CreatedByName,
				&row.CreatedByEmail,
				&row.RequestCount,
				&row.SuccessCount,
				&row.ErrorCount,
				&row.PromptTokens,
				&row.CompletionTokens,
				&row.CachedInputTokens,
				&row.BillableInputTokens,
				&row.ReasoningTokens,
				&row.TotalTokens,
				&row.InputCostMicros,
				&row.CachedInputCostMicros,
				&row.OutputCostMicros,
				&row.TotalCostMicros,
				&zeroCostPromptTokens,
				&zeroCostCompletionTokens,
				&zeroCostCachedInputTokens,
				&latencySum,
				&lastRequestAt,
				&upstreamModel,
				&authMode,
				&row.LongContextRequestCount,
			); err != nil {
				return nil, err
			}
		}
		row.Model = aiGatewayUsageModelLabel(upstreamModel)
		if shouldEstimateAIGatewayUsageCost(authMode) {
			estimated := aigateway.EstimateUsageCostBreakdown(upstreamModel, zeroCostPromptTokens, zeroCostCompletionTokens, zeroCostCachedInputTokens)
			row.InputCostMicros += estimated.InputCostMicros
			row.CachedInputCostMicros += estimated.CachedInputCostMicros
			row.OutputCostMicros += estimated.OutputCostMicros
			row.TotalCostMicros += estimated.TotalCostMicros
		}
		if row.BillableInputTokens == 0 && row.PromptTokens > 0 {
			row.BillableInputTokens = row.PromptTokens - row.CachedInputTokens
			if row.BillableInputTokens < 0 {
				row.BillableInputTokens = 0
			}
		}
		aggKey := row.CallerID + "\x00" + row.Model
		agg := summaryByCaller[aggKey]
		if agg == nil {
			agg = &summaryAgg{item: aiGatewayUsageSummaryResponse{
				CallerID:       row.CallerID,
				Model:          row.Model,
				KeyName:        row.KeyName,
				KeyPrefix:      row.KeyPrefix,
				CreatedByName:  row.CreatedByName,
				CreatedByEmail: row.CreatedByEmail,
			}}
			summaryByCaller[aggKey] = agg
			order = append(order, aggKey)
		}
		agg.item.RequestCount += row.RequestCount
		agg.item.SuccessCount += row.SuccessCount
		agg.item.ErrorCount += row.ErrorCount
		agg.item.PromptTokens += row.PromptTokens
		agg.item.CompletionTokens += row.CompletionTokens
		agg.item.CachedInputTokens += row.CachedInputTokens
		agg.item.BillableInputTokens += row.BillableInputTokens
		agg.item.ReasoningTokens += row.ReasoningTokens
		agg.item.TotalTokens += row.TotalTokens
		agg.item.InputCostMicros += row.InputCostMicros
		agg.item.CachedInputCostMicros += row.CachedInputCostMicros
		agg.item.OutputCostMicros += row.OutputCostMicros
		agg.item.TotalCostMicros += row.TotalCostMicros
		agg.item.LongContextRequestCount += row.LongContextRequestCount
		agg.latencySum += latencySum
		if !agg.lastRequest.Valid || (lastRequestAt.Valid && lastRequestAt.Time.After(agg.lastRequest.Time)) {
			agg.lastRequest = lastRequestAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	resp := []aiGatewayUsageSummaryResponse{}
	for _, key := range order {
		agg := summaryByCaller[key]
		if agg.item.RequestCount > 0 {
			agg.item.AverageLatencyMs = agg.latencySum / agg.item.RequestCount
		}
		agg.item.LastRequestAt = timestampToString(agg.lastRequest)
		resp = append(resp, agg.item)
	}
	sort.SliceStable(resp, func(i, j int) bool {
		if resp[i].TotalTokens != resp[j].TotalTokens {
			return resp[i].TotalTokens > resp[j].TotalTokens
		}
		return resp[i].RequestCount > resp[j].RequestCount
	})
	if len(resp) > 100 {
		resp = resp[:100]
	}
	return resp, nil
}

type aiGatewayVirtualKey = aigateway.VirtualKey
type aiGatewayRoute = aigateway.Route
type aiGatewayTarget = aigateway.Target
type aiGatewayForwardRequest = aigateway.ForwardRequest
type aiGatewayUsageTokens = aigateway.UsageTokens
type aiGatewayUsageRecord = aigateway.UsageRecord

func loadAIGatewayRoutesFromEnv() ([]aiGatewayRoute, error) { return aigateway.LoadRoutesFromEnv() }
func normalizeAIGatewayRoutes(routes []aiGatewayRoute) ([]aiGatewayRoute, error) {
	return aigateway.NormalizeRoutes(routes)
}
func findAIGatewayRoute(routes []aiGatewayRoute, model string) (aiGatewayRoute, bool) {
	return aigateway.FindRoute(routes, model)
}
func patchedAIGatewayBody(payload map[string]any, model string, endpoint string, target aiGatewayTarget) ([]byte, error) {
	return aigateway.PatchedBody(payload, model, endpoint, target)
}
func buildAIGatewayUpstreamRequest(endpoint string, payload map[string]any, target aiGatewayTarget, targetModel string) (string, []byte, error) {
	return aigateway.BuildUpstreamRequest(endpoint, payload, target, targetModel)
}
func responsesPayloadToChatCompletions(payload map[string]any, model string, target aiGatewayTarget) ([]byte, error) {
	return aigateway.ResponsesPayloadToChatCompletions(payload, model, target)
}
func chatCompletionToResponses(data []byte, req aiGatewayForwardRequest) ([]byte, error) {
	return aigateway.ChatCompletionToResponses(data, req)
}
func isAIGatewayReasoningEffort(effort string) bool { return aigateway.IsReasoningEffort(effort) }
func copyAIGatewayStream(w http.ResponseWriter, body io.Reader) (int64, aiGatewayUsageTokens, error) {
	written, usage, _, err := aigateway.CopyStream(w, body)
	return written, usage, err
}
func parseAIGatewayUsage(data []byte) aiGatewayUsageTokens { return aigateway.ParseUsage(data) }
func (h *Handler) aiGatewayRuntime() *aigateway.Runtime    { return aigateway.NewRuntime(h.DB) }
func (h *Handler) resolveAIGatewayVirtualKey(ctx context.Context, token string) (aiGatewayVirtualKey, bool, error) {
	return h.aiGatewayRuntime().ResolveVirtualKey(ctx, token)
}
func (h *Handler) touchAIGatewayKey(ctx context.Context, keyID string) {
	h.aiGatewayRuntime().TouchKey(ctx, keyID)
}
func (h *Handler) loadAIGatewayRoutes(ctx context.Context, workspaceID string) ([]aiGatewayRoute, error) {
	return h.aiGatewayRuntime().LoadRoutes(ctx, workspaceID)
}
func (h *Handler) loadAIGatewayRoutesFromDB(ctx context.Context, workspaceID string, includeDisabled bool) ([]aiGatewayRoute, error) {
	return h.aiGatewayRuntime().LoadRoutesFromDB(ctx, workspaceID, includeDisabled)
}
func (h *Handler) AIGatewayModels(w http.ResponseWriter, r *http.Request) {
	h.aiGatewayRuntime().Models(w, r)
}
func (h *Handler) AIGatewayResponses(w http.ResponseWriter, r *http.Request) {
	h.aiGatewayRuntime().Responses(w, r)
}
func (h *Handler) AIGatewayChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.aiGatewayRuntime().ChatCompletions(w, r)
}
func (h *Handler) recordAIGatewayUsage(ctx context.Context, record aiGatewayUsageRecord) {
	h.aiGatewayRuntime().RecordUsage(ctx, record)
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
			AuthMode:                    strings.TrimSpace(raw.AuthMode),
			APIKeyEnv:                   strings.TrimSpace(raw.APIKeyEnv),
			CookieEnv:                   strings.TrimSpace(raw.CookieEnv),
			CustomHeaderEnvs:            raw.CustomHeaderEnvs,
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
		if target.ReasoningEffort != "" && !isAIGatewayReasoningEffort(target.ReasoningEffort) {
			return aiGatewayRoute{}, fmt.Errorf("target %d reasoning_effort is unsupported", i)
		}
		if raw.Enabled != nil {
			target.Enabled = *raw.Enabled
		}
		if target.InputPricePerMillionMicros < 0 || target.OutputPricePerMillionMicros < 0 {
			return aiGatewayRoute{}, fmt.Errorf("target %d price must be non-negative", i)
		}
		route.Targets = append(route.Targets, target)
	}
	routes, err := normalizeAIGatewayRoutes([]aiGatewayRoute{route})
	if err != nil {
		return aiGatewayRoute{}, err
	}
	return routes[0], nil
}

func insertAIGatewayRouteTarget(ctx context.Context, db dbExecutor, routeID pgtype.UUID, target aiGatewayTarget, fallbackPriority int) error {
	priority := target.Priority
	if priority == 0 {
		priority = fallbackPriority
	}
	customHeaderEnvs := []byte("[]")
	if len(target.CustomHeaderEnvs) > 0 {
		data, err := json.Marshal(target.CustomHeaderEnvs)
		if err != nil {
			return fmt.Errorf("marshal custom_header_envs: %w", err)
		}
		customHeaderEnvs = data
	}
	_, err := db.Exec(ctx, `
		INSERT INTO ai_gateway_route_target (
			route_id, provider, base_url, auth_mode, api_key_env, cookie_env, custom_header_envs, model, upstream_api,
			reasoning_effort, organization_env, project_env, timeout_seconds, weight, priority, enabled,
			input_price_per_million_micros, output_price_per_million_micros
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7::jsonb, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), $13, $14, $15, $16, $17, $18)
	`, routeID, target.Provider, target.BaseURL, target.AuthMode, target.APIKeyEnv, target.CookieEnv, string(customHeaderEnvs), target.Model, target.UpstreamAPI,
		target.ReasoningEffort, target.OrganizationEnv, target.ProjectEnv, target.TimeoutSeconds, target.Weight, priority, target.Enabled,
		target.InputPricePerMillionMicros, target.OutputPricePerMillionMicros)
	if err != nil && isAIGatewayAuthModeColumnError(err) {
		_, err = db.Exec(ctx, `
			INSERT INTO ai_gateway_route_target (
				route_id, provider, base_url, api_key_env, model, upstream_api,
				reasoning_effort, organization_env, project_env, timeout_seconds, weight, priority, enabled,
				input_price_per_million_micros, output_price_per_million_micros
			)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, $13, $14, $15)
		`, routeID, target.Provider, target.BaseURL, target.APIKeyEnv, target.Model, target.UpstreamAPI,
			target.ReasoningEffort, target.OrganizationEnv, target.ProjectEnv, target.TimeoutSeconds, target.Weight, priority, target.Enabled,
			target.InputPricePerMillionMicros, target.OutputPricePerMillionMicros)
	}
	return err
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
				AuthMode:                    target.AuthMode,
				APIKeyEnv:                   target.APIKeyEnv,
				CookieEnv:                   target.CookieEnv,
				CustomHeaderEnvs:            target.CustomHeaderEnvs,
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
