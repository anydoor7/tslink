# MCP scopes

TSLink works with Tailscale and is an independent project. MCP scopes let the
owner delegate app operations through TSLink without delegating the owner's
whole control plane. Tailscale still controls which devices can reach the node.

## Built-in roles

| Role | Capabilities | App boundary |
|---|---|---|
| `viewer` | `list`, `status`, `health`, `doctor`, `url`, `tags_list`, `access_explain`, `access_log`, `access_summary`, `people_list`; static `recipe_list` and `template_list` catalogs | Explicit `apps`, or explicit `inventory: true` for all app inventory |
| `app-operator` | Viewer capabilities plus `app_restart`, `people_grant`, `people_revoke`, person `extend` | Listed apps only; a positive `max_duration` is required |
| `people-manager` | Viewer capabilities plus `people_grant`, `people_revoke`, person `extend`, and owner-approved `requests_list`/`requests_approve`/`requests_deny` | Listed apps only; a positive `max_duration` is required |
| `owner` | Every shipped tool, including `mcp_audit` | Unrestricted; cannot carry app or duration restrictions |

Reduced roles cannot register/delete services, use Funnel, change tags/global
settings, discover host listeners, apply/plan recipes or templates, read global
logs/invites, or call the original whole-person mutation tools. There are no
custom scopes or arbitrary tool grants in this release. Elevated invitations
also require the existing owner opt-in `mcp.allow_elevated_invites`. Scope
administration stays in the owner's configuration, outside MCP.

`people_grant` takes `{"who":"alice@example.com","app":"photos","for":"1h"}`.
`people_revoke` takes `{"who":"alice@example.com","app":"photos"}`. They change
one private HTTP/file grant and preserve other apps, deadlines and invitation
history. They cannot undo a whole-person owner revocation. `for` must be a
duration of at least 1h within audience policy and `max_duration` and cannot outlive the binding. `never`
is refused. These tools create no Tailscale invitations: people already on the
tailnet need none; the owner handles network invitations separately. Revocation
denies future HTTP requests; accepted network shares and in-flight streams may
remain. TCP and ungated public Funnel services cannot carry people grants. Gated guest apps retain people enforcement on their private listener.

`app_restart` takes `{"app":"photos"}` and queues restart of that app's TSLink
gateway node. It preserves enrolled node identity and other apps. It does not
restart the third-party application, install a daemon or prove the restart has
completed. Poll `status`/`health` afterward. Reduced operators cannot restart
an existing public Funnel app.

## Give your family's agent read-only access

Keep your own legacy owner entry, and add the family member's exact WhoIs login
in `config.json`:

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com"],
    "bindings": [
      {"principal":"family@example.com","role":"viewer","apps":["photos"]}
    ]
  }
}
```

Restart the daemon after editing MCP configuration. Let their MCP client connect
to `https://tslink-mcp.<tailnet>.ts.net/mcp` from a device able to reach your
tailnet. `tools/list` advertises only viewer tools. `list`, `status` and `health`
show photos; other apps, their counts, grants and global diagnostic evidence are
not returned. A viewer doctor reads scoped health observations and does not run
fresh credential, host, supervisor or external probes. Read-only MCP access does
not itself grant permission to open photos; manage app access separately.

## Remote bindings and expiry

`mcp.allow` remains an owner allowlist. To reduce an existing login's authority,
remove it from `allow` and add one binding; a duplicate principal across the two
lists is refused. A binding's login must be canonical: outer ASCII whitespace
removed and ASCII A-Z lowercased, without Unicode case folding. Tags use the exact `tag:` prefix and preserve their name's case, including legacy
uppercase names; case folding never turns them into a broader grant.
Unknown fields/roles, duplicate binding principals/apps, invalid
names, `all` app selectors, missing app lists and invalid lifetimes fail closed.

```json
{
  "principal":"colleague@example.com",
  "role":"people-manager",
  "apps":["finance"],
  "max_duration":"8h",
  "issued_at":"2026-10-02T12:00:00Z",
  "for":"7d"
}
```

Replace `issued_at` with the actual grant time. `for` is parsed using the shared
Go-duration-plus-days parser and anchored to that time, so restart never extends
it. Alternatively use an RFC3339 `expires_at` timestamp; it excludes
`issued_at`/`for`. Omitting both expiry forms makes the binding indefinite.
Expired bindings deny new requests; an admitted HTTP request/stream is cancelled
at its remaining deadline. In-progress remote effects are not rolled back by
expiry. Every MCP mutation rechecks session lifetime and cancellation after lock waits at its transaction or side-effect boundary. Cancellation can unwind only the exact tentative registration from an already-started share. Compensation retains and rechecks the binding deadline under its lock; after expiry it may leave that registration for the owner to inspect using the audit receipt.

WhoIs is resolved on every HTTP request, before tools are routed. A tag principal
matches node tags only, never a login with the same spelling. Explicit login
bindings (including legacy login entries) take precedence. Otherwise all matching
tag principals in `bindings` and legacy `allow` are evaluated together. More than
one matching tag is denied with HTTP 403. A single legacy entry retains owner authority. Treat a legacy owner tag as broad authority over
every device carrying it; remove it when migrating those devices to reduced
roles. Doctor warns about every owner tag, every expired binding and configurations
with multiple distinct tag principals that could match the same node. It does
not inspect live node tags.

## Reduced local sessions

The launching OS user is owner by default. Opt in per process:

```sh
tslink mcp --scope viewer --apps photos
tslink mcp --scope app-operator --apps photos --max-duration 8h
tslink mcp --scope people-manager --apps finance --max-duration 2h
tslink mcp --scope viewer --inventory
```

Operator/manager stdio sessions default `max_duration` to 24h. Viewer inventory
must be explicitly enabled; an empty app list alone grants nothing. MCP arguments
cannot change the launch scope. Configure an untrusted agent's MCP command with
these flags; also restrict its shell and filesystem capabilities. This is an MCP
permission boundary, not an OS sandbox: another process running as the owner can
read configuration or start a fresh owner session.

## Denials, output and audit

App/time/lifetime denials use `mcp_scope_denied`. Hidden tools behave exactly as
nonexistent tools: the legacy protocol returns JSON-RPC `-32602`; the current
header-routed HTTP transport returns the SDK's unknown-tool HTTP 400. HTTP
identity denials return 403 with `error.data.code: mcp_scope_denied`. Both text and
structured results are projected before leaving the tool choke point. Reduced
clients use polling; the owner's global `/events` stream returns 404 to them.

Every recognized mutating tool reaching the choke point records time, caller,
role, effective capabilities, tool, app references and stable result. The journal
is `mcp-audit.json` in the config directory, bounded to 1,024 entries and 1 MiB;
oldest entries rotate. A `started` receipt is persisted before effects, followed
by a completion receipt with the same ID. A missing completion means unknown
outcome. No raw arguments, links, credential values, targets or error messages
are stored. Protocol-level unknown tools never execute a mutation.

The owner reads it with `tslink mcp-audit --json` (the normal versioned envelope)
or `mcp_audit`. A failed intent write refuses mutation with
`mcp_audit_unavailable`; a failed completion write explicitly says mutation may
have occurred. Concurrent writers serialize through a file lock; lock waiting is
bounded. Partial/corrupt, special-file or oversized journals are preserved and
refused. `status` shows value-free bindings; `doctor` reports risky bindings.

`access_log` and `access_summary` combine the mutation journal with daemon access events, filtering before aggregation and truncation. Reduced roles need explicit app grants; inventory alone does not expose history. The shared duration parser accepts `90m`, `1d12h` and `until <date/time>` with presets `1h`, `8h`, `24h`, `3d`, `7d`. Guest/public defaults remain bounded to 7d; configured audience limits, `max_duration` and binding expiry all apply. Capability-binding lifetimes may be shorter than the 1h grant minimum.

`guest_create`, `guest_list`, `guest_show` and `guest_revoke` remain owner-role tools; creating a guest cannot outlive an expiring owner binding. `people_add`/`people_update`, including their QR arguments, also remain owner-role tools. `extend` permits operators/managers to change an in-scope person's grant; only an owner can select Funnel. Reduced roles cannot change protected portal-owner/admin people records.

Request decisions require an owner or people-manager role. Remote calls additionally require the current unrevoked human portal owner, checked under the same registry lock as the decision. A tagged principal cannot approve. A people-manager binding can further restrict that owner to listed apps and finite durations; an unrelated manager does not gain approval rights. Local scoped stdio retains the trusted host identity but still applies role, app and duration restrictions. Listing is annotated mutating because expiry/retention maintenance can write the request ledger. Guest list/show and access-log queries remain read-only.

See [access history](access-log.md) for the receipt field mapping, surfaces, independent bounds and post-commit audit failure semantics.

Tailscale references checked 2026-10-02: [tsnet LocalClient and WhoIs](https://tailscale.com/docs/reference/tsnet-server-api)
and [device tags](https://tailscale.com/docs/features/tags).

Reduced operators and managers can change grants only for people already in the people store. Creating a person is owner-only; `mcp_person_owner_required` tells the caller to ask the owner to add the person first. Revoking an unknown login is a no-op and does not create a person or alter other apps.

Audit entries separate `identity.login` and `identity.node` from the matched `principal`. The `kind=mcp`, `role`, `tool`, `apps`, `result` and `phase` fields are typed metadata. Share intent records the explicit requested name, or no app when allocation is pending; completion records the actual generated or reused name, including concurrent allocation results. Raw arguments, targets, invitation links and secrets are excluded.

Daemon bootstrap keeps the caller session through scope inspection, installer conflict checks, supervision checks before and after installation, definition writes, and manager commands. Each manager query checks the session before invocation and adds its existing timeout to the caller context, so cancellation stops an in-flight query subprocess. Bounded restoration of a prior installation and failure cleanup may continue after cancellation or expiry. Legacy journal `who`/`scope` fields are read as `principal`/`role` without inventing missing identity; journal reads reject a regular file anywhere in the parent directory chain.

Shared status reads retain the caller context through MCP `status`, `health`, owner and reduced `doctor`, `list`, `url`, share/add outcome polling, and owner event snapshots. Supervision queries stop when the caller cancels and check session expiry before starting each process. Reduced doctor projects app observations from the shared status reader; that reader also inspects supervision and stored credentials, without running full doctor host-discovery or external probes. An MCP inspection denial remains `mcp_scope_denied`; a setup inspection failure for a caller without an MCP session, including cancellation, retains `daemon_setup_failed` and recovery guidance.

Bounded manager compensation derives its context from the caller and retains identity and scope. Only restoration of captured prior state and disabling an uncertain replacement use an independent lifetime; ordinary manager reads and replacement activation retain request cancellation and expiry.
