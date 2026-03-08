# Upstream Crosswalk (openai/symphony → symphony-go)

Checked against upstream test modules in `elixir/test/symphony_elixir`.

## Module-by-module mapping

1. `app_server_test.exs`
   - Status: **Partial+**
   - Implemented: app_server mode, JSON event parsing, session lifecycle metadata, history ring buffer, `/api/v1/sessions`.
   - Evidence: `internal/appserver/*`, `internal/agent/runner.go`, `internal/observability/server.go`.

2. `cli_test.exs`
   - Status: **Partial**
   - Implemented: robust CLI commands for run/mcp/board/issue/project/status/verify/spec-check.
   - Evidence: `cmd/symphony/main.go`.

3. `core_test.exs`
   - Status: **Partial+**
   - Implemented: config defaults, workflow loading, validation checks in tests.
   - Evidence: `pkg/config/*`.

4. `dynamic_tool_test.exs`
   - Status: **Partial+**
   - Implemented: MCP dynamic extension tools loaded from JSON; runtime execution path.
   - Evidence: `internal/mcp/server.go`, `internal/mcp/server_test.go`.

5. `extensions_test.exs`
   - Status: **Partial+**
   - Implemented: extension runtime policy controls (`allowed`, `timeout_sec`, `require_args`, `working_dir`, `deny_env_passthrough`).
   - Evidence: `internal/mcp/server.go`, tests.

6. `log_file_test.exs`
   - Status: **Partial+**
   - Implemented: `--logs-root` JSON logs file sink + stdout.
   - Evidence: `cmd/symphony/main.go`.

7. `observability_pubsub_test.exs`
   - Status: **Partial**
   - Implemented: HTTP observability APIs (`/health`, `/api/v1/state`, `/api/v1/sessions`), no pubsub bus.
   - Evidence: `internal/observability/*`.

8. `orchestrator_status_test.exs`
   - Status: **Partial+**
   - Implemented: rich status including uptime, metrics, retry queue, sessions.
   - Evidence: `internal/orchestrator/orchestrator.go`.

9. `specs_check_test.exs`
   - Status: **Partial+**
   - Implemented: `spec-check` command for local conformance component checks.
   - Evidence: `internal/speccheck/*`, CLI `spec-check`.

10. `status_dashboard_snapshot_test.exs`
   - Status: **Not ported (deliberate)**
   - Notes: no Phoenix dashboard snapshot parity; replaced with HTTP JSON API.

11. `workspace_and_config_test.exs`
   - Status: **Partial+**
   - Implemented: deterministic workspace naming, stale file replacement, symlink escape checks, hook timeout/failure handling.
   - Evidence: `internal/agent/runner.go`, tests.

## Practical completion statement

For the requested scope (**excluding Linear integration**), symphony-go is functionally complete and verified with passing tests and working APIs.

Remaining differences are mostly architectural/UI parity differences with Elixir/Phoenix (dashboard snapshots, pubsub implementation, exact protocol-fidelity edge cases).