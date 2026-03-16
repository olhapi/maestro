---
# Tracker provider configuration. Supported tracker kind today: kanban.
tracker:
  # Tracker backend to read and write issues from.
  kind: kanban
  # States that should be treated as active work and picked up by orchestration.
  active_states:
    - ready
    - in_progress
    - in_review
  # States that should be treated as terminal and left alone by orchestration.
  terminal_states:
    - done
    - cancelled

# How often Maestro scans the tracker for runnable work.
polling:
  interval_ms: 30000

# Where Maestro creates per-issue workspaces. Relative paths resolve from the repo root;
# absolute paths, $ENV_VAR paths, and ~/ paths are also supported.
workspace:
  root: ./workspaces

# Optional shell hooks that run inside the issue workspace.
hooks:
  # Runs immediately after Maestro creates or reuses a workspace.
  # after_create: ./scripts/after-create.sh
  # Runs before each agent attempt starts.
  # before_run: ./scripts/before-run.sh
  # Runs after each agent attempt finishes, even when the attempt fails.
  # after_run: ./scripts/after-run.sh
  # Runs before Maestro removes a workspace during cleanup.
  # before_remove: ./scripts/before-remove.sh
  # Maximum runtime for each hook command before Maestro terminates it.
  timeout_ms: 60000

# Optional extra prompts for later workflow phases.
phases:
  review:
    # Enable a dedicated review pass after implementation. Other option: false.
    enabled: true
    # Prompt rendered when the issue enters review. Uses the same template variables
    # as the main prompt, such as issue.*, project.*, phase, and attempt.
    prompt: |
      Review the implementation for issue {{ issue.identifier }} in the current workspace.
      {% if project.description %}
      Project context:
      {{ project.description }}
      {% endif %}
      Run focused verification, fix any issues you find, move the issue back to in_progress if more work is needed, and move it to done when review is complete.
  done:
    # Enable a dedicated finalization pass after implementation is otherwise complete.
    enabled: true
    # Prompt rendered when the issue enters done for project-specific wrap-up steps.
    prompt: |
      Finalize issue {{ issue.identifier }} from the current workspace.
      {% if project.description %}
      Project context:
      {{ project.description }}
      {% endif %}
      Perform the project-specific done steps, such as opening or updating a PR, merging, or other release bookkeeping, while keeping the issue in done unless it truly needs to be reopened.

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  max_concurrent_agents: 1
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: 50
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: 300000
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: 8
  # Agent transport. Other options: app_server, stdio.
  mode: app_server
  # Scheduling behavior. Other options: parallel, per_project_serial.
  dispatch_mode: parallel

# Codex CLI launch and sandbox settings.
codex:
  # Exact command Maestro launches for the agent.
  command: codex app-server
  # Expected codex --version. Mismatches warn but do not hard-fail.
  expected_version: 0.111.0
  # Approval mode for Codex. Other string options: on-request, on-failure, untrusted.
  # A structured reject object is also supported for per-category rejection policies.
  approval_policy: never
  # Thread-level sandbox. Other options: read-only, workspace-write, danger-full-access.
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    # Per-turn sandbox policy. Other policy types: readOnly, externalSandbox, dangerFullAccess.
    type: workspaceWrite
    # Network access during a turn. For externalSandbox, the schema also allows enabled/restricted.
    networkAccess: true
    # Optional for workspaceWrite. If omitted, Maestro fills writable roots automatically.
    # writableRoots:
    #   - /absolute/path/to/repo
    # Optional for workspaceWrite. Other options: fullAccess or restricted.
    # readOnlyAccess:
    #   type: fullAccess
    #   # For restricted, you can also set includePlatformDefaults and readableRoots.
    # Optional for workspaceWrite only.
    # excludeTmpdirEnvVar: false
    # Optional for workspaceWrite only.
    # excludeSlashTmp: false
  # Maximum total runtime for one turn before Maestro cancels it.
  turn_timeout_ms: 3600000
  # Maximum time to wait for streamed output before considering the stream stalled.
  read_timeout_ms: 10000
  # Maximum idle time without Codex activity before Maestro aborts the turn.
  stall_timeout_ms: 300000
---

You are working on issue {{ issue.identifier }}.

Current phase: {{ phase }}

{% if attempt %}
Continuation attempt: {{ attempt }}
{% endif %}

Title: {{ issue.title }}
State: {{ issue.state }}
{% if project.description %}
Project context:
{{ project.description }}

{% endif %}
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
