# Real Claude E2E Harness

These harnesses exercise the full Maestro loop with the real Claude CLI:

1. build `maestro`
2. build `maestro-claude-e2e-probe`
3. create a temporary repo root, SQLite database, daemon registry, and evidence directories
4. write a dedicated `WORKFLOW.md`
5. initialize a minimal committed Git repo at that harness root
6. run a Claude preflight with `maestro verify`
7. create deterministic issues for the scenario under test
8. set the issue permission profiles required by that scenario
9. start `maestro run` on a temporary loopback HTTP port
10. launch Claude through a harness wrapper that copies the generated support files and probes the configured `maestro mcp` bridge
11. wait for Claude to complete, pause, revise, or resume as required by the scenario
12. verify the expected output artifacts plus the bridge/daemon evidence summaries

## Entry Point

```bash
make e2e-real-claude
make e2e-real-claude-profiles
```

The target runs:

- [`scripts/e2e_real_claude.sh`](../scripts/e2e_real_claude.sh) for the lifecycle flow (`full-access` success, resume, and interruption)
- [`scripts/e2e_real_claude_profiles.sh`](../scripts/e2e_real_claude_profiles.sh) for permission-profile coverage (`default`, `full-access`, and `plan-then-full-access`)
- [`scripts/lib/e2e_real_claude_harness.sh`](../scripts/lib/e2e_real_claude_harness.sh) for the reusable Claude bootstrap, preflight, orchestration, and failure-diagnostics helpers

## What It Verifies

The generated workflows ask Claude to:

- read the issue description
- emit deterministic stream markers so the probe can correlate live execution
- use exactly the requested built-in tool calls
- confirm the requested file contents or pause for plan approval as directed
- mark the issue `done` only when the scenario explicitly requires it

The harness also enforces the real-Claude prerequisites before the run starts:

- `runtime.default` must resolve to the generated `claude` runtime entry
- `maestro verify` must report `runtime_claude: ok`
- `claude_auth_source_status` must be `ok`
- `claude_auth_source` must be `OAuth` or `cloud provider`
- `claude_session_status`, `claude_session_bare_mode`, and `claude_session_additional_directories` must all be `ok`

During the real runtime launch, the wrapper and probe also verify:

- Claude received the generated `--mcp-config`, `--settings`, and `--strict-mcp-config` flags from Maestro's real startup path
- the wrapper keeps the orchestrator-provided stdin attached to the real Claude process while the bridge probe runs
- the copied `mcp.json` points at `maestro mcp --db <same-db>`
- the copied `settings.json` disables auto mode, bypass mode, hooks, and built-in git instructions
- the attached bridge exposes the expected Maestro tools and can successfully call `server_info`, `list_issues`, `get_runtime_snapshot`, and `list_sessions`
- the bridge response metadata matches the orchestrator-owned database path and daemon registry entry
- the daemon registry still contains exactly one stable entry before and after the bridge probe
- a live Claude `stdio` session is visible through `list_sessions` while the run is active

The permission-profile harness adds explicit verification that:

- `default` keeps approvals Maestro-managed through the permission-prompt bridge
- `full-access` switches to `--allowed-tools` without enabling Claude auto/bypass modes
- `plan-then-full-access` launches in Claude `plan` mode, pauses in `plan_approval_pending`, preserves planning session/version lineage, supports plan revision requests, and only switches to `--allowed-tools` after approval
- the same Claude session is resumed from planning through post-approval execution

The wider deterministic Claude guardrail matrix also covers unsupported and disallowed paths that should fail before a live Claude turn can degrade:

- readiness and attach prerequisites: the shell harness test matrix asserts `maestro verify` / `doctor` fail loudly for missing Claude, rejected auth, `--bare`, bypass/auto permission modes, and `additionalDirectories`
- attach/probe prerequisites: the shell harness also fails loudly when the bridge probe cannot observe a live Claude session, and it leaves the copied support files plus orchestrator diagnostics in place for debugging
- unsupported capability failures: runner/orchestrator coverage asserts first-turn Claude issue images fail with `unsupported_runtime_capability`, pause instead of auto-retrying, preserve Claude runtime metadata, and avoid partial workspace staging
- recoverable run failures: the approval harness keeps the edit-timeout case distinct as `retry_limit_reached` so operators can tell it apart from readiness and unsupported-capability failures

## Why It Covers Multiple Profiles

The lifecycle harness uses `full-access` so Claude can complete the deterministic local file-and-shell tasks without stopping on approval prompts. The permission-profile harness then exercises the Maestro-managed differences between `default`, `full-access`, and `plan-then-full-access` explicitly, while still keeping the work inside the temporary harness root and relying only on the operator's existing Claude auth/session.

## Failure Diagnostics

Failures keep the harness directory and print:

- harness root
- issue identifier and current state when an issue was already created
- daemon registry dir
- Claude evidence dir
- `verify.log`
- `orchestrator.log`
- logs root
- database path
- workspaces root

That leaves the generated `WORKFLOW.md`, SQLite store, per-issue workspaces, daemon registry, copied Claude support files, and orchestrator output available for follow-on debugging.

## Requirements

- `go`
- `claude`
- `git`
- `sqlite3`
- an active supported Claude auth/session that passes the harness preflight

## Environment Overrides

- `E2E_TIMEOUT_SEC`: total wait time for the issue. Default `600`.
- `E2E_POLL_SEC`: poll interval while waiting. Default `2`.
- `E2E_KEEP_HARNESS`: keep the temporary harness directory after success. Default `1`.
- `E2E_ROOT`: reuse a specific harness directory instead of creating a new temp directory.
- `E2E_PORT`: override the temporary loopback HTTP port passed to `maestro run`. Default `0` to let the OS choose a free port.
- `E2E_CLAUDE_COMMAND`: override the real Claude command that the harness wrapper executes and validates during shell preflight. The generated workflow points at the wrapper, which forwards to this command after it records the support-file and bridge evidence. The preflight parser supports direct command invocations with optional leading `KEY=value` assignments plus normal shell quoting/escaping to keep an executable and literal arguments together. It does not evaluate command substitution, variable expansion, globs, pipes, redirects, or other shell expressions while validating the override.
