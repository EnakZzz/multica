package aigateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	TokenPrefix = "mvk_"
	DefaultURL  = "https://api.openai.com/v1"
)

type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Runtime struct {
	DB DB
}

func NewRuntime(db DB) *Runtime {
	return &Runtime{DB: db}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type VirtualKey struct {
	ID             string
	WorkspaceID    string
	Name           string
	CreatedByEmail string
}

func (key VirtualKey) CallerID() string {
	if value := SanitizeCallerID(key.Name); strings.Contains(value, "@") {
		return value
	}
	return SanitizeCallerID(key.CreatedByEmail)
}

func (rt *Runtime) ResolveVirtualKey(ctx context.Context, token string) (VirtualKey, bool, error) {
	if rt.DB == nil {
		return VirtualKey{}, false, errors.New("database unavailable")
	}
	if !strings.HasPrefix(token, TokenPrefix) {
		return VirtualKey{}, false, nil
	}
	hash := auth.HashToken(token)
	var id pgtype.UUID
	var workspaceID pgtype.UUID
	var name string
	var createdByEmail string
	err := rt.DB.QueryRow(ctx, `
		SELECT k.id, k.workspace_id, k.name, COALESCE(u.email, '')
		FROM ai_gateway_virtual_key k
		LEFT JOIN "user" u ON u.id = k.created_by
		WHERE k.token_hash = $1
		  AND k.status = 'active'
		  AND (k.expires_at IS NULL OR k.expires_at > now())
	`, hash).Scan(&id, &workspaceID, &name, &createdByEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return VirtualKey{}, false, nil
	}
	if err != nil {
		return VirtualKey{}, false, err
	}
	key := VirtualKey{
		ID:             util.UUIDToString(id),
		WorkspaceID:    util.UUIDToString(workspaceID),
		Name:           name,
		CreatedByEmail: createdByEmail,
	}
	go rt.TouchKey(context.Background(), key.ID)
	return key, true, nil
}

func (rt *Runtime) TouchKey(ctx context.Context, keyID string) {
	if rt.DB == nil {
		return
	}
	_, _ = rt.DB.Exec(ctx, `UPDATE ai_gateway_virtual_key SET last_used_at = now() WHERE id = $1`, util.MustParseUUID(keyID))
}

type Route struct {
	ID        string   `json:"id,omitempty"`
	Alias     string   `json:"alias"`
	Strategy  string   `json:"strategy,omitempty"`
	Enabled   bool     `json:"enabled,omitempty"`
	Targets   []Target `json:"targets"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type Target struct {
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

type routeConfig struct {
	Models []Route `json:"models"`
}

func LoadRoutesFromEnv() ([]Route, error) {
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
		return []Route{{
			Alias:   alias,
			Enabled: true,
			Targets: []Target{{
				Provider:       "openai",
				BaseURL:        DefaultURL,
				APIKeyEnv:      "OPENAI_API_KEY",
				Model:          defaultModel,
				UpstreamAPI:    "responses",
				TimeoutSeconds: 300,
				Weight:         1,
				Enabled:        true,
			}},
		}}, nil
	}

	var cfg routeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err == nil && len(cfg.Models) > 0 {
		return NormalizeRoutes(cfg.Models)
	}
	var routes []Route
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, fmt.Errorf("parse AI_GATEWAY_ROUTES: %w", err)
	}
	return NormalizeRoutes(routes)
}

func NormalizeRoutes(routes []Route) ([]Route, error) {
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
				t.BaseURL = DefaultURL
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

func FindRoute(routes []Route, model string) (Route, bool) {
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
	return Route{}, false
}

func (rt *Runtime) LoadRoutes(ctx context.Context, workspaceID string) ([]Route, error) {
	if rt.DB != nil && workspaceID != "" {
		routes, err := rt.LoadRoutesFromDB(ctx, workspaceID, false)
		if err != nil {
			if !isRouteTableMissing(err) {
				return nil, err
			}
			return LoadRoutesFromEnv()
		}
		if len(routes) > 0 {
			return routes, nil
		}
	}
	return LoadRoutesFromEnv()
}

func isRouteTableMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), `relation "ai_gateway_route" does not exist`)
}

func (rt *Runtime) LoadRoutesFromDB(ctx context.Context, workspaceID string, includeDisabled bool) ([]Route, error) {
	if rt.DB == nil {
		return nil, nil
	}
	enabledClause := ""
	if !includeDisabled {
		enabledClause = "AND r.enabled = true AND t.enabled = true"
	}
	rows, err := rt.DB.Query(ctx, `
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
	`, util.MustParseUUID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]int{}
	routes := []Route{}
	for rows.Next() {
		var routeID, targetID pgtype.UUID
		var createdAt, updatedAt pgtype.Timestamptz
		var route Route
		var target Target
		if err := rows.Scan(
			&routeID, &route.Alias, &route.Strategy, &route.Enabled, &createdAt, &updatedAt,
			&targetID, &target.Provider, &target.BaseURL, &target.APIKeyEnv, &target.Model, &target.UpstreamAPI,
			&target.ReasoningEffort, &target.OrganizationEnv, &target.ProjectEnv, &target.TimeoutSeconds, &target.Weight, &target.Priority, &target.Enabled,
			&target.InputPricePerMillionMicros, &target.OutputPricePerMillionMicros,
		); err != nil {
			return nil, err
		}
		route.ID = util.UUIDToString(routeID)
		route.CreatedAt = util.TimestampToString(createdAt)
		route.UpdatedAt = util.TimestampToString(updatedAt)
		target.ID = util.UUIDToString(targetID)
		if idx, ok := byID[route.ID]; ok {
			routes[idx].Targets = append(routes[idx].Targets, target)
		} else {
			route.Targets = []Target{target}
			byID[route.ID] = len(routes)
			routes = append(routes, route)
		}
	}
	return routes, rows.Err()
}

func (rt *Runtime) Models(w http.ResponseWriter, r *http.Request) {
	key, ok := rt.requireKey(w, r)
	if !ok {
		return
	}
	routes, err := rt.LoadRoutes(r.Context(), key.WorkspaceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
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
				data = append(data, modelListItem(model))
			}
			if routeHasCodexModelTemplate(route) {
				for _, model := range codexModels() {
					if seen[model] {
						continue
					}
					seen[model] = true
					data = append(data, modelListItem(model))
				}
			}
			continue
		}
		if seen[route.Alias] {
			continue
		}
		seen[route.Alias] = true
		data = append(data, modelListItem(route.Alias))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func modelListItem(model string) map[string]any {
	return map[string]any{
		"id":       model,
		"object":   "model",
		"created":  0,
		"owned_by": "multica",
	}
}

func codexModels() []string {
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

func isCodexModel(model string) bool {
	for _, id := range codexModels() {
		if id == model {
			return true
		}
	}
	return false
}

func routeHasCodexModelTemplate(route Route) bool {
	for _, target := range route.Targets {
		if target.Enabled || target.ID == "" {
			if targetCanProxyCodexModel(target) {
				return true
			}
		}
	}
	return false
}

func targetCanProxyCodexModel(target Target) bool {
	upstreamAPI := strings.TrimSpace(target.UpstreamAPI)
	if upstreamAPI != "" && upstreamAPI != "responses" {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(target.Provider))
	baseURL := strings.ToLower(strings.TrimSpace(target.BaseURL))
	return strings.Contains(provider, "openai") || strings.Contains(baseURL, "api.openai.com")
}

func (rt *Runtime) Responses(w http.ResponseWriter, r *http.Request) {
	rt.proxy(w, r, "/responses")
}

func (rt *Runtime) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	rt.proxy(w, r, "/chat/completions")
}

func (rt *Runtime) requireKey(w http.ResponseWriter, r *http.Request) (VirtualKey, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		WriteError(w, http.StatusUnauthorized, "missing authorization")
		return VirtualKey{}, false
	}
	key, ok, err := rt.ResolveVirtualKey(r.Context(), token)
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "AI gateway auth unavailable")
		return VirtualKey{}, false
	}
	if !ok {
		WriteError(w, http.StatusUnauthorized, "invalid token")
		return VirtualKey{}, false
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

func (rt *Runtime) proxy(w http.ResponseWriter, r *http.Request, endpoint string) {
	key, ok := rt.requireKey(w, r)
	if !ok {
		return
	}
	if r.Body == nil {
		WriteError(w, http.StatusBadRequest, "missing request body")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<20))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, "request body too large (max 256MB)")
			return
		}
		WriteError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		WriteError(w, http.StatusBadRequest, "model is required")
		return
	}

	routes, err := rt.LoadRoutes(r.Context(), key.WorkspaceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	route, found := FindRoute(routes, model)
	if !found {
		WriteError(w, http.StatusNotFound, "model is not configured")
		return
	}

	stream, _ := payload["stream"].(bool)
	requestID := uuid.NewString()
	callerID := key.CallerID()
	var lastErr string
	var lastStatus int
	targets := selectTargets(route, model)
	if len(targets) == 0 {
		WriteError(w, http.StatusNotFound, "model is not configured")
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
		upstreamPayload := rt.preparePayloadForUpstream(r.Context(), key, endpoint, payload)
		previousResponseID := ""
		if endpoint == "/responses" {
			previousResponseID = strings.TrimSpace(stringValue(upstreamPayload["previous_response_id"]))
		}
		upstreamEndpoint, upstreamBody, err := BuildUpstreamRequest(endpoint, upstreamPayload, target, targetModel)
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		reasoningEffort := extractAIGatewayReasoningEffort(upstreamBody, upstreamEndpoint)
		forwardTarget := target
		if reasoningEffort != "" {
			forwardTarget.ReasoningEffort = reasoningEffort
		}
		status, retry, errText := rt.forward(w, r, ForwardRequest{
			Key:                key,
			RequestID:          requestID,
			CallerID:           callerID,
			Endpoint:           endpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			ModelAlias:         model,
			Target:             forwardTarget,
			TargetModel:        targetModel,
			PreviousResponseID: previousResponseID,
			APIKey:             apiKey,
			Body:               upstreamBody,
			Stream:             stream,
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
	WriteError(w, finalStatus, lastErr)
}

func PatchedBody(payload map[string]any, model string, endpoint string, target Target) ([]byte, error) {
	copyPayload := make(map[string]any, len(payload))
	for k, v := range payload {
		copyPayload[k] = v
	}
	copyPayload["model"] = model
	applyAIGatewayReasoningEffort(copyPayload, endpoint, target.ReasoningEffort)
	return json.Marshal(copyPayload)
}

func (rt *Runtime) preparePayloadForUpstream(ctx context.Context, key VirtualKey, endpoint string, payload map[string]any) map[string]any {
	copyPayload := make(map[string]any, len(payload))
	for k, v := range payload {
		copyPayload[k] = v
	}
	if endpoint == "/responses" {
		rt.scopeResponsesState(ctx, key, copyPayload)
	}
	return copyPayload
}

func (rt *Runtime) scopeResponsesState(ctx context.Context, key VirtualKey, payload map[string]any) {
	if payload == nil {
		return
	}
	delete(payload, "conversation")
	previousID := strings.TrimSpace(stringValue(payload["previous_response_id"]))
	if previousID == "" {
		return
	}
	if !rt.responseBelongsToKey(ctx, key, previousID) {
		delete(payload, "previous_response_id")
	}
}

func (rt *Runtime) responseBelongsToKey(ctx context.Context, key VirtualKey, responseID string) bool {
	if rt.DB == nil || key.ID == "" || responseID == "" {
		return false
	}
	var owner pgtype.UUID
	err := rt.DB.QueryRow(ctx, `
		SELECT virtual_key_id
		FROM ai_gateway_response_state
		WHERE response_id = $1
	`, responseID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		return false
	}
	if util.UUIDToString(owner) != key.ID {
		return false
	}
	_, _ = rt.DB.Exec(ctx, `
		UPDATE ai_gateway_response_state
		SET last_used_at = now()
		WHERE response_id = $1
	`, responseID)
	return true
}

func selectTargets(route Route, requestedModel string) []Target {
	targets := make([]Target, 0, len(route.Targets))
	codexTemplateTargets := make([]Target, 0, len(route.Targets))
	for _, target := range route.Targets {
		if target.Enabled || target.ID == "" {
			if route.Alias == "*" && target.Model != "" && target.Model != requestedModel {
				if isCodexModel(requestedModel) && targetCanProxyCodexModel(target) {
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

func BuildUpstreamRequest(endpoint string, payload map[string]any, target Target, targetModel string) (string, []byte, error) {
	if target.UpstreamAPI == "chat_completions" && endpoint == "/responses" {
		body, err := ResponsesPayloadToChatCompletions(payload, targetModel, target)
		return "/chat/completions", body, err
	}
	body, err := PatchedBody(payload, targetModel, endpoint, target)
	return endpoint, body, err
}

func ResponsesPayloadToChatCompletions(payload map[string]any, model string, target Target) ([]byte, error) {
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
		if effort, ok := payload["reasoning_effort"].(string); ok && IsReasoningEffort(effort) {
			return effort
		}
	}
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok && IsReasoningEffort(effort) {
			return effort
		}
	}
	if effort, ok := payload["reasoning_effort"].(string); ok && IsReasoningEffort(effort) {
		return effort
	}
	return ""
}

func IsReasoningEffort(effort string) bool {
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

type ForwardRequest struct {
	Key                VirtualKey
	RequestID          string
	CallerID           string
	Endpoint           string
	UpstreamEndpoint   string
	ModelAlias         string
	Target             Target
	TargetModel        string
	PreviousResponseID string
	ResponseSessionID  string
	APIKey             string
	Body               []byte
	Stream             bool
}

func (rt *Runtime) forward(w http.ResponseWriter, r *http.Request, req ForwardRequest) (status int, retry bool, errorText string) {
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
		rt.RecordUsage(context.Background(), UsageRecord{
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
		rt.RecordUsage(context.Background(), UsageRecord{
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
		rt.RecordUsage(context.Background(), UsageRecord{
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
			n, usage, responseID, copyErr := copyChatCompletionStreamAsResponses(w, resp.Body, req)
			errText := ""
			if copyErr != nil {
				errText = copyErr.Error()
			}
			responseSessionID := ""
			if resp.StatusCode < 400 {
				responseSessionID = rt.RecordResponseState(context.Background(), req.Key, responseID, req.PreviousResponseID, req.Target, req.TargetModel)
			}
			totalLatency := time.Since(start)
			rt.RecordUsage(context.Background(), UsageRecord{
				Key:                req.Key,
				RequestID:          req.RequestID,
				CallerID:           req.CallerID,
				Endpoint:           req.Endpoint,
				ResponseID:         responseID,
				PreviousResponseID: req.PreviousResponseID,
				ResponseSessionID:  responseSessionID,
				ModelAlias:         req.ModelAlias,
				Target:             req.Target,
				TargetModel:        req.TargetModel,
				StatusCode:         resp.StatusCode,
				PromptTokens:       usage.PromptTokens,
				CompletionTokens:   usage.CompletionTokens,
				TotalTokens:        usage.TotalTokens,
				CachedInputTokens:  usage.CachedInputTokens,
				ReasoningTokens:    usage.ReasoningTokens,
				LatencyMs:          totalLatency.Milliseconds(),
				Error:              errText,
				Bytes:              n,
			})
			return resp.StatusCode, false, errText
		}
		w.WriteHeader(resp.StatusCode)
		n, usage, responseID, copyErr := CopyStream(w, resp.Body)
		errText := ""
		if copyErr != nil {
			errText = copyErr.Error()
		}
		if resp.StatusCode < 400 && req.Target.UpstreamAPI == "responses" && req.Endpoint == "/responses" {
			responseSessionID := rt.RecordResponseState(context.Background(), req.Key, responseID, req.PreviousResponseID, req.Target, req.TargetModel)
			req.ResponseSessionID = responseSessionID
		}
		totalLatency := time.Since(start)
		rt.RecordUsage(context.Background(), UsageRecord{
			Key:                req.Key,
			RequestID:          req.RequestID,
			CallerID:           req.CallerID,
			Endpoint:           req.Endpoint,
			ResponseID:         responseID,
			PreviousResponseID: req.PreviousResponseID,
			ResponseSessionID:  req.ResponseSessionID,
			ModelAlias:         req.ModelAlias,
			Target:             req.Target,
			TargetModel:        req.TargetModel,
			StatusCode:         resp.StatusCode,
			LatencyMs:          totalLatency.Milliseconds(),
			Error:              errText,
			PromptTokens:       usage.PromptTokens,
			CompletionTokens:   usage.CompletionTokens,
			TotalTokens:        usage.TotalTokens,
			CachedInputTokens:  usage.CachedInputTokens,
			ReasoningTokens:    usage.ReasoningTokens,
			Bytes:              n,
		})
		return resp.StatusCode, false, errText
	}

	data, readErr := io.ReadAll(resp.Body)
	errText := ""
	if readErr != nil {
		errText = readErr.Error()
	}
	usage := ParseUsage(data)
	if resp.StatusCode >= 400 && errText == "" {
		errText = strings.TrimSpace(string(data))
	}
	if resp.StatusCode < 400 && req.Target.UpstreamAPI == "chat_completions" && req.Endpoint == "/responses" {
		converted, err := ChatCompletionToResponses(data, req)
		if err != nil {
			errText = err.Error()
			resp.StatusCode = http.StatusBadGateway
			data = []byte(`{"error":{"message":"failed to convert chat completion response","type":"multica_ai_gateway_error"}}`)
		} else {
			data = converted
			w.Header().Set("Content-Type", "application/json")
		}
	}
	responseID := ""
	responseSessionID := ""
	if resp.StatusCode < 400 && req.Endpoint == "/responses" {
		responseID = extractAIGatewayResponseID(data)
		responseSessionID = rt.RecordResponseState(context.Background(), req.Key, responseID, req.PreviousResponseID, req.Target, req.TargetModel)
	}
	rt.RecordUsage(context.Background(), UsageRecord{
		Key:                req.Key,
		RequestID:          req.RequestID,
		CallerID:           req.CallerID,
		Endpoint:           req.Endpoint,
		ResponseID:         responseID,
		PreviousResponseID: req.PreviousResponseID,
		ResponseSessionID:  responseSessionID,
		ModelAlias:         req.ModelAlias,
		Target:             req.Target,
		TargetModel:        req.TargetModel,
		StatusCode:         resp.StatusCode,
		LatencyMs:          time.Since(start).Milliseconds(),
		Error:              errText,
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		CachedInputTokens:  usage.CachedInputTokens,
		ReasoningTokens:    usage.ReasoningTokens,
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

func CopyStream(w http.ResponseWriter, body io.Reader) (int64, UsageTokens, string, error) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var written int64
	var usageParser sseUsageParser
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
				return written, usageParser.Usage(), usageParser.ResponseID(), writeErr
			}
		}
		if readErr != nil {
			usageParser.Finish()
			if errors.Is(readErr, io.EOF) {
				return written, usageParser.Usage(), usageParser.ResponseID(), nil
			}
			if errors.Is(readErr, context.Canceled) && usageParser.Completed() {
				return written, usageParser.Usage(), usageParser.ResponseID(), nil
			}
			return written, usageParser.Usage(), usageParser.ResponseID(), readErr
		}
	}
}

func ChatCompletionToResponses(data []byte, req ForwardRequest) ([]byte, error) {
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

func copyChatCompletionStreamAsResponses(w http.ResponseWriter, body io.Reader, req ForwardRequest) (int64, UsageTokens, string, error) {
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
		return written, UsageTokens{}, responseID, err
	}
	if err := writeEvent("response.output_item.added", map[string]any{"response_id": responseID, "output_index": 0, "item": map[string]any{"id": itemID, "type": "message", "role": "assistant", "content": []any{}}}); err != nil {
		return written, UsageTokens{}, responseID, err
	}
	if err := writeEvent("response.content_part.added", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}}); err != nil {
		return written, UsageTokens{}, responseID, err
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	var usage UsageTokens
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
		if chunkUsage := ParseUsage([]byte(raw)); chunkUsage.hasAny() {
			usage.merge(chunkUsage)
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
			return written, usage, responseID, err
		}
	}
	if err := scanner.Err(); err != nil {
		return written, usage, responseID, err
	}
	text := full.String()
	if err := writeEvent("response.output_text.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "text": text}); err != nil {
		return written, usage, responseID, err
	}
	if err := writeEvent("response.content_part.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text}}); err != nil {
		return written, usage, responseID, err
	}
	if err := writeEvent("response.output_item.done", map[string]any{"response_id": responseID, "output_index": 0, "item": map[string]any{"id": itemID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}}); err != nil {
		return written, usage, responseID, err
	}
	completed := minimalStreamingResponse(responseID, req.TargetModel, "completed", text)
	if usage.TotalTokens > 0 {
		completed["usage"] = usage.responsesUsage()
	}
	if err := writeEvent("response.completed", map[string]any{"response": completed}); err != nil {
		return written, usage, responseID, err
	}
	n, err := fmt.Fprint(w, "data: [DONE]\n\n")
	written += int64(n)
	if flusher != nil {
		flusher.Flush()
	}
	return written, usage, responseID, err
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

func SanitizeCallerID(value string) string {
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

type UsageTokens struct {
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	CachedInputTokens int64
	ReasoningTokens   int64
}

func (u UsageTokens) responsesUsage() map[string]any {
	usage := map[string]any{
		"input_tokens":  u.PromptTokens,
		"output_tokens": u.CompletionTokens,
		"total_tokens":  u.TotalTokens,
	}
	if u.CachedInputTokens > 0 {
		usage["input_tokens_details"] = map[string]int64{
			"cached_tokens": u.CachedInputTokens,
		}
	}
	if u.ReasoningTokens > 0 {
		usage["output_tokens_details"] = map[string]int64{
			"reasoning_tokens": u.ReasoningTokens,
		}
	}
	return usage
}

func (u UsageTokens) hasAny() bool {
	return u.PromptTokens > 0 ||
		u.CompletionTokens > 0 ||
		u.TotalTokens > 0 ||
		u.CachedInputTokens > 0 ||
		u.ReasoningTokens > 0
}

func (u *UsageTokens) merge(next UsageTokens) {
	if next.PromptTokens > 0 {
		u.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens > 0 {
		u.CompletionTokens = next.CompletionTokens
	}
	if next.TotalTokens > 0 {
		u.TotalTokens = next.TotalTokens
	}
	if next.CachedInputTokens > 0 {
		u.CachedInputTokens = next.CachedInputTokens
	}
	if next.ReasoningTokens > 0 {
		u.ReasoningTokens = next.ReasoningTokens
	}
	if u.TotalTokens == 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
}

type usageJSON struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheReadInputTokens  int64 `json:"cache_read_input_tokens"`
	PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
	ReasoningTokens       int64 `json:"reasoning_tokens"`
}

func ParseUsage(data []byte) UsageTokens {
	var envelope struct {
		Usage    usageJSON `json:"usage"`
		Response struct {
			Usage usageJSON `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return UsageTokens{}
	}
	usage := envelope.Usage
	if usage.TotalTokens == 0 &&
		usage.PromptTokens == 0 &&
		usage.CompletionTokens == 0 &&
		usage.InputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.CachedInputTokens == 0 &&
		usage.CacheReadInputTokens == 0 &&
		usage.PromptCacheHitTokens == 0 &&
		usage.PromptDetails.CachedTokens == 0 &&
		usage.InputDetails.CachedTokens == 0 &&
		usage.ReasoningTokens == 0 &&
		usage.OutputDetails.ReasoningTokens == 0 {
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
	cachedInput := firstPositive(
		usage.CachedInputTokens,
		usage.CacheReadInputTokens,
		usage.InputDetails.CachedTokens,
		usage.PromptDetails.CachedTokens,
		usage.PromptCacheHitTokens,
	)
	if prompt == 0 && usage.PromptCacheHitTokens > 0 && usage.PromptCacheMissTokens > 0 {
		prompt = usage.PromptCacheHitTokens + usage.PromptCacheMissTokens
		if total == completion {
			total = prompt + completion
		}
	}
	reasoning := firstPositive(usage.ReasoningTokens, usage.OutputDetails.ReasoningTokens)
	return UsageTokens{
		PromptTokens:      prompt,
		CompletionTokens:  completion,
		TotalTokens:       total,
		CachedInputTokens: cachedInput,
		ReasoningTokens:   reasoning,
	}
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

type sseUsageParser struct {
	buffer     string
	usage      UsageTokens
	completed  bool
	responseID string
}

func (p *sseUsageParser) Feed(chunk []byte) {
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

func (p *sseUsageParser) Finish() {
	if strings.TrimSpace(p.buffer) == "" {
		p.buffer = ""
		return
	}
	p.parseFrame(p.buffer)
	p.buffer = ""
}

func (p *sseUsageParser) Usage() UsageTokens {
	return p.usage
}

func (p *sseUsageParser) Completed() bool {
	return p.completed
}

func (p *sseUsageParser) ResponseID() string {
	return p.responseID
}

func (p *sseUsageParser) parseFrame(frame string) {
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
	if p.responseID == "" {
		p.responseID = extractAIGatewayResponseID([]byte(raw))
	}
	if usage := ParseUsage([]byte(raw)); usage.hasAny() {
		p.usage.merge(usage)
	}
}

func extractAIGatewayResponseID(data []byte) string {
	var envelope struct {
		ID       string `json:"id"`
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if len(data) == 0 || json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	if id := strings.TrimSpace(envelope.ID); id != "" {
		return id
	}
	return strings.TrimSpace(envelope.Response.ID)
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

type UsageRecord struct {
	Key                VirtualKey
	RequestID          string
	CallerID           string
	Endpoint           string
	ResponseID         string
	PreviousResponseID string
	ResponseSessionID  string
	ModelAlias         string
	Target             Target
	TargetModel        string
	StatusCode         int
	PromptTokens       int64
	CompletionTokens   int64
	TotalTokens        int64
	CachedInputTokens  int64
	ReasoningTokens    int64
	InputCostMicros    int64
	OutputCostMicros   int64
	TotalCostMicros    int64
	LatencyMs          int64
	Error              string
	Bytes              int64
}

type defaultUsagePricing struct {
	InputPricePerMillionMicros       int64
	CachedInputPricePerMillionMicros int64
	OutputPricePerMillionMicros      int64
	LongInputPricePerMillionMicros   int64
	LongOutputPricePerMillionMicros  int64
}

var defaultModelPricing = map[string]defaultUsagePricing{
	"claude-haiku-4-5":  {InputPricePerMillionMicros: 1_000_000, OutputPricePerMillionMicros: 5_000_000},
	"claude-sonnet-4-5": {InputPricePerMillionMicros: 3_000_000, OutputPricePerMillionMicros: 15_000_000},
	"claude-sonnet-4-6": {InputPricePerMillionMicros: 3_000_000, OutputPricePerMillionMicros: 15_000_000},
	"claude-opus-4-5":   {InputPricePerMillionMicros: 5_000_000, OutputPricePerMillionMicros: 25_000_000},
	"claude-opus-4-6":   {InputPricePerMillionMicros: 5_000_000, OutputPricePerMillionMicros: 25_000_000},
	"claude-opus-4-7":   {InputPricePerMillionMicros: 5_000_000, OutputPricePerMillionMicros: 25_000_000},
	"claude-opus-4-1":   {InputPricePerMillionMicros: 15_000_000, OutputPricePerMillionMicros: 75_000_000},
	"claude-opus-4":     {InputPricePerMillionMicros: 15_000_000, OutputPricePerMillionMicros: 75_000_000},
	"claude-sonnet-4":   {InputPricePerMillionMicros: 3_000_000, OutputPricePerMillionMicros: 15_000_000},
	"claude-haiku-3-5":  {InputPricePerMillionMicros: 800_000, OutputPricePerMillionMicros: 4_000_000},

	"gpt-5.5":             {InputPricePerMillionMicros: 5_000_000, CachedInputPricePerMillionMicros: 500_000, OutputPricePerMillionMicros: 30_000_000, LongInputPricePerMillionMicros: 10_000_000, LongOutputPricePerMillionMicros: 45_000_000},
	"gpt-5.5-pro":         {InputPricePerMillionMicros: 30_000_000, OutputPricePerMillionMicros: 180_000_000},
	"gpt-5.4-mini":        {InputPricePerMillionMicros: 750_000, OutputPricePerMillionMicros: 4_500_000},
	"gpt-5.4-nano":        {InputPricePerMillionMicros: 200_000, OutputPricePerMillionMicros: 1_250_000},
	"gpt-5.4":             {InputPricePerMillionMicros: 2_500_000, OutputPricePerMillionMicros: 15_000_000},
	"gpt-5.4-pro":         {InputPricePerMillionMicros: 30_000_000, OutputPricePerMillionMicros: 180_000_000},
	"gpt-5.3-chat-latest": {InputPricePerMillionMicros: 1_750_000, OutputPricePerMillionMicros: 14_000_000},
	"gpt-5.3-codex":       {InputPricePerMillionMicros: 1_750_000, OutputPricePerMillionMicros: 14_000_000},
	"gpt-5.2-codex":       {InputPricePerMillionMicros: 1_750_000, OutputPricePerMillionMicros: 14_000_000},
	"gpt-5.1-codex-max":   {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5.1-codex":       {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5.2-chat-latest": {InputPricePerMillionMicros: 1_750_000, OutputPricePerMillionMicros: 14_000_000},
	"gpt-5.2":             {InputPricePerMillionMicros: 1_750_000, OutputPricePerMillionMicros: 14_000_000},
	"gpt-5.2-pro":         {InputPricePerMillionMicros: 21_000_000, OutputPricePerMillionMicros: 168_000_000},
	"gpt-5.1-chat-latest": {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5.1":             {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5-chat-latest":   {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5-codex":         {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-5-mini":          {InputPricePerMillionMicros: 250_000, OutputPricePerMillionMicros: 2_000_000},
	"gpt-5-nano":          {InputPricePerMillionMicros: 50_000, OutputPricePerMillionMicros: 400_000},
	"gpt-5-pro":           {InputPricePerMillionMicros: 15_000_000, OutputPricePerMillionMicros: 120_000_000},
	"gpt-5":               {InputPricePerMillionMicros: 1_250_000, OutputPricePerMillionMicros: 10_000_000},
	"gpt-4.1":             {InputPricePerMillionMicros: 2_000_000, OutputPricePerMillionMicros: 8_000_000},
	"gpt-4.1-mini":        {InputPricePerMillionMicros: 400_000, OutputPricePerMillionMicros: 1_600_000},
	"gpt-4.1-nano":        {InputPricePerMillionMicros: 100_000, OutputPricePerMillionMicros: 400_000},
	"o3-mini":             {InputPricePerMillionMicros: 1_100_000, OutputPricePerMillionMicros: 4_400_000},
	"o3":                  {InputPricePerMillionMicros: 2_000_000, OutputPricePerMillionMicros: 8_000_000},
	"o4-mini":             {InputPricePerMillionMicros: 1_100_000, OutputPricePerMillionMicros: 4_400_000},
	"gpt-4o-mini":         {InputPricePerMillionMicros: 150_000, OutputPricePerMillionMicros: 600_000},
	"gpt-4o":              {InputPricePerMillionMicros: 2_500_000, OutputPricePerMillionMicros: 10_000_000},
}

func effectiveUsagePricing(target Target, targetModel string) defaultUsagePricing {
	pricing, _ := resolveDefaultModelPricing(targetModel, target.Model)
	if target.InputPricePerMillionMicros > 0 {
		pricing.InputPricePerMillionMicros = target.InputPricePerMillionMicros
		pricing.CachedInputPricePerMillionMicros = 0
		pricing.LongInputPricePerMillionMicros = 0
	}
	if target.OutputPricePerMillionMicros > 0 {
		pricing.OutputPricePerMillionMicros = target.OutputPricePerMillionMicros
		pricing.LongOutputPricePerMillionMicros = 0
	}
	return pricing
}

func EstimateUsageCostMicros(model string, promptTokens int64, completionTokens int64) int64 {
	return EstimateUsageCostBreakdown(model, promptTokens, completionTokens, 0).TotalCostMicros
}

type UsageCostBreakdown struct {
	BillableInputTokens    int64
	CachedInputTokens      int64
	InputCostMicros        int64
	CachedInputCostMicros  int64
	OutputCostMicros       int64
	TotalCostMicros        int64
	LongContext            bool
	InputPriceMicros       int64
	CachedInputPriceMicros int64
	OutputPriceMicros      int64
}

const gpt55LongContextInputThreshold = int64(272_000)

func EstimateUsageCostBreakdown(model string, promptTokens int64, completionTokens int64, cachedInputTokens int64) UsageCostBreakdown {
	pricing, ok := resolveDefaultModelPricing(model)
	if !ok {
		return UsageCostBreakdown{}
	}
	return estimateUsageCostBreakdownWithPricing(pricing, promptTokens, completionTokens, cachedInputTokens)
}

func estimateUsageCostBreakdownWithPricing(pricing defaultUsagePricing, promptTokens int64, completionTokens int64, cachedInputTokens int64) UsageCostBreakdown {
	return estimateUsageCostBreakdownWithPricingAndLong(pricing, promptTokens, completionTokens, cachedInputTokens, false)
}

func estimateUsageCostBreakdownWithPricingAndLong(pricing defaultUsagePricing, promptTokens int64, completionTokens int64, cachedInputTokens int64, forceLongContext bool) UsageCostBreakdown {
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	if cachedInputTokens > promptTokens {
		cachedInputTokens = promptTokens
	}
	billableInputTokens := promptTokens - cachedInputTokens
	inputPrice := pricing.InputPricePerMillionMicros
	outputPrice := pricing.OutputPricePerMillionMicros
	cachedInputPrice := pricing.CachedInputPricePerMillionMicros
	longContext := false
	if (forceLongContext || promptTokens > gpt55LongContextInputThreshold) && pricing.LongInputPricePerMillionMicros > 0 && pricing.LongOutputPricePerMillionMicros > 0 {
		longContext = true
		inputPrice = pricing.LongInputPricePerMillionMicros
		outputPrice = pricing.LongOutputPricePerMillionMicros
		cachedInputPrice = 0
		billableInputTokens = promptTokens
		cachedInputTokens = 0
	}
	inputCost := billableInputTokens * inputPrice / 1_000_000
	cachedInputCost := cachedInputTokens * cachedInputPrice / 1_000_000
	outputCost := completionTokens * outputPrice / 1_000_000
	return UsageCostBreakdown{
		BillableInputTokens:    billableInputTokens,
		CachedInputTokens:      cachedInputTokens,
		InputCostMicros:        inputCost,
		CachedInputCostMicros:  cachedInputCost,
		OutputCostMicros:       outputCost,
		TotalCostMicros:        inputCost + cachedInputCost + outputCost,
		LongContext:            longContext,
		InputPriceMicros:       inputPrice,
		CachedInputPriceMicros: cachedInputPrice,
		OutputPriceMicros:      outputPrice,
	}
}

func resolveDefaultModelPricing(models ...string) (defaultUsagePricing, bool) {
	for _, model := range models {
		for _, candidate := range modelPricingCandidates(model) {
			if pricing, ok := defaultModelPricing[candidate]; ok {
				return pricing, true
			}
		}
	}
	return defaultUsagePricing{}, false
}

func modelPricingCandidates(model string) []string {
	seen := map[string]struct{}{}
	var out []string
	push := func(s string) {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	stripProvider := func(s string) string {
		i := strings.Index(s, "/")
		if i <= 0 || !isModelProviderSegment(s[:i]) {
			return s
		}
		return s[i+1:]
	}
	canonAnthropic := func(s string) string {
		if strings.HasPrefix(s, "claude-") {
			return strings.ReplaceAll(s, ".", "-")
		}
		return s
	}

	raw := strings.TrimSpace(strings.ToLower(model))
	noProvider := stripProvider(raw)
	dashed := canonAnthropic(noProvider)
	push(raw)
	push(noProvider)
	push(dashed)
	push(stripModelDateSuffix(raw))
	push(stripModelDateSuffix(noProvider))
	push(stripModelDateSuffix(dashed))
	return out
}

func isModelProviderSegment(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if i > 0 && ((r >= '0' && r <= '9') || r == '_' || r == '-') {
			continue
		}
		return false
	}
	return true
}

func stripModelDateSuffix(s string) string {
	if strings.HasSuffix(s, "-latest") {
		return strings.TrimSuffix(s, "-latest")
	}
	if len(s) >= len("-20060102") {
		suffix := s[len(s)-len("-20060102"):]
		if suffix[0] == '-' && allDigits(suffix[1:]) {
			return s[:len(s)-len(suffix)]
		}
	}
	if len(s) >= len("-2006-01-02") {
		suffix := s[len(s)-len("-2006-01-02"):]
		if suffix[0] == '-' && suffix[5] == '-' && suffix[8] == '-' && allDigits(suffix[1:5]) && allDigits(suffix[6:8]) && allDigits(suffix[9:]) {
			return s[:len(s)-len(suffix)]
		}
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (rt *Runtime) RecordResponseState(ctx context.Context, key VirtualKey, responseID string, previousResponseID string, target Target, targetModel string) string {
	responseID = strings.TrimSpace(responseID)
	previousResponseID = strings.TrimSpace(previousResponseID)
	if rt.DB == nil || key.ID == "" || key.WorkspaceID == "" || responseID == "" {
		return ""
	}
	sessionID := responseID
	if previousResponseID != "" {
		if previousSessionID := rt.responseSessionID(ctx, key, previousResponseID); previousSessionID != "" {
			sessionID = previousSessionID
		}
	}
	_, err := rt.DB.Exec(ctx, `
		INSERT INTO ai_gateway_response_state (
			response_id, virtual_key_id, workspace_id, upstream_provider, upstream_model, session_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (response_id) DO UPDATE
		SET workspace_id = EXCLUDED.workspace_id,
		    upstream_provider = EXCLUDED.upstream_provider,
		    upstream_model = EXCLUDED.upstream_model,
		    session_id = EXCLUDED.session_id,
		    last_used_at = now()
		WHERE ai_gateway_response_state.virtual_key_id = EXCLUDED.virtual_key_id
	`,
		responseID,
		util.MustParseUUID(key.ID),
		util.MustParseUUID(key.WorkspaceID),
		target.Provider,
		targetModel,
		sessionID,
	)
	if err != nil && strings.Contains(err.Error(), "session_id") {
		_, _ = rt.DB.Exec(ctx, `
			INSERT INTO ai_gateway_response_state (
				response_id, virtual_key_id, workspace_id, upstream_provider, upstream_model
			)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (response_id) DO UPDATE
			SET workspace_id = EXCLUDED.workspace_id,
			    upstream_provider = EXCLUDED.upstream_provider,
			    upstream_model = EXCLUDED.upstream_model,
			    last_used_at = now()
			WHERE ai_gateway_response_state.virtual_key_id = EXCLUDED.virtual_key_id
		`,
			responseID,
			util.MustParseUUID(key.ID),
			util.MustParseUUID(key.WorkspaceID),
			target.Provider,
			targetModel,
		)
	}
	return sessionID
}

func (rt *Runtime) responseSessionID(ctx context.Context, key VirtualKey, responseID string) string {
	if rt.DB == nil || key.ID == "" || responseID == "" {
		return ""
	}
	var sessionID string
	err := rt.DB.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(session_id, ''), response_id)
		FROM ai_gateway_response_state
		WHERE response_id = $1
		  AND virtual_key_id = $2
	`, responseID, util.MustParseUUID(key.ID)).Scan(&sessionID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sessionID)
}

func (rt *Runtime) responseSessionHasLongContext(ctx context.Context, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if rt.DB == nil || sessionID == "" {
		return false
	}
	var exists bool
	err := rt.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM ai_gateway_usage
			WHERE response_session_id = $1
			  AND long_context
		)
	`, sessionID).Scan(&exists)
	return err == nil && exists
}

func (rt *Runtime) repriceLongContextSession(ctx context.Context, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if rt.DB == nil || sessionID == "" {
		return
	}
	rows, err := rt.DB.Query(ctx, `
		SELECT id, COALESCE(upstream_model, ''), prompt_tokens, completion_tokens, cached_input_tokens
		FROM ai_gateway_usage
		WHERE response_session_id = $1
	`, sessionID)
	if err != nil {
		return
	}
	defer rows.Close()

	type update struct {
		id        pgtype.UUID
		breakdown UsageCostBreakdown
	}
	updates := []update{}
	for rows.Next() {
		var id pgtype.UUID
		var model string
		var promptTokens, completionTokens, cachedInputTokens int64
		if err := rows.Scan(&id, &model, &promptTokens, &completionTokens, &cachedInputTokens); err != nil {
			return
		}
		pricing, ok := resolveDefaultModelPricing(model)
		if !ok || pricing.LongInputPricePerMillionMicros == 0 || pricing.LongOutputPricePerMillionMicros == 0 {
			continue
		}
		breakdown := estimateUsageCostBreakdownWithPricingAndLong(pricing, promptTokens, completionTokens, cachedInputTokens, true)
		updates = append(updates, update{id: id, breakdown: breakdown})
	}
	if rows.Err() != nil {
		return
	}
	for _, item := range updates {
		_, _ = rt.DB.Exec(ctx, `
			UPDATE ai_gateway_usage
			SET input_cost_micros = $2,
			    cached_input_cost_micros = $3,
			    output_cost_micros = $4,
			    total_cost_micros = $5,
			    cached_input_tokens = $6,
			    billable_input_tokens = $7,
			    long_context = true
			WHERE id = $1
		`,
			item.id,
			item.breakdown.InputCostMicros,
			item.breakdown.CachedInputCostMicros,
			item.breakdown.OutputCostMicros,
			item.breakdown.TotalCostMicros,
			item.breakdown.CachedInputTokens,
			item.breakdown.BillableInputTokens,
		)
	}
}

func (rt *Runtime) RecordUsage(ctx context.Context, record UsageRecord) {
	if rt.DB == nil || record.Key.ID == "" || record.Key.WorkspaceID == "" {
		return
	}
	errText := record.Error
	if len(errText) > 2048 {
		errText = errText[:2048]
	}
	pricing := effectiveUsagePricing(record.Target, record.TargetModel)
	forceLongContext := rt.responseSessionHasLongContext(ctx, record.ResponseSessionID)
	breakdown := estimateUsageCostBreakdownWithPricingAndLong(pricing, record.PromptTokens, record.CompletionTokens, record.CachedInputTokens, forceLongContext)
	inputCost := record.InputCostMicros
	if inputCost == 0 {
		inputCost = breakdown.InputCostMicros
	}
	outputCost := record.OutputCostMicros
	if outputCost == 0 {
		outputCost = breakdown.OutputCostMicros
	}
	totalCost := record.TotalCostMicros
	if totalCost == 0 {
		totalCost = inputCost + breakdown.CachedInputCostMicros + outputCost
	}
	billableInputTokens := breakdown.BillableInputTokens
	cachedInputTokens := breakdown.CachedInputTokens
	reasoningEffort := strings.TrimSpace(record.Target.ReasoningEffort)
	_, err := rt.DB.Exec(ctx, `
		INSERT INTO ai_gateway_usage (
			virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
			upstream_provider, upstream_model, reasoning_effort, status_code, prompt_tokens,
			completion_tokens, total_tokens, latency_ms, error,
			input_cost_micros, output_cost_micros, total_cost_micros,
			cached_input_tokens, billable_input_tokens, reasoning_tokens, long_context,
			cached_input_cost_micros, response_id, previous_response_id, response_session_id
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, NULLIF($15, ''), $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
	`,
		util.MustParseUUID(record.Key.ID),
		util.MustParseUUID(record.Key.WorkspaceID),
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
		cachedInputTokens,
		billableInputTokens,
		record.ReasoningTokens,
		breakdown.LongContext,
		breakdown.CachedInputCostMicros,
		record.ResponseID,
		record.PreviousResponseID,
		record.ResponseSessionID,
	)
	if err == nil && breakdown.LongContext && record.ResponseSessionID != "" {
		rt.repriceLongContextSession(ctx, record.ResponseSessionID)
	}
	if err != nil && isResponseSessionUsageInsertError(err) {
		_, err = rt.DB.Exec(ctx, `
			INSERT INTO ai_gateway_usage (
				virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
				upstream_provider, upstream_model, reasoning_effort, status_code, prompt_tokens,
				completion_tokens, total_tokens, latency_ms, error,
				input_cost_micros, output_cost_micros, total_cost_micros,
				cached_input_tokens, billable_input_tokens, reasoning_tokens, long_context,
				cached_input_cost_micros
			)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, NULLIF($15, ''), $16, $17, $18, $19, $20, $21, $22, $23)
		`,
			util.MustParseUUID(record.Key.ID),
			util.MustParseUUID(record.Key.WorkspaceID),
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
			cachedInputTokens,
			billableInputTokens,
			record.ReasoningTokens,
			breakdown.LongContext,
			breakdown.CachedInputCostMicros,
		)
	}
	if err != nil && isLegacyUsageInsertError(err) {
		_, _ = rt.DB.Exec(ctx, `
			INSERT INTO ai_gateway_usage (
				virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
				upstream_provider, upstream_model, status_code, prompt_tokens,
				completion_tokens, total_tokens, latency_ms, error
			)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13, NULLIF($14, ''))
		`,
			util.MustParseUUID(record.Key.ID),
			util.MustParseUUID(record.Key.WorkspaceID),
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

func isResponseSessionUsageInsertError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "response_id") ||
		strings.Contains(msg, "previous_response_id") ||
		strings.Contains(msg, "response_session_id")
}

func isLegacyUsageInsertError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, column := range []string{
		"reasoning_effort",
		"input_cost_micros",
		"cached_input_tokens",
		"response_id",
		"response_session_id",
	} {
		if strings.Contains(msg, column) {
			return true
		}
	}
	return false
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"message": msg,
			"type":    "multica_ai_gateway_error",
		},
	})
}
