# Share and access durations

TSLink works with Tailscale and is an independent project. All share/access
lifetimes use one grammar and one policy in `internal/duration`.

| Form | Examples and meaning |
| --- | --- |
| Relative | `90m`, `36h`, `3d`, `1w`, `1d12h`, `1.5h` |
| Absolute | `until 2030-06-01`, `until 2030-06-01T18:00`, `until 2030-06-01T18:00:00Z` |
| Permanent | `never`, only for a tailnet-member grant with `--ack-never` (MCP `ack_never: true`) |

Relative components must use unique units in descending order:
`w,d,h,m,s,ms,us,ns`. Decimal components are allowed; arithmetic is exact to
nanoseconds and overflow is refused. `1h30m` works; `30m1h`, `1h1h`, signed
values and spaces between components do not. Outer whitespace is ignored.
Days are exactly 24 elapsed hours and weeks exactly 168, including across DST.
Suggestions in help and MCP schemas are **1h, 8h, 24h, 3d, 7d**; they are not
an exhaustive list. The old finite Funnel values, including `72h`, keep their
meaning. Printed remaining durations such as `168h0m0s` parse too.

An absolute date means **midnight at the start of that local date**. Offset-free
times use the operator process's local timezone (`TZ` on platforms that support
it), including when called through MCP; they do not use the recipient's timezone.
The parser refuses nonexistent times during a DST jump and duplicate times
during a DST rollback, including half-hour changes. Supply an RFC3339 offset
to choose an instant, e.g. `until 2028-11-05T01:30:00-04:00` or `-05:00`.
Invalid leap days and deadlines at or before the operation time are refused.
Stored deadlines remain absolute UTC timestamps, unaffected by later TZ changes.

Every new share/access lifetime is at least **1h** from the operation time.
Guests and public Funnel have a default maximum of **7 x 24h**. Exactly 7d
works; 7d plus 1s does not. Tailnet members have no finite maximum beyond the
parser's representable range. New people grants and Funnel default to 24h.
If the configured maximum is below 24h, give an explicit lifetime within it.

The owner can set the guest/public maximum in their isolated or normal
`config.json`, preserving its other fields:

```json
{"durations":{"public_max":"14d"}}
```

Omit `durations` to use 7d. When present, `public_max` must be a relative value
at least 1h. Missing/empty/null values, absolute dates, `never`, overflow, bad
types and unknown keys are refused on both load and save. Invalid configuration
fails duration changes; it never silently falls back to 7d. Config changes apply
to subsequent operations, without retroactively rewriting stored deadlines.

People without device invitation history are owner-designated tailnet-member
logins. `--invite`, or any recorded device invitation history for that person,
uses guest policy, including later updates and extensions. This is deliberately
conservative: membership is not inferred from acceptance of an invite.
`never` is refused for guests and all public exposure even with acknowledgement.
Already persisted permanent state is read and preserved losslessly; new public
`never` requests are refused. Existing updates that omit expiry retain deadlines,
including legacy permanent grants; newly added apps get a finite 24h deadline.

The first `--invite` on existing member grants also checks any retained deadlines against guest policy; supply an explicit finite `--for`/`--until` if they were permanent, too long or under 1h remaining. Recorded invitation retries preserve their existing deadlines.

## Set a new deadline

```sh
tslink people add alice@example.com --apps photos --for 90m
tslink people update alice@example.com --until 2030-06-01T18:00:00Z
tslink people update alice@example.com --for never --ack-never
tslink extend photos --person alice@example.com --for 36h
tslink extend preview --until 2030-06-01T18:00:00Z
tslink extend photos --person alice@example.com --for 1h --regrant
```

`--for` and `--until` are mutually exclusive. `--for` can itself contain the
quoted `until ...` form. `extend` selects a person grant for one app with
`--person`; without it, it selects that app's Funnel TTL. A relative lifetime
sets **operation time + duration**, not old deadline + duration, so it can
extend or shorten. All policy checks are against the operation time.
Expired deadlines (including equality) and durable person expiry latches
require explicit `--regrant`; this resets expiry only, never a person's
revocation tombstone. An expired acknowledged Funnel that has been downgraded
to private can be reactivated with `--regrant`. Ordinary operator-disabled
Funnel with a future deadline cannot be reactivated by this command.
People `update` remains F1's explicit grant replacement/renewal operation;
use `extend` for one app and its stricter re-grant guard. No invitations are sent.

`extend` always emits the existing version-1 JSON envelope. Its data includes
`service`, optional `who`, `audience`, `previous_expires_at`, `expires_at`,
`regranted` and `changed_at`. The two expiry fields are always present; `null`
represents permanent access. Errors use existing stable codes (`usage_error`,
`conflict`, `not_found`) and the usual failure envelope. MCP tool `extend`
accepts `service`, optional `who`, exactly one of `for`/`until`, and optional
`regrant`/`ack_never`. Its output data has the same fields.

The registry resolves and saves changes under its existing lock. The
post-save `DurationChange` payload is the hook for future access-log integration;
there is no access-log dependency or new timer. Enforcement remains the existing
request-time person expiry and Funnel lifecycle reconciliation.

## Which syntax belongs to which flag

| Flags / MCP parameters | Syntax |
| --- | --- |
| `add` / `apps share` / `add --recipe --funnel-ttl`; MCP `add`, `share`, `recipe_plan`, `recipe_apply` `funnel_ttl` | This lifetime grammar; public policy |
| `people add/update --for`, `extend --for`; MCP `for` | This lifetime grammar; audience policy |
| `people add/update --until`, `extend --until`; MCP `until` | Absolute form without the `until ` prefix |
| `add/share/url --wait`, MCP `url wait`, MCP `logs since`, `login --expires-in`, `mcp.events_keepalive` | Existing operational Go duration syntax plus `d`, with their existing limits; no `until`, `never` or share/access policy |
| `--health-interval`, `--health-timeout`, `--request-header-timeout`, `--request-read-timeout`, `--idle-timeout` and corresponding config/MCP fields | Existing Go duration syntax and bounds |
| `login --expires-at` | Existing RFC3339-only credential metadata syntax |

Credential expiry metadata, log windows, polling, health, request timeouts and
keepalive are not share/access lifetimes. Their syntax and semantics are unchanged.
F10 guest links and F12 approvals can call `Policy.Resolve` or `Policy.Check`
with `Guest`, `Public` or `TailnetMember`; their workflows are not implemented here.
