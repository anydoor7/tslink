<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink ロゴ">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>パソコン上のアプリを、選んだ相手に、選んだ期間だけ共有できます。</strong><br>
  各アプリに、Tailscale ネットワーク内の専用のプライベートアドレスが付きます。誰がアクセスできるか確認し、権限を取り消せます。
</p>

<p align="center">
  <a href="#quickstart">クイックスタート</a> · <a href="#agents">エージェント向け</a> · <a href="getting-started.md">ドキュメント</a> ·
  <strong>日本語</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">すべての言語</a>
</p>

## こんな場面で使えます

- **自分の成果物をスマートフォンで開く。** スクリプトが生成したレポート、開発サーバー、ノートブック、ローカルモデルの API に、許可された端末からプライベートな HTTPS アドレスでアクセスできます。
- **一人に一つのアプリを期間限定で渡す。** パートナーに写真ライブラリを一週間、同僚にプレビュー版を三日間公開できます。アクセス権は自動で期限切れになり、早めに取り消すこともできます。
- **共有をエージェントに任せる。** コーディングエージェントがダッシュボードを作ったら、自分とチームメイトに金曜日まで共有するよう頼めます。現在の共有状況を確認したり、共有を取り消したりすることもできます。

アプリは元の場所で動き続けます。TSLink は各アプリへのアクセスを管理し、何を、誰に、いつまで共有しているかを一つの一覧に記録します。

<a id="quickstart"></a>

## クイックスタート

**Go 1.26.6+**、Git、[MagicDNS と HTTPS を有効にした](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale アカウントが必要です。ビルド済みリリースはまだ公開されていないため、ソースからインストールします。

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

ページを共有します。

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

初回は、新しいサービスノードを登録するためのサインインリンクを TSLink が表示します。tailnet によっては管理者による端末承認も必要です。登録後、その tailnet にサインインした許可済み端末でサービス URL を開きます。API トークンは不要です。

共有状況を確認し、デモを削除します。

```bash
tslink status --urls
tslink remove demo
```

バックエンドが動作していれば、次のものも共有できます。

| 共有するもの | コマンド |
|---|---|
| ローカル Web アプリ | `tslink share 3000` |
| ファイルフォルダー | `tslink share ./public --name files` |
| Ollama などのローカルモデル API | `tslink add model --proxy localhost:11434` |
| プライベート TCP 接続のデータベース | `tslink add database --tcp localhost:5432` |
| 対応するセルフホストアプリ（Jellyfin、Immich、Home Assistant とほか 13 種） | `tslink apps detect`、続いて `tslink apps share jellyfin --yes` |

[導入、プラットフォーム、バックグラウンドサービス →](getting-started.md)

## アクセスできる相手を選ぶ

| 対象 | 相手に必要なもの | 確認する身元 | 終了時点 |
|---|---|---|---|
| **自分の端末** | 自分の tailnet へのサインイン | 検証済みの Tailscale ログイン | アプリを削除したとき |
| **指定した人**（プライベート HTTP/ファイル） | Tailscale ログイン。外部の人はアプリごとに招待を承諾 | 検証済みの Tailscale ログイン | 設定した期限（`--for 7d`）または `tslink people remove` の実行時 |
| **URL を持つ誰でも**（Funnel） | ブラウザー | 誰でも。アプリ自体のログイン要件は有効 | 既定で 24 時間後（`--funnel-ttl`） |
| **ブラウザー用ゲストリンク** *（近日対応）* | ブラウザーと任意の PIN | リンクを持つ人 | リンクの期限切れまたは取り消し時 |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

プライベート HTTP とファイル共有では、リクエストのたびに期限を確認します。権限の取り消しは新しいリクエストを止めますが、ダウンロード済みのデータを回収したり、受け入れ済みのストリームや WebSocket 接続を閉じたりはしません。[人への共有 →](people.md) · [共有の境界 →](sharing.md)

<a id="agents"></a>

## エージェント向け

TSLink は MCP サーバーを備えており、エージェントもユーザーと同様に共有、一覧表示、説明、削除を行えます。ローカル MCP クライアントに次を追加してください。

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **正確な結果。** CLI 自動化は `--json`、`schema_version: 1`、安定したエラーコードに対応し、`tslink mcp` は JSON-RPC を使います。`tslink manifest` はすべてのコマンドとフラグを説明します。エージェントは URL を組み立てず、`tslink url <name> --wait` で実際の URL を取得してください。
- **待機状態を正しく伝える。** 人によるサインインが必要な新しいノードは、準備済みと装わず `needs_login` を返します。
- **権限。** ローカル MCP は実行ユーザーの権限で動作します。リモート MCP は明示的に有効にする tailnet 専用エンドポイントで、指定したログインまたはタグだけを許可します。エージェント別のロール、アプリ範囲、操作記録は*近日対応*です。

TSLink の MCP は TSLink 自体を操作します。別の MCP サーバーを TSLink 経由で公開する場合、そのサーバーには独自のツール権限管理が引き続き必要です。
[エージェントガイド →](agents.md) · [MCP クライアント →](mcp-clients.md) · [リモート MCP →](remote-mcp.md) · [JSON 自動化 →](json-automation.md)

## 別のツールが向いている場合

| 目的 | 検討するツール |
|---|---|
| すでに動いている Tailscale クライアントで、自分の端末から一つのローカルサービスを使う | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| 複数ホストにまたがる、安定した名前を持つ管理者運用のサービス | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Tailscale アカウント不要の webhook や API デモ用公開 URL | [ngrok](https://ngrok.com/docs/start) または [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| セルフホストアプリの共有に加え、インストールと実行も行う | [Umbrel](https://umbrel.com) または [Coolify](https://coolify.io) |
| 組織全体で使う、身元に基づくアクセス管理基盤 | [Pangolin](https://github.com/fosrl/pangolin) または [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

一人が複数のアプリを運用し、アプリごと、相手ごとに期限付きアクセスを設定して、自分とエージェントが確認したい場合に TSLink が適しています。

## 仕組み

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App、Docs、Database、Model は一つの tailnet 内にある個別の名前付きノードで、公開元のパソコン上の一つの TSLink デーモンが実行します。" width="720">
</picture>

一つのバックグラウンドデーモンがアプリごとに組み込み Tailscale ノードを動かすため、各アプリに固有の名前とアドレスが付きます。プライベート HTTP とファイル共有では、`WhoIs` と人への許可、または `--allow` ルールでアクセスを制御し、各リクエストで許可期限を確認します。生の TCP は tailnet ポリシーとバックエンドの認証を使います。Tailscale が tailnet 通信、暗号化、証明書を提供し、TSLink は独立したプロジェクトです。全アプリが公開元のパソコンを共用するため、TSLink はアプリ同士を隔離しません。[アーキテクチャ →](architecture.md)

## 現在の状況

利用可能：アプリごとのプライベートアドレス、期限と招待のまとめを備えた人別共有、期限付き公開 Funnel、アプリのヘルスチェックと通知、セルフホストアプリのレシピ、アプリごとのリクエスト制限、Windows のクラッシュ後の再起動、CLI と MCP。 さらに、ブラウザー用ゲストリンク、柔軟な期間設定、アクセスログ、アプリのホームページ、範囲を限定したエージェントロール、QR による導入、アクセス申請も利用できます。

複数のパソコンを一つの一覧にまとめる機能は計画中です。[ロードマップ →](roadmap.md)

## ドキュメントとライセンス

[llms.txt](../llms.txt) · [エージェント向けクイックスタート](agent-quickstart.md) · [共有ツールの選び方](comparison.md)

[導入](getting-started.md) · [CLI リファレンス](cli-reference.md) · [プラットフォーム](platforms.md) · [ローカルモデル](local-ai.md) · [貢献](../CONTRIBUTING.md) · [セキュリティ](../SECURITY.md)

商用利用を含め Apache License 2.0 が適用されます。再配布時には [NOTICE](../NOTICE) と[第三者の通知](../THIRD_PARTY_NOTICES.md)を保持してください。Tailscale の規約とプランは別途適用されます。
