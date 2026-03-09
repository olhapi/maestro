# Maestro

Maestro is a Go implementation of the Maestro orchestration loop with a local SQLite-backed Kanban tracker instead of Linear.

It gives you three durable surfaces:

- a local tracker for projects and issues
- an MCP server so Codex or ChatGPT can manage that tracker
- an orchestrator that reads `WORKFLOW.md`, picks up `ready` issues, and dispatches them to an agent

## Build

```bash
go build -o maestro ./cmd/maestro
```

Optional Docker build:

```bash
docker build -t maestro .
```

## Install

Preferred global install for macOS arm64:

```bash
npm install -g @olhapi/maestro
```

Update an existing global install:

```bash
npm update -g @olhapi/maestro
```

The npm package currently supports macOS arm64 only. The installed command name is still `maestro`.

For local development or unsupported platforms, build from source with Go:

```bash
go build -o maestro ./cmd/maestro
```

For local contributor tooling, install the repo-root dev dependency once:

```bash
npm install
```

This installs the repo-managed Git hooks with Husky and bootstraps the frontend dev dependencies used by the hooks.

## Quick Start

### 1. Initialize a workflow file

```bash
maestro workflow init .
```

This writes a repo-local `WORKFLOW.md` with the default Kanban workflow, Codex command, and prompt template.

### 2. Create a project and some issues

```bash
maestro project create "My App" --repo /absolute/path/to/my-app --desc "Example project"
maestro issue create "Add login page" --project <project_id> --labels feature,ui
maestro issue create "Fix auth bug" --priority 1 --labels bug
maestro issue list
maestro board
```

Move an issue into the ready queue when you want the orchestrator to pick it up:

```bash
maestro issue move ISS-1 ready
```

### 3. Expose the tracker to MCP clients

Add the built binary to your MCP client config:

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

The MCP server exposes project, issue, board, and blocker-management tools backed by the local Kanban store.

For a shared multi-project setup, point both `maestro mcp` and `maestro run` at the same central DB.

### 4. Start the orchestrator

```bash
maestro run /path/to/repo
```

When `--db` is omitted, Maestro uses the current Maestro workspace's `.maestro/maestro.db` by default.

The orchestrator:

1. loads `WORKFLOW.md`
2. polls for issues in the `ready` state
3. creates per-issue workspaces
4. dispatches work to the configured agent command
5. tracks retries, logs, and runtime status

`run` prints a preview warning because the default workflow can launch Codex without extra guardrails. Pass `--i-understand-that-this-will-be-running-without-the-usual-guardrails` only when that is intentional for your environment.

For local UI development against another repo:

```bash
REPO_PATH=/path/to/repo make dev
```

## Core Commands

```bash
# Projects
maestro project create <name> --repo <repo_path> [--desc <description>] [--workflow <workflow_path>]
maestro project list
maestro project delete <id>

# Issues
maestro issue create <title> [--desc <description>] [--project <id>] [--priority <n>] [--labels <label1,label2>]
maestro issue list [--state <state>] [--project <id>]
maestro issue show <identifier>
maestro issue move <identifier> <state>
maestro issue update <identifier> [--title <title>] [--desc <description>] [--pr <number> <url>]
maestro issue delete <identifier>
maestro issue block <identifier> <blocker_identifier...>

# Orchestration
maestro --log-level <debug|info|warn|error> run [repo_path] [--workflow <path>] [--extensions <json-file>] [--db <path>] [--logs-root <path>] [--log-max-bytes <n>] [--log-max-files <n>] [--port <port>]
maestro --log-level <debug|info|warn|error> mcp [--db <path>] [--extensions <json-file>]
maestro --log-level <debug|info|warn|error> status [--json]
maestro --log-level <debug|info|warn|error> status --dashboard [--dashboard-url <url>]
maestro --log-level <debug|info|warn|error> verify [--repo <path>] [--db <path>] [--json]
maestro --log-level <debug|info|warn|error> spec-check [--repo <path>] [--json]
maestro --log-level <debug|info|warn|error> workflow init [repo_path]
```

`--log-level` defaults to `info`. Use `debug` to include raw app-server stream output and session churn in the structured logs.

## Git Hooks

Repo-managed Git hooks are installed by running `npm install` at the repo root.

- `pre-commit` stays fast and only runs checks relevant to staged files.
- staged Go changes run `go test` for the impacted package directories under `./cmd`, `./internal`, and `./pkg`.
- staged changes to `go.mod`, `go.sum`, `Makefile`, or `scripts/check_coverage.sh` run `make test`.
- staged frontend changes run `npm --prefix frontend run lint` and `npm --prefix frontend run test:ci`.
- `pre-push` runs `make test-cover`, `make test-race`, `npm --prefix frontend run lint`, and `npm --prefix frontend run test:ci`.

Use standard Git `--no-verify` only when you intentionally need to bypass hooks.

## Issue States

| State | Meaning |
| --- | --- |
| `backlog` | Not yet prioritized |
| `ready` | Available for the orchestrator |
| `in_progress` | Actively being worked |
| `in_review` | Waiting for human review |
| `done` | Completed |
| `cancelled` | Closed without completion |

## Workflow Configuration

`WORKFLOW.md` is the repo-local source of truth for:

- tracker settings
- workspace root
- hook commands and timeout
- agent concurrency and mode
- optional review/done phase prompts
- Codex command and sandbox settings
- the prompt template rendered for each issue

The current canonical example lives in [`WORKFLOW.md`](WORKFLOW.md). Supported template variables are:

- `{{ issue.identifier }}`
- `{{ issue.title }}`
- `{{ issue.description }}`
- `{{ issue.state }}`
- `{{ phase }}`
- `{{ attempt }}`

Bootstrap behavior matters:

- `maestro workflow init` creates the file explicitly
- `maestro run` bootstraps a missing file automatically
- `maestro verify` also bootstraps a missing file
- `maestro spec-check` does not mutate the repo and fails if the workflow file is missing or invalid

## Operations and Advanced Usage

- [`docs/OPERATIONS.md`](docs/OPERATIONS.md) covers observability endpoints, `verify` and `spec-check`, extension tool files, logs, and current non-goals.
- [`docs/E2E_REAL_CODEX.md`](docs/E2E_REAL_CODEX.md) documents the end-to-end harness that runs the real Codex CLI against simple deterministic issues.

## Architecture

```text
Codex or ChatGPT (via MCP)
        |
        v
MCP server <-> SQLite Kanban store
        |
        v
Orchestrator -> workspace lifecycle -> agent runner
        ^
        |
   WORKFLOW.md
```

## Docker

```bash
docker build -t maestro .
docker run --rm -i -v ./data:/data maestro mcp --db /data/maestro.db
docker run --rm -v ./repo:/repo -v ./data:/data maestro run /repo --db /data/maestro.db
```

## License

MIT
