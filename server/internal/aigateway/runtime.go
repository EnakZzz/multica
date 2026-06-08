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
	"log/slog"
	"math/big"
	"net/http"
	"net/mail"
	"os"
	"regexp"
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
	TokenPrefix                 = "mvk_"
	DefaultURL                  = "https://api.openai.com/v1"
	AuthModeAPIKey              = "api_key"
	AuthModeCustomHeadersCookie = "custom_headers_cookie"
)

var (
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerNamePattern = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)
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
	ID                          string            `json:"id,omitempty"`
	Provider                    string            `json:"provider"`
	BaseURL                     string            `json:"base_url"`
	AuthMode                    string            `json:"auth_mode,omitempty"`
	APIKey                      string            `json:"api_key,omitempty"`
	APIKeyEnv                   string            `json:"api_key_env"`
	APIKeyPool                  []APIKeyPoolItem  `json:"api_key_pool,omitempty"`
	CookieEnv                   string            `json:"cookie_env,omitempty"`
	CustomHeaderEnvs            []CustomHeaderEnv `json:"custom_header_envs,omitempty"`
	Model                       string            `json:"model"`
	UpstreamAPI                 string            `json:"upstream_api,omitempty"`
	ReasoningEffort             string            `json:"reasoning_effort,omitempty"`
	OrganizationEnv             string            `json:"organization_env,omitempty"`
	ProjectEnv                  string            `json:"project_env,omitempty"`
	TimeoutSeconds              int               `json:"timeout_seconds,omitempty"`
	Weight                      int               `json:"weight,omitempty"`
	Priority                    int               `json:"priority,omitempty"`
	Enabled                     bool              `json:"enabled,omitempty"`
	InputPricePerMillionMicros  int64             `json:"input_price_per_million_micros,omitempty"`
	OutputPricePerMillionMicros int64             `json:"output_price_per_million_micros,omitempty"`
}

type APIKeyPoolItem struct {
	ID            string `json:"id,omitempty"`
	Label         string `json:"label"`
	APIKey        string `json:"api_key,omitempty"`
	KeyMasked     string `json:"key_masked,omitempty"`
	SharedByEmail string `json:"shared_by_email"`
	Enabled       bool   `json:"enabled"`
	ReenableAt    string `json:"reenable_at,omitempty"`
}

type CustomHeaderEnv struct {
	HeaderName string `json:"header_name"`
	EnvName    string `json:"env_name"`
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
			t.AuthMode = normalizeAuthMode(t.AuthMode)
			t.UpstreamAPI = strings.TrimSpace(t.UpstreamAPI)
			if t.UpstreamAPI == "" {
				t.UpstreamAPI = "responses"
			}
			if t.UpstreamAPI != "responses" && t.UpstreamAPI != "chat_completions" {
				return nil, fmt.Errorf("AI gateway route %q target %d has unsupported upstream_api %q", routes[i].Alias, j, t.UpstreamAPI)
			}
			t.APIKeyEnv = strings.TrimSpace(t.APIKeyEnv)
			t.APIKey = strings.TrimSpace(t.APIKey)
			t.OrganizationEnv = strings.TrimSpace(t.OrganizationEnv)
			t.ProjectEnv = strings.TrimSpace(t.ProjectEnv)
			t.CookieEnv = strings.TrimSpace(t.CookieEnv)
			normalizedPool, err := normalizeAPIKeyPool(t.APIKeyPool)
			if err != nil {
				return nil, fmt.Errorf("AI gateway route %q target %d has invalid api_key_pool: %w", routes[i].Alias, j, err)
			}
			t.APIKeyPool = normalizedPool
			normalizedHeaders, err := normalizeCustomHeaderEnvs(t.CustomHeaderEnvs)
			if err != nil {
				return nil, fmt.Errorf("AI gateway route %q target %d has invalid custom_header_envs: %w", routes[i].Alias, j, err)
			}
			t.CustomHeaderEnvs = normalizedHeaders
			switch t.AuthMode {
			case AuthModeAPIKey:
				if t.APIKeyEnv == "" && t.APIKey == "" && len(t.APIKeyPool) == 0 {
					return nil, fmt.Errorf("AI gateway route %q target %d is missing api_key_env", routes[i].Alias, j)
				}
				if t.APIKeyEnv != "" {
					if err := validateEnvName(t.APIKeyEnv); err != nil {
						return nil, fmt.Errorf("AI gateway route %q target %d api_key_env is invalid: %w", routes[i].Alias, j, err)
					}
				}
				if len(t.APIKeyPool) > 0 && !strings.EqualFold(t.Provider, "he-tokenapi") {
					return nil, fmt.Errorf("AI gateway route %q target %d api_key_pool only supports provider he-tokenapi", routes[i].Alias, j)
				}
				if len(t.APIKeyPool) > 0 && t.APIKey != "" {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set api_key together with api_key_pool", routes[i].Alias, j)
				}
				if len(t.APIKeyPool) > 0 && len(t.CustomHeaderEnvs) > 0 {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set custom_header_envs with api_key_pool", routes[i].Alias, j)
				}
				if len(t.APIKeyPool) > 0 && t.CookieEnv != "" {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set cookie_env with api_key_pool", routes[i].Alias, j)
				}
				if len(t.APIKeyPool) > 0 && t.UpstreamAPI == "" {
					return nil, fmt.Errorf("AI gateway route %q target %d is missing upstream_api", routes[i].Alias, j)
				}
				if t.CookieEnv != "" || len(t.CustomHeaderEnvs) > 0 {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set cookie_env/custom_header_envs with auth_mode %q", routes[i].Alias, j, t.AuthMode)
				}
				if t.OrganizationEnv != "" {
					if err := validateEnvName(t.OrganizationEnv); err != nil {
						return nil, fmt.Errorf("AI gateway route %q target %d organization_env is invalid: %w", routes[i].Alias, j, err)
					}
				}
				if t.ProjectEnv != "" {
					if err := validateEnvName(t.ProjectEnv); err != nil {
						return nil, fmt.Errorf("AI gateway route %q target %d project_env is invalid: %w", routes[i].Alias, j, err)
					}
				}
			case AuthModeCustomHeadersCookie:
				if t.APIKeyEnv != "" {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set api_key_env with auth_mode %q", routes[i].Alias, j, t.AuthMode)
				}
				if t.OrganizationEnv != "" || t.ProjectEnv != "" {
					return nil, fmt.Errorf("AI gateway route %q target %d cannot set organization_env/project_env with auth_mode %q", routes[i].Alias, j, t.AuthMode)
				}
				if t.CookieEnv == "" && len(t.CustomHeaderEnvs) == 0 {
					return nil, fmt.Errorf("AI gateway route %q target %d must set cookie_env or custom_header_envs for auth_mode %q", routes[i].Alias, j, t.AuthMode)
				}
				if t.CookieEnv != "" {
					if err := validateEnvName(t.CookieEnv); err != nil {
						return nil, fmt.Errorf("AI gateway route %q target %d cookie_env is invalid: %w", routes[i].Alias, j, err)
					}
				}
			default:
				return nil, fmt.Errorf("AI gateway route %q target %d has unsupported auth_mode %q", routes[i].Alias, j, t.AuthMode)
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

func normalizeAuthMode(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", AuthModeAPIKey:
		return AuthModeAPIKey
	case AuthModeCustomHeadersCookie:
		return AuthModeCustomHeadersCookie
	default:
		return strings.TrimSpace(raw)
	}
}

func validateEnvName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("environment variable name is required")
	}
	if strings.ContainsAny(name, "\r\n") || strings.HasPrefix(name, "sk-") {
		return errors.New("must be an environment variable name, not a raw secret")
	}
	if !envNamePattern.MatchString(name) {
		return errors.New("must look like OPENAI_API_KEY")
	}
	return nil
}

func normalizeCustomHeaderEnvs(items []CustomHeaderEnv) ([]CustomHeaderEnv, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]CustomHeaderEnv, 0, len(items))
	for _, item := range items {
		headerName := strings.TrimSpace(item.HeaderName)
		envName := strings.TrimSpace(item.EnvName)
		if headerName == "" && envName == "" {
			continue
		}
		if headerName == "" {
			return nil, errors.New("header_name is required")
		}
		if !headerNamePattern.MatchString(headerName) {
			return nil, fmt.Errorf("header_name %q is invalid", headerName)
		}
		if err := validateEnvName(envName); err != nil {
			return nil, fmt.Errorf("env_name for %q is invalid: %w", headerName, err)
		}
		out = append(out, CustomHeaderEnv{
			HeaderName: http.CanonicalHeaderKey(headerName),
			EnvName:    envName,
		})
	}
	return out, nil
}

func normalizeAPIKeyPool(items []APIKeyPoolItem) ([]APIKeyPoolItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]APIKeyPoolItem, 0, len(items))
	seenLabels := make(map[string]struct{}, len(items))
	seenAPIKeys := make(map[string]struct{}, len(items))
	for _, item := range items {
		normalized := APIKeyPoolItem{
			ID:        strings.TrimSpace(item.ID),
			Label:     strings.TrimSpace(item.Label),
			APIKey:    strings.TrimSpace(item.APIKey),
			KeyMasked: strings.TrimSpace(item.KeyMasked),
			Enabled:   item.Enabled,
		}
		if normalized.Label == "" && normalized.APIKey == "" && normalized.KeyMasked == "" && strings.TrimSpace(item.SharedByEmail) == "" {
			continue
		}
		if normalized.Label == "" {
			return nil, errors.New("label is required")
		}
		normalized.SharedByEmail = strings.ToLower(strings.TrimSpace(item.SharedByEmail))
		if normalized.SharedByEmail == "" {
			return nil, fmt.Errorf("shared_by_email is required for %q", normalized.Label)
		}
		addr, err := mail.ParseAddress(normalized.SharedByEmail)
		if err != nil || addr.Address != normalized.SharedByEmail || addr.Name != "" {
			return nil, fmt.Errorf("shared_by_email for %q must be a valid email address", normalized.Label)
		}
		labelKey := strings.ToLower(normalized.Label)
		if _, exists := seenLabels[labelKey]; exists {
			return nil, fmt.Errorf("duplicate api_key_pool label %q", normalized.Label)
		}
		seenLabels[labelKey] = struct{}{}
		if normalized.APIKey == "" && normalized.KeyMasked == "" {
			return nil, fmt.Errorf("api_key is required for %q", normalized.Label)
		}
		if normalized.APIKey != "" {
			if strings.ContainsAny(normalized.APIKey, "\r\n") {
				return nil, fmt.Errorf("api_key for %q is invalid", normalized.Label)
			}
			if _, exists := seenAPIKeys[normalized.APIKey]; exists {
				return nil, fmt.Errorf("duplicate api_key_pool api_key for %q", normalized.Label)
			}
			seenAPIKeys[normalized.APIKey] = struct{}{}
		}
		if normalized.ReenableAt = strings.TrimSpace(item.ReenableAt); normalized.ReenableAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, normalized.ReenableAt); err != nil {
				return nil, fmt.Errorf("reenable_at for %q is invalid", normalized.Label)
			}
		}
		out = append(out, normalized)
	}
	return out, nil
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

func inheritWildcardHEAPIKeyPools(routes []Route, route Route) Route {
	if route.Alias == "*" {
		return route
	}
	var wildcard Route
	foundWildcard := false
	for _, candidate := range routes {
		if candidate.Alias == "*" {
			wildcard = candidate
			foundWildcard = true
			break
		}
	}
	if !foundWildcard {
		return route
	}
	for idx := range route.Targets {
		target := &route.Targets[idx]
		if !strings.EqualFold(strings.TrimSpace(target.Provider), "he-tokenapi") {
			continue
		}
		if normalizeAuthMode(target.AuthMode) != AuthModeAPIKey {
			continue
		}
		if len(target.APIKeyPool) > 0 {
			continue
		}
		for _, wildcardTarget := range wildcard.Targets {
			if !strings.EqualFold(strings.TrimSpace(wildcardTarget.Provider), "he-tokenapi") {
				continue
			}
			if normalizeAuthMode(wildcardTarget.AuthMode) != AuthModeAPIKey {
				continue
			}
			if len(wildcardTarget.APIKeyPool) == 0 {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(wildcardTarget.BaseURL), strings.TrimSpace(target.BaseURL)) {
				continue
			}
			if strings.TrimSpace(wildcardTarget.APIKeyEnv) != strings.TrimSpace(target.APIKeyEnv) {
				continue
			}
			target.APIKeyPool = append([]APIKeyPoolItem(nil), wildcardTarget.APIKeyPool...)
			break
		}
	}
	return route
}

func mergeWildcardFallbackTargets(routes []Route, route Route, requestedModel string) Route {
	if route.Alias == "*" {
		return route
	}
	var wildcard Route
	foundWildcard := false
	for _, candidate := range routes {
		if candidate.Alias == "*" {
			wildcard = candidate
			foundWildcard = true
			break
		}
	}
	if !foundWildcard {
		return route
	}
	mergedTargets := append([]Target(nil), route.Targets...)
	for _, wildcardTarget := range wildcard.Targets {
		if shouldSkipWildcardFallbackTarget(route.Targets, wildcardTarget, requestedModel) {
			continue
		}
		mergedTargets = append(mergedTargets, wildcardTarget)
	}
	route.Targets = mergedTargets
	return route
}

func shouldSkipWildcardFallbackTarget(localTargets []Target, wildcardTarget Target, requestedModel string) bool {
	if !wildcardFallbackTargetMatchesRequestedModel(wildcardTarget, requestedModel) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(wildcardTarget.Provider), "he-tokenapi") &&
		normalizeAuthMode(wildcardTarget.AuthMode) == AuthModeAPIKey &&
		len(wildcardTarget.APIKeyPool) > 0 {
		for _, localTarget := range localTargets {
			if !strings.EqualFold(strings.TrimSpace(localTarget.Provider), "he-tokenapi") {
				continue
			}
			if normalizeAuthMode(localTarget.AuthMode) != AuthModeAPIKey {
				continue
			}
			if !sameTargetAuthSource(localTarget, wildcardTarget) {
				continue
			}
			return true
		}
	}
	for _, localTarget := range localTargets {
		if !sameTargetFallbackIdentity(localTarget, wildcardTarget, requestedModel) {
			continue
		}
		return true
	}
	return false
}

func sameTargetAuthSource(left Target, right Target) bool {
	return strings.EqualFold(strings.TrimSpace(left.BaseURL), strings.TrimSpace(right.BaseURL)) &&
		normalizeAuthMode(left.AuthMode) == normalizeAuthMode(right.AuthMode) &&
		strings.TrimSpace(left.APIKeyEnv) == strings.TrimSpace(right.APIKeyEnv) &&
		strings.TrimSpace(left.CookieEnv) == strings.TrimSpace(right.CookieEnv)
}

func sameTargetFallbackIdentity(left Target, right Target, requestedModel string) bool {
	if !strings.EqualFold(strings.TrimSpace(left.Provider), strings.TrimSpace(right.Provider)) {
		return false
	}
	if !sameTargetAuthSource(left, right) {
		return false
	}
	if strings.TrimSpace(left.UpstreamAPI) != strings.TrimSpace(right.UpstreamAPI) {
		return false
	}
	leftModel := comparableTargetModel(left.Model, requestedModel)
	rightModel := comparableTargetModel(right.Model, requestedModel)
	return strings.EqualFold(leftModel, rightModel)
}

func comparableTargetModel(model string, requestedModel string) string {
	model = strings.TrimSpace(model)
	if model != "" {
		return normalizeFallbackComparableModel(model)
	}
	return normalizeFallbackComparableModel(requestedModel)
}

func wildcardFallbackTargetMatchesRequestedModel(target Target, requestedModel string) bool {
	targetModel := strings.TrimSpace(target.Model)
	if targetModel == "" {
		return false
	}
	return strings.EqualFold(
		normalizeFallbackComparableModel(targetModel),
		normalizeFallbackComparableModel(requestedModel),
	)
}

func normalizeFallbackComparableModel(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "openai/")
	return model
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
			t.id, t.provider, t.base_url, COALESCE(t.auth_mode, ''), COALESCE(t.api_key_env, ''), COALESCE(t.api_key, ''), COALESCE(t.cookie_env, ''), COALESCE(t.custom_header_envs, '[]'::jsonb),
			t.model, t.upstream_api,
			COALESCE(t.reasoning_effort, ''), COALESCE(t.organization_env, ''), COALESCE(t.project_env, ''),
			t.timeout_seconds, t.weight, t.priority, t.enabled,
			t.input_price_per_million_micros, t.output_price_per_million_micros
		FROM ai_gateway_route r
		JOIN ai_gateway_route_target t ON t.route_id = r.id
		WHERE r.workspace_id = $1 `+enabledClause+`
		ORDER BY r.alias ASC, t.priority ASC, t.created_at ASC
	`, util.MustParseUUID(workspaceID))
	legacyColumns := false
	if err != nil && (isAIGatewayAuthModeColumnError(err) || isAIGatewayAPIKeyColumnError(err)) {
		legacyColumns = true
		rows, err = rt.DB.Query(ctx, `
			SELECT
				r.id, r.alias, r.strategy, r.enabled, r.created_at, r.updated_at,
				t.id, t.provider, t.base_url, COALESCE(t.api_key_env, ''),
				t.model, t.upstream_api,
				COALESCE(t.reasoning_effort, ''), COALESCE(t.organization_env, ''), COALESCE(t.project_env, ''),
				t.timeout_seconds, t.weight, t.priority, t.enabled,
				t.input_price_per_million_micros, t.output_price_per_million_micros
			FROM ai_gateway_route r
			JOIN ai_gateway_route_target t ON t.route_id = r.id
			WHERE r.workspace_id = $1 `+enabledClause+`
			ORDER BY r.alias ASC, t.priority ASC, t.created_at ASC
		`, util.MustParseUUID(workspaceID))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]int{}
	targetIndexByID := map[string][2]int{}
	routes := []Route{}
	for rows.Next() {
		var routeID, targetID pgtype.UUID
		var createdAt, updatedAt pgtype.Timestamptz
		var customHeaderEnvs []byte
		var route Route
		var target Target
		if legacyColumns {
			target.AuthMode = AuthModeAPIKey
			if err := rows.Scan(
				&routeID, &route.Alias, &route.Strategy, &route.Enabled, &createdAt, &updatedAt,
				&targetID, &target.Provider, &target.BaseURL, &target.APIKeyEnv, &target.Model, &target.UpstreamAPI,
				&target.ReasoningEffort, &target.OrganizationEnv, &target.ProjectEnv, &target.TimeoutSeconds, &target.Weight, &target.Priority, &target.Enabled,
				&target.InputPricePerMillionMicros, &target.OutputPricePerMillionMicros,
			); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(
				&routeID, &route.Alias, &route.Strategy, &route.Enabled, &createdAt, &updatedAt,
				&targetID, &target.Provider, &target.BaseURL, &target.AuthMode, &target.APIKeyEnv, &target.APIKey, &target.CookieEnv, &customHeaderEnvs, &target.Model, &target.UpstreamAPI,
				&target.ReasoningEffort, &target.OrganizationEnv, &target.ProjectEnv, &target.TimeoutSeconds, &target.Weight, &target.Priority, &target.Enabled,
				&target.InputPricePerMillionMicros, &target.OutputPricePerMillionMicros,
			); err != nil {
				return nil, err
			}
			if len(customHeaderEnvs) > 0 && string(customHeaderEnvs) != "null" {
				if err := json.Unmarshal(customHeaderEnvs, &target.CustomHeaderEnvs); err != nil {
					return nil, fmt.Errorf("decode ai gateway custom_header_envs: %w", err)
				}
			}
		}
		route.ID = util.UUIDToString(routeID)
		route.CreatedAt = util.TimestampToString(createdAt)
		route.UpdatedAt = util.TimestampToString(updatedAt)
		target.ID = util.UUIDToString(targetID)
		if idx, ok := byID[route.ID]; ok {
			routes[idx].Targets = append(routes[idx].Targets, target)
			targetIndexByID[target.ID] = [2]int{idx, len(routes[idx].Targets) - 1}
		} else {
			route.Targets = []Target{target}
			byID[route.ID] = len(routes)
			targetIndexByID[target.ID] = [2]int{len(routes), 0}
			routes = append(routes, route)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(targetIndexByID) == 0 {
		return routes, nil
	}
	targetIDs := make([]uuid.UUID, 0, len(targetIndexByID))
	for id := range targetIndexByID {
		targetIDs = append(targetIDs, uuid.MustParse(id))
	}
	poolRows, err := rt.DB.Query(ctx, `
		SELECT id, route_target_id, label, api_key, COALESCE(key_masked, ''), shared_by_email, enabled, reenable_at
		FROM ai_gateway_route_target_api_key_pool
		WHERE route_target_id = ANY($1)
		ORDER BY created_at ASC, id ASC
	`, targetIDs)
	if err != nil && !isAIGatewayAPIKeyPoolTableMissing(err) {
		return nil, err
	}
	if err == nil {
		defer poolRows.Close()
		for poolRows.Next() {
			var itemID pgtype.UUID
			var targetID pgtype.UUID
			var label string
			var apiKey string
			var keyMasked string
			var sharedByEmail string
			var enabled bool
			var reenableAt pgtype.Timestamptz
			if err := poolRows.Scan(&itemID, &targetID, &label, &apiKey, &keyMasked, &sharedByEmail, &enabled, &reenableAt); err != nil {
				return nil, err
			}
			targetPos, ok := targetIndexByID[util.UUIDToString(targetID)]
			if !ok {
				continue
			}
			item := APIKeyPoolItem{
				ID:            util.UUIDToString(itemID),
				Label:         label,
				APIKey:        apiKey,
				KeyMasked:     keyMasked,
				SharedByEmail: sharedByEmail,
				Enabled:       enabled,
			}
			if reenableAt.Valid {
				item.ReenableAt = reenableAt.Time.UTC().Format(time.RFC3339Nano)
			}
			routes[targetPos[0]].Targets[targetPos[1]].APIKeyPool = append(routes[targetPos[0]].Targets[targetPos[1]].APIKeyPool, item)
		}
		if err := poolRows.Err(); err != nil {
			return nil, err
		}
	}
	return routes, nil
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

func isAIGatewayAPIKeyColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "api_key")
}

func isAIGatewayAPIKeyPoolTableMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), `relation "ai_gateway_route_target_api_key_pool" does not exist`)
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

type UpstreamAuth struct {
	Headers http.Header
}

func ResolveUpstreamAuth(target Target) (UpstreamAuth, error) {
	headers := make(http.Header)
	switch normalizeAuthMode(target.AuthMode) {
	case AuthModeAPIKey:
		apiKey, err := resolveConfiguredAPIKey(target)
		if err != nil {
			return UpstreamAuth{}, err
		}
		if apiKey == "" {
			return UpstreamAuth{}, errors.New("api key is not configured")
		}
		headers.Set("Authorization", "Bearer "+apiKey)
		if target.OrganizationEnv != "" {
			if err := validateEnvName(target.OrganizationEnv); err != nil {
				return UpstreamAuth{}, fmt.Errorf("organization_env is invalid: %w", err)
			}
		}
		if org := envValue(target.OrganizationEnv); org != "" {
			headers.Set("OpenAI-Organization", org)
		}
		if target.ProjectEnv != "" {
			if err := validateEnvName(target.ProjectEnv); err != nil {
				return UpstreamAuth{}, fmt.Errorf("project_env is invalid: %w", err)
			}
		}
		if project := envValue(target.ProjectEnv); project != "" {
			headers.Set("OpenAI-Project", project)
		}
	case AuthModeCustomHeadersCookie:
		if target.CookieEnv != "" {
			if err := validateEnvName(target.CookieEnv); err != nil {
				return UpstreamAuth{}, err
			}
			cookieValue := envValue(target.CookieEnv)
			if cookieValue == "" {
				return UpstreamAuth{}, fmt.Errorf("environment variable %s is not set", target.CookieEnv)
			}
			headers.Set("Cookie", cookieValue)
		}
		if len(target.CustomHeaderEnvs) == 0 && target.CookieEnv == "" {
			return UpstreamAuth{}, errors.New("cookie_env or custom_header_envs is required")
		}
		for _, item := range target.CustomHeaderEnvs {
			value := envValue(item.EnvName)
			if value == "" {
				return UpstreamAuth{}, fmt.Errorf("environment variable %s is not set", item.EnvName)
			}
			headers.Set(http.CanonicalHeaderKey(item.HeaderName), value)
		}
	default:
		return UpstreamAuth{}, fmt.Errorf("unsupported auth_mode %q", target.AuthMode)
	}
	return UpstreamAuth{Headers: headers}, nil
}

func resolveConfiguredAPIKey(target Target) (string, error) {
	if target.APIKey != "" {
		return target.APIKey, nil
	}
	if item, ok := firstAvailableAPIKeyPoolItem(target.APIKeyPool, time.Now()); ok && item.APIKey != "" {
		return item.APIKey, nil
	}
	if target.APIKeyEnv == "" {
		return "", nil
	}
	if err := validateEnvName(target.APIKeyEnv); err != nil {
		return "", err
	}
	return envValue(target.APIKeyEnv), nil
}

func firstAvailableAPIKeyPoolItem(items []APIKeyPoolItem, now time.Time) (APIKeyPoolItem, bool) {
	for _, item := range items {
		if !item.Enabled {
			if reenableAt, ok := parseAPIKeyPoolReenableAt(item.ReenableAt); !ok || reenableAt.After(now) {
				continue
			}
		}
		if item.APIKey == "" {
			continue
		}
		return item, true
	}
	return APIKeyPoolItem{}, false
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

func (rt *Runtime) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	rt.proxy(w, r, "/images/generations")
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
	route = inheritWildcardHEAPIKeyPools(routes, route)
	route = mergeWildcardFallbackTargets(routes, route, model)

	stream, _ := payload["stream"].(bool)
	requestID := uuid.NewString()
	callerID := key.CallerID()
	var lastErr string
	var lastStatus int
	targets := selectTargets(route, model, endpoint)
	if len(targets) == 0 {
		WriteError(w, http.StatusNotFound, "model is not configured")
		return
	}
	slog.Info("AI gateway target selection",
		"request_id", requestID,
		"caller_id", callerID,
		"endpoint", endpoint,
		"model_alias", model,
		"target_count", len(targets),
		"targets", summarizeAIGatewayTargets(targets),
	)
	for _, target := range targets {
		status, retry, errText, fatalErr := rt.forwardTargetWithRouting(w, r, key, requestID, callerID, endpoint, model, payload, stream, target)
		if fatalErr != nil {
			WriteError(w, http.StatusBadRequest, fatalErr.Error())
			return
		}
		if !retry {
			return
		}
		lastStatus = status
		lastErr = errText
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

func (rt *Runtime) forwardTargetWithRouting(
	w http.ResponseWriter,
	r *http.Request,
	key VirtualKey,
	requestID string,
	callerID string,
	endpoint string,
	model string,
	payload map[string]any,
	stream bool,
	target Target,
) (status int, retry bool, errText string, fatalErr error) {
	if targetUsesHEAPIKeyPool(target) {
		return rt.forwardTargetWithHEAPIKeyPool(w, r, key, requestID, callerID, endpoint, model, payload, stream, target)
	}
	return rt.forwardSingleTarget(w, r, key, requestID, callerID, endpoint, model, payload, stream, target, apiKeyPoolAttempt{})
}

func (rt *Runtime) forwardSingleTarget(
	w http.ResponseWriter,
	r *http.Request,
	key VirtualKey,
	requestID string,
	callerID string,
	endpoint string,
	model string,
	payload map[string]any,
	stream bool,
	target Target,
	poolAttempt apiKeyPoolAttempt,
) (status int, retry bool, errText string, fatalErr error) {
	auth, err := ResolveUpstreamAuth(target)
	if err != nil {
		lastErr := err.Error()
		slog.Warn("AI gateway target auth resolution failed",
			appendLogFields([]any{
				"request_id", requestID,
				"caller_id", callerID,
				"endpoint", endpoint,
				"model_alias", model,
				"provider", target.Provider,
				"target_model", target.Model,
				"error", lastErr,
			}, apiKeyPoolLogFields(poolAttempt)...)...,
		)
		return 0, true, lastErr, nil
	}
	targetModel := resolveTargetModelForEndpoint(endpoint, model, target)
	upstreamPayload := rt.preparePayloadForUpstream(r.Context(), key, endpoint, payload, target)
	previousResponseID := ""
	if endpoint == "/responses" {
		previousResponseID = strings.TrimSpace(stringValue(upstreamPayload["previous_response_id"]))
	}
	upstreamEndpoint, upstreamBody, err := BuildUpstreamRequest(endpoint, upstreamPayload, target, targetModel)
	if err != nil {
		return 0, false, "", err
	}
	requestSummary := summarizeAIGatewayRequest(upstreamBody)
	reasoningEffort := extractAIGatewayReasoningEffort(upstreamBody, upstreamEndpoint)
	forwardTarget := target
	if reasoningEffort != "" {
		forwardTarget.ReasoningEffort = reasoningEffort
	}
	slog.Info("AI gateway target attempt",
		appendLogFields([]any{
			"request_id", requestID,
			"caller_id", callerID,
			"endpoint", endpoint,
			"model_alias", model,
			"provider", forwardTarget.Provider,
			"target_model", targetModel,
			"upstream_endpoint", upstreamEndpoint,
			"request_has_image_url", requestSummary.HasImageURL,
			"request_tool_count", requestSummary.ToolCount,
			"request_tool_types", requestSummary.ToolTypes,
			"request_tool_descriptors", requestSummary.ToolDescriptors,
			"request_text_only", requestSummary.TextOnly,
			"request_input_item_types", requestSummary.InputItemTypes,
		}, apiKeyPoolLogFields(poolAttempt)...)...,
	)
	status, retry, errText = rt.forward(w, r, ForwardRequest{
		Key:                key,
		RequestID:          requestID,
		CallerID:           callerID,
		Endpoint:           endpoint,
		UpstreamEndpoint:   upstreamEndpoint,
		ModelAlias:         model,
		Target:             forwardTarget,
		TargetModel:        targetModel,
		PreviousResponseID: previousResponseID,
		AuthHeaders:        auth.Headers,
		Body:               upstreamBody,
		Stream:             stream,
	})
	slog.Info("AI gateway target result",
		appendLogFields([]any{
			"request_id", requestID,
			"caller_id", callerID,
			"endpoint", endpoint,
			"model_alias", model,
			"provider", forwardTarget.Provider,
			"target_model", targetModel,
			"status", status,
			"retry", retry,
			"error", errText,
		}, apiKeyPoolLogFields(poolAttempt)...)...,
	)
	return status, retry, errText, nil
}

func (rt *Runtime) forwardTargetWithHEAPIKeyPool(
	w http.ResponseWriter,
	r *http.Request,
	key VirtualKey,
	requestID string,
	callerID string,
	endpoint string,
	model string,
	payload map[string]any,
	stream bool,
	target Target,
) (status int, retry bool, errText string, fatalErr error) {
	refreshed, err := rt.refreshExpiredAPIKeyPoolItems(r.Context(), target)
	if err != nil {
		slog.Warn("AI gateway pool key refresh failed",
			"request_id", requestID,
			"caller_id", callerID,
			"endpoint", endpoint,
			"model_alias", model,
			"provider", target.Provider,
			"error", err.Error(),
		)
	}
	target = refreshed
	attempts := buildAPIKeyPoolAttempts(target, callerID)
	if len(attempts) == 0 {
		errText = "no enabled HE API key pool item available"
		slog.Warn("AI gateway HE api key pool unavailable",
			"request_id", requestID,
			"caller_id", callerID,
			"endpoint", endpoint,
			"model_alias", model,
			"provider", target.Provider,
			"pool_size", len(target.APIKeyPool),
			"error", errText,
		)
		return 0, true, errText, nil
	}
	for _, attempt := range attempts {
		attemptTarget := target
		attemptTarget.APIKey = attempt.Item.APIKey
		status, retry, errText, fatalErr = rt.forwardSingleTarget(w, r, key, requestID, callerID, endpoint, model, payload, stream, attemptTarget, attempt)
		if fatalErr != nil {
			return status, retry, errText, fatalErr
		}
		if !retry {
			return status, retry, errText, nil
		}
		if shouldCooldownAPIKeyPoolItem(status, errText) {
			reenableAt := time.Now().Add(time.Hour)
			target = rt.applyAPIKeyPoolCooldown(target, attempt.Item.ID, reenableAt)
			if err := rt.persistAPIKeyPoolCooldown(context.Background(), target.ID, attempt.Item, reenableAt); err != nil {
				slog.Warn("AI gateway pool key cooldown update failed",
					appendLogFields([]any{
						"request_id", requestID,
						"caller_id", callerID,
						"endpoint", endpoint,
						"model_alias", model,
						"provider", target.Provider,
						"error", err.Error(),
					}, apiKeyPoolLogFields(attempt)...)...,
				)
			}
			continue
		}
		if retry && status > 0 {
			continue
		}
		return status, retry, errText, nil
	}
	if errText == "" {
		errText = "no usable HE API key pool item available"
	}
	return status, true, errText, nil
}

func summarizeAIGatewayTargets(targets []Target) []string {
	if len(targets) == 0 {
		return nil
	}
	summary := make([]string, 0, len(targets))
	for _, target := range targets {
		model := strings.TrimSpace(target.Model)
		if model == "" {
			model = "*"
		}
		summary = append(summary, fmt.Sprintf("%s:%s", strings.TrimSpace(target.Provider), model))
	}
	return summary
}

type apiKeyPoolAttempt struct {
	Item       APIKeyPoolItem
	Attempt    int
	PoolSize   int
	OwnerMatch bool
}

func targetUsesHEAPIKeyPool(target Target) bool {
	return strings.EqualFold(strings.TrimSpace(target.Provider), "he-tokenapi") &&
		normalizeAuthMode(target.AuthMode) == AuthModeAPIKey &&
		len(target.APIKeyPool) > 0
}

func parseAPIKeyPoolReenableAt(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func rotateAPIKeyPoolItems(items []APIKeyPoolItem) []APIKeyPoolItem {
	if len(items) <= 1 {
		return append([]APIKeyPoolItem(nil), items...)
	}
	idx := int(time.Now().UnixNano() % int64(len(items)))
	return append(append([]APIKeyPoolItem(nil), items[idx:]...), items[:idx]...)
}

func buildAPIKeyPoolAttempts(target Target, callerID string) []apiKeyPoolAttempt {
	now := time.Now()
	available := make([]APIKeyPoolItem, 0, len(target.APIKeyPool))
	callerID = strings.ToLower(strings.TrimSpace(callerID))
	var ownerItem APIKeyPoolItem
	ownerMatched := false
	for _, item := range target.APIKeyPool {
		if item.APIKey == "" {
			continue
		}
		if !item.Enabled {
			if reenableAt, ok := parseAPIKeyPoolReenableAt(item.ReenableAt); !ok || reenableAt.After(now) {
				continue
			}
		}
		if callerID != "" && strings.EqualFold(strings.TrimSpace(item.SharedByEmail), callerID) {
			ownerItem = item
			ownerMatched = true
			continue
		}
		available = append(available, item)
	}
	available = rotateAPIKeyPoolItems(available)
	attempts := make([]apiKeyPoolAttempt, 0, len(available)+1)
	poolSize := len(target.APIKeyPool)
	if ownerMatched {
		attempts = append(attempts, apiKeyPoolAttempt{
			Item:       ownerItem,
			Attempt:    1,
			PoolSize:   poolSize,
			OwnerMatch: true,
		})
	}
	for _, item := range available {
		attempts = append(attempts, apiKeyPoolAttempt{
			Item:       item,
			Attempt:    len(attempts) + 1,
			PoolSize:   poolSize,
			OwnerMatch: false,
		})
	}
	return attempts
}

func shouldCooldownAPIKeyPoolItem(status int, errorText string) bool {
	switch status {
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
		return isAIGatewayPoolQuotaCooldownError(errorText)
	default:
		return false
	}
}

func isAIGatewayPoolQuotaCooldownError(errorText string) bool {
	text := strings.ToLower(strings.TrimSpace(errorText))
	if text == "" {
		return false
	}
	markers := []string{
		"quota",
		"insufficient_quota",
		"credit",
		"credits",
		"balance",
		"exhausted",
		"out of credit",
		"out of credits",
		"insufficient balance",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func apiKeyPoolLogFields(attempt apiKeyPoolAttempt) []any {
	fields := []any{
		"pool_size", attempt.PoolSize,
		"pool_key_id", attempt.Item.ID,
		"pool_key_label", attempt.Item.Label,
		"pool_key_shared_by_email", attempt.Item.SharedByEmail,
		"pool_key_owner_match", attempt.OwnerMatch,
		"pool_attempt", attempt.Attempt,
	}
	if attempt.Item.ReenableAt != "" {
		fields = append(fields, "pool_key_reenable_at", attempt.Item.ReenableAt)
	}
	return fields
}

func appendLogFields(fields []any, extras ...any) []any {
	if len(extras) == 0 {
		return fields
	}
	return append(fields, extras...)
}

func (rt *Runtime) refreshExpiredAPIKeyPoolItems(ctx context.Context, target Target) (Target, error) {
	if !targetUsesHEAPIKeyPool(target) {
		return target, nil
	}
	now := time.Now()
	updated := false
	for idx := range target.APIKeyPool {
		item := &target.APIKeyPool[idx]
		if item.Enabled {
			continue
		}
		reenableAt, ok := parseAPIKeyPoolReenableAt(item.ReenableAt)
		if !ok || reenableAt.After(now) {
			continue
		}
		if rt.DB != nil && target.ID != "" && item.ID != "" {
			if _, err := rt.DB.Exec(ctx, `
				UPDATE ai_gateway_route_target_api_key_pool
				SET enabled = true, reenable_at = NULL, updated_at = now()
				WHERE id = $1 AND route_target_id = $2
			`, util.MustParseUUID(item.ID), util.MustParseUUID(target.ID)); err != nil {
				return target, err
			}
		}
		item.Enabled = true
		item.ReenableAt = ""
		updated = true
		slog.Info("AI gateway pool key re-enabled after cooldown",
			"route_target_id", target.ID,
			"pool_key_id", item.ID,
			"pool_key_label", item.Label,
			"pool_key_shared_by_email", item.SharedByEmail,
		)
	}
	if !updated {
		return target, nil
	}
	return target, nil
}

func (rt *Runtime) applyAPIKeyPoolCooldown(target Target, itemID string, reenableAt time.Time) Target {
	for idx := range target.APIKeyPool {
		if target.APIKeyPool[idx].ID != itemID {
			continue
		}
		target.APIKeyPool[idx].Enabled = false
		target.APIKeyPool[idx].ReenableAt = reenableAt.UTC().Format(time.RFC3339Nano)
		break
	}
	return target
}

func (rt *Runtime) persistAPIKeyPoolCooldown(ctx context.Context, targetID string, item APIKeyPoolItem, reenableAt time.Time) error {
	slog.Warn("AI gateway pool key disabled for quota cooldown",
		"route_target_id", targetID,
		"pool_key_id", item.ID,
		"pool_key_label", item.Label,
		"pool_key_shared_by_email", item.SharedByEmail,
		"pool_key_reenable_at", reenableAt.UTC().Format(time.RFC3339Nano),
	)
	if rt.DB == nil || targetID == "" || item.ID == "" {
		return nil
	}
	_, err := rt.DB.Exec(ctx, `
		UPDATE ai_gateway_route_target_api_key_pool
		SET enabled = false, reenable_at = $3, updated_at = now()
		WHERE id = $1 AND route_target_id = $2
	`, util.MustParseUUID(item.ID), util.MustParseUUID(targetID), reenableAt.UTC())
	return err
}

type requestAuditSummary struct {
	HasImageURL     bool
	ToolCount       int
	ToolTypes       []string
	ToolDescriptors []string
	TextOnly        bool
	InputItemTypes  []string
}

func summarizeAIGatewayRequest(body []byte) requestAuditSummary {
	if len(body) == 0 {
		return requestAuditSummary{}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return requestAuditSummary{}
	}
	summary := requestAuditSummary{}
	summary.ToolTypes, summary.ToolDescriptors = collectToolDetails(payload["tools"])
	summary.ToolCount = len(summary.ToolTypes)
	types, hasImage, nonText := collectInputShape(payload)
	summary.InputItemTypes = uniqueNonEmptyStrings(types)
	summary.HasImageURL = hasImage
	summary.TextOnly = len(summary.InputItemTypes) > 0 && !summary.HasImageURL && !nonText
	return summary
}

func collectInputShape(payload map[string]any) ([]string, bool, bool) {
	var types []string
	hasImage := false
	nonText := false
	appendPart := func(kind string) {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return
		}
		types = append(types, kind)
		switch kind {
		case "text", "input_text", "output_text":
		case "message":
		case "image_url", "input_image", "image":
			hasImage = true
			nonText = true
		default:
			nonText = true
		}
	}
	walkContent := func(content any) {
		switch value := content.(type) {
		case string:
			appendPart("text")
		case []any:
			for _, raw := range value {
				if obj, ok := raw.(map[string]any); ok {
					appendPart(stringValue(obj["type"]))
					continue
				}
				appendPart("text")
			}
		case map[string]any:
			appendPart(stringValue(value["type"]))
		}
	}
	switch input := payload["input"].(type) {
	case string:
		appendPart("text")
	case []any:
		for _, raw := range input {
			obj, ok := raw.(map[string]any)
			if !ok {
				appendPart("text")
				continue
			}
			typ := stringValue(obj["type"])
			if typ != "" {
				appendPart(typ)
			}
			if content, ok := obj["content"]; ok {
				walkContent(content)
			}
		}
	}
	if messages, ok := payload["messages"].([]any); ok {
		for _, raw := range messages {
			obj, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if content, ok := obj["content"]; ok {
				walkContent(content)
			}
		}
	}
	return types, hasImage, nonText
}

func collectToolDetails(raw any) ([]string, []string) {
	items, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	types := make([]string, 0, len(items))
	descriptors := make([]string, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		toolType := strings.TrimSpace(stringValue(obj["type"]))
		if toolType != "" {
			types = append(types, toolType)
		}
		toolName := strings.TrimSpace(stringValue(obj["name"]))
		switch {
		case toolType != "" && toolName != "":
			descriptors = append(descriptors, toolType+":"+toolName)
		case toolType != "":
			descriptors = append(descriptors, toolType)
		case toolName != "":
			descriptors = append(descriptors, "name:"+toolName)
		}
	}
	return uniqueNonEmptyStrings(types), uniqueNonEmptyStrings(descriptors)
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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

func (rt *Runtime) preparePayloadForUpstream(ctx context.Context, key VirtualKey, endpoint string, payload map[string]any, target Target) map[string]any {
	copyPayload := make(map[string]any, len(payload))
	for k, v := range payload {
		copyPayload[k] = v
	}
	if endpoint == "/responses" {
		rt.scopeResponsesState(ctx, key, copyPayload)
	}
	sanitizeAnthropicHEResponsesPayload(copyPayload, endpoint, target)
	filterUnsupportedResponseTools(copyPayload, endpoint, target)
	return copyPayload
}

func sanitizeAnthropicHEResponsesPayload(payload map[string]any, endpoint string, target Target) {
	if endpoint != "/responses" || !strings.EqualFold(strings.TrimSpace(target.Provider), "he-tokenapi") || !targetUsesAnthropicResponsesModel(target) {
		return
	}
	for _, key := range []string{
		"background",
		"client_metadata",
		"include",
		"max_tool_calls",
		"metadata",
		"parallel_tool_calls",
		"prompt_cache_retention",
		"service_tier",
		"store",
		"text",
	} {
		delete(payload, key)
	}
}

func filterUnsupportedResponseTools(payload map[string]any, endpoint string, target Target) {
	if endpoint != "/responses" || !strings.EqualFold(strings.TrimSpace(target.Provider), "he-tokenapi") {
		return
	}
	items, ok := payload["tools"].([]any)
	if !ok || len(items) == 0 {
		return
	}
	filtered := make([]any, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		toolType := strings.TrimSpace(stringValue(obj["type"]))
		if strings.EqualFold(toolType, "image_generation") {
			continue
		}
		if targetUsesAnthropicResponsesModel(target) && !strings.EqualFold(toolType, "function") {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		delete(payload, "tools")
		return
	}
	payload["tools"] = filtered
}

func targetUsesAnthropicResponsesModel(target Target) bool {
	model := strings.TrimSpace(strings.ToLower(target.Model))
	return strings.HasPrefix(model, "anthropic/")
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

func selectTargets(route Route, requestedModel string, endpoint string) []Target {
	targets := make([]Target, 0, len(route.Targets))
	codexTemplateTargets := make([]Target, 0, len(route.Targets))
	for _, target := range route.Targets {
		if target.Enabled || target.ID == "" {
			if endpoint == "/images/generations" && !targetSupportsImageGeneration(target) {
				continue
			}
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

func targetSupportsImageGeneration(target Target) bool {
	return strings.EqualFold(strings.TrimSpace(target.Provider), "openai")
}

func resolveTargetModelForEndpoint(endpoint string, requestedModel string, target Target) string {
	targetModel := strings.TrimSpace(target.Model)
	if endpoint == "/images/generations" {
		if imageModel := resolveImageGenerationTargetModel(requestedModel, targetModel); imageModel != "" {
			return imageModel
		}
	}
	if targetModel != "" {
		return targetModel
	}
	return strings.TrimSpace(requestedModel)
}

func resolveImageGenerationTargetModel(requestedModel string, targetModel string) string {
	if targetModel = strings.TrimSpace(targetModel); targetModel != "" {
		canonicalTargetModel := canonicalizeImageGenerationModel(targetModel)
		if canonicalTargetModel == "gpt-image-1" {
			return "gpt-image-1"
		}
	}
	switch canonicalizeImageGenerationModel(requestedModel) {
	case "gpt-5.4", "gpt-5.4-mini", "gpt-5.5", "gpt-image-1":
		return "gpt-image-1"
	default:
		return targetModel
	}
}

func canonicalizeImageGenerationModel(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "openai/")
	return model
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
	AuthHeaders        http.Header
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
	for key, values := range req.AuthHeaders {
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept-Encoding", "identity")
	upstreamReq.Header.Set("Accept", r.Header.Get("Accept"))
	if upstreamReq.Header.Get("Accept") == "" {
		upstreamReq.Header.Set("Accept", "application/json")
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

	if resp.StatusCode >= http.StatusBadRequest {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if len(errorBody) > 0 {
			resp.Body = io.NopCloser(bytes.NewReader(errorBody))
		}
		errText := strings.TrimSpace(string(errorBody))
		if errText == "" {
			errText = resp.Status
		}
		if shouldRetryAIGatewayFailure(req.Target, resp.StatusCode, errText) {
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

func shouldRetryAIGatewayFailure(target Target, status int, errorText string) bool {
	if shouldRetryAIGatewayStatus(status) {
		return true
	}
	switch status {
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusForbidden:
		if isAIGatewayQuotaError(errorText) {
			return true
		}
		if strings.EqualFold(target.Provider, "he-tokenapi") && isAIGatewayHardPermissionError(errorText) {
			return false
		}
		return isAIGatewayFallbackablePermissionError(errorText)
	default:
		return false
	}
}

func isAIGatewayQuotaError(errorText string) bool {
	text := strings.ToLower(strings.TrimSpace(errorText))
	if text == "" {
		return false
	}
	markers := []string{
		"quota",
		"insufficient_quota",
		"rate limit",
		"too many requests",
		"credit",
		"credits",
		"balance",
		"billing",
		"exhausted",
		"over limit",
		"out of credit",
		"out of credits",
		"insufficient balance",
		"额度",
		"超限",
		"余额不足",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isAIGatewayFallbackablePermissionError(errorText string) bool {
	text := strings.ToLower(strings.TrimSpace(errorText))
	if text == "" {
		return false
	}
	markers := []string{
		"permission_error",
		"available model group fallbacks=none",
		"key_model_access_denied",
		"key not allowed to access model",
		"model group",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func isAIGatewayHardPermissionError(errorText string) bool {
	text := strings.ToLower(strings.TrimSpace(errorText))
	if text == "" {
		return false
	}
	markers := []string{
		"not enabled for this group",
		"image generation is not enabled for this group",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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
	if normalizeAuthMode(target.AuthMode) == AuthModeCustomHeadersCookie {
		return defaultUsagePricing{}
	}
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
		SELECT id, COALESCE(auth_mode, ''), COALESCE(upstream_model, ''), prompt_tokens, completion_tokens, cached_input_tokens
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
		var authMode string
		var model string
		var promptTokens, completionTokens, cachedInputTokens int64
		if err := rows.Scan(&id, &authMode, &model, &promptTokens, &completionTokens, &cachedInputTokens); err != nil {
			return
		}
		if normalizeAuthMode(authMode) == AuthModeCustomHeadersCookie {
			continue
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
	authMode := normalizeAuthMode(record.Target.AuthMode)
	insertUsageWithResponseSession := func(includeAuthMode bool) error {
		if includeAuthMode {
			_, err := rt.DB.Exec(ctx, `
				INSERT INTO ai_gateway_usage (
					virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
					upstream_provider, upstream_model, auth_mode, reasoning_effort, status_code, prompt_tokens,
					completion_tokens, total_tokens, latency_ms, error,
					input_cost_micros, output_cost_micros, total_cost_micros,
					cached_input_tokens, billable_input_tokens, reasoning_tokens, long_context,
					cached_input_cost_micros, response_id, previous_response_id, response_session_id
				)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, $15, NULLIF($16, ''), $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)
			`,
				util.MustParseUUID(record.Key.ID),
				util.MustParseUUID(record.Key.WorkspaceID),
				record.RequestID,
				record.CallerID,
				record.Endpoint,
				record.ModelAlias,
				record.Target.Provider,
				record.TargetModel,
				authMode,
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
			return err
		}
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
		return err
	}
	insertUsageLegacyResponseSession := func(includeAuthMode bool) error {
		if includeAuthMode {
			_, err := rt.DB.Exec(ctx, `
				INSERT INTO ai_gateway_usage (
					virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
					upstream_provider, upstream_model, auth_mode, reasoning_effort, status_code, prompt_tokens,
					completion_tokens, total_tokens, latency_ms, error,
					input_cost_micros, output_cost_micros, total_cost_micros,
					cached_input_tokens, billable_input_tokens, reasoning_tokens, long_context,
					cached_input_cost_micros
				)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, $15, NULLIF($16, ''), $17, $18, $19, $20, $21, $22, $23, $24)
			`,
				util.MustParseUUID(record.Key.ID),
				util.MustParseUUID(record.Key.WorkspaceID),
				record.RequestID,
				record.CallerID,
				record.Endpoint,
				record.ModelAlias,
				record.Target.Provider,
				record.TargetModel,
				authMode,
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
			return err
		}
		_, err := rt.DB.Exec(ctx, `
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
		return err
	}
	includeAuthMode := true
	err := insertUsageWithResponseSession(includeAuthMode)
	if err != nil && isAIGatewayAuthModeColumnError(err) {
		includeAuthMode = false
		err = insertUsageWithResponseSession(includeAuthMode)
	}
	if err == nil && breakdown.LongContext && record.ResponseSessionID != "" {
		rt.repriceLongContextSession(ctx, record.ResponseSessionID)
	}
	if err != nil && isResponseSessionUsageInsertError(err) {
		err = insertUsageLegacyResponseSession(includeAuthMode)
	}
	if err != nil && isLegacyUsageInsertError(err) {
		if includeAuthMode {
			_, _ = rt.DB.Exec(ctx, `
				INSERT INTO ai_gateway_usage (
					virtual_key_id, workspace_id, request_id, caller_id, endpoint, model_alias,
					upstream_provider, upstream_model, auth_mode, status_code, prompt_tokens,
					completion_tokens, total_tokens, latency_ms, error
				)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, $14, NULLIF($15, ''))
			`,
				util.MustParseUUID(record.Key.ID),
				util.MustParseUUID(record.Key.WorkspaceID),
				record.RequestID,
				record.CallerID,
				record.Endpoint,
				record.ModelAlias,
				record.Target.Provider,
				record.TargetModel,
				authMode,
				int32(record.StatusCode),
				record.PromptTokens,
				record.CompletionTokens,
				record.TotalTokens,
				record.LatencyMs,
				errText,
			)
		} else {
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
}

func isResponseSessionUsageInsertError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "response_id") ||
		strings.Contains(msg, "previous_response_id") ||
		strings.Contains(msg, "response_session_id") ||
		strings.Contains(msg, "auth_mode")
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
		"auth_mode",
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
