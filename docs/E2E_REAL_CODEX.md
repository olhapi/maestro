# Real Codex E2E Harness

This repository now includes a reproducible end-to-end harness that exercises the real Symphony flow with Codex executing the work:

1. Build `symphony`
2. Create a temporary Symphony repo root and database
3. Create two issues
4. Move both issues to `ready`
5. Start `symphony run`
6. Let Codex complete both issues
7. Verify the resulting artifacts

## Entry Point

```bash
make e2e-real-codex
```

The target runs [`scripts/e2e_real_codex.sh`](/Users/olhapi/Projects/symphony-go/scripts/e2e_real_codex.sh).

## What The Prompt Asks Codex To Do

For each issue, the generated `WORKFLOW.md` tells Codex to:

- read the issue description
- create the requested artifact in a shared artifacts directory
- verify the file contents with shell commands
- mark the issue `done` with `symphony issue move <id> done --db <db>`
- stop

The two issues are intentionally simple and deterministic:

- `artifact-one.txt` must contain `symphony e2e ok 1`
- `artifact-two.txt` must contain `symphony e2e ok 2`

## Why This Uses `codex exec`

The harness runs the real Codex CLI in `stdio` mode via `codex exec`. That keeps the flow fully end-to-end while making the harness easier to run deterministically from a shell task:

- prompt comes from Symphony
- Codex executes real shell actions
- Symphony still owns issue creation, scheduling, workspaces, and completion checks

## Requirements

- `go`
- `codex`
- a working Codex login/session

## Useful Environment Variables

- `E2E_TIMEOUT_SEC`: overall wait time per issue. Default `600`.
- `E2E_POLL_SEC`: poll interval while waiting for issues to finish. Default `2`.
- `E2E_KEEP_HARNESS`: keep the temporary harness directory after success. Default `1`.
- `E2E_ROOT`: reuse a specific harness directory instead of a new temp directory.
- `E2E_CODEX_COMMAND`: override the Codex command. This is mainly useful for local harness validation without a real Codex run.
