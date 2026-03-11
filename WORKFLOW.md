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
  max_concurrent_agents: 1
  max_turns: 50
  max_retry_backoff_ms: 300000
  mode: app_server
  dispatch_mode: parallel
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

Instructions:
1. This is an unattended orchestration session. Do not ask a human to perform follow-up actions.
2. Work only inside the provided workspace for this issue.
3. Keep the change focused on the issue and preserve the existing project conventions.
4. Reproduce or inspect the current behavior before making code changes when possible.
5. Run the relevant validation for the scope you changed.
6. Create and work from a dedicated issue branch before making changes. Use a deterministic branch name such as `codex/{{ issue.identifier }}`.
7. Do not mark the issue done until the change is ready for the finalization pass. Merge and branch cleanup belong in the done phase.
8. Do not mark the issue done if the change is only committed on a side branch, only present in the workspace, or only opened as a PR. In those cases, leave the issue in a non-terminal state and report the exact merge blocker.
9. If you create a branch, commit, PR, or merge commit, update issue metadata with the result when applicable.
10. If blocked by missing credentials, permissions, merge conflicts, or required services, stop and report the blocker clearly in the final message.
11. Final message should contain only completed work, validation run, merge status, and blockers.
