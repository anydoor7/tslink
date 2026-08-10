<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">面向本地服务的私有 Tailscale 网关。<br>一条命令让每个 HTTP、文件或 TCP 服务获得独立 tailnet 身份。</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/actions"><img src="https://img.shields.io/github/actions/workflow/status/monody0007/tslink/ci.yml?branch=main&label=CI" alt="Build Status"></a>
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.26.5%2B-00ADD8.svg" alt="Go"></a>
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

**TSLink 为本地服务提供 per-service tailnet 身份。** 一条命令将你的机器变成 Tailscale-backed 网关。tailnet transport 遵循 Tailscale/WireGuard 语义；proxy/file 服务可以增加 HTTP identity 和 `--allow` 检查，raw TCP 保持私有字节流，不经过 TSLink HTTP middleware。

## 安全模型

TSLink 在每一层实现零信任原则：

| 零信任原则 | TSLink 实现 |
|-----------|------------|
| **HTTP 调用方验证** | tailnet 内的 HTTP 代理/文件请求可以通过 Tailscale WhoIs 认证。身份头（`X-Tailscale-User-Login`、`X-Tailscale-User-Name`、`X-Tailscale-User-Picture`、`X-Tailscale-Node`）只在 WhoIs 成功时注入代理请求。公网 Funnel 和 raw TCP 不视为 TSLink 强制执行的 Tailscale 用户认证。 |
| **HTTP 最小权限访问** | `--allow` 限制 proxy 和 file 服务的访问用户或标签。TCP 服务依赖 Tailscale 网络 ACL 和标签。 |
| **假设已被攻破** | tailnet 设备之间的流量使用 WireGuard 加密。即使本地网络被攻破，Tailscale 设备之间的流量仍然加密；公网 Funnel 路径遵循 Tailscale Funnel 语义。 |
| **Per-service 网络身份** | 每个服务作为独立 tsnet 节点运行，拥有自己的主机名和网络身份。这是网络分段，不是 host process isolation 或合规背书。 |
| **消除隐式信任** | 默认不暴露任何服务到公网。首次运行默认走 Tailscale interactive enrollment：不存储管理员凭证、不 advertise tags、也不修改 ACL。可选的 durable-install 凭证优先存入系统钥匙串；headless 环境可回退到受限权限文件。 |

## TSLink 做什么

一条命令将任何本地服务 — Web 应用、API、文件目录、数据库 — 暴露到你的私有 Tailscale 网络。proxy/file 服务使用 Tailscale HTTPS listener；raw TCP 服务使用私有 tailnet transport，不由 TSLink 终止 TLS。

```bash
tslink add myapp --proxy localhost:3000
tslink serve --daemon
# → https://myapp.<your-tailnet>.ts.net — 从 tailnet 上任何设备访问
```

### 功能特性

- **零配置** — 无需端口转发、DNS 或证书管理
- **WireGuard tailnet 路径** — tailnet 设备间流量通过 Tailscale 使用 WireGuard；公网暴露必须显式启用 Funnel
- **Proxy/file HTTPS** — HTTP proxy 和 file 服务使用 Tailscale HTTPS listener；raw TCP 保持私有 tailnet 字节流
- **Per-service 身份** — 每个服务获得独立的 tailnet 主机名和网络身份（proxy/file URL 形如 `https://<name>.<tailnet>.ts.net`）
- **热重载** — 运行时添加或移除服务，更改立即生效
- **全平台支持** — 支持 macOS、Linux 和 Windows
- **守护进程运行** — 启动一次，后台运行，支持开机自启
- **TCP 代理** — 暴露数据库、SSH、Redis 等非 HTTP 服务
- **HTTP 访问控制** — proxy 和 file 服务支持 `--allow user@example.com,tag:admin`
- **安全诊断** — `tslink doctor`、`tslink status --urls` 和 `tslink access explain` 明确展示本地证据和未知的外部策略层
- **本地 API 模式** — JSON-over-stdin/stdout，覆盖 list/add/remove/status、诊断、访问解释和模板自动化
- **个人模板** — 预览并添加小型私有服务套件，不覆盖已有服务
- **Headscale 兼容路径** — 通过 `--control-url` 支持高级/自托管控制服务器场景
- **Funnel 护栏** — 公网暴露必须显式选择，并要求 `--public` 确认

### 发布状态

| 已交付 | Roadmap / experimental |
|---|---|
| Proxy、file、原始 TCP 服务 | Roadmap/experimental 中间件管道（限流、Basic Auth、IP 白名单、CORS） |
| 每服务一个嵌入式 `tsnet` 节点 | Roadmap/experimental Docker 标签自动发现 |
| 身份感知 HTTP 代理头 | Roadmap/experimental 管理面板或 REST surface |
| proxy/file 的 HTTP `--allow` | Roadmap/experimental Prometheus `/metrics` 端点 |
| 注册表热重载 | Roadmap/experimental 自定义域名 / ACME 运行时 TLS |
| 守护进程和开机自启 | Roadmap/experimental Cluster / 多节点注册表同步 |
| owner-only `status --urls`、`doctor` 和 `access explain` | Roadmap/experimental 成员可见 portal 或服务目录 |
| 已交付 owner 工作流的本地 JSON `tslink api` parity | Roadmap/experimental 远程 API、dashboard 或多用户管理面 |
| 内置个人模板 | Roadmap/experimental marketplace 或第三方模板注册表 |

## 快速开始

### 安装

```bash
# 首个公开 release 前的源码安装（任何平台）
git clone https://github.com/monody0007/tslink.git
cd tslink
go install .

# Homebrew cask 和预构建归档只在首个公开 release 与产物/tap readback 后可用。
```

### 发布产物

当前还没有公开 tag/release，Homebrew tap 也尚未发布可安装产物。首个公开
release/readback 前请从源码安装。该外部 gate 通过后，GitHub Releases 预计发布以下可安装产物：

| 平台 | 产物 | 说明 |
|---|---|---|
| macOS | Homebrew cask 和 `tar.gz` 归档 | Homebrew cask 使用 GoReleaser `skip_upload: auto`，pre-release tag 可以跳过 tap upload 且不让发布失败。预发布验证优先使用归档产物。 |
| Linux | `.deb`、`.rpm` 和 `tar.gz` 归档 | 包内包含原生 `tslink` 二进制。安装后用 `tslink install` 注册 user service。 |
| Windows | `.zip` 归档 | Windows 当前是 archive-only 支持。尚未提供 MSI/MSIX/Winget 包或 Windows 代码签名安装器。解压后用 `tslink install` 注册 Startup 自启动。 |

发布产物是并列的 release assets，不是嵌入归档内部的文件。GoReleaser 会上传可安装归档/包、`checksums.txt`、归档对应的 CycloneDX SBOM sidecar，以及 `checksums.txt` 和 SBOM sidecar 的 keyless Sigstore bundle 签名。签名后的 `checksums.txt` 覆盖可安装产物和 SBOM sidecar。release workflow 还会为可安装产物和供应链 sidecar 发布 GitHub artifact attestations。

### 验证发布完整性

下面命令需要 `gh` 2.49 或更新版本并支持 `gh attestation verify`，`cosign` 支持 `verify-blob --bundle`，以及 `sha256sum` 或 `shasum`。将 `<version>` 替换为 GitHub Release tag，将 `<artifact>` 替换为该 release 里的产物文件名。

Sigstore 证书的信任根是 GitHub Actions OIDC issuer `https://token.actions.githubusercontent.com`。验证会钉住精确的 release workflow 身份 `https://github.com/monody0007/tslink/.github/workflows/release.yml@refs/tags/<version>`，以及 GitHub attestation signer workflow `github.com/monody0007/tslink/.github/workflows/release.yml`。tag ref 绑定意味着匹配的签名或 attestation 必须来自本仓库对该 tag 运行的 release workflow。

```bash
set -euo pipefail

repo="monody0007/tslink"
version="<version>"
artifact="<artifact>"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

mkdir -p "tslink-$version-verify"
cd "tslink-$version-verify"

gh release download "$version" --repo "$repo" \
  --pattern "$artifact" \
  --pattern "checksums.txt" \
  --pattern "checksums.txt.sigstore.json"

require_file "$artifact"
require_file "checksums.txt"
require_file "checksums.txt.sigstore.json"
verify_checksum "$artifact"

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$artifact" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

归档 SBOM sidecar 是独立 release asset，需要单独验证。请在归档验证后的同一个验证目录中运行，使用相同的 `version` 和 `artifact`。

```bash
set -euo pipefail

repo="monody0007/tslink"
version="<version>"
artifact="<artifact>"
sbom="$artifact.sbom.json"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

gh release download "$version" --repo "$repo" \
  --pattern "$sbom" \
  --pattern "$sbom.sigstore.json"

require_file "$sbom"
require_file "$sbom.sigstore.json"
verify_checksum "$sbom"

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
# 1. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 2. 启动网关——无需 API token 或 OAuth secret。
# TSLink 会为 user-owned node 打开/打印一个 Tailscale 授权 URL。
tslink serve --daemon

# 从任何设备访问 https://myapp.<your-tailnet>.ts.net
```

TSLink 有两层认证模式：

- **Tier 1 — 零凭证（默认）**：user-owned node，不 advertise tags，也不调用远端 ACL API。适合临时展示页面或 ephemeral share。每个新的 service node 都有自己的 enrollment URL；单服务 quick share 只需一次 browser click。Tailscale 的 user-owned node key 会过期，因此持续运行数月的节点最终可能需要重新认证。
- **Tier 2 — 存储凭证（opt-in）**：保留 tagged、per-service 的启动行为，适合 durable multi-service 安装。只有需要这一层时才运行 `tslink login`。

Tier 2 接受以下任一种管理员凭证：

- **API 访问令牌** (`tskey-api-*`) — 在 [管理后台 → Keys](https://login.tailscale.com/admin/settings/keys) 生成。当前自动化能力最完整，包括通过 Tailscale API 管理标签和设备。它会周期性过期。
- **OAuth 客户端密钥** (`tskey-client-*`) — 在 [管理后台 → OAuth](https://login.tailscale.com/admin/settings/oauth) 生成。它不会过期，但 TSLink 当前的 Tailscale 标签/设备自动化在该模式下更窄，因为这些操作依赖 Tailscale REST API。用于无人值守前请先验证所需的标签/设备操作。

`tslink login` 会交互式引导你完成任一 Tier 2 凭证路径；它不会先做一次无实际作用的临时 browser login。凭证优先存储在系统钥匙串（macOS Keychain / Linux secret service / Windows 凭据管理器）中；headless 环境可回退到受限权限文件。

非交互式自动化优先使用 stdin。环境变量只适合由 secret manager 在进程启动前预注入；不要在 shell 命令里 inline secret 值，否则可能进入 shell history：

```bash
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
```

兼容性保留的 `--api-key` 和 `--client-secret` flags 仍可用，但命令行参数可能被其它本机进程看到，不作为推荐路径。

### 标签管理

TSLink 默认管理本地服务标签。远端 Tailscale ACL mutation 默认关闭，因为 TSLink 还没有本地证明对 HuJSON policy 的无损保留。

零凭证 Tier 1 会保留 registry 中的 tags 配置，但 user-owned node 不 advertise 这些 tags，也不会调用远端 tag/ACL API。下面的标签行为适用于存储凭证的 Tier 2。

- **默认标签** — 当 `tslink add` 未指定 `--tags` 时，每个服务自动应用 `tag:tsmain`。
- **远端 ACL 读取** — `tslink tags pull` 只在 API 访问令牌模式下拉取远端 ACL 标签；OAuth-only 模式会跳过远端读取并提示需要 API 访问令牌。
- **远端 ACL 写入** — `tslink login --manage-acl`、`tslink serve --manage-acl` 和 `tslink tags delete-remote --manage-acl` 才会 opt in typed whole-policy ACL writes，并输出 machine-readable side-effect plan。默认 login、serve 和 tag flows 不会改写共享 ACL policy。
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

# 通过本地安全检查和显式 ACL 管理 opt-in 后全局移除 ACL 标签所有者规则
tslink tags delete-remote tag:old-tag --force --manage-acl
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

# 通过 Tailscale Funnel 公开暴露（必须显式确认）
tslink add public --proxy localhost:3000 --funnel --public

# ACL 标签
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

## 命令

| 命令 | 描述 |
|------|------|
| `tslink login` | 存储可选的 Tier 2 API 访问令牌或 OAuth 客户端密钥 |
| `tslink logout` | 清除认证状态 |
| `tslink add <name> --proxy host:port` | 暴露本地 Web 服务 |
| `tslink add <name> --dir /path` | 暴露文件目录 |
| `tslink add <name> --tcp host:port` | 暴露 TCP 服务（数据库、SSH 等） |
| `tslink remove <name>` | 移除已注册的服务，并报告 protected/manual 远端清理指引 |
| `tslink list` | 列出所有已注册的服务 |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink status --urls` | 显示 owner-only 服务 URL、暴露模式、allow 摘要、后端和 warning code |
| `tslink doctor` | 只读诊断凭证、daemon、注册表、runtime snapshot、暴露模式和目标安全性 |
| `tslink access explain <service>` | 解释某个服务的本地访问模型，以及仍属于外部策略/后端认证的部分 |
| `tslink logs` | 查看最近的网关日志 |
| `tslink template list` | 列出内置个人服务模板 |
| `tslink template show <name>` | 预览内置模板 |
| `tslink template apply <name> --yes` | 只添加缺失的模板服务，不覆盖已有服务；不加 `--yes` 或传 `--dry-run` 只预览 |
| `tslink tags list` | 列出所有服务及其标签 |
| `tslink tags pull` | 使用 API 访问令牌从 Tailscale ACL 拉取远端标签；OAuth-only 模式会跳过 |
| `tslink tags add <service> <tag>` | 为服务追加一个标签 |
| `tslink tags set <service> <tag>` | 替换服务的全部标签 |
| `tslink tags set-default <tag>` | 修改新服务的默认标签 |
| `tslink tags delete-remote <tag> --force --manage-acl` | 通过本地安全检查和显式远端写入 opt-in 后从 Tailscale ACL 全局移除 ACL 标签所有者规则 |
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
| `--allow user@,tag:x` | proxy/file 服务的 HTTP 访问控制；TCP 会拒绝该标志，因为原始 TCP 使用 Tailscale ACL 标签和目标服务自身认证 |
| `--control-url URL` | 服务级控制服务器覆盖，例如 Headscale |
| `--funnel` | 通过 Tailscale Funnel 暴露到公网（仅限 proxy，必须同时传 `--public`） |
| `--public` | 显式确认 `--funnel` 的公网暴露；没有 `--funnel` 时无效 |
| `--domain example.com` | Reserved roadmap flag：会以 `feature_unavailable` 拒绝；自定义域名运行时 TLS 尚未接入 |
| `--acme-email user@example.com` | Reserved roadmap flag：会以 `feature_unavailable` 拒绝；尚无已交付 ACME listener |

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

TSLink 为每个注册的服务创建一个专用的 [tsnet](https://tailscale.com/kb/1244/tsnet) 节点 — 服务端无需安装 Tailscale 客户端。每个服务作为独立设备加入 tailnet（如 `myapp`、`docs`、`mydb`）。proxy/file 服务使用 Tailscale HTTPS listener；raw TCP 服务使用私有 tailnet transport，并把字节代理到配置的目标。

**关键架构决策：**
- **Per-service 嵌入式节点** — 每个服务获得独立的 tailnet 身份和主机名；proxy/file 服务还获得 Tailscale HTTPS listener 语义
- **身份感知代理** — tailnet 内的 HTTP 代理/文件请求进行 WhoIs 验证，注入身份头并防止伪造；公网 Funnel 和 raw TCP 不获得 TSLink 强制执行的 HTTP 身份认证
- **安全凭证管理** — 系统钥匙串存储，headless 环境支持受限权限文件后备
- **基于文件的注册表** — 服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载** — 注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期** — 通过进程身份检查管理守护进程，并按平台明确停止行为
- **结构化日志** — 基于 slog 的结构化日志 + 访问日志
- **指标采集** — 内部记录请求指标；公开 `/metrics` 端点仍在 roadmap

## API 模式

TSLink 提供本地 JSON-over-stdin/stdout API 模式，用于 owner 侧自动化。它不是 REST server、dashboard 或成员可见的服务目录。未知 JSON 字段会被拒绝，公开 API 响应使用脱敏视图，不直接输出原始注册表记录。

```bash
# 列出服务
echo '{"action":"list"}' | tslink api

# 添加服务
echo '{"action":"add","name":"myapp","type":"proxy","target":"localhost:3000"}' | tslink api

# 删除服务
echo '{"action":"remove","name":"myapp"}' | tslink api

# 查看状态
echo '{"action":"status"}' | tslink api

# 查看 owner-only endpoint / exposure 概览
echo '{"action":"status","urls":true}' | tslink api

# 运行只读诊断
echo '{"action":"doctor","probe_external":false}' | tslink api

# 解释一个服务的本地访问模型
echo '{"action":"access_explain","name":"myapp"}' | tslink api

# 预览/应用内置模板
echo '{"action":"template_list"}' | tslink api
echo '{"action":"template_plan","name":"personal-harness"}' | tslink api
echo '{"action":"template_apply","name":"personal-harness"}' | tslink api
```

API `add` 和 CLI 使用同一套安全护栏。Funnel 服务必须传 `public_ack:true`；TCP 服务会拒绝 `allow`，因为 TSLink 不会对原始 TCP 字节流应用 HTTP 身份检查。

## Roadmap / Experimental 包

仓库中包含一些尚未接入已交付 `tslink serve` 运行路径的包和注册表字段。除非后续有端到端集成测试证明，否则请把它们视为 roadmap 或 experimental：

| 领域 | 当前状态 |
|---|---|
| Docker 标签 | 包存在，但 `serve` 不会启动 Docker discovery。 |
| Middleware | 包和 schema 存在，但 runtime 不应用限流、Basic Auth、IP 白名单或 CORS。 |
| Admin dashboard / REST API | 默认构建不包含 package、REST handler 或 admin 节点。未来恢复必须显式标为 experimental，并补端到端测试。 |
| Prometheus `/metrics` | 内部 instrumentation 存在，但没有挂载 scrape endpoint。 |
| Custom domain / ACME | 字段保留但会以 `feature_unavailable` 拒绝；runtime TLS/ACME listener 尚未接入。 |
| Cluster sync | 包存在，但没有 production transport 或 `serve` 集成。 |

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- Go 1.26.5+（如果从源码构建）

## 平台支持

| 平台 | 守护进程 | 自动启动 | 停止行为 |
|------|---------|---------|---------|
| macOS | `--daemon` | LaunchAgent | SIGTERM 优雅停止 |
| Linux | `--daemon` | systemd user service | SIGTERM 优雅停止 |
| Windows | `--daemon` | 启动文件夹 | 强制终止进程 |

macOS LaunchAgent 安装会使用 launchd `KeepAlive` 和 `ThrottleInterval=30`。如果 LaunchAgent 仍在安装状态，运行 `tslink stop` 后 launchd 会重启 TSLink。想禁用自启动时，先运行 `tslink uninstall`，再运行 `tslink stop`。SSH/headless macOS 安装时，`tslink install` 会先尝试 `gui/$(id -u)`，如果 GUI launchd domain 不可用，会回退到 `user/$(id -u)`。Linux headless user service 如需登出后继续运行，可能需要执行 `loginctl enable-linger "$USER"`；如果 lingering 只为 TSLink 启用，卸载后运行 `loginctl disable-linger "$USER"`。

## 路线图

- [x] OAuth client secret 可由 login 和 tsnet auth 路径接受；无人值守前需验证标签/设备自动化
- [ ] 自定义域名 / ACME 运行时 TLS
- [ ] 可从 tailnet 访问的 Web 管理面板
- [ ] Docker 镜像和 Docker 标签发现
- [ ] Headscale 端到端测试
- [x] 已交付 owner 工作流的本地 API parity
- [ ] 经过集成测试的 Layer 2 模块和可选远程/管理面

## 贡献

欢迎贡献！请参阅 [CONTRIBUTING.md](./CONTRIBUTING.md) 了解指南。

## 安全

安全相关事宜请参阅 [SECURITY.md](./SECURITY.md)。

## 许可证

本项目基于 [Apache License 2.0](./LICENSE) 许可。

```
Copyright 2026 Maintainer (monody0007)
```
