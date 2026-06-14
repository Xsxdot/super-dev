# Language Runtime Smoke

## Scope

This smoke verifies the Phase A local language runtime path through the agent API:

- `runtime.type=language` YAML loading and project registration.
- Default deployment start intent (`start_dev`).
- Runtime status sampling with `base=language`.
- Debug launch through `{"intent":"debug_launch"}`.
- Legacy `{"mode":"debug"}` rejection.

It is a backend API smoke. It does not cover Desktop UI flows, remote hosts, systemd, launchd, Docker, or non-Go providers.

## Minimal Go Fixture

Use a temporary project with this deployment shape:

```yaml
name: lang-smoke
root_path: .
environments:
  - name: dev
    is_dev: true
services:
  - id: api
    name: api
    language: go
    deployments:
      - id: dep-api-dev
        name: api-dev
        env: dev
        service_id: api
        host_id: local
        is_local: true
        managed: true
        runtime:
          type: language
          cwd: ./server
          env:
            ENABLE: "true"
          config:
            program: ./cmd/server
```

When running inside a restricted sandbox, set `GOCACHE` in `runtime.env` to a writable temporary directory.

## API Steps

```bash
PROJECT_ROOT=/absolute/path/to/project

curl -sS -X POST "$SUPERDEV_AGENT_URL/api/projects" \
  -H 'content-type: application/json' \
  -d "{\"root_path\":\"$PROJECT_ROOT\"}"

curl -sS -X POST "$SUPERDEV_AGENT_URL/api/deployments/dep-api-dev/start" \
  -H 'content-type: application/json' \
  -d '{}'

curl -sS "$SUPERDEV_AGENT_URL/api/projects/<project-id>/runtime-status"

curl -sS -X POST "$SUPERDEV_AGENT_URL/api/deployments/dep-api-dev/restart" \
  -H 'content-type: application/json' \
  -d '{"intent":"debug_launch"}'

curl -i -X POST "$SUPERDEV_AGENT_URL/api/deployments/dep-api-dev/restart" \
  -H 'content-type: application/json' \
  -d '{"mode":"debug"}'
```

Expected results:

- Default start returns `{"status":"starting"}`.
- The debug binary is written under `<DataDir>/run-bin/dep-api-dev/server` and is overwritten on restart instead of accumulating in the project tree.
- Runtime status includes `health=running` and `base=language` for the deployment instance.
- `intent=debug_launch` returns `{"status":"starting"}` and runtime status includes `base=debug` with `debugger.origin=launched`.
- Legacy `mode=debug` returns HTTP 400 with an error telling callers to use `intent=start_dev`, `start_normal`, or `debug_launch`.

## Attach Source Breakpoints

start_dev builds a debug binary (`go build -gcflags="all=-N -l"`) under the agent
data dir before exec, so the attach session can set source-level breakpoints.
No `go run` temp-dir leakage, and incremental rebuilds are ~0.1s on a warm cache.
