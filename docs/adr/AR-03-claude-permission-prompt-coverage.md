# AR-03: Claude permission-prompt coverage is incomplete for Maestro's control model

Status: Accepted
Date: 2026-04-01
Related issue: MAES-2
Depends on: AR-02

## Context

I ran `scripts/claude-permission-prompt-spike.mjs` against `claude -p` in non-bare mode with:

- `--model sonnet`
- `--allowed-tools Bash,Edit,Write,MultiEdit`
- `--permission-prompt-tool mcp__permission-spy__approval_prompt`
- a local stdio MCP server that records the raw transport

Claude Code 2.1.85 in this setup uses JSONL transport, not Content-Length framing. The raw MCP handshake was:

- `initialize`
- `notifications/initialized`
- `tools/list`
- `tools/call`

The server response must advertise `capabilities.tools.listChanged: true` for Claude to proceed to the tool list and approval callbacks.

The permission-prompt MCP tool is always called as `approval_prompt`. The approval class is carried in `arguments.tool_name`, and the foreign key is carried in `arguments.tool_use_id` plus `_meta.claudecode/toolUseId`.

## Raw Payloads

Command execution approval:

```json
{
  "tool_name": "Bash",
  "input": {
    "command": "pwd && git status --short",
    "description": "Run pwd and git status --short"
  },
  "tool_use_id": "toolu_016Hyh6SUiGCih1vdA16WHMk"
}
```

File modification approval:

```json
{
  "tool_name": "Write",
  "input": {
    "file_path": "/.../workspace/notes.txt",
    "content": "file-change-ok\n"
  },
  "tool_use_id": "toolu_01LLPcEG4aPwyAoCMXnJj45A"
}
```

Protected `.git` write approval:

```json
{
  "tool_name": "Edit",
  "input": {
    "file_path": "/.../workspace/.git/config",
    "old_string": "[core]",
    "new_string": "# spike\n[core]",
    "replace_all": false
  },
  "tool_use_id": "toolu_01FqaiLLDUTFvuydqs3Yjhde"
}
```

User-input interruption:

- No `tools/call` was emitted.
- Claude returned a plain-text clarification instead: `What should the note in \`questions.txt\` say? I don't want to assume the content.`

## Coverage Matrix

| Interaction class | Verdict | Notes |
| --- | --- | --- |
| Command execution | Partial | The callback includes the command and `tool_use_id`, but not an explicit `cwd`. Maestro can correlate the approval, but the raw payload does not fully satisfy the normalized approval model on its own. |
| File modification | Supported | The callback includes the target file path and content. |
| Protected `.git` write | Supported | The callback includes the edit target and replacement text. Deny behavior is represented in Claude's final `permission_denials` result. |
| User-input interruption | Unsupported | Claude does not surface this as a permission-prompt callback. There is no structured question id, no options array, and no deny/resume metadata. |

## Verdict

No-go for full Maestro parity.

Claude's permission-prompt tool is enough to mediate approval-class interactions, but it does not cover structured user-input interruptions, so it cannot be the only interaction surface for Maestro.

## References

- `scripts/claude-permission-prompt-spike.mjs`
- `.maestro/claude-permission-prompt-spike/run-2026-04-01T08-26-42-772Z-717339/report.json`
- `.maestro/claude-permission-prompt-spike/run-2026-04-01T08-26-42-772Z-717339/command-exec/tool-calls.jsonl`
- `.maestro/claude-permission-prompt-spike/run-2026-04-01T08-26-42-772Z-717339/file-modification/tool-calls.jsonl`
- `.maestro/claude-permission-prompt-spike/run-2026-04-01T08-26-42-772Z-717339/protected-git-write/tool-calls.jsonl`
- `.maestro/claude-permission-prompt-spike/run-2026-04-01T08-26-42-772Z-717339/user-input-interruption/tool-calls.jsonl`
