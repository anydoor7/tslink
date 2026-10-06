# Connecting an MCP client

TSLink speaks MCP over two transports. Both serve the same tool registry, so a
client written against either works against the other. An owner session lists
44 tools and scoped roles list fewer; the live `tools/list` is authoritative.

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

### How a stdio session ends

When the client closes stdin, calls that were already read still get their
answers before the server exits. The `url` tool's `wait` is capped at 5m, and
a larger value is refused as a usage error. A call still running 6m after
stdin closed is cancelled; after a 5s grace for its handler to return,
`tslink mcp` exits 1 with a single error line. If a response is still blocked
on a stdout the client no longer reads, the exit waits at most another 5s for
it and then abandons it.

SIGINT or SIGTERM cancels the calls in flight, so a `share` still waiting for
its URL is rolled back unless an identical `share` or `add` has relied on that
registration in the meantime; then it stays. The command exits 1 with an error
line that names the signal. A second signal terminates the process at once.

Some inputs end the session and drop the answers of calls still in flight,
and nothing after them is read: malformed JSON, a JSON value that is not a
JSON-RPC message, a JSON-RPC batch, and a record longer than 1048576 bytes. A
request that reuses the id of a call still in flight gets no answer.

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
    "allow_elevated_invites": false,
    "node_name": "tslink-mcp"
  }
}
```

An empty `allow` list, or one containing only blank entries, makes `serve`
refuse to start and never grants open access. Every rejection returns the
same `403` JSON-RPC `forbidden` body. A peer that passes `mcp.allow` can
register and delete services, publish them to the public internet through
Funnel, and send or revoke real Tailscale invitations. Elevated invitations
also require `mcp.allow_elevated_invites`.

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
| `unshare` | Remove a service; delete its recorded tailnet device by exact NodeID when credentials and ownership proof permit, then remove its local node state when safe. Confirm with the user first. Its result matches `tslink remove --json`, including `node_state_kept_reason` when applicable. |
| `status` | Report local TSLink daemon, stored-credential, node-authorization, and service-count state. |
| `url` | Return one registered service's exact runtime URL. |
| `tags_list` | List every registered service with the ACL tags recorded for it locally. |
| `tags_set` | Replace one service's ACL tags with a single tag in the local registry. |
| `access_explain` | Explain what TSLink knows locally about one service's access: its exposure, whether an allow-list is enforced, and which layers this answer does not cover. |
| `doctor` | Run local TSLink diagnostics and return findings by severity. |
| `logs` | Read recent lines from the local TSLink daemon log. |
| `invite_user` | Sends a real Tailscale invitation. Confirm the address and role with the user. A role other than `member` needs `mcp.allow_elevated_invites: true` in `config.json`. |
| `invite_device` | Sends a real device-sharing invitation outside the tailnet. Confirm the service and address with the user. `allow_exit_node: true` needs `mcp.allow_elevated_invites: true` in `config.json`. |
| `invite_list` | List open tailnet user invitations and TSLink-owned device invitations. |
| `invite_revoke` | Cancels a real outstanding invitation on the user's tailnet, so confirm with the user before calling this. |
| `invite_resend` | Sends another real invitation email to the original recipient, so confirm with the user before calling this. |
| `template_list` | List the built-in service templates. |
| `template_plan` | Preview which services a built-in template would create, without writing the registry. |
| `template_apply` | Write a built-in template's missing services into the local registry. |

Daemon lifecycle, install, login and logout, and configuration stay CLI-only.

`mcp.allow_elevated_invites` is off by default; CLI invitations never need it.
Refused calls return `isError: true` with a JSON failure object (`code`,
`message`, `next`, optional `data`) as one text item and no
`structuredContent`. Invalid arguments return `usage_error` tool results.
Every tool declares `readOnlyHint`, `destructiveHint`, `idempotentHint`, and
`openWorldHint`. `share`, `add`, and `template_apply` add `daemon_installed`
(`manager`, `path`, `undo`) to a successful result if they installed the daemon;
later failures carry it in `data`. The read-only `list`, `status`, `url`, and
`doctor` tools describe missing credential metadata without recording it or
creating `credential-meta.json` or `credentials.lock`.
MCP `logs` `since` and `url` `wait` accept Go durations plus `d` for days;
MCP `funnel_ttl` uses the [unified duration grammar](durations.md): relative or `until <date/time>`, min 1h, default max 7d; public `never` is refused. Presets: 1h, 8h, 24h, 3d, 7d. MCP `extend` changes a person grant or Funnel TTL and requires `regrant` for expired grants.

## Redaction is not a boundary

The `logs` tool sanitizes what it returns, but the same log file is emitted
verbatim by the `tslink logs` CLI. Treat the redaction as narrowing what
incidentally reaches a model's context, never as a way to keep a credential
from an agent that has a shell. See [SECURITY.md](../SECURITY.md) for the
shapes it does not cover.
