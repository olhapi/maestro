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
  interval_ms: 50
workspace:
  root: /var/folders/85/1w5q_mmd18nbnxtpfdbgb3xr0000gn/T/TestStoppedProjectsDoNotDispatchUntilStarted1145615763/001/workspaces
hooks:
  timeout_ms: 1000
phases:
  review:
    enabled: false
  done:
    enabled: false
agent:
  max_concurrent_agents: 2
  max_turns: 2
  max_retry_backoff_ms: 100
  max_automatic_retries: 8
  mode: stdio
codex:
  command: cat
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 500
  turn_timeout_ms: 1000
---
Test prompt for {{ issue.identifier }}
