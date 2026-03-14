# Maestro

Maestro is a local-first orchestration runtime for agent-driven software work.

It combines a SQLite-backed tracker, an orchestrator that reads `WORKFLOW.md`, a private MCP daemon bridged by `maestro mcp`, and an HTTP server that serves the embedded dashboard plus JSON/WebSocket APIs.

Maestro stays local-first even when a project syncs issues from Linear. Provider-backed issues are synchronized into the local store, then supervised through the same local queue, runtime state, MCP tools, and dashboard surfaces as local kanban issues.

## Install

### npm

Preferred install on supported platforms:

```bash
npm install -g @olhapi/maestro
```

Update an existing install:

```bash
npm update -g @olhapi/maestro
```

The installed command name is still `maestro`.

Official npm builds currently cover:

| Platform | Arch | Notes |
| --- | --- | --- |
| macOS | arm64 | native package |
| macOS | x64 | native package |
| Linux | x64 | glibc only |
| Linux | arm64 | glibc only |
| Windows | x64 | native package |

Linux npm packages currently target glibc only. Alpine and other musl-based distros should build from source or use Docker.

### Docker

Published image:

```bash
docker pull ghcr.io/olhapi/maestro:latest
```

The image entrypoint is `maestro`. Its default command is `run --db /data/maestro.db`, so this starts the shared daemon with container defaults:

```bash
docker run --rm -v ./data:/data ghcr.io/olhapi/maestro:latest
```

To run against a mounted repo explicitly:

```bash
docker run --rm -v ./repo:/repo -v ./data:/data ghcr.io/olhapi/maestro:latest run --db /data/maestro.db /repo --port 8787
```

### Build From Source

For local development or unsupported platforms:

```bash
go build -o maestro ./cmd/maestro
```

Local contributor Docker build:

```bash
docker build -t maestro-local .
```

## Quick Start

### 1. Initialize a workflow file

```bash
maestro workflow init .
```

This writes a repo-local `WORKFLOW.md` with the default orchestration settings and prompt template.

### 2. Create a project and queue some work

```bash
maestro project create "My App" --repo /absolute/path/to/my-app --desc "Example project"
maestro issue create "Add login page" --project <project_id> --labels feature,ui
maestro issue create "Fix auth bug" --project <project_id> --priority 1 --labels bug
maestro issue move ISS-1 ready
```

Projects default to the local `kanban` provider. You can also register a project with limited Linear-backed sync by passing `--provider linear --provider-project-ref <slug>` and, if needed, `--provider-endpoint` or `--provider-assignee`.

### 3. Start the daemon

```bash
maestro run
```

When `--db` is omitted, Maestro uses `~/.maestro/maestro.db` by default. When `--port` is omitted, Maestro serves HTTP on `http://127.0.0.1:8787`.

Running `maestro run` without `repo_path` starts the shared daemon for the current database. It does not infer the repo from your shell working directory.

Issue images are stored next to the active database under `assets/images`. With the default database path, that means `~/.maestro/assets/images`. If you run with `--db /custom/path/maestro.db`, image assets move to `/custom/path/assets/images`.

The preview warning on `run` is intentional. Pass `--i-understand-that-this-will-be-running-without-the-usual-guardrails` only when unattended Codex execution is actually what you want.

### 4. Expose the tracker to MCP clients

Add the built or installed binary to your MCP client config:

```json
{
  "mcpServers": {
    "maestro": {
      "command": "/absolute/path/to/maestro",
      "args": ["mcp"]
    }
  }
}
```

`maestro mcp` is a stdio bridge into the live `maestro run` daemon for the same database. Start `maestro run` first, then let your MCP client invoke `maestro mcp`.

### 5. Open the dashboard or use live CLI helpers

By default, `maestro run` serves:

- the embedded dashboard on `/`
- the live observability API on `/api/v1/*`
- the dashboard application API on `/api/v1/app/*`
- the dashboard invalidation stream on `/api/v1/ws`

The shared issue composer in the embedded dashboard also supports browser speech-to-text for issue descriptions. In supported Chromium-based browsers it shows live interim text while you speak; elsewhere it degrades to a disabled control without changing the API surface.

Useful live helpers:

```bash
maestro status --dashboard --api-url http://127.0.0.1:8787
maestro sessions --api-url http://127.0.0.1:8787
maestro events --api-url http://127.0.0.1:8787 --limit 20
maestro runtime-series --api-url http://127.0.0.1:8787 --hours 24
maestro project start <project_id> --api-url http://127.0.0.1:8787
maestro project stop <project_id> --api-url http://127.0.0.1:8787
```

## MCP, Run, and Dashboard Model

`maestro run` is the long-lived process for a given database. It starts:

- the provider service and local SQLite-backed store
- the orchestrator and agent runner
- a private MCP daemon used by `maestro mcp`
- the public HTTP server when `--port` is set or left at its default

`maestro mcp` does not start a separate orchestration server. It discovers the live daemon for the same `--db` and bridges that session over stdio for MCP clients.

Operationally:

- start `maestro run` first
- point `maestro mcp` at the same `--db`
- use `--api-url` for CLI helpers and live control commands that talk to the daemon over HTTP

## Common Operator Commands

Queue inspection and filtering:

```bash
maestro issue list --state backlog --project <project_id>
maestro issue list --blocked --search auth --sort priority_asc
maestro board --project <project_id>
```

Issue updates:

```bash
maestro issue update ISS-1 --labels bug,urgent --priority 1
maestro issue update ISS-1 --branch codex/ISS-1 --pr-url https://example.com/pull/123
maestro issue blockers set ISS-1 ISS-2 ISS-3
maestro issue unblock ISS-1 ISS-2
```

Issue images:

```bash
maestro issue images add ISS-1 ./screenshots/failing-checkout.png
maestro issue images list ISS-1
maestro issue images remove ISS-1 <image_id>
```

Image attachments are local-only, including for Linear-backed issues. Maestro accepts PNG, JPEG, WEBP, and GIF files up to 10 MiB each and serves them back through the local HTTP API and dashboard.

Recurring automation:

```bash
maestro issue create "Sync GitHub ready-to-work" \
  --project <project_id> \
  --type recurring \
  --cron "*/15 * * * *" \
  --desc "Check the GitHub project for issues labeled ready-to-work and create corresponding Maestro issues when they do not already exist."
maestro issue list --type recurring --wide
maestro issue run-now ISS-42 --api-url http://127.0.0.1:8787
```

Recurring issues are Maestro-native issues with a cron schedule in the daemon host's local timezone. The orchestrator will enqueue at most one catch-up run after downtime, will not overlap active runs, and coalesces extra schedule hits into a single pending rerun.

Readiness checks:

```bash
maestro verify --repo /absolute/path/to/my-app
maestro doctor --repo /absolute/path/to/my-app
maestro spec-check --repo /absolute/path/to/my-app
```

## Workflow Basics

`WORKFLOW.md` is the repo-local source of truth for orchestration behavior. It covers:

- tracker settings
- workspace root
- hook commands and timeout
- agent concurrency, mode, retry limits, and dispatch behavior
- optional review and done phase prompts
- Codex command and sandbox settings
- the prompt template rendered for each issue

Fresh `maestro workflow init --defaults` output currently defaults to:

- `tracker.kind: kanban`
- `polling.interval_ms: 10000`
- `workspace.root: ./workspaces`
- `agent.max_concurrent_agents: 3`
- `agent.max_turns: 4`
- `agent.max_retry_backoff_ms: 60000`
- `agent.max_automatic_retries: 8`
- `agent.mode: app_server`
- `agent.dispatch_mode: parallel`
- `codex.command: codex app-server`
- `codex.expected_version: 0.111.0`
- `codex.approval_policy: never`
- `codex.thread_sandbox: workspace-write`
- `codex.turn_sandbox_policy.type: workspaceWrite`
- `codex.turn_sandbox_policy.networkAccess: true`

Supported prompt-template variables are:

- `{{ issue.identifier }}`
- `{{ issue.title }}`
- `{{ issue.description }}`
- `{{ issue.state }}`
- `{{ phase }}`
- `{{ attempt }}`

The checked-in [`WORKFLOW.md`](WORKFLOW.md) is this repository's own workflow example. It is not guaranteed to match fresh `workflow init` defaults exactly.

Missing-file behavior differs by command:

- `maestro workflow init` creates `WORKFLOW.md` explicitly
- `maestro run` bootstraps a missing file automatically
- `maestro verify` checks readiness and returns remediation guidance
- `maestro doctor` runs the same readiness checks with different presentation
- `maestro spec-check` is non-mutating and fails if the workflow file is missing or invalid

## More Documentation

- [`docs/OPERATIONS.md`](docs/OPERATIONS.md): runtime surfaces, HTTP endpoints, extension tools, logs, and operational details
- [`docs/E2E_REAL_CODEX.md`](docs/E2E_REAL_CODEX.md): end-to-end harness that runs the real Codex CLI against deterministic issues
- [`WORKFLOW.md`](WORKFLOW.md): the workflow configuration used by this repository

## Contributor Setup

If you are contributing from a repo checkout, run the root install once:

```bash
pnpm install
```

That single install:

- installs the repo-managed Git hooks through Husky
- bootstraps the shared `pnpm` workspace across `apps/frontend` and `apps/website`
- makes the root workspace scripts available for common local tasks

If you want shared cache hits across machines, Turborepo supports Remote Cache out of the box:

```bash
pnpm exec turbo login
pnpm exec turbo link
```

The CI workflow is already wired to use `TURBO_TEAM` and `TURBO_TOKEN` when those GitHub variables and secrets are configured.

Common contributor commands:

```bash
make build
make test
pnpm verify
pnpm run website:dev
pnpm run website:check
```

Repo-managed Git hooks stay targeted:

- staged Go changes run package-scoped Go tests
- staged frontend changes run frontend lint and tests
- staged website changes run Astro checks and website tests
- staged workspace and hook changes run the full `pnpm verify` suite
- `pnpm verify` runs the JS lint/test/check/smoke flow, npm packaging unit test, and Go build/test/coverage/race gates
- package-scoped root commands such as `pnpm run frontend:test` and `pnpm run website:build` now go through `turbo --filter=...` so they benefit from task caching too
- `pre-push` now runs the same full `pnpm verify` command

## License

MIT
