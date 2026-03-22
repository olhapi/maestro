# Setup

Use these commands when you are preparing a repo or teaching another agent how to connect to Maestro.

## Bundle install

Install the Maestro skill bundle into the current user's personal skill directories:

```bash
maestro install --skills
```

The installer writes the skill to:

- `~/.agents/skills/maestro` for Codex
- `~/.claude/skills/maestro` for Claude Code

## Workflow bootstrap

Create or refresh the repo contract:

```bash
maestro workflow init .
```

Use `--defaults` when you want the non-interactive scaffold. Use `--force` only when overwriting an existing file is intentional.

## Launch the loop

Start the daemon after the workflow file is in place:

```bash
maestro run
```

## Attach MCP

Bridge the live daemon over stdio for a connected coding agent:

```bash
maestro mcp
```

Start `maestro run` first, then point the agent at the same database.

