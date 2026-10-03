<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 标志">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>把电脑上的应用分享给你选择的人，分享多久由你决定。</strong><br>
  每个应用在你的 Tailscale 网络中都有独立的私有地址。查看谁能访问，也能随时收回权限。
</p>

<p align="center">
  <a href="#quickstart">快速开始</a> · <a href="#agents">给智能体</a> · <a href="getting-started.md">文档</a> ·
  <strong>简体中文</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">全部语言</a>
</p>

## 大家用它做什么

- **在手机上打开自己的工作成果。** 脚本生成的报告、开发服务器、Jupyter 笔记本或本地模型 API，都可以通过私有 HTTPS 地址，在获准的设备上访问。
- **把一个应用临时分享给一个人。** 让伴侣使用照片库一周，或让同事试用预览版三天。访问权限会自动到期，你也可以提前结束。
- **让智能体帮你分享。** 编程智能体刚做出了一个仪表盘，你可以让它分享给你和队友，开放到周五。它也能告诉你当前分享了什么，并收回分享。

应用继续在原来的地方运行。TSLink 管理每个应用的访问权限，并用一份列表记录分享了什么、分享给谁、到什么时候。

<a id="quickstart"></a>

## 快速开始

你需要 **Go 1.26.6+**、Git，以及一个[已启用 MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) 的 Tailscale 账户。目前尚未发布预编译版本，请从源码安装：

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

分享一个页面：

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

首次使用时，TSLink 会输出登录链接，用来注册新的服务节点；你的 tailnet 也可能要求管理员批准设备。注册完成后，在已登录你的 tailnet 且获准访问的设备上打开服务 URL。无需 API 令牌。

查看当前分享，然后移除演示服务：

```bash
tslink status --urls
tslink remove demo
```

后端启动后，你还可以分享这些内容：

| 内容 | 命令 |
|---|---|
| 本地 Web 应用 | `tslink share 3000` |
| 文件夹 | `tslink share ./public --name files` |
| 本地模型 API，例如 Ollama | `tslink add model --proxy localhost:11434` |
| 通过私有 TCP 访问的数据库 | `tslink add database --tcp localhost:5432` |
| 已支持的自托管应用（Jellyfin、Immich、Home Assistant 等，共 16 种） | `tslink apps detect`，然后执行 `tslink apps share jellyfin --yes` |

[入门、平台与后台服务 →](getting-started.md)

## 选择谁能打开

| 访问对象 | 接收方需要什么 | 身份依据 | 何时结束 |
|---|---|---|---|
| **你自己的设备** | 已登录你的 tailnet | 经验证的 Tailscale 登录身份 | 移除应用时 |
| **指定的人**（私有 HTTP/文件） | Tailscale 登录账户；网络外的人需要逐个接受应用邀请 | 经验证的 Tailscale 登录身份 | 你设定的期限到达时（`--for 7d`），或执行 `tslink people remove` |
| **任何持有 URL 的人**（Funnel） | 浏览器 | 任何人；仍需遵守应用自身的登录要求 | 默认 24 小时后（`--funnel-ttl`） |
| **浏览器访客链接** *（即将推出）* | 浏览器，以及可选的 PIN | 链接持有者 | 链接到期或被撤销时 |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

私有 HTTP 和文件分享会在每次请求时检查期限。撤销权限会阻止新请求；它无法收回已下载的数据，也不会关闭已接受的数据流和 WebSocket 连接。[按人分享 →](people.md) · [分享边界 →](sharing.md)

<a id="agents"></a>

## 给智能体

TSLink 内置 MCP 服务器，智能体可以像你一样分享、列出、解释和移除分享。在本地 MCP 客户端中加入：

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **准确的结果。** CLI 自动化支持 `--json`，使用 `schema_version: 1` 和稳定的错误码；`tslink mcp` 则使用 JSON-RPC。`tslink manifest` 描述每个命令和参数。智能体应通过 `tslink url <name> --wait` 获取真实 URL，而不要自行拼接。
- **如实报告等待状态。** 新节点如果还需要人完成登录，就会报告 `needs_login`，不会假装已经就绪。
- **操作权限。** 本地 MCP 以你的用户权限运行。远程 MCP 需要主动启用，只能在 tailnet 内访问，且仅允许你列出的登录身份或标签。每个智能体的角色、应用范围和操作回执*即将推出*。

TSLink 的 MCP 用来操作 TSLink 本身。如果你通过 TSLink 发布其他 MCP 服务器，该服务器仍然需要自己的工具权限控制。
[智能体指南 →](agents.md) · [MCP 客户端 →](mcp-clients.md) · [远程 MCP →](remote-mcp.md) · [JSON 自动化 →](json-automation.md)

## 什么时候选择其他工具

| 你的需求 | 可以考虑 |
|---|---|
| 使用已有的 Tailscale 客户端，在自己的设备上访问一个本地服务 | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| 由管理员管理、跨多个主机使用稳定名称的服务 | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| 无需 Tailscale 账户的 webhook 或 API 演示公共 URL | [ngrok](https://ngrok.com/docs/start) 或 [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| 安装并运行自托管应用，而不只是分享 | [Umbrel](https://umbrel.com) 或 [Coolify](https://coolify.io) |
| 覆盖整个组织、根据身份控制访问的平台 | [Pangolin](https://github.com/fosrl/pangolin) 或 [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

如果一个人运行着多个应用，想按应用、按人设置有期限的访问权限，并让自己和智能体都能查看，TSLink 就适合这种场景。

## 工作原理

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App、Docs、Database 和 Model 是同一 tailnet 中独立命名的节点，由发布端电脑上的一个 TSLink 后台进程运行。" width="720">
</picture>

一个后台进程为每个应用运行一个嵌入式 Tailscale 节点，因此每个应用都有自己的名称和地址。私有 HTTP 和文件分享通过 `WhoIs`、按人授权或 `--allow` 规则控制访问，每次请求都会检查按人授权的期限。原始 TCP 使用 tailnet 策略和后端自身的认证。Tailscale 提供 tailnet 传输、加密和证书；TSLink 是独立项目。所有应用共用发布端电脑，TSLink 不会将它们彼此隔离。[架构 →](architecture.md)

## 当前状态

现已可用：每个应用的私有地址、带期限和邀请包的按人分享、带到期时间的公共 Funnel、应用健康检查与告警、自托管应用配方、每个应用的请求限制、Windows 崩溃重启、CLI 和 MCP。 同时提供：浏览器访客链接、灵活的时长、访问日志、应用首页、限定范围的智能体角色、QR 扫码引导和访问申请。

将多台电脑的应用汇总到一个列表中仍在规划中。[路线图 →](roadmap.md)

## 文档与许可

[llms.txt](../llms.txt) · [智能体快速入门](agent-quickstart.md) · [如何选择共享工具](comparison.md)

[入门](getting-started.md) · [CLI 参考](cli-reference.md) · [平台](platforms.md) · [本地模型](local-ai.md) · [贡献](../CONTRIBUTING.md) · [安全](../SECURITY.md)

采用 Apache License 2.0，允许商业使用。重新分发时请保留 [NOTICE](../NOTICE) 和[第三方声明](../THIRD_PARTY_NOTICES.md)。Tailscale 的服务条款与套餐另行适用。
