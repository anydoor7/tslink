# Security Policy

## TSLink's Security Model

TSLink is designed around zero-trust principles. Security is not an afterthought — it is a core architectural decision:

- **No public internet exposure by default** — all services are only accessible within your Tailscale network
- **End-to-end WireGuard encryption** — all traffic is encrypted between devices
- **Identity verification on every request** — Tailscale WhoIs authenticates each request
- **System keychain credential storage** — API keys are stored in macOS Keychain / Linux secret service, not in plaintext files
- **Dynamic auth key derivation** — authentication keys are derived on demand and never persisted to disk
- **Per-service isolation** — each service runs as its own tsnet node with an independent identity
- **Inbound header stripping** — identity headers from external sources are stripped to prevent spoofing

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do not** open a public GitHub issue
2. Open a private GitHub Security Advisory: <https://github.com/monody0007/tslink/security/advisories/new>
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

Private GitHub Security Advisories are the supported vulnerability intake channel for this repository. Maintainers coordinate disclosure, fixes, and credit in the advisory thread before any public issue or pull request is opened.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Previous release | Best effort |
| Older versions | No |

## Scope

The following are in scope for security reports:

- Authentication bypass or credential exposure
- Unauthorized access to services
- Identity header spoofing
- Privilege escalation
- Denial of service against the TSLink daemon
- Vulnerabilities in dependencies that affect TSLink

The following are out of scope:

- Issues in Tailscale itself (report to [Tailscale](https://tailscale.com/security))
- Social engineering
- Issues requiring physical access to the machine running TSLink
