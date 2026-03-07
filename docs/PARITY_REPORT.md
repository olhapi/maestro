# Parity Report (Checkpoint)

Date: 2026-03-07

This report tracks parity against `openai/symphony` Elixir implementation, excluding Linear integration by design.

## Status Summary

- Core orchestration loop: **implemented**
- Local tracker replacement (Kanban+MCP): **implemented**
- Workspace lifecycle safety: **partially implemented (improved)**
- App-server compatibility: **partial compatibility mode added**
- Observability/status: **improved, partial parity**
- Web dashboard/pubsub/extensions: **not yet implemented**

## Matrix

| Upstream area | Status | Notes |
|---|---|---|
| `core_test.exs` | Partial | workflow/config defaults + validations exist; not full option parity |
| `workspace_and_config_test.exs` | Partial+ | deterministic keys, stale path recovery, hook timeout support, symlink escape checks |
| `orchestrator_status_test.exs` | Partial+ | richer status fields and run metrics implemented |
| `log_file_test.exs` | Partial | `--logs-root` JSON logs to file+stdout |
| `app_server_test.exs` | Partial+ | `agent.mode=app_server` + JSON event parsing + live session metadata tracking (`session_id`, tokens, last_event) |
| `dynamic_tool_test.exs` | Partial | MCP tool registry and calls implemented |
| `extensions_test.exs` | Not yet | extension runtime not added |
| `observability_pubsub_test.exs` | Not yet | event bus not added |
| `status_dashboard_snapshot_test.exs` | Not yet | no HTTP dashboard yet |
| `specs_check_test.exs` | Partial | manual SPEC alignment; no automatic spec conformance checks |
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

## Remaining high-priority items

1. Real Codex app-server protocol integration (event-driven session tracking)
2. Event stream/pubsub and optional HTTP status API
3. Extensions runtime parity and dynamic tool lifecycle parity
4. Automated parity tests mapped 1:1 from upstream test modules
