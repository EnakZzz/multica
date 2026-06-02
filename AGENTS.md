# Repository Guidelines

This file provides guidance to AI agents when working with code in this repository.

> **Single source of truth:** This file is a concise pointer document.
> All authoritative architecture, coding rules, commands, and conventions
> live in **CLAUDE.md** at the project root. Read that file first.

## Quick Reference

### Local browser / E2E testing

When testing a local or self-host page in the in-app browser, do not stop at
the login screen and do not invent ad-hoc auth headers. Reuse the repo's dev
verification-code login path through the UI:

- If the backend was started with `MULTICA_DEV_VERIFICATION_CODE` from `.env`,
  open `/login`, enter a local test email such as `local@multica.test`, then
  use that 6-digit dev code. In this checkout the local `.env` commonly uses
  `888888`.
- Browser tests should exercise that same UI flow: email first, then the dev
  code. Do not seed `localStorage` with `multica_token` as a shortcut unless an
  existing repo helper explicitly does so for a narrow unit/integration setup.
- For automated browser checks, use
  [`microsoft/webwright`](https://github.com/microsoft/webwright) as the default
  browser testing approach. Keep `PLAYWRIGHT_BASE_URL` pointed at the active
  frontend port and set `MULTICA_E2E_USE_DEV_CODE=true` when using the repo's
  existing E2E helpers in `e2e/helpers.ts` / `e2e/fixtures.ts`.
- For authenticated API checks, reuse the CLI token from
  `%USERPROFILE%\.multica\config.json` and pass `X-Workspace-Slug` matching the
  URL, for example `local-agents`. Never print or paste the token value.

For pages like `http://10.160.108.87:3001/local-agents/...`, the user is
usually testing the local Docker self-host build from this checkout. After
frontend or backend code changes, refresh that environment with:

```powershell
.\scripts\docker-selfhost.ps1 update
```

Do not start a separate Next.js dev server on another port unless the user
explicitly asks for it; testing should stay on the same self-host URL.
If the update script builds images and restarts `multica-backend-1` /
`multica-frontend-1` successfully but later fails while stopping/replacing the
local daemon or CLI (for example `OpenProcess: Access is denied` from
`multica daemon stop`), treat that as a post-restart local-daemon issue, not as
a frontend/backend deploy failure. Verify with:

```powershell
.\scripts\docker-selfhost.ps1 status
Invoke-RestMethod http://127.0.0.1:8081/health
Invoke-WebRequest http://127.0.0.1:3001
```

### Architecture

Go backend + monorepo frontend (pnpm workspaces + Turborepo) with shared packages.

- `server/` — Go backend (Chi router, sqlc, gorilla/websocket)
- `apps/web/` — Next.js frontend (App Router)
- `apps/desktop/` — Electron desktop app
- `packages/core/` — Headless business logic (Zustand stores, React Query hooks, API client)
- `packages/ui/` — Atomic UI components (shadcn/Base UI, zero business logic)
- `packages/views/` — Shared business pages/components
- `packages/tsconfig/` — Shared TypeScript config

### State Management (critical)

- **React Query** owns all server state (issues, members, agents, inbox, workspace list)
- **Zustand** owns all client state (current workspace selection, view filters, drafts, modals)
- All Zustand stores live in `packages/core/` — never in `packages/views/` or app directories
- WS events invalidate React Query — never write directly to stores

### Package Boundaries (hard rules)

- `packages/core/` — zero react-dom, zero localStorage, zero process.env
- `packages/ui/` — zero `@multica/core` imports
- `packages/views/` — zero `next/*`, zero `react-router-dom`, use `NavigationAdapter` for routing
- `apps/web/platform/` — only place for Next.js APIs

### Commands

```bash
make dev              # Auto-setup + start everything
pnpm typecheck        # TypeScript check
pnpm test             # TS unit tests (Vitest)
make test             # Go tests
make check            # Full verification pipeline
```

See CLAUDE.md for the complete command reference.
