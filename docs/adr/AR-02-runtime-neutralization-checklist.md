# AR-02: Freeze the runtime-neutralization checklist

Status: Frozen
Date: 2026-04-01
Related issue: MAES-4
Depends on: AR-01

## Purpose

RT-04 audited the repository for Codex-shaped leakage and froze the checklist below before any neutralization code changes. RT-06 through RT-12 should cite this document directly rather than redoing the inventory.

The rule of thumb is simple:

- anything in `must neutralize` becomes a first-class runtime-neutral surface
- anything in `provider metadata` may stay codex-branded only inside the adapter boundary
- anything in `docs/copy` is a wording change unless it also serializes into a config or API field

## Must Neutralize

| Surface | Current Codex form | Classification | Decision |
| --- | --- | --- | --- |
| Workflow schema | `WORKFLOW.md` `codex:` block, plus `command`, `expected_version`, `approval_policy`, `initial_collaboration_mode`, `turn_timeout_ms`, `read_timeout_ms`, `stall_timeout_ms` | persisted-schema change | Replace with neutral runtime/workflow fields. Keep legacy read-path aliases only long enough to migrate old workflow files. |
| Workflow aliases | `codex_command`, `codex_expected_version`, `codex_approval_policy`, `codex_initial_collaboration_mode`, `codex_turn_timeout_ms`, `codex_read_timeout_ms`, `codex_stall_timeout_ms`, `codex_thread_sandbox`, `codex_turn_sandbox_policy` | persisted-schema change | Keep as compatibility shims only. They are not the long-term schema. |
| CLI flag | `--codex-command` in `cmd/maestro/root.go` | public API change | Rename to a neutral launch-command flag. |
| Verification output | `codex_version` in `internal/verification/verify.go` | public API change | Rename the check key to a neutral runtime/version name. |
| Spec-check output | `codex_schema_json` in `internal/speccheck/check.go` | public API change | Rename the check key to a neutral schema-validation name. |
| Runtime snapshot JSON | `codex_totals` in `internal/observability/model.go`, `internal/observability/presenter.go`, `internal/observability/dashboard.go`, `internal/orchestrator/orchestrator.go`, `cmd/maestro/output.go`, `apps/frontend/src/lib/types.ts`, and `apps/frontend/src/routes/overview.tsx` | public API change | Replace with a neutral totals field. The Go struct names can be renamed in the same pass. |
| Session JSON | `codex_app_server_pid` in `internal/agentruntime/session.go`, `internal/orchestrator/orchestrator.go`, and `apps/frontend/src/lib/types.ts` | public API change | Replace with a neutral session/process field. |
| Issue payload JSON | `codex_session_logs` in `internal/observability/presenter.go` | public API change | Rename to a neutral session-log field. |
| Branch default | `codex/<issue>` in `internal/agent/runner.go` | public API change, persisted branch naming | Replace the provider-prefixed fallback branch name with a neutral issue branch name. |
| User-facing runtime copy | `Codex` wording in `cmd/maestro/helpers.go`, `cmd/maestro/install.go`, `internal/appserver/client.go`, `apps/frontend/src/components/dashboard/global-interrupt-panel.tsx`, `apps/frontend/src/components/dashboard/elicitation-form.tsx`, `apps/frontend/src/components/dashboard/session-execution-card.tsx`, `apps/website/src/components/react/McpSetupAccordion.tsx`, `apps/website/public/images/screens/architecture-runtime.svg`, and `skills/maestro/references/setup.md` | docs/copy change | Rewrite copy that describes Maestro behavior or the UI, unless the sentence is intentionally about the vendor CLI itself. |
| Workflow template copy | `Codex command`, `Codex CLI launch and collaboration settings`, `Codex` comments in `pkg/config/init.go`, `cmd/maestro/root.go`, and `WORKFLOW.md` | docs/copy change, persisted-schema change | Rewrite the scaffold text so the repo-local contract does not hard-code the provider name. |
| Config validation and advisory copy | `codex.approval_policy=never`, `codex.initial_collaboration_mode`, and the legacy sandbox warnings/errors in `pkg/config/config.go` | docs/copy change | Reword the validation and advisory text so it describes the runtime neutrally. |
| README and docs prose | `README.md`, `docs/OPERATIONS.md`, `docs/E2E_REAL_CODEX.md`, `docs/adr/AR-01-runtime-vocabulary-and-support-contract.md`, `apps/website/src/content/docs/install.mdx`, `apps/website/src/content/docs/quickstart.mdx`, `apps/website/src/content/docs/architecture.mdx`, `apps/website/src/content/docs/cli-reference.mdx`, `apps/website/src/content/docs/workflow-config.mdx`, and `apps/website/src/content/docs/advanced/e2e-harness.mdx` | docs-only change | Reword to neutral runtime language where the text is describing Maestro behavior rather than the upstream Codex CLI. |
| Harness and maintenance names | `scripts/e2e_real_codex.sh`, `scripts/e2e_real_codex_phases.sh`, `scripts/e2e_real_codex_issue_images.sh`, `scripts/update_codex_schemas.sh`, `apps/website/scripts/smoke.mjs`, and `Makefile` targets `e2e-real-codex*` | docs/copy change | Keep the vendor-specific names only if the file is intentionally about the real Codex CLI. Otherwise rename the harness wording to neutral runtime language. |

## Internal-Only Rename

These symbols do not cross a public boundary by themselves, but they should be renamed as part of the neutralization work so the codebase vocabulary stops leaking through internal types and helpers:

- `CodexConfig` in `pkg/config/config.go`
- `CodexCommand` fields in `pkg/config/init.go`, `cmd/maestro/root.go`, `internal/appserver/client.go`, `internal/agentruntime/factory/workflow.go`, and `internal/agentruntime/codex/runtime.go`
- `CodexTotals` Go struct names in `internal/observability/model.go`
- `CodexAppServerPID` Go struct names in `internal/observability/model.go` and `internal/agentruntime/session.go`
- `ProviderCodex` in `internal/agentruntime/runtime.go`
- `CodexVersionStatus`, `DetectCodexVersion`, `codexExecutableFromCommand`, `codexVersionCache`, and `codexVersionPattern` in `internal/appserver/version.go`
- `warnOnCodexVersionMismatch` and `looksLikeCodexCommand` in `internal/appserver/client.go`
- import aliases such as `codexruntime` in the runtime, agent, orchestrator, verification, and spec-check packages

Decision:

- Rename these symbols in the same implementation waves that neutralize the public and persisted surfaces.
- Do not treat these names as the canonical API; they are implementation details only.

## Provider Metadata

These items can stay codex-branded because they are adapter-local metadata or vendor schema bindings, not Maestro public surfaces:

- runtime metadata fields `provider: "codex"` and `transport` in `internal/agentruntime/codex/runtime.go`, `internal/agentruntime/codex/stdio.go`, and the runtime/session/activity adapters
- the adapter packages `internal/agentruntime/codex/*` and `internal/codexschema/*`
- the vendor schema directory `schemas/codex/*`
- vendor schema identifiers in `internal/appserver/protocol/gen/models.go`, including `CodexHome`, `codexHome`, `CodexErrorInfo`, `codexErrorInfo`, `CodexErrorInfoEnum`, `CodexErrorInfoOther`, and `CodexAppServerProtocol`
- adapter-local helpers such as `CodexVersionStatus`, `DetectCodexVersion`, `codexExecutableFromCommand`, `codexVersionCache`, and `codexVersionPattern`
- `codexschema.SupportedVersion` and the schema-maintenance path names in `scripts/update_codex_schemas.sh`

## Frozen Wording Notes

- Keep the provider metadata inside the adapter boundary.
- Do not introduce new Codex-specific public JSON keys, CLI flags, or workflow keys.
- When a later RT neutralizes one of the items above, add or update the relevant test in the same change.
- If a future change needs to preserve a provider-specific label for compatibility, make that label a shim, not the canonical surface.

## Reference Set

Use this checklist as the canonical inventory for follow-on work:

- config schema and init scaffolding
- CLI flags and verification output
- runtime/session/dashboard JSON
- branch naming
- user-facing copy and docs
- provider metadata and upstream Codex schema bindings
