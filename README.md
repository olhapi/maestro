# Symphony-Go

A Go implementation of [OpenAI's Symphony](https://github.com/openai/symphony) - agent orchestration with a local Kanban board.

**Key difference:** Instead of Linear, this uses a local SQLite-backed Kanban board with MCP support for Codex/ChatGPT integration.

## Features

- 🗂️ **Local Kanban Board** - Projects, Epics, Issues with full state management
- 🔌 **MCP Server** - Let Codex or ChatGPT manage your backlog
- 🤖 **Agent Orchestration** - Poll for work, dispatch to coding agents
- 📝 **WORKFLOW.md** - Version your agent prompts with your code
- 🔒 **Single Binary** - No dependencies, just SQLite

## Installation

```bash
# Build
cd symphony-go
go build -o symphony ./cmd/symphony

# Or with Docker
docker build -t symphony .
```

## Quick Start

### 1. Create a Project

```bash
./symphony project create "My App" --desc "My awesome application"
```

### 2. Create Issues

```bash
./symphony issue create "Add authentication" --project <project_id> --labels feature,security
./symphony issue create "Fix login bug" --priority 1 --labels bug
```

### 3. View the Board

```bash
./symphony board
```

### 4. Start the MCP Server

Add to your Codex/ChatGPT config:

```json
{
  "mcpServers": {
    "symphony": {
      "command": "/path/to/symphony",
      "args": ["mcp"]
    }
  }
}
```

Now you can ask Codex to manage your backlog:
- "Create an issue for implementing the login page"
- "Show me all issues in the backlog"
- "Move issue APP-1 to in_progress"

### 5. Run the Orchestrator

```bash
./symphony run /path/to/your/repo
```

This will:
1. Poll for issues in "ready" state
2. Spawn coding agents (Codex by default) for each issue
3. Track progress and handle retries

## MCP Tools

The MCP server exposes these tools:

You can also load external extension tools via `--extensions ext.json`.
Each entry defines `name`, `description`, `command`, and optional controls:
- `timeout_sec` (default 15)
- `allowed` (set false to disable)
- `require_args` (reject call unless `args` provided)
- `working_dir`
- `deny_env_passthrough` (only SYMPHONY_* env)
At runtime, arguments are provided via `SYMPHONY_ARGS_JSON` and tool name via `SYMPHONY_TOOL_NAME`.

| Tool | Description |
|------|-------------|
| `create_project` | Create a new project |
| `list_projects` | List all projects |
| `delete_project` | Delete a project |
| `create_epic` | Create an epic within a project |
| `list_epics` | List epics (optionally filtered) |
| `delete_epic` | Delete an epic |
| `create_issue` | Create a new issue |
| `get_issue` | Get issue details |
| `list_issues` | List issues with filters |
| `update_issue` | Update an issue |
| `set_issue_state` | Change issue state |
| `delete_issue` | Delete an issue |
| `board_overview` | Get kanban board overview |
| `set_blockers` | Set issue blockers |

## Issue States

| State | Description |
|-------|-------------|
| `backlog` | Not yet prioritized |
| `ready` | Ready to be picked up |
| `in_progress` | Currently being worked on |
| `in_review` | PR created, awaiting review |
| `done` | Completed |
| `cancelled` | Cancelled |

## WORKFLOW.md

Create a `WORKFLOW.md` in your repo to customize agent behavior:

You can bootstrap one with:

```bash
./symphony workflow init /path/to/repo
```

```yaml
---
tracker:
  kind: kanban
  active_states:
    - ready
    - in_progress
    - in_review
  terminal_states:
    - done
    - cancelled
polling:
  interval_ms: 30000
workspace:
  root: ./workspaces
hooks:
  timeout_ms: 60000
agent:
  max_concurrent_agents: 3
  max_turns: 20
  max_retry_backoff_ms: 300000
  mode: app_server # or stdio
codex:
  command: codex app-server
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
---

You are working on issue {{ issue.identifier }}.

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

Title: {{ issue.title }}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

## Your Tasks

1. Create a feature branch
2. Implement the changes
3. Write tests
4. Create a pull request
```

The prompt template uses strict Liquid-style variables. Supported values include:
- `{{ issue.identifier }}` - Issue ID (for example `APP-123`)
- `{{ issue.title }}` - Issue title
- `{{ issue.description }}` - Issue description
- `{{ issue.state }}` - Current issue state
- `{{ attempt }}` - Retry attempt number on continuation/retry runs

Missing `WORKFLOW.md` files are bootstrapped automatically by `run` and `verify`. `spec-check` stays non-mutating and reports missing or invalid workflow files as failures.

## CLI Reference

```bash
# Projects
./symphony project create <name> [--desc <description>]
./symphony project list
./symphony project delete <id>

# Issues
./symphony issue create <title> [--project <id>] [--priority <n>] [--labels <l1,l2>]
./symphony issue list [--state <state>] [--project <id>]
./symphony issue show <identifier>
./symphony issue move <identifier> <state>
./symphony issue update <identifier> [--title <title>] [--pr <number> <url>]
./symphony issue delete <identifier>
./symphony issue block <identifier> <blocker_identifier...>

# Board
./symphony board [--project <id>]

# Orchestrator
./symphony run [repo_path] [--db <path>] [--logs-root <path>] [--log-max-bytes <n>] [--log-max-files <n>] [--port <port>]
# Observability API (if --port set)
# GET /health
# GET /api/v1/state                  (global status)
# GET /api/v1/sessions               (all live app_server sessions + event history)
# GET /api/v1/sessions?issue=ISS-1   (single issue session)
# GET /api/v1/events?since=0&limit=100   (in-memory event feed with cursor)
# GET /api/v1/dashboard              (state + sessions + recent events snapshot)

# MCP Server
./symphony mcp [--db <path>] [--extensions <json-file>]

# Status
./symphony status [--json]

# Verification
./symphony verify [--repo <path>] [--db <path>] [--json]
./symphony spec-check [--repo <path>] [--json]
./symphony workflow init [repo_path]
```

## Architecture

```
┌─────────────────┐     ┌──────────────────┐
│   Codex/GPT     │────▶│   MCP Server     │
│   (via MCP)     │◀────│   (stdio)        │
└─────────────────┘     └────────┬─────────┘
                                 │
                                 ▼
                        ┌────────────────┐
                        │  Kanban Store  │
                        │   (SQLite)     │
                        └────────┬───────┘
                                 │
                                 ▼
┌─────────────────┐     ┌──────────────────┐
│  WORKFLOW.md    │────▶│  Orchestrator    │
│  (repo config)  │     │  (polls/dispatch)│
└─────────────────┘     └────────┬─────────┘
                                 │
                                 ▼
                        ┌──────────────────┐
                        │  Agent Runner    │
                        │  (Codex/other)   │
                        └──────────────────┘
```

## Docker

```bash
# Build
docker build -t symphony .

# Run MCP server
docker run --rm -i -v ./data:/data symphony mcp --db /data/symphony.db

# Run orchestrator
docker run --rm -v ./my-repo:/repo -v ./data:/data symphony run /repo --db /data/symphony.db
```

## Comparison with Original Symphony

| Feature | Original (Elixir) | This (Go) |
|---------|-------------------|-----------|
| Issue Tracker | Linear | Local Kanban |
| MCP Support | ❌ | ✅ |
| Single Binary | ❌ | ✅ |
| WORKFLOW.md | ✅ | ✅ |
| Orchestrator | ✅ | ✅ |
| Language | Elixir | Go |

## License

MIT
