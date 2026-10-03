# Agents

## MCP server for agents

`tslink mcp` runs a local MCP server over stdio. The MCP process itself opens no
network listener; invoking its `share` tool may start the separate TSLink daemon
and the requested tsnet service. The owner tool registry covers the per-service
surface of the CLI: `share`, `add`, `list`, `unshare`, `status`, `url`,
`tags_list`, `tags_set`, `access_explain`, `doctor`, `logs`, `invite_user`,
`invite_device`, `invite_list`, `invite_revoke`, `invite_resend`,
`template_list`, `template_plan`, and `template_apply`, plus people, recipe,
scoped per-app operations, health and audit tools. Run `tools/list` for the
current scope. Daemon lifecycle,
install, login/logout, and configuration stay CLI-only. Configure an
MCP client to launch the installed `tslink` command with the single argument
`mcp`:

```json
{
  "command": "tslink",
  "args": ["mcp"]
}
```

The `share` tool accepts the same path/port/host:port targets as the CLI. When
authorization is pending it returns `{"status":"needs_login","auth_url":"..."}`
as a normal tool result so an agent can open the URL and retry. Credential
values are never returned through MCP. The same tools can also be served to
other machines on your tailnet; see [Remote MCP Control Plane](remote-mcp.md).

A refused tool call has `isError: true` and one text item containing the JSON
failure object (`code`, `message`, `next`, and optional `data`), without
`structuredContent`. Arguments outside a tool's schema return a `usage_error`
tool result. Every tool declares `readOnlyHint`, `destructiveHint`,
`idempotentHint`, and `openWorldHint`. MCP `unshare` returns the same result as
`tslink remove --json`, including `node_state_kept_reason` when applicable.
MCP `invite_user` roles other than `member` and `invite_device` with
`allow_exit_node: true` need the owner's `mcp.allow_elevated_invites` opt-in.

## Delegate with a reduced MCP session

The local OS user is owner by default. Configure an untrusted local agent with
`["mcp","--scope","viewer","--apps","photos"]`; tool arguments cannot widen
this scope. Restrict shell/filesystem access separately. Remote agents receive
WhoIs-bound roles from `mcp.bindings`; legacy `mcp.allow` stays owner. Use
`people_grant`/`people_revoke` for one app and preserve the person's other grants.
Reduced clients get filtered data and tools, with `mcp_scope_denied` for app or
time refusals. Owner reads mutation receipts via `mcp_audit` or
`tslink mcp-audit --json`. See [MCP scopes](mcp-scopes.md) for all roles and a
recipe giving your family's agent read-only access.
