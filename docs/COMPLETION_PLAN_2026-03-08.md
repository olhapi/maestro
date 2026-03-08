# Completion Plan — 2026-03-08

Goal: close high-value missing parity parts synchronously and verify end-to-end.

## Plan

1. [x] Improve log-file parity
   - [x] add file rotation by size
   - [x] add max rotated files policy
   - [x] add tests for rotation behavior
   - [x] expose CLI controls (`--log-max-bytes`, `--log-max-files`)

2. [x] Harden app-server event parsing parity
   - [x] support nested event envelopes (`event`, `data`, `payload`)
   - [x] support alternate naming (`threadId`, `turnId`)
   - [x] support nested usage token extraction
   - [x] add parser tests for these cases

3. [x] Verify runtime + docs
   - [x] `go test ./...`
   - [x] `go build`
   - [x] `spec-check` + `verify`
   - [x] smoke check new/updated runtime behavior
   - [x] update parity and handoff docs

## Remaining after this pass

- Full pubsub transport semantics (beyond HTTP cursor feed)
- Full dashboard UI parity/snapshot equivalence vs upstream Phoenix surfaces
- Deeper app-server protocol edge cases and fixture parity suite
- Full upstream test transliteration (currently functional mapping)
