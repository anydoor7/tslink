<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>你的應用程式，在你的 Tailscale 網路裡有私有位址。</strong></p>
<p align="center">用自己的裝置開啟。可以把應用程式分享給某個人，也可以透過連結分享，效期到你選的日期。</p>
<p align="center"><strong>繁體中文</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">更多語言</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## 安裝

```sh
brew install --cask anydoor7/tap/tslink
```

Linux 的 `.deb`、`.rpm` 套件和 Windows 版本在[最新版本](https://github.com/anydoor7/tslink/releases/latest)頁面。第一次分享應用程式時，TSLink 會給出這個應用程式的 Tailscale 登入連結。[入門指南](getting-started.md)

<a id="why"></a>

## 什麼時候需要 TSLink

只在自己的裝置上開啟一個應用程式，用 Serve 就夠了。TSLink 把應用程式網址、期限和存取變更放進同一套流程。

| 情境 | 只用 Tailscale | TSLink |
|---|---|---|
| 在手機上開啟一個 Web 應用程式 | `tailscale serve 3000` 就夠了 | `tslink share 3000` |
| 多個應用程式，各有自己的名稱 | 設定 Services，或分別執行節點 | 每個應用程式一次 `share`/`add`，每個節點分別登入 |
| 一個人，一個應用程式，七天 | 先寫政策規則，再用 JIT 臨時授權工具或手動移除 | `tslink people add alice@example.com --apps photos --for 7d`（HTTP/檔案） |
| 瀏覽器連結，三天 | 公開的 Funnel；自行加上存取關卡，並安排到期關閉 | `tslink guest create photos --for 3d --public --print-link`（僅限 HTTP） |

私人存取的對象需要安裝 Tailscale。訪客連結是公開且可轉傳的存取憑證。

[完整比較](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## 你的應用程式，在自己的裝置上開啟

- **每個應用程式一個位址。** Web 應用程式、資料夾、單一檔案和 TCP 連接埠在你的 tailnet 裡各有自己的名稱，用名稱就好，不必記 IP 位址。
- **預設私人。** 只有在你建立訪客連結或透過 Funnel 發布之後，才會有公開入口。
- **只開放這個應用程式。** 你發布的每個應用程式都有自己的節點，只轉送到這個應用程式。主機上沒有安裝 Tailscale App 時，TSLink 不會把主機的其他連接埠加進你的 tailnet。
- **一個入口頁**，列出你的應用程式和它們的健康狀態。[入口頁](portal.md)
- **健康檢查與警示**，透過指令或 webhook 通知；存取紀錄也包含遭拒的請求。[健康檢查與警示](health-and-alerts.md) · [存取紀錄](access-log.md)
- **15 個自架應用程式的設定範本**，包括 Home Assistant、Jellyfin、Immich 和 Ollama。`tslink apps detect` 會找出已在執行的那些。[應用設定範本](apps.md)

## 想分享時再分享

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

訪客連結和新建立的公開網址會過期，而且只適用於 Web 應用程式；資料夾、檔案和 TCP 連接埠一律保持私人。[人員授權](people.md) · [訪客連結](guest-links.md) · [公開存取](funnel.md)

<a id="agents"></a>

## 給 AI 代理

AI 代理在 `localhost` 上啟動的開發伺服器，你的手機連不到。有了 TSLink，代理可以給它一個私有位址，回報確切的 URL，用完再移除。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI 或 MCP。** 管理指令支援 `--json`，回傳帶版本號的結果；`tslink mcp` 透過 MCP 提供應用程式和存取管理操作。
- **只分享一個檔案。** 代理可以用 `tslink share ./report.html` 只分享它的 HTML 報告；同一個資料夾裡的其他檔案仍然無法存取。
- **有限的角色。** `viewer`、`app-operator` 或 `people-manager`，權限範圍限於你指定的應用程式。`tslink mcp-audit` 顯示代理改了什麼。角色只限制 TSLink 的工具，管不到代理自己的 shell。

[代理快速開始](agent-quickstart.md) · [MCP 權限](mcp-scopes.md) · [遠端 MCP](remote-mcp.md)

<a id="architecture"></a>

## 運作方式

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="一台電腦或雲端伺服器：CLI/MCP 管理共用背景程序和各應用節點。私人裝置透過 Tailscale 加密連線；選用的公開 HTTPS/Funnel 透過訪客驗證或明確的公開發布存取 HTTP 應用。" width="960">
</picture>

一個背景程序為每個應用程式執行獨立的 Tailscale 節點。Tailscale 提供 tailnet 傳輸和 HTTPS 憑證。私人的 Web 和檔案存取可以用 `--allow` 和人員授權依 Tailscale 身分限制；原始 TCP 仰賴你的 tailnet 政策和應用程式本身的登入。[架構](architecture.md)

<a id="requirements"></a>

## 使用需求

| 誰 | 需要什麼 |
|---|---|
| 你 | 已開啟 MagicDNS 和 HTTPS 的 Tailscale 帳號 |
| 執行應用程式的機器 | TSLink，它內建 Tailscale；在 Linux 上還需要 systemd 使用者工作階段 |
| 你的裝置，以及你分享的對象 | Tailscale App |
| 訪客 | 瀏覽器 |

HTTPS 應用程式的名稱會出現在公開的憑證紀錄裡，請選你不介意別人看到的名稱。

<a id="documentation"></a>

## 更多

[所有文件](INDEX.md) · [CLI 參考](cli-reference.md) · [與 Serve、ngrok 和 Cloudflare 的比較](comparison.md) · [貢獻](../CONTRIBUTING.md) · [安全性](../SECURITY.md)

Apache 2.0。TSLink 是獨立專案，非由 Tailscale 開發，也未經 Tailscale 認可。
