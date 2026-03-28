# AR-01: Freeze runtime vocabulary and support contract

Status: Accepted
Date: 2026-03-28
Related issue: MAES-1
Depends on: AR-00

## Context

The repo currently mixes tracker, provider, workflow, Codex, permission, session, and approval terms across config, runtime, and public docs.

Current code areas that motivate this ADR:

- `pkg/config/config.go` and `pkg/config/init.go` for `agent.mode`, `CodexConfig`, `approval_policy`, and `initial_collaboration_mode`
- `internal/kanban/models.go` and `internal/kanban/store.go` for `permission_profile`, `collaboration_mode_override`, and `ResumeThreadID`
- `internal/appserver/session.go`, `internal/appserver/activity.go`, `internal/appserver/interaction.go`, and `internal/appserver/client.go` for session, event, and interaction plumbing
- `internal/orchestrator/orchestrator.go` and `internal/agent/runner.go` for runtime flow, plan handling, and thread resumption
- `internal/providers/provider.go`, `internal/providers/service.go`, and `internal/providers/kanban_provider.go` for the tracker CRUD boundary
- `README.md` and `apps/website/src/content/docs/workflow-config.mdx` for the public vocabulary that still blends runtime, workflow, and permission terms

## Decision

Maestro will use a neutral runtime vocabulary for product and design discussion. The code may still carry legacy names during the transition, but future implementation work must use the ADR terms below.

The runtime owns the execution loop, session state, event stream, and interaction handling. A backend is the concrete execution engine that the runtime drives. Runtime selection is separate from future persona or agent selection.

`internal/providers.Provider` remains tracker CRUD only. It is not a runtime backend interface and must not absorb runtime, session, or approval responsibilities.

`program line` means the selected runtime/backend contract for a unit of work, including its access profile, startup mode, and approval surface. It is not a repository branch and it is not a persona.

`trunk branch` means the repository's long-lived integration branch. In this repo that is `main` / `origin/main`.

`integration branch` means a short-lived branch created from trunk for a single issue or change, then merged back after validation. `maestro/MAES-1` is the current example.

## Glossary

| Term | Product-facing meaning | Code-facing meaning |
| --- | --- | --- |
| Runtime | The Maestro-owned control loop that schedules work, persists state, and mediates approvals and interruptions. | The orchestrator, agent runner, appserver client, and observability surfaces that execute and report a run. |
| Backend | The concrete execution engine chosen by the runtime for a program line. | Today this is Codex-backed execution; future backends are separate from persona selection. |
| Session | One contiguous execution context for an issue or attempt. | `internal/appserver/session.go` `Session`, plus the backend thread identity used to continue it. |
| Event | An immutable record that something changed in the runtime or backend. | `internal/appserver/activity.go` `ActivityEvent` and the related runtime event records in the store. |
| Interaction request/response | A pending approval or input request and the response that resolves it. | `PendingInteraction` and `PendingInteractionResponse` in `internal/appserver/interaction.go`. |
| Access profile | The permission contract granted to a project or issue. | The `permission_profile` field and related normalization in `internal/kanban/models.go` and `internal/kanban/store.go`. |
| Startup mode | The initial posture for a fresh session. | The `initial_collaboration_mode` value, plus `collaboration_mode_override` for per-issue overrides. |
| Approval surface | The runtime-owned approval and user-input surface presented to the operator. | The approval policy config, pending approval payloads, and interrupt plumbing in `pkg/config`, `internal/appserver`, and `internal/orchestrator`. |
| Plan checkpoint | A paused point where a proposed plan is recorded and must be accepted before execution continues. | Plan state such as `plan_approval_pending` and `pending_plan_markdown`. |
| Program line | The chosen runtime/backend contract for a unit of work. | The combination of runtime selection, backend selection, access profile, startup mode, and approval surface. |
| Trunk branch | The long-lived integration branch for the repository. | `main` / `origin/main`. |
| Integration branch | A short-lived issue branch used to stage one change before merging to trunk. | A branch such as `maestro/MAES-1`. |

## Current term mappings

| Current term | ADR term | Notes |
| --- | --- | --- |
| `agent.mode` | Runtime/backend selection | Legacy config key that currently chooses `app_server` or `stdio`. Treat it as implementation detail for runtime/backend selection, not as a persona selector. |
| `CodexConfig` | Backend configuration | Legacy config block for Codex-backed execution settings. |
| `approval_policy` | Approval surface policy | Legacy config key that controls what the runtime exposes on the approval surface. |
| `initial_collaboration_mode` | Startup mode | Legacy config key for the initial posture of a fresh session. |
| `permission_profile` | Access profile | Existing project and issue permission contract. |
| `collaboration_mode_override` | Startup mode override | Per-issue override for the fresh-session startup posture. |
| `ResumeThreadID` | Session continuation thread id | Persisted backend thread identifier used to resume a session. It is not a user-facing concept. |

## Support Statement

- Supported: Maestro-owned Codex execution where the runtime can own the session, event, interaction, and approval surfaces end to end.
- Blocked: Claude Code until Maestro-managed approvals exist. Until the runtime owns approvals, Claude is not a supported backend for this product contract.
- Boundary: `internal/providers.Provider` stays tracker CRUD only.
- Separation: runtime selection is not persona selection. A future persona or agent layer may exist later, but it must not be conflated with backend choice or approval ownership.

## Non-goals

- Renaming every code symbol in this issue
- Preserving old workflow/config compatibility forever
- Extending `internal/providers.Provider` into a runtime or backend abstraction
- Defining a persona system or agent identity model
- Changing runtime behavior beyond freezing the vocabulary and support contract

## Migration Note

The old workflow/config vocabulary is transitional. Compatibility with the legacy names will be removed rather than maintained indefinitely.

Future implementation work should treat this ADR as the source of truth for:

- runtime
- backend
- session
- event
- interaction request/response
- access profile
- startup mode
- approval surface
- plan checkpoint
- program line
- trunk branch
- integration branch

## References

- `pkg/config/config.go`
- `pkg/config/init.go`
- `internal/appserver/session.go`
- `internal/appserver/activity.go`
- `internal/appserver/interaction.go`
- `internal/appserver/client.go`
- `internal/orchestrator/orchestrator.go`
- `internal/agent/runner.go`
- `internal/kanban/models.go`
- `internal/kanban/store.go`
- `internal/providers/provider.go`
- `internal/providers/service.go`
- `internal/providers/kanban_provider.go`
- `README.md`
- `apps/website/src/content/docs/workflow-config.mdx`
