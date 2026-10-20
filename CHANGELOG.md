# Changelog

All notable changes to TSLink are documented here. The public compatibility
surface for the 0.x series is defined below; TSLink is a CLI, not a Go library.

## [Unreleased]

### Fixed

- `tslink invite list` no longer reports `invite_response_invalid` for a service whose device has no invitations. Tailscale answers that listing with JSON `null` instead of an empty array; it is now read as an empty list, so the service shows `checked: true` with `invite_count: 0` and the result is `complete: true` when nothing else fails. Blank, partial and malformed bodies, and a `null` devices listing, are still incomplete. The people flows that wait for proof that an invitation is gone (replacement, removal, `--reconcile-invite app=none`) accept the same `null` as that proof.

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
