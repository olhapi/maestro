# Symphony-Go

Symphony-Go is a Go implementation of the Symphony orchestration loop with a local SQLite-backed Kanban tracker instead of Linear.

It gives you three durable surfaces:

- a local tracker for projects and issues
- an MCP server so Codex or ChatGPT can manage that tracker
- an orchestrator that reads `WORKFLOW.md`, picks up `ready` issues, and dispatches them to an agent

## Build

```bash
go build -o symphony ./cmd/symphony
```

Optional Docker build:

```bash
docker build -t symphony .
```

## Install

Preferred global install for macOS arm64:

```bash
npm install -g @olhapi/symphony-go
```

Update an existing global install:

```bash
npm update -g @olhapi/symphony-go
```

The npm package currently supports macOS arm64 only. The installed command name is still `symphony`.

For local development or unsupported platforms, build from source with Go:

```bash
go build -o symphony ./cmd/symphony
```

## Quick Start

### 1. Initialize a workflow file

```bash
symphony workflow init .
```

This writes a repo-local `WORKFLOW.md` with the default Kanban workflow, Codex command, and prompt template.

### 2. Create a project and some issues

```bash
symphony project create "My App" --desc "Example project"
symphony issue create "Add login page" --project <project_id> --labels feature,ui
symphony issue create "Fix auth bug" --priority 1 --labels bug
symphony issue list
symphony board
```

Move an issue into the ready queue when you want the orchestrator to pick it up:

```bash
symphony issue move ISS-1 ready
```

### 3. Expose the tracker to MCP clients

Add the built binary to your MCP client config:

```json
{
  "mcpServers": {
    "symphony": {
      "command": "/absolute/path/to/symphony",
      "args": ["mcp"]
    }
  }
}
```

The MCP server exposes project, issue, board, and blocker-management tools backed by the local Kanban store.

### 4. Start the orchestrator

```bash
symphony run /path/to/repo
```

The orchestrator:

1. loads `WORKFLOW.md`
2. polls for issues in the `ready` state
3. creates per-issue workspaces
4. dispatches work to the configured agent command
5. tracks retries, logs, and runtime status

`run` prints a preview warning because the default workflow can launch Codex without extra guardrails. Pass `--i-understand-that-this-will-be-running-without-the-usual-guardrails` only when that is intentional for your environment.

## Core Commands

```bash
# Projects
symphony project create <name> [--desc <description>]
symphony project list
symphony project delete <id>

# Issues
symphony issue create <title> [--desc <description>] [--project <id>] [--priority <n>] [--labels <label1,label2>]
symphony issue list [--state <state>] [--project <id>]
symphony issue show <identifier>
symphony issue move <identifier> <state>
symphony issue update <identifier> [--title <title>] [--desc <description>] [--pr <number> <url>]
symphony issue delete <identifier>
symphony issue block <identifier> <blocker_identifier...>

# Orchestration
symphony run [repo_path] [--workflow <path>] [--extensions <json-file>] [--db <path>] [--logs-root <path>] [--log-max-bytes <n>] [--log-max-files <n>] [--port <port>]
symphony mcp [--db <path>] [--extensions <json-file>]
symphony status [--json]
symphony status --dashboard [--dashboard-url <url>]
symphony verify [--repo <path>] [--db <path>] [--json]
symphony spec-check [--repo <path>] [--json]
symphony workflow init [repo_path]
```

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
- Codex command and sandbox settings
- the prompt template rendered for each issue

The current canonical example lives in [`WORKFLOW.md`](WORKFLOW.md). Supported template variables are:

- `{{ issue.identifier }}`
- `{{ issue.title }}`
- `{{ issue.description }}`
- `{{ issue.state }}`
- `{{ attempt }}`

Bootstrap behavior matters:

- `symphony workflow init` creates the file explicitly
- `symphony run` bootstraps a missing file automatically
- `symphony verify` also bootstraps a missing file
- `symphony spec-check` does not mutate the repo and fails if the workflow file is missing or invalid

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
docker build -t symphony .
docker run --rm -i -v ./data:/data symphony mcp --db /data/symphony.db
docker run --rm -v ./repo:/repo -v ./data:/data symphony run /repo --db /data/symphony.db
```

## License

MIT
