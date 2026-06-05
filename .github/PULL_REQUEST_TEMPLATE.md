## Summary

- 

## Validation

- [ ] `cd agent && go test ./...`
- [ ] `cd desktop && pnpm build`
- [ ] `cd desktop && pnpm test`
- [ ] Other:

## Runtime Safety

- [ ] No write operation bypasses preview, approval, and audit boundaries.
- [ ] No secret, token, local path, generated binary, or machine-specific artifact is committed.
- [ ] Changes that touch agent, MCP, remote execution, ingress, or approvals include security notes.

## Release Impact

- [ ] No release metadata change.
- [ ] Version, changelog, and release notes are updated together.
- [ ] This PR requires a migration or operator action:
