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
  interval_ms: 10000

# Where Maestro creates per-issue workspaces. Relative paths resolve from the repo root;
# absolute paths, $ENV_VAR paths, and ~/ paths are also supported.
workspace:
  root: ~/.maestro/worktrees

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
    # Enable a dedicated review pass after implementation. Available values: true, false. Fresh maestro init default: true.
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
    # Enable a dedicated finalization pass after implementation is otherwise complete. Available values: true, false. Fresh maestro init default: true.
    enabled: true
    # Prompt rendered when the issue enters done for project-specific wrap-up steps.
    prompt: |
      Finalize issue {{ issue.identifier }} from the current workspace.
      {% if project.description %}
      Project context:
      {{ project.description }}

      {% endif %}
      The done phase owns merge-back and finalization. Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.

      - Commit all remaining changes to the prepared issue branch.
      - Merge the issue branch into the repository default branch.
      - Rerun the relevant validation on the default branch.
      - Push the default branch to origin.
      - Do not remove the issue worktree yourself; leave the workspace intact for Maestro's post-run cleanup.
      - If merge conflicts, missing credentials, permissions, or required services block completion, report the blocker clearly and stop.

# Agent runtime settings.
agent:
  # Maximum concurrent issues per project when dispatch_mode is parallel.
  # dispatch_mode=per_project_serial forces effective per-project concurrency to 1.
  max_concurrent_agents: 2
  # Maximum turns Maestro gives Codex before ending an attempt.
  max_turns: 40
  # Maximum delay between automatic retries after failed attempts.
  max_retry_backoff_ms: 60000
  # Maximum automatic retry attempts for the same issue before Maestro stops retrying.
  max_automatic_retries: 8
  # Agent transport. Available values: app_server, stdio. Fresh maestro init default: app_server.
  mode: app_server
  # Scheduling behavior. Available values: parallel, per_project_serial. Fresh maestro init default: parallel.
  dispatch_mode: per_project_serial

# Codex CLI launch and collaboration settings.
codex:
  # Exact command Maestro launches for the agent.
  command: codex app-server
  # Expected Codex CLI version. Mismatches warn but do not hard-fail.
  expected_version: 0.118.0
  # Approval mode for Codex. Available values: never, on-request, on-failure, untrusted. Fresh maestro init default: never.
  # "never" keeps unattended runs non-interactive, so permission recovery must come
  # from the project or issue permission profile rather than live approval prompts.
  # Use on-request when initial_collaboration_mode is plan so the agent can ask
  # questions and recover through approvals before Maestro promotes the run.
  # A structured granular object is also supported for per-category approval policies.
  approval_policy: never
  # Initial collaboration mode for fresh app_server threads. Available values: default, plan. Fresh maestro init default: default.
  # Use plan for a planning pass before implementation. Pair it with on-request
  # when you want the agent to ask questions and pause for approval.
  # Ignored for stdio runs and resumed threads.
  initial_collaboration_mode: default
  # Maximum total runtime for one turn before Maestro cancels it.
  turn_timeout_ms: 1800000
  # Maximum time to wait for streamed output before considering the stream stalled.
  read_timeout_ms: 10000
  # Maximum idle time without Codex activity before Maestro aborts the turn.
  stall_timeout_ms: 300000
---

If Codex is not installed globally, `codex.command` can instead be pinned to `npx -y @openai/codex@0.118.0 app-server`.

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
{% if plan_mode %}
- This is a planning turn. Ask the clarifying questions you need, validate assumptions, and stop with a single <proposed_plan> block once the approach is ready.
- Do not start implementation until the plan is approved.
{% endif %}
- Reproduce or inspect current behavior first so the target is explicit.
- Keep metadata current: state, checklist, acceptance criteria, and links.
- Treat the persistent workpad comment as the source of truth.
- If you find meaningful out-of-scope work, file a separate maestro CLI issue instead of expanding scope. Include a clear title, description, and acceptance criteria; place it in Backlog; use the same project; link the current issue; and add a blocker relation when needed.
- Move status only when the quality bar for that status is met.
- Use the blocked-access escape hatch only for genuine external blockers after documented fallbacks are exhausted.
- In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles cleanup hooks and worktree removal after your run exits.

## Instructions

1. Stay inside the provided workspace.
2. Keep the change focused and preserve project conventions.
3. Reproduce or inspect current behavior before editing when possible.
4. Run validation that covers the changed scope.
5. Use the issue branch already prepared by Maestro in the provided workspace. Do not create, rename, or switch issue branches manually unless you are recovering from a broken workspace.
6. Do not consider the task complete until the change is merged into the repository default branch.
7. Before marking done, merge the issue branch into the repository default branch, rerun validation on that branch, and push the default branch to origin.
8. In the done phase, after merge, push, and final validation succeed, leave the workspace intact; Maestro handles preview publication, cleanup hooks, and worktree removal after your run exits.
9. Add an issue comment when you create a branch, commit, PR, or merge commit, when relevant.
10. If blocked by credentials, permissions, merge conflicts, or required services, stop, report it clearly in the final message, and add the same blocker comment.
11. Final message must contain only completed work, validation run, merge status, and blockers.

## Guardrails

- If the workspace branch is unusable or a prior branch was already merged or closed, do not manually create a replacement branch. Report the condition clearly and stop; Maestro owns workspace and branch bootstrap.
- If the issue state is Backlog, do not modify it; wait for a human to move it to Ready.
- Do not edit the issue body for planning or progress updates.
- Use exactly one persistent workpad comment (## Maestro Workpad) per issue.
- Temporary proof edits are allowed only for local verification and must be reverted before commit.
- Keep issue text concise, specific, and reviewer-oriented.
- If blocked and no workpad exists yet, add one blocker comment with the blocker, impact, and next unblock action.
