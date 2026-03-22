# Operations

Use these commands when the daemon is running and you want to inspect or steer live work.

## Status and runtime

```bash
maestro status
maestro status --dashboard --api-url http://127.0.0.1:8787
maestro sessions --api-url http://127.0.0.1:8787
maestro events --api-url http://127.0.0.1:8787
maestro runtime-series --api-url http://127.0.0.1:8787
```

## When to use

- Use `status` for a quick health check.
- Use `sessions` when multiple runs are in flight.
- Use `events` when you need a recent timeline of what happened.
- Use `runtime-series` when you need token or activity trends over time.

