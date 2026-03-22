# Maestro

Maestro is a local-first orchestration runtime for agent-driven software work.

Website: [maestro.olhapi.com](https://maestro.olhapi.com/)
Repository: [github.com/olhapi/maestro](https://github.com/olhapi/maestro)
Docs: [maestro.olhapi.com/docs](https://maestro.olhapi.com/docs)

This project is inspired by [openai/symphony](https://github.com/openai/symphony).

It combines a SQLite-backed tracker, an orchestrator that reads `WORKFLOW.md`, a private MCP daemon bridged by `maestro mcp`, and an HTTP server that serves the embedded dashboard plus JSON/WebSocket APIs.

Maestro stays local-first. External work is translated into Maestro projects and issues through the CLI, the embedded dashboard, or MCP prompts, then supervised through the same local queue, runtime state, MCP tools, and dashboard surfaces.

## Docs Website

The docs site is organized around the same operator flow the product uses:

- [Install](https://maestro.olhapi.com/docs/install)
- [Quickstart](https://maestro.olhapi.com/docs/quickstart)
- [Architecture](https://maestro.olhapi.com/docs/architecture)
- [Control center](https://maestro.olhapi.com/docs/control-center)
- [Workflow config](https://maestro.olhapi.com/docs/workflow-config)
- [Operations and observability](https://maestro.olhapi.com/docs/operations)
- [CLI reference](https://maestro.olhapi.com/docs/cli-reference)
- [Real Codex E2E harness](https://maestro.olhapi.com/docs/advanced/e2e-harness)

## Install

### npm

Current public npm install on supported platforms:

```bash
npm install -g @olhapi/maestro
```

Install the newest prerelease instead:

```bash
npm install -g @olhapi/maestro@next
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
maestro project create "My App" --repo /absolute/path/to/my-app --desc "Repo-wide Codex guidance: use pnpm, keep changes scoped, and run focused validation for touched packages."
maestro issue create "Add login page" --project <project_id> --labels feature,ui
maestro issue create "Fix auth bug" --project <project_id> --priority 1 --labels bug
maestro issue move ISS-1 ready
```

Projects use the local tracker only.

If you need to import work from another system, ask your MCP-capable agent to translate it into Maestro records. For example:

```text
Take my Jira issues from the "make a react todo app" epic and create the corresponding Maestro project, epics, and issues.
Use the current repo as the project repo path, keep the issues local, and mark the imported work ready.
```

Project descriptions are not just dashboard notes. Maestro passes `project.description` into every implementation, review, and done prompt by default, so use it for standing requirements, conventions, and validation expectations Codex should keep in mind for every issue.

### 3. Start the daemon

```bash
maestro run
```

When `--db` is omitted, Maestro uses `~/.maestro/maestro.db` by default. When `--port` is omitted, Maestro serves HTTP on `http://127.0.0.1:8787`.

Running `maestro run` without `repo_path` starts the shared daemon for the current database. It does not infer the repo from your shell working directory.

Issue images are stored next to the active database under `assets/images`. With the default database path, that means `~/.maestro/assets/images`. If you run with `--db /custom/path/maestro.db`, image assets move to `/custom/path/assets/images`.

The preview warning on `run` is intentional. Pass `--i-understand-that-this-will-be-running-without-the-usual-guardrails` only when unattended Codex execution is actually what you want.

### 4. Install the Maestro skill bundle and add the MCP server to your coding agent

Install the bundled Maestro skill first so Codex and Claude Code can load the repo-specific guidance automatically:

```bash
maestro install --skills
```

That writes the skill to `~/.agents/skills/maestro` for Codex and `~/.claude/skills/maestro` for Claude Code.

Then use the setup path that matches your coding agent:

Codex:

```bash
codex mcp add maestro -- maestro mcp
```

Claude Code:

```bash
claude mcp add maestro -- maestro mcp
claude mcp add --scope project maestro -- maestro mcp
```

Other MCP-capable agents:

```json
{
  "mcpServers": {
    "maestro": {
      "command": "maestro",
      "args": ["mcp"]
    }
  }
}
```

If you built Maestro from source and did not add it to your `PATH`, replace `maestro` with the absolute path to the binary.

`maestro mcp` is a stdio bridge into the live `maestro run` daemon for the same database. Start `maestro run` first, then let your coding agent invoke `maestro mcp`.

Paginated MCP list tools return a `pagination` object when more results remain. When `pagination.has_more` is true, call the exact `pagination.next_request` payload to fetch the next batch instead of guessing the next offset by hand.

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

- the local issue service and SQLite-backed store
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

Image attachments are local-only for every issue. Maestro accepts PNG, JPEG, WEBP, and GIF files up to 10 MiB each and serves them back through the local HTTP API and dashboard.

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
- `codex.expected_version: 0.116.0`
- `codex.approval_policy: never`
- `codex.initial_collaboration_mode: default` for fresh `app_server` threads
- runtime permission profiles now live in the DB per project/issue instead of `WORKFLOW.md`

`initial_collaboration_mode: default` keeps unattended runs execution-first for a fresh `app_server` thread. Use `plan` only when you explicitly want a plan-gated startup mode. Interactive approvals and `requestUserInput` prompts still depend on using a non-`never` approval policy, and those prompts are queued through the dashboard's global interrupt panel. Resumed threads and `stdio` runs do not use that startup-mode path.

Supported prompt-template variables are:

- `{{ issue.identifier }}`
- `{{ issue.title }}`
- `{{ issue.description }}`
- `{{ issue.state }}`
- `{{ project.id }}`
- `{{ project.name }}`
- `{{ project.description }}`
- `{{ phase }}`
- `{{ attempt }}`

When a project has a description, Maestro's default implementation, review, and done prompts include it automatically under a `Project context:` section. Custom workflows can place `{{ project.description }}` wherever they want.
The default done prompt now focuses on merge-back, PR readiness, and blocker reporting instead of asking for a preview artifact.

The checked-in [`WORKFLOW.md`](WORKFLOW.md) is this repository's own workflow example. It is not guaranteed to match fresh `workflow init` defaults exactly.

Missing-file behavior differs by command:

- `maestro workflow init` creates `WORKFLOW.md` explicitly
- `maestro run` bootstraps a missing file automatically
- `maestro verify` checks readiness and returns remediation guidance
- `maestro doctor` runs the same readiness checks with different presentation
- `maestro spec-check` is non-mutating and fails if the workflow file is missing or invalid

## More Documentation

- [`docs/OPERATIONS.md`](docs/OPERATIONS.md): runtime surfaces, HTTP endpoints, extension tools, logs, and operational details
- [`docs/NPM_RELEASE.md`](docs/NPM_RELEASE.md): first npm prerelease bootstrap and the trusted-publishing release flow
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
- `pnpm run verify:pre-push` adds current-host npm packaging smoke, the shared retry stress test, and the full retry-safety harness on top of `pnpm verify`
- package-scoped root commands such as `pnpm run frontend:test` and `pnpm run website:build` now go through `turbo --filter=...` so they benefit from task caching too
- `pre-push` now runs `pnpm run verify:pre-push`, leaving GitHub Actions with the cross-platform packaging matrix and registry smoke coverage

## License

MIT
