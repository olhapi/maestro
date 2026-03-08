---
# Supported tracker kind today: kanban.
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
# Workspaces are created relative to this repo unless you use an absolute path.
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

You are working on issue {{ issue.identifier }}.

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

Title: {{ issue.title }}
State: {{ issue.state }}
Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

Requirements:
1. Keep the change focused on the issue.
2. Run the relevant tests or checks.
3. Update issue metadata with PR details when applicable.
4. Leave the workspace in a reviewable state.
