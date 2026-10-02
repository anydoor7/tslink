<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 標誌">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>把電腦上的應用程式分享給你選擇的人，分享多久由你決定。</strong><br>
  每個應用程式在你的 Tailscale 網路中都有獨立的私人位址。查看誰能存取，也能隨時收回權限。
</p>

<p align="center">
  <a href="#quickstart">快速開始</a> · <a href="#agents">給代理</a> · <a href="getting-started.md">文件</a> ·
  <strong>繁體中文</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">所有語言</a>
</p>

## 大家用它做什麼

- **在手機上開啟自己的工作成果。** 腳本產生的報告、開發伺服器、筆記本或本機模型 API，都能透過私人 HTTPS 位址，在獲准的裝置上存取。
- **把一個應用程式暫時分享給一個人。** 讓伴侶使用相片庫一週，或讓同事試用預覽版三天。存取權限會自動到期，你也能提前結束。
- **讓代理幫你分享。** 程式開發代理剛做出一個儀表板，你可以請它分享給你和隊友，開放到週五。它也能告訴你目前分享了什麼，並收回分享。

應用程式繼續在原本的地方執行。TSLink 管理各個應用程式的存取權限，並用一份清單記錄分享了什麼、分享給誰、到什麼時候。

<a id="quickstart"></a>

## 快速開始

你需要 **Go 1.26.6+**、Git，以及一個[已啟用 MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) 的 Tailscale 帳戶。目前尚未發布預先編譯的版本，請從原始碼安裝：

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

分享一個頁面：

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

首次使用時，TSLink 會輸出登入連結，用來註冊新的服務節點；你的 tailnet 也可能要求管理員核准裝置。註冊完成後，在已登入你的 tailnet 且獲准存取的裝置上開啟服務 URL。不需要 API 權杖。

查看目前的分享，再移除示範服務：

```bash
tslink status --urls
tslink remove demo
```

後端啟動後，你還能分享這些內容：

| 內容 | 指令 |
|---|---|
| 本機 Web 應用程式 | `tslink share 3000` |
| 檔案資料夾 | `tslink share ./public --name files` |
| 本機模型 API，例如 Ollama | `tslink add model --proxy localhost:11434` |
| 透過私人 TCP 存取的資料庫 | `tslink add database --tcp localhost:5432` |
| 已支援的自架應用程式（Jellyfin、Immich、Home Assistant 等，共 16 種） | `tslink apps detect`，接著執行 `tslink apps share jellyfin --yes` |

[入門、平台與背景服務 →](getting-started.md)

## 選擇誰能開啟

| 存取對象 | 接收者需要什麼 | 身分依據 | 何時結束 |
|---|---|---|---|
| **你自己的裝置** | 已登入你的 tailnet | 經驗證的 Tailscale 登入身分 | 移除應用程式時 |
| **指定的人**（私人 HTTP/檔案） | Tailscale 登入帳戶；網路外的人須逐一接受應用程式邀請 | 經驗證的 Tailscale 登入身分 | 你設定的期限到達時（`--for 7d`），或執行 `tslink people remove` |
| **任何持有 URL 的人**（Funnel） | 瀏覽器 | 任何人；仍需遵守應用程式本身的登入要求 | 預設 24 小時後（`--funnel-ttl`） |
| **瀏覽器訪客連結** *（即將推出）* | 瀏覽器，以及選用的 PIN | 連結持有者 | 連結到期或被撤銷時 |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

私人 HTTP 和檔案分享會在每次請求時檢查期限。撤銷權限會阻止新請求；它無法收回已下載的資料，也不會關閉已接受的資料串流和 WebSocket 連線。[依對象分享 →](people.md) · [分享邊界 →](sharing.md)

<a id="agents"></a>

## 給代理

TSLink 內建 MCP 伺服器，代理能像你一樣分享、列出、解釋和移除分享。在本機 MCP 用戶端中加入：

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **準確的結果。** CLI 自動化支援 `--json`，使用 `schema_version: 1` 和穩定的錯誤碼；`tslink mcp` 則使用 JSON-RPC。`tslink manifest` 描述每個指令和參數。代理應透過 `tslink url <name> --wait` 取得真正的 URL，而不要自行組合。
- **如實回報等待狀態。** 新節點若還需要人完成登入，就會回報 `needs_login`，不會假裝已經就緒。
- **操作權限。** 本機 MCP 以你的使用者權限執行。遠端 MCP 須主動啟用，只能在 tailnet 內存取，且僅允許你列出的登入身分或標籤。個別代理的角色、應用程式範圍和操作收據*即將推出*。

TSLink 的 MCP 用來操作 TSLink 本身。如果你透過 TSLink 發布其他 MCP 伺服器，該伺服器仍需要自己的工具權限控制。
[代理指南 →](agents.md) · [MCP 用戶端 →](mcp-clients.md) · [遠端 MCP →](remote-mcp.md) · [JSON 自動化 →](json-automation.md)

## 何時選擇其他工具

| 你的需求 | 可以考慮 |
|---|---|
| 使用現有的 Tailscale 用戶端，在自己的裝置上存取一個本機服務 | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| 由管理員管理、跨多台主機使用固定名稱的服務 | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| 不需要 Tailscale 帳戶的 webhook 或 API 示範公開 URL | [ngrok](https://ngrok.com/docs/start) 或 [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| 安裝並執行自架應用程式，而不只是分享 | [Umbrel](https://umbrel.com) 或 [Coolify](https://coolify.io) |
| 涵蓋整個組織、依身分控制存取的平台 | [Pangolin](https://github.com/fosrl/pangolin) 或 [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

如果一個人執行著多個應用程式，想依應用程式、依對象設定有期限的存取權限，並讓自己和代理都能查看，TSLink 就適合這種情境。

## 運作原理

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App、Docs、Database 和 Model 是同一 tailnet 中獨立命名的節點，由發布端電腦上的一個 TSLink 背景程序執行。" width="720">
</picture>

一個背景程序為每個應用程式執行一個嵌入式 Tailscale 節點，讓每個應用程式都有自己的名稱和位址。私人 HTTP 和檔案分享透過 `WhoIs`、依對象授權或 `--allow` 規則控制存取，每次請求都會檢查依對象授權的期限。原始 TCP 使用 tailnet 政策和後端本身的驗證。Tailscale 提供 tailnet 傳輸、加密和憑證；TSLink 是獨立專案。所有應用程式共用發布端電腦，TSLink 不會將它們彼此隔離。[架構 →](architecture.md)

## 目前狀態

現已可用：各應用程式的私人位址、附期限和邀請組合的依對象分享、會到期的公開 Funnel、應用程式健康檢查與警示、自架應用程式配方、各應用程式的請求限制、Windows 當機重啟、CLI 和 MCP。

即將推出：瀏覽器訪客連結、彈性的時長、存取紀錄、應用程式首頁、限定範圍的代理角色、QR 掃碼引導和存取申請。將多台電腦的應用程式彙整到同一份清單仍在規劃中。[路線圖 →](roadmap.md)

## 文件與授權

[入門](getting-started.md) · [CLI 參考](cli-reference.md) · [平台](platforms.md) · [本機模型](local-ai.md) · [貢獻](../CONTRIBUTING.md) · [安全](../SECURITY.md)

採用 Apache License 2.0，允許商業使用。重新散布時請保留 [NOTICE](../NOTICE) 和[第三方聲明](../THIRD_PARTY_NOTICES.md)。Tailscale 的服務條款與方案另行適用。
