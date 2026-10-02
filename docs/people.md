# Share apps with a person

Register and enroll your apps first. Then grant access using the person's **actual Tailscale login** (the account they use in the Tailscale app):

```sh
tslink people add alice@example.com --apps photos,finance --for 7d
tslink people list --json
tslink people update alice@example.com --apps photos --for 1h
tslink people remove alice@example.com
```

Grant changes are local; removal also attempts pending-invitation cleanup after saving local denial. They need no stored credential and do not install or start the daemon. Existing tailnet members can use their grants immediately when the daemon runs this version and their tailnet policy permits reaching the app. The output includes a message you can send to the person; an exact app address appears when a current running-daemon snapshot proves it. Otherwise the message asks the owner to get it with `tslink status --urls` after enrollment finishes.

`--apps all` selects every **currently registered private HTTP proxy and file service**. It excludes TCP and public Funnel and does not automatically include future apps. Explicitly naming a TCP or Funnel service fails atomically with `people_service_unsupported`. File services enforce the same HTTP WhoIs authorization as proxies, including single-file shares.

Adding an existing active person refuses; use `update`. An update with only `--apps` preserves deadlines for retained apps and gives newly added apps no deadline. Specify `--for` to apply the chosen lifetime to every selected grant. An update with only `--for` keeps the app set. `--for never` explicitly removes deadlines and renews expired grants. Removing a person is idempotent. Adding them again is an explicit new grant and refuses until outstanding invite operations are cleaned up or reconciled.

## Outsiders: one owner command and one message

For someone outside your tailnet:

```sh
tslink people add alice@example.com --apps photos,finance --for 7d --invite --print-links
```

This saves the grants and creates one single-use device invitation for each app. TSLink requests links, so **Tailscale sends no email**. Send the generated message yourself. The recipient installs Tailscale, signs in using the named account, accepts each app link, keeps Tailscale connected, and opens the app addresses. A device invite can be accepted with a different account than the addressed email, but TSLink denies that other login: the local grant binds the verified WhoIs login, not whoever holds a bearer link.

Device invitation creation requires a stored **user-owned API access token**. OAuth client tokens cannot create device invites. Use the existing stdin login path to store a user-owned token; do not put credentials in argv. Without a token, ordinary `people add` remains useful for existing tailnet members. You can also create app-sharing links manually in Tailscale's Machines page and send them with the no-token message.

No invitation is created unless `--invite` is supplied. Invitation URLs are bearer capabilities. They appear only with explicit `--print-links`; otherwise the result contains IDs and says the links are hidden. Recover hidden links through `tslink invite list --show-urls`, which is also an explicit disclosure. Links are never stored in the people registry or written to TSLink's invitation audit logs.

An invitation bundle can partially fail. JSON reports `complete: false`, per-app `code` and durable `state`, successful IDs and side-effect plans. **Local grants remain saved.** Read these fields even when the command envelope succeeds. Resume with `tslink people update <login> --invite`: completed operations reuse their IDs, and only unfinished operations are sent. Add `--print-links` to retrieve existing links explicitly; an unavailable link is reported rather than recreated.

The registry stores non-secret person/app/node associations and operation states (`pending`, `sending`, `unknown`, `complete`, `revoked`, `accepted`, `cancelled`, `target_gone`, `replaced`), never URLs or tokens. TSLink saves `sending` before POST. A crash, timeout, failed POST response or invalid response requires remote reconciliation before another create. A device-invite list has no recipient metadata for link-mode invitations; TSLink never automatically assigns an unaddressed link, even when only one is listed. Inspect `tslink invite list --json` (or `--show-urls` to reveal links), verify the app and intended invitation, then explicitly resolve:

```sh
# Associate an owner-verified, existing invite ID; no new POST for that app.
tslink people update alice@example.com --invite --reconcile-invite photos=12345
# After verifying no invitation was created: requires an empty device invite list.
tslink people update alice@example.com --invite --reconcile-invite photos=none
# Resolve an unknown operation while keeping the person revoked, then clean up.
tslink people remove alice@example.com --reconcile-invite photos=12345
```

`--reconcile-invite` can be repeated for different apps. `none` is an explicit owner assertion plus a remote-list check, not an exactly-once guarantee. The API provides no idempotency key or proof that an absent result cannot appear later. TSLink **does not promise exactly-once delivery**. Do not use standalone `invite device` as recovery for a people operation; that separate command intentionally creates a new invitation. Local remote-work locking prevents simultaneous owner processes from sending the same operation; contention reports `people_invite_busy` and asks for retry. Each bundle's remote work is bounded to 15 seconds, or an earlier caller deadline.

`people remove` commits the deny tombstone and removes grants first, then revokes every recorded pending invite on its proven app node. JSON includes `complete` and per-app `cleanup` states/codes; human output reports deferred/partial remote cleanup. Without a token it still denies locally and retains the invite IDs for later cleanup. Retry removal after restoring the user-owned token. Completed cleanup is skipped; missing remote IDs are treated as already cleaned up. Accepted invitations are reported as `accepted`; accepted network shares may remain and need separate Tailscale management. If a concurrent create is still running, removal reports deferred cleanup immediately; retry removal to clean up its recorded outcome. Removing an app does not discard its invitation ledger.

If a recorded completed invitation disappears or expires, confirm replacement explicitly:

```sh
tslink people update alice@example.com --invite --replace-invite photos=12345 --print-links
```

The value is the **recorded old ID**, not a new ID. TSLink verifies the same owned node, lists its invitations and refuses while that ID is still present (including accepted invitations). It retires the old attempt as `replaced` before creating a successor and preserves every grant and deadline. Do not supply `--apps` or `--for` with replacement. Repeat the flag for different apps; MCP uses `people_update` with `invite: true` and `replace_invites: {"photos":"12345"}`. A retry bound to that old ID resumes/reuses its successor instead of replacing it again. An indeterminate successor still requires reconciliation. For compatibility, explicit `--reconcile-invite app=none` on a completed record also confirms replacement, but additionally requires the entire device invite list to be empty. Ordinary retries never replace automatically.

When a successful complete devices listing confirms both the recorded node and its hostname are absent, removal records terminal `target_gone`. The old app, hostname, node ID, invite ID and attempt remain in the ledger, and re-adding the person to another app works. Absence evidence requires HTTP 200, a nonempty body, and a present non-null array (`devices` for devices, a top-level array for invites). Any other 2xx, including 204 and 206, blank/null responses, malformed collections and ownership mismatches remain incomplete: no attempt is retired, no terminal outcome is recorded and no replacement POST is sent. This applies to replacement, removal and explicit `none` reconciliation. Bodyless mutation responses retain their separate handling. Terminal attempts are immutable history; successors use an increasing `attempt` number per person/app/node (omitted means the original attempt zero). This retains attempt outcomes, not a full event log of every transition.

## Why per-app invitations

| Design | Owner and recipient work | App compatibility | Revocation |
| --- | --- | --- | --- |
| Per-app nodes, bundled links (implemented) | One owner command/message per person; N API creates and N recipient accepts for N apps | Apps keep their root URL, host, redirects, cookies and existing WebSocket proxy | Local WhoIs grants deny subsequent requests even if device shares remain |
| One shared home/gateway node | One invite/accept per person | Path prefixes require app base-path configuration or rewriting root-relative assets, redirects, cookies and WebSocket URLs. Per-app hostnames need DNS and certificates reachable in the recipient's tailnet; sharing only the gateway does not share the other app nodes | A gateway would need the same local identity gate, plus prevention of direct-app bypass |

The bundled design preserves the existing per-app transport isolation and avoids changing arbitrary applications. It eliminates N×M **owner CLI operations**, not the underlying N×M invites or recipient accepts. A one-accept gateway remains a future design, with explicit routing/hostname requirements; this package does not claim that gateway exists. Tailscale documents that an outsider sees only the machine shared with them, and reaches shared machines by their full tailnet-qualified hostname.

## Enforcement and expiry

Accept any valid UTF-8 login string except an empty string, control characters, or internal whitespace. Invalid UTF-8 is rejected with `usage_error` before CLI, MCP or Store mutations; an invalid WhoIs login is an authoritative deny. Trim outer ASCII whitespace (space, tab, CR, LF, VT and FF). Lowercase ASCII A-Z only; apply no Unicode case folding or Unicode normalization, and compare the resulting bytes exactly. Apostrophes, `!`, `~`, other punctuation and non-ASCII addresses are supported. U+212A KELVIN SIGN stays distinct from `k`, and U+0130 dotted capital I stays distinct from `i`; lookalikes cannot acquire another login's grant. Unmanaged people retain legacy allow fallback, with the same exact comparison for login rules. Tagged machines remain machine identities and cannot use a person's grant.

For upgrade compatibility only, a login already stored by parent schema-2 writers remains loadable, exactly matchable and removable even if it contains legacy internal Unicode whitespace or C1 controls. New input cannot create such keys; this exception never folds or rewrites existing bytes. Parent JSON writers already replaced invalid UTF-8 with U+FFFD before saving; reads preserve those actual stored Unicode identities without repairing or rewriting them.

`access explain` and MCP `access_explain` report redacted people scope, grant/tombstone counts and the known-login override, and point to `people list --json` for detailed policy. Services with no people policy retain the legacy explanation.

The first people grant makes the app `people_scoped`. This marker stays after grants are removed. Unknown callers then need either an active grant or a named legacy `--allow` rule; an empty legacy allow list no longer means allow-all on that app. Existing explicit allow users/tags remain usable. For a known untagged login, the person's app set is authoritative and overrides legacy allow rules: losing an app, expiry and revocation all deny access. Tagged machines cannot impersonate people.

`people remove` removes matching legacy allow entries across apps and keeps a deny tombstone. This also denies that login on formerly unrestricted private HTTP/file apps. Removing a service removes its grants while preserving other people data; recreating that app does not restore old grants. Ordinary `add` preserves `people_scoped` and refuses conversion of a scoped app to public Funnel or TCP. Other people and legacy explicitly allowed accounts continue to work. Network policy still controls reachability; this feature does not edit remote ACLs, delete accepted device shares, revoke tailnet membership, or enforce raw TCP/public Funnel access.

Grants store absolute UTC `expires_at` values. A request at or after the deadline is denied even between the 30-second ticks. Startup, ticks and requests persist an `expired` latch. Clock jumps forward can expire grants early; clock rollback before a deadline has been observed can extend elapsed access. Once expiry is observed and saved, rollback and restart cannot restore it; explicitly update `--for` to renew. A failed registry read or expiry write denies the request. As with any local policy file, restoring an older backup can restore older authorizations.

HTTP requests and WebSocket upgrades are checked individually. Already accepted downloads, response streams and WebSocket connections may finish; revocation does not retract delivered data or forcibly close an upgraded connection. Reconnecting after removal/expiry fails.

## JSON, MCP and registry compatibility

All CLI JSON uses the existing `schema_version: 1` result envelope. `data.person` contains canonical `login`, `revoked` and `grants`; each grant has `app`, optional `expires_at`/`expired`, `active`, and optional exact `url`. Add/update also return `invites`, `complete`, `message` and `invite_requirement`. List returns `data.people`, including tombstones. People views also include non-secret `invites` operation records. Remove returns `login`, `removed`, `revoked`, `complete`, `cleanup`. Add/update invite views include `state` and optional candidate `reconcile_ids`; candidates are evidence, not recipient attribution.

MCP provides `people_add`, `people_list`, `people_update`, `people_remove`. Mutation tools describe confirmation requirements. Add/update are destructive (they can narrow existing app exposure), non-idempotent with renewal/invites, and open-world because invites are optional; list is read-only, including file modes; remove is destructive and idempotent with optional remote cleanup (open-world). Update accepts an invite-only retry; update/remove accept `reconcile_invites`, an app-to-ID (or `none`) object requiring explicit owner verification. Elevated exit-node, reusable-link and tailnet-role invitation arguments are not accepted here. The existing invite tools retain their `mcp.allow_elevated_invites` owner-configured guard.

People-enabled registries write schema version 2, with top-level `people` and per-service `people_scoped`. Existing schema-2 registries, including those written before the invitation ledger, load without rewriting bytes. The person object has optional `invites`; successor records add optional `attempt` and new terminal states. Earlier readers that do not understand these fields or states refuse rather than discard them. The schema version alone is not feature negotiation; use a compatible reader or restore a separately backed-up registry for downgrade. Existing version 0/1 registries load without changing bytes; ordinary services-only writes retain version 1. Unknown fields remain strictly refused. An older binary rejects version 2 or the unknown people fields rather than silently rewriting them away. Before downgrade, stop the new daemon and restore a separately backed-up version 1 registry only after intentionally deciding to discard people authorization. An older daemon cannot enforce grants; do not downgrade a running sharing installation by just swapping the CLI binary.

Expected input, missing-person/app and state conflicts use `usage_error`, `not_found` and `conflict` consistently in CLI and MCP.

The exported `registry.Person`, `PersonGrant`, `PersonGrantActiveAt`, and read-only `registry.Preflight` form the extension point for future lifecycle and audit packages.

Official invite API and sharing sources checked 2026-10-02 (other sources checked 2026-10-01):

- [Sharing machines](https://tailscale.com/docs/features/sharing): recipient account, full hostnames, bearer links and separate network revocation.
- [Tailscale API](https://tailscale.com/api), [current OpenAPI document](https://api.tailscale.com/api/v2?outputOpenapiSchema=true): `POST /device/{deviceId}/device-invites`, no OAuth-client creation, optional email and `multiUse`/`allowExitNode`, device invite listing and `DELETE /device-invites/{deviceInviteId}`. The schema supplies no client idempotency key.
- [Trust credentials](https://tailscale.com/docs/reference/trust-credentials): invitation read/delete scopes do not imply create support.
- [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve): HTTP reverse proxy and caller identity headers.
