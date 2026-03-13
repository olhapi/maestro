# Operations and Reference

This document collects the durable operational details for Maestro: runtime surfaces, HTTP endpoints, validation commands, extension tool files, logging, and current scope boundaries.

## Runtime surfaces

`maestro run` is the long-lived process for a given database. In code, it owns five connected layers:

- the SQLite-backed local store and runtime persistence
- the provider service that reads local `kanban` projects and syncs limited `linear` projects into the local store
- the orchestrator and agent runner that turn queued issues into per-issue workspaces and Codex runs
- a private loopback-only MCP daemon used by `maestro mcp`
- an optional public HTTP server that serves the embedded dashboard UI plus JSON and WebSocket APIs

`WORKFLOW.md` still governs orchestration behavior. Its `tracker.kind` remains `kanban`, even when a project itself is configured to sync issues from a provider such as Linear.

## HTTP surfaces

Start the daemon with an HTTP port:

```bash
./maestro run /path/to/repo --port 8787
```

If `--db` is omitted, `run` uses `~/.maestro/maestro.db`.

The public server exposes two related API surfaces plus the embedded dashboard:

### Live observability API

These endpoints power CLI helpers such as `status --dashboard`, `sessions`, and `events`:

- `GET /health`: process health and timestamp
- `GET /api/v1/state`: live orchestrator status payload
- `GET /api/v1/<issue_identifier>`: single issue status payload from the live runtime view
- `GET /api/v1/sessions`: all live app-server sessions
- `GET /api/v1/sessions?issue=ISS-1`: single session lookup by issue identifier
- `GET /api/v1/events?since=0&limit=100`: live in-memory event feed
- `POST /api/v1/refresh`: request a refresh event
- `GET /api/v1/dashboard`: combined live snapshot of state, sessions, and recent events

`maestro status --dashboard` is a CLI formatter over this live API. It is not the same thing as the richer dashboard application API.

### Dashboard application API

These endpoints back the embedded UI and expose the broader local control plane:

- `GET /api/v1/app/bootstrap`
- `GET|POST /api/v1/app/projects`
- `GET|PATCH|DELETE|POST /api/v1/app/projects/:id` and project actions such as `/run` and `/stop`
- `GET|POST /api/v1/app/epics`
- `GET|PATCH|DELETE /api/v1/app/epics/:id`
- `GET|POST /api/v1/app/issues`
- `GET|PATCH|DELETE /api/v1/app/issues/:identifier`
- `GET /api/v1/app/issues/:identifier/execution`
- `POST /api/v1/app/issues/:identifier/state`
- `POST /api/v1/app/issues/:identifier/blockers`
- `POST /api/v1/app/issues/:identifier/commands`
- `POST /api/v1/app/issues/:identifier/retry`
- `POST /api/v1/app/issues/:identifier/run-now`
- `GET /api/v1/app/runtime/events`
- `GET /api/v1/app/runtime/series`
- `GET /api/v1/app/sessions`

### WebSocket invalidation

- `GET /api/v1/ws`: dashboard invalidation stream used by the embedded UI to refetch live data

## CLI and API usage

Commands that talk to a running daemon over HTTP require `--api-url`:

- `maestro status --dashboard --api-url http://127.0.0.1:8787`
- `maestro sessions --api-url http://127.0.0.1:8787`
- `maestro events --api-url http://127.0.0.1:8787`
- `maestro runtime-series --api-url http://127.0.0.1:8787`
- `maestro project start PRJ-1 --api-url http://127.0.0.1:8787`
- `maestro project stop PRJ-1 --api-url http://127.0.0.1:8787`
- `maestro issue execution ISS-1 --api-url http://127.0.0.1:8787`
- `maestro issue retry ISS-1 --api-url http://127.0.0.1:8787`
- `maestro issue run-now ISS-1 --api-url http://127.0.0.1:8787`

The embedded dashboard does not need `--api-url` because it is served by the same HTTP server it talks to.

## Recurring issues

Recurring issues are first-class Maestro issues with `issue_type=recurring` plus recurrence metadata:

- `cron`
- `enabled`
- `next_run_at`
- `last_enqueued_at`
- `pending_rerun`

Cron expressions use the daemon host's local timezone and standard 5-field minute granularity.

Example:

```bash
maestro issue create "Sync GitHub ready-to-work" \
  --project <project_id> \
  --type recurring \
  --cron "*/15 * * * *" \
  --desc "Check GitHub project issues with the ready-to-work label and create matching Maestro issues when missing."
```

Operational behavior:

- recurring issues reuse the same orchestration flow, retries, MCP tools, and dashboard surfaces as any other issue
- the scheduler enqueues at most one catch-up run if the daemon was down
- active recurring work never overlaps; extra schedule hits are coalesced into one pending rerun
- `cancelled` or `enabled=false` suppresses future scheduled runs without deleting the issue
- `run-now` triggers an immediate execution for an idle recurring issue, or records a pending rerun when the issue is already occupied

## MCP attach model

`maestro run` is the only daemon for a given database. It starts a private MCP endpoint bound to loopback and records the daemon metadata for that database.

`maestro mcp` does not start a separate server. It discovers the live daemon for the same `--db`, authenticates to the private MCP endpoint, and bridges that session over stdio for MCP clients.

Operationally:

- start `maestro run` first
- point `maestro mcp` at the same `--db`
- expect an error if no live daemon exists for that store

## Provider model

Projects can use one of two provider kinds:

- `kanban`: fully local project and issue lifecycle backed by the SQLite store
- `linear`: limited project-backed issue sync and issue mutation through Linear's GraphQL API

Current Linear support is intentionally limited:

- project provider support is available
- issue sync and issue state updates are supported
- assignee filtering is supported through project provider config
- epics are not supported
- some create and update flows reject labels, blockers, or project reassignment

Regardless of provider, Maestro supervises work through the same local store, orchestration loop, runtime events, and dashboard surfaces.

## Workflow bootstrap and checks

`WORKFLOW.md` is required for orchestration. The commands treat it differently:

- `maestro workflow init [repo_path]` creates the file explicitly
- `maestro run [repo_path]` bootstraps a missing file automatically before starting
- `maestro verify [--repo <path>] [--db <path>] [--json]` checks readiness and returns remediation guidance; it does not create the file
- `maestro doctor [--repo <path>] [--db <path>] [--json]` runs the same readiness checks with a different presentation
- `maestro spec-check [--repo <path>] [--json]` is non-mutating and fails if the workflow file is missing or invalid

`verify` and `doctor` are readiness checks. `spec-check` is the lightweight conformance check.

## Extensions file

Only `maestro run` loads extension tools via `--extensions`.

`maestro mcp` inherits whatever tool set the live daemon started with. It rejects `--extensions` so the stdio bridge cannot drift away from the daemon it is attached to.

Each extension entry supports:

- `name`: required unique tool name
- `description`: required tool description
- `command`: required shell command to execute
- `timeout_sec`: optional command timeout, default `15`
- `allowed`: optional boolean policy gate
- `working_dir`: optional working directory for the command
- `require_args`: optional boolean that rejects empty `args`
- `deny_env_passthrough`: optional boolean that restricts the environment to `MAESTRO_*`

Example:

```json
[
  {
    "name": "echo_issue",
    "description": "Print the args object for debugging",
    "command": "jq -r . <<< \"$MAESTRO_ARGS_JSON\"",
    "timeout_sec": 10,
    "require_args": true
  }
]
```

At runtime:

- the tool name is passed as `MAESTRO_TOOL_NAME`
- the JSON arguments object is passed as `MAESTRO_ARGS_JSON`

These commands execute on the local machine. Review them with the same care you would apply to any local shell automation.

## Logs

Write structured JSON logs to both stdout and a rotating file sink:

```bash
./maestro --log-level info run /path/to/repo --logs-root ./log
./maestro --log-level debug run /path/to/repo --logs-root ./log --log-max-bytes 1048576 --log-max-files 5
```

Behavior:

- the main log file is `maestro.log`
- rotation is size-based
- `--log-level` is global and applies to every CLI command
- `debug` includes raw app-server stream output
- `info` keeps logs focused on lifecycle and status transitions
- `--log-max-bytes` controls the rotation threshold
- `--log-max-files` controls how many rotated files are retained

## Deliberate scope

Current scope boundaries and differences from the original Maestro project:

- Maestro stays local-first even when a project syncs issues from Linear
- the public observability surface is HTTP plus an embedded dashboard, not Phoenix-style pubsub
- `status --dashboard` is a live CLI formatter, not a second control plane
- extension tools remain local shell commands, not a separate plugin runtime
