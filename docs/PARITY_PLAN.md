# Symphony-Go Parity Plan (vs openai/symphony elixir)

Scope: match upstream behavior except Linear integration (replaced by local Kanban + MCP).

## Upstream test modules mapped

- [~] `app_server_test.exs` → Go: codex app-server JSON-RPC protocol compatibility tests (handshake + approvals/tool replies implemented, full fixture parity pending)
- [x] `cli_test.exs` → Go: CLI smoke + arg parsing tests (basic)
- [~] `core_test.exs` → Go: config/workflow defaults + validation (partial)
- [~] `dynamic_tool_test.exs` → Go: MCP dynamic tool behavior (partial)
- [ ] `extensions_test.exs` → Go: extension loading and runtime hooks parity
- [ ] `log_file_test.exs` → Go: structured log file rotation and content assertions
- [~] `observability_pubsub_test.exs` → Go: runtime event feed parity (`/api/v1/events`), full pubsub semantics pending
- [~] `orchestrator_status_test.exs` → Go: status fields (partial)
- [~] `specs_check_test.exs` → Go: spec conformance checks (partial)
- [~] `status_dashboard_snapshot_test.exs` → Go: dashboard snapshot API (`/api/v1/dashboard`), full UI parity pending
- [~] `workspace_and_config_test.exs` → Go: workspace semantics (partial)

## Phase 1 — hard gaps to close first

1. Codex app-server protocol runner
   - [x] JSON-RPC handshake (`initialize`, `thread/start`, `turn/start`)
   - [x] workspace cwd guard
   - [x] approval and tool-input reply handling
   - [x] dynamic tool call reply path
   - [~] full upstream fixture parity + event/log fidelity
2. Workspace hardening parity
   - symlink escape checks
   - stale path replacement semantics
   - hook timeout/error propagation parity
3. Orchestrator status parity surface (JSON)
4. Structured log output + file sink

## Phase 2 — observability and extensions

5. Event stream / pubsub surface
6. Status dashboard/API (lightweight HTTP in Go)
7. Extension + dynamic tools runtime parity

## Acceptance criteria

- All local Go tests pass
- Add parity tests corresponding to each upstream test module
- Produce `PARITY_REPORT.md` with pass/fail matrix

## Current baseline

- Core orchestrator + kanban + MCP: working
- Build/tests: passing
- Parity level: **functional core**, not full upstream-equivalent
