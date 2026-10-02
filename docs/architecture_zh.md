# 架构

## 工作原理

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│  你的机器   │         │   Tailscale 网络     │         │   你的手机   │
│             │         │   (WireGuard 网状)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  浏览器      │
│  :3000      │  tsnet  │  WireGuard 加密       │  HTTPS │              │
│  :5432      │  节点   │  加密 tailnet 路径     │  +TLS  │              │
│  ~/Documents│ (1/服务)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink 为每个注册的服务创建一个专用的 [tsnet](https://tailscale.com/kb/1244/tsnet) 节点，服务端无需安装 Tailscale 客户端。每个服务作为独立设备加入 tailnet（如 `myapp`、`docs`、`mydb`）。proxy/file 服务使用 Tailscale HTTPS listener；raw TCP 服务使用私有 tailnet transport，并把字节代理到配置的目标。

**关键架构决策：**
- **Per-service 嵌入式节点**：每个服务获得独立的 tailnet 身份和主机名；proxy/file 服务还获得 Tailscale HTTPS listener 语义
- **身份感知代理**：配置 `--allow` 后，HTTP 代理/文件请求必须通过 WhoIs 并匹配名单，否则在到达后端前返回 403。未配置名单时，tailnet 策略决定能否连接，WhoIs 身份头和日志属于尽力提供的信息。即使 WhoIs 失败，代理仍会移除客户端传来的 Tailscale 身份头及下划线变体。raw TCP 不经过 HTTP 身份层；公网 Funnel 调用方不视为经过 TSLink 强制认证的 Tailscale 用户
- **安全凭证管理**：系统钥匙串存储；macOS/Linux 的受限权限文件回退仅在证明残留钥匙串凭证不存在或已清除后成功
- **基于文件的注册表**：服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载**：注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期**：通过进程身份检查管理守护进程，并按平台明确停止行为
- **结构化日志**：slog 诊断日志与有界异步本地访问历史，覆盖 HTTP/文件请求和 TCP 连接，见 [access-log_zh.md](access-log_zh.md)
