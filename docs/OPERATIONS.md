# Operations and Reference

This document collects the durable operational details for Symphony-Go: runtime endpoints, validation commands, extension tool files, logging, and deliberate non-goals.

## Observability

Start the orchestrator with an HTTP port to expose runtime state:

```bash
./symphony run /path/to/repo --port 8787
```

Available endpoints:

- `GET /health`: process health and timestamp
- `GET /api/v1/state`: orchestrator status payload
- `GET /api/v1/<issue_identifier>`: single running or retrying issue payload
- `GET /api/v1/sessions`: all live app-server sessions
- `GET /api/v1/sessions?issue=ISS-1`: single session lookup by issue identifier
- `GET /api/v1/events?since=0&limit=100`: in-memory event feed
- `POST /api/v1/refresh`: request a refresh event
- `GET /api/v1/dashboard`: combined snapshot of state, sessions, and recent events

`symphony status --dashboard` is a local CLI rendering helper. It is not the same thing as the live HTTP observability API and should not be treated as a remote status feed.

## Workflow Bootstrap and Checks

`WORKFLOW.md` is required for orchestration. The commands treat it differently:

- `symphony workflow init [repo_path]` creates the file explicitly.
- `symphony run [repo_path]` bootstraps a missing file automatically before starting.
- `symphony verify [--repo <path>] [--db <path>] [--json]` bootstraps a missing file, verifies it loads, and checks database initialization.
- `symphony spec-check [--repo <path>] [--json]` is non-mutating and fails if the workflow file is missing or invalid.

`verify` is intended for local readiness checks. `spec-check` is intended for lightweight conformance checks against the repo layout and workflow surface.

## Extensions File

Both `symphony run` and `symphony mcp` can load the same JSON file via `--extensions`.

Each extension entry supports:

- `name`: required unique tool name
- `description`: required tool description
- `command`: required shell command to execute
- `timeout_sec`: optional command timeout, default `15`
- `allowed`: optional boolean policy gate
- `working_dir`: optional working directory for the command
- `require_args`: optional boolean that rejects empty `args`
- `deny_env_passthrough`: optional boolean that restricts the environment to `SYMPHONY_*`

Example:

```json
[
  {
    "name": "echo_issue",
    "description": "Print the args object for debugging",
    "command": "jq -r . <<< \"$SYMPHONY_ARGS_JSON\"",
    "timeout_sec": 10,
    "require_args": true
  }
]
```

At runtime:

- the tool name is passed as `SYMPHONY_TOOL_NAME`
- the JSON arguments object is passed as `SYMPHONY_ARGS_JSON`

These commands execute on the local machine. Review them with the same care you would apply to any local shell automation.

## Logs

Write structured JSON logs to both stdout and a rotating file sink:

```bash
./symphony run /path/to/repo --logs-root ./log
./symphony run /path/to/repo --logs-root ./log --log-max-bytes 1048576 --log-max-files 5
```

Behavior:

- the main log file is `symphony.log`
- rotation is size-based
- `--log-max-bytes` controls the rotation threshold
- `--log-max-files` controls how many rotated files are retained

## Deliberate Scope

Current non-goals and differences from the original Symphony project:

- the tracker is local Kanban backed by SQLite, not Linear
- the observability surface is HTTP JSON, not a Phoenix dashboard or pubsub transport
- `status --dashboard` is a local snapshot formatter, not live orchestrator introspection
- extension tools are intentionally simple shell commands, not a separate plugin runtime
