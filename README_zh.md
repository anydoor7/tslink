<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">你的本地服务，从任何设备安全访问。</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README.md">English</a>
</p>

---

## 为什么需要 TSLink

我们正在进入个人计算的新时代。

AI 代理已经在你的机器上运行——生成报告、处理数据、构建应用、提供本地工具服务。你的 Mac 或 PC 不再只是一个工作站，它正在成为你的**个人服务器**，一个智能与生产力的私有枢纽。

但问题是：**一切都被锁在你的机器里。**

想从手机查看 AI 生成的报告？想从沙发上的平板访问本地开发服务器？今天你的选择是：

- **端口转发** — 复杂、不安全、暴露你的家庭网络
- **ngrok / Cloudflare Tunnel** — 你的私有数据经过第三方服务器
- **VPN** — 笨重、缓慢、需要基础设施和维护

这些方案都不是为我们即将进入的世界设计的——一个每个人都有 AI 驱动的机器、不断产生有价值的私有内容、需要**从任何设备即时安全访问**的世界。

### 隐私不是可选项

你的 AI 产出——研究、代码、个人文档、商业数据——不应该经过任何第三方服务器。在数据泄露和监控日益加剧的时代，**设备之间最安全的路径是直连。**

### 国家与全球利益

随着 AI 融入日常工作，一个关键的基础设施缺口已经出现：**个人和组织如何将本地 AI 系统的产出安全地桥接到他们实际使用的设备上？**

这不仅仅是便利性问题。这是一个**安全问题**、一个**生产力问题**、一个**基础设施问题**，影响到：

- **个人开发者和研究者**——需要对本地服务的私密、零信任访问
- **中小企业和初创公司**——在没有企业 IT 预算的情况下加速 AI 采用
- **大型企业**——通过消除内部工具的公共暴露来减少攻击面
- **国家网络安全态势**——每一个留在公共互联网之外的服务，就少一个攻击目标

TSLink 直接解决了这个缺口。

## TSLink 做什么

TSLink 将你的机器变成一个安全网关。一条命令就能将任何本地服务——Web 应用、API、文件目录——暴露到你的私有 [Tailscale](https://tailscale.com) 网络上。从你的手机、平板或 tailnet 上的任何设备访问。

- **零配置** — 无需端口转发、DNS 或证书管理
- **端到端加密** — 通过 Tailscale 的 WireGuard 加密，你的数据永远不经过公共互联网
- **即时 TLS** — 自动 HTTPS，有效证书，无需设置
- **热重载** — 在 TSLink 运行时添加或移除服务，更改立即生效
- **守护进程运行** — 启动一次，后台运行，通过 macOS LaunchAgent 开机自启

## 快速开始

### 安装

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# 从源码构建
git clone https://github.com/monody0007/tslink.git
cd tslink && go install .
```

### 30 秒上手

```bash
# 1. 认证 Tailscale
tslink login

# 2. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 3. 启动网关
tslink serve --daemon

# 从任何设备打开 https://tslink.<your-tailnet>.ts.net/s/myapp
```

### 暴露文件目录

```bash
tslink add documents --dir ~/Documents
# 访问 https://tslink.<your-tailnet>.ts.net/f/documents/
```

## 命令

| 命令 | 描述 |
|------|------|
| `tslink login` | 认证你的 Tailscale 账户 |
| `tslink logout` | 清除认证状态 |
| `tslink add <name> --proxy host:port` | 暴露本地 Web 服务 |
| `tslink add <name> --dir /path` | 暴露文件目录 |
| `tslink remove <name>` | 移除已注册的服务 |
| `tslink list` | 列出所有已注册的服务 |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink install` | 开机自启（macOS LaunchAgent） |
| `tslink uninstall` | 移除自启 |

## 工作原理

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│   你的 Mac  │         │   Tailscale 网络     │         │   你的手机   │
│             │         │   (WireGuard 网状)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  浏览器      │
│  :3000      │  tsnet  │  端到端加密           │  HTTPS │              │
│  ~/Documents│  节点   │  不经过公共互联网      │  +TLS  │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink 在二进制文件中嵌入了一个 [tsnet](https://tailscale.com/kb/1244/tsnet) 节点——服务端无需安装 Tailscale 客户端。它作为名为 `tslink` 的设备加入你的 tailnet，自动获取 TLS 证书，并将请求反向代理到你的本地服务。

**关键架构决策：**
- **嵌入式节点** — 不依赖外部 Tailscale 守护进程
- **基于文件的注册表** — 服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载** — 注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期** — 信号处理实现干净的守护进程管理

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- Go 1.25+（如果从源码构建）

## 路线图

- [ ] Linux 和 Windows 支持
- [ ] Web 管理面板
- [ ] 多节点服务共享
- [ ] 自定义域名映射
- [ ] API 模式，支持编程集成
- [ ] 按服务的访问控制

## 贡献

欢迎贡献。请先开一个 issue 讨论你想要的更改。

## 许可证

本项目基于 [Apache License 2.0](./LICENSE) 许可。

```
Copyright 2026 Maintainer (monody0007)
```
