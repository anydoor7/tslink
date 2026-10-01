# 跨机器操作

## 跨机器操作

`~/.config/tslink/registry.json` 中的注册表属于单台机器，而 tailnet 是共享的。下面两个只读能力让这条边界可见；[远程 MCP 控制面](remote-mcp_zh.md)则让 tailnet 内另一台机器上的 agent 能操作这台机器的安装。

### 查看 tailnet 中的全部 TSLink 设备

`tslink list` 读的是本机注册表。`tslink list --tailnet` 改为查询 Tailscale API，报告 tailnet 中每一台带 TSLink 标签的设备：本机注册的服务、其它机器注册的服务，以及孤儿节点。设备带有配置的默认标签（未修改时为 `tag:tsmain`）、`tag:tslink-funnel` 或任何其它 `tag:tslink-*` 标签时，即被视为 TSLink 设备。

```bash
tslink list --tailnet
tslink list --tailnet --json
```

每一行都标明它来自机器边界的哪一侧：

| `origin` | 含义 |
|---|---|
| `local_registry` | 本机 `registry.json` 中有一个主机名完全相同的服务 |
| `local_name_variant` | 该主机名是本机某个已注册服务的 `<service>-N` tsnet 冲突变体，这是本机留下孤儿节点的常见形态 |
| `unregistered` | 本机注册表对该主机名一无所知：它是另一台机器的服务，或是一个孤儿节点 |

人类可读输出以 `N of M TSLink-owned tailnet devices are not registered on this machine.` 结尾，JSON 负载在 `count` 之外还带 `registered_count` 与 `unregistered_count`。每个结果都带一个常量字段 `cleanup_authority`，因为这个视图暴露了一条真实的限制：`tslink cleanup` 只删除精确 NodeID 记录在本机 `node-ownership.json` 中的设备，所以本机注册表没有命名的设备，必须回到创建它的那台机器上清理。`--tailnet` 永远不输出 NodeID，也永远不删除任何东西。

`--tailnet` 需要已存储的 Tailscale API 凭证（`tskey-api-*` 访问令牌或 OAuth 客户端密钥）。没有凭证时它以 `auth_error`（退出码 3）失败，并在 `error.next` 中给出 bootstrap 指引；它不会返回空列表。它与 `--name`、`--type`、`--fields`、`--verbose` 互斥（退出码 2），因为那些 flag 过滤的是本机已注册服务，而 `--tailnet` 报告的是 tailnet 设备。

### 用 Tailscale SSH 远程执行 CLI

如果运行 TSLink 的机器开启了 Tailscale SSH，且 tailnet policy 中有一条允许你的 `ssh` 规则，那么 `tailscale ssh <host> tslink <command>` 就能从 tailnet 内任何设备操作那台机器上的安装，无需额外软件。这两个条件都是 Tailscale 层的配置：TSLink 不开启 Tailscale SSH，也不修改 policy，更不依赖二者。

为了让第一个条件可被发现，`tslink doctor` 从本机 `tailscaled` 读取 Tailscale SSH 的开启状态，并打印 `Tailscale SSH (this node): <state>`。JSON 负载带有 `tailscale_ssh.state` 与 `tailscale_ssh.acl_rule_required: true`，后者记录本地读取无法观测的第二个条件。

| 状态 | Finding code | 含义 |
|---|---|---|
| `enabled` | `tailscale_ssh_enabled` | 一旦 tailnet ACL 有 `ssh` 规则允许调用者，`tailscale ssh <this-host> tslink list --json` 即可用 |
| `disabled` | `tailscale_ssh_disabled` | 在这台机器上运行 `tailscale set --ssh` 并添加 ACL `ssh` 规则，才能使用远程路径 |
| `unknown` | `tailscale_ssh_unknown` | 一秒内无法读取本机 Tailscale client 状态；检查 `tailscale status` |

三种结果都是 informational，永远不会改变 doctor 的 status 或退出码。

设置 `TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1` 可以跳过这次本地读取，例如测试套件在开发者的机器上运行编译出的二进制。此时状态为 `unknown`，`tailscale_ssh_unknown` finding 会注明跳过了这项检查。

