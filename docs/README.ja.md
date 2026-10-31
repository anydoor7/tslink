<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink は、パソコン上のアプリごとに <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> 形式のプライベートなアドレスを割り当てる、オープンソースの CLI と MCP サーバーです。自分でも coding agent でも、実行中の開発サーバーをスマートフォンで開けます。独立したプロジェクトで、Tailscale 製ではありません。</p>
<p align="center">スマートフォンには Tailscale アプリが必要で、同じ tailnet にサインインしておきます。</p>
<p align="center"><a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <strong>日本語</strong> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">他の言語</a></p>

```text
# Claude Code。先に CLI をインストールしてください（下の「インストール」を参照）
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# mcpServers 形式の JSON を使うローカル MCP クライアント（ほかのクライアントは、そのクライアントの MCP 設定を参照）
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

エージェントに頼む：「開発サーバーを共有して、スマートフォンで開けるようにして」
```

同じことを手作業で行う手順です。開発サーバーはすでにポート 5173 で待ち受けているものとします。`--json` を付けると、`share` は登録された名前を `data.name` で返します。以降のコマンドにはこの名前を使ってください。`--name` の名前がすでに使われていると、`web-2` のように別の名前になります。

```sh
tslink share 5173 --name web --json   # data.name が名前。data.url が正確な URL。status が needs_login のときは data.auth_url が Tailscale のサインインリンク
tslink url <name> --wait              # サインインを終えたあとに、正確な URL を取得
tslink remove <name>                  # 使い終わったら削除。share が失敗したなら実行しない。開発サーバーは動いたまま
```

<a id="installation"></a>
<a id="quickstart"></a>

## インストール

macOS ホストには macOS 13 Ventura 以降が必要です（[プラットフォーム対応](platforms.md)）。

```sh
brew install --cask anydoor7/tap/tslink
```

Windows では Scoop でインストールします：

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

更新には `brew upgrade --cask tslink` または `scoop update; scoop update tslink` を実行します。TSLink をバックグラウンドサービスとして使用している場合は、更新後に `tslink install` を再実行してください。

Linux 向けの `.deb` と `.rpm` パッケージ、Windows 版は[最新リリース](https://github.com/anydoor7/tslink/releases/latest)にあります。初めてアプリを共有するとき、TSLink がそのアプリ用の Tailscale ログインリンクを表示します。[はじめに](getting-started.md)

<a id="agents"></a>

## AI エージェント向け

エージェントが `localhost` で起動した開発サーバーには、スマートフォンから届きません。TSLink を使えば、エージェントはそのサーバーにプライベートなアドレスを付け、正確な URL を渡し、終わったら共有を削除できます。スマートフォンには Tailscale アプリが必要で、同じ tailnet にサインインしておきます。

2026-10-22 に Vite 8.3.5 と iPhone 15 Pro で確認しました。エージェントが MCP で開発サーバーを共有し、スマートフォンでプライベート URL を開くと、保存するだけで手動の再読み込みなしにページが更新されました。

Claude Code では、先に CLI をインストールしてから次を実行します。

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

プラグインには[プレビュー用スキル](../skills/tslink-preview/SKILL.md)と、下の MCP 接続が含まれます。マーケットプレイスはこのリポジトリにあり、プラグイン本体は [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin) から取得します。正本のスキルはここにあり、プラグインには CI で照合されるコピーが入っています。ほかのエージェントも同じスキルを使うか、MCP に直接接続できます。この連携にはローカルの CLI が必要で、claude.ai のチャットでは動きません。[セットアップとサインイン](agent-integrations.md)

`mcpServers` 形式の JSON を使うローカル MCP クライアントの場合（ほかのクライアントは、そのクライアントの MCP 設定手順と [MCP クライアントガイド](mcp-clients.md)を参照）：

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **新規インストールはゲートウェイが既定。** 非公開 HTTP/ファイルアプリには `https://home.<tailnet>.ts.net:8443/` のような固定ポートを割り当てます。既存環境のモードと各アプリのノード/URLは維持され、`--portal-only` は明示的な選択です。
- **ゲートウェイを登録。** 資格情報なしの登録では、タグのないゲートウェイ自身の検証済みアカウントを owner にできます。tagged/OAuth の場合はローカルで `tslink portal enable --owner <login>` を実行します。アプリノードの登録待ちは準備済みのゲートウェイルートを妨げません。
- **人ごとに許可。** 新しいゲートウェイアプリは owner/admin と明示的な許可の対象者だけが利用できます。`people add`、`add --allow`、MCP `add/share.allow` は所属を確認し必要な招待を準備します。メンバーへのデバイス招待は不要で、外部の人はゲートウェイ招待を一度受諾します。ディレクトリ/API の障害は未完了として返ります。
- **編集して保存すれば、画面に反映。** コードを保存すると、スマートフォンのページが更新されます。アプリ自身の Host と Origin のルールでリクエストが拒否されることはあります。今回のテストでは Vite は `allowedHosts` の変更が不要でした。Next.js は未検証で、`allowedDevOrigins` が必要です。
- **後片付け。** エージェントが `unshare` を呼ぶか、自分で `tslink remove <name>` を実行します。名前は必ず share の結果で返されたものを使ってください。削除すると共有の登録が解除され、TSLink が自分のものだと確認できる場合は tailnet 上のノードも削除を試みます。結果のクリーンアップや残存状態のフィールドを確認してください。開発サーバーは止まりません。
- **CLI または MCP。** 管理コマンドは `--json` に対応し、バージョン付きの結果を返します。`tslink mcp` はアプリとアクセスの操作を MCP で提供します。
- **フォルダーではなく、ファイル 1 つ。** エージェントは `tslink share ./report.html` で、単体で完結した HTML レポートだけを共有できます。同じフォルダーのほかのファイルには届きません。
- **限定されたロール。** `viewer`、`app-operator`、`people-manager` から選び、対象を指定したアプリに絞れます。新しいアプリの共有には `owner` ロールが必要です。`tslink mcp-audit` でエージェントの変更を確認できます。ロールが制限するのは TSLink のツールで、エージェント自身のシェルには及びません。

[エージェントの入門](agent-quickstart.md) · [MCP 権限](mcp-scopes.md) · [リモート MCP](remote-mcp.md)

<a id="why"></a>

## TSLink が必要になるとき

自分で起動した 1 つのアプリを自分のスマートフォンで開くだけなら Serve で十分です。TSLink が向いているのは、エージェントが起動する（たいていは複数の）開発サーバーです。それぞれに専用のプライベートな名前を付け、共有・URL の取得・後片付けを CLI、MCP、Claude Code のスキルで手軽に行えます。

| やりたいこと | Tailscale だけ | TSLink |
|---|---|---|
| エージェントが起動する複数の開発サーバー、1 つずつ別の名前 | Serve は 1 台のマシンにつき 1 つの名前のもとで、デバイス単位のルートを設定。Services は名前ごとにサービス定義、承認、タグ付きホストが必要 | アプリごとに 1 つのノード。共有、URL の取得、後片付けを CLI（`share`、`url`、`remove`）、MCP（`share`、`url`、`unshare`）、スキルにまとめて提供 |
| 自分で起動した 1 つのアプリを、スマートフォンで開く | `tailscale serve 3000` で十分 | `tslink share 3000` |
| 1 人に 1 つのアプリを 7 日間 | ポリシールールを書き、JIT（一時アクセス）ツールか手動で削除 | `tslink people add alice@example.com --apps photos --for 7d`（HTTP/ファイル） |
| ブラウザー用リンクを 3 日間 | 公開の Funnel。アクセス制限と期限での停止は自分で用意 | `tslink guest create photos --for 3d --public --print-link`（HTTP のみ） |

非公開で共有する相手には Tailscale が必要です。ゲストリンクは公開リンクで、アクセス用の鍵として他の人にも渡せます。

[詳しい比較](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## 自分のアプリを、自分のデバイスで

- **アプリごとのアドレス。** Web アプリ、フォルダー、単一ファイル、TCP ポートがそれぞれ tailnet 内で名前を持つので、IP アドレスではなく名前で使えます。
- **最初は非公開。** ゲストリンクを作るか Funnel で公開するまで、何も公開されません。
- **マシン全体ではなく、アプリだけ。** 公開するアプリごとに専用のノードがあり、転送先はそのアプリだけです。ホストに Tailscale アプリを入れていなければ、TSLink がホストのほかのポートを tailnet に加えることはありません。
- **ホームページ**にアプリと稼働状態を一覧表示します。[ポータル](portal.md)
- **稼働監視と通知**はコマンドか webhook で。アクセス履歴には拒否されたリクエストも残ります。[稼働監視と通知](health-and-alerts.md) · [アクセス履歴](access-log.md)
- **15 種類のセルフホストアプリ用レシピ**。Home Assistant、Jellyfin、Immich、Ollama などがあります。`tslink apps detect` は既に動いているものを見つけます。[アプリ設定レシピ](apps.md)

## 共有したいときに

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

ゲストリンクと新しい公開 URL には期限があり、使えるのは Web アプリだけです。フォルダー、ファイル、TCP ポートは非公開のままです。[ユーザー共有](people.md) · [ゲストリンク](guest-links.md) · [一般公開](funnel.md)

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
| アプリを動かすマシン | TSLink（Tailscale を内蔵。Linux では systemd のユーザーセッションも必要） |
| 自分のデバイスと共有相手 | Tailscale アプリ |
| ゲスト | ブラウザー |

HTTPS アプリの名前は公開の証明書ログに載るので、人に見られても構わない名前にしてください。

<a id="documentation"></a>

## その他

[全ドキュメント](INDEX.md) · [CLI リファレンス](cli-reference.md) · [Serve・ngrok・Cloudflare との比較](comparison.md) · [貢献](../CONTRIBUTING.md) · [セキュリティ](../SECURITY.md)

Apache 2.0。TSLink は独立したプロジェクトで、Tailscale が開発または承認したものではありません。
