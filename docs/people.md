# Share apps with a person

Register and enroll your apps first. Then grant access using the person's **actual Tailscale login** (the account they use in the Tailscale app):

```sh
tslink people add alice@example.com --apps photos,finance --for 7d
tslink people list --json
tslink people update alice@example.com --apps photos --for 1h
tslink people remove alice@example.com
```

These commands only change local authorization. They need no stored credential and do not install or start the daemon. Existing tailnet members can use their grants immediately when the daemon runs this version and their tailnet policy permits reaching the app. The output includes a message you can send to the person; an exact app address appears when a current running-daemon snapshot proves it. Otherwise the message asks the owner to get it with `tslink status --urls` after enrollment finishes.

`--apps all` selects every **currently registered private HTTP proxy and file service**. It excludes TCP and public Funnel and does not automatically include future apps. Explicitly naming a TCP or Funnel service fails atomically with `people_service_unsupported`. File services enforce the same HTTP WhoIs authorization as proxies, including single-file shares.

Adding an existing active person refuses; use `update`. An update with only `--apps` preserves deadlines for retained apps and gives newly added apps no deadline. Specify `--for` to apply the chosen lifetime to every selected grant. An update with only `--for` keeps the app set. `--for never` explicitly removes deadlines and renews expired grants. Removing a person is idempotent, and adding them again is an explicit new grant.

## Outsiders: one owner command and one message

For someone outside your tailnet:

```sh
tslink people add alice@example.com --apps photos,finance --for 7d --invite --print-links
```

This saves the grants and creates one single-use device invitation for each app. TSLink requests links, so **Tailscale sends no email**. Send the generated message yourself. The recipient installs Tailscale, signs in using the named account, accepts each app link, keeps Tailscale connected, and opens the app addresses. A device invite can be accepted with a different account than the addressed email, but TSLink denies that other login: the local grant binds the verified WhoIs login, not whoever holds a bearer link.

Device invitation creation requires a stored **user-owned API access token**. OAuth client tokens cannot create device invites. Use the existing stdin login path to store a user-owned token; do not put credentials in argv. Without a token, ordinary `people add` remains useful for existing tailnet members. You can also create app-sharing links manually in Tailscale's Machines page and send them with the no-token message.

No invitation is created unless `--invite` is supplied. Invitation URLs are bearer capabilities. They appear only with explicit `--print-links`; otherwise the result contains IDs and says the links are hidden. Recover hidden links through `tslink invite list --show-urls`, which is also an explicit disclosure. Links are never stored in the people registry or written to TSLink's invitation audit logs.

An invitation bundle can partially fail. JSON reports `complete: false`, a per-app stable `code`, and successful invitation IDs and side-effect plans. **Local grants remain saved.** Read these fields even when the command's envelope is successful. Resolve the error and use `tslink invite device <app> <login> --print-link` for each missing link; repeating a people update with `--invite` creates new invitations for its selected apps.

## Why per-app invitations

| Design | Owner and recipient work | App compatibility | Revocation |
| --- | --- | --- | --- |
| Per-app nodes, bundled links (implemented) | One owner command/message per person; N API creates and N recipient accepts for N apps | Apps keep their root URL, host, redirects, cookies and existing WebSocket proxy | Local WhoIs grants deny subsequent requests even if device shares remain |
| One shared home/gateway node | One invite/accept per person | Path prefixes require app base-path configuration or rewriting root-relative assets, redirects, cookies and WebSocket URLs. Per-app hostnames need DNS and certificates reachable in the recipient's tailnet; sharing only the gateway does not share the other app nodes | A gateway would need the same local identity gate, plus prevention of direct-app bypass |

The bundled design preserves the existing per-app transport isolation and avoids changing arbitrary applications. It eliminates N×M **owner CLI operations**, not the underlying N×M invites or recipient accepts. A one-accept gateway remains a future design, with explicit routing/hostname requirements; this package does not claim that gateway exists. Tailscale documents that an outsider sees only the machine shared with them, and reaches shared machines by their full tailnet-qualified hostname.

## Enforcement and expiry

The first people grant makes the app `people_scoped`. This marker stays after grants are removed. Unknown callers then need either an active grant or a named legacy `--allow` rule; an empty legacy allow list no longer means allow-all on that app. Existing explicit allow users/tags remain usable. For a known untagged login, the person's app set is authoritative and overrides legacy allow rules: losing an app, expiry and revocation all deny access. Tagged machines cannot impersonate people.

`people remove` removes matching legacy allow entries across apps and keeps a deny tombstone. This also denies that login on formerly unrestricted private HTTP/file apps. Removing a service removes its grants while preserving other people data; recreating that app does not restore old grants. Ordinary `add` preserves `people_scoped` and refuses conversion of a scoped app to public Funnel or TCP. Other people and legacy explicitly allowed accounts continue to work. Network policy still controls reachability; this feature does not edit remote ACLs, delete accepted device shares, revoke tailnet membership, or enforce raw TCP/public Funnel access.

Grants store absolute UTC `expires_at` values. A request at or after the deadline is denied even between the 30-second ticks. Startup, ticks and requests persist an `expired` latch. Clock jumps forward can expire grants early; clock rollback before a deadline has been observed can extend elapsed access. Once expiry is observed and saved, rollback and restart cannot restore it; explicitly update `--for` to renew. A failed registry read or expiry write denies the request. As with any local policy file, restoring an older backup can restore older authorizations.

HTTP requests and WebSocket upgrades are checked individually. Already accepted downloads, response streams and WebSocket connections may finish; revocation does not retract delivered data or forcibly close an upgraded connection. Reconnecting after removal/expiry fails.

## JSON, MCP and registry compatibility

All CLI JSON uses the existing `schema_version: 1` result envelope. `data.person` contains canonical `login`, `revoked` and `grants`; each grant has `app`, optional `expires_at`/`expired`, `active`, and optional exact `url`. Add/update also return `invites`, `complete`, `message` and `invite_requirement`. List returns `data.people`, including tombstones. Remove returns `login`, `removed`, `revoked`.

MCP provides `people_add`, `people_list`, `people_update`, `people_remove`. Mutation tools describe confirmation requirements. Add/update are destructive (they can narrow existing app exposure), non-idempotent with renewal/invites, and open-world because invites are optional; list is read-only; remove is local, destructive and idempotent. Elevated exit-node, reusable-link and tailnet-role invitation arguments are not accepted here. The existing invite tools retain their `mcp.allow_elevated_invites` owner-configured guard.

People-enabled registries write schema version 2, with top-level `people` and per-service `people_scoped`. Existing version 0/1 registries load without changing bytes; ordinary services-only writes retain version 1. Unknown fields remain strictly refused. An older binary rejects version 2 or the unknown people fields rather than silently rewriting them away. Before downgrade, stop the new daemon and restore a separately backed-up version 1 registry only after intentionally deciding to discard people authorization. An older daemon cannot enforce grants; do not downgrade a running sharing installation by just swapping the CLI binary.

The exported `registry.Person`, `PersonGrant`, `PersonGrantActiveAt`, and read-only `registry.Preflight` form the extension point for future lifecycle and audit packages.

Official sources checked 2026-10-01:

- [Sharing machines](https://tailscale.com/docs/features/sharing): recipient account, full hostnames, bearer links and separate network revocation.
- [Tailscale API](https://tailscale.com/api), [current OpenAPI document](https://api.tailscale.com/api/v2?outputOpenapiSchema=true): `POST /device/{deviceId}/device-invites`, no OAuth-client creation, optional email and `multiUse`/`allowExitNode`.
- [Trust credentials](https://tailscale.com/docs/reference/trust-credentials): invitation read/delete scopes do not imply create support.
- [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve): HTTP reverse proxy and caller identity headers.
