# AR-01: Freeze runtime vocabulary and support contract

Status: Accepted
Date: 2026-03-26

## Context

Maestro currently mixes tracker vocabulary, runtime vocabulary, session vocabulary, and permission vocabulary across several code paths:

- `pkg/config/config.go` and `pkg/config/init.go` still expose `agent.mode`, `codex.approval_policy`, `codex.initial_collaboration_mode`, and the `CodexConfig` struct.
- `internal/appserver/client.go`, `internal/appserver/session.go`, `internal/appserver/interaction.go`, and `internal/appserver/protocol/builders.go` model live sessions, thread/turn IDs, and structured interaction requests and responses.
- `internal/kanban/models.go` and `internal/kanban/store.go` persist `permission_profile`, `collaboration_mode_override`, `plan_approval_pending`, and `pending_plan_markdown`.
- `internal/agent/runner.go` and `internal/orchestrator/orchestrator.go` already implement resume handling, plan approval pauses, and branch/workspace recovery.
- `internal/providers/provider.go` and `internal/providers/service.go` are tracker CRUD and sync boundaries and must stay that way.
- `apps/website/src/content/docs/workflow-config.mdx` and `docs/OPERATIONS.md` still use the legacy vocabulary.

## Decision

Maestro will use the following runtime vocabulary.

### Glossary

| Term | Product-facing meaning | Code-facing meaning |
| --- | --- | --- |
| `runtime` | The local orchestration system that starts, supervises, and records issue execution. | The `maestro run` process plus the orchestrator, agent runner, app-server bridge, live sessions, and runtime events. |
| `backend` | The concrete execution implementation the runtime drives for a session. | The backend-specific execution contract, currently Codex launch settings plus the app-server client. Future backends plug into the same runtime contract. |
| `session` | One live execution for one issue. | `internal/appserver.Session`, including thread ID, turn ID, token counts, and history. |
| `event` | Something that happened during execution. | `internal/appserver.Event` and stored runtime event records. |
| `interaction request / response` | An operator prompt or approval request and the structured answer that resolves it. | `internal/appserver.PendingInteraction` and `PendingInteractionResponse`. |
| `access profile` | The permission baseline for a project or issue. | `kanban.PermissionProfile` persisted on projects and issues. |
| `startup mode` | The initial mode for a fresh backend session. | `codex.initial_collaboration_mode` and the issue-level `collaboration_mode_override` routing for fresh sessions. |
| `approval surface` | The set of action categories that can pause execution for approval. | `codex.approval_policy` plus approval requests and resolved approval events. |
| `plan checkpoint` | A pause where the proposed plan must be reviewed before execution continues. | `plan_approval_pending`, `pending_plan_markdown`, `plan_approval_requested`, and `ApproveIssuePlan`. |
| `program line` | One durable line item in the Maestro program queue. | A `kanban.Issue` and its execution metadata. |
| `trunk branch` | The long-lived repository branch that receives merged issue work. | `agent-runtime-v2` and `origin/agent-runtime-v2` in this repository. |
| `integration branch` | The checked-out local branch used to merge and validate issue work before pushing trunk. | The local `agent-runtime-v2` worktree branch. |

### Legacy term mappings

| Current term | ADR term | Notes |
| --- | --- | --- |
| `agent.mode` | runtime transport mode | Selects `app_server` or `stdio` in the current Codex launch path. It is not a persona or agent identity selector. |
| `CodexConfig` | backend launch config | Legacy Codex-specific config block for runtime launch and collaboration settings. |
| `approval_policy` | approval surface | The serialized policy that describes what actions need approval. |
| `initial_collaboration_mode` | startup mode | The initial mode for fresh sessions. |
| `permission_profile` | access profile | The sandbox and approval baseline stored on projects and issues. |
| `collaboration_mode_override` | startup mode override | Issue-level override that changes the startup mode for a fresh session. |
| `ResumeThreadID` | session resume ID | Backend-specific handle used to resume an existing session or thread. |

## Support statement

- Claude Code is blocked until Maestro-managed approvals exist. Until then, Codex remains the only supported backend.
- `internal/providers.Provider` stays a tracker CRUD and sync interface. It must not absorb runtime, session, or approval responsibilities.
- Runtime selection is separate from future persona and agent selection. Choosing a backend does not choose an agent persona, model identity, or prompt persona.

## Non-goals

- Reworking tracker CRUD, project sync, or issue storage semantics.
- Defining future persona or model-selection behavior.
- Preserving the legacy workflow/config vocabulary indefinitely.
- Moving runtime logic into `internal/providers`.
- Introducing new persistence or new runtime nouns beyond the vocabulary needed for this cutover.

## Migration note

The current `WORKFLOW.md` keys and DB fields are compatibility shims only. `agent.mode`, `CodexConfig`, `approval_policy`, `initial_collaboration_mode`, `permission_profile`, `collaboration_mode_override`, and `ResumeThreadID` will be retired after cutover. Old workflow/config compatibility will be removed rather than maintained in parallel.
