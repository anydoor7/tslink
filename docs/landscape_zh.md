# 方案背景

## 为什么需要 TSLink？

### 你省掉的那些步骤

给服务单独的主机名是 Tailscale 的原生能力。[Tailscale Services](https://tailscale.com/docs/features/tailscale-services) [自 2026 年 2 月起正式可用](https://tailscale.com/blog/services-ga)，`tailscale serve --service=svc:web-server --https=443 127.0.0.1:8080` 在普通 `tailscaled` 上就能得到 `web-server.<tailnet>.ts.net`。TSLink 建立在 [tsnet](https://tailscale.com/docs/features/tsnet) 之上，使用其文档介绍的集成方式独立开发。

TSLink 改变的是谁能配、配多久。原生路径要求：

- **管理员权限**。定义一个 Service 需要 Owner、Admin 或 Network admin 账号权限。
- **宿主设备是 tag 身份**。官方原文：用户账号认证的设备不能作为 Service host。
- **一次审批**。宿主生效前需要 Admin、Network admin 或 Owner 批准。
- **一次 tailnet 全局策略修改**才能收窄访问范围，只能走管理后台、GitOps 或 API。编辑 ACL 没有 CLI 命令。
- **一个 Go 程序**，如果直接用 tsnet。它是个库，每个服务都要自己写、自己编译。

TSLink 这些都不要。`tslink add ollama --proxy localhost:11434` 指向的是一个已经在跑的进程，普通账号即可执行，`--allow you@example.com` 是同一条命令上的一个 flag，而不是对共享策略文件的一次改动。

这份访问名单在 HTTP 层生效，所以它与 tailnet ACL 是互补关系。tailnet 上任何能直接连到该端口的东西，仍然由你的 ACL 管辖。

### 更大的背景

端口转发、VPN、ngrok、Cloudflare Tunnel，每一种要么把服务放到公网上，要么让私有流量经过第三方；两者都避免的 VPN 则要承担自建运维的开销。

随着本地 AI 工作负载、自托管服务和个人基础设施的增长，个人开发者和小团队的安全需求与企业级工具之间的差距越来越大。大多数零信任工具面向的是拥有专业安全团队的大型企业。

**TSLink 为本地服务提供 per-service tailnet 身份。** 一条命令将你的机器变成 Tailscale-backed 网关。tailnet transport 遵循 Tailscale/WireGuard 语义；proxy/file 服务可以增加 HTTP identity 和 `--allow` 检查，raw TCP 保持私有字节流，不经过 TSLink HTTP middleware。

## TSLink 做什么

一条命令就能把任何本地服务暴露到你的私有 Tailscale 网络，Web 应用、API、文件目录、数据库都可以。proxy/file 服务使用 Tailscale HTTPS listener；raw TCP 服务使用私有 tailnet transport，不由 TSLink 终止 TLS。

```bash
tslink add myapp --proxy localhost:3000
# tailnet 上任何设备都能访问 https://myapp.<your-tailnet>.ts.net
```

### 功能特性

- **零配置**：无需端口转发、DNS 或证书管理
- **WireGuard tailnet 路径**：tailnet 设备间流量通过 Tailscale 使用 WireGuard；公网暴露必须显式启用 Funnel
- **Proxy/file HTTPS**：HTTP proxy 和 file 服务使用 Tailscale HTTPS listener；raw TCP 保持私有 tailnet 字节流
- **Per-service 身份**：每个服务获得独立的 tailnet 主机名和网络身份（proxy/file URL 形如 `<name>.<tailnet>.ts.net`）
- **热重载**：运行时添加或移除服务，更改立即生效
- **全平台支持**：支持 macOS、Linux 和 Windows
- **守护进程运行**：启动一次，后台运行，支持开机自启
- **TCP 代理**：暴露数据库、SSH、Redis 等非 HTTP 服务
- **HTTP 访问控制**：proxy 和 file 服务支持 `--allow user@example.com,tag:admin`
- **安全诊断**：`tslink doctor`、`tslink status --urls` 和 `tslink access explain` 明确展示本地证据和未知的外部策略层
- **面向 agent 的自动化**：除 stdio 的 `tslink mcp` 外，每个命令都接受 `--json` 并返回同一套版本化 envelope；`tslink mcp`（stdio）与 `tslink serve --mcp`（仅限 tailnet 的远程控制面）暴露同一组 MCP tools
- **个人模板**：预览并添加小型私有服务套件，不覆盖已有服务
- **Headscale 兼容路径**：通过 `--control-url` 支持高级/自托管控制服务器场景
- **Funnel 护栏**：公网暴露必须显式选择，并要求 `--public` 确认

