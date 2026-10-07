<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>あなたのアプリに、Tailscale ネットワーク上のプライベートなアドレスを。</strong></p>
<p align="center">自分のデバイスから開けます。人やリンクに共有でき、期限の日付も自分で選べます。</p>
<p align="center"><a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>日本語</strong> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">他の言語</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## インストール

```sh
brew install --cask anydoor7/tap/tslink
```

Linux 向けの `.deb` と `.rpm` パッケージ、Windows 版は[最新リリース](https://github.com/anydoor7/tslink/releases/latest)にあります。初めてアプリを共有するとき、TSLink がそのアプリ用の Tailscale ログインリンクを表示します。[はじめに](getting-started.md)

<a id="use-cases"></a>

## 自分のアプリを、自分のデバイスで

- **アプリごとのアドレス。** Web アプリ、フォルダー、単一ファイル、TCP ポートがそれぞれ tailnet 内で名前を持つので、IP アドレスとポートではなく名前で開けます。
- **最初は非公開。** ゲストリンクを作るか Funnel で公開するまで、何も公開されません。
- **ホームページ**にアプリと稼働状態を一覧表示します。[ポータル](portal.md)
- **稼働監視と通知**はコマンドか webhook で。アクセス履歴には拒否されたリクエストも残ります。[稼働監視と通知](health-and-alerts.md) · [アクセス履歴](access-log.md)
- **15 種類のセルフホストアプリ用レシピ**。Home Assistant、Jellyfin、Immich、Ollama などがあります。`tslink apps detect` は既に動いているものを見つけます。[アプリ設定レシピ](apps.md)

## 共有したいときに

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

ゲストリンクと公開 URL には必ず期限があり、使えるのは Web アプリだけです。フォルダー、ファイル、TCP ポートは非公開のままです。[ユーザー共有](people.md) · [ゲストリンク](guest-links.md) · [一般公開](funnel.md)

<a id="agents"></a>

## AI エージェント向け

エージェントが `localhost` で起動した開発サーバーには、スマートフォンから届きません。TSLink を使えば、エージェントはあなたが選んだロールの範囲で、そのサーバーにプライベートなアドレスを付け、正確な URL を報告し、終わったら削除できます。

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI または MCP。** 管理コマンドは `--json` に対応し、バージョン付きの結果を返します。`tslink mcp` は同じ操作を MCP で提供します。
- **限定されたロール。** `viewer`、`app-operator`、`people-manager` から選び、対象を指定したアプリに絞れます。`tslink mcp-audit` でエージェントの変更を確認できます。ロールが制限するのは TSLink のツールで、エージェント自身のシェルには及びません。

[エージェントの入門](agent-quickstart.md) · [MCP 権限](mcp-scopes.md) · [リモート MCP](remote-mcp.md)

<a id="architecture"></a>

## 仕組み

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="PC またはクラウドの1ホスト上で CLI/MCP が共通デーモンとアプリ別ノードを管理。プライベート接続は Tailscale で暗号化され、任意の公開 HTTPS/Funnel はゲスト認証または明示的な一般公開を経て HTTP アプリに接続する。" width="960">
</picture>

1 つのバックグラウンドプロセスが、アプリごとに別々の Tailscale ノードを動かします。tailnet の通信と HTTPS 証明書は Tailscale が提供します。プライベートな Web とファイルへのアクセスは、`--allow` とユーザー共有で Tailscale の ID ごとに制限できます。生の TCP は tailnet のポリシーとアプリ自身のログインに頼ります。[アーキテクチャ](architecture.md)

<a id="requirements"></a>

## 必要なもの

| 誰 | 必要なもの |
|---|---|
| あなた | MagicDNS と HTTPS を有効にした Tailscale アカウント |
| アプリを動かすマシン | TSLink だけ（Tailscale を内蔵） |
| 自分のデバイスと共有相手 | Tailscale アプリ |
| ゲスト | ブラウザー |

アプリ名は公開の証明書ログに載るので、人に見られても構わない名前にしてください。

<a id="documentation"></a>

## その他

[全ドキュメント](INDEX.md) · [CLI リファレンス](cli-reference.md) · [Serve・ngrok・Cloudflare との比較](comparison.md) · [貢献](../CONTRIBUTING.md) · [セキュリティ](../SECURITY.md)

Apache 2.0。TSLink は独立したプロジェクトで、Tailscale が開発または承認したものではありません。
