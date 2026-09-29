<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">一条命令，给任何本地服务一个自己的 tailnet 主机名。<br>不用进管理后台，不用打标签，不用等审批，不用写 Go。</p>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.26.6%2B-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README.md">English</a> ·
  <a href="https://github.com/monody0007/tslink">GitHub</a>
</p>

**开源许可：**Apache 2.0 允许个人和任何规模组织按许可用于个人及商业用途。[自愿支持与合作](./COMMERCIAL_zh.md)。

---

## 为什么需要 TSLink？

### 你省掉的那些步骤

给服务单独的主机名是 Tailscale 的原生能力。[Tailscale Services](https://tailscale.com/docs/features/tailscale-services) [自 2026 年 2 月起正式可用](https://tailscale.com/blog/services-ga)，`tailscale serve --service=svc:web-server --https=443 127.0.0.1:8080` 在普通 `tailscaled` 上就能得到 `https://web-server.<tailnet>.ts.net`。TSLink 建立在 [tsnet](https://tailscale.com/docs/features/tsnet) 之上，使用其文档介绍的集成方式独立开发。

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

## 安全模型

TSLink 与常见零信任原则的对应关系：

| 零信任原则 | TSLink 实现 |
|-----------|------------|
| **HTTP 调用方验证** | tailnet 内的 HTTP 代理/文件请求可以通过 Tailscale WhoIs 认证。身份头（`X-Tailscale-User-Login`、`X-Tailscale-User-Name`、`X-Tailscale-User-Picture`、`X-Tailscale-Node`）只在 WhoIs 成功时注入代理请求。公网 Funnel 和 raw TCP 不视为 TSLink 强制执行的 Tailscale 用户认证。 |
| **HTTP 最小权限访问** | `--allow` 限制 proxy 和 file 服务的访问用户或标签。TCP 服务依赖 Tailscale 网络 ACL 和标签。 |
| **假设已被攻破** | tailnet 设备之间的流量使用 WireGuard 加密。即使本地网络被攻破，Tailscale 设备之间的流量仍然加密；公网 Funnel 路径遵循 Tailscale Funnel 语义。 |
| **Per-service 网络身份** | 每个服务作为独立 tsnet 节点运行，拥有自己的主机名和网络身份。这是网络分段，不是 host process isolation 或合规背书。 |
| **消除隐式信任** | 默认不暴露任何服务到公网。首次运行默认走 Tailscale interactive enrollment：不存储管理员凭证、不 advertise tags、也不修改 ACL。可选的 durable-install 凭证优先存入系统钥匙串；headless 的 macOS 与 Linux 环境可回退到受限权限文件。 |

这是一份设计层面的对应关系，不是正式背书。TSLink 不声称任何合规状态；机器可读的能力清单是 [`internal/security/capabilities.v1.json`](./internal/security/capabilities.v1.json)，其中每一条 capability 都显式记录了自己的合规状态。

## TSLink 做什么

一条命令将任何本地服务 — Web 应用、API、文件目录、数据库 — 暴露到你的私有 Tailscale 网络。proxy/file 服务使用 Tailscale HTTPS listener；raw TCP 服务使用私有 tailnet transport，不由 TSLink 终止 TLS。

```bash
tslink add myapp --proxy localhost:3000
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
- **面向 agent 的自动化** — 除 stdio 的 `tslink mcp` 外，每个命令都接受 `--json` 并返回同一套版本化 envelope；`tslink mcp`（stdio）与 `tslink serve --mcp`（仅限 tailnet 的远程控制面）暴露同一组 MCP tools
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
| 每个命令的 `--json` envelope、stdio 的 `tslink mcp`，以及 opt-in 的仅限 tailnet MCP 控制面（`tslink serve --mcp`） | Roadmap/experimental dashboard、REST API 或多用户管理面 |
| 内置个人模板 | Roadmap/experimental marketplace 或第三方模板注册表 |

## 快速开始

### 安装

需要 Go 1.26.6 或更高版本。

```bash
go install github.com/monody0007/tslink@latest

# 二进制装到 $(go env GOPATH)/bin，该目录默认不在 PATH 中：
export PATH="$PATH:$(go env GOPATH)/bin"
```

Homebrew 与预构建归档随首个 tagged release 提供。在那之前，源码安装是受支持的路径。
从 clone 构建同样可行：

```bash
git clone https://github.com/monody0007/tslink.git && cd tslink && go install .
```

### 从零到一个 URL

一条命令，不用注册账号，不用复制任何 token：

```bash
tslink share ./build
```

TSLink 会注册这个目录，daemon 没在跑就顺手启动，然后打印一个 Tailscale 授权 URL。
打开它、批准这个节点，命令就返回真实 URL。用手机、平板或 tailnet 上任何其它设备打开即可。

不需要 API token，不需要 OAuth client，不用去 admin console。节点以你的身份入网，
因此它自己不需要任何 ACL 策略。

如果你更想显式注册服务并长期保留：

```bash
# 1. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 首次使用会自动安装后台服务并打印精确 URL。
# 如果需要 Tailscale 入网，打开打印的授权 URL 批准节点。

# 从任何设备访问 https://myapp.<your-tailnet>.ts.net
```

`add`、`share` 和 `template apply --yes` 在后台服务未运行时自动安装并启动它，
CI、无 TTY 与交互终端行为相同。安装会向 stderr 声明监管器、安装路径、配置目录和
撤销命令 `tslink uninstall`；`--json` 的 stdout 保持单条结果。使用
`--no-daemon-install` 可跳过安装。离线 `add` 只保存配置，返回
`daemon_running:false` 和修复指引，不显示绿勾；带此参数的 `share` 要求已有服务在运行。
`add` 默认等待至多 30 秒取得精确 URL 或入网授权 URL；`--wait=0` 可关闭等待。

MCP 的 `add`、`share`、`template_apply` 同样自举，并提供 `no_daemon_install`。
MCP `add` 自举完成后返回当前 URL/入网证据，不额外等待 URL；仍 pending 时可调用 `url`。
`add` 和模板先写 registry 再安装，安装失败保留已保存的配置。显式 `install` 也支持尚无
registry 的新环境。失败消息会说明监管定义是否残留：Linux 可能留下 enabled 且正在
重试的 unit；macOS 新装校验失败会在清理成功时撤回 job/plist。重试前检查 `tslink logs`
和 `tslink doctor`；Linux 还可用 `journalctl --user -u tslink.service`。
安装器验证监管状态稳定，自举再验证新鲜业务证据及稳定窗口；两者均不保证未来不会崩溃。

`status` 与 `doctor` 的文本和 JSON 都报告 `supervision`：监管器、自启动、
`autostart_scope`、重启策略与探测说明。无法确认监管的运行进程记为 `manual`；
没有运行进程且无可验证监管记为 `none`。`autostart_scope` 回答单个 autostart 布尔量
无法回答的问题：`boot` 表示无人登录时也随开机返回，`login` 表示要等这个用户登录，
`unknown` 表示无法判定。macOS LaunchAgent 与 Windows 启动项恒为 `login`；
systemd user unit 只有开启 lingering 才是 `boot`，否则报 `login` 并给出
`loginctl enable-linger "$USER"`。TSLink 只报告 lingering，不代为修改，
因为它作用于该用户的所有服务。
已有注册服务但无监管时 doctor 判 error；确认后台服务未运行时延后后端探测。PID 身份或监管状态无法确认时则给 warning 并保留探针，
先检查运行二进制与日志，再决定是否安装/重启。任何节点正在入网时，`url` 会返回授权动作，避免重复等待。
此时 `url` 的后续动作是 `tslink install`。

macOS 注册登录时启动的 LaunchAgent；Linux 启用 systemd user unit。Linux 若要开机无需
登录、退出登录后也继续运行，需执行一次 `loginctl enable-linger "$USER"`。
Windows 自动安装后立即运行 Startup 脚本，后续登录时再次启动；它不监管崩溃重启，也无法
证明当前 PID 的归属。后端应用本身仍需设置开机启动，首次 Tailscale 入网仍需授权。
安装文件绑定绝对 `TSLINK_CONFIG_DIR`，自动安装拒绝覆盖另一配置的监管器。
Homebrew 不注册第二套服务管理器。

### 一条命令分享

`tslink share` 会判断参数是目录、普通文件、裸端口还是 `host:port`。它会
注册服务且不覆盖已有同名项，在需要时启动 daemon，等待精确 runtime URL，
最后只向 stdout 打印该 URL。share 默认使用 ephemeral node。

```bash
tslink share ./build                # 服务整个 ./build 目录树，可浏览目录
tslink share ./report.html          # 只服务 report.html，同目录其它文件不可达
tslink share 3000
tslink share localhost:8080 --name preview
tslink share ./build --ephemeral=false
```

两种路径形态的可达范围不同，这是 service 自身的边界，不是列目录的显示偏好。
目录 target 会服务其下全部文件，没有 `index.html` 的目录会渲染目录列表。
普通文件 target 只服务那一个文件：URL 即该文件，service 根路径 302 跳转到它，
其余任何路径都返回 404，包括同目录下的其它文件。registry 用 file service 的
`file` 字段记录这个收窄；没有该字段的条目就是目录 share。

两种形态都不限制**谁**可以读：tailnet 的每个成员都能取到该 share，而且
`tslink share` 没有 `--allow` 参数。要限制目录的读者，改用
`tslink add <name> --dir <directory> --allow <principal>` 注册，或在 MCP
`share` 工具里传 `allow`。

零凭证首次运行时，stdout 的唯一一行是 Tailscale 授权 URL；stderr 会给出
精确的 `tslink url <name> --wait` 后续命令。使用 `--json` 时，这是包含
`auth_url` 的成功 `status:"needs_login"` 结果，不是认证错误。对同一 target
重试会复用已有 service，不会持续创建带数字后缀的孤儿 node。

### 面向 agent 的 MCP server

`tslink mcp` 通过 stdio 运行本地 MCP server。MCP 进程本身不打开网络
listener；调用其中的 `share` tool 可能启动独立的 TSLink daemon 及所请求的
tsnet service。它暴露 19 个 tools，覆盖 CLI 的 per-service 能力面：`share`、`add`、
`list`、`unshare`、`status`、`url`、`tags_list`、`tags_set`、`access_explain`、
`doctor`、`logs`、`invite_user`、`invite_device`、`invite_list`、`invite_revoke`、
`invite_resend`、`template_list`、`template_plan` 和 `template_apply`。daemon
生命周期、install、login/logout 和配置仍只通过 CLI 操作。可让 MCP client
启动已安装的 `tslink` 命令，并传入唯一参数 `mcp`：

```json
{
  "command": "tslink",
  "args": ["mcp"]
}
```

`share` tool 接受与 CLI 相同的 path/port/host:port target。需要授权时，它会
把 `{"status":"needs_login","auth_url":"..."}` 作为正常 tool result 返回，agent
可以打开该 URL 后重试。MCP 永远不会返回 credential 值。同一组 tools 也可以提供给
tailnet 内的其它机器，见[远程 MCP 控制面](#远程-mcp-控制面)。

TSLink 有两层认证模式：

- **Tier 1 — 零凭证（默认）**：user-owned node，不 advertise tags，也不调用远端 ACL API。适合临时展示页面或 ephemeral share。每个新的 service node 都有自己的 enrollment URL；单服务 quick share 只需一次 browser click。Tailscale 的 user-owned node key 会过期，因此持续运行数月的节点最终可能需要重新认证。
- **Tier 2 — 存储凭证（opt-in）**：保留 tagged、per-service 的启动行为，适合 durable multi-service 安装。只有需要这一层时才运行 `tslink login`。

Tier 2 接受以下任一种管理员凭证：

- **API 访问令牌** (`tskey-api-*`) — 在 [管理后台 → Keys](https://login.tailscale.com/admin/settings/keys) 生成。当前自动化能力最完整，包括通过 Tailscale API 管理标签和设备。它会周期性过期。
- **OAuth 客户端密钥** (`tskey-client-*`) — 在 [管理后台 → OAuth](https://login.tailscale.com/admin/settings/oauth) 生成。它不会过期，但 TSLink 当前的 Tailscale 标签/设备自动化在该模式下更窄，因为这些操作依赖 Tailscale REST API。用于无人值守前请先验证所需的标签/设备操作。

`tslink login` 会交互式引导你完成任一 Tier 2 凭证路径；它不会先做一次无实际作用的临时 browser login。凭证优先存储在系统钥匙串（macOS Keychain / Linux secret service / Windows 凭据管理器）中。macOS 与 Linux 的 headless 环境可回退到受限权限文件。Windows 没有文件回退：TSLink 无法在本地证明该文件的 DACL 只允许当前用户访问，所以凭据管理器不可用时 `tslink login` 会直接失败。

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
| `tslink list` | 列出本机已注册的服务 |
| `tslink list --tailnet` | 只读：列出整个 tailnet 中所有带 TSLink 标签的设备，包括其它机器的服务和孤儿节点（需要已存储的 API 凭证） |
| `tslink share <path\|port\|host:port>` | 分享一个本地路径或 Web 端口，并打印它的 tailnet URL |
| `tslink url <name>` | 打印某个服务的准确运行时 URL |
| `tslink cleanup` | 回收过期的 Funnel 暴露和 TSLink 拥有的资源；默认只预览，传 `--dry-run=false` 才实际执行；即使没有本地服务在用，也保留共享的 Funnel ACL grant |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink serve --mcp` | 启动网关，并在专用的仅限 tailnet 节点上提供远程 MCP 控制面（必须配置 `mcp.allow`） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink status --urls` | 显示 owner-only 服务 URL、暴露模式、allow 摘要、后端和 warning code |
| `tslink doctor` | 只读诊断凭证、daemon、注册表、runtime snapshot、暴露模式、目标安全性和 Tailscale SSH 开启状态 |
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
| `tslink invite user <email>` | 邀请一位用户加入 tailnet；需要 user-owned API 访问令牌 |
| `tslink invite device <service> <email>` | 把某个 TSLink 拥有的服务设备分享给外部用户；需要确切的节点归属证明 |
| `tslink invite list` | 列出未完成的用户邀请和 TSLink 拥有的设备邀请 |
| `tslink invite revoke <id> --kind <user\|device>` | 撤销一个用户或设备邀请 |
| `tslink invite resend <id> --kind <user\|device>` | 重新发送用户或设备的邮件邀请 |
| `tslink mcp` | 面向 agent 的本地 stdio MCP server；不开网络 listener，不需要 `mcp.allow` |
| `tslink config` | 管理全局配置（set/get/list） |
| `tslink manifest` | 打印每个命令、flag、退出码和 error code 的机器可读描述 |
| `tslink registry check [path]` | 严格校验一个 `registry.json`，不做任何修改 |
| `tslink install` | 开机自启（macOS LaunchAgent / Linux systemd / Windows 启动文件夹） |
| `tslink uninstall` | 移除自启 |

### 退出码

| 退出码 | 含义 |
|------|------|
| `0` | 成功 |
| `1` | 一般运行时错误 |
| `2` | 用法、参数或 flag 错误 |
| `3` | 认证错误 |
| `4` | 冲突，例如 daemon 已在运行 |
| `5` | 请求的资源不存在 |
| `64` | 诊断 warning 阈值 |
| `65` | 诊断 critical 阈值 |

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
- **安全凭证管理** — 系统钥匙串存储，headless 的 macOS 与 Linux 环境支持受限权限文件后备
- **基于文件的注册表** — 服务在 `~/.config/tslink/registry.json` 中持久化，跨重启保存
- **热重载** — 注册表文件监听意味着 `tslink add` 无需重启服务即可生效
- **基于 PID 的生命周期** — 通过进程身份检查管理守护进程，并按平台明确停止行为
- **结构化日志** — 基于 slog 的结构化日志 + 访问日志
- **指标采集** — 内部记录请求指标；公开 `/metrics` 端点仍在 roadmap

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

# 运行只读诊断（只有传 --probe-external 才会探测非 loopback 目标）
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
    "schema_version": "vnext.1",
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

`--json` 只改变输出格式。`tslink add --json` 与人类路径使用同一套安全护栏：Funnel 服务必须传 `--public`；TCP 服务会拒绝 `--allow`，因为 TSLink 不会对原始 TCP 字节流应用 HTTP 身份检查。

## 跨机器操作

`~/.config/tslink/registry.json` 中的注册表属于单台机器，而 tailnet 是共享的。下面两个只读能力让这条边界可见；[远程 MCP 控制面](#远程-mcp-控制面)则让 tailnet 内另一台机器上的 agent 能操作这台机器的安装。

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

## 远程 MCP 控制面

`tslink serve --mcp` 在一个专用 tsnet 节点上，通过 HTTPS 在 `https://<node>.<tailnet>.ts.net/mcp` 提供与 `tslink mcp` 相同的 19 个 MCP tools。它是给 MCP client 用的 MCP endpoint，没有可供浏览器打开的页面。节点默认主机名为 `tslink-mcp`；其 tsnet 状态位于 `~/.config/tslink/mcp-node/`，与服务节点并列存放，而非混在其中。

控制面默认关闭。用 `--mcp` flag 或 `config.json` 中的 `mcp.enabled: true` 开启，二者任一生效。`mcp.allow` 是必填项，且只存在于 `config.json` 中，因为它是整个功能的安全边界（`tslink config set` 只管理 `control-url`，所以要直接编辑该文件）：

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com", "tag:ops"],
    "node_name": "tslink-mcp"
  }
}
```

| 事实 | 细节 |
|---|---|
| 默认 | 关闭。没有 `--mcp` 或 `mcp.enabled: true` 时，`serve` 不打开控制面 listener，也不创建控制面节点 |
| 授权 | `mcp.allow` 是登录邮箱和/或 `tag:` 条目的列表，与调用者的 Tailscale WhoIs 身份匹配。空列表或全是空白的列表会让 `serve` 拒绝启动，永远不表示「允许所有人」。所有拒绝都返回同一个 `403` JSON-RPC `forbidden` 响应体 |
| 可达范围 | 唯一的 listener 是控制面自己 tsnet 节点上的 `ListenTLS`。它永远不通过 Funnel 发布，永远不绑定主机网络接口或 `0.0.0.0` |
| 节点 | 专用节点，不与任何服务共用。它不是 registry 服务，因此不出现在 `tslink list` 中，也没有任何代码路径能给它加 `--funnel` |
| 生命周期 | 取决于 `serve` 的登录方式。已存凭证（`tslink login`）路径上节点是 ephemeral 的：派生出的 auth key 携带 ephemeral capability，tsnet 也以 ephemeral 标记登录，因此守护进程正常停止时会先登出节点，Tailscale 在几秒内把它从 tailnet 移除；关闭 `--mcp` 后没有需要手动删除的设备。如果守护进程崩溃，节点会留到 Tailscale 的 ephemeral 垃圾回收把它回收为止（Tailscale KB 写的是通常在最后活动后 30 到 60 分钟；这个数字来自 Tailscale KB，不是 TSLink 的实测）。零凭证（交互式浏览器登录）路径上节点是持久的、user-owned 的：一次浏览器授权在守护进程重启后仍然有效，关闭 `--mcp` 后 `tslink-mcp` 这台设备会留在 tailnet 里，需要你在 Tailscale admin console 手动删除 |
| 权限 | 通过授权的 peer 拥有完整控制权：注册和删除服务、通过 Funnel 把服务发布到公网、发送和撤销真实的 Tailscale 邀请。填写 `mcp.allow` 时按这个前提考虑；`serve` 每次启动都会以 warning 级别记录 `mcp.controlplane.enabled` |
| Origin | 带 `Origin` 头的请求必须与 endpoint 自身的 `https://<node>.<tailnet>.ts.net` origin 完全一致，遵循 MCP Streamable HTTP 传输规范；其它情况在授权之前即返回 `403`。不带 `Origin` 的请求（命令行 MCP client 就是这样）直接放行 |
| 传输 | 无状态 Streamable HTTP；单个请求体上限 1 MiB，与 stdio 传输每条记录的上限一致 |

**哪些 client 能连上。** 只有运行在你 tailnet 内某台机器上的 MCP client 能连接，例如笔记本或服务器上的 Claude Code。Claude Desktop 和 claude.ai 连不上：它们的 remote MCP 连接从 Anthropic 云端发起，不是从你的设备发起，因此到不了私有 tailnet 地址。这两者与 tslink 在同一台机器上时，请用 stdio 的 `tslink mcp`。

### `tslink mcp` 与 `tslink serve --mcp` 对照

| | `tslink mcp` | `tslink serve --mcp` |
|---|---|---|
| 传输 | stdio，newline-delimited JSON-RPC | Streamable HTTP，地址 `https://<node>.<tailnet>.ts.net/mcp` |
| 网络 listener | 无 | 专用 tsnet 节点上的 TLS listener，仅限 tailnet |
| 授权 | 启动它的本机用户 | `mcp.allow` 中的登录邮箱和/或 `tag:` 条目，必填 |
| 配置 | 无 | `--mcp` 或 `mcp.enabled`，加上 `config.json` 中的 `mcp.allow` |
| Tools | 19 个 | 同一组 19 个，来自同一个 tool registry |
| 典型 client | 本机上的 MCP client | tailnet 内另一台机器上的 MCP client |

## Roadmap / Experimental 包

仓库中包含一些尚未接入已交付 `tslink serve` 运行路径的包和注册表字段。除非后续有端到端集成测试证明，否则请把它们视为 roadmap 或 experimental：

| 领域 | 当前状态 |
|---|---|
| Docker 标签 | 未实现；注册表 schema 保留了这些字段，runtime 会以 `feature_unavailable` 拒绝。 |
| Middleware | 未实现；注册表 schema 保留了这些字段，runtime 会以 `feature_unavailable` 拒绝。 |
| Admin dashboard / REST API | 没有交付 dashboard 或 REST handler；仅限 tailnet 的 MCP 控制面（`tslink serve --mcp`）是唯一的远程管理面。未来的 dashboard 或 REST 工作必须显式标为 experimental，并补端到端测试。 |
| Prometheus `/metrics` | 内部 instrumentation 存在，但没有挂载 scrape endpoint。 |
| Custom domain / ACME | 字段保留但会以 `feature_unavailable` 拒绝；runtime TLS/ACME listener 尚未接入。 |
| Cluster sync | 未实现；注册表 schema 保留了这些字段，runtime 会以 `feature_unavailable` 拒绝。 |

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- Go 1.26.6+（如果从源码构建）

## 平台支持

| 平台 | 守护进程 | 自动启动 | 停止行为 |
|------|---------|---------|---------|
| macOS | `--daemon` | LaunchAgent | SIGTERM 优雅停止 |
| Linux | `--daemon` | systemd user service | SIGTERM 优雅停止 |
| Windows | `--daemon` | 启动文件夹 | 强制终止进程 |

配置与状态目录在 macOS 和 Linux 上是 `~/.config/tslink/`，在 Windows 上是 `%AppData%\tslink\`；设置 `TSLINK_CONFIG_DIR` 可以改用其他目录。本 README 其他地方写的 `~/.config/tslink/` 都指这个目录。Windows 上如果还留着旧的 `%USERPROFILE%\.config\tslink\`，TSLink 第一次解析配置目录时会把它移到 `%AppData%\tslink\`。移动失败时（例如启用了文件夹重定向）继续使用旧目录；两个目录同时存在时，TSLink 拒绝二选一，并报出两个路径。

macOS LaunchAgent 安装会使用 launchd `KeepAlive` 和 `ThrottleInterval=30`。如果 LaunchAgent 仍在安装状态，运行 `tslink stop` 后 launchd 会重启 TSLink。想禁用自启动时，先运行 `tslink uninstall`，再运行 `tslink stop`。`gui/$(id -u)` 是否可用取决于该 uid 是否存在桌面（Aqua）session，而不是调用者是否通过 SSH 连接；没有 Aqua session 时，`tslink install` 会先尝试 `gui/$(id -u)`，如果该 launchd domain 不可用则回退到 `user/$(id -u)`。Linux headless user service 如需登出后继续运行，可能需要执行 `loginctl enable-linger "$USER"`；如果 lingering 只为 TSLink 启用，卸载后运行 `loginctl disable-linger "$USER"`。

所有平台都支持通过重新运行 `tslink install` 来升级。macOS 上，TSLink 会在 launchd handoff 前保存已有 plist，并且只有 pidfile PID 与 `launchctl print` 报告的 PID 一致时，才把运行中的 daemon 视为 launchd 所有。升级中的 post-bootstrap verification 失败时，TSLink 会恢复旧 plist，并重新加载此前确认由 launchd 管理的 job；但它无法恢复在命令运行前已经被替换的可执行二进制。首次安装发生同类 verification 失败时，TSLink 会 bootout 新 job，并且只在 bootout 成功后删除新 plist。如果清理未完成，plist 会保留，使 `tslink uninstall` 能再次尝试。升级或卸载无法检查某个 launchd domain 时，默认会保留 plist 并以 `launchctl_domain_unavailable` 拒绝；JSON failure `data` 会给出 `unavailable_domain`、`force_available`、精确的 `force_command` 与 `force_risk`。只有在接受残余 daemon 风险时才运行相应的 `--force` 命令。强制卸载后，domain 恢复可寻址时先运行 `launchctl print gui/<uid>/com.tslink.daemon`（或相应的 `user/<uid>` target）检查；如果 job 仍已加载，再运行 `launchctl bootout gui/<uid>/com.tslink.daemon`（或相应的 `user/<uid>` target）将其移除。真实的 `launchctl` 错误即使在 `--force` 下也仍然致命。

Linux 上，TSLink 同样会在替换前保存已有 systemd user unit。如果 `daemon-reload`、`enable`、`restart` 或 restart 后验证失败，TSLink 会停止失败的 service，原子恢复旧 unit，重新加载 systemd，并重启此前已确认由 systemd 管理的 service。两个平台都无法恢复在 `tslink install` 运行前已被替换的可执行二进制。修复报出的原因后，重新运行 `tslink install`。

## 路线图

- [x] OAuth client secret 可由 login 和 tsnet auth 路径接受；无人值守前需验证标签/设备自动化
- [ ] 自定义域名 / ACME 运行时 TLS
- [ ] 可从 tailnet 访问的 Web 管理面板
- [ ] Docker 镜像和 Docker 标签发现
- [ ] Headscale 端到端测试
- [x] 每个命令的 `--json` envelope，以及 stdio 与仅限 tailnet 的 MCP 传输
- [ ] 经过集成测试的 Layer 2 模块和可选远程/管理面

#### 发布产物

当前还没有公开 tag/release，Homebrew tap 也尚未发布可安装产物。首个公开
release/readback 前请从源码安装。该外部 gate 通过后，GitHub Releases 预计发布以下可安装产物：

| 平台 | 产物 | 说明 |
|---|---|---|
| macOS | Homebrew cask 和 `tar.gz` 归档 | Homebrew cask 使用 GoReleaser `skip_upload: auto`，pre-release tag 可以跳过 tap upload 且不让发布失败。预发布验证优先使用归档产物。稳定版 macOS 二进制带 Developer ID 签名并经 Apple notarize，无论通过 cask 还是下载归档安装，Gatekeeper 都直接放行，前提是首次运行能联网向 Apple 查 notarization 票据（裸二进制无法 staple 票据）。pre-release tag 可能发出未签名归档，Gatekeeper 会拦，只用于验证。 |
| Linux | `.deb`、`.rpm` 和 `tar.gz` 归档 | 包内包含原生 `tslink` 二进制。安装后用 `tslink install` 注册 user service。 |
| Windows | `.zip` 归档 | Windows 当前是 archive-only 支持。尚未提供 MSI/MSIX/Winget 包或 Windows 代码签名安装器。解压后用 `tslink install` 注册 Startup 自启动。 |

发布产物是并列的 release assets，不是嵌入归档内部的文件。GoReleaser 会上传可安装归档/包、`checksums.txt`、归档对应的 CycloneDX SBOM sidecar，以及 `checksums.txt` 和 SBOM sidecar 的 keyless Sigstore bundle 签名。签名后的 `checksums.txt` 覆盖可安装产物和 SBOM sidecar。release workflow 还会为可安装产物和供应链 sidecar 发布 GitHub artifact attestations。

#### 验证发布完整性

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


## 文档

| 文档 | 内容 |
|---|---|
| [docs/mcp-clients.md](./docs/mcp-clients.md) | 通过 stdio 或 HTTP 接入 MCP client、19 个 tools、以及事件流契约（英文） |
| [docs/cli-manifest.json](./docs/cli-manifest.json) | 自动生成的机器可读描述，覆盖每个命令、flag、退出码和 error code |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | 开发环境、本地检查、CI 范围和贡献者权利（英文） |
| [SECURITY.md](./SECURITY.md) | 安全模型、边界，以及如何报告漏洞（英文） |
| [COMMERCIAL_zh.md](./COMMERCIAL_zh.md) | 商业使用与自愿合作 |
| [CHANGELOG.md](./CHANGELOG.md) | 版本历史（英文） |
| [AGENTS.md](./AGENTS.md) | 面向 AI agent 的操作手册（英文） |

## 贡献

欢迎贡献！请参阅 [CONTRIBUTING.md](./CONTRIBUTING.md) 了解指南。

## 安全

安全相关事宜请参阅 [SECURITY.md](./SECURITY.md)。

## 许可证

TSLink 采用 [Apache License 2.0](./LICENSE)。个人和任何规模的组织均可按许可使用、修改和分发，包括商业用途。TSLink 不收取软件许可费，也不要求注册、申报使用情况或满足规模门槛。

如果 TSLink 对你的组织有帮助，欢迎贡献代码，或洽谈维护、集成协助和定制开发。参与完全自愿；付费工作及支持承诺须另行书面约定。详见[商业使用与合作](./COMMERCIAL_zh.md)。

分发时请保留适用的许可和署名信息，参阅 [NOTICE](./NOTICE) 和 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。TSLink 是独立项目，其许可不授予 Tailscale 服务的使用权，也不代表官方背书；Tailscale 协议和套餐资格另行适用。

```
Copyright 2026 monody0007
```
