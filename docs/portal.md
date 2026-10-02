# One home address for your apps

TSLink works with Tailscale, independent project. The optional home portal gives each visitor one address to bookmark. It lists private HTTP/file apps according to the access enforced for their current Tailscale identity, with the app address, recent health and any access deadline.

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

The portal uses a separate node and `portal-nodes/<hostname>` state directory. It never edits app registrations, targets, tags, health settings or request limits, and does not restart existing app nodes. An unrelated app startup or auth-key error cannot block portal disable/replacement. The old portal worker is cancelled, joined and closed before replacement. Changing the portal hostname preserves previous enrollment state. On the credentialed path the node uses the configured default tag and is ephemeral; on the zero-credential path it is persistent so enrollment survives a daemon restart. Disable retains local enrollment state and owner/admin identities; it does not delete a remote device.

## Who sees an app?

The portal and private HTTP/file app listeners use the same `AppAccessDecisionAt` model (`AppAccessAt` exposes its listener decision). The result separates actual enforcement, directory visibility and grant expiry. Grants use F1's `PeopleAccessAt` and `PersonGrantActiveAt`: a deadline is exclusive, an expiry latch remains denied, and a tombstoned person sees no apps even if a legacy allow rule remains. Unknown people use the existing service allow list; an unscoped service with an empty allow list is available to tailnet peers. A tagged machine follows explicit tag allow rules and cannot inherit the human profile's grants or administrator role.

`--owner` is the exact Tailscale login of the person maintaining these apps. It is required because WhoIs does not tell the portal which visitor has an owner/admin role in the Tailscale admin console. `--admins` explicitly designates additional **TSLink app administrators**. These identities can open all private HTTP/file apps, and the portal shows them every registered app. This is an authorization choice; choose these identities carefully. Roles are retained on disable, while a people revocation tombstone takes precedence. They do not override Tailscale network policy or the app's own login. Raw TCP does not enforce `allow` or WhoIs; the registry refuses `allowed_users` for TCP. Public Funnel bypasses the private identity gate. These services cannot be restricted per person by TSLink, so their cards appear **only for the owner/admins**, as an inventory. Their cards say: "Anyone who can reach this device can connect; TSLink can't limit it per person." Omitting them from other visitors' directories does not decide or deny those visitors' network/public access. Private HTTP and file cards exactly follow the shared enforcement decision, including legacy allow rules, grants, deadlines, tags and tombstones.

Runtime and health observations are collected only after directory filtering. Registry parsing still depends on registry size; the portal makes no constant-time or remote timing-side-channel guarantee.

No hidden app names, totals, backend addresses, invite bearer links or other visitors' deadlines are emitted. HTML and `GET /api/apps` use the same filtered list. Invalid service entries are withheld; an unreadable or malformed registry fails closed with a generic unavailable message. A visible app whose canonical address is not yet known shows “Address not ready”. Health uses F2's `healthy`, `degraded`, `down` or `unknown` with the same freshness rule as status. Health describes the backend's latest probe, not proof that a particular visitor's network path works.

The portal reads grants on every request. Access-request reads can persist request expiry/retention; the protected POST saves new requests. It enforces deadlines immediately; app listeners and the daemon's existing lifecycle reconciler persist expiry latches. If the clock moves backwards before a latch is written by those existing paths, the portal follows F1's current deadline decision. No browser authorization cache is used: WhoIs is queried afresh with a five-second bound.

HTTPS apps have an Open link. TCP services show the same `host:port` connection address as `tslink url`, with a prompt to use the appropriate app; browsers cannot open a raw TCP service.

Funnel deadlines use the effective service type: after expiry and private-listener reconciliation, the same private identity decision applies, even if the stored registry still contains the old Funnel flag. Handoff reads are limited to regular files of at most 64 KiB; Unix rejects symlinks and opens nonblocking so a FIFO cannot hold up cancellation.

## Send one address to a visitor

When enabled, `tslink people add/update` includes the portal in its plain-language guide. Once its runtime URL is exact, the guide says to bookmark that one address. Before enrollment completes it tells the owner to use `tslink status --urls` to find it.

The guide keeps the address verified before its own grant update, so the first grant includes an already-running home portal link.

Visitors need Tailscale installed, connected and signed in with the granted login. For someone outside your tailnet, **share the home node as well as the app nodes** through Tailscale's Machines page. Per-app invitation links do not make the portal node reachable, and the portal does not send invitations or alter ACLs. Sharing only the portal does not grant network access to its app links. [Tailscale device sharing](https://tailscale.com/docs/features/sharing) grants access only to the shared machine and remains subject to network policy. Tagged machines on another tailnet cannot use user device shares.

## Agent access and safety

```sh
curl https://home.example.ts.net/api/apps
```

The response uses TSLink's normal versioned envelope:

```json
{"type":"tslink.result","ok":true,"schema_version":1,"command":"portal apps","code":0,"data":{"apps":[{"name":"photos","url":"https://photos.example.ts.net","health":"healthy","expires_at":"2030-01-02T12:00:00Z","expiry":"Access ends January 2, 2030 at 12:00 UTC."}]}}
```

MCP tools `portal_enable` (required `owner`; optional `hostname`, `admins`, `funnel`) and `portal_disable` use the same command implementation. Over HTTP MCP, only the current portal owner can enable or replace portal settings, including owner and admin identities. The check uses the same exact, untagged, non-revoked WhoIs owner identity as the request tools and is repeated under the registry write lock. Other callers receive `access_request_owner_required`, including attempts to set, clear or replace the owner or change admins. An unset owner cannot be claimed remotely. Use local `tslink portal enable --owner <login>` or stdio MCP to set the first owner or recover a lost owner identity. Disable retains owner/admin identities. `funnel: true` and `tslink portal enable --funnel` fail explicitly with `portal_funnel_refused`. Persisted `portal.funnel: true` also refuses admission. The only production listener is tsnet `ListenTLS`; there is no public Funnel or host-interface listener.

Pages are server-rendered with embedded CSS, responsive layout and automatic light/dark mode. There are no scripts, frameworks, CDNs or external assets. HTML is escaped. The CSP allows only the exact embedded stylesheet hash and forbids scripts, frames and external assets; forms can submit only to the same origin. Every response uses `Cache-Control: private, no-store`, `nosniff` and `no-referrer`. Host must match the trusted canonical node authority, including HTTP absolute-form requests. An Origin, when present, must be a single matching HTTPS origin; opaque, HTTP and foreign origins are refused.

GET and HEAD serve the directory. `POST /access-requests` accepts an 8 KiB form from a WhoIs-identified human tailnet member, with a matching HTTPS Origin and an identity/host-bound two-hour CSRF token. Only owner-marked `requestable` apps appear in the form; hidden apps and old histories for apps made non-requestable remain hidden. The visitor sees their own request status, and can choose 1 hour, 1 day, 3 days or 7 days using English/Chinese labels, or let the owner choose. The owner approves with a duration through CLI/MCP. If expiry maintenance cannot acquire the registry lock, the page keeps accessible apps available and explains that requests are temporarily unavailable; reload to retry. See [requests](requests.md). The independent HTTP server uses F8's default 32 MiB request cap, 10-second header deadline, 30-second body-read inactivity bound and 60-second keep-alive idle timeout, with the same listener and request-budget wrappers as app nodes. The portal's registry reader rejects symlinks/special files and caps reads at 4 MiB. App logins, browser guest links and multi-host discovery are outside this package.

The people guide supports `--qr` and `--qr-png <file>` for this exact portal URL. Its four-step English/Chinese phone guide and bearer-link discipline are documented in [people](people.md).
