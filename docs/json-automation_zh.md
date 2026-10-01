# JSON 自动化

## JSON 自动化

除 stdio 的 `tslink mcp` 外，每个命令都接受 `--json`，并向 stdout 写出同一套版本化 envelope，owner 侧自动化就是同一个 CLI 加一个 flag。TSLink 没有 REST server、dashboard 或成员可见的服务目录；JSON 视图经过脱敏，不直接输出原始注册表记录。

```bash
# 列出本机已注册的服务
tslink list --json

# 添加服务
tslink add myapp --proxy localhost:3000 --json

# 删除服务
tslink remove myapp --json

# 查看状态
tslink status --json

# 查看 owner-only endpoint / exposure 概览
tslink status --urls --json

# 运行诊断，可能补写凭证 metadata（探测非 loopback 目标需传 --probe-external）
tslink doctor --json

# 解释一个服务的本地访问模型
tslink access explain myapp --json

# 预览/应用内置模板
tslink template list --json
tslink template apply local-web --dry-run --json
tslink template apply local-web --yes --json
```

同样的操作也可以由 MCP client 通过 `tslink mcp`（stdio）和下文的远程控制面完成；`tslink manifest --json` 会打印每个命令、flag、退出码和 error code 的机器可读描述。

所有 `--json` 输出使用同一套版本化 envelope，`command` 字段标明产生它的命令：

```json
{
  "type": "tslink.result",
  "ok": true,
  "schema_version": 1,
  "command": "list",
  "code": 0,
  "data": {
    "schema_version": 1,
    "services": [],
    "count": 0
  }
}
```

失败时包含稳定的机器 error code 和人类可读文本；有可用的恢复命令时 `error.next` 会列出：

```json
{
  "type": "tslink.result",
  "ok": false,
  "schema_version": 1,
  "command": "list",
  "code": 2,
  "error": {
    "code": "usage_error",
    "message": "--tailnet conflicts with --verbose; --verbose filters this machine's registered services, while --tailnet reports tailnet devices",
    "next": ["tslink --help"]
  }
}
```

macOS 上，`launchctl_domain_unavailable` 是 TSLink 无法证明 install / uninstall handoff 安全时的刻意 exit-1 拒绝。其 failure `data` 包含 `unavailable_domain`、`force_available`、精确的 `force_command` 和 `force_risk`；agent 无需解析 `error.message` 就能拿到恢复契约。

只有 service node 完成授权后，`status` 才设置 `authenticated`，并把
`auth_status` 设为 `authenticated`；`credential_stored` 单独表示已有存储凭证。
`daemon_state` 可为 `running`、`absent` 或 `unknown`；缺少 PID 文件表示
`absent`。MCP `status` 和事件流也提供该字段。`doctor --json` 在 envelope 的
`code` 中返回诊断退出码 0、64 或 65；`enrollment_required` 的退出码为 3。

MCP `logs` 的 `since`、MCP `url` 的 `wait`、`login --expires-in` 和
`mcp.events_keepalive` 接受 Go duration 语法及表示天数的 `d`，各自仍有范围限制。
MCP `funnel_ttl` 和 CLI `--funnel-ttl` 只接受 `1h`、`8h`、`24h`、`72h`、`7d`
或 `never`，会拒绝 `168h`。CLI `add --wait`、`share --wait` 和 `url --wait`
接受 Go duration 语法，也接受用 `d` 表示的分数天数及组合时长。

`--json` 只改变输出格式。`tslink add --json` 与人类路径使用同一套安全护栏：Funnel 服务必须传 `--public`；TCP 服务会拒绝 `--allow`，因为 TSLink 不会对原始 TCP 字节流应用 HTTP 身份检查。
