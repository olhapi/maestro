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
  mode: app_server
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

You are an autonomous coding agent working on issue {{ issue.identifier }}.

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

## Issue Details

- **Title**: {{ issue.title }}
- **Description**:
  {% if issue.description %}
  {{ issue.description }}
  {% else %}
  No description provided.
  {% endif %}
- **State**: {{ issue.state }}

## Instructions

1. Create a branch for this issue
2. Implement the changes described
3. Run tests and ensure they pass
4. Create a pull request
5. Update the issue with the PR link

## Guidelines

- Follow the project's coding standards
- Write clear commit messages
- Keep changes focused and minimal
