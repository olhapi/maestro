# AR-02: Persist explicit plan blocks and provider session UUIDs for Claude planning

Status: Accepted
Date: 2026-03-31
Related issue: MAES-3
Depends on: AR-01

## Context

Maestro needs a stable way to run Claude in plan mode, capture the plan checkpoint, and resume after a pause or process restart.

I probed `claude -p` locally on 2026-03-31 with the non-bare CLI, including `--output-format=text`, `--output-format=json`, and `--output-format=stream-json`.
The stream-json path exposed assistant/tool-use scaffolding before the plan payload.
In a later constrained probe with `--allowed-tools ''`, the text path returned the requested `<proposed_plan>` block cleanly, which confirms the explicit block is the stable payload boundary.

That means the plan payload must come from an explicit marker in the final assistant message rather than from the stream envelope.

## Decision

Maestro treats a single explicit `<proposed_plan>...</proposed_plan>` block in the final assistant message as the authoritative plan payload.
Structured `stream-json` output is useful for diagnostics and transport, but it is not the plan source of truth.

For session persistence, Maestro stores the provider-specific durable resume token.
For Claude Code that token is the CLI session UUID used for `--resume`/`-r`.
Same-process continuation reuses the live in-memory session, while fresh-process resume rehydrates from the persisted session UUID.

`turn_id` is lineage metadata, not the cross-process resume key.
If Claude exposes a turn identifier, Maestro stores it alongside the plan version so that version lineage remains traceable across revisions.

## Metadata To Persist

Maestro must persist the following planning metadata for each checkpoint:

- `session_id`: the durable provider session identifier
- `turn_id`: optional turn lineage
- `version_number`: monotonically increasing plan version within the planning session
- `markdown`: the canonical plan body extracted from `<proposed_plan>`
- `revision_note`: the latest requested revision text, when present
- `attempt`: the execution attempt that produced the plan version
- `created_at`: the timestamp for the plan version

## Consequences

- The plan parser stays simple and deterministic.
- Resume behavior stays aligned with the provider's native session model.
- Maestro can resume a paused plan without reconstructing the plan payload from stream event order.
