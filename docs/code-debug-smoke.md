# Code Debug Smoke

## Scope

This smoke verifies local command deployment code debug sessions through the agent API. It does not run remote, systemd, launchd, Docker, or attach scenarios.

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

## Expected

The command prints JSON lines for target discovery, session open, optional capture/inspect, and close.
