# Public Funnel lifetime

Funnel exposes an HTTP proxy publicly. It requires explicit `--public` (MCP
`public_ack`) and does not enforce a person's login or allow-list.

```sh
tslink add preview --proxy localhost:3000 --funnel --public --funnel-ttl 90m
tslink add preview --proxy localhost:3000 --funnel --public --funnel-ttl 'until 2030-06-01T18:00:00Z'
tslink extend preview --for 3d
tslink extend preview --for 1h --regrant
```

The [duration grammar and policy](durations.md) accepts relative or absolute
lifetimes. Minimum 1h, default 24h, default maximum 7d (168h). Presets:
1h, 8h, 24h, 3d, 7d. `72h` remains valid; `never` is refused for new public
requests. The owner can change the maximum in `config.json`:
`{"durations":{"public_max":"14d"}}`. A smaller-than-default cap requires an
explicit lifetime within it. The same parser/policy applies to MCP `share`,
`add`, `recipe_plan`, `recipe_apply`, and the recipe CLI.

`extend` sets operation time + duration, so it can shorten as well as extend;
`--until` sets an absolute deadline. An expired TTL requires `--regrant`, which
can also reactivate an acknowledged Funnel already downgraded to private by
cleanup. It cannot reactivate an operator-disabled Funnel with a future deadline.
The result always uses the versioned JSON envelope and records both deadlines.

The persisted `funnel_expires_at` remains an absolute UTC timestamp. Cleanup
retains it when downgrading public exposure to private. Historical explicit
`"never"` entries remain readable and are preserved when no new TTL is supplied;
`extend` can replace them with a finite deadline. No lossful state migration is
performed. Publishing an existing private service without a TTL uses the finite
24h default within current policy. Only a previously decided, acknowledged public
lifetime can be preserved, including a legacy explicit public `never`. CLI dry
runs and MCP add follow the same rule. The registry checks the final new lifetime
after preservation, before saving. Existing `add` omission preserves a public deadline; its expired
deadline retry and repeated `share` renewal keep their existing re-arm behavior,
subject to the current duration policy.

Funnel policy provisioning and `--no-auto-provision` retain their existing
behavior. Finite TTL does not retract already delivered data or force-close
in-flight streams. TSLink works with Tailscale and is an independent project.
