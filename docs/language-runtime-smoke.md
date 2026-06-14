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
- Runtime status includes `health=running` and `base=language` for the deployment instance.
- `intent=debug_launch` returns `{"status":"starting"}` and runtime status includes `base=debug` with `debugger.origin=launched`.
- Legacy `mode=debug` returns HTTP 400 with an error telling callers to use `intent=start_dev`, `start_normal`, or `debug_launch`.

## Attach Capture Note

The deployment-subject capture endpoint can resolve the running Go process and start a Delve attach session, but `go run` may produce a temporary binary that Delve reports as stripped for source breakpoints:

```json
{
  "code": "dap_request_failed",
  "error": "breakpoint line 22 unverified: could not find file ..., binary is stripped"
}
```

This is a Delve attach/source-mapping limitation for `go run` in Phase A, not a failure of language runtime start or `debug_launch`. Use `intent=debug_launch` for source-level breakpoint debugging until the Go provider grows a prebuilt debug-binary start strategy.
