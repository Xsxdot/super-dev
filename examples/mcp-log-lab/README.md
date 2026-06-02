# MCP Log Lab

`mcp-log-lab` is a local SuperDev fixture for validating MCP runtime and log diagnostics. It is intentionally small: one Go binary runs four roles that produce predictable stdout and stderr logs.

## Roles

Run from this directory:

```sh
go run . --role api --port 18190
go run . --role worker
go run . --role noisy
go run . --role crasher
```

- `api` starts an HTTP service on `127.0.0.1:18190` with `/health` and `/work`.
- `worker` simulates queue jobs and retry warnings.
- `noisy` emits frequent `HEARTBEAT` debug logs.
- `crasher` emits a deterministic failure sequence and exits with code `2`.

## Stable Search Markers

Use these markers when validating MCP tools:

```text
trace_id=mcp-lab-target
request_id=req-mcp-lab-001
database connection refused
retry exhausted
HEARTBEAT
```

The bundled `.superdev/config.yaml` defines four local managed deployments in the `dev` environment. It also includes a project log rule that excludes `HEARTBEAT`, so `tail_logs` can verify project-rule filtering while raw log reads can still inspect the noisy service.
