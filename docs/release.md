# Release and Versioning

This document defines how SuperDev controls versions and publishes releases.

## Version Policy

SuperDev uses Semantic Versioning for public releases:

```text
MAJOR.MINOR.PATCH
```

- Increment `PATCH` for compatible bug fixes.
- Increment `MINOR` for compatible user-visible features.
- Increment `MAJOR` for breaking changes after the project reaches `v1.0.0`.
- Before `v1.0.0`, breaking changes may happen in minor releases, but release notes must call them out clearly.

Pre-release tags may be used for validation builds:

```text
v0.1.0-alpha.1
v0.1.0-beta.1
v0.1.0
```

## Version Source of Truth

`VERSION` is the repository version source of truth.

The release manager must keep these files synchronized:

- `VERSION`
- `agent/internal/buildinfo/version.go`
- `desktop/package.json`
- `desktop/src-tauri/Cargo.toml`
- `desktop/src-tauri/tauri.conf.json`

CI runs:

```bash
node scripts/check-version.mjs
```

This prevents a release where the app bundle, package metadata, and repository version disagree.

## Release Checklist

1. Confirm `main` is green in GitHub Actions.
2. Update `VERSION`.
3. Update `desktop/package.json`, `desktop/src-tauri/Cargo.toml`, and `desktop/src-tauri/tauri.conf.json` to match `VERSION`.
4. Update `agent/internal/buildinfo/version.go` to match `VERSION`.
5. Update `CHANGELOG.md`.
6. Run local verification:

   ```bash
   node scripts/check-version.mjs
   cd agent
   go test ./...
   ```

   ```bash
   cd desktop
   pnpm install --frozen-lockfile
   pnpm build
   pnpm test
   ```

7. Build release artifacts from a clean checkout.
8. Sign and notarize macOS artifacts when signing credentials are available.
9. Generate checksums for all published binaries and archives.
10. Create an annotated tag:

   ```bash
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   git push origin vX.Y.Z
   ```

11. Create a GitHub Release with release notes, artifacts, and checksums.

## Release Notes

Release notes should include:

- Highlights
- Added, changed, fixed, and security sections
- Breaking changes
- Upgrade or migration notes
- Known issues
- Artifact checksums

For security-sensitive changes, describe impact and mitigation without publishing exploit details before users have a reasonable update window.
