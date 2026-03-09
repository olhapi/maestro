# Real Codex E2E Harness

This harness exercises the full Maestro loop with the real Codex CLI:

1. build `maestro`
2. create a temporary repo root and SQLite database
3. write a dedicated `WORKFLOW.md`
4. create two simple issues and move them to `ready`
5. start `maestro run`
6. wait for Codex to complete both issues
7. verify the expected output artifacts

## Entry Point

```bash
make e2e-real-codex
```

The target runs [`scripts/e2e_real_codex.sh`](../scripts/e2e_real_codex.sh).

## What It Verifies

The generated workflow asks Codex to:

- read the issue description
- create the requested artifact in a shared output directory
- confirm the file contents from the shell
- mark the issue `done`

The test issues are intentionally deterministic:

- `artifact-one.txt` must contain `maestro e2e ok 1`
- `artifact-two.txt` must contain `maestro e2e ok 2`

## Why It Uses `codex exec`

The harness uses the real Codex CLI in `stdio` mode via `codex exec` so the run stays end-to-end while remaining easy to launch from a shell script:

- Maestro still creates issues, manages workspaces, and drives scheduling
- Codex still performs the file and shell actions
- the verification remains deterministic and local

## Requirements

- `go`
- `codex`
- an active Codex login/session

## Environment Overrides

- `E2E_TIMEOUT_SEC`: total wait time per issue. Default `600`.
- `E2E_POLL_SEC`: poll interval while waiting. Default `2`.
- `E2E_KEEP_HARNESS`: keep the temporary harness directory after success. Default `1`.
- `E2E_ROOT`: reuse a specific harness directory instead of creating a new temp directory.
- `E2E_CODEX_COMMAND`: override the Codex command, mainly for local harness validation.
