<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>電腦或伺服器上的每個應用程式，都能在你的 Tailscale 網路裡擁有自己的私人位址，誰能存取，由你決定。</strong></p>

用自己的手機和筆電開啟 Web 應用程式、資料夾、模型 API 和資料庫，每個應用都有健康檢查和存取紀錄。你的 AI 代理也能在你指派的角色範圍內發布和檢查這些應用。需要讓別人存取時，可以授權指定的人使用到某個日期，或把一個 Web 應用限時開放到公開網際網路。

**需要 Tailscale。** 你需要一個 Tailscale 帳號（個人使用免費），每台要開啟私人應用的裝置都要安裝 Tailscale 用戶端；訪客和公開訪問者只需要瀏覽器。TSLink 是獨立專案，並非由 Tailscale 開發，也未獲 Tailscale 認可。[使用需求](#requirements)

<p align="center"><a href="#quickstart">快速開始</a> · <a href="#agents">給 AI 代理</a> · <a href="comparison.md">與 Serve、ngrok 和 Cloudflare 的比較</a> · <a href="#documentation">文件</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>繁體中文</strong> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 你可以做什麼

### 存取自己的應用程式

- **每個應用一個位址。** `tslink share 3000`、`tslink share ./photos` 或 `tslink add db --tcp localhost:5432` 會在你的 tailnet（你的 Tailscale 私人網路）裡，為 Web 應用、資料夾、檔案或 TCP 服務配發獨立的私人位址，例如 `https://photos.<tailnet>.ts.net`。每個應用在 Tailscale 裡都是一台獨立的裝置，所以你用名稱開啟應用，不必記 IP 位址和連接埠。
- **預設私人。** 應用只留在你的 tailnet 內，哪些裝置能連線由 tailnet 政策決定。只有在你建立訪客連結或公開發布應用之後，應用才會出現在公開網際網路上。
- **在同一處查看。** `tslink status --urls` 列出這台電腦上的所有應用；選用的私人入口頁會顯示每個應用的位址和健康狀態。[入口頁](portal.md)
- **出問題時馬上知道。** 應用停擺或恢復、Tailscale 登入即將到期，背景健康檢查都可以透過指令或 webhook 通知你。存取紀錄列出每次存取的人、應用和時間，包括遭到拒絕的請求。[健康檢查與警示](health-and-alerts.md) · [存取紀錄](access-log.md)
- **常用應用直接可用。** 設定範本涵蓋 15 個自架應用，包括 Home Assistant、Jellyfin、Immich 和 Ollama；`tslink apps detect` 會找出已在執行的應用。照片和影片應用會套用適合大型檔案的[上傳限制](sharing.md)。[應用設定範本](apps.md) · [本機 AI](local-ai.md)

### 讓 AI 代理來操作

代理啟動的開發伺服器、預覽頁面或本機模型 API 只在 `localhost` 上，你的手機和其他電腦都打不開。TSLink 讓代理以私人方式發布它、告訴你確切位址，用完再撤下，全程不超出你設定的限制。

- **分享、檢查、撤銷。** `share` 會回傳它註冊的名稱，以及確切的 URL 或一個讓你開啟的登入連結。`url --wait` 和 `status` 會回報應用何時可用，`remove`（在 MCP 中是 `unshare`）會把它撤下。[代理快速開始](agent-quickstart.md)
- **為自動化設計。** 指令支援 `--json`，回傳帶版本號的結果和穩定的錯誤碼。`tslink mcp` 把同樣的操作提供給本機的 MCP 用戶端，`tslink serve --mcp` 則透過 tailnet 提供給你其他裝置上的代理。[JSON 自動化](json-automation.md) · [遠端 MCP](remote-mcp.md)
- **權限有限。** 你自己執行的代理以擁有者身分操作。其他代理可以指派較低的角色（`viewer`、`app-operator` 或 `people-manager`），只能管理你指定的應用，它發出的授權也有最長期限。透過 MCP 做的變更都會記錄，可以用 `tslink mcp-audit` 查看。角色只限制 TSLink 的工具，管不到代理自己的 shell 和檔案。[MCP 權限](mcp-scopes.md)

### 分享給你選定的人

- **指定的人，到期為止。** `tslink people add alice@example.com --apps photos,notes --for 7d` 讓這個 Tailscale 登入帳號在期限前開啟這些 Web 和檔案應用。`people update`、`extend` 和 `people remove` 用來修改或結束授權；移除後，對方的下一個請求就會遭到拒絕，但已經下載的內容收不回來。[人員授權](people.md) · [期限](durations.md)
- **tailnet 以外的人。** 加上 `--invite --print-links`，就會產生一則可以直接傳出去的訊息，內含每個應用的裝置邀請（需要使用者名下的 API 權杖）。`--qr` 會印出 QR 碼，方便在手機上設定。
- **存取申請。** tailnet 裡的人可以在入口頁申請延長時間，或申請你標為可申請的應用。你用一道指令核准，同時指定期限。[存取申請](requests.md)

### 把 Web 應用限時開放到網際網路

- **訪客連結。** `tslink guest create photos --for 3d --public --print-link` 為單一 Web 應用建立瀏覽器連結，可以加 PIN，也可以單獨撤銷。訪客不需要 Tailscale 帳號。任何持有連結的人都能使用，所以它無法證明來訪者是誰。[訪客連結](guest-links.md)
- **開放的公開 URL。** `tslink add preview --proxy localhost:3000 --funnel --public` 把一個 Web 應用公開給任何知道 URL 的人。預設 24 小時後到期，可以用 `--funnel-ttl` 設定別的期限。[Funnel](funnel.md)
- 兩者都透過 Tailscale Funnel 運作，一定會到期（1 小時到 7 天，除非你調高上限），而且只適用於 Web 應用。資料夾、檔案和 TCP 服務一律保持私人。

以上功能都包含在 v0.1.0 中。

<a id="requirements"></a>

## 使用需求

TSLink 建立在 Tailscale 之上。它是獨立專案，並非由 Tailscale 開發，也未獲 Tailscale 認可；Tailscale 本身的條款和[方案](https://tailscale.com/pricing)同樣適用。

| 對象 | 需要什麼 |
|---|---|
| 你 | 一個 Tailscale 帳號，並開啟 [MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)。免費的 Personal 方案僅限非商業用途。 |
| 執行應用的電腦或伺服器 | 只需要 TSLink。它內建 Tailscale，不必另外安裝。每個新應用都要在瀏覽器登入一次；如果你的 tailnet 要求裝置核准，也要核准。 |
| 你的其他裝置 | Tailscale 用戶端，並登入你的 tailnet。 |
| 你選定的人 | Tailscale 用戶端和他們自己的登入帳號。他們可以加入你的 tailnet（會在你的方案裡多占一個使用者名額），也可以為每個應用接受一份裝置邀請。你的 tailnet 政策必須允許他們存取。 |
| 訪客和公開訪問者 | 一個瀏覽器。你的 tailnet 必須允許 Funnel，Tailscale 目前仍把它標為 beta。 |

開啟 HTTPS 後，你的 tailnet 名稱和裝置名稱（包括每個應用的名稱）會寫入公開的憑證紀錄，所以幫應用取名時，請選公開了也無妨的名稱。

<a id="installation"></a>
<a id="quickstart"></a>

## 快速開始

在 macOS 或 Linux 上使用 Homebrew 安裝。macOS 版執行檔以 Developer ID 憑證簽署，並經 Apple 公證。日後升級請執行 `brew upgrade --cask tslink`；若 TSLink 以背景服務執行，請再執行一次 `tslink install`。

```bash
brew install --cask anydoor7/tap/tslink
```

Windows 使用者請從[最新版本](https://github.com/anydoor7/tslink/releases/latest)下載 `tslink_<version>_windows_<arch>.zip`，以 `checksums.txt` 核對後執行 `tslink install`，讓 TSLink 在登入時啟動。此 zip 沒有 Authenticode 簽章，請透過已簽署的校驗和與建置證明[驗證版本](verify-release.md)。Linux 的 `.deb` 與 `.rpm` 套件也在同一個版本頁面。若要從原始碼建置，需要 **Git 和 Go 1.26.6+**。以下指令適用於 bash/zsh，請參閱 [macOS、Linux 和 Windows 設定](platforms.md)。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

你需要 **Tailscale 帳號**以及 [MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)。私人存取的接收裝置需要 Tailscale 與網路政策許可。TSLink 在應用主機上內嵌 Tailscale。

假設應用程式已在 3000 連接埠執行：

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

使用尚未占用的名稱；若 `share` 傳回不同名稱，請在 `url` 使用該名稱。先完成提示的瀏覽器註冊和裝置核准，再於獲准裝置開啟確切的應用 URL。`share` 會視需要啟動背景服務。首次私人存取不需要管理員 API 權杖。檔案可用 `tslink share ./report.html` 分享；應用須已執行，檔案須已存在。[完整設定](getting-started.md)

成功使用後，若覺得有幫助，歡迎[為 TSLink 加星](https://github.com/anydoor7/tslink)，讓更多人發現它。完全自願。

<a id="architecture"></a>

## 運作方式

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="一台電腦或雲端伺服器：CLI/MCP 管理共用背景程序和各應用節點。私人裝置透過 Tailscale 加密連線；選用的公開 HTTPS/Funnel 透過訪客驗證或明確的公開發布存取 HTTP 應用。" width="960">
</picture>

可以將它想成通往應用程式的私人加密通道。**Tailscale 提供網路傳輸和 HTTPS；TSLink 在每台主機管理應用存取。** 一個背景程序為每項服務執行獨立的內嵌節點。私人入口頁列出獲准應用，健康狀態和存取紀錄協助日常維護。

公開存取須主動啟用：訪客需要連結及選用的 PIN；開放的 Funnel 則讓任何持有 URL 的人存取。兩者均使用公開 HTTPS，而非私人使用者身分驗證。原始 TCP 保持私有，依賴 tailnet 政策及後端驗證。TSLink 不安裝應用、不隔離主機程序、不建立雲端 VPC，也不彙整多台主機。這是與 Tailscale 搭配使用的獨立專案。[架構與界線](architecture.md)

<a id="agents"></a>

## 給 AI 代理

透過 CLI 或 MCP 管理清單、健康狀態、URL 和權限。從[代理快速開始](agent-quickstart.md)入手；讀取實際工具 schema，確認應用確實可存取後才回報成功。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI 自動化使用 `--json`；MCP 透過 stdio 使用 JSON-RPC。[用戶端設定](mcp-clients.md) · [遠端 MCP](remote-mcp.md) · [角色與應用範圍](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## 文件與授權

[所有指南](INDEX.md) · [CLI 參考](cli-reference.md) · [本機 AI](local-ai.md) · [健康檢查](health-and-alerts.md) · [存取紀錄](access-log.md) · [路線圖](roadmap.md)

多主機應用清單仍在規劃中。歡迎[貢獻](../CONTRIBUTING.md)及[回報安全問題](../SECURITY.md)。[Apache 2.0](../LICENSE) 允許商業使用；散布時請保留 [NOTICE](../NOTICE) 和[第三方聲明](../THIRD_PARTY_NOTICES.md)。[商業合作](../COMMERCIAL.md)完全自願。Tailscale 條款與方案另行適用。
