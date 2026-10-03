# Agents

## MCP server for agents

`tslink mcp` runs a local MCP server over stdio. The MCP process itself opens no
network listener; invoking its `share` tool may start the separate TSLink daemon
and the requested tsnet service. Read `tools/list` for the current tool registry,
covering the per-service surface of the CLI: `share`, `add`, `list`, `unshare`, `status`, `url`,
`tags_list`, `tags_set`, `access_explain`, `doctor`, `logs`, `invite_user`,
`invite_device`, `invite_list`, `invite_revoke`, `invite_resend`,
`template_list`, `template_plan`, `template_apply`, `people_add`, `people_update`,
`people_list`, `people_remove`, `recipe_list`, `apps_detect`, `recipe_plan`, and
`recipe_apply`. Daemon lifecycle,
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
authorization is pending, its normal successful result includes
`{"name":"preview-2","status":"needs_login","auth_url":"..."}`. Every
successful share returns its actually registered `name`. After human enrollment,
pass that name to `url`; use it for `unshare` to undo. Never guess or fall back to
the requested name. Credential
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
