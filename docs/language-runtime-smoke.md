# Language Runtime Smoke

## Scope

This smoke verifies the Phase A local language runtime path through the agent API:

- `runtime.type=language` YAML loading and project registration.
- Default deployment start intent (`start_dev`).
- Runtime status sampling with `base=language`.
- Debug launch through `{"intent":"debug_launch"}`.
- Legacy `{"mode":"debug"}` rejection.

It is a backend API smoke. It does not cover Desktop UI flows, remote hosts, systemd, launchd, or Docker.

## Phase C Node/Python Preflight

Node and Python debugger-ready smoke needs local debugger tooling:

```bash
node --version
pnpm --version
python3 -c 'import debugpy, sys; print(sys.version.split()[0]); print(debugpy.__version__)'
which js-debug-adapter || which vscode-js-debug
```

Expected:

- Node and pnpm are available for high-level and escape-hatch startup.
- Python can import `debugpy`.
- Node debug has an explicit js-debug adapter command available and configured through `code_debug.adapter_command`.

On 2026-06-15 in this workspace, Node (`v23.11.0`) and pnpm (`10.33.0`) were available, but `debugpy` and a js-debug adapter command were not present. Full live DAP attach smoke therefore remains blocked on local tooling, while backend contract coverage is provided by:

```bash
cd agent
go build ./...
go test ./... -count=1
```

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

## Node High-Level Smoke

Fixture:

```javascript
// server.js
let n = 0;
setInterval(() => {
  n += 1;
  console.log("node tick", n);
}, 1000);
```

Runtime config:

```yaml
runtime:
  type: language
  cwd: .
  config:
    program: server.js
code_debug:
  adapter_command: <path-to-js-debug-adapter>
```

Expected:

- `start_dev` runs `node server.js` without `--inspect`.
- Attach resolves the running node process, sends `SIGUSR1`, then starts js-debug and attaches by PID.
- Breakpoints bind to `server.js`, and stack/variables are visible.

## Node Escape-Hatch Smoke

Fixture:

```json
{
  "scripts": {
    "worker": "node server.js"
  }
}
```

Runtime config:

```yaml
runtime:
  type: language
  cwd: .
  config:
    runtime_executable: pnpm
    runtime_args: ["worker"]
code_debug:
  adapter_command: <path-to-js-debug-adapter>
```

Expected:

- `start_dev` runs `pnpm worker` verbatim.
- Attach locates the `node` child in the pnpm process group, sends `SIGUSR1` to that child, then attaches.
- The pnpm parent is not used as the debuggee PID.

## Python Prearm Smoke

Fixture:

```python
# app.py
import time

n = 0
while True:
    n += 1
    print("python tick", n, flush=True)
    time.sleep(1)
```

Runtime config:

```yaml
runtime:
  type: language
  cwd: .
  config:
    program: app.py
```

Expected:

- `start_dev` runs `python -m debugpy --listen 127.0.0.1:<port> app.py`.
- The command does not include `--wait-for-client`, so the process does not block waiting for a debugger.
- Attach starts `debugpy.adapter` and sends a DAP attach request with `connect.host=127.0.0.1` and `connect.port=<port>`.
- Breakpoints bind to `app.py`, and stack/variables are visible.

Known Phase C follow-up: the manager has a listen-attach path, but the normal process start path still needs a durable way to expose the allocated Python debug port to later attach requests. Unit coverage verifies the listen path directly; live end-to-end attach needs that runtime-state handoff plus `debugpy` installed.
