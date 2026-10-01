# 面向 Agent

## 面向 agent 的 MCP server

`tslink mcp` 通过 stdio 运行本地 MCP server。MCP 进程本身不打开网络
listener；调用其中的 `share` tool 可能启动独立的 TSLink daemon 及所请求的
tsnet service。它暴露 19 个 tools，覆盖 CLI 的 per-service 能力面：`share`、`add`、
`list`、`unshare`、`status`、`url`、`tags_list`、`tags_set`、`access_explain`、
`doctor`、`logs`、`invite_user`、`invite_device`、`invite_list`、`invite_revoke`、
`invite_resend`、`template_list`、`template_plan` 和 `template_apply`。daemon
生命周期、install、login/logout 和配置仍只通过 CLI 操作。可让 MCP client
启动已安装的 `tslink` 命令，并传入唯一参数 `mcp`：

```json
{
  "command": "tslink",
  "args": ["mcp"]
}
```

`share` tool 接受与 CLI 相同的 path/port/host:port target。需要授权时，它会
把 `{"status":"needs_login","auth_url":"..."}` 作为正常 tool result 返回，agent
可以打开该 URL 后重试。MCP 永远不会返回 credential 值。同一组 tools 也可以提供给
tailnet 内的其它机器，见[远程 MCP 控制面](remote-mcp_zh.md)。

MCP tool 拒绝调用时返回 `isError: true`，在一个 text item 中放入 JSON failure
object（`code`、`message`、`next` 和可选的 `data`），不包含 `structuredContent`。
参数不符合 tool schema 时，返回 `usage_error` tool result。每个 tool 都声明
`readOnlyHint`、`destructiveHint`、`idempotentHint` 和 `openWorldHint`。
MCP `unshare` 返回与 `tslink remove --json` 相同的结果，适用时包含
`node_state_kept_reason`。MCP `invite_user` 使用 `member` 以外的 role，或
`invite_device` 设置 `allow_exit_node: true`，需要所有者开启
`mcp.allow_elevated_invites`。
