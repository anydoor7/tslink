# Connecting an MCP client

TSLink speaks MCP over two transports. Both expose the same 19 tools from one
registry, so a client written against either works against the other.

| | `tslink mcp` | `tslink serve --mcp` |
|---|---|---|
| Transport | stdio, newline-delimited JSON-RPC | Streamable HTTP at `https://<node>.<tailnet>.ts.net/mcp` |
| Network listener | None | TLS listener on a dedicated tsnet node, tailnet-only |
| Authorization | The local user who launched it | `mcp.allow` login emails and `tag:` entries, mandatory |
| Reachable from | This machine | Any machine in the tailnet |

## stdio

Configure the client to launch the installed `tslink` binary with the single
argument `mcp`. The process opens no network listener of its own; calling its
`share` tool may start the separate TSLink daemon and the requested tsnet
service.

```json
{
  "mcpServers": {
    "tslink": {
      "command": "tslink",
      "args": ["mcp"]
    }
  }
}
```

## HTTP

`tslink serve --mcp` serves the same tools over HTTPS on a dedicated tsnet
node. The control plane is off by default. Turn it on with the `--mcp` flag or
with `mcp.enabled` in `config.json`; either is enough.

`mcp.allow` is required and lives only in `config.json`, because it is the
security boundary of the whole feature. `tslink config set` manages only
`control-url`, so edit the file directly:

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com", "tag:ops"],
    "node_name": "tslink-mcp"
  }
}
```

An empty `allow` list, or one containing only blank entries, makes `serve`
refuse to start. It never means "allow everyone". Every rejection returns the
same `403` JSON-RPC `forbidden` body, and a peer that passes `mcp.allow` has
full control: it can register and delete services, publish them to the public
internet through Funnel, and send or revoke real Tailscale invitations.

A request carrying an `Origin` header must match the endpoint's own
`https://<node>.<tailnet>.ts.net` origin exactly, per the MCP Streamable HTTP
transport spec; anything else is rejected with `403` before authorization. A
request with no `Origin`, which is what a command-line MCP client sends, is
allowed through.

Only a client running on a machine inside your tailnet can connect. Claude
Desktop and claude.ai cannot: their remote MCP connections originate from
Anthropic's cloud rather than from your device, so they never reach a private
tailnet address. Use the stdio transport for those.

## Event stream

The control plane also mounts a server-sent event stream at `/events` on the
same node. Four properties decide whether a client works:

- **It emits only named events** (`snapshot`, `update`, `keepalive`) and never
  a default `message` event. A client that only sets `onmessage` receives
  nothing at all, not merely "no heartbeats".
- **There is no `retry:` field and no `Last-Event-ID` replay.** A reconnect
  gets a full snapshot.
- **`id:` carries the instance-global `event_id`**, which is a different
  counter from the per-stream `sequence`. Deduplicate on `event_id`.
- **Drop cached state whenever `instance` changes.** A new `instance` means the
  daemon restarted and the previous ids no longer refer to the same sequence.

Every frame states its type twice, in the SSE `event:` field and in the
payload's own `type`, so a client never has to infer whether it is holding a
full state or a delta.

## Tools

The one-line summaries below condense each tool's own description as
registered in `cmd/mcp.go`; the client receives the full text.

| Tool | What it does |
|---|---|
| `share` | Expose a local directory, a single file, or an HTTP port on the user's private Tailscale network. Setting `funnel` true publishes the target to the entire public internet, so ask the user first. |
| `add` | Write a registry entry for a proxy, file, or TCP service reachable on the user's private Tailscale network. Setting `funnel` true publishes it to the entire public internet, so ask the user first. |
| `list` | List locally registered TSLink services with exact runtime URLs and explicit Funnel requested/active/reason state. |
| `unshare` | Remove one named service from the local TSLink registry. |
| `status` | Report local TSLink daemon, stored-credential, node-authorization, and service-count state. |
| `url` | Return one registered service's exact runtime URL. |
| `tags_list` | List every registered service with the ACL tags recorded for it locally. |
| `tags_set` | Replace one service's ACL tags with a single tag in the local registry. |
| `access_explain` | Explain what TSLink knows locally about one service's access: its exposure, whether an allow-list is enforced, and which layers this answer does not cover. |
| `doctor` | Run local TSLink diagnostics and return findings by severity. |
| `logs` | Read recent lines from the local TSLink daemon log. |
| `invite_user` | Sends a real Tailscale invitation to a real email address, so confirm the address and role with the user before calling this. |
| `invite_device` | Sends a real device-sharing invitation to a real email address outside the tailnet, so confirm the service and address with the user before calling this. |
| `invite_list` | List open tailnet user invitations and TSLink-owned device invitations. |
| `invite_revoke` | Cancels a real outstanding invitation on the user's tailnet, so confirm with the user before calling this. |
| `invite_resend` | Sends another real invitation email to the original recipient, so confirm with the user before calling this. |
| `template_list` | List the built-in service templates. |
| `template_plan` | Preview which services a built-in template would create, without writing the registry. |
| `template_apply` | Write a built-in template's missing services into the local registry. |

Daemon lifecycle, install, login and logout, and configuration stay CLI-only.

## Redaction is not a boundary

The `logs` tool sanitizes what it returns, but the same log file is emitted
verbatim by the `tslink logs` CLI. Treat the redaction as narrowing what
incidentally reaches a model's context, never as a way to keep a credential
from an agent that has a shell. See [SECURITY.md](../SECURITY.md) for the
shapes it does not cover.
