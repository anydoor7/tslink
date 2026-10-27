# Share and access durations

TSLink works with Tailscale and is an independent project. All share/access
lifetimes use one grammar and one policy in `internal/duration`.

| Form | Examples and meaning |
| --- | --- |
| Relative | `90m`, `36h`, `3d`, `1w`, `1d12h`, `1.5h` |
| Absolute | `until 2030-06-01`, `until 2030-06-01T18:00`, `until 2030-06-01T18:00:00Z` |
| Permanent | `never`, for member or device-invited guest grants with `--ack-never` (MCP `ack_never: true`) |

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
Finite guest grants and public Funnel have a default maximum of **7 x 24h**. Exactly 7d
works; 7d plus 1s does not. Tailnet members have no finite maximum beyond the
parser's representable range. New `people add` grants and Funnel default to 24h. Recipients of `add --allow`, of the MCP `add`/`share` `allow` field and of `invite device` (CLI and MCP, which have no lifetime flag) default to no expiry, as an allow list always did; for `add --allow`, `--for`/`--until` limit them.
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

The access action verifies membership using the complete network user directory. Device-invited guests retain their guest classification for later finite updates and extensions; invitation acceptance alone is not proof of membership. Members and device guests may use `never` with `--ack-never`. Browser guest links and public Funnel always remain finite. Existing updates that omit expiry preserve deadlines, including legacy permanent grants; newly added apps default to 24h. `add --allow` is the exception for its own recipients: omitting `--for` creates a permanent grant without `--ack-never` (the allow list is the acknowledgement), re-adding never shortens or lengthens a grant that exists, and an explicit `--for never` still needs `--ack-never`. `invite device` (which has no lifetime flag) gives a grant it creates the same permanent default and leaves a grant that exists alone. A caller that could not create a permanent grant with an explicit `never` (an MCP binding with an expiry, or a reduced role) does not get one by omission either: it keeps the 24h default, which its binding expiry and `max_duration` accept or refuse like any explicit 24h.

When an existing member is first classified as a guest, retained finite deadlines must fit guest policy; retained permanent grants require acknowledgement. Guest classification is saved atomically with grants before remote work, even when sharing is incomplete. Retries preserve deadlines, and concurrent grant changes before invitation sending fail with `conflict`.

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
People `update` remains the people registry's explicit grant replacement/renewal operation;
use `extend` for one app and its stricter re-grant guard. No invitations are sent.

`extend` always emits the existing version-1 JSON envelope. Its data includes
`service`, optional `who`, `audience`, `previous_expires_at`, `expires_at`,
`regranted` and `changed_at`. The two expiry fields are always present; `null`
represents permanent access. Errors use existing stable codes (`usage_error`,
`conflict`, `not_found`) and the usual failure envelope. MCP tool `extend`
accepts `service`, optional `who`, exactly one of `for`/`until`, and optional
`regrant`/`ack_never`. Its output data has the same fields.

The registry resolves and saves changes under its existing lock. Successful extensions record the typed `DurationChange` in [access history](access-log.md); MCP changes attach to their completion receipt. People, guest and Funnel expiry latches also record committed transitions. Registry publication and the audit receipt are separate commits, so a crash can leave an audit gap. Enforcement remains the existing request-time person expiry and Funnel lifecycle reconciliation. Scoped grants and extensions additionally obey the scope maximum and binding expiry.

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
Guest links and access-request approvals can call `Policy.Resolve` or `Policy.Check`
with `Guest`, `Public` or `TailnetMember`; their workflows are not implemented here.
