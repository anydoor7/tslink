# 远程 MCP

## 远程 MCP 控制面

`tslink serve --mcp` 在一个专用 tsnet 节点上，通过 HTTPS 在 `<node>.<tailnet>.ts.net/mcp` 提供与 `tslink mcp` 相同的 MCP 工具注册表，按调用者作用域过滤。它是给 MCP client 用的 MCP endpoint，没有可供浏览器打开的页面。节点默认主机名为 `tslink-mcp`；其 tsnet 状态位于 `~/.config/tslink/mcp-node/`，与服务节点并列存放，而非混在其中。

控制面默认关闭。用 `--mcp` flag 或 `config.json` 中的 `mcp.enabled: true` 开启，二者任一生效。`mcp.allow`（旧 owner 条目）和/或 `mcp.bindings`（受限身份）是必填项，且只存在于 `config.json` 中，因为它是整个功能的安全边界（`tslink config set` 只管理 `control-url`，所以要直接编辑该文件）：

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

`mcp.allow` 中的 `tag:` 条目会授权所有带该 tag 的机器，包括 service node。优先填写具体的登录邮箱；如需用 tag，请建立专用 tag 并控制哪些机器带有它。

`mcp.allow_elevated_invites` 也是 `config.json` 的 key，默认关闭。设为 `true`
后，MCP client 可以邀请用户担任 `member` 以外的 role，或通过
`allow_exit_node: true` 分享设备。CLI 邀请命令无需此设置。
`mcp.events_keepalive` 接受 Go duration 语法和表示天数的 `d`，范围为 5 秒至 5 分钟。

`config.json` 按严格模式读取：TSLink 不认识的 key（包括拼写错误）会让写配置的命令以 `config_load_failed` 拒绝，`tslink doctor` 会报出来，`serve` 则回到默认控制服务器，并且不开控制面。

| 事实 | 细节 |
|---|---|
| 默认 | 关闭。没有 `--mcp` 或 `mcp.enabled: true` 时，`serve` 不打开控制面 listener，也不创建控制面节点 |
| 授权 | WhoIs login/tag 匹配 `mcp.allow` owner 条目或严格的 `mcp.bindings`。空授权拒绝启动；身份拒绝统一返回 403，带 `error.data.code: mcp_scope_denied`，见 [MCP 作用域](mcp-scopes_zh.md) |
| 可达范围 | 唯一的 listener 是控制面自己 tsnet 节点上的 `ListenTLS`。它永远不通过 Funnel 发布，永远不绑定主机网络接口或 `0.0.0.0` |
| 节点 | 专用节点，不与任何服务共用。它不是 registry 服务，因此不出现在 `tslink list` 中，也没有任何代码路径能给它加 `--funnel` |
| 生命周期 | 取决于 `serve` 的登录方式。已存凭证（`tslink login`）路径上节点是 ephemeral 的：派生出的 auth key 携带 ephemeral capability，tsnet 也以 ephemeral 标记登录，因此守护进程正常停止时会先登出节点，Tailscale 在几秒内把它从 tailnet 移除；关闭 `--mcp` 后没有需要手动删除的设备。如果守护进程崩溃，节点会留到 Tailscale 的 ephemeral 垃圾回收把它回收为止（Tailscale KB 写的是通常在最后活动后 30 到 60 分钟；这个数字来自 Tailscale KB，不是 TSLink 的实测）。零凭证（交互式浏览器登录）路径上节点是持久的、user-owned 的：一次浏览器授权在守护进程重启后仍然有效，关闭 `--mcp` 后 `tslink-mcp` 这台设备会留在 tailnet 里，需要你在 Tailscale admin console 手动删除 |
| 权限 | Owner 保留所有工具，包括删除、Funnel 和邀请；受限角色只获得显式工具及 app。高权限邀请仍需开启 `mcp.allow_elevated_invites`。委派使用受限 binding |
| Origin | 带 `Origin` 头的请求必须与 endpoint 自身的 `<node>.<tailnet>.ts.net` origin 完全一致，遵循 MCP Streamable HTTP 传输规范；其它情况在授权之前即返回 `403`。不带 `Origin` 的请求（命令行 MCP client 就是这样）直接放行 |
| 传输 | 无状态 Streamable HTTP；单个请求体上限 1 MiB，与 stdio 传输每条记录的上限一致 |

**哪些 client 能连上。** 只有运行在你 tailnet 内某台机器上的 MCP client 能连接，例如笔记本或服务器上的 Claude Code。Claude Desktop 和 claude.ai 连不上：它们的 remote MCP 连接从 Anthropic 云端发起，不是从你的设备发起，因此到不了私有 tailnet 地址。这两者与 tslink 在同一台机器上时，请用 stdio 的 `tslink mcp`。

### `tslink mcp` 与 `tslink serve --mcp` 对照

| | `tslink mcp` | `tslink serve --mcp` |
|---|---|---|
| 传输 | stdio，newline-delimited JSON-RPC | Streamable HTTP，地址 `<node>.<tailnet>.ts.net/mcp` |
| 网络 listener | 无 | 专用 tsnet 节点上的 TLS listener，仅限 tailnet |
| 授权 | 启动 OS 用户默认 owner，可显式用 `--scope`/`--apps` 降权 | WhoIs login/tag 在 `mcp.allow` 或 `mcp.bindings` 中，必填 |
| 配置 | 可选的作用域启动参数 | `--mcp` 或 `mcp.enabled`，加上 `config.json` 中的身份绑定 |
| Tools | 同一 tool registry；默认 owner，可用启动参数缩减 | 同一 tool registry，按 WhoIs binding 过滤 |
| 典型 client | 本机上的 MCP client | tailnet 内另一台机器上的 MCP client |

## 委派与迁移

旧 `mcp.allow` 条目全部保留 owner 权限。要降权，将 principal 移到 `mcp.bindings`；
两处重复 principal 会拒绝。使用 `viewer`、`app-operator` 或 `people-manager`，
显式列出 app 和合适的时长限制。修改配置后重启 daemon。受限 client 轮询工具；
全局 `/events` 仅限 owner，对受限角色返回 404。家人只读示例、固定期限、角色矩阵
和持久审计日志见 [MCP 作用域](mcp-scopes_zh.md)。
