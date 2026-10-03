# Architecture

TSLink is an app access and management layer for a computer, server or cloud
machine. Use it for your own cross-device access, named private sharing, or
deliberately enabled public browser access. It runs one shared daemon per host, with a dedicated
[tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service.
Each node has its own network identity and hostname. The publishing machine
does not need a separate Tailscale client installation.

## Configuration and request paths

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="One app host with private Tailscale access, optional public HTTPS/Funnel for HTTP proxy apps, per-service nodes and local targets. CLI/MCP manages the registry and daemon; the private portal, health and access history serve this host." width="960">
</picture>

Dashed arrows show management; solid arrows show incoming access routes (responses
return along the same route). Each row is an example service, not a separate
host. Tailscale supplies encrypted private transport and HTTPS; TSLink supplies
per-app registration, access policy and operations. Repeat the setup independently
on each host. There is no cross-host aggregate inventory or cloud VPC creation.

1. **Configure.** CLI and MCP service-management operations persist service
   settings in `~/.config/tslink/registry.json`. The file survives restarts.
2. **Reconcile.** The shared daemon watches the registry and applies changes
   to its embedded nodes without a daemon restart. Each service has its
   own node identity; a fresh node must complete Tailscale enrollment.
3. **Connect.** A permitted tailnet device reaches the selected node. An HTTP
   proxy forwards to an existing app or model API; a file service reads the
   configured files directly; a TCP service forwards bytes to its target.
4. **Optionally publish.** Public HTTPS/Funnel is an explicit opt-in for HTTP
   proxy apps. A guest-gated app requires an unexpired bearer link/session and
   optional PIN; a separate open publication is reachable by anyone with its URL.
   Public callers do not acquire a Tailscale user identity. A guest label is a
   note for the owner, not proof of who visited. Raw TCP and direct file services
   do not use this public path. See [guest links](guest-links.md) and [Funnel](funnel.md).
5. **Maintain.** The private [portal](portal.md) lists the current visitor's
   permitted apps. [Health checks and alerts](health-and-alerts.md), bounded
   [access history](access-log.md) and [MCP roles and audit](mcp-scopes.md) support
   ongoing operation. The portal is not a public gateway or a network permission
   shortcut; an outside-tailnet recipient still needs the appropriate node shares.

The nodes share a process and publishing host. Separate network identities
allow separate tailnet rules; they do not isolate host processes or data.
A model endpoint is an ordinary HTTP proxy target. The application handles
model inference and any document processing, as shown in [Local AI](local-ai.md).

## Access and identity

- **Tailnet policy** governs which devices can reach each node. HTTP proxy
  and file nodes use Tailscale HTTPS listeners. Raw TCP uses private tailnet
  transport; it is not an HTTPS listener.
- **Optional HTTP allow lists:** with `--allow`, WhoIs must succeed and the
  caller must match, or the HTTP proxy/file request receives 403 before the
  backend or file handler. [People grants](people.md) additionally enforce
  named-user app membership, deadlines and revocation. A people-scoped app does
  not become unrestricted when its last grant is removed. Without people policy
  or an allow list, tailnet policy governs reachability; identity headers and logs
  are best effort. Already accepted private streams can finish after revocation;
  guest streams have their own cancellation semantics.
- **Identity headers:** the proxy strips client-supplied Tailscale identity
  headers, including underscore variants, even if WhoIs fails. Public Funnel
  callers are not treated as TSLink-enforced Tailscale user identities.
- **Raw TCP** has no HTTP WhoIs/allow-list layer. Use tailnet policy and the
  target application's authentication.

MCP is a management interface, not a hop in ordinary service requests. Local
MCP uses stdio; optional [remote MCP](remote-mcp.md) has its own dedicated
node and explicit caller configuration.

People grants, guest grants, access requests and portal configuration share the
atomic registry. Scoped MCP mutations combine role, app and lifetime checks with
the current portal-owner check inside the writer transaction. The shared duration
policy applies to people, guest links, approvals and extensions.

The private home portal shows each visitor's permitted apps and offers requests
only for explicitly requestable private HTTP/file apps. Browser guest links use
a bearer gate on the public Funnel listener; private traffic to the same app
still passes through people authorization. Guest apps are not requestable.

Mutation intent/completion receipts and committed lifecycle changes use a
separately bounded, locked journal. Access-log queries combine that journal with
daemon-owned HTTP/TCP/guest-use segments, applying app scopes before aggregation.
Registry writes and lifecycle receipts are separate commits; see
[access history](access-log.md) for audit gaps and retention boundaries.

## Operational details

- Credentials use the system keychain. On macOS/Linux, restricted-permission
  file fallback succeeds only after stale keychain authority is proven absent
  or cleared.
- Daemon lifecycle uses PID and process-identity checks, with platform-specific
  stop behavior. See [daemon lifecycle](daemon-lifecycle.md).
- Structured logging uses `slog` for diagnostics and bounded asynchronous local
  access history for HTTP/file requests and TCP connections. See [access log](access-log.md).
