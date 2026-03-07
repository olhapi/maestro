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

```yaml
---
poll_interval: 30
max_concurrent: 3
workspace_root: ./workspaces
agent:
  executable: codex
  args: []
  timeout: 3600
  mode: stdio  # stdio | app_server (compat mode)
hooks:
  timeout_sec: 60
---

# Instructions for {{.Identifier}}

You are working on: **{{.Title}}**

{{.Description}}

## Your Tasks

1. Create a feature branch
2. Implement the changes
3. Write tests
4. Create a pull request
```

The prompt template supports Go template syntax with access to issue fields:
- `{{.Identifier}}` - Issue ID (e.g., APP-123)
- `{{.Title}}` - Issue title
- `{{.Description}}` - Issue description
- `{{.Labels}}` - Issue labels
- `{{.Priority}}` - Issue priority

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
./symphony run [repo_path] [--db <path>] [--logs-root <path>] [--port <port>]
# Observability API (if --port set)
# GET /health
# GET /api/v1/state

# MCP Server
./symphony mcp [--db <path>]

# Status
./symphony status [--json]
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
