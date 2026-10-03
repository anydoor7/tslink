<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>隨時隨地存取和管理你的應用程式。<br>保持私有，或依你的意願分享。</strong></p>

無論應用程式在自己的電腦或雲端伺服器上，都能透過加密的私人網路存取；也可以主動選擇瀏覽器訪客連結或公開存取。自己操作，或交給 AI 代理管理。

<p align="center"><a href="#quickstart">快速開始</a> · <a href="#agents">給 AI 代理</a> · <a href="#documentation">文件</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>繁體中文</strong> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 讓應用程式隨手可用

| 你的需求 | TSLink 提供的功能 |
|---|---|
| 跨裝置使用自己的應用程式 | 為電腦或伺服器上的家庭儀表板、僅限本機的網頁、檔案、模型 API 和 TCP 服務提供私人位址。 |
| 分享給指定的人 | 為指定 HTTP/檔案應用設定期限並撤銷存取，以實際 Tailscale 登入身分驗證；接收者需要 Tailscale。[人員授權](people.md) |
| 讓訪客用瀏覽器開啟 | 為 HTTP 代理應用建立有期限、可選 PIN 的訪客連結，或明確啟用 Funnel 公開 HTTPS。訪客連結可被轉傳，無法證明訪客身分。[訪客連結](guest-links.md) |
| 持續管理一組應用程式 | 每台主機的應用清單、私人入口頁、健康檢查與警示、存取紀錄，以及支援代理角色、應用範圍和稽核回執的 CLI/MCP 存取管理。[入口頁](portal.md) · [MCP 權限](mcp-scopes.md) |

[應用設定範本](apps.md)、[上傳限制](sharing.md)、[彈性期限](durations.md)及 [QR 碼引導與存取申請](requests.md)讓日常維護更方便。這些功能已包含在本原始碼中。

<a id="installation"></a>
<a id="quickstart"></a>

## 快速開始

使用 **Git 和 Go 1.26.6+** 從原始碼安裝；預編譯版本和 Homebrew 尚未發布。以下指令適用於 bash/zsh。請參閱 [macOS、Linux 和 Windows 設定](platforms.md)。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

你需要儲存庫存取權、**Tailscale 帳號**以及 [MagicDNS 和 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)。私人存取的接收裝置需要 Tailscale 與網路政策許可。TSLink 在應用主機上內嵌 Tailscale。

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
