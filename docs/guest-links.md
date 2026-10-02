# Browser guest links

TSLink works with Tailscale and is an independent project. Guest links open one HTTP proxy app in a browser for a limited time. Recipients install nothing and need no Tailscale account. The owner still needs a running TSLink node, Tailscale HTTPS and Funnel permission. The public edge uses that node's `*.ts.net` name; no domain or VPS is needed.

## Create and send

Start a private proxy normally, then create a guest grant:

```sh
tslink guest create photos --for 3d --label "Aunt May" --public --print-link --json
```

`--public` explicitly acknowledges internet reachability through Funnel, with a mandatory guest gate. It is required when first enabling the gate. The command changes local configuration; the watcher opens Funnel using the existing provisioning rules. Check `status` and `doctor` for actual availability. It does not install a daemon, enroll a node or call a live tailnet API itself.

Use `--pin` to read a 6–64 digit PIN from hidden terminal input or stdin. Send the PIN separately. Do not put PINs in shell arguments. MCP accepts a secret `pin` string. Avoid logging MCP request payloads.

`--print-link` / MCP `print_link:true` is the explicit bearer disclosure. Without it, `link` is null and the message contains no URL. The private registry stores only a randomly salted SHA-256 token hash, so the link cannot be recovered later: create another grant with explicit disclosure if you need a sendable link. List/show never reveal hashes, salts or tokens. For disclosure, a current exact node URL must exist before the mutation; otherwise creation fails without saving a grant. Bring the private app online first. No guessed hostname is printed.

JSON uses the standard schema-version-1 envelope. Create's `data` has `grant`, `link` (string or null), `message` (a short sendable message) and `edge_state`. List has `grants`; show/revoke have `grant`. Grant views include id, app, label, authentication, created_at, expires_at, revoked, expired, pin_required, uses, sessions, last_used_at and pin_failures.

The shared lifetime policy accepts `1h`, `8h`, `24h`, `3d`, `7d`, composite durations and `until ...`. Minimum is one hour; the default maximum is seven days. The owner may change `durations.public_max` via [duration configuration](durations.md). `never` is refused. Every request checks the grant's own persisted deadline. Session expiry cannot extend it; observing expiry latches it against clock rollback.

## Public gate and migration

Only connections attested by Tailscale's Funnel transport marker take the guest path. HTTP headers cannot impersonate that marker. Public requests without a valid session receive a generic 401, and the backend is not contacted. The gate is mandatory even if all grants have expired or been revoked. The public listener's deadline is extended to accommodate newly created grants; each grant remains independent.

An existing open Funnel is refused. First disable its open Funnel using `tslink add photos --proxy <existing-target>` without `--funnel`, preserving the app's settings; wait for `status` to show the private listener. Then create with `--public`. Guest mode is sticky: ordinary `add` cannot silently remove the gate. To intentionally return to private mode, remove and re-add the service; old grants remain historical but cannot authorize an ungated or missing service. A service name with guest history cannot become an open Funnel, even by editing JSON; use a separate explicitly acknowledged service name for an independent open publication. Removing and recreating a gate does not erase revocation tombstones or expiry latches.

Tailnet connections continue through people/allow authorization, including `people_scoped` and revocation tombstones. Guest sessions cannot authorize tailnet callers. A gated proxy supports people grants; ordinary open Funnel remains unsupported for people grants. TCP and file services cannot create browser guest grants. `preserve_host` forwards the same trusted canonical node authority for both audiences; missing authority fails closed after authorization.

## Browser flow and boundaries

A 256-bit base64url token opens `/guest/<token>`. Without a PIN, it creates a server-side session and immediately returns 303 to `/`. With a PIN, the gate renders a generic form whose POST action is `/guest/pin`. The form has a synchronizer CSRF token bound to a server-side challenge and a Secure HttpOnly cookie; browser Origin, when present, must match. Its body is capped at 1 KiB. Challenges last at most five minutes and never beyond grant expiry.

The session cookie is `__Host-TSLinkGuest`: Secure, HttpOnly, SameSite=Lax, host-only, path `/`. A separate `__Host-TSLinkGuestPIN` cookie holds a pending challenge. Both are stripped before proxying. Backend attempts to set these reserved names are removed. Other app cookies retain their values and attributes. Public forwarding strips Referer, `X-TSLink-Guest-*` headers and reserved query names `token`, `pin`, `guest_token`, `guest_pin`. These names and `/guest/` routes are reserved for TSLink on gated services. The gate consumes the PIN form; its body and the bearer route are never forwarded. All guest responses use `Referrer-Policy: no-referrer`, including backend responses. Public guest requests do not acquire Tailscale identity headers.

PIN checking uses salted PBKDF2-HMAC-SHA256 with 100,000 iterations. There is a persisted five-attempt window per grant and a ten-attempt window per source across grants, both 15 minutes. Attempts include successful checks. Source comes from the trusted Funnel connection's client address, never forwarding headers; unavailable source metadata shares a conservative `unknown` bucket. Grant lockout survives restart; the source buckets, pending challenges and sessions are in memory. Restart requires reopening a link but keeps grants. These maps and the ledger have a 4096-entry bound. Busy, missing, corrupt or unsafe registry state denies access.

## Revoke, inspect and audit

```sh
tslink guest list --json
tslink guest show <id> --json
tslink guest revoke <id> --json
tslink access log --app photos --json
```

Revoke is permanent for that ID. Every subsequent request rechecks it; a request already authorized and in flight may finish. Established streams are not forcibly disconnected. Use a new grant to renew/reissue. Counters track successful session creations and authorized app requests; `uses` includes requests whose backend later fails. Registry writes must succeed before an app request is forwarded.

MCP tools: `guest_create`, `guest_list`, `guest_show`, `guest_revoke`. They are registered as owner-only; current MCP principals are owners. Reduced F6 scopes must map the explicit owner-only registry before granting these tools to other roles. There is no portal integration.

`status` shows active labels/apps/deadlines without secrets. `doctor` warns within 24 hours of expiry and when current runtime evidence cannot verify active Funnel for a configured gate. F4 receives `kind=guest`, link ID, app, decision and stable reason: allowed, expired, revoked, bad_pin, rate_limited, plus invalid_token, session_required, csrf or unavailable. It also records the ordinary HTTP completion event. Link/PIN/cookie values are absent from both access events and daemon logs. The bounded asynchronous F4 writer may drop events during failure; consult its health. Counters are durable local state, not a claim of complete audit history.

## Choose the right sharing tier

| Need | Guest browser link | People grant |
| --- | --- | --- |
| Recipient setup | Browser only, optionally enter PIN | Tailscale install, sign-in and possibly accept a device invite |
| Identity | Anyone holding the link/PIN; label is an owner's note | Actual Tailscale login verified on each request |
| Scope | Exactly one HTTP proxy app | Named person's selected supported apps |
| Time | Finite; default maximum 7d | Deeper, longer access; permanent tailnet-member grants require acknowledgement |
| Internet edge | Public Funnel with mandatory guest authentication | Tailnet transport and people enforcement |
| Forwardability | Can be copied and shared | Bound to the verified login |

Use guest links for a short visit or a few days of access. Use people grants for recurring access and identifiable people. A label or PIN does not verify who visited. OIDC is not implemented; the separate `authentication:"token"` field leaves room for a future owner's-machine sign-in tier without changing Tailscale login semantics.

Implementation facts checked 2026-10-02: [Tailscale Funnel](https://tailscale.com/docs/features/tailscale-funnel), [ListenFunnel](https://pkg.go.dev/tailscale.com/tsnet#Server.ListenFunnel), [FunnelConn](https://pkg.go.dev/tailscale.com/ipn#FunnelConn). The pinned v1.102.4 source confirms ListenFunnel also accepts tailnet connections by default and FunnelConn.Src identifies the original client. Tests use local listeners, fake WhoIs and fake Funnel markers; no real Funnel or tailnet is used.
