# Changelog

All notable changes to SuperDev will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) for public releases.

## [Unreleased]

## [0.2.4] - 2026-08-20

### Fixed

- Repaired the runtime status of a project whose dev environment has been transferred to a dev machine: it reported `stopped` while the service was in fact running on the home machine. The snapshot dispatcher only asked whether a deployment was `location: remote`, never which machine currently owns the dev environment, so a homed `location: local` deployment was sampled against the local process manager and could only ever come back stopped — even though the home node's status frame carried the correct `running`. Home resolution now reuses the same rule as start/stop forwarding, so the two can no longer drift into "it starts but shows as stopped".
- Made `deployment.ports` settable through the config-change path. Validation for it had always been there, but `DeploymentPatch` carried no `Ports` field and the merge step ignored it, so ports sent to preview/apply were silently dropped on decode and the request still returned `200`. Declaring the ports that drive port mirroring was therefore impossible from MCP; the only remaining route was the desktop's whole-project setup call, whose semantics delete any service missing from the payload.
- Gave every pre-flight rejection in the transfer execute endpoint a log line. Twelve rejection branches — request body, missing `host_id`, unknown project, not a git repository, no `origin`, detached `HEAD`, uncommitted changes, unpushed commits, no upstream, target is the local machine, unknown host, host not in dev machine mode — returned an error to the caller and recorded nothing server-side, leaving a user-visible failure with no evidence to diagnose it from.
- Separated "cannot reach the home host" from "reached it but got no reply in time"; both previously surfaced as `home_unreachable`. A slow remote build was reported as an unreachable host, pointing diagnosis at the network instead of at the work still running on that machine. The two also differ in consequence: unreachable means the operation never started, whereas a timeout means it very likely arrived and is still executing, so a retry would trigger the side effect twice. Timeout detection covers both `context.DeadlineExceeded` and `net.Error` timeouts, the latter being what an HTTP client's awaiting-headers timeout actually produces. A failure to read the caller's own request body no longer masquerades as an unreachable host either; it is now a `400`.

### Added

- Extended the transfer asset audit to cover git-ignored top-level files. The audit followed only `env_file` declarations, so an ignored service config such as `config.yaml` — which `git clone` structurally cannot carry to the target — went missing in silence while the report claimed nothing needed attention. Ignored directories are folded into a single entry rather than expanded, and the scan is capped at twenty files with an explicit report item when it truncates, so a partial scan never reads as a complete one. As with suspected secrets, entries carry file names only, never contents.

### Changed

- Updated repository, agent runtime, desktop package, Tauri, Cargo metadata, and Cargo lock metadata to version `0.2.4`.

## [0.2.3] - 2026-07-28

### Added

- Added trust-on-first-use SSH host key verification: saving a Host now scans for its host key fingerprint, shows it for explicit confirmation, and never silently degrades a failed scan into an empty fingerprint. Hosts missing a fingerprint are surfaced in the Host list, and a rescan flow covers hosts that were legitimately reinstalled.
- Added one-click local SSH key import to the Host form, backed by a read-only endpoint that scans local SSH private key candidates by content, reports paths under the home directory in `~`-prefixed form, and flags encrypted keys (including PKCS8) without attempting to decrypt them.

### Changed

- Updated repository, agent runtime, desktop package, Tauri, Cargo metadata, and Cargo lock metadata to version `0.2.3`.

### Fixed

- Repaired a deadlock that left a reinstalled Agent permanently unable to provision, stuck on a `401` or a transport `EOF` with no way to self-heal. Five independent causes each reproduced it on their own: the Agent ignored the new bootstrap token written by the reinstall because its on-disk state was already provisioned; the installer deleted `security.json` while the old Agent was still running, letting it write the provisioned state straight back; `tls.mode=auto` sent HTTPS before the certificate existed, and the failing handshakes tore down and reconnected the tunnel in a loop that interrupted the in-flight provision; the long-lived token was persisted only after a successful response, so a response lost in transit left the Agent advanced while the desktop retried with a fresh token that could never hit the idempotent branch; and missing or expired bootstrap tokens surfaced as a bare `401` with no logs.
- Distinguished an ordinary Agent restart from a genuine reinstall by recording the consumed bootstrap token hash, so a restart of the same installation is no longer knocked back to pending bootstrap. Note: upgrading from an earlier version requires pushing the security configuration once more, because a pre-upgrade provisioned state carries no consumed hash and is indistinguishable from a reinstall.
- Prevented four classes of stale-async result leaking across Hosts: an in-flight host key or local key scan that resolved after the edit target changed could pair one Host's fingerprint or imported key path with another Host's form, including for two fingerprint-less Hosts sharing one address.
- Invalidated a captured fingerprint when the address or port is edited while the confirm card is open, so a fingerprint scanned from one host can no longer be pinned against a different address.
- Waited out transient SQLite lock contention when opening the store, so a second instance opening the same file while the background log cleaner holds a write lock no longer fails migration with `SQLITE_BUSY`.
- Repaired cross-platform CI failures: UTC-independent log context timestamps, LF-pinned `validation/windows-real` payloads so hashes and PowerShell contract matching survive Windows checkout, slash-separated zip entry resolution, relayed nested cleanup output, and uninstall contract stubs that run on non-Windows pwsh lanes.
- Repaired two type defects that broke the production `vue-tsc -b` build but passed the looser development `--noEmit` check.
- Relabeled the Host list rescan button as a standing action rather than a claim that the host was reinstalled, since it renders purely on the presence of a pinned fingerprint.

## [0.2.2] - 2026-07-23

### Added

- Added package-verified cross-platform runtime validation bundles and a strict target-native campaign runner covering the full live MCP surface, seven language runtime/debug providers, human operation approvals, one-time credential login, borrowed remote pipelines, redacted evidence, and fail-closed cleanup on five supported targets.
- Added a deterministic, portable Windows 10 22H2 x64 (build 19045) validation package that can be built on macOS, copied to a dedicated Windows host, and used to verify the frozen MSI/NSIS installers, all 75 packaged MCP tools, seven language providers, browser/code debugging, remote pipeline behavior, redacted evidence, and cleanup without claiming Windows results during packaging.
- Added campaign-owned, project/service-scoped debug credential leases that remain only in Agent process memory, expire automatically, and let Windows validation exercise the existing credential tool without persisting or evidencing the human-entered value.
- Added safe remote Agent uninstall that preserves Agent data by default, supports explicit data purge, provides version-matched manual Shell and PowerShell scripts, and offers configuration-only detach only as a warned fallback.
- Added a built-in **Grok** Agent Connector (Full): detect Grok CLI, install SuperDev MCP via `grok mcp` (`--scope user`), install Skill under `~/.grok/skills/superdev`, and install an owned SessionStart hook file under `~/.grok/hooks/` (Grok SessionStart is passive; guidance is Skill-first).

### Changed

- Updated repository, agent runtime, desktop package, Tauri, Cargo metadata, and Cargo lock metadata to version `0.2.2`.

### Fixed

- Removed non-functional Search filter/Open/Copy controls and heuristic trace-path claims, leaving the real project-log search, time-aligned service context, and pin workflow; remote installation now also rejects Windows ARM before upload when no matching agent binary is packaged.
- Unified Windows validation steps, scenarios, providers, installer lifecycle, tool coverage, report sections, and summaries on one fact-and-evidence-derived result contract, so unattempted work is no longer reported as failure or success and post-assertion responses remain auditable.
- Made the packaged Windows validation Runbook directly executable with stock Windows PowerShell 5.1 by preserving UTF-8 script bytes, avoiding automatic-variable parameter collisions, and keeping structured output readable without loaders or source rewriting.
- Prevented runtime log panels from reusing virtual-scroll state across workspace tabs or deployment source changes, and delegated bottom reconciliation to TanStack Virtual to avoid large blank gaps below visible logs.
- Made remote Agent uninstall retry idempotent after a partial `config_remove` failure, and stopped reporting configuration-only detach as a remote uninstall.
- Kept detach provenance out of uninstall recovery paths, and fail closed on invalid agent-removal recovery modes so rejected recoveries perform no tunnel invalidation, apply, or status side effects.
- Hardened Grok connector finish logging, shell quoting, DTO shaping, and error codes for clearer install/uninstall diagnosis.

## [0.2.1] - 2026-07-17

### Changed

- Enabled signed and notarized macOS desktop release packaging in CI via App Store Connect API key and certificate import for agent-install binaries.

### Fixed

- Used a portable `GOCACHE` path in auth sidecar package contract tests so Linux CI no longer depends on the macOS-only `/private/tmp` path.
- Hardened Windows runtime packaging contracts and related validation evidence/approval lifecycle fixes for more reliable release gating.

## [0.2.0] - 2026-06-17

### Added

- Published SuperDev's first official release line with versioned GitHub Release automation for downloadable agent binaries and macOS, Linux, and Windows desktop packages.
- Added cross-platform remote agent release assets and an install script path that resolves from the versioned GitHub Release instead of a localhost-only development endpoint.
- Added sidebar project chips with pinned and recent ordering, plus a compact current-project context line for faster switching across multiple projects.

### Changed

- Expanded all sidebar environment groups by default from the main sidebar so deployments are immediately visible after project selection.
- Updated repository, agent runtime, desktop package, Tauri, Cargo metadata, and Cargo lock metadata to version `0.2.0`.

### Fixed

- Fixed the Agent settings page blanking after creating a new Agent when the backend runtime health snapshot is still empty.
- Preserved the legacy `dev` default-expanded behavior for standalone `EnvGroup` consumers while allowing the sidebar to explicitly control expansion.

## [0.1.2] - 2026-06-17

### Added

- Added Playwright-backed local browser debugging for SuperDev-managed frontend deployments, including browser discovery, isolated debug sessions, page snapshots, screenshots, console and network inspection, click/type/navigation/viewport controls, evaluate gating, audit records, and ephemeral or persistent isolated profiles.
- Added desktop settings for AI browser debugging, including Chromium-compatible browser detection, default browser selection, evaluate safety controls, profile mode, and session TTL.
- Added deployment-scoped code debugging with DAP capture-at-line support, paused-location snapshots, breakpoint/continue escape hatches, approval-aware MCP tools, and smoke documentation.
- Added schema-driven managed language runtime providers for Go, Node, Python, Java/Kotlin, Rust, C, and C++, with API and MCP provider/schema/suggest/validate/preview endpoints.
- Added desktop runtime UX for Run/Debug actions, debugger status, code debug policy configuration, language runtime config forms, browser-debug approval controls, and the sidebar getting-started guide.
- Added bundled js-debug resources, browser/code debug smoke docs, language runtime smoke docs, and SuperDev skill guidance for browser debugging, code debugging, and language runtime service creation.

### Changed

- Reframed the English and Chinese READMEs around the See / Inspect / Operate workflow, added the demo link, and updated the public capability descriptions for browser control and breakpoint debugging.
- Shifted code debugging from explicit debug sessions to deployment-scoped targets, made `service.language` a first-class runtime identity, and simplified code debug configuration around policy and overrides.
- Split service runtime health from debugger status so paused or debug-attached runtimes no longer overload service health.
- Improved runtime/process management with process-group reconciliation, stderr ring buffers, exit evidence, runtime lifecycle events, and run-scoped folded logs.
- Strengthened operation safety docs and MCP schemas for browser-debug and code-debug approval previews, evaluate auditing, and safe service/config workflows.
- Changed project licensing metadata to Apache-2.0 and added NOTICE coverage.
- Updated repository, agent runtime, desktop package, Tauri, and Cargo metadata to version `0.1.2`.

### Fixed

- Fixed Go, Node, and Python debug attach flows, including source-path normalization, working-directory-relative Delve programs, package-manager child process attach, Node inspector port discovery, and Python prearmed listen attach.
- Fixed JVM and native debug attach behavior by marking JVM support experimental, wiring JDWP prearmed listen runtimes, and aligning native attach with `lldb-dap`.
- Fixed local process state drift by reconciling managed services before runtime controls and retaining exit evidence for failures.
- Fixed getting-started flow issues around completed intro steps and keeping the onboarding popover inside the viewport.

## [0.1.1] - 2026-06-10

### Added

- Added GitHub community health files for issues, pull requests, contribution guidance, and security reporting.
- Added CI for Go agent tests, desktop dependency installation, desktop build, desktop tests, and version metadata checks.
- Added Dependabot configuration for GitHub Actions, Go modules, npm, and Cargo dependencies.
- Added release and versioning documentation for the first public release.

### Changed

- Standardized release metadata around a repository-level `VERSION` file.
- Removed local pnpm cache metadata, E2E output artifacts, and product-audit screenshots from tracked source.
- Removed internal Superpowers planning notes from tracked source.

## 0.1.0

This is the planned first public release line. No public release tag has been cut yet.
