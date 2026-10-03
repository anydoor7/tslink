# Remote Mcp

## Remote MCP Control Plane

`tslink serve --mcp` serves the same MCP tool registry as `tslink mcp`, filtered by the caller's scope, over HTTPS on a dedicated tsnet node at `https://<node>.<tailnet>.ts.net/mcp`. It is an MCP endpoint for MCP clients, and there is no page to open in a browser. The node's default hostname is `tslink-mcp`; its tsnet state lives in `~/.config/tslink/mcp-node/`, beside the service nodes rather than among them.

The control plane is off by default. Enable it with the `--mcp` flag or with `mcp.enabled: true` in `config.json`; either one turns it on. `mcp.allow` (legacy owner entries) and/or `mcp.bindings` (scoped identities) are required and live only in `config.json`, because it is the security boundary of the whole feature (`tslink config set` manages only `control-url`, so edit the file directly):

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com"],
    "allow_elevated_invites": false,
    "node_name": "tslink-mcp"
  }
}
```

A `tag:` entry in `mcp.allow` authorizes every machine carrying that tag, including service nodes. Prefer specific login emails or a dedicated tag whose membership you control.

`mcp.allow_elevated_invites` is another `config.json` key, off by default. Set
it to `true` to let MCP clients invite users with roles other than `member` or
share a device with `allow_exit_node: true`. CLI invitation commands never need
this opt-in. `mcp.events_keepalive` accepts Go duration syntax plus `d` for
days, within its 5-second to 5-minute bounds.

`config.json` is read strictly: a key TSLink does not know, a typo included, is refused with `config_load_failed` by the commands that write settings, reported by `tslink doctor`, and makes `serve` fall back to the default control server without the control plane.

| Fact | Detail |
|---|---|
| Default | Off. Without `--mcp` or `mcp.enabled: true`, `serve` opens no control-plane listener and creates no control-plane node |
| Authorization | WhoIs login/tag matches `mcp.allow` owner entries or strict `mcp.bindings`. Empty authorization refuses startup. Identity denials are uniform 403 with `error.data.code: mcp_scope_denied`; see [MCP scopes](mcp-scopes.md) |
| Reach | The only listener is `ListenTLS` on the control plane's own tsnet node. It is never published through Funnel and never bound to a host interface or `0.0.0.0` |
| Node | Its own dedicated node, shared with no service. It is not a registry service, so it is absent from `tslink list`, and no code path can attach `--funnel` to it |
| Lifetime | Depends on how `serve` is logged in. With a stored credential (`tslink login`) the node is ephemeral: the derived auth key carries the ephemeral capability and tsnet logs in with the ephemeral flag, so a clean daemon stop logs the node out and Tailscale removes it within seconds; disabling `--mcp` leaves no device to delete by hand. After a crash the node lingers until Tailscale's ephemeral garbage collection reclaims it (Tailscale's KB states this normally happens 30 to 60 minutes after the last activity; that figure is Tailscale's, not measured by TSLink). With zero credentials (interactive browser login) the node is persistent and user-owned: one browser authorization survives daemon restarts, and disabling `--mcp` leaves the `tslink-mcp` device in your tailnet until you delete it in the Tailscale admin console |
| Power | Owner entries retain all tools, including service deletion, Funnel and invitations. Reduced roles get explicit tools and apps; elevated invitations also need `mcp.allow_elevated_invites`. Use scoped bindings to delegate |
| Origin | A request that carries an `Origin` header must match the endpoint's own `https://<node>.<tailnet>.ts.net` origin exactly, per the MCP Streamable HTTP transport specification; anything else is `403` before authorization runs. Requests without `Origin`, which is what command-line MCP clients send, pass through |
| Transport | Stateless Streamable HTTP; one request body is bounded at 1 MiB, the same limit the stdio transport applies per record |

**Which clients can reach it.** Only MCP clients running on a machine inside your tailnet, such as Claude Code on your laptop or a server, can connect. Claude Desktop and claude.ai cannot: their remote MCP connections originate from Anthropic's cloud rather than from your device, so they never reach a private tailnet address. Use `tslink mcp` over stdio for those clients when they run on the same machine.

### `tslink mcp` and `tslink serve --mcp` side by side

| | `tslink mcp` | `tslink serve --mcp` |
|---|---|---|
| Transport | stdio, newline-delimited JSON-RPC | Streamable HTTP at `https://<node>.<tailnet>.ts.net/mcp` |
| Network listener | None | TLS listener on a dedicated tsnet node, tailnet-only |
| Authorization | Launching OS user is owner; explicit `--scope`/`--apps` reduce it | WhoIs login/tag in `mcp.allow` or `mcp.bindings`, mandatory |
| Configuration | Optional scope launch flags | `--mcp` or `mcp.enabled`, plus principal bindings in `config.json` |
| Tools | Filtered by the launch scope | Same registry, filtered by the authenticated binding |
| Typical client | An MCP client on this machine | An MCP client on another machine in the tailnet |

## Delegation and migration

Every existing `mcp.allow` entry keeps owner authority. To reduce one, move its
principal into `mcp.bindings`; duplicate principals across the lists are refused.
Use `viewer`, `app-operator` or `people-manager` with explicit apps and appropriate
limits. Config changes take effect after daemon restart. Reduced clients poll
scoped tools; the global `/events` stream is owner-only and returns 404 to them.
See [MCP scopes](mcp-scopes.md) for the family read-only recipe, anchored expiry,
role matrix and durable audit journal.
