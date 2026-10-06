<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>把自托管应用分享给你选定的人，分享多久由你决定。</strong></p>

TSLink 为你电脑或服务器上的每个应用分配独立的 Tailscale 私有地址。你可以给指定的人授权到某个期限，给不用 Tailscale 的人发送浏览器访客链接，并且一条命令就能撤销任一种访问。可以亲自操作，也可以交给 AI 智能体，它只能在你分配的角色范围内行事。与 Tailscale 配合使用的独立项目。

<p align="center"><a href="#quickstart">快速开始</a> · <a href="#agents">面向智能体</a> · <a href="comparison.md">与 Serve、ngrok 和 Cloudflare 的对比</a> · <a href="#documentation">文档</a></p>
<p align="center">
<a href="../README.md">English</a> · <strong>简体中文</strong> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 让应用触手可及

| 你的需要 | TSLink 提供的能力 |
|---|---|
| 跨设备使用自己的应用 | 为电脑或服务器上的家庭面板、仅本机可用的网页、文件、模型 API 和 TCP 服务提供私有地址。 |
| 分享给指定的人 | 为指定 HTTP/文件应用设置访问期限并随时撤销，通过真实 Tailscale 登录身份验证；接收方需要使用 Tailscale。[人员授权](people.md) |
| 让访客用浏览器打开 | 为 HTTP 代理应用创建有期限、可选 PIN 的访客链接，或明确启用 Funnel 公开 HTTPS。访客链接可被转发，不能证明访问者身份。[访客链接](guest-links.md) |
| 持续管理一组应用 | 每台主机的应用清单、私有入口页、健康检查与告警、访问记录，以及支持智能体角色、应用范围和审计回执的 CLI/MCP 访问管理。[入口页](portal.md) · [MCP 权限](mcp-scopes.md) |

[应用配方](apps.md)、[上传限制](sharing.md)、[灵活期限](durations.md)和[二维码引导与访问申请](requests.md)让日常维护更方便。这些能力随 v0.1.0 发布。

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
