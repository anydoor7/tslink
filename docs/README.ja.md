<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>どこからでも、自分のアプリにアクセスして管理。<br>非公開のまま使う。必要な相手に、必要な条件で共有する。</strong></p>

自分のPCやクラウドサーバーのアプリを、暗号化されたプライベートネットワークから利用できます。必要ならブラウザー用ゲストリンクや一般公開も選べます。操作は自分でも、エージェント経由でも。

<p align="center"><a href="#quickstart">クイックスタート</a> · <a href="#agents">エージェント向け</a> · <a href="#documentation">ドキュメント</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <strong>日本語</strong> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 自分のアプリを、いつでも手元に

| やりたいこと | TSLink でできること |
|---|---|
| 別の端末から自分のアプリを使う | PCやサーバーの家庭用ダッシュボード、ローカル限定のWebページ、ファイル、モデル API、TCP サービスにプライベートアドレスを付けます。 |
| 指定した相手に共有する | HTTP/ファイルアプリを選び、期限設定・取り消し・Tailscale ログイン確認でアクセスを管理。相手にも Tailscale が必要です。[ユーザー共有](people.md) |
| ブラウザーだけで来てもらう | HTTP プロキシアプリに有効期限と任意の PIN 付きゲストリンク、または明示的な Funnel 公開 HTTPS。リンクは転送でき、本人確認にはなりません。[ゲストリンク](guest-links.md) |
| 複数のアプリを日常的に管理する | ホスト単位の一覧、プライベートポータル、ヘルスチェックと通知、アクセス履歴、CLI/MCP による権限管理。エージェントの役割・対象アプリ制限・監査記録にも対応。[ポータル](portal.md) · [MCP 権限](mcp-scopes.md) |

[アプリ設定レシピ](apps.md)、[アップロード制限](sharing.md)、[柔軟な有効期間](durations.md)、[QR コードでの案内とアクセス申請](requests.md)も、このソースツリーに含まれています。

<a id="installation"></a>
<a id="quickstart"></a>

## クイックスタート

**Git と Go 1.26.6+** でソースからインストールします。ビルド済みリリースと Homebrew は未公開です。以下は bash/zsh 用です。[macOS・Linux・Windows の設定](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

**Tailscale アカウント**と [MagicDNS と HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)が必要です。プライベート接続する端末には Tailscale とポリシー上の許可が必要です。ホスト側の Tailscale は TSLink に内蔵されています。

アプリがポート 3000 ですでに動いている場合：

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

未使用の名前を使ってください。`share` が別の名前を返したら、`url` にもその名前を使います。表示されたブラウザー登録と端末承認を済ませ、許可された端末で正確なアプリ URL を開きます。`share` は必要に応じてバックグラウンドサービスを起動します。この最初のプライベート利用に管理者 API トークンは不要です。既存ファイルは `tslink share ./report.html` で共有できます。アプリは事前に起動してください。[詳しい設定](getting-started.md)

使ってみて役に立ったら、[TSLink に Star](https://github.com/anydoor7/tslink) を付けて、ほかの人が見つけるきっかけにしていただけると嬉しいです。もちろん任意です。

<a id="architecture"></a>

## 仕組み

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="PC またはクラウドの1ホスト上で CLI/MCP が共通デーモンとアプリ別ノードを管理。プライベート接続は Tailscale で暗号化され、任意の公開 HTTPS/Funnel はゲスト認証または明示的な一般公開を経て HTTP アプリに接続する。" width="960">
</picture>

アプリへの暗号化されたプライベートな経路と考えてください。**ネットワーク転送と HTTPS は Tailscale、ホストごとのアプリアクセス管理は TSLink が担います。** 1つのデーモンがサービスごとに独立した内蔵ノードを動かします。ポータルには許可されたアプリを表示し、稼働状態とアクセス履歴で保守を支援します。

公開は明示的に選びます。ゲストにはリンクと任意の PIN が必要で、一般公開の Funnel は URL を持つ誰でも到達できます。どちらも公開 HTTPS であり、プライベートなユーザー識別とは異なります。生の TCP はプライベート限定で、tailnet ポリシーと接続先の認証に従います。TSLink はアプリのインストール、ホストプロセスの隔離、クラウド VPC の作成、複数ホストの集約は行いません。Tailscale と連携する独立プロジェクトです。[アーキテクチャと境界](architecture.md)

<a id="agents"></a>

## エージェント向け

CLI/MCP から一覧、稼働状態、URL、権限を管理します。[エージェントの入門](agent-quickstart.md)から始め、実際のツールスキーマを読み、アプリへのアクセスを確認してから成功を報告してください。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI 自動化は `--json`、MCP は stdio 上の JSON-RPC を使います。[クライアント設定](mcp-clients.md) · [リモート MCP](remote-mcp.md) · [役割とアプリ範囲](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## ドキュメントとライセンス

[全ガイド](INDEX.md) · [CLI リファレンス](cli-reference.md) · [ローカル AI](local-ai.md) · [稼働監視](health-and-alerts.md) · [アクセス履歴](access-log.md) · [ロードマップ](roadmap.md)

複数ホストのアプリ一覧は計画段階です。[貢献](../CONTRIBUTING.md)と[脆弱性報告](../SECURITY.md)を歓迎します。[Apache 2.0](../LICENSE) は商用利用を認めています。再配布時は [NOTICE](../NOTICE) と[第三者の告知](../THIRD_PARTY_NOTICES.md)を保持してください。[商業協力](../COMMERCIAL.md)は任意です。Tailscale の規約・プランは別途適用されます。
