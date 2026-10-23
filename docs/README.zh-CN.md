<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink 是开源的命令行工具和 MCP 服务，给你电脑上的每个应用一个形如 <code>&lt;名字&gt;.&lt;tailnet&gt;.ts.net</code> 的私有 HTTPS 地址，让你或你的 coding agent 能在手机上打开正在运行的 dev server。TSLink 是独立项目，不是 Tailscale 出品。</p>
<p align="center">手机需要装 Tailscale，并登录同一个 tailnet。</p>
<p align="center"><a href="../README.md">English</a> · <strong>简体中文</strong> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">更多语言</a></p>

```text
# Claude Code，先装好命令行工具（见下面的安装）
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# 使用 mcpServers JSON 的本地 MCP 客户端（其他客户端请看它自己的 MCP 配置说明）
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

对 agent 说：把 dev server 分享出来，我要在手机上打开。
```

手动做同样的事，前提是 dev server 已在 5173 端口监听。加上 `--json` 后，share 会在 `data.name` 里报告注册的名字。后面的命令要用这个名字，它和 `--name` 不一定相同，别的服务已经用了 web 时，会是 web-2 这样的名字。

```sh
tslink share 5173 --name web --json   # data.name 是名字；data.url 是确切的 URL，status 为 needs_login 时 data.auth_url 是 Tailscale 登录链接
tslink url <名字> --wait              # 完成登录后，取得确切的 URL
tslink remove <名字>                  # 用完移除；share 失败就不要执行。dev server 继续运行
```

<a id="installation"></a>
<a id="quickstart"></a>

## 安装

macOS 主机需要 macOS 13 Ventura 或更新版本（[平台支持](platforms.md)）。

```sh
brew install --cask anydoor7/tap/tslink
```

在 Windows 上，使用 Scoop 安装：

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

升级时运行 `brew upgrade --cask tslink` 或 `scoop update; scoop update tslink`。如果 TSLink 作为后台服务运行，升级后请再次运行 `tslink install`。

Linux 的 `.deb`、`.rpm` 安装包和 Windows 版本在[最新发布](https://github.com/anydoor7/tslink/releases/latest)页面。第一次分享应用时，TSLink 会给出这个应用的 Tailscale 登录链接。[上手指南](getting-started.md)

<a id="why"></a>

## 什么时候需要 TSLink

只在自己的设备上打开一个应用，用 Serve 就够了。TSLink 把应用地址、期限和访问变更放进同一套流程。

| 场景 | 只用 Tailscale | TSLink |
|---|---|---|
| 在手机上打开一个 Web 应用 | `tailscale serve 3000` 就够了 | `tslink share 3000` |
| 多个应用，各用各的名字 | 配置 Services，或分别运行节点 | 每个应用一次 `share`/`add`，每个节点分别登录 |
| 一个人，一个应用，七天 | 先写策略规则，再用 JIT 临时授权工具或手动移除 | `tslink people add alice@example.com --apps photos --for 7d`（HTTP/文件） |
| 浏览器链接，三天 | 公开的 Funnel；自己加访问关卡，并安排到期关闭 | `tslink guest create photos --for 3d --public --print-link`（仅 HTTP） |

私有访问的对象需要安装 Tailscale。访客链接是公开且可转发的访问凭证。

[完整对比](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## 你的应用，在自己的设备上打开

- **每个应用一个地址。** Web 应用、文件夹、单个文件和 TCP 端口在你的 tailnet 里各有自己的名字，用名字就行，不用记 IP 地址。
- **默认私有。** 只有在你创建访客链接或通过 Funnel 发布之后，才会有公开入口。
- **只开放这个应用。** 你发布的每个应用都有自己的节点，只转发到这个应用。主机上没有安装 Tailscale 客户端时，TSLink 不会把主机的其他端口加进你的 tailnet。
- **一个入口页**，列出你的应用和它们的健康状态。[入口页](portal.md)
- **健康检查与告警**，通过命令或 webhook 通知；访问记录也包括遭到拒绝的请求。[健康检查与告警](health-and-alerts.md) · [访问记录](access-log.md)
- **15 个自托管应用的配方**，包括 Home Assistant、Jellyfin、Immich 和 Ollama。`tslink apps detect` 会找出已经在运行的那些。[应用配方](apps.md)

## 想分享时再分享

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

访客链接和新建的公网 URL 会过期，而且只适用于 Web 应用；文件夹、文件和 TCP 端口始终保持私有。[人员授权](people.md) · [访客链接](guest-links.md) · [公网访问](funnel.md)

<a id="agents"></a>

## 面向 AI 智能体

智能体在 `localhost` 上启动的开发服务器，你的手机访问不到。有了 TSLink，智能体可以给它一个私有地址，报告准确的 URL，用完再移除。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI 或 MCP。** 管理命令支持 `--json`，返回带版本号的结果；`tslink mcp` 通过 MCP 提供应用和访问管理操作。
- **只分享一个文件。** 智能体可以用 `tslink share ./report.html` 只分享它的 HTML 报告；同一文件夹里的其他文件仍然访问不到。
- **有限的角色。** `viewer`、`app-operator` 或 `people-manager`，权限范围限于你指定的应用。`tslink mcp-audit` 显示智能体改了什么。角色只限制 TSLink 的工具，管不到智能体自己的 shell。

[智能体快速开始](agent-quickstart.md) · [MCP 权限](mcp-scopes.md) · [远程 MCP](remote-mcp.md)

<a id="architecture"></a>

## 它如何工作

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="一台电脑或云服务器：CLI/MCP 管理共享守护进程和各应用节点。私有设备通过 Tailscale 加密连接；可选的公开 HTTPS/Funnel 通过访客验证或明确的公开发布访问 HTTP 应用。" width="960">
</picture>

一个后台进程为每个应用运行独立的 Tailscale 节点。Tailscale 提供 tailnet 传输和 HTTPS 证书。私有的 Web 和文件访问可以用 `--allow` 和人员授权按 Tailscale 身份限制；原始 TCP 依靠你的 tailnet 策略和应用自己的登录。[架构](architecture.md)

<a id="requirements"></a>

## 使用要求

| 谁 | 需要什么 |
|---|---|
| 你 | 一个开启了 MagicDNS 和 HTTPS 的 Tailscale 账户 |
| 运行应用的机器 | TSLink，它内置了 Tailscale；在 Linux 上还需要 systemd 用户会话 |
| 你的设备，以及你分享的对象 | Tailscale 客户端 |
| 访客 | 浏览器 |

HTTPS 应用的名称会出现在公开的证书日志里，请选你不介意别人看到的名字。

<a id="documentation"></a>

## 更多

[全部文档](INDEX.md) · [CLI 参考](cli-reference.md) · [与 Serve、ngrok 和 Cloudflare 的对比](comparison.md) · [贡献](../CONTRIBUTING.md) · [安全](../SECURITY.md)

Apache 2.0。TSLink 是独立项目，不由 Tailscale 开发，也未获 Tailscale 认可。
