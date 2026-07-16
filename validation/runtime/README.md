# Cross-platform runtime and MCP validation

This directory defines the strict validation campaign for the five targets in
`targets.txt`:

- `darwin/amd64`
- `darwin/arm64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`

`package_verified` and target `PASS` are different claims. A builder may
cross-compile and hash a bundle on any supported build host, but only a complete
run on the matching native OS and architecture may produce target `PASS`.
Rosetta, Wine, emulation, containers with a mismatched machine architecture,
and cross-compilation never count as native execution.

## Safety model

Run this campaign only on a dedicated, resettable validation machine. The
runner starts the packaged Agent and MCP against a disposable clone; it must not
reuse a production profile or an Agent already serving another task.

The foundation is a read-only, topology-only baseline. It may contain only the
borrowed non-self Linux Host/Agent/Tunnel topology needed by the remote pipeline
scenario and the local browser definition needed by browser validation. It must
not contain projects, managed deployments, PIDs, active browser/code-debug
sessions, credential leases, operation approvals or grace records, pipeline
runs, artifacts, or `start_on_boot` candidates.

The foundation and the remote Linux node are borrowed resources. The campaign
may create campaign-owned state in the clone and under
`/srv/superdev-runtime-validation/<campaign-id>`, but it must not create, update,
delete, or relabel the borrowed Host/Agent/Tunnel records.

The cleanup journal records both lifecycle-owning roots and every MCP write
call. A write receives `intent` before the call and `acquired` after application
success, but it is not marked `released` merely because the call returned.
Projects, sessions, browser/debug children, and other nested Agent state are
transitively owned by the disposable clone/process roots; their write entries
are released only after those roots and the remote pipeline guard are clean.
Credential leases additionally have their own exact create/delete action. The
borrowed Host/Agent/transport projection is read live before the first business
mutation and again before process-tree cleanup, and both safe projection digests
must match.

The local control plane deliberately uses loopback with authentication and TLS
disabled. This is acceptable only because the machine and profile are dedicated,
the Agent binds to `127.0.0.1`, the profile permissions are restricted, and the
profile is not shared with an ordinary Agent process.

## Prepare the foundation

Create a new directory outside the extracted bundle. Never point
`foundation_path` at an existing SuperDev profile. At minimum it contains:

```text
runtime-validation-foundation/
├── validation-profile.json
├── security.json
├── settings.json
├── hosts.json                 # borrowed non-self Linux host
├── agents.json                # borrowed remote Agent identity
└── tunnels.json               # if the topology requires a tunnel
```

`validation-profile.json` opts into the exact validation contract:

```json
{
  "kind": "superdev.runtime-validation.profile",
  "profile_id": "runtime-validation-dedicated",
  "allow_strict_validation": true,
  "foundation_read_only": true,
  "baseline_policy": "borrowed-topology-only"
}
```

`security.json` must represent an unprovisioned loopback-only control plane:

```json
{
  "require_auth": false,
  "provision_state": "open",
  "tls_mode": "off"
}
```

Approval and grace stores may be absent. If preparation tooling creates them,
they must use the current Agent object schemas rather than legacy empty arrays:

```json
{"approvals": []}
```

```json
{"grants": []}
```

The runner rejects a schema-incompatible empty file before creating
`active.json`; an empty container that the Agent cannot load is not a valid
topology-only baseline.

The borrowed Host selected by `remote_host_id` must carry the exact
`superdev-validation-dedicated-resettable` tag, and `agents.json` must contain
that Host ID with a non-empty transport chain. The runner verifies these facts
before creating `active.json`; live `list_hosts` later binds the same Host to the
out-of-band `expected_remote_identity` and `is_self=false` evidence.

Do not add token hashes, certificate paths, keys, passwords, cookies, or other
credentials to that file. `settings.json` must keep all mutation approvals on,
keep `grace_minutes` inside the product-valid range, explicitly allow the
read-only browser evaluate probe, and point to a target-native
Chromium-compatible executable. The runner never requests `grant_grace`, so the
configured grace duration is inert for this campaign:

```json
{
  "log_retention_days": 7,
  "log_max_bytes": 268435456,
  "log_cleanup_interval_seconds": 3600,
  "artifact_keep_versions": 10,
  "approval": {
    "config_upsert": true,
    "pipeline_upsert": true,
    "pipeline_run": true,
    "template_import": true,
    "browser_debug_open": true,
    "code_debug_open": true,
    "code_debug_evaluate": true,
    "grace_minutes": 15
  },
  "debug_browser": {
    "default_browser_id": "validation-chromium",
    "profile_mode": "ephemeral",
    "allow_evaluate": true,
    "session_ttl_minutes": 30,
    "browsers": [
      {
        "id": "validation-chromium",
        "name": "Validation Chromium",
        "executable_path": "/absolute/target-native/path/to/chromium"
      }
    ]
  }
}
```

Leave `projects.json`, `pids.json`, `debug-sessions.json`,
`operation-approvals.json`, and `operation-grace.json` absent or empty. Leave
`browser-debug/`, `code-debug/`, and `artifacts/` absent or empty. Check the
borrowed remote identity out of band and record it in the governance attestation;
do not infer it from the same foundation files being attested.

On Darwin and Linux, make the foundation root inaccessible to group/other and
make the marker, security, settings, hosts, and agents files owner-only:

```sh
chmod 700 /absolute/path/to/runtime-validation-foundation
chmod 600 /absolute/path/to/runtime-validation-foundation/*.json
```

On Windows, remove inherited access and grant only the dedicated validation
account full control. The runner rejects reparse points but deliberately does
not rewrite or weaken DACLs:

```powershell
icacls C:\validation\runtime-validation-foundation /inheritance:r
icacls C:\validation\runtime-validation-foundation /grant:r "$env:USERNAME:(OI)(CI)F"
```

## Prepare native dependencies

Each target machine needs target-native installations of:

- Go, Node.js, Python 3, a JDK, Kotlin, Rust, and a C++ compiler/CMake;
- `dlv`, a debugpy DAP adapter, a JVM DAP wrapper, and `lldb-dap`;
- a Chromium-compatible browser configured in the foundation;
- filesystem permissions to create the results and foundation sibling state
  directories;
- network reachability to the already governed non-self Linux Agent/Tunnel.

The bundle contains js-debug and a target-native Playwright driver. Runtime
downloads are forbidden. Prepare each driver on its matching native staging host
before building packages. From the repository's `agent` directory, this command
downloads only the Playwright driver into the target directory; it does not make
that host a passing validation target:

```sh
PLAYWRIGHT_DRIVER_PATH=/absolute/drivers/darwin-arm64 \
  go run github.com/playwright-community/playwright-go/cmd/playwright --help
```

Repeat on the other four native staging hosts and collect the five directories
under one root named exactly `darwin-amd64`, `darwin-arm64`, `linux-amd64`,
`linux-arm64`, and `windows-amd64`. Every collected directory must contain the
Playwright root executable (`node` on Darwin/Linux or `node.exe` on Windows) and
`package/cli.js` for that target. Treat these as supply-chain inputs: transfer
them over an authenticated channel and verify their source before packaging.

Populate the repository js-debug resource with the existing desktop build step:

```sh
bash desktop/scripts/build-agent.sh
```

## Build package-verified bundles

Use an empty output directory. The builder copies the target-native resources,
cross-compiles the runner/Agent/MCP, writes the canonical manifest, creates an
archive, and writes an external archive checksum:

```sh
cd agent
go run ./cmd/build-runtime-validation \
  --repo-root /absolute/path/to/super-debug \
  --output /absolute/path/to/runtime-validation-packages \
  --playwright-drivers /absolute/path/to/playwright-drivers
```

Expected output contains one `package_verified` line per target. Preserve all of
these for every package:

- the extracted `superdev-runtime-validation-<os>-<arch>/` directory;
- `.tar.gz` on Darwin/Linux or `.zip` on Windows;
- the sibling `.sha256` archive digest;
- `bundle-manifest.json` and `bundle-manifest.sha256` inside the bundle.

The build result is not target `PASS` and must use package artifact names such as
`runtime-validation-package-verified-darwin-arm64`. Native results use the
separate name `runtime-validation-native-summary-darwin-arm64`.

## Prepare non-sensitive input

Copy `runtime-input.example.json` outside the bundle and replace every
placeholder. All paths must be absolute. Adapter paths must identify executables
or launchers native to the current target. The remote root template is fixed and
cannot be changed:

```json
"remote_root_template": "/srv/superdev-runtime-validation/{campaign_id}"
```

Copy `remote-governance-attestation.example.json` outside the foundation. Its
`remote_host_id` and `expected_remote_identity` must match the input, while
`dedicated_resettable` is `true` and `is_self` is `false`. Never put a token,
password, cookie, private key, or credential value in either JSON file; the
strict decoder rejects sensitive field names.

The Linux target must still borrow a different non-self Linux node for the
remote pipeline. Pointing the Linux target back at itself is invalid even when
the operating system and architecture happen to match.

## Run on the five native targets

Verify the external archive checksum, extract the archive without changing its
layout, then invoke the wrapper from the extracted root. Supply the one-time
test credential through stdin; never place it in a command argument, environment
file, input JSON, CI log, or report:

```sh
printf '%s\n' "$ONE_TIME_TEST_CREDENTIAL" | \
  ./run-validation.sh \
    --input /absolute/path/to/runtime-input.json \
    --target darwin/arm64
```

PowerShell equivalent:

```powershell
$credential | .\run-validation.cmd `
  --input C:\validation\runtime-input.json `
  --target windows/amd64
```

The runner is unattended. For every operation that returns `approval_required`,
its approval actor first registers a short-lived allowlist identity containing
the plan ID, kind, normalized target, fingerprint, and expiry. It re-reads the
official pending state, rejects identity drift, expiry, or duplicate pending
requests, and calls the real approve endpoint only for the exact match with
`grant_grace=false`. Unknown pending requests are never approved. Low-risk local
dev runtime controls may succeed without a pending approval according to the
product policy; operations whose frozen foundation policy requires approval
must not bypass this flow. Before the token retry, the runner rejects matching
`grace_granted` or `approved_by_grace` audit events. After the retry, it requires
exactly one matching `executed` event with the same approval ID and checks the
grace ban again. The packaged auth sidecar likewise receives its credential only
through an inherited anonymous stdin pipe.

Use the exact matching target on each machine. The runner checks the kernel and
native machine architecture through Darwin sysctl/uname, Linux `uname(2)`, or
Windows `IsWow64Process2`/`RtlGetVersion`, and blocks compatibility layers before
reading the foundation.

Exit codes are stable:

| Code | Meaning |
| --- | --- |
| `0` | Complete target-native `PASS` |
| `1` | Product, contract, integrity, assertion, evidence, or cleanup `FAIL` |
| `2` | Safe execution was prevented by a missing/invalid external prerequisite (`BLOCKED`) |

Always retain the complete campaign report directory printed by the runner. Do
not infer status from console text, `summary.md`, an archive checksum, a stale
report, or a partial directory. Only a current `summary.json` with
`kind=superdev.runtime-validation.summary`, `complete=true`, and a complete
`verdict` is authoritative; it is written last by atomic rename.

## Required PASS evidence

For every one of the five summaries, require all of the following:

- target and `native_host` match exactly and no compatibility layer is active;
- the bundle manifest, sidecar, payload, modes, sizes, and hashes verify;
- `live_tools` exactly equals the manifest's unique primary tool assignments;
- every primary evidence assertion passed;
- Go, Node.js, Python, Java, Kotlin, Rust, and C++ each have runtime and debug
  `PASS` with real readiness/probes and DAP stack/scope/variable evidence;
- browser evaluate and approved code evaluate succeeded rather than being
  replaced by a policy denial;
- the remote node is attested non-self and the foundation/topology digests are
  stable before and after the run;
- the cleanup journal is complete, the pipeline is terminal, the remote root is
  absent, the active marker is removed, and residuals are empty;
- the evidence manifest digest matches all retained, redacted evidence files.

Preserve the full redacted process evidence and the manifest/archive digests.
The runner propagates the strict exit code; CI wrappers must not use `|| true` or
replace it with upload success.

## Compare the five summaries

First compare the invariant contract projection. The output must be identical
for all five files:

```sh
jq -S '{
  schema_version, kind, complete,
  verdict: {status: .verdict.status, complete: .verdict.complete},
  live_tools: (.live_tools | sort),
  coverage: {
    complete: .coverage.complete,
    missing_primary: (.coverage.missing_primary | sort),
    unexpected_primary: (.coverage.unexpected_primary | sort),
    duplicate_primary: (.coverage.duplicate_primary | sort),
    assignments: (.coverage.assignments | sort_by(.tool, .scenario_id, .step_id))
  },
  languages: (.languages | map({provider, runtime_status, debug_status}) | sort_by(.provider)),
  cleanup
}' */summary.json
```

Then review the full diff. Only target, native path syntax, tool/runtime versions,
timestamps/durations, PIDs/ports, campaign/resource identities, and the resulting
bundle/evidence digests may differ. A missing tool, different primary assignment,
different assertion, policy denial, cleanup residual, or schema difference is a
validation failure, not an allowed platform variance.

## Interrupted run and manual reset

After entering the mutation phase, an abnormal kill intentionally leaves
`active.json` under:

```text
<foundation-parent>/.runtime-validation/<profile-id>/active.json
```

The next run returns `BLOCKED`. This is fail-closed behavior, not a stale-lock
bug. Do not delete only `active.json`, because that would discard the identities
needed to inspect campaign-owned residuals.

Use the marker and `campaigns/<campaign-id>/cleanup.jsonl` to perform a manual,
operator-reviewed reset:

1. stop only the packaged Agent, MCP, auth sidecar, fixtures, adapters, and
   browser/debug sessions named by that campaign;
2. cancel/wait the named pipeline run to a terminal state;
3. remove only `/srv/superdev-runtime-validation/<campaign-id>` on the borrowed
   node through the governed Agent/Tunnel path;
4. verify the borrowed Host/Agent/Tunnel records and foundation digest still
   match their pre-run identities;
5. remove the named disposable clone/work directory and confirm no campaign
   process, lease, artifact, port, or remote path remains;
6. archive the marker, journal, and diagnostic evidence outside the state root;
7. only after those checks, delete the whole fixed
   `<foundation-parent>/.runtime-validation/<profile-id>` state directory.

Recreate the state directory by starting a new run; the runner does not replay
old journals or implement cross-version automatic recovery. If any residual or
borrowed-topology drift cannot be explained and repaired, keep the marker and
classify the machine as blocked instead of forcing a new campaign.
