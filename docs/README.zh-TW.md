<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 標誌">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>讓本機應用程式、模型與檔案擁有各自的私有位址。</strong><br>
  從 Tailscale 網路中的另一台獲准裝置存取它們。
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="授權：Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 或更新版本"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale：內嵌 tsnet 節點"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP：19 個工具"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>繁體中文</strong> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## 安裝

需要 **Go 1.26.6+** 與 Git。尚未發布預先建置的安裝包或 Homebrew cask，請從原始碼安裝。範例使用 **bash 或 zsh**；Windows 與背景服務需求見[平台支援](platforms.md)。中文詳細指南目前使用簡體中文。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

使用已[啟用 MagicDNS 與 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) 的 Tailscale 帳號。接收端裝置需登入你的 Tailscale 網路（**tailnet**），並由網路原則允許存取服務。發布端的 TSLink 已內嵌 Tailscale。

### 分享你的第一個網頁

建立一個網頁，TSLink 會直接提供存取，並在需要時啟動背景服務：

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

若 TSLink 輸出節點授權 URL，開啟它完成授權；tailnet 可能還要求管理員核准裝置。接著取得確切位址：

```bash
tslink url demo --wait
```

在獲准裝置上開啟傳回的 URL。首次分享不需要 API token。[完整設定與生命週期說明 →](getting-started.md)

<a id="use-cases"></a>

## 你想分享什麼？

檔案需要事先存在；應用程式、資料庫與模型後端需要已執行並接聽指定連接埠。

| 使用情境 | 指令 |
|---|---|
| 從另一台裝置開啟本機應用程式 | `tslink share 3000` |
| 瀏覽目錄中的檔案 | `tslink share ./public --name files` |
| 在手機閱讀產生的 HTML 報告 | `tslink share ./report.html --name report` |
| 透過 TCP 連線至本機資料庫 | `tslink add database --tcp localhost:5432` |
| 呼叫 Ollama 等本機模型 HTTP API | `tslink add model --proxy localhost:11434` |

使用 Ollama 時，先用 `tslink url model --wait` 取得確切 URL；OpenAI API 相容用戶端的 `baseURL` 是該 URL 加上 `/v1`。[本機模型與私有資料工作流程（簡體中文）→](local-ai.md)

在一台主機上管理多個應用時，TSLink 提供命名服務節點、HTTP 身分允許名單、Funnel 有效期限與 MCP 管理。若只在自己的裝置間存取一個應用，[Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) 可能已經足夠。

<a id="architecture"></a>

## 架構

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="服務關係範例：App、Docs、Database 與 Model 是同一個 tailnet 內各自獨立的具名節點。應用程式、檔案與模型 API 使用 HTTPS，資料庫使用私有 TCP。" width="960">
</picture>

**一個 tailnet，多個服務節點。** 共用的守護程序為每個服務執行內嵌 tsnet 節點，轉發 HTTP、提供檔案存取或代理 TCP。登錄資料的變更會在執行期間生效。每個節點有自己的網路身分，服務共用發布端主機。[架構詳解 →](architecture.md)

| 技術 | 用途 |
|---|---|
| [Go](../go.mod) | 原生命令列程式 |
| [Tailscale tsnet](architecture.md) | 服務節點與 tailnet 傳輸 |
| [Cobra](https://github.com/spf13/cobra) | 指令與說明 |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Agent 傳輸 |
| 系統鑰匙圈與使用者服務管理器 | 選用的憑證儲存與背景執行 |

服務僅在 tailnet 內提供存取，除非你明確啟用[公開 Funnel](getting-started.md#more-examples)。HTTP/檔案服務支援依身分設定允許清單 （`WhoIs`、`--allow`）；原始 TCP 依賴 tailnet 原則與後端本身的驗證。詳見[分享範圍](sharing.md)。

TSLink 不安裝應用、不執行模型、不隔離主機程序，也不彙整多台主機。網路傳輸、加密與 HTTPS 由 Tailscale 提供；TSLink 是獨立專案。

<a id="agents"></a>

## 提供給 agent

**19 個 MCP 工具**讓 agent 分享報告、管理服務、取得 URL 與檢查設定。本機 MCP 用戶端可以這樣連線至已安裝的程式：

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

MCP 管理 TSLink；應用程式使用模型 HTTP API 進行推論。設定與自動化方法見 [MCP 用戶端](mcp-clients.md)、[遠端 MCP](remote-mcp.md)與 [agent 操作指南](../AGENTS.md)。

CLI 自動化支援 `--json`，其中 `schema_version` 為 `1`；可用 `tslink status --urls --json` 查看。本機 MCP 使用 stdio 上的 JSON-RPC。詳見 [JSON 自動化](json-automation.md)。

<a id="roadmap"></a>

## 即將推出

標為合併中、審查中或規劃中的項目尚未包含在上面的原始碼安裝中。

| 使用情境 | 狀&#8288;態 |
|---|---|
| <!-- roadmap:people --> 給親友 3 天的私有 HTTP/檔案應用存取權限，並將各應用邀請合成一則訊息；接收者仍需 Tailscale。 | 合&#8288;併&#8288;中 |
| <!-- roadmap:health --> 檢查應用健康狀態，並透過可選的命令或 webhook 接收離線或到期提醒。 | 合&#8288;併&#8288;中 |
| <!-- roadmap:recipes --> 探索支援的回環位址應用，並在分享前預覽自架應用配方。 | 合&#8288;併&#8288;中 |
| <!-- roadmap:limits --> 為每個 HTTP 應用設定上傳大小與請求逾時，以支援大檔案上傳與慢速用戶端。 | 合&#8288;併&#8288;中 |
| <!-- roadmap:windows --> 透過排程工作與內建監護程序，在使用者已登入 Windows 時重新啟動當機的守護程序。 | 合&#8288;併&#8288;中 |
| <!-- roadmap:access-log --> 透過本機存取紀錄查看誰開啟了哪個應用，並選擇 `prefix`、`full` 或 `off` 路徑紀錄模式。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:portal --> 用一個首頁列出訪客獲准存取的應用，並向擁有者提供節點授權交接資訊；訪客仍需 Tailscale。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:mcp-scopes --> 給 agent 指派角色與應用範圍，並記錄其修改操作的稽核憑據。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:guest-links --> 讓訪客無須安裝 Tailscale，透過受控的公開 Funnel，在瀏覽器中用限時連結與可選 PIN 開啟一個 HTTP 應用。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:durations --> 選擇預設或自訂有效期限，最短 1 小時，訪客預設最長 7 天且可設定。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:requests --> 協助手機使用者透過 QR code 加入，讓擁有者一步核准應用存取或延長時間請求。 | 審&#8288;查&#8288;中 |
| <!-- roadmap:multi-host --> 在一份清單中查看多台主機上的應用。 | 規&#8288;劃&#8288;中 |

<a id="documentation"></a>

## 文件與授權

[入門](getting-started.md) · [本機模型](local-ai.md) · [CLI 參考](cli-reference.md) · [平台](platforms.md) · [路線圖](roadmap.md)

貢獻方式見 [CONTRIBUTING.md](../CONTRIBUTING.md)；漏洞報告請使用 [SECURITY.md](../SECURITY.md) 中的管道。

TSLink 使用未經修改的 [Apache License 2.0](../LICENSE)，允許依該授權用於商業用途。散布時請保留適用的 [NOTICE](../NOTICE) 與[第三方聲明](../THIRD_PARTY_NOTICES.md)。[商業合作](../COMMERCIAL.md)完全自願，不增加授權條件。Tailscale 服務條款與方案另行適用。
