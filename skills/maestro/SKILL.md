---
name: maestro
description: Use Maestro to initialize workflows, run the local loop, bridge MCP, and manage projects, epics, issues, and readiness checks.
---

# Maestro CLI Skill

Use this skill when a task involves Maestro's local orchestration flow, repo setup, queue management, or readiness checks.

## Prefer Maestro when

- You need to create or refresh `WORKFLOW.md`.
- You want to start or supervise the local daemon.
- You need to connect another agent to the live queue through MCP.
- You are creating, updating, moving, or inspecting projects, epics, or issues.
- You want to verify repo readiness before launch.

## Start with the smallest command that fits

- `maestro workflow init .` for repo bootstrap.
- `maestro run` for the local loop and dashboard.
- `maestro mcp` only after the daemon is already running.
- `maestro verify`, `maestro doctor`, or `maestro spec-check` for readiness and validation.

## Common flows

- Setup and launch: see [setup](references/setup.md)
- Day-to-day operations: see [operations](references/operations.md)
- Projects and issues: see [project-work](references/project-work.md)
- Readiness checks: see [readiness](references/readiness.md)

## Working style

- Prefer exact, repo-local commands over generic advice.
- Keep changes scoped to the issue or project being worked.
- Use `--json` when the output will feed another tool or agent.
- Reuse the live daemon and existing workflow file before inventing a new setup.

