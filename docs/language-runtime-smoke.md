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
test -f "$SUPERDEV_DATA_DIR/js-debug/src/dapDebugServer.js"
```

Expected:

- Node and pnpm are available for high-level and escape-hatch startup.
- Python can import `debugpy`.
- Node debug has `@vscode/js-debug` standalone extracted under the agent data dir:
  `<DataDir>/js-debug/src/dapDebugServer.js`.

On 2026-06-15 in this workspace, Node (`v23.11.0`) and pnpm (`10.33.0`) were available. Node full attach was verified with `microsoft/vscode-js-debug` release `v1.117.0`, downloaded as `js-debug-dap-v1.117.0.tar.gz` and extracted so the agent data dir contained `js-debug/src/dapDebugServer.js`.

Backend contract coverage:

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
```

Verified end-to-end on real node v23.11 with js-debug v1.117.0 (2026-06-15):

- `start_dev` runs `node server.js` without `--inspect`; before any signal the process has no
  inspector open and `node tick N` flows to stdout normally.
- `kill -USR1 <pid>` makes node print `Debugger listening on ws://127.0.0.1:9229/<uuid>` to
  **stderr** and stdout keeps flowing.
- The manager parses the inspector port from deployment stderr. If the stderr line is missed in
  this environment, it falls back to default port `9229` only when that port was closed before
  SIGUSR1 and opens afterwards.
- The agent starts the bundled adapter as
  `node <DataDir>/js-debug/src/dapDebugServer.js <adapterPort> 127.0.0.1`, attaches js-debug to
  `127.0.0.1:<inspectorPort>`, and sets a source breakpoint through the DAP path.
- js-debug standalone may answer the root attach by sending a reverse `startDebugging` request.
  The real source-binding session is the child DAP connection opened with the request's
  `__pendingTargetId`; breakpoints on the root session can stay provisional.
- The smoke hit a breakpoint at `server.js:5`, reported `reason=breakpoint`, used js-debug
  `threadId=0`, and returned stack/source data for the fixture path under `/private/tmp`.

Smoke note: start the Node process and wait briefly before attaching. Sending `SIGUSR1` during
Node's earliest startup window can terminate the process before Node installs its inspector signal
handler. Product attach targets are already-running services; the smoke used a short pre-attach wait.

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
```

Verified end-to-end with pnpm 10.33.0 and js-debug v1.117.0 (2026-06-15):

- `start_dev` runs `pnpm worker` verbatim.
- Attach locates the real `node server.js` child in the pnpm process group, sends `SIGUSR1` to
  that child, parses the inspector port from the deployment stderr tail, and then attaches through
  the bundled js-debug server.
- The resolver deliberately skips the deployment main PID when the runtime config is an escape
  hatch (`runtime_executable=pnpm`), even if the launcher process command name is also `node`.
- The smoke hit the same `server.js:5` breakpoint with `reason=breakpoint` and js-debug
  `threadId=0`.

Restricted sandbox caveat: the pnpm smoke requires the agent to enumerate the process group with
`ps`. In a sandbox where `/bin/ps` is denied, direct Node can still fall back to the main process,
but the pnpm escape hatch should run the agent with permissions that allow process-group lookup.

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

Verified end-to-end against real debugpy 1.8.21 (2026-06-15):

- `start_dev` runs `python -m debugpy --listen 127.0.0.1:<port> app.py` (port allocated by
  the agent and injected via `BuildPlanInput.DebugPort`).
- The command does **not** include `--wait-for-client`: confirmed the process does not block —
  `python tick 1` / `tick 2` kept flowing to stdout while the listener was up, proving listen-only
  does not hijack the runner stdout pipeline.
- A DAP client connecting with `connect.host=127.0.0.1, connect.port=<port>` (exactly what
  `PythonProvider.AttachArguments` produces) set a breakpoint at `app.py:5` that came back
  `verified=true`. Breakpoint binding requires a successful attach, so this proves the
  prearm-listen attach path works against a live debuggee.
- The agent recovers `<port>` at attach time by parsing `--listen host:port` from the running
  process argv (`Manager.DeploymentArgv` → `parseListenPort`); no separate port store is needed.
- Path sensitivity: both `/tmp/...` (symlink) and `/private/tmp/...` (real) breakpoint paths bound
  verified=true — debugpy is more tolerant than dlv here, and the Phase A `sourceRoot`+EvalSymlinks
  normalization covers it regardless.

Note: `start_normal` (or any Python process started without `--listen`) is intentionally
not attachable — `parseListenPort` returns 0 and the manager reports `ErrAttachUnsupported`
rather than silently relaunching.
