<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">面向本地服务的私有 Tailscale 网关。<br>一条命令让每个 HTTP、文件或 TCP 服务获得独立 tailnet 身份。</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/actions"><img src="https://img.shields.io/github/actions/workflow/status/monody0007/tslink/ci.yml?branch=main&label=CI" alt="Build Status"></a>
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.26.3%2B-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README.md">English</a> ·
  <a href="https://github.com/monody0007/tslink">GitHub</a>
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
| **永不信任，始终验证** | 每个请求通过 Tailscale WhoIs 认证 — 身份头（`X-Tailscale-User-Login`、`X-Tailscale-User-Name`、`X-Tailscale-User-Picture`、`X-Tailscale-Node`）注入每个代理请求。入站身份头被剥离以防伪造。 |
| **HTTP 最小权限访问** | `--allow` 限制 proxy 和 file 服务的访问用户或标签。TCP 服务依赖 Tailscale 网络 ACL 和标签。 |
| **假设已被攻破** | 每个连接都有端到端 WireGuard 加密。即使本地网络被攻破，设备间流量仍然加密。 |
| **微分段** | 每个服务作为隔离的 tsnet 节点运行，拥有独立的主机名、TLS 证书和网络身份。攻破一个服务不会影响其他服务。 |
| **消除隐式信任** | 默认不暴露任何服务到公网。凭证优先存储在系统钥匙串中；headless 环境可回退到受限权限文件。认证密钥动态派生，从不持久化。 |

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
- **HTTP 访问控制** — proxy 和 file 服务支持 `--allow user@example.com,tag:admin`
- **最小 API 模式** — JSON-over-stdin/stdout，支持 list/add/remove/status 自动化
- **Headscale 兼容** — 通过 `--control-url` 支持自托管控制服务器
- **Funnel** — 可选通过 Tailscale Funnel 暴露到公网

### 发布状态

| 已交付 | Roadmap / experimental |
|---|---|
| Proxy、file、原始 TCP 服务 | Roadmap/experimental 中间件管道（限流、Basic Auth、IP 白名单、CORS） |
| 每服务一个嵌入式 `tsnet` 节点 | Roadmap/experimental Docker 标签自动发现 |
| 身份感知 HTTP 代理头 | Roadmap/experimental 管理面板和 REST API |
| proxy/file 的 HTTP `--allow` | Roadmap/experimental Prometheus `/metrics` 端点 |
| 注册表热重载 | Roadmap/experimental 自定义域名 / ACME 运行时 TLS |
| 守护进程和开机自启 | Roadmap/experimental Cluster / 多节点注册表同步 |
| 最小本地 `tslink api` | Roadmap/experimental 与 `tslink add` 全量标志对齐的 API |

## 快速开始

### 安装

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# 从源码构建（任何平台）
go install github.com/monody0007/tslink@latest
```

### 发布产物

GitHub Releases 发布以下可安装产物：

| 平台 | 产物 | 说明 |
|---|---|---|
| macOS | Homebrew formula 和 `tar.gz` 归档 | Homebrew formula 使用 GoReleaser `skip_upload: auto`，pre-release tag 可以跳过 tap upload 且不让发布失败。预发布验证优先使用归档产物。 |
| Linux | `.deb`、`.rpm` 和 `tar.gz` 归档 | 包内包含原生 `tslink` 二进制。安装后用 `tslink install` 注册 user service。 |
| Windows | `.zip` 归档 | Windows 当前是 archive-only 支持。尚未提供 MSI/MSIX/Winget 包或 Windows 代码签名安装器。解压后用 `tslink install` 注册 Startup 自启动。 |

发布产物是并列的 release assets，不是嵌入归档内部的文件。GoReleaser 会上传可安装归档/包、`checksums.txt`、归档对应的 CycloneDX SBOM sidecar，以及 `checksums.txt` 和 SBOM sidecar 的 keyless Sigstore bundle 签名。签名后的 `checksums.txt` 覆盖可安装产物和 SBOM sidecar。release workflow 还会为可安装产物和供应链 sidecar 发布 GitHub artifact attestations。

### 验证发布完整性

下面命令需要 `gh`、`cosign` 和 `shasum`。将 `<version>` 替换为 GitHub Release tag，将 `<artifact>` 替换为该 release 里的产物文件名。

```bash
repo="monody0007/tslink"
version="<version>"
artifact="<artifact>"

mkdir -p "tslink-$version-verify"
cd "tslink-$version-verify"

gh release download "$version" --repo "$repo" \
  --pattern "$artifact" \
  --pattern "checksums.txt" \
  --pattern "checksums.txt.sigstore.json"

expected="$(awk -v file="$artifact" '$2 == file {print $1}' checksums.txt)"
actual="$(shasum -a 256 "$artifact" | awk '{print $1}')"
test -n "$expected" && test "$actual" = "$expected"

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$artifact" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

归档 SBOM sidecar 是独立 release asset，需要单独验证：

```bash
sbom="$artifact.sbom.json"

gh release download "$version" --repo "$repo" \
  --pattern "$sbom" \
  --pattern "$sbom.sigstore.json"

expected="$(awk -v file="$sbom" '$2 == file {print $1}' checksums.txt)"
actual="$(shasum -a 256 "$sbom" | awk '{print $1}')"
test -n "$expected" && test "$actual" = "$expected"

cosign verify-blob "$sbom" \
  --bundle "$sbom.sigstore.json" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$sbom" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

### 30 秒上手

```bash
# 1. 认证 Tailscale（选择 API 访问令牌或 OAuth 客户端密钥）
tslink login

# 2. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 3. 启动网关
tslink serve --daemon

# 从任何设备访问 https://myapp.<your-tailnet>.ts.net
```

TSLink 支持两种凭证（只需选一种）：

- **API 访问令牌** (`tskey-api-*`) — 在 [管理后台 → Keys](https://login.tailscale.com/admin/settings/keys) 生成。当前自动化能力最完整，包括通过 Tailscale API 管理标签和设备。它会周期性过期。
- **OAuth 客户端密钥** (`tskey-client-*`) — 在 [管理后台 → OAuth](https://login.tailscale.com/admin/settings/oauth) 生成。它不会过期，但 TSLink 当前的 REST API 自动化路径在该模式下更窄。用于无人值守前请先验证所需的标签/设备操作。

`tslink login` 会交互式引导你完成任一路径。凭证优先存储在系统钥匙串（macOS Keychain / Linux secret service / Windows 凭据管理器）中；headless 环境可回退到受限权限文件。

### 标签自动管理

TSLink 会自动为你的服务管理 Tailscale ACL 标签：

- **默认标签** — 当 `tslink add` 未指定 `--tags` 时，每个服务自动应用 `tag:tsmain`。
- **API 访问令牌标签自动化** — 使用 API 访问令牌时，启动阶段可以在节点启动前确保注册表中的标签存在。`tslink tags pull` 也只在 API 访问令牌模式下拉取远端 ACL 标签；OAuth-only 模式会跳过远端读取并提示需要 API 访问令牌。
- **严格标签语法** — 标签必须匹配 `tag:<lowercase-hyphen-name>`，只使用小写字母、数字和连字符。将 `tag:Web`、`tag:db_main` 或 `web` 这类旧值迁移为 `tslink tags set <service> tag:<lowercase-hyphen-name>`，也可以直接编辑 `registry.json`。无效旧标签会让 `tslink serve` 验证失败，必须先修复才能启动网关。
- **运行时认证刷新** — 标签、临时节点设置和有效控制服务器 URL 变化时，受影响节点会删除本地状态并用新的每服务认证材料重启。切换凭证模式或修改旧版 `authkey` 文件后仍需重启 `tslink serve` 进程。

使用 `tslink tags` 查看和自定义标签分配：

```bash
# 查看所有服务及其标签
tslink tags list

# 拉取 Tailscale ACL 中当前定义的标签（需要 API 访问令牌；OAuth-only 模式会跳过）
tslink tags pull

# 为某个服务添加标签（节点自动重启）
tslink tags add myapp tag:production

# 替换某个服务的全部标签
tslink tags set myapp tag:webserver

# 修改新服务的默认标签
tslink tags set-default tag:myteam

# 通过本地安全检查后全局移除 ACL 标签所有者规则
tslink tags delete-remote tag:old-tag --force
```

### 更多示例

```bash
# 暴露文件目录
tslink add documents --dir ~/Documents

# 通过 TCP 代理暴露数据库
tslink add mydb --tcp localhost:5432

# 临时节点（停止后自动从 tailnet 移除）
tslink add demo --proxy localhost:8080 --ephemeral

# 基于身份的 HTTP 访问控制（仅 proxy/file）
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# 通过 Tailscale Funnel 公开暴露
tslink add public --proxy localhost:3000 --funnel

# ACL 标签
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

## 命令

| 命令 | 描述 |
|------|------|
| `tslink login` | 使用 API 访问令牌或 OAuth 客户端密钥认证 Tailscale |
| `tslink logout` | 清除认证状态 |
| `tslink add <name> --proxy host:port` | 暴露本地 Web 服务 |
| `tslink add <name> --dir /path` | 暴露文件目录 |
| `tslink add <name> --tcp host:port` | 暴露 TCP 服务（数据库、SSH 等） |
| `tslink remove <name>` | 移除已注册的服务（+ 所有权安全的远端清理尝试） |
| `tslink list` | 列出所有已注册的服务 |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink tags list` | 列出所有服务及其标签 |
| `tslink tags pull` | 使用 API 访问令牌从 Tailscale ACL 拉取远端标签；OAuth-only 模式会跳过 |
| `tslink tags add <service> <tag>` | 为服务追加一个标签 |
| `tslink tags set <service> <tag>` | 替换服务的全部标签 |
| `tslink tags set-default <tag>` | 修改新服务的默认标签 |
| `tslink tags delete-remote <tag> --force` | 通过本地安全检查后从 Tailscale ACL 全局移除 ACL 标签所有者规则 |
| `tslink api` | JSON-over-stdin/stdout 模式，用于程序化控制 |
| `tslink config` | 管理全局配置（set/get/list） |
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
| `--allow user@,tag:x` | proxy/file 服务的 HTTP 访问控制；TCP 不应用该 HTTP ACL |
| `--funnel` | 通过 Tailscale Funnel 暴露到公网（仅限 proxy） |
| `--domain example.com` | Roadmap/experimental：可写入服务配置，但自定义域名运行时 TLS 尚未接入 |
| `--acme-email user@example.com` | Roadmap/experimental：随 `--domain` 存储；尚无已交付 ACME listener |

## 工作原理

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│  你的机器   │         │   Tailscale 网络     │         │   你的手机   │
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
- **安全凭证管理** — 系统钥匙串存储，headless 环境支持受限权限文件后备
- **基于文件的注册表** — 服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载** — 注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期** — 通过进程身份检查管理守护进程，并按平台明确停止行为
- **结构化日志** — 基于 slog 的结构化日志 + 访问日志
- **指标采集** — 内部记录请求指标；公开 `/metrics` 端点仍在 roadmap

## API 模式

TSLink 提供最小 JSON-over-stdin/stdout API 模式，用于本地自动化。当前支持基础 `list`、`add`、`remove`、`status` 动作；尚未与每个 `tslink add` 标志完全对齐。

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

## Roadmap / Experimental 包

仓库中包含一些尚未接入已交付 `tslink serve` 运行路径的包和注册表字段。除非后续有端到端集成测试证明，否则请把它们视为 roadmap 或 experimental：

| 领域 | 当前状态 |
|---|---|
| Docker 标签 | 包存在，但 `serve` 不会启动 Docker discovery。 |
| Middleware | 包和 schema 存在，但 runtime 不应用限流、Basic Auth、IP 白名单或 CORS。 |
| Admin dashboard / REST API | handler 存在，但不会启动 admin 节点。 |
| Prometheus `/metrics` | 内部 instrumentation 存在，但没有挂载 scrape endpoint。 |
| Custom domain / ACME | 字段可写入，但 runtime TLS/ACME listener 尚未接入。 |
| Cluster sync | 包存在，但没有 production transport 或 `serve` 集成。 |

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- Go 1.26.3+（如果从源码构建）

## 平台支持

| 平台 | 守护进程 | 自动启动 | 停止行为 |
|------|---------|---------|---------|
| macOS | `--daemon` | LaunchAgent | SIGTERM 优雅停止 |
| Linux | `--daemon` | systemd user service | SIGTERM 优雅停止 |
| Windows | `--daemon` | 启动文件夹 | 强制终止进程 |

## 路线图

- [x] OAuth client secret 可由 login 和 tsnet auth 路径接受；无人值守前需验证标签/设备自动化
- [ ] 自定义域名 / ACME 运行时 TLS
- [ ] 可从 tailnet 访问的 Web 管理面板
- [ ] Docker 镜像和 Docker 标签发现
- [ ] Headscale 端到端测试
- [ ] 完整 API parity 和经过集成测试的 Layer 2 模块

## 贡献

欢迎贡献！请参阅 [CONTRIBUTING.md](./CONTRIBUTING.md) 了解指南。

## 安全

安全相关事宜请参阅 [SECURITY.md](./SECURITY.md)。

## 许可证

本项目基于 [Apache License 2.0](./LICENSE) 许可。

```
Copyright 2026 Maintainer (monody0007)
```
