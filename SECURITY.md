# Security Policy

## TSLink's Security Model

TSLink is designed around explicit capability boundaries. The machine-readable product SSOT is `internal/security/capabilities.v1.json`; public docs and help text must stay within that manifest.

- **No public internet exposure by default** — services are private to your Tailscale network unless proxy-only Funnel exposure is explicitly acknowledged
- **Explicit public exposure guardrail** — Tailscale Funnel is proxy-only and requires an explicit `--public` / `public_ack:true` acknowledgement
- **Tailnet transport encryption** — traffic between Tailscale devices uses WireGuard; public Funnel paths follow Tailscale Funnel semantics
- **HTTP identity verification for proxy/file requests** — Tailscale WhoIs authenticates TSLink-managed HTTP requests before identity headers are injected; raw TCP streams and public Funnel exposure are not treated as TSLink-enforced Tailscale user authentication
- **Credential storage with restricted fallback** — API keys and OAuth client secrets use macOS Keychain / Linux secret service / Windows Credential Manager when available. On macOS and Linux, restricted-permission file fallback succeeds only after TSLink proves any stale keychain credential absent or removed. A completely unreachable or uncertain keychain causes an explicit failure to preserve existing credential authority; restore keychain access and retry. Windows has no file fallback, because TSLink cannot prove a user-only DACL locally
- **Dynamic auth key derivation** — API-token-derived startup auth keys are generated on demand and not persisted; legacy authkey files may still be read for compatibility and should be migrated
- **Per-service isolation** — ordinary services run as their own tsnet nodes; explicit portal-only apps share the gateway identity
- **Inbound header stripping** — the proxy strips client-supplied `Tailscale-*` and `X-Tailscale-*` identity headers, including underscore and mixed spellings, before injecting WhoIs identity

Important boundaries:

- `--allow` is HTTP access control for proxy and file services. Raw TCP services do not receive TSLink HTTP identity filtering; protect them with Tailscale/Headscale policy, tags, tailnet membership, and the target service's own authentication.
- `tslink doctor`, `tslink status --urls`, and `tslink access explain` are local evidence tools. They do not prove live remote Tailscale ACL/grants, Funnel reachability, or backend application authentication unless those checks are explicitly added in the future.
- `tslink mcp` is a local stdio MCP server. It is not a REST/admin server and does not create a member-facing service directory.
- Ordinary tag ACL writes require `--manage-acl` on `login`, `serve`, or `tags delete-remote`. Acknowledged Funnel services are a separate default-on case: auto-provisioning may add the shared `tag:tslink-funnel` owner and `nodeAttrs` grant to the tailnet policy file. Use `--no-auto-provision` on `add --funnel`, `serve`, or `install` to disable its service or daemon setup path. The side-effect plan is machine-readable.
- Per-service tsnet nodes provide network identity and routing separation. TSLink does not provide host process isolation or a compliance attestation.
- Atomic writes into existing directories reject foreign-owned and group- or world-writable parents on Unix. On Windows, TSLink does not validate the parent directory's DACL: Go's `os.FileMode` exposes only synthesized bits that do not represent Windows access control. Windows callers must provision an appropriately restricted DACL when the parent directory is security-sensitive.
- Proxy and TCP target validation refuses literal link-local and unspecified addresses, known cloud-metadata addresses (`fd00:ec2::254`, `100.100.100.200`) and hostnames (`metadata.google.internal`, `metadata.tencentyun.com`, `instance-data`, `metadata`), and numeric respellings of refused IPv4 addresses. Loopback shorthand such as `127.1` is accepted. It never resolves DNS: another hostname that resolves to a refused address is accepted at registration and dialed by the daemon. Treat the check as a guard against accidental or injected targets, not as an SSRF boundary; a dial-time address check is tracked as follow-up work.
- The `logs` MCP tool's redaction narrows what incidentally reaches a model's context; it is not an isolation boundary. The same log file is emitted verbatim by the `tslink logs` CLI, so any agent with a shell reads the originals.
- That redaction does not cover a capability separated from its host by a delimiter. `mcpLogsTailscaleURLPattern` ends a URL at whitespace, a quote, an angle bracket or `)`, and requires one or two slashes after `https:`. Both choices are deliberate -- the token class is what lets the rule find a URL inside a quoted log field, and the slash count is what made it match `https:/login...` -- but they leave two shapes that reach `sanitizeLogLine` intact:

      https://login.tailscale.com<SP|TAB|CR|">/a/<token>
      https:login.tailscale.com/a/<token>          (zero slashes, url.Opaque)

  Nothing in TSLink emits either shape: `internal/logging` routes tsnet's authorization URL through slog as one unbroken token. The exposure is a log line authored elsewhere -- a third-party library, a user pasting into a log, a future formatter that wraps long lines. Widening the token class is not a fix on its own: it trades this leak for over-redacting ordinary prose that mentions the host.

## Gateway boundaries

Gateway mode is private to Tailscale and persistent. Its own checks remain the
security boundary even when tailnet policy permits every gateway port. Every
connection is identified before TLS; every request or upgrade repeats WhoIs and
people policy. Directory visibility does not grant app access. Registry failures
fail closed, zero people never permits a routed app, and accepted streams may
finish after a later revocation.

Browser identity is ambient: an unrelated page can cause requests made as the
visitor. Tailnet pages can be same-site, so routes enforce same-origin checks,
including the port. Top-level navigations and non-browser requests still need
identity and grants. `--allow-cross-origin` explicitly weakens this browser
boundary. Ports are not a cookie sandbox; untrusted apps on one gateway hostname
can collide on cookie names. Cookie namespacing is planned. Reserved gateway
cookies are stripped, and request-controlled Host/URLs cannot change a target.

An untagged gateway could be recognized remotely as its enrolling human, including
that human's owner privilege. Remote routes therefore require an **observed**
tagged gateway, an HTTPS MagicDNS name in its observed tailnet suffix and normal
TLS verification. The registered hostname must match a peer DNSName in the
gateway LocalAPI status at route activation, for each new upstream connection
and on health probes. Traffic and probes resolve the registered name only
against that peer list, require its Tailscale IP to pass
`UseNetstackForIP`, and call `NetstackDialTCP` directly. They do not call tsnet
`Dial`/`UserDial`, the host resolver, or a host-network dialer. A disappearing
peer fails in netstack without host fallback. TLS verifies the registered
hostname. The tag and tailnet suffix are rechecked on every request, including
requests that reuse an upstream connection. Remote hosts
must explicitly trust the gateway tag. Visitor Tailscale identity and IP headers
are stripped and are not replaced with human claims; signed assertions are not
implemented. Local upstreams continue receiving verified visitor identity.

One shared gateway device enables network reachability to all its route ports;
it does not authorize all apps. Gateway invitations require exact NodeID and
local ownership-ledger proof. Revocation is enforced locally even if an accepted
network share remains. Changed node identity invalidates the assumption that old
shares reach the current gateway, and is reported for owner recovery.

## Dependency advisory GO-2026-5932

The [Go vulnerability database](https://pkg.go.dev/vuln/GO-2026-5932) identifies `golang.org/x/crypto/openpgp` and its six subpackages as unmaintained and unsafe, with no known fixed release. TSLink requires other `x/crypto` packages through Tailscale. As reviewed on 2026-09-30, the package and test import graphs for all six supported release targets contain none of the affected OpenPGP packages. The required-module advisory remains visible in `govulncheck` output. The release candidate gate scans `./...` for all six OS/architecture targets so a future platform-specific affected import fails the gate; this statement must be rechecked when dependencies or targets change.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do not** open a public GitHub issue
2. Open a private GitHub Security Advisory: <https://github.com/anydoor7/tslink/security/advisories/new>
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

GitHub private vulnerability reporting is the vulnerability intake channel for this repository. The repository maintainer must enable it in GitHub settings. The maintainers coordinate disclosure, fixes, and credit in the advisory thread before any public issue or pull request is opened.

Expect an initial acknowledgement within 7 days. The coordinated disclosure target is 90 days from that acknowledgement; if a fix needs longer, the advisory thread says so and names a new date rather than going quiet.

## Supported Versions

| Version | Supported |
|---------|-----------|
| v0.1.x | Yes |
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
