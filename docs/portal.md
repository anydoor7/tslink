# One home address for your apps

TSLink works with Tailscale, independent project. New installations enable a persistent home gateway that gives each visitor one address to bookmark. Existing installations keep their current mode until explicitly changed. It lists private HTTP/file apps according to the access enforced for their current Tailscale identity, with the app address, recent health and any access deadline.

```sh
tslink portal enable --owner you@example.com
# Optional hostname and additional administrative identities:
tslink portal enable --hostname family --owner you@example.com --admins helper@example.com
tslink status --urls
# Stop only the portal listener:
tslink portal disable
```

The default hostname is `home`. Use the **portal URL reported by status**, for example `https://home.example.ts.net`; the hostname may be renamed by Tailscale after a collision. Enable/disable save configuration only. A running daemon applies it automatically. If the daemon is stopped, run `tslink serve`. A fresh portal node can require its own interactive enrollment; portal enrollment runs independently of app nodes. Daemon readiness preserves a pending portal login offer. Completion or cancellation removes only the offer matching its daemon PID, service and auth URL, preserving another node's pending offer. An exact running portal snapshot counts as authorization evidence, including portal-only installations, without increasing the app service count. `pending` or `starting` does not prove that its URL is ready. Doctor and both status variants report `portal.enabled`, `hostname`, `state`, `url` and any stable startup error. URLs are omitted when runtime evidence is stopped, stale or does not match the current registry.

Pending offers are stored together in one atomically replaced `auth-handoff.json` document (schema version 2), keyed by node name; publishing or retiring one node leaves the others intact. Previous single-record files remain readable and migrate on the next publication. `status --json` and `status --urls --json` include `pending_logins`, with `node`, `auth_url` and `expires_at` for every unexpired pending offer for the current daemon. Human status shows every node and URL. Compatibility `auth_url`, `auth_status` and `expires_at` select the oldest still-pending publication; replacing a node's offer puts it last. Completed nodes with current running evidence are excluded. Stale daemon PID entries are excluded and replaced on a new daemon publication. Readiness never retires pending offers.

In directory-only mode, the portal uses a separate node and `portal-nodes/<hostname>` state directory. It never edits app registrations, targets, tags, health settings or request limits, and does not restart existing app nodes. An unrelated app startup or auth-key error cannot block portal disable/replacement. The old portal worker is cancelled, joined and closed before replacement. Changing the portal hostname preserves previous enrollment state. On the credentialed path the node uses the configured default tag and is ephemeral; on the zero-credential path it is persistent so enrollment survives a daemon restart. Disable retains local enrollment state and owner/admin identities; it does not delete a remote device.

## Who sees an app?

The portal and private HTTP/file app listeners use the same `AppAccessDecisionAt` model (`AppAccessAt` exposes its listener decision). The result separates actual enforcement, directory visibility and grant expiry. Grants use the people registry's `PeopleAccessAt` and `PersonGrantActiveAt`: a deadline is exclusive, an expiry latch remains denied, and a tombstoned person sees no apps even if a legacy allow rule remains. Unknown people use the existing service allow list; an unscoped service with an empty allow list is available to tailnet peers. A tagged machine follows explicit tag allow rules and cannot inherit the human profile's grants or administrator role.

`--owner` is the exact Tailscale login of the person maintaining these apps. A new gateway may temporarily have no owner or admins. After credential-free enrollment, only that gateway's verified untagged self account can become owner. Tagged/OAuth enrollment leaves ownership unset and reports "Choose the account that will manage your apps"; use the local command to choose it. Remote callers cannot claim an unset owner. Omitting `--owner` or `--admins` on re-enable preserves the existing identities. `--admins` explicitly designates additional **TSLink app administrators**. These identities can open all private HTTP/file apps, and the portal shows them every registered app. This is an authorization choice; choose these identities carefully. Roles are retained on disable, while a people revocation tombstone takes precedence. They do not override Tailscale network policy or the app's own login. Raw TCP does not enforce `allow` or WhoIs; the registry refuses `allowed_users` for TCP. Ungated public Funnel bypasses the private identity gate. TCP and ungated public services cannot be restricted per person by TSLink, so their cards appear **only for the owner/admins**, as an inventory. Their cards say: "Anyone who can reach this device can connect; TSLink can't limit it per person." Omitting them from other visitors' directories does not decide or deny those visitors' network/public access. Private HTTP and file cards exactly follow the shared enforcement decision, including legacy allow rules, grants, deadlines, tags and tombstones.

Guest-gated public apps also appear only in owner/admin inventory. Their private listener still enforces people grants and revocation; public requests require a guest link.

Runtime and health observations are collected only after directory filtering. Registry parsing still depends on registry size; the portal makes no constant-time or remote timing-side-channel guarantee.

No hidden app names, totals, backend addresses, invite bearer links or other visitors' deadlines are emitted. HTML and `GET /api/apps` use the same filtered list. Invalid service entries are withheld; an unreadable or malformed registry fails closed with a generic unavailable message. A visible app whose canonical address is not yet known shows “Address not ready”. Health uses the health checker's `healthy`, `degraded`, `down` or `unknown` with the same freshness rule as status. Health describes the backend's latest probe, not proof that a particular visitor's network path works.

The portal reads grants on every request. Access-request reads can persist request expiry/retention; the protected POST saves new requests. It enforces deadlines immediately; app listeners and the daemon's existing lifecycle reconciler persist expiry latches. If the clock moves backwards before a latch is written by those existing paths, the portal follows the people registry's current deadline decision. No browser authorization cache is used: WhoIs is queried afresh with a five-second bound.

HTTPS apps have an Open link. TCP services show the same `host:port` connection address as `tslink url`, with a prompt to use the appropriate app; browsers cannot open a raw TCP service.

Funnel deadlines use the effective service type: after expiry and private-listener reconciliation, the same private identity decision applies, even if the stored registry still contains the old Funnel flag. Handoff reads are limited to regular files of at most 64 KiB; Unix rejects symlinks and opens nonblocking so a FIFO cannot hold up cancellation.

## Send one address to a visitor

When enabled, `tslink people add/update` includes the portal in its plain-language guide. Once its runtime URL is exact, the guide says to bookmark that one address. Before enrollment completes it tells the owner to use `tslink status --urls` to find it.

The guide reads exact runtime evidence after grants and routes are saved. Stale registry fingerprints or mismatched daemon identities cannot supply an address or invitation target.

Visitors need Tailscale installed, connected and signed in with the granted login. In directory-only mode, for someone outside your tailnet, **share the home node as well as the app nodes** through Tailscale's Machines page. Per-app invitation links do not make the portal node reachable, and the directory-only portal does not send invitations or alter ACLs. Sharing only the portal does not grant network access to its app links. [Tailscale device sharing](https://tailscale.com/docs/features/sharing) grants access only to the shared machine and remains subject to network policy. Tagged machines on another tailnet cannot use user device shares.

## Agent access and safety

```sh
curl https://home.example.ts.net/api/apps
```

The response uses TSLink's normal versioned envelope:

```json
{"type":"tslink.result","ok":true,"schema_version":1,"command":"portal apps","code":0,"data":{"apps":[{"name":"photos","url":"https://photos.example.ts.net","health":"healthy","expires_at":"2030-01-02T12:00:00Z","expiry":"Access ends January 2, 2030 at 12:00 UTC."}]}}
```

MCP tools `portal_enable` (optional `owner`, `hostname`, `admins`, `funnel`, `gateway`, `gateway_ports`, `gateway_tag`) and `portal_disable` use the same command implementation. Over HTTP MCP, only the current portal owner can enable or replace portal settings, including owner and admin identities. The check uses the same exact, untagged, non-revoked WhoIs owner identity as the request tools and is repeated under the registry write lock. Other callers receive `access_request_owner_required`, including attempts to set, clear or replace the owner or change admins. An unset owner cannot be claimed remotely. Use local `tslink portal enable --owner <login>` or stdio MCP to set the first owner or recover a lost owner identity. With gateway mode on, HTTP MCP `portal_disable` also requires the current portal owner, checked in the registry write transaction; other callers receive `access_request_owner_required`. Disable retains owner/admin identities. `funnel: true` and `tslink portal enable --funnel` fail explicitly with `portal_funnel_refused`. Persisted `portal.funnel: true` also refuses admission. The only production listener is tsnet `ListenTLS`; there is no public Funnel or host-interface listener.

HTTP MCP checks request listing and approval/denial against the current, untagged, non-revoked portal owner inside the registry write transaction. Any person add, update, removal, restoration or grant change targeting the current owner or a portal admin requires that same owner, checked under the lock before mutation; a revoked owner cannot restore itself remotely. Refusals use `access_request_owner_required`. Local CLI and stdio MCP remain trusted recovery paths, and ordinary-person restoration is unchanged.

Pages are server-rendered with embedded CSS, responsive layout and automatic light/dark mode. There are no scripts, frameworks, CDNs or external assets. HTML is escaped. The CSP allows only the exact embedded stylesheet hash and forbids scripts, frames and external assets; forms can submit only to the same origin. Every response uses `Cache-Control: private, no-store`, `nosniff` and `no-referrer`. Host must match the trusted canonical node authority, including HTTP absolute-form requests. An Origin, when present, must be a single matching HTTPS origin; opaque, HTTP and foreign origins are refused.

GET and HEAD serve the directory. `POST /access-requests` accepts an 8 KiB form from a WhoIs-identified human tailnet member, with a matching HTTPS Origin and an identity/host-bound two-hour CSRF token. Only owner-marked `requestable` private HTTP/file apps appear in the form; public Funnel and guest-gated apps are excluded; hidden apps and old histories for apps made non-requestable remain hidden. The visitor sees their own request status, and can choose 1 hour, 8 hours, 1 day, 3 days or 7 days using English/Chinese labels, or let the owner choose. The owner approves with a duration through CLI/MCP. If expiry maintenance cannot acquire the registry lock, the page keeps accessible apps available and explains that requests are temporarily unavailable; reload to retry. See [requests](requests.md). The independent HTTP server uses the default HTTP limits: a 32 MiB request cap, 10-second header deadline, 30-second body-read inactivity bound and 60-second keep-alive idle timeout, with the same listener and request-budget wrappers as app nodes. The portal's registry reader rejects symlinks/special files and caps reads at 4 MiB. App logins, browser guest links and multi-host discovery are outside this package.

The people guide supports `--qr` and `--qr-png <file>` for this exact portal URL. Its four-step English/Chinese phone guide and bearer-link discipline are documented in [people](people.md).

## Gateway mode: one device share for routed apps

Gateway mode is the default for genuinely new installations. Existing registries and residual node/runtime/supervisor state are never silently migrated. The portal becomes one persistent tsnet node serving the
home page on port 443 and each routed app on its own HTTPS port. A visitor outside
your tailnet accepts one share of this gateway node. Local people grants then
determine which routed apps they can open, including registered remote apps.
Tailscale network policy still applies. Direct per-app URLs keep their own node
shares and access checks.

```sh
tslink portal enable --owner you@example.com --gateway
# Route an existing private HTTP or file app; its own node keeps running:
tslink portal route add photos
# New apps can have only a gateway route, with no app node:
tslink add notes --proxy localhost:3000 --portal-only
tslink add files --dir /srv/shared --portal-only --portal-port 8445
tslink portal route list --json
tslink people add alice@example.com --apps photos,notes,files --for 7d --print-links --qr
```

Use the exact URLs from status, for example `https://home.example.ts.net:8443/`.
Routes retain the app's root path; each port is a separate browser origin. The
allocator defaults to 8443-8499, reserves 443/80 and browser-blocked ports, avoids
occupied route ports, and persists its choice. Re-adding a route without a port
keeps its existing port. To select a range, use
`portal enable --owner you@example.com --gateway --gateway-ports 9000-9099`;
existing route ports must still fit. `portal route add photos --port 9001`
selects an explicit port. `portal route remove photos` removes only that route.
A portal-only app must retain a route; remove that app with `tslink remove`
instead. TCP, Funnel and browser guest-link apps cannot be gateway routes.

New private HTTP/file apps receive stable routes automatically when the gateway is enabled; their own app nodes and URLs remain. Authorization also routes supported existing apps as needed. Running `portal enable` without gateway options preserves an existing mode and defaults a new portal to gateway mode.
`--gateway` alone retains its existing range and tag. Range or tag options require
`--gateway`. After removing every route, `portal enable --owner you@example.com
--gateway=false` explicitly turns gateway mode off. `portal disable` closes the
home listener and all routes while retaining configuration and enrollment state. It stays off across subsequent install/add/serve actions. Ordinary apps use their own nodes; the next authorization prepares any per-app invitations. The disable result lists portal-only/remote apps whose sole entry becomes unavailable. `portal enable` restores the retained configuration.

### Remote apps and trust

```sh
# Credentialed enrollment; the tag must be permitted by your Tailscale policy:
tslink portal enable --owner you@example.com --gateway --gateway-tag tag:gateway
# The remote machine's MagicDNS name must be in this gateway's tailnet:
tslink add media --remote https://mini.example.ts.net:9443 --portal-port 8446
```

`--remote` creates a portal-only HTTPS proxy. IP literals, HTTP, credentials in
URLs, fragments and hosts outside the gateway's observed MagicDNS tailnet suffix
are refused. Registration requires a `<node>.<tailnet>.ts.net` name; custom
control-server domains outside `.ts.net` are not supported by `--remote`. Port
443 is the default. The hostname must match a peer DNSName in this gateway
node's LocalAPI status (case-insensitive, ignoring a trailing dot), checked at
route activation, for each new upstream connection and on health probes. The
tag and tailnet suffix are rechecked on every request, including requests that
reuse an upstream connection. Traffic and
health checks dial that peer's Tailscale IP directly through the gateway netstack,
only while `UseNetstackForIP` confirms it is a peer. They never use the host
resolver or host network; a missing or disappearing peer fails closed with
`remote_upstream_not_peer` (visitors get 503 with that code, and the access log
records the request as `denied` with that reason, as for every
`remote_upstream_*` refusal). A netstack dial error, or a failure on an already
open upstream connection, is a backend failure instead: visitors get 502, or 504
when the backend timed out, with the code `upstream_down`, and the access log
records an allowed request with an `error_class`. TLS still verifies the
registered hostname normally. The remote app must permit the gateway tag in its own policy
(for a TSLink app, its explicit tag allow list). This command does not change
remote ACLs or registrations.

Remote routes start only when LocalAPI reports that the gateway is tagged.
An untagged gateway would authenticate to the remote node as its enrolling human,
which could accidentally inherit that person's owner privileges. Configuring a
tag is not evidence that enrollment applied it. `remote_upstream_requires_tag`
refuses an untagged gateway; `remote_upstream_outside_tailnet` reports a foreign
host. Tag/status/suffix checks also run before each forwarded request and dial,
so an existing upstream connection cannot bypass later tag removal. A zero-
credential gateway supports local apps, but `--gateway-tag` requires credentials.

Local upstreams receive verified visitor Tailscale identity headers. Remote
upstreams receive no visitor identity headers or forwarded visitor IP: the
remote side authenticates the gateway node itself with WhoIs. Signed visitor
identity assertions remain planned. Remote apps that need to distinguish humans
must use their own login. Dashboard groups use “this host” or the remote node label (for example “mini”);
they do not disclose backend URLs.

### Access at each port

Before TLS, each connection needs successful WhoIs within five seconds. A caller
allowed for any routed app is a known visitor: they can complete TLS on another
route, but its HTTP gate denies an app for which they lack a grant. Strangers and
callers granted only a non-routed app receive no TLS bytes on route ports. The
home page keeps its directory audience, including visible non-routed apps and
eligible tailnet members requesting access. Shared-in outsiders do not gain the
member-only access-request form.

Every HTTP request and WebSocket upgrade repeats WhoIs, registry validation and
the people decision. Revocation affects subsequent requests; accepted streams
may finish. Registry failures fail closed. Denials name an app only when it is
visible to that caller or requestable. One broken app entry does not block other
routes or the home page.

Tailscale identity is ambient in the browser, and different tailnet pages can be
same-site. Routes therefore require **same-origin** browser fetches, forms and
WebSocket upgrades by default. Ordinary top-level GET/HEAD navigation is allowed;
non-browser requests without Origin/fetch metadata still pass identity checks.
An origin includes the HTTPS scheme, hostname and port. To deliberately allow
cross-origin browser calls for an app, use
`portal route add photos --allow-cross-origin`; this weakens that route's browser
request boundary. Do not enable it merely because two apps are in the same tailnet.

Ports do not isolate cookies. Apps on this hostname can collide on cookie names
or set cookies affecting another port. App cookie namespacing is not implemented.
TSLink reserves and strips `__tslink_gateway_` cookies. Proxy destinations come
only from the registry; Host, absolute-form requests and CONNECT cannot select
an upstream. Redirect and Refresh URLs are rewritten only when they refer to the
registered upstream origin. Request limits follow the app; there is no fixed
response-header deadline preventing long polls.

### Health, persistence and upgrades

Portal-only proxy and file apps are probed periodically without visitor traffic.
Remote probes use the same tagged tsnet transport as requests. The existing health
configuration, its classification and the three-failure down threshold apply: a
route whose backend answers outside its health settings is `degraded`, never
`down`, and has no “Offline since”. Observations older than
twice the interval plus timeout become `unknown`; `health_checked` records their
time. The directory displays “Offline since” when known. A backend observation
is not proof of a guest's network reachability. `doctor --probe-external` never
probes remote gateway targets from the host network.

Gateway enrollment is persistent on both credentialed and interactive paths.
Switching a credentialed ephemeral portal to gateway mode replaces its enrollment
state unless a matching persistent-enrollment marker proves it can be reused.
The separate `portal-nodes/<hostname>/ownership.json` ledger records observed
NodeIDs. A changed NodeID produces `gateway_node_identity_changed`: existing
accepted device shares still refer to the old node and require owner recovery.
Replaced NodeIDs are retired in the ownership ledger. `people update --invite`
reports `complete: false` with `gateway_invite_stale` for a live invitation
attached to another NodeID. After status proves the new persistent gateway,
use `tslink people update <login> --invite --replace-invite @gateway=<old-id>`.
This retires the old local attempt as `target_gone` (including accepted shares)
and allows a new invitation; it does not revoke the old remote share. For a
stale pending/sending/unknown attempt without a recorded ID, explicitly retire
it with `--reconcile-invite @gateway=none`. Use plain `--invite` for later retries.
See [gateway invitation recovery](people.md) for the full procedure.
The `gateway_node_identity_changed` warning persists after completed recovery:
retired NodeIDs remain in the ownership ledger as history. It does not mean
current invitations still target an old device. Check `tslink people list --json`
for live gateway invitations whose `node_id` differs from `portal.gateway.node_id`
in `tslink status --json`; `target_gone` history needs no further replacement.
Never copy an enrollment marker between nodes or assume a configured flag proves
persistence. Route add/remove, grants, owner/admin edits and port-range changes
keep the shared node running. Gateway enable/disable, hostname/tag changes, and
changes of credential mode or control URL on daemon restart can replace it.
A route activation failure is retried with exponential backoff from one to
32 seconds, on sync/retry ticks; another route remains available.

A request that TSLink itself answers, rather than the app, gets the same
response on a gateway route and on the app's own node, so the visitor sees the
same thing and the access log's `bytes_out` for a person and app does not depend
on how the request arrived, apart from those gateway-only refusals. A browser navigation (`Sec-Fetch-Mode: navigate`, or
an `Accept` that prefers `text/html`) gets a small HTML page; any other client
gets the JSON error envelope, with `"command": "app request"` (the envelope no
longer carries the gateway's old `portal route` label). A refusal is 403 with a
visitor code that is the same on both paths: `no_grant`, `expired`, `revoked`,
`whois_failed` (the caller could not be identified) or `registry_unavailable`
(the access policy could not be read). The page names the app only for a caller
who can see it or ask for it: a tagged or shared-in node, or a revoked or guest
person, cannot ask, so a requestable app is not named to them. The page carries
no link to the dashboard, because whether a dashboard exists depends on whether
the gateway runs and a refusal reads the same either way. Access is also
decided before the request-size limit, on both paths: a refused caller who sends
an oversized request gets the same 403 as any other refused request, and only a
caller who may use the app is told the limit (413, which names the app and the
configured limit). The connection and header limits and the bounded disposal of
a body nobody read apply to every request first. The access log keeps each path's own reason for the same refusal
(`people`, `expired` or `acl` on an app node; `no_grant`, `expired` or
`revoked` on the gateway). Some refusals exist on one path only:
`cross_origin_blocked`, `route_changed` and the gateway's malformed-request
`no_grant` are gateway-only, and the legacy `--allow` list (`acl`) is app-node
only, since the gateway routes people-scoped apps; a legacy refusal uses the
same page and names no app. The gateway-only refusals are deliberate and are the
only places the two paths decide differently for a caller the gateway admits:
`cross_origin_blocked` (routes share one hostname and its cookies, while an app
node has its own origin) and `route_changed` (a gateway route serves the
configuration it started with and refuses once the registry has moved on). A
caller with no routed grant is dropped before TLS and never sees a page at all.
The [access log guide](access-log.md) gives the reason for each and what it does
to the traffic counters. A request cut because TSLink is closing a route or
stopping or reloading a node is 503 with visitor code `app_restarting` and the
same text on both paths (`<app> is being reconfigured; try again`); only the
access-log reason differs (`route_closed`, `node_closed`). Guest links, PIN and
Funnel guest-gate pages are unchanged.

Status and doctor expose `portal.gateway` with observed persistence, tag and node
identity, ownership proof, range, route states and a per-run `dropped` connection
count. Connection-drop logs are rate-limited per gate; the home gate uses the
fixed app marker `@portal`. Request events retain app names and add `via: portal`
and a stable reason. A visitor who cancels during identification leaves no event;
one who cancels during proxying, before the response starts, is `allowed` with
status 499 and `client_canceled`, and does not mark the route down. A visitor
who leaves after the response started keeps the forwarded status. Requests in
flight when TSLink tears a route down are `allowed` with `route_closed`, not a
visitor cancellation, and do not mark the route down: status 503 before the
response starts, the forwarded status once it has started. A response that
completed before the teardown keeps its reason. App nodes, including guest-gated
apps, record requests they cut on stop or reload the same way with `node_closed`
(see [access log](access-log.md)). The drop counter includes events omitted by the rate limit.

Gateway registries use schema 3, which older readers refuse. Before downgrading,
stop the new daemon, back up the registry and portal state, and intentionally
remove/replace portal-only apps and remote apps, remove all routes, then turn off
gateway mode using this version. Recorded gateway invites keep the registry at
schema 3 even with gateway mode off; if any remain, restore a registry backup
taken before gateway mode was enabled (people and invite changes made since
then are lost). Confirm all guest network shares and authorization expectations
before starting an older daemon. Replacing only the binary is not a downgrade.
