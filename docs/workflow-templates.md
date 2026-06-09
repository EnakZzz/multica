# Workflow templates

This checkout keeps reusable Multica workflows in `.multica/pipelines/*.yaml`.
They are repo-local templates that can be imported into a workspace with:

```powershell
cd F:\UnityAgentOne\AgentsTeam\multica
make cli ARGS="repo import-context"
```

On Windows environments without `make`, run the CLI from the Go module:

```powershell
cd F:\UnityAgentOne\AgentsTeam\multica\server
go run .\cmd\multica repo import-context
```

Use these templates as the default routing layer before asking one agent to run
a long task in a single context.

## Review gated feature development

Use for normal feature work that fits in one implementation branch.

Shape:

- classify the scope and verification route
- implement on an isolated branch
- run focused verification
- pass spec and code review gates
- create or reuse a PR/MR with `multica repo integrate --strategy pr-first`

Stop conditions:

- the classify step says the work is too broad
- focused verification has an unexplained failure
- either review gate finds a blocking issue
- PR/MR creation fails

## Root-cause investigation

Use for failures, regressions, local self-host bugs, daemon/runtime issues, or
unclear user reports.

Shape:

- collect the exact failing command, URL, logs, state, and reproduction boundary
- generate source-level hypotheses
- generate runtime/state hypotheses
- verify and falsify hypotheses on the real failing path
- synthesize one root cause and rejected alternatives
- apply the smallest fix or create a bounded follow-up

Stop conditions:

- the original failure cannot be reproduced and no reliable evidence exists
- source and runtime hypotheses remain untestable without owner input
- evidence shows the fix is outside the current issue scope

## Dynamic fan-out feature development

Use for broad cross-module changes that touch multiple contracts or require
parallel work packages.

Shape:

- classify affected surfaces and produce a fan-out plan
- run backend/data-plane and frontend/shared-package work in separate nodes
- verify each work package
- synthesize into one coherent branch
- run adversarial review for cross-module regressions
- create or reuse a PR/MR only after the gate passes

Stop conditions:

- the task is narrow enough for Review gated feature development
- a work package lacks independent verification
- packages changed the same contract incompatibly
- adversarial review finds a blocker

## Operating rules

- Keep AGENTS.md and CLAUDE.md constraints visible in classify, implementation,
  and review nodes.
- Prefer current checkout evidence over stale memory or old run logs.
- For browser checks against local self-host, use the verification-code login
  path and stay on the active self-host URL.
- Do not restart self-host services from a workflow unless the issue explicitly
  asks for deployment or refresh.
- For Windows-authored comments, write UTF-8 files and use `--content-file`.
