# Security Policy

## TSLink's Security Model

TSLink is designed around explicit capability boundaries. The machine-readable product SSOT is `internal/security/capabilities.v1.json`; public docs and help text must stay within that manifest.

- **No public internet exposure by default** — services are private to your Tailscale network unless proxy-only Funnel exposure is explicitly acknowledged
- **Explicit public exposure guardrail** — Tailscale Funnel is proxy-only and requires an explicit `--public` / `public_ack:true` acknowledgement
- **Tailnet transport encryption** — traffic between Tailscale devices uses WireGuard; public Funnel paths follow Tailscale Funnel semantics
- **HTTP identity verification for proxy/file requests** — Tailscale WhoIs authenticates TSLink-managed HTTP requests before identity headers are injected; raw TCP streams and public Funnel exposure are not treated as TSLink-enforced Tailscale user authentication
- **Credential storage with restricted fallback** — API keys and OAuth client secrets are stored in macOS Keychain / Linux secret service when available, with restricted-permission file fallback for headless or unavailable-keychain environments
- **Dynamic auth key derivation** — API-token-derived startup auth keys are generated on demand and not persisted; legacy authkey files may still be read for compatibility and should be migrated
- **Per-service isolation** — each service runs as its own tsnet node with an independent identity
- **Inbound header stripping** — identity headers from external sources are stripped to prevent spoofing

Important boundaries:

- `--allow` is HTTP access control for proxy and file services. Raw TCP services do not receive TSLink HTTP identity filtering; protect them with Tailscale/Headscale policy, tags, tailnet membership, and the target service's own authentication.
- `tslink doctor`, `tslink status --urls`, and `tslink access explain` are local evidence tools. They do not prove live remote Tailscale ACL/grants, Funnel reachability, or backend application authentication unless those checks are explicitly added in the future.
- `tslink api` is a local JSON-over-stdin/stdout interface. It is not a REST/admin server and does not create a member-facing service directory.
- Remote Tailscale ACL mutation is disabled by default. `tslink login --manage-acl`, `tslink serve --manage-acl`, and `tslink tags delete-remote --manage-acl` opt in to typed whole-policy ACL writes with a machine-readable side-effect plan. Default login, serve, and tag flows do not rewrite shared ACL policy.
- Per-service tsnet nodes provide network identity and routing separation. TSLink does not provide host process isolation or a compliance attestation.
- Atomic writes into existing directories reject foreign-owned and group- or world-writable parents on Unix. On Windows, TSLink does not validate the parent directory's DACL: Go's `os.FileMode` exposes only synthesized bits that do not represent Windows access control. Windows callers must provision an appropriately restricted DACL when the parent directory is security-sensitive.

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
