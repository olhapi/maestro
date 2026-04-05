# AR-01: Freeze the runtime architecture around `internal/agentruntime`

Status: Accepted
Date: 2026-03-31
Related issue: MAES-1
Depends on: AR-00

## Context

Maestro already has a runtime layer in `internal/agentruntime`. The target architecture is to extend that package for Codex and Claude, not to replace it with a parallel execution path or move runtime responsibility into the tracker layer.

Current code areas that motivate this ADR:

- `internal/agentruntime/runtime.go`, `internal/agentruntime/factory/workflow.go`, and `internal/agentruntime/codex/runtime.go` for runtime contracts and runtime startup
- `internal/providers/provider.go` and `internal/providers/service.go` for the tracker CRUD boundary
- `internal/agent/runner.go` and `internal/orchestrator/orchestrator.go` for orchestration around the runtime
- `pkg/config/config.go` and `pkg/config/init.go` for workflow configuration and approval-related defaults
- `docs/OPERATIONS.md` for repository default branch handling via `origin/HEAD`

## Decision

`internal/agentruntime` is the extension point for Codex and Claude. Future work must extend the existing runtime package and its factory/runtime contracts. It must not replace them or build a second runtime layer beside them.

`internal/providers.Provider` stays tracker CRUD only. It can validate projects and manage issues, comments, and attachments, but it is not a runtime, transport, or approval interface.

`runtime` means the Maestro-owned control loop that schedules work, owns session state, streams events, and mediates interactions and approvals.

`provider` means the concrete runtime implementation chosen for a run.

`transport` means the execution channel used by a provider, such as `app_server` or `stdio`.

`runtime name` means the named runtime option selected for a unit of work. It is the runtime selector, not `agent_name`.

`access profile` means the permission contract granted to a project or issue.

`approval surface` means the runtime-owned approval and user-input surface presented to an operator.

`plan checkpoint` means a paused point where a plan is recorded and must be accepted before execution continues.

`program line` means the complete runtime contract for a unit of work: runtime, provider, transport, runtime name, access profile, approval surface, and plan checkpoint handling.

Program-line handling is operational through the repository default branch resolved from `origin/HEAD`. Maestro does not hard-code `main` as the operational branch reference.

`agent_name` is issue metadata and not runtime selection. It may describe the work, but it must not choose the runtime name, provider, or transport.

Supported Claude runs require Maestro-managed approvals. Claude runs that cannot use the runtime-owned approval surface are out of contract.

## Glossary

| Term | Product-facing meaning | Code-facing meaning |
| --- | --- | --- |
| Runtime | The Maestro-owned control loop that schedules work, persists state, and mediates approvals and interruptions. | The runtime contracts in `internal/agentruntime`, plus the runner and orchestrator surfaces that execute and report a run. |
| Provider | The concrete runtime implementation chosen for the run. | `internal/agentruntime.Provider` plus the concrete package selected by the factory. |
| Transport | The execution channel used by a provider. | `internal/agentruntime.Transport` (`app_server`, `stdio`). |
| Runtime name | The named runtime option selected for the run. | The selector value that chooses a provider; it is not `agent_name`. |
| Session | One contiguous execution context for an issue or attempt. | `internal/appserver/session.go` `Session`, plus the thread identity used to continue it. |
| Event | An immutable record that something changed in the runtime. | `internal/appserver/activity.go` `ActivityEvent` and related runtime event records. |
| Interaction request/response | A pending approval or input request and the response that resolves it. | `PendingInteraction` and `PendingInteractionResponse` in `internal/appserver/interaction.go`. |
| Access profile | The permission contract granted to a project or issue. | The `permission_profile` field and related normalization in `internal/kanban/models.go` and `internal/kanban/store.go`. |
| Startup mode | The initial posture for a fresh session. | The `initial_collaboration_mode` value, plus `collaboration_mode_override` for per-issue overrides. |
| Approval surface | The runtime-owned approval and user-input surface presented to the operator. | The approval policy config, pending approval payloads, and interrupt plumbing in `pkg/config`, `internal/appserver`, and `internal/orchestrator`. |
| Plan checkpoint | A paused point where a proposed plan is recorded and must be accepted before execution continues. | Plan state such as `plan_approval_pending` and `pending_plan_markdown`. |
| Program line | The chosen runtime contract for a unit of work. | The combination of runtime, provider, transport, runtime name, access profile, approval surface, and plan checkpoint handling. |
| Default branch | The repository branch that receives operational branch context. | The ref pointed to by `origin/HEAD`. |

## Current term mappings

| Current term | ADR term | Notes |
| --- | --- | --- |
| `agent.mode` | Transport selection | Legacy config key that currently chooses `app_server` or `stdio`. It is not runtime selection. |
| `CodexConfig` | Runtime configuration | Legacy config block for the Codex runtime today. |
| `approval_policy` | Approval surface policy | Legacy config key that controls what the runtime exposes on the approval surface. |
| `initial_collaboration_mode` | Initial runtime posture | Legacy config key for the initial posture of a fresh session. |
| `permission_profile` | Access profile | Existing project and issue permission contract. |
| `collaboration_mode_override` | Initial runtime posture override | Per-issue override for the fresh-session posture. |
| `agent_name` | Issue metadata | Human-facing label only; not runtime selection. |
| `ResumeThreadID` | Session continuation thread id | Persisted thread identifier used to resume a session. It is not a user-facing concept. |

## Support Statement

- Supported: Codex-backed runs through `internal/agentruntime`, and Claude-backed runs when Maestro-managed approvals own the approval surface.
- Boundary: `internal/providers.Provider` stays tracker CRUD only.
- Separation: runtime selection is not `agent_name`, and runtime name is not a repository branch.

## Non-goals

- Creating a second runtime layer beside `internal/agentruntime`
- Expanding `internal/providers.Provider` into runtime, transport, or approval logic
- Treating `agent_name` as runtime selection
- Hard-coding branch names instead of resolving `origin/HEAD`
- Defining a persona system or agent identity model
- Renaming every code symbol in this issue

## Migration Note

Future implementation work should use the ADR terms above. Legacy words may remain in existing code during migration, but they should not become the terminology for new tasks, tickets, or docs.

## References

- `internal/agentruntime/runtime.go`
- `internal/agentruntime/factory/workflow.go`
- `internal/agentruntime/codex/runtime.go`
- `internal/providers/provider.go`
- `internal/providers/service.go`
- `internal/agent/runner.go`
- `internal/orchestrator/orchestrator.go`
- `pkg/config/config.go`
- `pkg/config/init.go`
- `docs/OPERATIONS.md`
