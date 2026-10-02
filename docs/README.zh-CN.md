<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 标志">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>让本地应用、模型和文件拥有自己的私有地址。</strong><br>
  从 Tailscale 网络里的另一台获准设备访问它们。
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="许可证：Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 或更高版本"></a>
  <a href="architecture_zh.md"><img src="assets/badge-tsnet.svg" alt="Tailscale：内嵌 tsnet 节点"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP：19 个工具"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <strong>简体中文</strong> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## 安装

需要 **Go 1.26.6+** 和 Git。尚未发布预构建安装包或 Homebrew cask，请从源码安装。示例使用 **bash 或 zsh**；Windows 与后台服务要求见[平台支持](platforms_zh.md)。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

使用已[启用 MagicDNS 与 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) 的 Tailscale 账户。接收设备需登录你的 Tailscale 网络（**tailnet**），并由网络策略允许访问服务。发布端的 TSLink 已内嵌 Tailscale。

### 分享你的第一个页面

创建一个页面，TSLink 会直接提供访问，并按需启动后台服务：

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

若 TSLink 输出节点授权 URL，打开它完成授权；tailnet 可能还要求管理员审批设备。然后获取准确地址：

```bash
tslink url demo --wait
```

在获准设备上打开返回的 URL。首次分享无需 API token。[完整设置与生命周期说明 →](getting-started_zh.md)

<a id="use-cases"></a>

## 你想分享什么？

文件需要事先存在；应用、数据库和模型后端需要已运行并监听指定端口。

| 使用场景 | 命令 |
|---|---|
| 从另一台设备打开本地应用 | `tslink share 3000` |
| 浏览目录里的文件 | `tslink share ./public --name files` |
| 在手机阅读生成的 HTML 报告 | `tslink share ./report.html --name report` |
| 通过 TCP 连接本地数据库 | `tslink add database --tcp localhost:5432` |
| 调用 Ollama 等本地模型 HTTP API | `tslink add model --proxy localhost:11434` |

使用 Ollama 时，先用 `tslink url model --wait` 获取准确 URL；OpenAI API 兼容客户端的 `baseURL` 是这个 URL 加上 `/v1`。[本地模型与私有数据工作流 →](local-ai_zh.md)

在一台主机上管理多个应用时，TSLink 提供命名服务节点、HTTP 身份允许名单、Funnel 有效期和 MCP 管理。若只在自己的设备间访问一个应用，[Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) 可能已经够用。

<a id="architecture"></a>

## 架构

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="服务关系示例：App、Docs、Database 和 Model 是同一个 tailnet 内各自独立的命名节点。应用、文件与模型 API 使用 HTTPS，数据库使用私有 TCP。" width="960">
</picture>

**一个 tailnet，多个服务节点。** 同一个守护进程为每个服务运行内嵌 tsnet 节点，转发 HTTP、提供文件访问或代理 TCP。注册表改动会在运行期间生效。每个节点有自己的网络身份，服务共用发布端主机。[架构详解 →](architecture_zh.md)

| 技术 | 用途 |
|---|---|
| [Go](../go.mod) | 原生命令行程序 |
| [Tailscale tsnet](architecture_zh.md) | 服务节点与 tailnet 传输 |
| [Cobra](https://github.com/spf13/cobra) | 命令与帮助 |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Agent 传输 |
| 系统钥匙串与用户服务管理器 | 可选凭证存储与后台运行 |

服务仅在 tailnet 内提供访问，除非你显式启用[公网 Funnel](getting-started_zh.md#更多示例)。HTTP/文件服务支持按身份设置允许名单 （`WhoIs`、`--allow`）；原始 TCP 依赖 tailnet 策略和后端自身的认证。详情见[分享边界](sharing_zh.md)。

TSLink 不安装应用、不运行模型、不隔离主机进程，也不汇总多台主机。网络传输、加密和 HTTPS 由 Tailscale 提供；TSLink 是独立项目。

<a id="agents"></a>

## 面向 agent

**19 个 MCP 工具**让 agent 分享报告、管理服务、获取 URL 和检查设置。本机 MCP 客户端可以这样连接已安装的程序：

```json
{
  "mcpServers": {
    "tslink": {
      "command": "tslink",
      "args": ["mcp"]
    }
  }
}
```

MCP 管理 TSLink；应用使用模型 HTTP API 执行推理。配置与自动化方法见 [MCP 客户端](mcp-clients.md)、[远程 MCP](remote-mcp_zh.md)和 [agent 操作指南](../AGENTS.md)。

CLI 自动化支持 `--json`，其中 `schema_version` 为 `1`；可用 `tslink status --urls --json` 查看。本机 MCP 使用 stdio 上的 JSON-RPC。详见 [JSON 自动化](json-automation_zh.md)。

<a id="roadmap"></a>

## 即将推出

标为合并中、审查中或计划中的项目尚未包含在上面的源码安装中。

| 使用场景 | 状&#8288;态 |
|---|---|
| <!-- roadmap:people --> 给亲友 3 天的私有 HTTP/文件应用访问权限，并将各应用邀请合成一条消息；接收者仍需 Tailscale。 | 合&#8288;并&#8288;中 |
| <!-- roadmap:health --> 检查应用健康状态，并通过可选的命令或 webhook 接收掉线或到期提醒。 | 合&#8288;并&#8288;中 |
| <!-- roadmap:recipes --> 发现支持的回环地址应用，并在分享前预览自托管应用配方。 | 合&#8288;并&#8288;中 |
| <!-- roadmap:limits --> 为每个 HTTP 应用设置上传大小和请求超时，以适应大文件上传和慢速客户端。 | 合&#8288;并&#8288;中 |
| <!-- roadmap:windows --> 通过计划任务和内置监护进程，在用户已登录 Windows 时重启崩溃的守护进程。 | 合&#8288;并&#8288;中 |
| <!-- roadmap:access-log --> 通过本地访问记录查看谁打开了哪个应用，并选择 `prefix`、`full` 或 `off` 路径记录模式。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:portal --> 用一个主页列出访客获准访问的应用，并向所有者提供节点授权交接信息；访客仍需 Tailscale。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:mcp-scopes --> 给 agent 分配角色和应用范围，并记录其修改操作的审计回执。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:guest-links --> 让访客无需安装 Tailscale，通过受控的公网 Funnel，在浏览器中用限时链接和可选 PIN 打开一个 HTTP 应用。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:durations --> 选择预设或自定义有效期，最短 1 小时，访客默认最长 7 天且可配置。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:requests --> 帮助手机用户通过二维码加入，让所有者一步批准应用访问或延时请求。 | 审&#8288;查&#8288;中 |
| <!-- roadmap:multi-host --> 在一份清单中查看多台主机上的应用。 | 计&#8288;划&#8288;中 |

<a id="documentation"></a>

## 文档与许可

[入门](getting-started_zh.md) · [本地模型](local-ai_zh.md) · [CLI 参考](cli-reference_zh.md) · [平台](platforms_zh.md) · [路线图](roadmap_zh.md)

贡献方法见 [CONTRIBUTING.md](../CONTRIBUTING.md)；漏洞报告请使用 [SECURITY.md](../SECURITY.md) 中的渠道。

TSLink 使用未经修改的 [Apache License 2.0](../LICENSE)，允许按该许可用于商业用途。分发时请保留适用的 [NOTICE](../NOTICE) 和[第三方声明](../THIRD_PARTY_NOTICES.md)。[商业合作](../COMMERCIAL_zh.md)完全自愿，不增加许可条件。Tailscale 服务条款与套餐另行适用。
