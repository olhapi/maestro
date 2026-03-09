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
phases:
  review:
    enabled: false
    prompt: |
      Review the implementation for issue {{ issue.identifier }} in the current workspace.
      Run focused verification, fix any issues you find, move the issue back to in_progress if more work is needed, and move it to done when review is complete.
  done:
    enabled: false
    prompt: |
      Finalize issue {{ issue.identifier }} from the current workspace.
      Perform the project-specific done steps, such as opening or updating a PR, merging, or other release bookkeeping, while keeping the issue in done unless it truly needs to be reopened.
agent:
  max_concurrent_agents: 3
  max_turns: 20
  max_retry_backoff_ms: 300000
  mode: app_server
codex:
  command: codex app-server
  expected_version: 0.111.0
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000
---

You are working on issue {{ issue.identifier }}.

Current phase: {{ phase }}

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
