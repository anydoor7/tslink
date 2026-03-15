<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">面向私有网络的零信任服务网关。<br>一条命令安全暴露本地服务 — 不经公网，不过第三方。</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/actions"><img src="https://img.shields.io/github/actions/workflow/status/monody0007/tslink/ci.yml?branch=main&label=CI" alt="Build Status"></a>
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README.md">English</a> ·
  <a href="https://tslink.dev">官网</a> ·
  <a href="https://tslink.dev/docs">文档</a>
</p>

<!-- TODO: 添加终端录屏 / GIF 演示 -->
<!-- <p align="center"><img src="docs/demo.gif" alt="TSLink Demo" width="700"></p> -->

---

## 为什么需要 TSLink？

端口转发、VPN、ngrok、Cloudflare Tunnel — 这些传统方案不是为零信任时代设计的。它们要么把服务暴露到公网，要么让数据经过第三方，要么需要大量运维开销。

随着本地 AI 工作负载、自托管服务和个人基础设施的增长，个人开发者和小团队的安全需求与企业级工具之间的差距越来越大。美国联邦政府已认识到这一转变：[Executive Order 14028](https://www.whitehouse.gov/briefing-room/presidential-actions/2021/05/12/executive-order-on-improving-the-nations-cybersecurity/) 要求采用零信任架构，[NIST SP 800-207](https://csrc.nist.gov/publications/detail/sp/800-207/final) 定义了标准。但大多数零信任工具面向的是拥有专业安全团队的大型企业。

**TSLink 让每个人都能用上零信任网络。** 一条命令将你的机器变成安全网关。每个服务在你的 [Tailscale](https://tailscale.com) 网络上获得独立的加密身份 — 经过认证、端到端加密、对公网不可见。

## 安全模型

TSLink 在每一层实现零信任原则：

| 零信任原则 | TSLink 实现 |
|-----------|------------|
| **永不信任，始终验证** | 每个请求通过 Tailscale WhoIs 认证 — 身份头（`X-Tailscale-User-Login`、`X-Tailscale-User-Name`）注入每个代理请求。入站身份头被剥离以防伪造。 |
| **最小权限访问** | 通过 `--allow` 实现 per-service ACL，限制特定用户或标签访问。每个服务以独立身份运行。 |
| **假设已被攻破** | 每个连接都有端到端 WireGuard 加密。即使本地网络被攻破，设备间流量仍然加密。 |
| **微分段** | 每个服务作为隔离的 tsnet 节点运行，拥有独立的主机名、TLS 证书和网络身份。攻破一个服务不会影响其他服务。 |
| **消除隐式信任** | 默认不暴露任何服务到公网。凭证存储在系统钥匙串（macOS Keychain / Linux secret service）中，不以明文存储。认证密钥动态派生，从不持久化。 |

## TSLink 做什么

一条命令将任何本地服务 — Web 应用、API、文件目录、数据库 — 暴露到你的私有 Tailscale 网络，自动配置 TLS。

```bash
tslink add myapp --proxy localhost:3000
tslink serve --daemon
# → https://myapp.<your-tailnet>.ts.net — 从 tailnet 上任何设备访问
```

### 功能特性

- **零配置** — 无需端口转发、DNS 或证书管理
- **端到端加密** — 通过 Tailscale 的 WireGuard 加密，数据永远不经过公网
- **即时 TLS** — 自动 HTTPS，有效证书，无需设置
- **Per-service 隔离** — 每个服务获得独立的 tailnet 主机名和身份（`https://<name>.<tailnet>.ts.net`）
- **热重载** — 运行时添加或移除服务，更改立即生效
- **全平台支持** — 支持 macOS、Linux 和 Windows
- **守护进程运行** — 启动一次，后台运行，支持开机自启
- **TCP 代理** — 暴露数据库、SSH、Redis 等非 HTTP 服务
- **访问控制** — 基于用户/标签的 per-service ACL（`--allow user@example.com,tag:admin`）
- **中间件** — 内置限流、Basic Auth、IP 白名单、CORS
- **Prometheus 指标** — 请求计数、延迟直方图、活跃连接数
- **Docker 发现** — 通过标签自动注册容器（`tslink.enable=true`）
- **API 模式** — JSON-over-stdin/stdout，供 AI 代理和脚本程序化控制
- **Headscale 兼容** — 通过 `--control-url` 支持自托管控制服务器
- **Funnel** — 可选通过 Tailscale Funnel 暴露到公网

## 快速开始

### 安装

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# 从源码构建（任何平台）
go install github.com/monody0007/tslink@latest
```

### 30 秒上手

```bash
# 1. 认证 Tailscale（浏览器登录 + API 密钥）
tslink login

# 2. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 3. 启动网关
tslink serve --daemon

# 从任何设备访问 https://myapp.<your-tailnet>.ts.net
```

TSLink 只需要一个密钥 — 你的 [Tailscale API 访问令牌](https://login.tailscale.com/admin/settings/keys)。认证密钥自动派生。API 密钥存储在系统钥匙串（macOS Keychain）中，不以明文保存。

### 更多示例

```bash
# 暴露文件目录
tslink add documents --dir ~/Documents

# 通过 TCP 代理暴露数据库
tslink add mydb --tcp localhost:5432

# 临时节点（停止后自动从 tailnet 移除）
tslink add demo --proxy localhost:8080 --ephemeral

# 基于身份的访问控制
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# 通过 Tailscale Funnel 公开暴露
tslink add public --proxy localhost:3000 --funnel

# ACL 标签
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

## 命令

| 命令 | 描述 |
|------|------|
| `tslink login` | 认证你的 Tailscale 账户 |
| `tslink logout` | 清除认证状态 |
| `tslink add <name> --proxy host:port` | 暴露本地 Web 服务 |
| `tslink add <name> --dir /path` | 暴露文件目录 |
| `tslink add <name> --tcp host:port` | 暴露 TCP 服务（数据库、SSH 等） |
| `tslink remove <name>` | 移除已注册的服务（+ 自动清理 tailnet 设备） |
| `tslink list` | 列出所有已注册的服务 |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink api` | JSON-over-stdin/stdout 模式，用于程序化控制 |
| `tslink install` | 开机自启（macOS LaunchAgent / Linux systemd / Windows 启动文件夹） |
| `tslink uninstall` | 移除自启 |

### add 命令标志

| 标志 | 描述 |
|------|------|
| `--proxy host:port` | 反向代理到本地 HTTP 服务 |
| `--dir /path` | 文件目录服务 |
| `--tcp host:port` | 原始 TCP 转发 |
| `--ephemeral` | 临时节点，停止后自动从 tailnet 移除 |
| `--tags tag:a,tag:b` | ACL 标签，用于 Tailscale 网络策略 |
| `--allow user@,tag:x` | Per-service 访问控制（逗号分隔） |
| `--funnel` | 通过 Tailscale Funnel 暴露到公网（仅限 proxy） |
| `--domain example.com` | 自定义域名映射（仅限 proxy） |

## 工作原理

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│   你的 Mac  │         │   Tailscale 网络     │         │   你的手机   │
│             │         │   (WireGuard 网状)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  浏览器      │
│  :3000      │  tsnet  │  端到端加密           │  HTTPS │              │
│  :5432      │  节点   │  不经过公共互联网      │  +TLS  │              │
│  ~/Documents│ (1/服务)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink 为每个注册的服务创建一个专用的 [tsnet](https://tailscale.com/kb/1244/tsnet) 节点 — 服务端无需安装 Tailscale 客户端。每个服务作为独立设备加入 tailnet（如 `myapp`、`docs`、`mydb`），自动获取 TLS 证书，并将请求代理到你的本地服务。

**关键架构决策：**
- **Per-service 嵌入式节点** — 每个服务获得独立的 tailnet 身份、主机名和 TLS 证书（微分段）
- **身份感知代理** — 每个请求进行 WhoIs 验证，注入身份头并防止伪造
- **安全凭证管理** — 系统钥匙串存储 + 动态认证密钥派生（无密钥文件存储）
- **基于文件的注册表** — 服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载** — 注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期** — 信号处理实现干净的守护进程管理
- **中间件管道** — per-service 限流、Basic Auth、IP 白名单、CORS
- **结构化日志** — 基于 slog 的结构化日志 + 访问日志
- **Prometheus 指标** — `tslink_requests_total`、`tslink_request_duration_seconds`、`tslink_active_connections`
- **Docker 发现** — 通过 `tslink.enable=true` 标签自动注册容器

## API 模式

TSLink 提供 JSON-over-stdin/stdout API 模式，专为 AI 代理、脚本和 CI/CD 流水线设计。

```bash
# 列出服务
echo '{"action":"list"}' | tslink api

# 添加服务
echo '{"action":"add","name":"myapp","type":"proxy","target":"localhost:3000"}' | tslink api

# 删除服务
echo '{"action":"remove","name":"myapp"}' | tslink api

# 查看状态
echo '{"action":"status"}' | tslink api
```

## Docker 自动发现

TSLink 可以通过标签自动发现和注册 Docker 容器：

```yaml
services:
  webapp:
    image: nginx
    labels:
      tslink.enable: "true"
      tslink.name: "webapp"
      tslink.type: "proxy"
      tslink.port: "8080"
```

容器启动时自动注册，停止时自动注销。

## 中间件

每个服务可以通过 `registry.json` 配置中间件：

```json
{
  "middleware": {
    "rate_limit": 10.0,
    "basic_auth": "admin:secret",
    "ip_allow_list": ["100.64.0.1/16"],
    "cors_origins": ["https://frontend.example.com"]
  }
}
```

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- Go 1.25+（如果从源码构建）

## 平台支持

| 平台 | 守护进程 | 自动启动 |
|------|---------|---------|
| macOS | `--daemon` | LaunchAgent |
| Linux | `--daemon` | systemd user service |
| Windows | `--daemon` | 启动文件夹 |

## 路线图

- [x] OAuth 长期凭证（`tskey-client-*` 支持）
- [x] Let's Encrypt 集成自定义域名证书（`--domain` + `--acme-email`）
- [x] 可从 tailnet 访问的 Web 管理面板（管理 API + HTML 仪表板）
- [ ] Docker 镜像（`ghcr.io/monody0007/tslink`）
- [ ] Headscale 端到端测试
- [ ] Web 管理面板增强

## 贡献

欢迎贡献！请参阅 [CONTRIBUTING.md](./CONTRIBUTING.md) 了解指南。

## 安全

安全相关事宜请参阅 [SECURITY.md](./SECURITY.md)。

## 许可证

本项目基于 [Apache License 2.0](./LICENSE) 许可。

```
Copyright 2026 Maintainer (monody0007)
```
