# Parity Report (Checkpoint)

Date: 2026-03-08

This report tracks parity against `openai/symphony` Elixir implementation, excluding Linear integration by design.

## Status Summary

- Core orchestration loop: **implemented**
- Local tracker replacement (Kanban+MCP): **implemented**
- Workspace lifecycle safety: **implemented (practical parity)**
- App-server compatibility: **implemented (event/session tracking parity baseline)**
- Observability/status: **implemented (state + sessions APIs)**
- Extensions runtime: **implemented with safety controls**

See also detailed mapping: `docs/UPSTREAM_CROSSWALK.md`.

## Matrix

| Upstream area | Status | Notes |
|---|---|---|
| `core_test.exs` | Partial | workflow/config defaults + validations exist; not full option parity |
| `workspace_and_config_test.exs` | Partial+ | deterministic keys, stale path recovery, hook timeout support, symlink escape checks |
| `orchestrator_status_test.exs` | Partial+ | richer status fields and run metrics implemented |
| `log_file_test.exs` | Partial+ | `--logs-root` JSON logs to file+stdout + size-based rotation (`--log-max-bytes`, `--log-max-files`) |
| `app_server_test.exs` | Partial+ | real JSON-RPC app-server runner (`initialize` / `thread/start` / `turn/start`), workspace cwd guard, approval handling, tool-input replies, unsupported tool-call replies, buffered large-line parsing, plus live session metadata/event history |
| `dynamic_tool_test.exs` | Partial | MCP tool registry and calls implemented |
| `extensions_test.exs` | Partial+ | extension runtime added with timeout/allow-policy/arg-requirement controls |
| `observability_pubsub_test.exs` | Partial+ | in-memory event feed with cursor/limit via `/api/v1/events` |
| `status_dashboard_snapshot_test.exs` | Partial+ | dashboard snapshot endpoint `/api/v1/dashboard` |
| `specs_check_test.exs` | Partial+ | automated `spec-check` command added for local conformance component checks |
| `cli_test.exs` | Partial | CLI smoke coverage exists; no full dedicated suite |

## Most recent changes

1. Workspace/hook hardening:
   - sanitized deterministic workspace keys
   - stale path replacement
   - symlink-escape check
   - hook timeout + output on failure
2. Orchestrator status/logging:
   - richer status surface
   - structured logging with `--logs-root`
   - size-based log rotation (`--log-max-bytes`, `--log-max-files`)
   - locking/concurrency fixes in reconcile/run metrics
3. App-server compatibility checkpoint:
   - `agent.mode: app_server` now uses a real JSON-RPC protocol client
   - `initialize`, `thread/start`, `turn/start` handshake implemented
   - workspace cwd guard matches upstream safety intent
   - approval requests and tool-input prompts are handled without stalling
   - unsupported tool calls receive failure payloads instead of hanging turns
   - live session metadata + ring-buffer event history
   - observability endpoint `/api/v1/sessions`

## Remaining items / deliberate differences

1. Full Codex app-server protocol fidelity beyond the implemented handshake/reply path (for example broader dynamic-tool parity, richer event semantics, and exact fixture-level equivalence).
2. Phoenix-style dashboard/pubsub UI is not ported (HTTP observability API provided instead).
3. Test suites are mapped functionally, not a byte-for-byte transliteration of Elixir tests.
