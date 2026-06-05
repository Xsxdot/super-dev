# SuperDev Desktop

This package contains the SuperDev desktop UI. It is a Tauri 2 application with a Vue 3 frontend and bundled Go sidecars for the local agent, MCP server, and sample project.

## Development

```bash
pnpm install --frozen-lockfile
pnpm build
pnpm test
```

For a full desktop run:

```bash
pnpm tauri dev
```

`tauri dev` invokes `scripts/build-agent.sh` before starting Vite so the desktop app can load the current sidecar binaries.

## Boundaries

- The frontend talks to the local SuperDev agent instead of reading process, log, or approval state directly.
- Runtime write operations must preserve the preview, approval, token, and audit flow.
- Generated sidecar binaries and local screenshots should not be committed.
