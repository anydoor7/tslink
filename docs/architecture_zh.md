# 架构

TSLink 在发布服务的机器上运行一个共享守护进程，为每个已注册服务创建独立的
[tsnet](https://tailscale.com/kb/1244/tsnet) 节点。每个节点有自己的网络身份和主机名，
发布端无需另行安装 Tailscale 客户端。

## 配置路径与访问路径

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/system-architecture-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/system-architecture-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/system-architecture-dark.svg">
  <img src="assets/system-architecture-light.svg" alt="TSLink 架构图：虚线表示 CLI 或 MCP 修改 registry.json，再由共享守护进程中的监听器同步节点；实线表示获准的 tailnet 设备经独立的应用、文件、数据库或模型节点访问本地目标。HTTP 和文件使用 HTTPS，原始 TCP 使用私有 tailnet 传输。" width="960">
</picture>

虚线表示配置变更，实线表示请求和响应。图中展示同一 tailnet 内的普通私有服务，
目标均为本机示例；未展示公网 Funnel 路径。

1. **写入配置。** CLI 与 MCP 的服务管理操作把设置保存到
   `~/.config/tslink/registry.json`，重启后仍保留。
2. **同步节点。** 共享守护进程监听注册表，更新其中的嵌入式节点，无需重启守护进程。
   每个服务使用独立节点身份；新节点需要完成 Tailscale 授权加入流程。
3. **访问服务。** 获准的 tailnet 设备连接选定节点：HTTP 代理转发到已运行的应用或
   模型 API，文件服务直接读取配置的文件，TCP 服务把字节转发到目标。

节点共享进程和发布主机。独立网络身份可以适用不同的 tailnet 规则，但不隔离主机进程或数据。
模型接口也是普通的 HTTP 代理目标；模型推理和文档处理由应用负责，见[本地 AI](local-ai_zh.md)。

## 访问与身份

- **Tailnet 策略**决定哪些设备能连接各节点。HTTP 代理与文件节点使用 Tailscale
  HTTPS listener；原始 TCP 使用私有 tailnet 传输，不是 HTTPS listener。
- **可选的 HTTP 允许名单：**配置 `--allow` 后，WhoIs 必须成功且调用者必须匹配名单，
  否则在后端或文件处理器执行前返回 403。未配置名单时，tailnet 策略决定能否连接，
  WhoIs 身份头和日志属于尽力提供的信息。
- **身份头处理：**即使 WhoIs 失败，代理仍移除客户端传来的 Tailscale 身份头及下划线变体。
  公网 Funnel 调用者不视为经过 TSLink 强制认证的 Tailscale 用户。
- **原始 TCP**不经过 HTTP WhoIs 或允许名单层，需要使用 tailnet 策略及目标应用自身的认证。

MCP 是管理入口，普通服务请求不需要经过 MCP。本地 MCP 使用 stdio；可选的
[远程 MCP](remote-mcp_zh.md) 使用独立节点，并要求明确配置调用者权限。

## 运行细节

- 凭证保存在系统钥匙串中。macOS/Linux 仅在证明残留钥匙串凭证不存在或已清除后，
  才允许回退到权限受限的文件。
- 守护进程通过 PID 与进程身份检查管理生命周期，停止行为按平台区分，见
  [守护进程生命周期](daemon-lifecycle_zh.md)。
- 使用基于 `slog` 的结构化日志，包括访问日志。
