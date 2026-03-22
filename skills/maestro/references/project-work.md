# Projects and issues

Use these commands for the local queue and issue lifecycle.

## Projects

```bash
maestro project create <name> --repo <repo_path>
maestro project list
maestro project show <id>
maestro project update <id>
maestro project delete <id>
```

## Epics

```bash
maestro epic create <name> --project <project_id>
maestro epic list
maestro epic show <id>
maestro epic update <id>
maestro epic delete <id>
```

## Issues

```bash
maestro issue create <title>
maestro issue list
maestro issue show <identifier>
maestro issue update <identifier>
maestro issue move <identifier> <state>
maestro issue block <identifier> <blocker_identifier...>
maestro issue unblock <identifier> <blocker_identifier...>
maestro issue comments list <identifier>
maestro issue assets add <identifier> <path>
```

## Guidance

- Use `--project` and labels to keep work scoped.
- Use `move` when you need to change queue state, not when you only want to update metadata.
- Use comments and assets for review context rather than burying details in the issue title.

