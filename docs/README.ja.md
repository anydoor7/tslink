<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink ロゴ">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>ローカルのアプリ、モデル、ファイルに、それぞれ専用のプライベートアドレスを。</strong><br>
  Tailscale ネットワーク内の、アクセスを許可された別の端末から利用できます。
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="ライセンス：Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 以降"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale：組み込み tsnet ノード"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP：19 のツール"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <strong>日本語</strong><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## インストール

**Go 1.26.6 以降** と Gitが必要です。ビルド済みリリースや Homebrew cask はまだ公開されていないため、ソースからインストールします。例は **bash または zsh** 用です。Windows とバックグラウンドサービスの要件は[プラットフォーム対応](platforms.md)を参照してください。詳細ガイドは英語です。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

[MagicDNS と HTTPS を有効にした](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale アカウントを使います。アクセスする端末は、自分の Tailscale ネットワーク（**tailnet**）にログインし、ポリシーでサービスへの接続を許可されている必要があります。配信側では TSLink が Tailscale を組み込んでいます。

### 最初のページを共有する

ページを作ると、TSLink が直接配信し、必要に応じてバックグラウンドサービスを起動します。

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

ノード認証の URL が表示されたら、開いて認証します。tailnet によっては管理者のデバイス承認も必要です。その後、正確なアドレスを取得します。

```bash
tslink url demo --wait
```

許可された端末で、返された URL を開いてください。最初の共有に API トークンは不要です。[詳しい設定とライフサイクル →](getting-started.md)

<a id="use-cases"></a>

## 何を共有しますか？

ファイルは事前に用意してください。アプリ、データベース、モデルのバックエンドは、指定ポートで動作している必要があります。

| 利用例 | コマンド |
|---|---|
| 別の端末でローカルアプリを開く | `tslink share 3000` |
| ディレクトリ内のファイルを閲覧する | `tslink share ./public --name files` |
| 生成した HTML レポートをスマートフォンで読む | `tslink share ./report.html --name report` |
| TCP でローカルのデータベースに接続する | `tslink add database --tcp localhost:5432` |
| Ollama などのローカルモデル HTTP API を使う | `tslink add model --proxy localhost:11434` |

Ollama では `tslink url model --wait` で正確な URL を取得します。OpenAI API 互換クライアントの `baseURL` は、その URL に `/v1` を付けたものです。[ローカルモデルと非公開データの処理 →](local-ai.md)

1 台のホストで複数のアプリを管理するために、TSLink は名前付きサービスノード、HTTP の ID 許可リスト、Funnel の有効期限、MCP 管理をまとめて提供します。自分のデバイス間で 1 つのアプリを使うだけなら、[Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) で足りる場合があります。

<a id="architecture"></a>

## アーキテクチャ

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="サービス構成の例：App、Docs、Database、Model は、同じ tailnet 内の別々の名前付きノードです。アプリ、ファイル、モデル API は HTTPS、データベースはプライベート TCP を使います。" width="960">
</picture>

**ひとつの tailnet に、用途ごとのサービスノード。** 共通のデーモンがサービスごとに組み込み tsnet ノードを動かし、HTTP の転送、ファイル配信、TCP の中継を行います。レジストリの変更は稼働中に反映されます。ノードごとに独立したネットワーク識別情報を持ちますが、サービスが動くホストは共通です。[アーキテクチャの詳細 →](architecture.md)

| 技術 | 役割 |
|---|---|
| [Go](../go.mod) | ネイティブのコマンドラインプログラム |
| [Tailscale tsnet](architecture.md) | サービスノードと tailnet 通信 |
| [Cobra](https://github.com/spf13/cobra) | コマンドとヘルプ |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | エージェント用トランスポート |
| OS キーチェーンとユーザーサービス管理 | 任意の認証情報保存とバックグラウンド動作 |

[公開 Funnel](getting-started.md#more-examples) を明示的に有効にしない限り、サービスは tailnet 内で提供します。HTTP・ファイルサービスでは識別情報に応じた許可リストを使えます（`WhoIs`、`--allow`）。TCP は tailnet ポリシーとバックエンド自身の認証に依存します。[共有の範囲](sharing.md)を参照してください。

TSLink はアプリのインストール、モデルの実行、ホストプロセスの隔離、複数ホストの集約を行いません。ネットワーク、暗号化、HTTPS は Tailscale が提供します。TSLink は独立したプロジェクトです。

<a id="agents"></a>

## エージェントから使う

**19 の MCP ツール**で、エージェントがレポートの共有、サービス管理、URL 取得、設定確認を行えます。ローカルの MCP クライアントを、インストール済みのプログラムに接続します。

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

MCP は TSLink を管理し、アプリの推論にはモデル HTTP API を使います。設定と自動化は [MCP クライアント](mcp-clients.md)、[リモート MCP](remote-mcp.md)、[エージェント運用ガイド](../AGENTS.md)を参照してください。

CLI の自動化では `--json` を使えます。`schema_version` は `1` です。`tslink status --urls --json` で確認できます。ローカル MCP は stdio 上の JSON-RPC を使います。[JSON 自動化](json-automation.md)を参照してください。

<a id="roadmap"></a>

## 今後の予定

マージ中、レビュー中、計画中の項目は、上記のソースインストールには含まれません。

| 用途 | 状&#8288;態 |
|---|---|
| <!-- roadmap:people --> 親族にプライベート HTTP/ファイルアプリへの 3 日間のアクセスを与え、各アプリの招待を 1 通にまとめます。受信者には Tailscale が必要です。 | マ&#8288;ー&#8288;ジ&#8288;中 |
| <!-- roadmap:health --> アプリの正常性を確認し、任意のコマンドまたは webhook で停止や期限の通知を受け取ります。 | マ&#8288;ー&#8288;ジ&#8288;中 |
| <!-- roadmap:recipes --> 対応するループバックアプリを検出し、共有前にセルフホストアプリのレシピをプレビューします。 | マ&#8288;ー&#8288;ジ&#8288;中 |
| <!-- roadmap:limits --> 大容量アップロードや低速クライアントに合わせて、HTTP アプリごとにアップロードサイズとリクエストのタイムアウトを設定します。 | マ&#8288;ー&#8288;ジ&#8288;中 |
| <!-- roadmap:windows --> Windows にユーザーがサインインしている間、スケジュールされたタスクと内蔵スーパーバイザーで停止したデーモンを再起動します。 | マ&#8288;ー&#8288;ジ&#8288;中 |
| <!-- roadmap:access-log --> ローカルアクセスログで誰がどのアプリを開いたか確認し、パス記録を `prefix`、`full`、`off` から選びます。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:portal --> 許可されたアプリを 1 つのホームページに表示し、所有者にノード登録の引き継ぎ情報を示します。訪問者には Tailscale が必要です。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:mcp-scopes --> エージェントにロールとアプリの範囲を与え、変更操作の監査記録を残します。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:guest-links --> ゲート付き公開 Funnel を通じて、有効期限付きリンクと任意の PIN でゲストがTailscale をインストールせずブラウザーから 1 つの HTTP アプリを開けるようにします。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:durations --> プリセットまたは任意の期間を選びます。最短は 1 時間、ゲストの最長は既定で 7 日間で変更可能です。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:requests --> スマートフォンのユーザーが QR コードで参加できるようにし、所有者がアプリへのアクセスや期間延長のリクエストを 1 回の操作で承認できるようにします。 | レ&#8288;ビ&#8288;ュ&#8288;ー&#8288;中 |
| <!-- roadmap:multi-host --> 複数ホストのアプリを 1 つの一覧で確認します。 | 計&#8288;画&#8288;中 |

<a id="documentation"></a>

## ドキュメントとライセンス

[はじめに](getting-started.md) · [ローカルモデル](local-ai.md) · [CLI リファレンス](cli-reference.md) · [プラットフォーム](platforms.md) · [ロードマップ](roadmap.md)

貢献方法は [CONTRIBUTING.md](../CONTRIBUTING.md)を参照してください。脆弱性の報告には [SECURITY.md](../SECURITY.md)の窓口を使ってください。

TSLink は変更を加えていない [Apache License 2.0](../LICENSE)を採用し、同ライセンスに従って商用利用できます。再配布時には、適用される [NOTICE](../NOTICE)と[第三者の通知](../THIRD_PARTY_NOTICES.md)を保持してください。[商業協力](../COMMERCIAL.md)は任意であり、追加のライセンス条件はありません。Tailscale のサービス条件とプランは別途適用されます。
