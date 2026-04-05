# Operations and Reference

This document collects the durable operational details for Maestro: runtime surfaces, HTTP endpoints, validation commands, extension tool files, logging, and current scope boundaries.

## Runtime surfaces

`maestro run` is the long-lived process for a given database. In code, it owns five connected layers:

- the SQLite-backed local store and runtime persistence
- the local project and issue service that keeps Maestro data in the SQLite store
- the orchestrator and agent runner that turn queued issues into per-issue workspaces and runtime sessions
- a private loopback-only MCP daemon used by `maestro mcp`
- an optional public HTTP server that serves the embedded dashboard UI plus JSON and WebSocket APIs

The standard Go build/test path uses a pure-Go SQLite driver now. Only the
standalone shell scripts under `scripts/` still shell out to the `sqlite3`
CLI when you run them directly.

`WORKFLOW.md` still governs orchestration behavior. Its `tracker.kind` remains `kanban`, which is the local tracker used for every project.

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
- `GET /api/v1/sessions`: all live sessions
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

The shared issue composer in the embedded dashboard also supports browser-native speech dictation for issue descriptions. It uses the browser's built-in speech recognition in supported Chromium-based browsers, shows live interim text while you speak, and falls back to a disabled control elsewhere. This is a UI-only feature and does not add new HTTP endpoints.

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

`codex.approval_policy: never` only disables Maestro-managed app-server approvals. It does not override a client's own trust gate for external MCP tools, so Codex can still prompt on `maestro mcp` calls when the local MCP configuration or advertised tool metadata requires review.

Operationally:

- start `maestro run` first
- point `maestro mcp` at the same `--db`
- expect an error if no live daemon exists for that store

## Provider model

Projects use the local tracker only:

- project records live in the SQLite store
- issues, epics, blockers, comments, and attachments are managed locally
- MCP prompts, CLI commands, or dashboard actions can translate external work into local Maestro records before execution

If you are importing another tracker, create local Maestro projects and issues first, then let the orchestrator supervise those local records.

## Claude Runtime Runbook

The supported Claude runtime entry in `WORKFLOW.md` is:

- `provider: claude`
- `transport: stdio`
- `command: claude`
- `approval_policy: never`

The dashboard and API surface the runtime identity as `runtime_name`, `runtime_provider`, `runtime_transport`, `runtime_auth_source`, `pending_interaction_state`, and `stop_reason`. Use those fields to distinguish Claude and Codex runs. `session_source` only tells you whether the row came from a live snapshot or a persisted snapshot.

For failures, use `failure_class` plus `current_error` together. Claude guardrail failures that are out of contract, such as unsupported `local_image` delivery, surface as `failure_class=unsupported_runtime_capability` while `current_error` keeps the specific remediation text.

### Ambient Auth

`maestro verify` reports Claude auth readiness through three checks:

- `claude_auth_source`: the effective auth source
- `claude_auth_source_detail`: provider-specific detail when available
- `claude_auth_source_status`: `ok`, `warn`, or `fail`

Common values mean:

- `OAuth` means Claude Code is logged in with an interactive session.
- `cloud provider` means Claude is using a managed cloud provider such as Bedrock, Vertex, or Foundry. The specific provider appears in `claude_auth_source_detail` when available.
- `ANTHROPIC_AUTH_TOKEN` means the runtime is using a token-based environment credential.
- `warn` means Maestro found a usable source but wants an operator to confirm it.
- `fail` means the runtime is not ready for execution.

### Preflight

Before starting or resuming Claude work, run `maestro verify` in the repo root and confirm:

- `claude_version_status`
- `claude_auth_source_status`
- `claude_session_status`
- `claude_session_bare_mode`
- `claude_session_additional_directories`
- `runtime_claude`

If `claude_session_bare_mode` fails, remove `--bare`, `--permission-mode auto`, `--permission-mode bypassPermissions`, or the corresponding config entries before retrying.

If `claude_session_additional_directories` fails, remove `additionalDirectories` or `--add-dir` so the session stays scoped to the Maestro workspace.

### Stale Workspace Cleanup

If the dashboard or API reports `workspace_recovery.status = required`, treat the workspace as dirty rather than retrying blindly. Check the worktree for in-progress Git operations, clear stale build artifacts only after confirming nothing is still running, and retry once the workspace is clean. `git status` and the recovery message should be the source of truth.

### Supported Session Invariants

Supported session records should always let operators answer:

- which issue and issue identifier the run belongs to
- which runtime executed it
- which transport and auth source were effective
- whether the run is waiting on approval, user input, elicitation, or an alert
- why the session stopped

The dashboard exposes that information through `runtime_name`, `runtime_provider`, `runtime_transport`, `runtime_auth_source`, `pending_interaction_state`, and `stop_reason`.

## Workflow bootstrap and checks

`WORKFLOW.md` is required for orchestration. The commands treat it differently:

- `maestro workflow init [repo_path]` creates the file explicitly
- `maestro run [repo_path]` bootstraps a missing file automatically before starting
- `maestro verify [--repo <path>] [--db <path>] [--json]` checks readiness and returns remediation guidance; it does not create the file
- `maestro doctor [--repo <path>] [--db <path>] [--json]` runs the same readiness checks with a different presentation
- `maestro spec-check [--repo <path>] [--json]` is non-mutating and fails if the workflow file is missing or invalid

`verify` and `doctor` are readiness checks. `spec-check` is the lightweight conformance check.

When Maestro creates a brand-new issue workspace, it refreshes the repository's `origin` refs first if that remote exists, then bases the new worktree on the refreshed remote-tracking default branch when it can and falls back to the resolved local branch when it cannot. Local-only repos keep the local-ref fallback, and existing active workspaces are reused without refreshing or recreating them.

## Extensions file

Only `maestro run` loads extension tools via `--extensions`.

`maestro mcp` inherits whatever tool set the live daemon started with. It rejects `--extensions` so the stdio bridge cannot drift away from the daemon it is attached to.

Each extension entry supports:

- `name`: required unique tool name
- `description`: required tool description
- `command`: required shell command to execute
- `annotations`: optional MCP tool metadata object
- `annotations.title`: optional human-readable title
- `annotations.read_only_hint`: optional boolean read-only hint
- `annotations.destructive_hint`: optional boolean destructive hint
- `annotations.idempotent_hint`: optional boolean idempotent hint
- `annotations.open_world_hint`: optional boolean open-world hint
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
    "annotations": {
      "read_only_hint": true,
      "destructive_hint": false,
      "idempotent_hint": true,
      "open_world_hint": false
    },
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
- `debug` includes raw runtime stream output
- `info` keeps logs focused on lifecycle and status transitions
- `--log-max-bytes` controls the rotation threshold
- `--log-max-files` controls how many rotated files are retained

## Deliberate scope

Current scope boundaries and differences from the original Maestro project:

- Maestro stays local-first and does not sync a remote provider into the live store
- the public observability surface is HTTP plus an embedded dashboard, not Phoenix-style pubsub
- `status --dashboard` is a live CLI formatter, not a second control plane
- extension tools remain local shell commands, not a separate plugin runtime
