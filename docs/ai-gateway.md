# AI Gateway

Multica can expose an OpenAI-compatible API gateway for a workspace. Team
members receive a Multica virtual key (`mvk_...`) instead of upstream provider
keys. The server validates that key, rewrites configured model aliases to real
upstream models, proxies the request, and records usage metadata without storing
prompts or completions.

## Endpoints

- `GET /v1/models`
- `POST /v1/responses`
- `POST /v1/chat/completions`

Use the virtual key as a bearer token:

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer mvk_xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"team-agent","input":"Summarize this error"}'
```

## Create a Virtual Key

Workspace owners/admins can manage keys through the regular authenticated API.
Send either `X-Workspace-ID` or `X-Workspace-Slug`.

For per-person accounting, create one virtual key per person or device. Use a
stable key name such as `alice@company.com` or `alice-codex-laptop`.

```bash
curl http://localhost:8080/api/ai-gateway/keys \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents" \
  -H "Content-Type: application/json" \
  -d '{"name":"engineering","expires_in_days":90}'
```

The raw `token` is returned only once. List keys and recent usage with:

```bash
curl http://localhost:8080/api/ai-gateway/keys \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents"

curl http://localhost:8080/api/ai-gateway/usage?limit=100 \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents"

curl http://localhost:8080/api/ai-gateway/usage/summary?days=30 \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents"
```

Revoke a key:

```bash
curl -X DELETE http://localhost:8080/api/ai-gateway/keys/<key-id> \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents"
```

## Configure Routes

Preferred path: use **Settings -> AI Gateway -> Routes and providers**.
Workspace owners/admins can create model aliases, apply provider presets, probe
provider capabilities, add multiple targets, and set per-million-token prices
for cost reporting.

The route API is also available:

```bash
curl http://localhost:8080/api/ai-gateway/provider-presets \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents"

curl http://localhost:8080/api/ai-gateway/probe \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents" \
  -H "Content-Type: application/json" \
  -d '{"base_url":"http://claude-proxy.example.com/v1","api_key_env":"ANTHROPIC_AUTH_TOKEN","model":"claude-sonnet-4-6"}'
```

Example managed route with OpenAI first and a Claude local fallback that exposes
OpenAI-compatible Chat Completions:

```bash
curl http://localhost:8080/api/ai-gateway/routes \
  -H "Authorization: Bearer mul_or_jwt_token" \
  -H "X-Workspace-Slug: local-agents" \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "team-agent",
    "strategy": "fallback",
    "enabled": true,
    "targets": [
      {
        "provider": "openai",
        "base_url": "https://api.openai.com/v1",
        "api_key_env": "OPENAI_API_KEY",
        "model": "gpt-5-codex",
        "upstream_api": "responses",
        "timeout_seconds": 300,
        "weight": 1,
        "input_price_per_million_micros": 1250000,
        "output_price_per_million_micros": 10000000
      },
      {
        "provider": "claude-local",
        "base_url": "http://claude-proxy.example.com/v1",
        "api_key_env": "ANTHROPIC_AUTH_TOKEN",
        "model": "claude-sonnet-4-6",
        "upstream_api": "chat_completions",
        "timeout_seconds": 3000,
        "weight": 1
      }
    ]
  }'
```

`upstream_api = "chat_completions"` lets Multica accept Codex/OpenAI Responses
requests on `/v1/responses`, call the upstream `/v1/chat/completions`, and
convert the result back into a Responses-shaped response. Streaming text deltas
are converted to basic Responses SSE events. Advanced tool-call conversion is
best validated against the exact client workload before broad rollout.

Set `reasoning_effort` on a target only when the route must force one upstream
reasoning level regardless of the caller. Responses targets receive
`reasoning.effort`; Chat Completions targets receive `reasoning_effort`. Leave
it empty to preserve the caller's request, including Codex UI reasoning choices,
or the upstream default.

Supported route strategies:

- `fallback`: try targets in order, moving on after timeout, `429`, or `5xx`.
- `single`: only use the first target.
- `round_robin`: rotate the first target by request time.
- `weighted`: choose the first target by configured weight, then use the rest
  as fallback candidates.

Legacy environment-only route config still works when no database route exists
for the workspace:

Minimal single-upstream config:

```env
OPENAI_API_KEY=sk-...
AI_GATEWAY_DEFAULT_ALIAS=team-agent
AI_GATEWAY_DEFAULT_MODEL=gpt-5
```

Fallback route across multiple OpenAI-compatible upstreams:

```env
OPENAI_API_KEY=sk-...
OPENROUTER_API_KEY=sk-or-...
AI_GATEWAY_ROUTES=[{"alias":"team-agent","targets":[{"provider":"openai","base_url":"https://api.openai.com/v1","api_key_env":"OPENAI_API_KEY","model":"gpt-5"},{"provider":"openrouter","base_url":"https://openrouter.ai/api/v1","api_key_env":"OPENROUTER_API_KEY","model":"anthropic/claude-sonnet"}]}]
```

`/v1/models` returns configured aliases. For wildcard (`"alias":"*"`) routes,
it also returns every non-empty target model, so clients such as Codex can
discover switchable model IDs. Incoming requests using `"model":"team-agent"`
are rewritten to the target model before they leave Multica. Targets are tried
in order on upstream timeout, `429`, or `5xx`.

## Configure Codex

Codex can use Multica as an OpenAI-compatible model provider. Put the virtual
key in an environment variable, and point Codex at the Multica `/v1` gateway.

PowerShell:

```powershell
[Environment]::SetEnvironmentVariable("MULTICA_AI_GATEWAY_KEY", "mvk_xxx", "User")
[Environment]::SetEnvironmentVariable("MULTICA_AI_CALLER", "alice@company.com", "User")
```

`~/.codex/config.toml`:

```toml
model = "team-agent"
model_provider = "multica"

[model_providers.multica]
name = "Multica AI Gateway"
base_url = "https://multica.example.com/v1"
wire_api = "responses"
env_key = "MULTICA_AI_GATEWAY_KEY"
env_key_instructions = "Set MULTICA_AI_GATEWAY_KEY to your mvk_ virtual key"
env_http_headers = { "X-Multica-Caller" = "MULTICA_AI_CALLER" }
```

For local testing, `base_url` can be `http://localhost:8081/v1`. If Codex and
Multica run on different machines, use the reachable HTTPS origin for the
Multica server.

`X-Multica-Caller` is optional but recommended when several devices share one
virtual key. Multica stores it as usage metadata only; prompts and completions
are still not persisted by the gateway.

### Codex Model Switching

If you want Codex's model selector to affect Multica routing, configure the
gateway models as route aliases or use a wildcard route.

Exact aliases are the most explicit option:

```env
AI_GATEWAY_ROUTES=[
  {"alias":"gpt-5-codex","targets":[{"provider":"openai","base_url":"https://api.openai.com/v1","api_key_env":"OPENAI_API_KEY","model":"gpt-5-codex","upstream_api":"responses"}]},
  {"alias":"claude-sonnet-4-6","targets":[{"provider":"claude-local","base_url":"http://claude-proxy.example.com/v1","api_key_env":"ANTHROPIC_AUTH_TOKEN","model":"claude-sonnet-4-6","upstream_api":"chat_completions","timeout_seconds":3000}]}
]
```

With that config, changing Codex from `gpt-5-codex` to
`claude-sonnet-4-6` changes the selected route while keeping the same Multica
virtual key.

A wildcard route can aggregate several switchable models under one route:

```env
AI_GATEWAY_ROUTES=[{"alias":"*","targets":[
  {"provider":"openai","base_url":"https://api.openai.com/v1","api_key_env":"OPENAI_API_KEY","model":"gpt-5-codex","upstream_api":"responses"},
  {"provider":"claude-local","base_url":"http://claude-proxy.example.com/v1","api_key_env":"ANTHROPIC_AUTH_TOKEN","model":"claude-sonnet-4-6","upstream_api":"chat_completions","timeout_seconds":3000}
]}]
```

On wildcard routes, a target with a non-empty `model` is only used when the
incoming Codex model matches that model ID. A target with an empty `model`
passes any requested model through to that upstream.

Wildcard route for direct model pass-through to one OpenAI-compatible upstream:

```env
AI_GATEWAY_ROUTES=[{"alias":"*","targets":[{"provider":"openai","base_url":"https://api.openai.com/v1","api_key_env":"OPENAI_API_KEY"}]}]
```

Use the wildcard only when the virtual key should be allowed to call arbitrary
models on that upstream.

## Usage and Cost Reporting

`ai_gateway_usage` records request metadata, selected upstream provider/model,
token counts returned by the upstream, latency, error text, caller metadata, and
estimated cost in micro-USD. Cost is calculated from the selected target's
`input_price_per_million_micros` and `output_price_per_million_micros` fields.
If an upstream does not return token usage, request count and latency are still
recorded, but token and cost fields stay at zero.
