---
# Tracker configuration. Supported tracker kind today: kanban.
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
      Commit all changes to the feature branch, merge it to main, rerun validation on main, and push main to origin. Do not remove the issue worktree yourself; Maestro handles post-run cleanup after your run exits. If merge or push is blocked, report the blocker clearly and stop.

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  max_concurrent_agents: 2
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: 50
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: 300000
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: 8
  # Agent transport. Other options: app_server, stdio.
  mode: app_server
  # Scheduling behavior. Other options: parallel, per_project_serial.
  dispatch_mode: per_project_serial

# Codex CLI launch and collaboration settings.
codex:
  # Exact command Maestro launches for the agent.
  command: codex app-server
  # Expected codex --version. Mismatches warn but do not hard-fail.
  expected_version: 0.116.0
  # Approval mode for Codex. Other string options: on-request, on-failure, untrusted.
  # A structured reject object is also supported for per-category rejection policies.
  approval_policy: never
  # Initial collaboration mode for fresh app_server threads. Other option: plan.
  # Ignored for stdio runs and resumed threads.
  initial_collaboration_mode: default
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

## Default posture

- Determine the issue status first, then follow the matching flow.
- Open the Maestro Workpad comment immediately and update it before new implementation work.
- Plan before coding. Design verification before changing code.
- Reproduce or inspect current behavior first so the target is explicit.
- Keep metadata current: state, checklist, acceptance criteria, and links.
- Treat the persistent workpad comment as the source of truth.
- If you find meaningful out-of-scope work, file a separate maestro CLI issue instead of expanding scope. Include a clear title, description, and acceptance criteria; place it in Backlog; use the same project; link the current issue; and add a blocker relation when needed.
- Move status only when the quality bar for that status is met.
- Use the blocked-access escape hatch only for genuine external blockers after documented fallbacks are exhausted.

## Instructions
1. Stay inside the provided workspace.
2. Keep the change focused and preserve project conventions.
3. Reproduce or inspect current behavior before editing when possible.
4. Run validation that covers the changed scope.
5. Create a dedicated issue branch before editing. Use maestro/{{ issue.identifier }}.
6. Do not consider the task complete until the change is merged into local main.
7. Before marking done, sync origin/main, merge the issue branch into local main, rerun validation on main, and push main to origin.
8. In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.
9. Add an issue comment when you create a branch, commit, PR, or merge commit, when relevant.
10. If blocked by credentials, permissions, merge conflicts, or required services, stop, report it clearly in the final message, and add the same blocker comment.
11. Final message must contain only completed work, validation run, merge status, and blockers.


## Guardrails

- If the branch PR is already closed or merged, do not reuse it. Create a new branch from origin/main and restart from reproduction and planning.
- If the issue state is Backlog, do not modify it; wait for a human to move it to Ready.
- Do not edit the issue body for planning or progress updates.
- Use exactly one persistent workpad comment (## Maestro Workpad) per issue.
- Temporary proof edits are allowed only for local verification and must be reverted before commit.
- Keep issue text concise, specific, and reviewer-oriented.
- If blocked and no workpad exists yet, add one blocker comment with the blocker, impact, and next unblock action.
