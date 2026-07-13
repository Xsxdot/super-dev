# Windows-to-Linux pipeline validation assets

This directory contains the portable, campaign-scoped assets used by the Windows 10 x64 validation package to prove the SuperDev Agent pipeline route against a dedicated Linux x64 node.

Responsibilities:

- provide two immutable payload versions (`A` and `B`);
- provide the imported deploy template used by the campaign pipeline;
- provide the Linux release helper that validates ownership before activate, verify, or cleanup;
- keep every Linux write below `/srv/superdev-validation/<campaign-id>`.

Boundaries:

- these assets do not connect to a host or execute a deployment while the package is built on macOS;
- the Windows runner must supply the canonical non-self Host ID returned by `list_hosts`;
- the positive route is valid only when run logs contain `remote route host <id> -> agent` and contain no `-> ssh` line;
- tokens, TLS material, SSH credentials, approval tokens, and hostnames must never be written into these files or their evidence.

The Windows side does not execute Bash or this `.sh` file. The cross-platform `archive_package` plugin packages the helper as data, the `transfer` step sends it through the SuperDev Agent route, and only the dedicated Linux target invokes it with `sh` after the campaign root guard passes.

After packaging, a Windows-native PowerShell `Get-FileHash` step writes a campaign-owned SHA-256 sidecar under the project workspace. The artifact and sidecar are transferred separately; Linux compares the exact tar.gz digest before extraction. The A sidecar remains available for rollback, so rollback reuses the registered A artifact instead of rebuilding it.

The scenario imports `templates/remote-validation-deploy.yaml`, upserts a campaign-owned project pipeline, then performs deploy A, update B, rollback A, and exact cleanup. `deploy_project_pipeline` owns coverage only on deploy A; subsequent calls are marked as supporting calls so the frozen 75-tool manifest still assigns every tool exactly once.
