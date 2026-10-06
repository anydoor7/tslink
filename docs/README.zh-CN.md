<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>电脑或服务器上的每个应用，都能在你的 Tailscale 网络里拥有自己的私有地址，谁能访问，由你决定。</strong></p>

用自己的手机和笔记本电脑打开 Web 应用、文件夹、模型 API 和数据库，每个应用都有健康检查和访问记录。你的 AI 智能体可以在你分配的角色范围内，为它在 localhost 上启动的应用分配私有地址，供你的其他设备打开，并检查这些应用。需要让别人访问时，可以给指定的人开放到某个日期，或把一个 Web 应用限时开放到公网。

**需要 Tailscale。** 你需要一个 Tailscale 账户（个人使用免费），每台要打开私有应用的设备都要安装 Tailscale 客户端；访客和公网访问者只需要浏览器。TSLink 是独立项目，不由 Tailscale 开发，也未获 Tailscale 认可。[使用要求](#requirements)

<p align="center"><a href="#quickstart">快速开始</a> · <a href="#agents">面向智能体</a> · <a href="comparison.md">与 Serve、ngrok 和 Cloudflare 的对比</a> · <a href="#documentation">文档</a></p>
<p align="center">
<a href="../README.md">English</a> · <strong>简体中文</strong> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 你可以做什么

### 访问自己的应用

- **每个应用一个地址。** `tslink share 3000`、`tslink share ./photos` 或 `tslink add db --tcp localhost:5432` 会在你的 tailnet（你的 Tailscale 私有网络）里，为 Web 应用、文件夹、文件或 TCP 服务分配独立的私有地址，例如 `https://photos.<tailnet>.ts.net`。每个应用在 Tailscale 里都是一台单独的设备，所以你按名称打开应用，不用记 IP 地址和端口。
- **默认私有。** TSLink 默认只提供私有的应用访问，哪些设备能连接由你的 tailnet 策略决定。只有你创建访客链接或明确通过 Funnel 公开发布，它才会开放公网上的应用入口。
- **在一处查看。** `tslink status --urls` 列出在这台电脑上注册的应用；可选的私有入口页显示它们的地址和健康状态。[入口页](portal.md)
- **出了问题及时知道。** 应用宕机或恢复、Tailscale 登录快要过期，后台健康检查都可以通过命令或 webhook 提醒你。访问记录列出每次访问的人、应用和时间，包括遭到拒绝的请求。[健康检查与告警](health-and-alerts.md) · [访问记录](access-log.md)
- **应用配方。** 配方覆盖 15 个自托管应用，包括 Home Assistant、Jellyfin、Immich 和 Ollama；`tslink apps detect` 可以找出本机已在监听的受支持应用。要上传大的照片和视频，请[为对应应用调高上传限制](sharing.md)。[应用配方](apps.md) · [本地 AI](local-ai.md)

### 让智能体来操作

如果智能体在 `localhost` 上启动开发服务器、预览页面或本地模型 API，你的手机和其他电脑就访问不到这个地址。TSLink 让智能体为它分配私有地址，告诉你准确的 URL，用完再撤销它的注册，全程不超出你设定的限制。

- **分享、检查、撤销。** `share --json` 返回它注册的名称，以及准确的 URL 或一个供你打开的登录链接。`url <name> --wait` 和 `status --urls --name <name>` 报告入口是否就绪，`remove <name>`（在 MCP 中是 `unshare`）移除这次分享。[智能体快速开始](agent-quickstart.md)
- **为自动化设计。** 除 `tslink mcp` 以外的管理命令都支持 `--json`，返回带版本号的结果和稳定的错误码。`tslink mcp` 通过 JSON-RPC 把应用和访问相关的工具提供给本机的 MCP 客户端。配置好调用方绑定后，`tslink serve --mcp` 会通过 tailnet 把这些工具提供给你其他设备上的 MCP 客户端。[JSON 自动化](json-automation.md) · [远程 MCP](remote-mcp.md)
- **权限有限。** 本机智能体默认拥有所有者权限。你可以给智能体分配较低的角色（`viewer`、`app-operator` 或 `people-manager`），只能管理你指定的应用，它发出的授权也有最长期限。通过 MCP 做的更改都会记录，可以用 `tslink mcp-audit` 查看。角色只限制 TSLink 的工具，管不到智能体自己的 shell 和文件。[MCP 权限](mcp-scopes.md)

### 分享给你选定的人

- **指定的人，到期为止。** `tslink people add alice@example.com --apps photos,notes --for 7d` 让这个 Tailscale 登录账号在截止日期前打开这些 Web 应用和文件应用。`people update`、`extend` 和 `people remove` 用来修改或结束授权；移除后，对方的下一个请求就会遭到拒绝，但已经下载的内容收不回来。[人员授权](people.md) · [期限](durations.md)
- **tailnet 以外的人。** 加上 `--invite --print-links`，会生成一条可以直接发出去的消息，里面有每个应用的设备邀请（需要用户名下的 API 令牌）。`--qr` 会打印一个二维码，方便在手机上设置。
- **访问申请。** tailnet 里的人可以在入口页申请延长时间，或申请你标为可申请的应用。你用一条命令批准，同时指定期限。[访问申请](requests.md)

### 把 Web 应用限时开放到公网

- **访客链接。** `tslink guest create photos --for 3d --public --print-link` 为一个 Web 应用生成浏览器链接，可以加 PIN，也可以单独撤销。访客不需要 Tailscale 账户。任何拿到链接的人都能用，所以它无法证明来访者是谁。[访客链接](guest-links.md)
- **开放的公网 URL。** `tslink add preview --proxy localhost:3000 --funnel --public` 把一个 Web 应用公开给任何知道 URL 的人。新建的公网发布默认有效 24 小时，可以用 `--funnel-ttl` 选择别的时长。[Funnel](funnel.md)
- 新建的访客链接和开放的公网发布都通过 Tailscale Funnel 运行，有效期有限（最短 1 小时，默认最长 7 天，所有者可以调整这个上限）。这些公网路径支持 HTTP 代理应用；直接提供的文件夹或文件服务，以及原始 TCP，都保持私有。

以上功能都包含在 v0.1.0 中。

<a id="requirements"></a>

## 使用要求

TSLink 基于 Tailscale 构建。它是独立项目，不由 Tailscale 开发，也未获 Tailscale 认可；Tailscale 自己的条款和[套餐](https://tailscale.com/pricing)同样适用。

| 谁 | 需要什么 |
|---|---|
| 你 | 一个 Tailscale 账户，并开启 [MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)。免费的 Personal 套餐仅限非商业用途。 |
| 运行应用的电脑或服务器 | 只需要 TSLink。它内置 Tailscale，不用另外安装 Tailscale。按默认设置，每个新建的应用节点都要在浏览器里登录，可能还需要设备审批。改用[已保存的凭据](credentials-and-tags.md)，应用加入 tailnet 就不必逐个在浏览器里登录。 |
| 你的其他设备 | Tailscale 客户端，并登录到你的 tailnet。 |
| 你选定的人 | Tailscale 客户端和他们自己的登录账号。他们可以加入你的 tailnet（会在你的套餐里多占一个用户名额），也可以为每个应用接受一份设备邀请。你的 tailnet 策略必须允许他们访问。 |
| 访客和公网访问者 | 一个浏览器。你的 tailnet 必须允许 Funnel，Tailscale 目前仍把它标为 beta。 |

为某个应用签发 HTTPS 证书后，它的 Tailscale 设备名称和你的 tailnet DNS 名称会出现在公开的证书日志里。请给应用选公开了也无妨的名称。

<a id="installation"></a>
<a id="quickstart"></a>

## 快速开始

在 macOS 或 Linux 上用 Homebrew 安装。macOS 版二进制文件使用 Developer ID 证书签名，并已通过 Apple 公证。以后升级时运行 `brew upgrade --cask tslink`；如果 TSLink 作为后台服务运行，再执行一次 `tslink install`。

```bash
brew install --cask anydoor7/tap/tslink
```

Windows 用户请从[最新发行版](https://github.com/anydoor7/tslink/releases/latest)下载 `tslink_<version>_windows_<arch>.zip`，用 `checksums.txt` 核对后运行 `tslink install`，让 TSLink 在登录时自动启动。该 zip 没有 Authenticode 签名，请通过已签名的校验和与构建证明[验证发行版](verify-release.md)。Linux 的 `.deb` 和 `.rpm` 安装包也在同一发行页面上。如需从源码构建，需要 **Git 和 Go 1.26.6+**。以下命令适用于 bash/zsh，参见 [macOS、Linux 和 Windows 配置](platforms.md)。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

你需要 **Tailscale 账户**以及 [MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)。私有访问的接收设备需要 Tailscale 和网络策略许可。应用主机上的 Tailscale 由 TSLink 内嵌提供。

假设应用已在 3000 端口运行：

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

使用尚未占用的名称；若 `share` 返回另一个名称，请在 `url` 中使用返回值。先完成输出提示的浏览器注册和设备审批，再在获准设备上打开准确的应用 URL。`share` 会按需启动后台服务。首次私有访问不需要管理员 API 令牌。文件可用 `tslink share ./report.html` 分享；应用必须已运行，文件必须已存在。[完整配置](getting-started.md)

成功用起来后，如果觉得有帮助，欢迎[给 TSLink 点个 Star](https://github.com/anydoor7/tslink)，帮助更多人发现它。完全自愿。

<a id="architecture"></a>

## 它如何工作

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="一台电脑或云服务器：CLI/MCP 管理共享守护进程和各应用节点。私有设备通过 Tailscale 加密连接；可选的公开 HTTPS/Funnel 通过访客验证或明确的公开发布访问 HTTP 应用。" width="960">
</picture>

可以把它理解为通向应用的私有加密通道。**Tailscale 提供网络传输和 HTTPS；TSLink 在每台主机上管理应用访问。** 一个守护进程为每项服务运行独立的内嵌节点。私有入口页列出获准访问的应用，健康状态和访问记录帮助日常维护。

公开访问需主动启用：访客需要链接和可选 PIN；开放的 Funnel 则允许任何持有 URL 的人访问。两者都使用公开 HTTPS，而非私有用户身份验证。原始 TCP 保持私有，依赖 tailnet 策略和后端认证。TSLink 不安装应用、不隔离主机进程、不创建云 VPC，也不聚合多台主机。与 Tailscale 配合使用的独立项目。[架构与边界](architecture.md)

<a id="agents"></a>

## 面向智能体

通过 CLI 或 MCP 管理应用清单、健康状态、URL 和权限。从[智能体快速开始](agent-quickstart.md)入手；读取实际工具 schema，并验证应用确实可访问后再报告成功。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI 自动化使用 `--json`；MCP 通过 stdio 使用 JSON-RPC。[客户端配置](mcp-clients.md) · [远程 MCP](remote-mcp.md) · [角色与应用范围](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## 文档与许可证

[全部指南](INDEX.md) · [CLI 参考](cli-reference.md) · [本地 AI](local-ai.md) · [健康检查](health-and-alerts.md) · [访问记录](access-log.md) · [路线图](roadmap.md)

多主机应用清单仍在规划中。欢迎[贡献](../CONTRIBUTING.md)和[报告安全问题](../SECURITY.md)。[Apache 2.0](../LICENSE) 允许商业使用；再分发时保留 [NOTICE](../NOTICE) 和[第三方声明](../THIRD_PARTY_NOTICES.md)。[商业合作](../COMMERCIAL.md)完全自愿。Tailscale 的条款和套餐另行适用。
