# Parity Report (Checkpoint)

Date: 2026-03-07

This report tracks parity against `openai/symphony` Elixir implementation, excluding Linear integration by design.

## Status Summary

- Core orchestration loop: **implemented**
- Local tracker replacement (Kanban+MCP): **implemented**
- Workspace lifecycle safety: **implemented (practical parity)**
- App-server compatibility: **implemented (event/session tracking parity baseline)**
- Observability/status: **implemented (state + sessions APIs)**
- Extensions runtime: **implemented with safety controls**

## Matrix

| Upstream area | Status | Notes |
|---|---|---|
| `core_test.exs` | Partial | workflow/config defaults + validations exist; not full option parity |
| `workspace_and_config_test.exs` | Partial+ | deterministic keys, stale path recovery, hook timeout support, symlink escape checks |
| `orchestrator_status_test.exs` | Partial+ | richer status fields and run metrics implemented |
| `log_file_test.exs` | Partial | `--logs-root` JSON logs to file+stdout |
| `app_server_test.exs` | Partial+ | `agent.mode=app_server` + JSON event parsing + live session metadata tracking (`session_id`, tokens, last_event) + terminal semantics and event history |
| `dynamic_tool_test.exs` | Partial | MCP tool registry and calls implemented |
| `extensions_test.exs` | Partial+ | extension runtime added with timeout/allow-policy/arg-requirement controls |
| `observability_pubsub_test.exs` | Not yet | event bus not added |
| `status_dashboard_snapshot_test.exs` | Not yet | no HTTP dashboard yet |
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
   - locking/concurrency fixes in reconcile/run metrics
3. App-server compatibility checkpoint:
   - `agent.mode: app_server` accepted
   - JSON event parsing for app-server lines
   - live session metadata + ring-buffer event history
   - observability endpoint `/api/v1/sessions`

## Remaining items / deliberate differences

1. Full Codex app-server protocol fidelity beyond JSON line event envelopes (current implementation tracks event/session metadata via subprocess stream parsing).
2. Phoenix-style dashboard/pubsub UI is not ported (HTTP observability API provided instead).
3. Test suites are mapped functionally, not a byte-for-byte transliteration of Elixir tests.
