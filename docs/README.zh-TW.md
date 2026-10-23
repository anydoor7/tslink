<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink 是開源的命令列工具和 MCP 伺服器，替你電腦上的每個應用程式提供一個形如 <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> 的私有位址，讓你或你的 coding agent 能在手機上開啟正在執行的 dev server。TSLink 是獨立專案，並非 Tailscale 出品。</p>
<p align="center">手機需要安裝 Tailscale App，並登入同一個 tailnet。</p>
<p align="center"><strong>繁體中文</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">更多語言</a></p>

```text
# Claude Code，先裝好命令列工具（見下方的安裝）
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# 使用 mcpServers JSON 的本機 MCP 用戶端（其他用戶端請看它自己的 MCP 設定說明）
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

對代理說：把 dev server 分享出來，我要在手機上開啟。
```

手動做同樣的事，前提是 dev server 已在 5173 連接埠監聽。加上 `--json` 後，`share` 會在 `data.name` 裡回報註冊的名稱。後面的指令要用這個名稱；如果 `--name` 的名稱已被占用，兩者就會不同，例如 `web-2`。

```sh
tslink share 5173 --name web --json   # data.name 是名稱；data.url 是確切的 URL，status 為 needs_login 時 data.auth_url 是 Tailscale 登入連結
tslink url <name> --wait              # 完成登入後，取得確切的 URL
tslink remove <name>                  # 用完移除；share 失敗就不要執行。dev server 會繼續執行
```

<a id="installation"></a>
<a id="quickstart"></a>

## 安裝

macOS 主機需要 macOS 13 Ventura 或更新版本（[平台支援](platforms.md)）。

```sh
brew install --cask anydoor7/tap/tslink
```

在 Windows 上，使用 Scoop 安裝：

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

升級時執行 `brew upgrade --cask tslink` 或 `scoop update; scoop update tslink`。若 TSLink 作為背景服務執行，升級後請再次執行 `tslink install`。

Linux 的 `.deb`、`.rpm` 套件和 Windows 版本在[最新版本](https://github.com/anydoor7/tslink/releases/latest)頁面。第一次分享應用程式時，TSLink 會給出這個應用程式的 Tailscale 登入連結。[入門指南](getting-started.md)

<a id="agents"></a>

## 給 AI 代理

AI 代理在 `localhost` 上啟動的 dev server，你的手機連不到。有了 TSLink，代理可以給它一個私有位址，把確切的 URL 交給你，用完再移除這次分享。手機需要安裝 Tailscale App，並登入同一個 tailnet。

實測於 2026-10-22：Vite 8.3.5，iPhone 15 Pro。代理透過 MCP 分享了 dev server，手機開啟了這個私有 URL，儲存程式碼後頁面自動更新，不需要手動重新整理。

使用 Claude Code 的話，請先安裝命令列工具，再執行：

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

外掛包含一個[預覽 skill](../skills/tslink-preview/SKILL.md)和下方的 MCP 連線。外掛市集仍在這個儲存庫，外掛本身取自 [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin)。標準版 skill 留在這個儲存庫，外掛內附一份經 CI 檢查的副本。其他代理可以使用同一個 skill，也可以直接連接 MCP。這個整合需要本機命令列工具，無法在 claude.ai 聊天中執行。[設定與登入](agent-integrations.md)

使用 `mcpServers` JSON 的本機 MCP 用戶端（其他用戶端請看各自的 MCP 設定說明和 [MCP 用戶端指南](mcp-clients.md)）：

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **每個 dev server 一個名稱。** 每次分享都有自己的私有位址 `https://<name>.<tailnet>.ts.net`，位於根路徑，連接埠 443。名稱被占用時會加上數字後綴，所以請以代理取回的 URL 為準。
- **每個新應用程式都需要人來操作。** 預設設定下，每個新的應用程式節點都需要人完成一次 Tailscale 登入。如果結果是 `needs_login`，先完成登入，再執行 `tslink url <name> --wait` 取得確切的 URL。另外，新名稱要過一會兒才連得上：我們兩次測試花了 36 到 38 秒。
- **誰能開啟。** 預設是你的政策允許的 tailnet 裝置。MCP `share` 接受 `allow`，可限制為只有你的登入身分；在命令列請用 `tslink add web --proxy localhost:5173 --allow you@example.com`。名稱會出現在公開的憑證記錄中，所以並不保密。
- **改程式碼，儲存，就看得到。** 儲存程式碼後，手機上的頁面會更新。應用程式自己的 Host 和 Origin 規則仍可能擋下請求：我們的測試中 Vite 不需要修改 `allowedHosts`，Next.js 未測試，仍需要 `allowedDevOrigins`。
- **清理。** 代理呼叫 `unshare`，或由你執行 `tslink remove <name>`，一律使用 share 結果回傳的名稱。移除會取消註冊這次分享，並在 TSLink 能證明節點屬於自己時，嘗試刪除它在 tailnet 裡的節點，所以請查看結果中的清理或保留狀態欄位。它不會停止 dev server。
- **CLI 或 MCP。** 管理指令支援 `--json`，回傳帶版本號的結果；`tslink mcp` 透過 MCP 提供應用程式和存取管理操作。
- **只分享一個檔案。** 代理可以用 `tslink share ./report.html` 只分享自成一體的 HTML 報告；同一個資料夾裡的其他檔案仍然無法存取。
- **有限的角色。** `viewer`、`app-operator` 或 `people-manager`，權限範圍限於你指定的應用程式；分享新的應用程式需要 `owner` 角色。`tslink mcp-audit` 會顯示代理改了什麼。角色只限制 TSLink 的工具，管不到代理自己的 shell。

[代理快速開始](agent-quickstart.md) · [MCP 權限](mcp-scopes.md) · [遠端 MCP](remote-mcp.md)

<a id="why"></a>

## 什麼時候需要 TSLink

Serve 足以應付你自己啟動、在自己手機上開啟的一個應用程式。TSLink 針對的是代理啟動的 dev server（往往不只一個）：每個都該有自己的私有名稱，分享、取得 URL 和清理這幾步由命令列、MCP 和 Claude Code skill 現成提供。

| 情境 | 只用 Tailscale | TSLink |
|---|---|---|
| 代理啟動的多個 dev server，各用一個名稱 | Serve 在一台機器的一個名稱下設定裝置層級的路由；Services 為每個名稱要求服務定義、核准和有標籤的主機 | 每個應用程式一個節點，分享、取得 URL 和清理都包在命令列（`share`、`url`、`remove`）、MCP（`share`、`url`、`unshare`）和 skill 裡 |
| 你自己啟動的一個應用程式，在手機上開啟 | `tailscale serve 3000` 就夠了 | `tslink share 3000` |
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
