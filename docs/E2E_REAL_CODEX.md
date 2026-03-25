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
make e2e-real-codex-phases
make e2e-real-codex-issue-images
```

The targets run:

- [`scripts/e2e_real_codex.sh`](../scripts/e2e_real_codex.sh) for the basic single-pass artifact flow
- [`scripts/e2e_real_codex_phases.sh`](../scripts/e2e_real_codex_phases.sh) for the implementation/review/done phase flow
- [`scripts/e2e_real_codex_issue_images.sh`](../scripts/e2e_real_codex_issue_images.sh) for the app-server multimodal issue-image flow

## What It Verifies

The generated workflow asks Codex to:

- read the issue description
- create the requested artifact in a shared output directory
- confirm the file contents from the shell
- mark the issue `done`

The test issues are intentionally deterministic:

- `artifact-one.txt` must contain `maestro e2e ok 1`
- `artifact-two.txt` must contain `maestro e2e ok 2`

The phase harness verifies two additional deterministic paths:

- one issue must go `implementation -> review -> done -> complete`
- one issue must go `implementation -> done -> complete` without running review
- each phase writes a dedicated artifact and appends to a phase log in the expected order
- completed workspaces are cleaned up immediately, and restarting `maestro run` also removes any leftovers on startup

The image harness verifies the new app-server image path:

- Maestro attaches a local PNG to an issue through the normal issue-image storage flow
- the harness reuploads the fixture under an opaque filename so the expected OCR text is not leaked through image metadata
- app-server mode stages that attachment into the issue workspace under `.maestro/issue-images`, and the staged bytes must match the original fixture exactly
- the real Codex app-server receives the image as multimodal input and returns only `MAESTRO`
- Maestro persists that final answer in issue activity, which the harness extracts from the raw final-answer payload and compares exactly

## Why It Uses `codex exec`

The harness uses the real Codex CLI in `stdio` mode via `codex exec` so the run stays end-to-end while remaining easy to launch from a shell script:

- Maestro still creates issues, manages workspaces, and drives scheduling
- Codex still performs the file and shell actions
- the verification remains deterministic and local

## Why The Image Harness Uses `codex app-server`

The issue-image harness specifically uses app-server mode because the feature under test is multimodal `localImage` delivery on `turn/start`:

- Maestro stages issue attachments into the workspace before the first fresh turn
- the runner sends one text input plus one `localImage` input for the attached issue image
- the harness verifies both the staged file and the persisted final answer from the real Codex turn

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
- `E2E_IMAGE_FIXTURE`: override the image fixture used by `e2e_real_codex_issue_images.sh`.
- `E2E_EXPECTED_TEXT`: override the expected OCR text for the image harness. Default `MAESTRO`.
