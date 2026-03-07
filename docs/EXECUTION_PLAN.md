# Execution Plan to Finish Remaining Parity Gaps

Date: 2026-03-07

## Objective
Reach practical completion for Symphony-Go parity (excluding Linear integration), with concrete verifiable APIs and runtime surfaces.

## Remaining Gaps
1. App-server deep session visibility (event history endpoint)
2. Observability API expansion for sessions
3. Stronger extension/runtime safety controls
4. More explicit parity verification matrix updates

## Work Plan (synchronous)

### Phase A — App-server + Observability completion
- [x] Add app-server event history ring buffer per live session
- [x] Expose `/api/v1/sessions` endpoint
- [x] Add tests for session parsing/history and sessions API

### Phase B — Extension/runtime hardening
- [x] Add extension command timeout
- [x] Add optional extension allowlist
- [x] Return normalized extension execution error payloads
- [x] Add tests for timeout and allowlist

### Phase C — Verification & report
- [ ] Full test run + build
- [ ] Update `PARITY_REPORT.md` with completed deltas
- [ ] Final checklist against upstream test-module matrix

## Done Definition
- All tests pass
- Build passes
- APIs verifiably respond:
  - `/health`
  - `/api/v1/state`
  - `/api/v1/sessions`
- Parity report updated with explicit remaining/non-remaining items
