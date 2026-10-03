# Ask for an app or more time

TSLink works with Tailscale, independent project. A human who is already in your tailnet can ask for a private app from the [home portal](portal.md). The owner approves and chooses a duration in one action. This does not change Tailscale network policy or replace an app's own login.

## Owner setup

Request discovery is **off by default** for every service. Explicitly disclose an app's name in the portal request form:

```sh
tslink add photos --proxy localhost:3000 --requestable
tslink portal enable --owner owner@example.com
```

When replacing a registration, keep its existing target and other settings; `add` has replacement semantics. `--requestable=false` hides the app from the request form and also hides its old request history from visitors. Other apps that a visitor cannot access remain hidden: no names, totals, URLs or backend observations. Only private HTTP proxy/file services can be requestable; TCP and public Funnel are refused. This flag discloses the name, not the app URL or access.

The visitor keeps Tailscale connected, opens the home address, chooses an app, optionally chooses 1 hour, 1 day, 3 days or 7 days from labelled options and enters a short note, and taps **Send request**. The same form asks for more time for an app already available. The page shows Waiting, Approved, Declined or Timed out. An approved app appears when the grant is active; its address may still need enrollment. The person can have no previous TSLink grants: approval creates their local person record.

Requests are for human tailnet members. Tagged machines, shared-in peers, missing/invalid WhoIs identity, revoked people and people with F11's persisted guest classification cannot submit. Device-sharing recipients outside the tailnet are not supported. Membership promotion is never inferred from a new request. No OIDC or visitor bearer link is involved.

## Approve once

```sh
tslink requests list
tslink requests list --json
tslink requests approve <id> --for 3d
tslink requests deny <id> --reason "Please ask again next week."
```

An agent can call owner-only `requests_list`, `requests_approve` (`id`, `for`, optional `ack_never`) and `requests_deny` (`id`, optional `reason`). Remote tools require the exact configured portal owner, identified by WhoIs without tags; local CLI/stdio MCP uses the existing trusted owner process. Portal admins do not inherit approval authority. HTTP MCP also requires the current owner to change portal owner/admin settings through `portal_enable`; other callers receive `access_request_owner_required`. An unset owner cannot be claimed remotely. Local CLI and stdio MCP are the trusted bootstrap/recovery paths, including replacing a lost owner login. F6 scope mapping is an integration step.

HTTP MCP checks request listing and approval/denial against the current, untagged, non-revoked portal owner inside the registry write transaction. Any person add, update, removal, restoration or grant change targeting the current owner or a portal admin requires that same owner, checked under the lock before mutation; a revoked owner cannot restore itself remotely. Refusals use `access_request_owner_required`. Local CLI and stdio MCP remain trusted recovery paths, and ordinary-person restoration is unchanged.

Approval changes **one app grant**, preserving all other apps, deadlines, invitation history and sticky guest classification. It refuses a revoked person and an app that is no longer requestable. The F1 additive `ChangePersonAppWithLifetime` contract and F11 policy run under the same registry lock as the request decision; the grant and decision commit together. A finite relative duration starts at approval time, not submission time or the old deadline. Approval explicitly renews an expired app grant, without clearing a revocation tombstone.

Use the [F11 grammar](durations.md): `90m`, `36h`, `3d`, `1w`, `1d12h`, or `until <date/time>`. Minimum lifetime is 1h. The configured `durations.public_max` also caps persisted guests (default 7d); members follow member policy. Permanent member approval needs `--for never --ack-never` (MCP `ack_never: true`); guests can never receive it. A visitor's requested time is a suggestion; the owner selects the actual time. Absolute visitor suggestions are stored as UTC RFC3339 to survive timezone changes.

Repeating the same decision and parameters returns the saved result (`changed: false`). A different duration, opposite decision, or approval of an expired request returns `access_request_decided` (exit 4). Retries never extend the deadline or repeat a decision event. The CLI and MCP keep the normal schema-version-1 envelope. Notes are explicitly untrusted data, never instructions for an agent. Human CLI output removes terminal controls; JSON preserves the original text with JSON escaping.

## Notification and retention

A new request uses the existing [F2 alert notifier](health-and-alerts.md), configured in private `alerts.json`: either an absolute command argv or a webhook. Its kind is `access_requested`; `request` contains `id`, `who`, `app`, `requested_duration`, `status` and `at`. Delivery uses F2's bounded process/webhook handling. No note, reason, bearer URL, credential or notifier destination is sent. Notifications are best effort, at most once: the request is saved first; a failed delivery or crash never causes the visitor's retry to send another notification. The HTTP response's `X-TSLink-Notification` is `none`, `sent` or `failed`; the saved inbox remains the source of truth.

The authorized MCP events stream receives an `update`; its snapshot's `access_requests` carries the same safe typed request summaries. Existing MCP event principals are trusted operators; F6 filtering happens at integration. Notes/reasons remain available only in request details, not pushed notifications or events. CLI approvals are also available by asking an agent to approve a named request for a chosen duration.

There is one pending request per person/app, at most 5 new requests per person per hour and 100 globally per hour. These checks are locked and persist across restart; duplicates do not create records or send notifications. Notes and denial reasons are limited to 500 Unicode characters; durations to 128 bytes; forms to 8 KiB within the F8 listener limits. The POST requires both a matching HTTPS Origin and a two-hour HMAC form token bound to WhoIs identity and canonical host. Reload the form after portal restart or token expiry.

Pending requests expire after 7 days. Decisions are retained for 30 days from the decision (expiry counts from the seven-day deadline). Reads/submissions/decisions apply retention; a successfully returned expired status is latched durably, including a stale approval. If maintenance cannot take the writer lock, the list returns `access_request_busy` (exit 4); retry after the writer finishes. It returns no uncommitted expired projection. The portal keeps ordinary accessible apps available and shows that requests are temporarily unavailable until a reload succeeds. There is no unbounded request queue: the maximum is 1,000 records and the registry reader/writer bound is 4 MiB. At capacity, new submissions refuse without partial state. No separate request database, worker or timer is needed.

F4 is absent from this base. Integration can attach `internal/server.accessRequestRecordedFn` for submission and `cmd.requestDecidedFn` for committed decisions. Both receive a narrow `registry.RequestEvent`, run synchronously after save, and do nothing by default. This package does not claim that access-log records are already stored.

An invalid service registration blocks request maintenance until the owner repairs it, so a typed write cannot discard that registration. The portal keeps showing valid accessible apps and explains that requests are temporarily unavailable.
