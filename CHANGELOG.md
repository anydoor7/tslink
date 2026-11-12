# Changelog

All notable changes to TSLink are documented here. The public compatibility
surface for the 0.x series is defined below; TSLink is a CLI, not a Go library.

## [Unreleased]

### Added

- TSLink now logs TLS handshake trouble per app: failed handshakes (`tls.handshake.failed`) and successful but slow ones over 2 s (`tls.handshake.slow`), each aggregated to one record per app per 10 s with a count, the error classes (`timeout_before_hello`, `timeout_after_hello`, `timeout`, `eof`, `reset`, `bad_record`, `client_alert`, `protocol`), the longest duration and, when known, the client's node name. Before this, a connection that died during the TLS handshake left no trace, because the access log only records completed requests. `timeout_before_hello` means no TLS ClientHello had been parsed within the handshake budget: either the client or network did not deliver it, or TSLink was too starved to read it, so it is not proof the server is innocent. `timeout_after_hello` means the ClientHello was parsed but the handshake did not finish in time. Plain `timeout` appears for every connection to a Funnel-enabled app, whose TLS configuration TSLink does not hook. Records carry no addresses, request data or login names.
- `tslink startup set <service> <priority>` chooses which services come back first after a daemon restart: the daemon starts nodes one at a time, higher priority first, registry order among equals; `tslink startup list` shows the order. Priorities are stored in `startup-priority.json` beside `registry.json`, which builds without this command ignore, so downgrading keeps every service serving and only loses the ordering.
- `tslink limits set <app>` (MCP `limits_set`; the app's node restarts to apply the change, so guests reopen their link) changes only an app's HTTP request limits (`--max-request-body`, `--request-header-timeout`, `--request-read-timeout`, `--idle-timeout`, with the unlimited acknowledgement rule) in one registry transaction, returns without waiting for the restart and says so (`node_restarting` and a `message` naming `tslink url <app> --wait`; with no daemon running `node_restarting` is false and the message says the limits apply when it starts), keeping target, access, routes, health, preserve-host, the guest gate and the enrolled node. It works on apps with guest links, where `add` is refused, and remote MCP needs the current portal owner for a routed or public app. Every hint that used to say to re-run `add` to change a limit now names it.
- `tslink guest create` (and MCP `guest_create`) report the app's per-request upload cap in `request_body_limit` (`max_body_bytes`, -1 for an acknowledged unlimited body, and a message with the `tslink limits set <app> --max-request-body <value>` command), and the human output prints it, so an upload app's owner sees the 32 MiB default before a guest's upload is refused.
- A recipe in the catalog can carry finite `request_limits`; a new service from that recipe uses them unless request-limit flags (MCP `request_limits`) are given.
- A panic while serving one connection (the TLS handshake and header-budget worker, gateway admission, or a raw TCP connection and its copy loops) now drops only that connection. It is logged at error level as `connection.panic_recovered` with the app, the panic, its stack and the running total for the process. Before, it stopped the daemon and every node had to come back.

### Changed

- An upload refused by a request limit now gets TSLink's JSON error envelope (code `request_body_limit` or `request_read_timeout`, with a message stating the limit, such as `at most 32 MiB (33554432 bytes) per request`) instead of a plain-text line that named the service and the owner's `--max-request-body` flag. The status codes (413, 408) are unchanged.
- The `request_body_limit`, `request_read_timeout` and `request_header_timeout` warnings in `status --urls`, `list --verbose` and `doctor` now say the app is rejecting requests, count the rejections since its node last started (and state that time; changing limits restarts the node and resets the count, while each 413 and upload 408 stays in `tslink access log`), give the most recent time and name the `tslink limits set` command that raises the limit. A connection that times out before sending any request byte, such as an idle Funnel or edge connection, still gets the 408 (and an Info-level log line) but no longer counts toward the `request_header_timeout` warning, which had told owners to raise the header timeout on that evidence. The first rejection is published at once and later ones within 30 seconds at the end of that window. Generated refusals carry `X-Content-Type-Options: nosniff`, and the request-header-timeout 408 no longer names the service or an owner flag.
- People grants on a guest-gated app: the refusal (`people_service_unsupported`) now says that grants given before the gate keep working on the private tailnet listener and names the start-over. `docs/guest-links.md` had said a gated proxy supports people grants; the grant action has refused new grants on gated apps since the common grant action was introduced.

### Fixed

- `tslink guest create` works on a new installation. New installations serve through the home gateway, so `tslink add` also gives the app a gateway route, and guest-gated apps cannot be routed: the documented `tslink add photos --proxy localhost:3000` then `tslink guest create photos --for 3d --public --print-link` failed with `portal_route_guest_refused` until `tslink portal route remove photos`. The first guest link now removes the route in the same registry write that saves the grant when only the owner and admins use it and the app's own node is running with an exact URL, and says so (`removed_gateway_route` in JSON and MCP, with the node URL; check `status`/`doctor` that the public Funnel listener came up, which the evidence cannot prove). Without that node evidence it refuses with `url_not_ready` and keeps the route, with or without `--print-link`. When people grants (including an admin who reaches TSLink only through the gateway), allow-list entries or a cross-origin route depend on it, or the app is portal-only, it refuses with `portal_route_guest_refused`, changes nothing and names the next command. Over remote MCP, a create needs the current portal owner (see Security), checked in the write transaction. With `--print-link`, creation now waits up to 8 seconds for a running daemon to publish the app's node URL, which covers the node restart the first link causes (measured at about 5 seconds until the URL is exact again). Exit code change: a node that still needs browser authorization now fails at once with `enrollment_required` (exit 3) instead of `url_not_ready` (exit 5).
- `tslink remove` (and MCP `unshare`, and a share's rollback) revoke the app's guest links in the same write, with one `guest_revoked` receipt each. They had stayed active in the ledger, so re-adding an app with the same name and creating a guest link for it made the removed app's links work again. Grants an earlier release left unrevoked are revoked, with receipts, when a new app of that name raises its gate, and a grant created before its app's current `created_at` never authorizes and lists as revoked, which also covers a name an earlier release already re-added and re-gated. A receipt that cannot be written after the commit no longer turns a finished `remove` or `guest create` into a failure: the result is reported with `audit_warning`, and remove still cleans up the node.
- `add` on a guest-gated app is still refused (the gate is sticky), and the refusal now names `tslink limits set` and `tslink health set` instead of only the start-over.
- `docs/guest-links.md` told owners to disable an open Funnel with an `add` that leaves `--funnel` out, which keeps the Funnel; it now says `--funnel=false`.
- The daemon no longer exits with a nil pointer dereference when a connection dies during its TLS handshake. Since the handshake logging above, recording such a handshake read the peer address after gVisor had already dropped it; each occurrence restarted the daemon and every node. The address is now read when the connection is accepted, so the failure is still attributed to the client. The gateway connection gate and TCP access records read the peer the same nil-safe way.
- A finished health check now frees its slot before reporting the result, so the next check of the same app is never skipped as still running.
- MCP `doctor` called by a reduced role (viewer, app-operator or people-manager) no longer reports a running daemon as stopped and a host with warnings as healthy. It used to return the owner's result shape with every field it could not show set to zero, so `daemon.running` was `false`, `health_status` was `healthy` with `health_exit_code` 0, and credential mode, supervision, guest links and node keys read as absent. It now returns its own shape: `daemon.running` and `daemon.state` exactly as `status` reports them, the health findings of the role's apps under `findings_scope: "app_health"`, and a `withheld` array naming the omitted fields, including the host-wide verdict. Reduced `status` had the same flaw (supervision `installed: false`, credentials `present: false`, `credential_stored: false`, access log disabled, no guest links) and now withholds those fields and the host-level `authenticated` and `node_authorized`. Its `authorized_service_count` is now the owner's own per-service decision, so it no longer reads 0 while the registry is unreadable. Reduced `health` and `list` name the per-service fields they remove or rewrite. `tools/list` advertises these reduced output schemas to reduced roles; owner schemas and results are unchanged, except that MCP `status` and `health` services gain an `authorized` flag.
- `tslink guest create <app> --print-link` (and MCP `guest_create`) issues a second link, or a replacement after `guest revoke`, while the guest Funnel gate is up. It had failed with `url_not_ready` because the guest-link and people URL lookup accepted only a tailnet `https` endpoint, and the gate reports the same node URL as `public_https`; for the same reason `people list`/`show` showed no `url` for an existing grant on an app that later became guest-gated. A failed node, a missing hostname or a stale runtime snapshot still returns `url_not_ready`, which now also says that nothing was saved and to rerun the create once `tslink url <app> --wait=30s` returns.

### Security

- Over remote HTTP MCP (`serve --mcp`), every change to a public app now needs the current portal owner, not only changes to a routed app. An app is public with an open Funnel, a lapsed Funnel that keeps its public acknowledgement, or guest links. The rule covers `add`, `share`, `recipe_apply`, `unshare`, `limits_set`, `health_set`, `tags_set`, `app_restart`, `extend` of the Funnel and `guest_create`, every write that makes a private app public, and settling another caller's tentative public share (an identical `share` or a `recipe_apply` naming it), and is checked inside the registry write transaction at the one point every write passes, on the registry loaded under the lock. Before, the first guest link took an app off the gateway and with it out of the portal-owner check, so a remote principal holding the MCP owner role (an `mcp.allow` login or tag, or an owner binding) who was not the portal owner could set an unlimited request body on the public app, mint itself a link with `print_link` and delete it; it could also make any unrouted private app public. **Breaking for remote operators:** such a principal now gets `access_request_owner_required` for these changes, including reducing ones such as unsharing a public app; revoking an existing guest link stays open to the MCP owner role so a leaked link can be pulled, and its receipt names the caller; run them as the portal owner, or on the gateway host with the local CLI or stdio MCP, which are unaffected. The rule applies once a portal owner is recorded or a home gateway is on; a legacy install with neither keeps the MCP owner-role rule. Private apps without a route are unchanged.
- Over remote HTTP MCP, creating a home gateway route needs the portal owner on every path: `template_apply` and `share` created routed apps for a remote non-owner although `add` refused them.
- On a home gateway whose owner is not set yet, remote person changes are refused: a remote MCP owner-role caller could revoke the login about to be bootstrapped as owner and lock it out of every remote change. The daemon no longer bootstraps a revoked login, and `tslink portal enable --owner <login>` restores a revoked owner login in the same write and reports it (`owner_restored`, a `portal_owner_restored` receipt), which is the local recovery. A `portal enable` that does not name the owner leaves a revoked owner revoked and reports `owner_revoked` with that command.
- The registry write guard no longer depends on file-lock exclusivity alone: a path's transactions are also serialized inside the process.

## [0.3.1] - 2026-10-27

### Upgrade

- No registry migration is required. After upgrading, run `tslink install` again if TSLink runs as a background service so it uses the new binary.

### Fixed

- Adding a person for another app, including re-adding a revoked person, no longer conflicts with an open GuestGate; on its private entry, a non-revoked person with no grant for that GuestGate keeps the unknown-human access baseline even when all their other grants have expired or latched, while explicit GuestGate grants and denials retain their existing rules. Re-adding a revoked person restores the unknown-user baseline on open GuestGate services.

## [0.3.0] - 2026-10-27

Gateway mode by default, and one action to give someone access. New installations start the persistent home gateway: a person outside your tailnet accepts one device share and you choose which apps they can use. Granting access is the same action in either mode: TSLink checks whether the person is in your tailnet, keeps what they already have, and completes the routes and invitations it needs. Access totals count each person's traffic per app the same way whether a request came through the gateway or an app's own node.

### Upgrade

- Homebrew: `brew upgrade --cask anydoor7/tap/tslink`. Scoop: `scoop update; scoop update tslink`. Other installs: download the new archive or package from this release.
- If TSLink runs as a background service, run `tslink install` again after upgrading so the service starts the new binary.
- Existing installations, including empty registries, keep their current mode; nothing is migrated. To use the gateway on an existing setup, run `tslink portal enable --owner <login> --gateway` (see the [portal guide](docs/portal.md)).

### Changed

- New installations default to the persistent home gateway. Existing setups keep their access paths. Private HTTP/file apps gain stable gateway routes while retaining their own nodes/URLs; `--portal-only` remains opt-in. Disabling the portal persists across later starts and registration. Untagged self enrollment can establish the first owner; tagged gateways require an explicit local owner.
- Human access through people, add/share allow, HTTP/file device invitations, MCP grants and request approval now uses one action: verify membership, preserve existing permissions, prepare routes and complete necessary invitations. People add is additive and retries preserve omitted deadlines and defaults a new grant to 24h. `add --allow` and the MCP `add`/`share` `allow` field keep the meaning they had before: a person named there has no expiry unless `--for`/`--until` (MCP `for`/`until`) limits it, without `--ack-never`, so a wrapper that runs `tslink add app --allow me@example.com` keeps its access; an explicit `--for never` still needs `--ack-never`, and re-adding never shortens an existing grant. `tslink invite device` and MCP `invite_device`, which share a device and never had a lifetime, give the same permanent default to a grant they create (an app already on the person's allow list stays permanent) and leave an existing grant's deadline alone; limit access with `people add --for/--until`. An MCP binding that expires, or a reduced role, cannot create a permanent grant by omission either: its omitted lifetime stays 24h. Incomplete setup returns an error with retained state; pending acceptance is a completed owner action.
- Device-invited guests can receive `never` grants with explicit acknowledgment. Browser guest links and Funnel remain finite; scoped MCP lifetime limits remain enforced.
- When an app's backend cannot be reached or times out, an app's own node now answers with the gateway's error response instead of plain text: an HTML page for a browser navigation, otherwise the JSON error envelope with code `upstream_down` (`photos on this host is not reachable right now`, or `did not respond in time` for a timeout). The status is unchanged on a direct app node (502, or 504 for a timeout). Through the gateway a backend timeout is now 504 instead of 502; other failures stay 502. A remote route refused by the gateway's own policy answers 503 with the specific `remote_upstream_*` code instead of 502 `upstream_down`.
- An access refusal on an app's own node is now the page the gateway gives for the same refusal, instead of the short `{"error":"access denied"}` JSON: an HTML page for a browser navigation, otherwise the JSON error envelope, status 403 as before. The visitor code is `no_grant`, `expired`, `revoked`, `whois_failed` or `registry_unavailable` on both paths; a legacy `--allow` list refusal uses `no_grant` or `whois_failed`. The page names the app only for a caller who can see it or request it (a tagged or shared-in node, or a revoked or guest person, cannot request, so a requestable app is not named to them on either path), and links nowhere: the dashboard link the gateway's refusal page used to carry, "Back to your apps" and "Request access", is gone from both paths, so the same refusal is the same bytes whether or not the gateway is running. Guest links, PIN and Funnel guest-gate pages are unchanged.
- A request that TSLink cuts while it closes a gateway route, or stops or reloads an app node, gets the same 503 page on both: `<app> is being reconfigured; try again`, visitor code `app_restarting`. The gateway's code was `route_closed` and the app node answered with plain text; the access log still records `route_closed` or `node_closed`.
- On an app's own node an oversized request from a caller who is refused is answered with the refusal, as through the gateway, instead of a `413` that named the app and its configured limit; an admitted caller still gets the `413`. Connection and header limits and the bounded disposal of an unread body are unchanged and still come first. The access log records such a request as the refusal, not as `limits`.
- The gateway now bounds the disposal of a refused request's unread body the way an app's own node does: a caller who holds a grant to one routed app can no longer hold a connection to another route open with an incomplete body. Responses are unchanged, as is the order that decides access first.
- The JSON error envelope TSLink generates for visitors (refusals, backend failures, closing) is labelled `"command": "app request"` on both paths instead of the gateway's `portal route`.
- The README MCP badge uses one centered HTML link/image block.

### Security

- Built with Go 1.27.3, which fixes GO-2026-6809 (CVE-2026-111943): `crypto/x509` `Certificate.Verify` now returns an error for certain intermediate certificates that are no longer trusted. `govulncheck ./...` reported the symbol as reachable from TSLink with Go 1.27.2 and reports no vulnerabilities with Go 1.27.3. The module minimum stays Go 1.27.1; only the `toolchain` directive moves, so builds from source pick up the fix automatically while Go's automatic toolchain selection is enabled. With `GOTOOLCHAIN=local`, install Go 1.27.3 or newer yourself: Go 1.27.1 and 1.27.2 still compile TSLink but keep the vulnerable standard-library code. Staticcheck is updated to 2026.2.2 so it reads Go 1.27.3 export data.

### Fixed

- `tslink invite list` no longer reports `invite_response_invalid` for a service whose device has no invitations. Tailscale answers that listing with JSON `null` instead of an empty array; it is now read as an empty list, so the service shows `checked: true` with `invite_count: 0` and the result is `complete: true` when nothing else fails. Blank, partial and malformed bodies, and a `null` devices listing, are still incomplete. The people flows that wait for proof that an invitation is gone (replacement, removal, `--reconcile-invite app=none`) accept the same `null` as that proof.
- When the completion receipt of an MCP tool call or of `requests approve` also fails, the tool's own failure keeps its code, next steps and data and stays first in the message; the missing receipt is added to it, not substituted for it. A successful call whose receipt cannot be written still reports `mcp_audit_unavailable`.

### Access traffic

- `access log` and MCP `access_summary` add complete filtered HTTP traffic totals by person and app, with separate non-human buckets and TCP counters. Gateway and direct app entries share the same keys; event limits do not reduce totals. Existing audit counts and event schema 1 remain unchanged. `access explain` reads reason counts for direct apps as well as routed apps. A request whose app cannot be reached is recorded `allowed` with status 502 (504 for a timeout) and an `error_class` through the gateway, as on a direct app node, instead of `denied` with `upstream_down`; events already written are not rewritten. Both entry paths now generate the same response for that failure, for a refusal and for a request TSLink cuts while closing a route or node, so a person's recorded `bytes_out` for an app is equal whichever way the request arrived. Four gateway-only exceptions remain by design, each for a security reason (see the [access log guide](docs/access-log.md)): a caller who holds no routed grant is dropped by the gateway before TLS (an unidentified `tcp_open` with reason `conn_gate_drop`) while the same caller on an app's own node gets an identified HTTP denial, so such a caller's person and app HTTP counts differ by path; the gateway refuses a cross-origin non-navigation request as `cross_origin_blocked` because its routes share one hostname and its cookies, where an app node has its own origin; a gateway route refuses with `route_changed` once the registry has moved on from the configuration it serves; and a gateway route refuses a `CONNECT` or absolute-form request target as `no_grant`, since a node that serves many apps must not let a request pick its destination. A remote app route that the gateway itself refuses (`remote_upstream_requires_tag`, `remote_upstream_outside_tailnet`, `remote_upstream_not_peer`, `remote_upstream_unavailable`) stays `denied` with that reason and status 503 whether the refusal comes before or while the upstream connection is made, and never advances `last_allowed_at`.

## [0.2.0] - 2026-10-14

Portal gateway mode: a person outside your tailnet accepts one device share, of the portal node, and you decide which apps they can use, on this host or on others, without sending more invitations. Gateway mode is opt-in; per-app sharing is unchanged and remains the default.

### Upgrade

- Homebrew: `brew upgrade --cask anydoor7/tap/tslink`. Scoop: `scoop update; scoop update tslink`. Other installs: download the new archive or package from this release.
- If TSLink runs as a background service, run `tslink install` again after upgrading so the service starts the new binary.
- Existing registries keep their schema version until gateway mode is configured; read Compatibility below before turning it on.

### Added

- `tslink portal enable --gateway`: a persistent portal node with one stable device identity, the private dashboard on port 443, and one HTTPS port per routed app (`tslink portal route add|remove|list`). Each connection is identified before TLS, each request is checked against people grants, and non-navigation requests from other origins, including other gateway ports, are refused unless a route opts out with `--allow-cross-origin`.
- Portal-only apps (`tslink add --portal-only`) with no node of their own, and apps on other hosts (`tslink add --remote https://<node>.<tailnet>.ts.net`). Remote apps are reached only through the tagged gateway node's own netstack, using the peer address from the gateway's netmap, never through the host network or the host resolver. The remote host authenticates the gateway's tag; visitor identity is not forwarded.
- One gateway device invite per person (`tslink people add|update --invite`); later grant changes need no new invite. An invite left behind by a gateway identity change is reported as stale and can be retired explicitly.
- Periodic health checks for portal-only apps; gateway fields in `status`, `list`, `url` and `doctor`; access-log reason codes and connection-drop counts; MCP tools `portal_route_add`, `portal_route_remove` and `portal_route_list`, and gateway options for `portal_enable` and `add`.
- `tslink health set <app>` and the owner MCP tool `health_set` change only an app's health check (path, status range, body, timeout, interval, or `--reset`) in one registry transaction, keeping access, tags, people scope, routes and the enrolled node. Use it instead of re-running `add`, which replaces the whole service.
- App health reports the observed HTTP `status_code`. `doctor` reports a backend that answers outside its health settings as `app_probe_unexpected_response` (warning) with the exact `tslink health set` command to fix the check.
- Access-log events for real backend failures add `error_class` (`connection_refused`, `connection_reset`, `tls`, `timeout` or `other`).

### Changed

- People-scoped apps now report `allow.mode: people` wherever an app's access summary is printed (`list`, `status`, `add`), including when gateway mode is off.
- HTTP health checks without an explicit status range accept 200-399. Redirects are still not followed; a redirect proves the app answers. 401 and 403 need an explicit range. A single explicit bound keeps its previous meaning, and registries are not rewritten.
- A health check that gets an unexpected status or body is `degraded`, never `down`, and sends no `app_down` alert. Only unreachable backends (refused, timed out, reset) become `down` after three consecutive failures.

### Fixed

- `tslink invite list` no longer fails when Tailscale returns an empty user-invite list as JSON `null`; device invitations stay visible. Device list completeness checks are unchanged.
- Apps that redirect their root page, require authentication, or have no root route were reported `down` and triggered false `app_down` alerts, and the portal showed them as Down to visitors.
- A client that cancels a request while TSLink identifies it, on an app node, a gateway route or the remote MCP control plane, is no longer recorded as an access denial or logged as an identification error, and the request is still never served. On app nodes and gateway routes, a client that cancels during proxying before the response starts is recorded as allowed with status 499 and `client_canceled`, instead of a 502 backend failure; once the response has started, the event keeps the status the client received. Requests that TSLink cancels itself, when it tears down a gateway route, when an app node's graceful shutdown runs out of time, or when a guest-gated app stops during a valid guest's request, are recorded as allowed with `route_closed` or `node_closed`, not as client cancellations, guest denials or backend failures: with status 503 before the response starts, and with the forwarded status once it has started. A guest grant that expires or is revoked during a request is still recorded as a denial.

### Compatibility

Gateway configuration uses registry schema 3, which older releases refuse. Before downgrading, follow the steps in `docs/portal.md`: stop the daemon, back up the registry and portal state, remove portal-only and remote apps and all routes, and turn gateway mode off with this version. Recorded gateway invites keep the registry at schema 3 even with gateway mode off; if any remain, restore a registry backup taken before gateway mode was enabled (people and invite changes made since then are lost). Replacing only the binary is not a downgrade.

## [0.1.2] - 2026-10-12

macOS background-service startup fix. Commands, flags and stored data are unchanged from 0.1.1.

### Upgrade

- Homebrew: `brew upgrade --cask anydoor7/tap/tslink`. Other installs: download the new archive or package from this release.
- If TSLink runs as a background service, run `tslink install` again after upgrading so the service starts the new binary.

### Fixed

- On macOS, including macOS 27, a successful LaunchAgent bootstrap can leave the job pending despite `RunAtLoad`. This caused `tslink install` to time out and could leave the previous daemon stopped after rollback. Installation and restoration now request `launchctl kickstart` for the bootstrapped job before checking for a stable running process. The request does not kill an already running process; if it reports an error, the running-state check still decides success and includes the kickstart error and output when verification fails.
- The kickstart request and running-state verification share the existing ten-second startup window, including rollback restoration. A slow start request gets at most two seconds and leaves time for the final state check.

## [0.1.1] - 2026-10-09

Security update. Commands, flags and stored data are unchanged from 0.1.0.

### Upgrade

- Homebrew: `brew upgrade --cask anydoor7/tap/tslink`. Other installs: download the new archive or package from this release.
- If TSLink runs as a background service, run `tslink install` again after upgrading so the service starts the new binary.

### Security

- Built with Go 1.27.2. The 0.1.0 Linux amd64 binary was built with Go 1.26.6, and `govulncheck -mode=binary` reports vulnerable standard-library symbols for 12 advisories: GO-2026-6599, GO-2026-6600, GO-2026-6603, GO-2026-6605, GO-2026-6607, GO-2026-6608, GO-2026-6609, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6613 and GO-2026-6617. They cover HTTP/2 server crashes, resource exhaustion and flow-control errors, HTTP/2 response smuggling via malformed headers, HTTP/1 connection desynchronization after CONNECT, Range header parsing without a size limit, MIME header memory-limit bypasses, `html/template` escaping and malformed `crypto/tls` ECH extension references. With Go 1.27.2, `govulncheck ./...` reports no reachable vulnerabilities in the 0.1.1 source on macOS arm64.
- `tailscale.com` is updated to v1.104.1 and `golang.org/x/net` to v0.60.0.

### Changed

- Building from source needs Go 1.27.1 or newer, and `tslink manifest --json` reports that minimum.

### Documentation

- The README is shorter, adds a "When you need TSLink" comparison with doing the same by hand, and is available in 21 translations.
- Guest links are described as public links that can be forwarded: whoever has the link reaches the guest gate, and a PIN, when set, is still required. The 0.1.0 notes said "Anyone holding the link can use it", which left out the PIN.

## [0.1.0] - 2026-10-06

First public release. TSLink gives each app on your computer or server, whether a web app, a folder or file, or a TCP port, its own private address on your Tailscale network, so you can open it from your own devices. You decide who else can reach it: people you name, browser guests with an expiring link, or, only when you ask for it, the public internet. Run it yourself from the CLI or through an AI agent over MCP. TSLink needs a Tailscale account and is an independent project, not made or endorsed by Tailscale.

### Install

- Prebuilt archives for macOS, Linux and Windows (amd64 and arm64), `.deb` and `.rpm` packages for Linux, and a Homebrew cask for macOS and Linux: `brew install --cask anydoor7/tap/tslink`.
- macOS binaries are signed with a Developer ID certificate and notarized by Apple. Windows archives are not code-signed.
- `checksums.txt` and the SBOMs carry Sigstore signatures, and every release asset has a GitHub build provenance attestation. See [Verify a release](https://github.com/anydoor7/tslink/blob/v0.1.0/docs/verify-release.md). Building from source needs Go 1.26.6 or newer.

### Share your apps

- `tslink share 3000`, `tslink share ./photos` or `tslink share ./report.html` gives a web app, folder or single file its own HTTPS address in your tailnet. `tslink add <name> --tcp <port>` does the same for a TCP service. Each app runs as its own embedded Tailscale node inside one background daemon.
- `tslink install` starts the daemon at login: a LaunchAgent on macOS, a systemd user service on Linux and a per-user scheduled task on Windows (`--startup` uses the Startup folder instead).
- Recipes for 15 self-hosted apps, including Home Assistant, Jellyfin, Immich, Nextcloud, Open WebUI and Ollama, plus a generic web recipe. `tslink apps detect` finds apps listening on this machine, and `tslink apps share <recipe>` previews a registration that `--yes` applies.
- Per-app request limits (`--max-request-body` and timeouts) for large or slow uploads. The default body limit is 32 MiB.

### People, guest links and public access

- People: `tslink people add alice@example.com --apps photos,finance --for 7d` lets that Tailscale login open the chosen web and file apps until the deadline. `people update`, `extend` and `people remove` change or revoke access. `--invite --print-links` adds device invitations for someone outside your tailnet (this needs a user-owned API token), and `--qr` prints a QR code for phone setup.
- Guest links: `tslink guest create photos --for 3d --public --print-link` creates an expiring browser link, optionally with a PIN, for a web app. Guests need no Tailscale account. The link goes through Tailscale Funnel behind a mandatory guest gate. Anyone holding the link can use it; TSLink stores only its hash.
- Public access: an open Funnel needs `--funnel --public`, works for web apps only and always expires, after 24 hours by default and at most 7 days unless you raise `durations.public_max`.
- Durations: every share and access lifetime uses one grammar, such as `90m`, `36h`, `3d`, `1w`, `1d12h` or `until 2030-06-01`. The minimum is one hour. Only tailnet members can be given `never`, and only with `--ack-never`.

### Portal and access requests

- `tslink portal enable --owner you@example.com` starts a private home page on its own tailnet node. Each visitor sees the apps they may open, with their health and deadlines.
- People in your tailnet can ask for an app, or for more time, from the portal. Only apps registered with `--requestable` are offered. `tslink requests approve <id> --for 3d` or `tslink requests deny <id>` decides, and a new request can trigger an alert.

### Health, alerts and access history

- The daemon checks each app's backend (HTTP status and an optional body match, or a TCP connection) and reports `healthy`, `degraded`, `down` or `unknown` in `status`, `list --verbose`, `doctor` and the portal. `status` and `doctor` warn 14 days and 3 days before a node key expires.
- Optional alerts for an app going down or recovering, an approaching expiry and new access requests run a command or call a webhook configured in `alerts.json`.
- `tslink access log` shows who opened which app and when, including denied requests, with summaries per person and per app. By default only the first path segment is recorded; `tslink access path` changes that per app.

### Agents and automation

- Every command in the published manifest except `tslink mcp` accepts `--json` and returns a versioned `tslink.result` envelope; Cobra's `help` and `completion` are not in the manifest and print plain text. `tslink manifest --json` lists those commands with their flags, exit codes and error codes.
- `tslink mcp` is an MCP server over stdio. `tslink serve --mcp` adds a remote MCP endpoint on its own tailnet-only node; it is never published through Funnel.
- Scoped roles `viewer`, `app-operator` and `people-manager` limit an agent to listed apps, a fixed set of tools and a maximum grant duration, while `owner` keeps full control. Use `tslink mcp --scope` locally, or `mcp.bindings` for remote callers, who are identified by Tailscale login or tag. `tslink mcp-audit` reads the journal of changes made through MCP.

### Security defaults

- Apps stay private to your tailnet unless you create a guest link or publish through Funnel. TCP services are never public.
- Registration refuses link-local and cloud metadata targets (`link_local_target_refused`) and file shares that would expose TSLink's config directory (`path_exposes_config_dir`).
- The proxy removes client-supplied `Tailscale-*` and `X-Tailscale-*` identity headers before adding the caller's identity from Tailscale.
- `tslink remove` and cleanup delete a tailnet device only when TSLink recorded that exact device as its own.
- On macOS and Linux, daemon logs are owner-only (mode 0600). Guest PINs are checked against salted PBKDF2 hashes, with attempt limits per link and per source address.
- Over MCP, invitations with a role above member, or that allow exit-node use, need the owner's `mcp.allow_elevated_invites` opt-in.

### Not in this release

- Multi-host inventory is planned. Today each host runs its own TSLink, and the portal lists that host's apps.
- There is no admin dashboard or REST API, Docker image, custom domain support, middleware or Prometheus endpoint. See the [roadmap](https://github.com/anydoor7/tslink/blob/v0.1.0/docs/roadmap.md).

### Compatibility (0.x)

For later 0.x releases, these are the public automation and data contracts:

- `--json` keeps the `tslink.result` envelope and its `type`, `ok`, `schema_version`, `command`, `code`, `data`, and `error` fields. Optional `data`, `error`, and `error.next` stay optional; fields may be added.
- Documented command `data` fields keep their meaning; fields and warnings may be added. Public view `data.schema_version` stays an integer.
- Published error codes keep their meanings and exit-code mapping. Codes may be added; exit classes 0, 1, 2, 3, 4, 5, 64, and 65 stay as documented. `tslink manifest --json` derives `error_codes` from this mapping.
- `registry.json` schemas 1 and 2 and known `config.json` keys remain readable; fields may be added. Run `registry check` before an upgrade.
- MCP tool names and output schemas remain available with additive fields; tools and optional fields may be added. Refusals remain tool results with `isError: true`, one JSON text failure object (`code`, `message`, `next`, optional `data`), and no `structuredContent`; schema argument errors use `usage_error`. The four behavior annotations remain present on every tool.
- Human output, error messages, and logs are not parsing interfaces. Use `--json` instead. `runtime.json` and other daemon/CLI state files are private.
- Go packages are not a library API. A future incompatible 0.x change needs a migration note; private pre-release behavior is not a baseline.
