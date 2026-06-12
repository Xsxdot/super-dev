# Code Debug Smoke

## Scope

This smoke verifies local command deployment code debug sessions through the agent API. It does not run remote, systemd, launchd, or Docker scenarios.

Code debug is a last-resort path. Use logs and diagnosis tools first, then use breakpoint debugging only when runtime state cannot be inferred from logs.

## Requirements

- SuperDev agent is running.
- A local deployment has `code_debug.enabled=true`.
- Go targets need `dlv` available on `PATH`.
- Python targets need `debugpy` available to the configured Python interpreter.
- Node targets are experimental and need `code_debug.adapter_command` configured.

## Run

```bash
cd /Users/xushixin/workspace/super-debug/agent
SUPERDEV_AGENT_URL=http://127.0.0.1:57017 \
SUPERDEV_DEPLOYMENT_ID=dep-api-dev \
go run ./cmd/code-debug-smoke
```

To verify lease close without stopping Debug Runtime:

```bash
cd /Users/xushixin/workspace/super-debug/agent
SUPERDEV_CODE_DEBUG_KEEP_RUNTIME=1 \
SUPERDEV_AGENT_URL=http://127.0.0.1:57018 \
SUPERDEV_DEPLOYMENT_ID=dep_aihub_server_dev \
go run ./cmd/code-debug-smoke
```

The deployment should remain `debug-running` after `close_code_debug_session` when `code_debug.keep_runtime_on_lease_close=true`.

To verify attach-first debugging for an already running Go service:

```bash
cd /Users/xushixin/workspace/super-debug/agent
SUPERDEV_CODE_DEBUG_ATTACH=1 \
SUPERDEV_AGENT_URL=http://127.0.0.1:57017 \
SUPERDEV_DEPLOYMENT_ID=dep-api-dev \
SUPERDEV_CODE_DEBUG_SOURCE=main.go \
SUPERDEV_CODE_DEBUG_LINE=42 \
SUPERDEV_CODE_DEBUG_THREAD_ID=1 \
go run ./cmd/code-debug-smoke
```

Attach smoke starts the deployment in normal mode, calls the deployment-subject debug capture endpoint, checks `debugger.origin=attached`, closes the lease with `stop_runtime=true` to detach, and checks that the normal service PID is unchanged.

Attach prerequisites:

- The deployment is a local Go command deployment with a dev build that keeps symbols.
- `dlv` is available on `PATH`.
- On macOS, developer tools attach permission is enabled (`DevToolsSecurity -enable`) or the first attach prompt has been accepted.
- `SUPERDEV_CODE_DEBUG_SOURCE` and `SUPERDEV_CODE_DEBUG_LINE` point to a line that the running service will hit before `SUPERDEV_CODE_DEBUG_TIMEOUT_MS` expires.

## Expected

The command prints JSON lines for target discovery, session open, optional capture/inspect, and close.
With `SUPERDEV_CODE_DEBUG_KEEP_RUNTIME=1`, it also prints `runtime_after_close` with the expected `debug-running` runtime state.
With `SUPERDEV_CODE_DEBUG_ATTACH=1`, it also prints `normal_started`, `capture`, `attached_runtime`, and `detached_runtime`; the PID should remain unchanged across attach and detach, and `attached_runtime.debugger.origin` should be `attached`.
