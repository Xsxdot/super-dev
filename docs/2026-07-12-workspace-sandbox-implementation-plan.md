# Workspace Sandbox Implementation Plan

> Implement in order. Each task must leave its tests green and preserve existing Host behavior. Design: `docs/2026-07-12-workspace-sandbox-design.md`; terms: `CONTEXT.md`; decisions: `docs/adr/`.

## Goal

Deliver an opt-in Workspace Sandbox path in which two worktrees of the same Project can independently start, observe and debug the same Deployment through existing SuperDev tools without port collisions or Host/container artifact pollution.

## Architecture and seams

The implementation introduces three deep modules and keeps HTTP/MCP/Desktop as adapters:

| Module | External interface | Hidden implementation |
| --- | --- | --- |
| `workspace` | register/list/resolve/load view/update desired state | Registry v2 migration, path rebind, caller context, per-Workspace config loading |
| `runtimecontrol` | execute one Runtime Command and read status | provider resolution, process manager, readiness, observed spec revision, local/remote routing |
| `sandbox` | status, plan/apply lifecycle, ensure ready, query operation | conditions, singleflight, capacity, trust, Driver, storage, credentials, endpoints and recovery |

Real seams exist only where two adapters are required:

- `workspace.Store`: JSON v2 production adapter and in-memory test adapter.
- `sandbox.Driver`: Docker-compatible production adapter and deterministic fake adapter.
- `sandbox.CredentialStore`: private file production adapter and in-memory test adapter.
- `runtimecontrol.NodeClient`: NodeTransport production adapter and in-memory test adapter.

Do not expose Engine commands, persistence records or test-only seams through the three module interfaces. HTTP handlers call the modules, never their Store/Driver adapters. DTO conversion lives in explicit API assembler files.

## Observability and comments

Every task below includes mandatory instrumentation work:

- New Go behavior uses the project-approved structured logger with component/entry name and stable key-value context. If the Agent module still lacks that logger at implementation time, establish the logger adapter in Task 1 before writing Sandbox behavior; do not use `fmt.Printf` or ad-hoc stdout.
- Rust/Tauri changes use `tracing`.
- Never log tokens, raw environment values, `.env` content, full lifecycle command text or secret-bearing Engine output.
- New source files require responsibility/boundary headers; exported methods require doc comments; non-obvious recovery, identity and security branches require Chinese “why” comments.

## Verification commands

```bash
cd agent && go test ./...
cd desktop && pnpm test
cd desktop && pnpm build
bash desktop/scripts/build-agent.sh --remote-install
```

Docker integration tests are opt-in and must skip with an explicit reason when the local Provider is unavailable; release validation must run them on macOS and Linux.

---

## Phase A — Identity foundations with no Sandbox behavior

### Task 1: Establish module contracts and test adapters

**Files:**

- Create: `agent/workspace/`
- Create: `agent/runtimecontrol/`
- Create: `agent/sandbox/`
- Modify: `agent/api/server.go`
- Modify: `agent/go.mod` only if the approved structured logger is not already available

- [ ] Define the smallest external interfaces and immutable domain request/result types; keep Driver, Store and credential ports internal to their owning module.
- [ ] Add compile-time interface assertions for production and in-memory adapters.
- [ ] Add interface-level tests proving callers can exercise behavior without importing implementation packages.
- [ ] Wire empty/no-op modules into `api.App` without changing routes or current Host runtime behavior.
- [ ] Add structured entry/error/success logs to module construction and top-level operations with request/workspace/operation IDs.
- [ ] Add file headers, exported method comments and seam rationale comments; document that handlers may not reach Stores or Drivers.
- [ ] Run `cd agent && go test ./...`.

### Task 2: Migrate to Workspace Registry v2 and Workspace Project Views

**Files:**

- Replace: `agent/config/registry.go` usage with the `agent/workspace` module
- Modify: `agent/config/registry_test.go`
- Modify: `agent/api/handler_projects.go`
- Modify: `agent/api/server.go`
- Modify: `agent/model/model.go`
- Create: `agent/api/workspace_assembler.go`

- [ ] Write migration tests for v1 path arrays, atomic v2 write, timestamp backup, migration failure rollback and fail-closed malformed v2.
- [ ] Write identity tests for two worktrees sharing Project/Service/Deployment IDs, stable Workspace UUIDs, exact path moves and ambiguous Git rebind fingerprints.
- [ ] Implement Registry v2 as the sole desired-state store with `execution_mode=host` defaults; remove duplicate-ID rewriting from registered Workspace loading.
- [ ] Load `.superdev/config.yaml` independently into a Workspace Project View and isolate invalid/missing config to that Workspace.
- [ ] Implement list/register/remove/rebind through the Workspace module; keep handlers limited to decode → module call → assembler → response.
- [ ] Add structured logs for migration start/outcome, allocation, rebind, orphaning and config-load failures; never log project secrets or full config.
- [ ] Add headers and docs explaining membership vs Project identity, plus “why” comments on fail-closed downgrade and non-rewrite behavior.
- [ ] Run registry, project-handler and full Agent tests.

### Task 3: Add canonical Workspace target resolution

**Files:**

- Create: `agent/workspace/resolver.go`
- Modify: `agent/mcp/protocol.go`
- Modify: `agent/mcp/resolve.go`
- Modify: `agent/mcp/tools_runtime.go`
- Modify: `agent/api/handler_projects.go`

- [ ] Write table tests for precedence: explicit Workspace ID → caller cwd context → unique Project Workspace → `ambiguous_workspace`.
- [ ] Capture MCP process cwd once, resolve the deepest registered containing Workspace and pass Caller Workspace Context with requests.
- [ ] Return candidate Workspace summaries on ambiguity; never choose main worktree, last-used item or lexical-first path.
- [ ] Extend project/runtime snapshots to group by Workspace while keeping single-Workspace legacy calls working.
- [ ] Add structured resolution logs containing selector kinds and candidate counts, not full secret-bearing requests.
- [ ] Add exported resolver docs and comments explaining why caller context is convenience rather than authorization.
- [ ] Run MCP resolver, protocol and runtime tool tests.

### Task 4: Migrate Runtime and Runtime Log identity

**Files:**

- Create: `agent/model/runtime_instance.go`
- Modify: `agent/model/runtime_status.go`
- Modify: `agent/store/store.go`
- Modify: `agent/logbuf/buffer.go`
- Modify: `agent/logbackend/`
- Modify: `agent/api/handler_deployment_logs.go`
- Modify: `agent/mcp/tools_logs.go`

- [ ] Define deterministic Runtime Instance ID derivation from Workspace, Deployment and Slot and add collision/stability tests.
- [ ] Add nullable `workspace_id` and `runtime_instance_id` columns without rewriting historical rows; replace `(deployment_id, seq)` uniqueness with the partial Runtime Instance index.
- [ ] Write migration tests using a large synthetic legacy table to prove no full-table identity backfill occurs.
- [ ] Require all new Host/Remote/Sandbox writes to carry both identities and move seq watermark, follow cursor and folding keys to Runtime Instance.
- [ ] Expose historical NULL rows as `legacy_unscoped`; scoped queries exclude them unless explicitly requested.
- [ ] Keep deployment-only query compatibility only when Workspace resolution is unique.
- [ ] Add migration/write/query logs with row counts, index outcomes and Runtime Instance context; avoid logging message bodies.
- [ ] Add schema and cursor “why” comments, especially around nullable physical schema vs required new-write contract.
- [ ] Run store, log buffer/backend, API and MCP log tests.

---

## Phase B — One Runtime Service and one Node model

### Task 5: Extract the shared Runtime Control module

**Files:**

- Create: `agent/runtimecontrol/service.go`
- Create: `agent/runtimecontrol/spec_revision.go`
- Modify: `agent/api/handler_deployments.go`
- Modify: `agent/process/manager.go`
- Modify: `agent/model/managed_deployment.go`
- Create: `agent/api/handler_runtime_commands.go`

- [ ] Characterize current language/command start, readiness, stop and restart behavior with tests before extraction.
- [ ] Move `startDeploymentRuntime` orchestration behind one Runtime Control interface used by local handlers and the new Runtime Command endpoint.
- [ ] Key process ownership and PID state by Runtime Instance ID; preserve Deployment ID as definition metadata.
- [ ] Add desired/observed Runtime Spec Revision, expected-revision validation and stale-runtime status without automatic restart.
- [ ] Make start/stop/restart/status idempotent and return structured revision, already-running and not-running results.
- [ ] Prevent active Deployment removal unless the approved config operation explicitly stops its Runtime Instance first.
- [ ] Add structured start/stop/readiness/revision logs at entry, external/provider calls, every error and final outcome.
- [ ] Add headers/doc comments and “why” comments for no-auto-restart and deletion-before-stop prevention.
- [ ] Run process manager, deployment handler, runtime status and end-to-end managed-deployment tests.

### Task 6: Introduce Node Reference without inventing Sandbox transport

**Files:**

- Modify: `agent/nodetransport/transport.go`
- Modify: `agent/nodetransport/direct.go`
- Modify: `agent/nodetransport/dispatcher.go`
- Modify: `agent/noderegistry/registry.go`
- Modify: `agent/api/handler_node_status.go`
- Modify: `agent/api/agent_dto.go`

- [ ] Add Node Reference/Kind with self, remote and sandbox variants; Remote Node IDs remain compatible with Host IDs.
- [ ] Make NodeRegistry and Dispatcher canonicalize on Node ID while preserving legacy `host_id` fields at remote compatibility adapters.
- [ ] Add a generated Node target source seam so Sandbox targets can later join DirectTransport without entering remote Host CRUD.
- [ ] Prove existing tunnel/direct remote behavior is unchanged through contract tests.
- [ ] Add structured node registration, route selection, reachability and compatibility logs.
- [ ] Add file headers/exported-method docs and “why” comments explaining why Sandbox Node is not a synthetic SSH Host and why there is no SandboxTransport.
- [ ] Run NodeTransport, NodeRegistry, Agent DTO and remote integration tests.

---

## Phase C — Sandbox domain with a fake Driver

### Task 7: Implement Sandbox status, operations and operation logs

**Files:**

- Create: `agent/sandbox/types.go`
- Create: `agent/sandbox/coordinator.go`
- Create: `agent/sandbox/operations.go`
- Create: `agent/sandbox/capacity.go`
- Create: `agent/store/sandbox_operations.go`
- Refactor: `agent/api/run_hub.go` into an owner-typed replay/subscription module
- Create: `agent/api/sandbox_assembler.go`

- [ ] Write interface tests for Desired + Observed + Conditions + Active Operation and derived readiness/allowed actions.
- [ ] Implement per-Workspace singleflight: identical EnsureReady coalesces; conflicting mutations return `workspace_busy`.
- [ ] Persist asynchronous operation state and stage logs; mark in-flight records interrupted after Controller restart.
- [ ] Add global capacity gate defaulting to two expensive stages and visible/cancelable `waiting_for_capacity`.
- [ ] Revalidate generation, revisions, runtime set and approval after acquiring a capacity lease.
- [ ] Implement bounded/redacted Sandbox Operation Logs with truncation marker and replay-before-live subscription.
- [ ] Test entirely through the Sandbox module interface with the fake Driver and in-memory stores.
- [ ] Add structured operation transition, capacity, recovery and final-outcome logs; stage output itself goes only through the redacting operation-log sink.
- [ ] Add headers/docs and “why” comments for non-resume after crash, visible capacity waits and non-overwriting failure state.
- [ ] Run Sandbox module, store and event-stream tests.

### Task 8: Resolve and validate Sandbox Definitions

**Files:**

- Create: `agent/sandbox/devcontainer/resolve.go`
- Create: `agent/sandbox/devcontainer/preflight.go`
- Create: `agent/sandbox/devcontainer/revision.go`
- Modify: `agent/configchange/`
- Modify: `agent/langruntime/types.go`
- Modify: all language Runtime Providers

- [ ] Add fixture tests for supported image/build/Features/user/env/mount/port/lifecycle fields and exact diagnostics for unsupported Compose/Host-command/privilege fields.
- [ ] Resolve through the bundled CLI `read-configuration`; normalize only the effective inputs that belong in Sandbox Revision.
- [ ] Add `IsolationHints` to language providers and validate an explicit `customizations.superdev` Isolation Manifest.
- [ ] Make high-confidence missing platform isolation a readiness blocker and low-confidence findings warnings.
- [ ] Extend config preview/apply for `devcontainer.json` and `.devcontainer-lock.json`; prepare always uses frozen lock behavior and never writes Workspace files.
- [ ] Build exact Trust fingerprints from definition digest, resolved mounts, lifecycle commands, supported sensitive capabilities and Git metadata mounts.
- [ ] Add structured resolver/preflight logs with file-relative field paths, counts, digest prefixes and stable diagnostic codes; redact values.
- [ ] Add headers and comments explaining why hints do not become mounts until config apply and why ordinary Runtime Spec changes do not alter Sandbox Revision.
- [ ] Run Dev Container fixture, config-change and language-provider tests.

---

## Phase D — Real Dev Container and Docker-compatible Driver

### Task 9: Package the Dev Container Toolchain and Provider Profile

**Files:**

- Create: `desktop/scripts/prepare-devcontainer-cli.sh`
- Modify: `desktop/scripts/build-agent.sh`
- Modify: `desktop/src-tauri/tauri.conf.json`
- Modify: desktop release workflows and third-party notices
- Create: `agent/sandbox/provider_profile.go`
- Modify: `agent/config/settings.go`

- [ ] Pin one tested Dev Container CLI version and per-target SHA-256 manifest; package the official CLI plus Node runtime for macOS/Linux amd64/arm64.
- [ ] Add packaging tests that fail when the target bundle, digest or license notice is missing.
- [ ] Resolve the bundle from Controller resources, never PATH, except an explicit development-test override.
- [ ] Implement Provider Profile detection/persistence, local-endpoint validation, capability health and Engine fingerprint checks.
- [ ] Reject ambient context drift and remote SSH/TCP Docker endpoints with stable diagnostics.
- [ ] Implement explicit profile-switch preview data; do not yet delete or migrate resources.
- [ ] Add `tracing`/Go structured logs for bundle resolution, digest verification, profile probe, engine change and final outcomes without absolute secret paths.
- [ ] Add script/source headers and comments explaining build-time download vs forbidden user-machine installation.
- [ ] Run packaging tests, Agent tests and `bash desktop/scripts/build-agent.sh --remote-install`.

### Task 10: Implement the Docker-compatible Driver and owned storage

**Files:**

- Create: `agent/sandbox/dockerdriver/driver.go`
- Create: `agent/sandbox/dockerdriver/engine.go`
- Create: `agent/sandbox/dockerdriver/resources.go`
- Create: `agent/sandbox/dockerdriver/mounts.go`
- Create: `agent/sandbox/dockerdriver/endpoints.go`
- Create: `agent/sandbox/dockerdriver/command_runner.go`

- [ ] Define argv-only command execution with cancellation, timeouts and redacted stdout/stderr streaming into Sandbox Operation Log.
- [ ] Implement resource labels using Controller Installation ID, Workspace ID, kind, revision and generation; names remain diagnostic only.
- [ ] Discover exactly one owned container/volume set, report absent, and block duplicate matches as conflicted.
- [ ] Materialize Host-bound source, read-only Git metadata mounts, Workspace-private state, shared cache namespaces, Sandbox Agent State and dynamic endpoint publishes.
- [ ] Implement stop/recreate/reset/cache-purge distinctions and leases; never call global Docker prune.
- [ ] Map `host.superdev.internal` through the active local Engine and probe it after prepare.
- [ ] Add deterministic fake-runner unit tests plus opt-in real Docker tests for lifecycle, labels, dynamic ports, resource recovery and cleanup safety.
- [ ] Add structured Driver logs before/after every CLI/Engine call, on resource state transitions and final outcomes; store raw redacted CLI output only in operation logs.
- [ ] Add file/exported-method comments and “why” comments for ownership rechecks, no-adoption rules, mount ordering and destructive-operation guards.
- [ ] Run unit tests and the gated Docker integration suite on macOS and Linux.

### Task 11: Inject, bootstrap and recover the Sandbox Agent

**Files:**

- Modify: `agent/main.go`
- Modify: `agent/internal/buildinfo/version.go`
- Modify: `agent/api/security_handler.go`
- Modify: `agent/security/store.go`
- Create: `agent/sandbox/credentials.go`
- Create: `agent/sandbox/agent_payload.go`
- Modify: `agent/nodetransport/direct.go`

- [ ] Add `--bootstrap-token-file` that reads once, clears memory where practical and never echoes the token.
- [ ] Extend health/handshake with build identity, executable digest and capabilities.
- [ ] Select Linux amd64/arm64 Sandbox Agent Payload from bundled resources and mount it read-only.
- [ ] Implement Controller Node Credential Store plus temporary crash-recoverable bootstrap secret; provision through the existing endpoint and burn bootstrap state on success.
- [ ] Register `sandbox:<workspace_id>` through existing DirectTransport using the dynamic loopback control endpoint and per-node token.
- [ ] Reuse Sandbox Agent State across stop/recreate/reset; clear stale PID state on generation change without deleting logs/identity/security.
- [ ] Add explicit high-risk credential repair that rotates only security state.
- [ ] Test bootstrap interruption/retry, token loss repair, payload mismatch, Agent restart, Controller restart and secret redaction.
- [ ] Add structured bootstrap/handshake/recovery logs with Node/Workspace/generation context and no credential material.
- [ ] Add comments explaining why the Agent is full/reused, why the token uses a file and why credential loss does not trigger silent auth disablement.
- [ ] Run security, health, DirectTransport and Sandbox integration tests.

---

## Phase E — End-to-end Runtime, logs, endpoints and debugging

### Task 12: Project managed views and Runtime Commands into the Sandbox

**Files:**

- Modify: `agent/model/managed_deployment.go`
- Modify: `agent/api/managed_deployments.go`
- Modify: `agent/api/handler_managed_deployments.go`
- Modify: `agent/api/host_deployment_reconciler.go`
- Modify: `agent/runtimecontrol/`
- Modify: `agent/sandbox/coordinator.go`

- [ ] Extend managed projection with Workspace/Runtime Instance IDs, container project root, language, readiness, endpoints, debug configuration and Runtime Spec Revision.
- [ ] Reuse the current reconcile transport to deliver exactly one Workspace Project View to its Sandbox Node.
- [ ] Gate new start/restart on Sandbox Readiness and expected revisions; keep status/log/stop available for stale/degraded reachable nodes.
- [ ] Make `start_service` automatically join EnsureReady only when definition, trust and current revision already permit it.
- [ ] Return operation-in-progress or a structured blocker instead of transport timeout, config write, approval or Host fallback.
- [ ] Test two fake Workspaces with identical Project/Deployment IDs through one Controller and two Sandbox Agents.
- [ ] Add structured projection, Runtime Command, readiness-gate and routing logs with Workspace/Runtime/Node identities.
- [ ] Add comments on why projection updates do not restart processes and why stop does not start an offline Sandbox.
- [ ] Run managed-deployment, Runtime Control and Sandbox interface tests.

### Task 13: Complete Runtime logs and debug routing

**Files:**

- Modify: `agent/api/backend_factory.go`
- Modify: `agent/logbackend/remote.go`
- Modify: `agent/api/handler_log_search.go`
- Modify: `agent/api/handler_code_debug.go`
- Modify: `agent/codedebug/`
- Modify: `agent/api/handler_browser_debug.go`
- Modify: `agent/browserdebug/`

- [ ] Route Runtime log reads/follow/search to the owning Agent by Runtime Instance ID; do not mirror Sandbox logs into Controller.
- [ ] Return partial results plus unavailable Sandbox Nodes for global searches; an offline Sandbox is never silently started by a log read.
- [ ] Keep DAP/code-debug runtime inside Sandbox Agent and translate Host/container source paths through validated Workspace-relative paths.
- [ ] Resolve Browser Debug targets from Application Endpoint to current dynamic Endpoint Binding on the Host.
- [ ] Add tests for identical Deployment IDs, offline log history, partial search, path traversal rejection, container stack-path translation and endpoint rebinding after recreate.
- [ ] Add structured log-query/debug-session/translation logs with Runtime Instance and session IDs; never log evaluated secrets or credential values.
- [ ] Add comments on no-mirroring, no-read-side-start and relative-path trust boundaries.
- [ ] Run log backend/search, code-debug and browser-debug tests plus their smoke programs.

---

## Phase F — Public surfaces and release hardening

### Task 14: Add HTTP and MCP Workspace/Sandbox surfaces

**Files:**

- Create: `agent/api/handler_workspaces.go`
- Create: `agent/api/handler_sandboxes.go`
- Modify: `agent/api/handler_operations.go`
- Modify: `agent/api/handler_config_changes.go`
- Modify: `agent/mcp/client.go`
- Create: `agent/mcp/tools_sandbox.go`
- Modify: `agent/mcp/tools.go`
- Modify: existing Runtime/log/debug MCP tools

- [ ] Add list/get Workspace and Sandbox status endpoints plus operation/status/log reads.
- [ ] Extend preview/apply kinds for execution mode, Sandbox Definition, prepare/reconcile/stop/reset and credential repair.
- [ ] Add `apply_sandbox_operation` with preconditions, approval token and bounded wait.
- [ ] Register `list_workspaces`, `get_sandbox_status`, `get_sandbox_operation` and `tail_sandbox_operation_logs`.
- [ ] Preserve old single-Workspace `deployment_id` calls and return ambiguity in multi-Workspace cases.
- [ ] Sanitize all MCP structured content and produce actionable next actions for blockers/in-progress operations.
- [ ] Add structured handler/tool entry, authorization, module-call failure and success logs; never log request bodies containing definitions or credentials.
- [ ] Add handler/tool headers, schema docs and assembler tests for empty arrays, conditions, operations and endpoint bindings.
- [ ] Run HTTP API, MCP unit and MCP end-to-end tests.

### Task 15: Prototype, then add Desktop Workspace/Sandbox UI

**Precondition:** `prototypes/base/` is currently absent. Before changing production Vue layout, establish the project prototype baseline and approve a clickable Workspace/Sandbox state flow under the project’s prototype rules.

**Likely files after prototype approval:**

- Modify: `desktop/src/api/agent.ts`
- Modify: `desktop/src/stores/workspace.ts`
- Create: `desktop/src/stores/sandbox.ts`
- Modify: `desktop/src/components/Overview/ProjectOverviewPane.vue`
- Modify: `desktop/src/components/Overview/RuntimeStatusTab.vue`
- Modify: `desktop/src/components/Settings/OperationApprovalsTab.vue`
- Add focused Vue/Vitest tests and locale strings

- [ ] Prototype Workspace selector, execution-mode transition, condition/blocker display, operation progress/logs, endpoint links and destructive action preview.
- [ ] Implement only the approved shape using the same HTTP contracts as MCP; do not add Desktop-only business rules.
- [ ] Display dynamic URLs as observed bindings, stale/degraded/partial states explicitly, and Windows unsupported state without an enable action.
- [ ] Keep destructive Reset/Purge/Credential Repair behind preview and existing approval UI.
- [ ] Add `tracing` at Tauri command boundaries if new commands are required; Vue must not use console output as operational logging.
- [ ] Add component responsibility comments and non-obvious state-derivation comments; keep status derivation server-owned.
- [ ] Run `cd desktop && pnpm test` and `pnpm build`, then compare production UI to the approved prototype.

### Task 16: Real two-worktree smoke, packaging and rollout gates

**Files:**

- Create: `agent/cmd/workspace-sandbox-smoke/`
- Create: `docs/workspace-sandbox-smoke.md`
- Modify: release workflows, capability reporting and release documentation

- [ ] Build fixtures for Node and Go Workspaces using the same Project/Deployment IDs and container ports.
- [ ] Create two real Git worktrees, opt both into Sandbox, start concurrently through MCP and verify different Host endpoint bindings.
- [ ] Prove Host `node_modules`/venv/native artifacts remain unchanged and Workspace-private volumes differ.
- [ ] Tail/follow/search each Runtime independently; verify legacy lane behavior and partial results when one Sandbox stops.
- [ ] Run code-debug in the Sandbox and browser-debug on the Host; verify source mapping and endpoint recreation.
- [ ] Restart Controller and Sandbox Agent, recover resources/credentials/logs, then exercise stale reconcile, Engine change refusal, conflict detection and GC ownership guards.
- [ ] Add structured smoke-run entry, external-call, error and success logs with fixture/Workspace/Runtime IDs; do not use print-style output as operational logging.
- [ ] Add responsibility/boundary headers to the smoke command and docs for every exported helper, plus “why” comments around cleanup and ownership checks.
- [ ] Verify all Sandbox Operation stages log entry/external call/error/transition/success with stable context and no secrets; verify every new file/exported function/complex branch meets comment rules.
- [ ] Run full Agent/Desktop/build suites on macOS and Linux amd64/arm64; verify Windows reports unsupported while Host behavior remains green.
- [ ] Do not enable public `execution_mode=sandbox` until every acceptance scenario in the design document passes.

## Completion checklist

- [ ] Existing Host-only behavior and tests remain unchanged until explicit Workspace opt-in.
- [ ] No handler calls a Store, Driver or Engine adapter directly.
- [ ] No raw Docker/Dev Container detail appears in MCP contracts or project runtime schema.
- [ ] Every new Runtime Log has Workspace and Runtime Instance identity; only historical rows are NULL.
- [ ] Runtime and Sandbox stale states never trigger automatic restart/reconcile.
- [ ] Sandbox reads never start an offline Sandbox.
- [ ] Unsupported capabilities and platforms fail closed with structured diagnostics.
- [ ] Secrets are absent from process argv, labels, definitions, operation logs, Runtime logs and final user-facing errors.
- [ ] Key operations have entry, external-call, error, transition and success logs with stable IDs.
- [ ] Every new source file and exported method has required responsibility/boundary documentation.
