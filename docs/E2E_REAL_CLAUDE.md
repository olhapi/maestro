# Real Claude E2E Harness

This harness exercises the full Maestro loop with the real Claude CLI:

1. build `maestro`
2. create a temporary repo root and SQLite database
3. write a dedicated `WORKFLOW.md`
4. initialize a minimal committed Git repo at that harness root
5. run a Claude preflight with `maestro verify`
6. create one deterministic issue and move it to `ready`
7. switch that issue to `full-access` so Claude's local file and shell actions are auto-approved
8. start `maestro run` on a temporary loopback HTTP port
9. wait for Claude to complete the issue
10. verify the expected output artifact

## Entry Point

```bash
make e2e-real-claude
```

The target runs:

- [`scripts/e2e_real_claude.sh`](../scripts/e2e_real_claude.sh) for the baseline single-issue flow
- [`scripts/lib/e2e_real_claude_harness.sh`](../scripts/lib/e2e_real_claude_harness.sh) for the reusable Claude bootstrap, preflight, orchestration, and failure-diagnostics helpers

## What It Verifies

The generated workflow asks Claude to:

- read the issue description
- create the requested artifact in a shared output directory
- confirm the file contents from the shell
- mark the issue `done`

The deterministic issue is:

- `claude-artifact.txt` must contain `maestro claude e2e ok`

The harness also enforces the real-Claude prerequisites before the run starts:

- `runtime.default` must resolve to the generated `claude` runtime entry
- `maestro verify` must report `runtime_claude: ok`
- `claude_auth_source_status` must be `ok`
- `claude_auth_source` must be `OAuth` or `cloud provider`
- `claude_session_status`, `claude_session_bare_mode`, and `claude_session_additional_directories` must all be `ok`

## Why It Uses `full-access`

The baseline issue is set to `full-access` so the Claude runtime can pre-allow the local built-in tools needed by the harness and avoid the current approval-prompt bridge path during unattended local runs. The file and shell work still stays inside the temporary harness root, and the only non-local dependency should remain the operator's existing Claude auth/session.

## Failure Diagnostics

Failures keep the harness directory and print:

- harness root
- issue identifier and current state when an issue was already created
- `verify.log`
- `orchestrator.log`
- logs root
- database path
- workspaces root

That leaves the generated `WORKFLOW.md`, SQLite store, per-issue workspaces, and orchestrator output available for follow-on debugging.

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
- `E2E_CLAUDE_COMMAND`: override the Claude command written into the generated workflow, mainly for local harness validation.
