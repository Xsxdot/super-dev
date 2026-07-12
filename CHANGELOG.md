# Changelog

All notable changes to SuperDev will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) for public releases.

## [Unreleased]

### Added

- Added a built-in **Grok** Agent Connector (Full): detect Grok CLI, install SuperDev MCP via `grok mcp` (`--scope user`), install Skill under `~/.grok/skills/superdev`, and install an owned SessionStart hook file under `~/.grok/hooks/` (Grok SessionStart is passive; guidance is Skill-first).

### Fixed

- Prevented runtime log panels from reusing virtual-scroll state across workspace tabs or deployment source changes, and delegated bottom reconciliation to TanStack Virtual to avoid large blank gaps below visible logs.

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
