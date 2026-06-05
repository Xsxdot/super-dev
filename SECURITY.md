# Security Policy

SuperDev connects AI agents to local runtime state, logs, deployments, ingress, and operation approvals. Please report vulnerabilities privately so maintainers can investigate before details are public.

## Supported Versions

SuperDev has not shipped a stable public release yet. Until `v1.0.0`, security fixes target the latest `main` branch and the most recent public release tag, if one exists.

| Version | Supported |
| --- | --- |
| `main` | Yes |
| Latest public release | Yes |
| Older pre-releases | Best effort |

## Reporting a Vulnerability

Use GitHub private vulnerability reporting:

[Report a vulnerability](https://github.com/Xsxdot/super-dev/security/advisories/new)

If private vulnerability reporting is unavailable, contact the maintainers through the GitHub repository owner profile and avoid posting exploit details in a public issue.

Please include:

- Affected version, commit, or release artifact
- Operating system and architecture
- Reproduction steps or proof of concept
- Impact assessment
- Whether local services, remote hosts, approval tokens, logs, or secrets are involved

Do not include production secrets, approval tokens, private keys, or unredacted customer logs.

## Response Expectations

Maintainers aim to acknowledge valid reports within 72 hours, triage severity within 7 days, and publish fixes as soon as practical. Coordinated disclosure timelines may vary based on exploitability, affected surfaces, and release complexity.

## Security-Sensitive Areas

Extra care is required for changes touching:

- MCP tool implementations
- Local agent HTTP handlers
- Remote execution and file transfer
- Operation preview, approval, token, and audit flows
- Ingress, DNS, TLS, and reverse proxy configuration
- Log collection, redaction, and export
- Installer, onboarding, and bundled sidecar binaries

Runtime write operations must remain previewable, approvable, short-lived, auditable, and scoped to the exact operation the user approved.
