<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>PC やサーバー上のアプリごとに、自分の Tailscale ネットワーク内のプライベートアドレスを割り当て、アクセスできる相手を自分で決められます。</strong></p>

Web アプリ、フォルダー、モデル API、データベースを自分のスマートフォンやノート PC から開けて、アプリごとにヘルスチェックとアクセス履歴が付きます。AI エージェントは、あなたが与えたロールの範囲内で、自分が localhost で起動したアプリにほかの端末から開けるプライベートアドレスを割り当て、その状態を確認できます。ほかの人にも使ってもらうときは、指定した相手に期日までアクセスを許可するか、Web アプリを期間限定でインターネットに公開します。

**Tailscale が必要です。** Tailscale アカウント（個人利用は無料）が必要で、プライベートなアプリを開く端末にはそれぞれ Tailscale アプリが要ります。ゲストや一般の訪問者はブラウザーだけで使えます。TSLink は独立したプロジェクトで、Tailscale が開発・承認したものではありません。[必要なもの](#requirements)

<p align="center"><a href="#quickstart">クイックスタート</a> · <a href="#agents">エージェント向け</a> · <a href="comparison.md">Serve・ngrok・Cloudflare との比較</a> · <a href="#documentation">ドキュメント</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <strong>日本語</strong> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## できること

### 自分のアプリにアクセスする

- **アプリごとにアドレス。** `tslink share 3000`、`tslink share ./photos`、`tslink add db --tcp localhost:5432` で、Web アプリ、フォルダー、ファイル、TCP サービスに tailnet（自分専用の Tailscale ネットワーク）内のプライベートアドレスが付きます。たとえば `https://photos.<tailnet>.ts.net` です。アプリごとに別々の Tailscale デバイスになるので、IP アドレスとポート番号を覚えなくても名前で開けます。
- **自分で選ばない限りプライベート。** TSLink はアプリへのアクセスを既定でプライベートに保ち、どの端末が接続できるかは tailnet のポリシーで決まります。公開のアプリエンドポイントを開くのは、ゲストリンクを作るか、Funnel で明示的に公開したときだけです。
- **一か所で確認。** `tslink status --urls` でこの PC に登録されたアプリを一覧でき、任意で使えるプライベートなホームページにはそれらのアドレスと状態が表示されます。[ポータル](portal.md)
- **止まったらすぐ気づく。** アプリが停止・復旧したときや、Tailscale のサインイン期限が近づいたとき、バックグラウンドのヘルスチェックがコマンドや Webhook で知らせます。アクセス履歴には、誰がいつどのアプリを開いたかが、拒否されたリクエストも含めて残ります。[稼働監視と通知](health-and-alerts.md) · [アクセス履歴](access-log.md)
- **アプリのレシピ。** Home Assistant、Jellyfin、Immich、Ollama を含む 15 のセルフホストアプリ向けにレシピがあり、`tslink apps detect` でローカルですでに待ち受けている対応アプリを見つけられます。大きな写真や動画をアップロードするなら、[アプリごとのアップロード制限を引き上げてください](sharing.md)。[アプリ設定レシピ](apps.md) · [ローカル AI](local-ai.md)

### エージェントに任せる

エージェントが開発サーバー、プレビュー、ローカルのモデル API を `localhost` で起動すると、スマートフォンやほかの PC からはそのアドレスに届きません。TSLink を使うと、エージェントがそれにプライベートアドレスを割り当て、正確な URL を伝え、あとで登録を解除できます。どれも、あなたが決めた制限の範囲内で行われます。

- **共有、確認、取り消し。** `share --json` は登録した名前と、正確な URL またはあなたが開くサインインリンクを返します。`url <name> --wait` と `status --urls --name <name>` でエンドポイントの準備ができたかがわかり、`remove <name>`（MCP では `unshare`）で共有を削除します。[エージェントの入門](agent-quickstart.md)
- **自動化向け。** `tslink mcp` 以外の管理コマンドは `--json` に対応し、バージョン付きの結果と安定したエラーコードを返します。`tslink mcp` は JSON-RPC で、アプリとアクセスのツールをローカルの MCP クライアントに提供します。呼び出し元のバインディングを設定すれば、`tslink serve --mcp` が tailnet 経由で、ほかの端末の MCP クライアントに同じツールを提供します。[JSON 自動化](json-automation.md) · [リモート MCP](remote-mcp.md)
- **権限は限定的。** ローカルのエージェントは既定でオーナー権限を持ちます。エージェントには制限付きのロール（`viewer`、`app-operator`、`people-manager`）を与えられます。対象は指定したアプリだけで、エージェントが付与できるアクセス期間にも上限があります。MCP 経由の変更は記録され、`tslink mcp-audit` で確認できます。ロールが制限するのは TSLink のツールだけで、エージェント自身のシェルやファイルには及びません。[MCP 権限](mcp-scopes.md)

### 選んだ相手と共有する

- **指定した相手に、期日まで。** `tslink people add alice@example.com --apps photos,notes --for 7d` で、その Tailscale ログインが期限までそれらの Web アプリとファイルアプリを開けるようになります。`people update`、`extend`、`people remove` で変更や終了ができます。削除すると次のリクエストから拒否されますが、すでにダウンロードされたものは取り戻せません。[ユーザー共有](people.md) · [有効期間](durations.md)
- **tailnet の外にいる相手。** `--invite --print-links` を付けると、アプリごとのデバイス招待をまとめた、そのまま送れるメッセージができます（ユーザー所有の API トークンが必要です）。`--qr` はスマートフォン設定用のコードを表示します。
- **アクセス申請。** tailnet 内の人は、ホームページから期間の延長や、申請可能にしたアプリへのアクセスを申請できます。期間を指定して、コマンド1つで承認します。[アクセス申請](requests.md)

### Web アプリを期間限定でインターネットに公開する

- **ゲストリンク。** `tslink guest create photos --for 3d --public --print-link` で、1つの Web アプリへのブラウザー用リンクを作れます。PIN は任意で、リンクごとに取り消せます。ゲストに Tailscale アカウントは要りません。リンクを持っていれば誰でも使えるので、誰が訪れたかの証明にはなりません。[ゲストリンク](guest-links.md)
- **一般公開 URL。** `tslink add preview --proxy localhost:3000 --funnel --public` で、URL を知っている人なら誰でも Web アプリを開けるようになります。新しい公開は既定で 24 時間有効で、`--funnel-ttl` で別の期間を選べます。[Funnel](funnel.md)
- 新しいゲストリンクと新しい一般公開は、どちらも Tailscale Funnel を使い、有効期間が決まっています（最短 1 時間、既定の上限 7 日、上限はオーナーが変更可能）。これらの公開経路が対応するのは HTTP プロキシのアプリで、フォルダーやファイルを直接提供するサービスと生の TCP はプライベートのままです。

ここまでの機能はすべて v0.1.0 に含まれています。

<a id="requirements"></a>

## 必要なもの

TSLink は Tailscale の上に作られています。独立したプロジェクトで、Tailscale が開発・承認したものではありません。Tailscale 自身の規約と[プラン](https://tailscale.com/pricing)が適用されます。

| 対象 | 必要なもの |
|---|---|
| あなた | [MagicDNS と HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) を有効にした Tailscale アカウント。無料の Personal プランは非商用利用向けです。 |
| アプリを動かす PC やサーバー | TSLink だけ。Tailscale を内蔵しているので、Tailscale を別にインストールする必要はありません。既定の設定では、新しいアプリのノードごとにブラウザーでのサインインが必要で、デバイス承認が必要な場合もあります。[保存した認証情報](credentials-and-tags.md)を使えば、アプリごとのブラウザーサインインなしで登録できます。 |
| 自分のほかの端末 | tailnet にサインインした Tailscale アプリ。 |
| 選んだ相手 | Tailscale アプリと本人のログイン。あなたの tailnet に参加する（プランのユーザーが1人増えます）か、アプリごとのデバイス招待を受け入れます。tailnet のポリシーで相手のアクセスを許可しておく必要があります。 |
| ゲストと一般の訪問者 | ブラウザー。tailnet で Funnel を許可しておく必要があります。Funnel は Tailscale ではまだベータ扱いです。 |

アプリに HTTPS 証明書が発行されると、その Tailscale デバイス名と tailnet の DNS 名が公開の証明書ログに載ります。人に見られても構わないアプリ名を付けてください。

<a id="installation"></a>
<a id="quickstart"></a>

## クイックスタート

macOS と Linux では Homebrew でインストールします。macOS 版のバイナリは Developer ID 証明書で署名され、Apple の公証を受けています。後でアップグレードするには `brew upgrade --cask tslink` を実行し、TSLink をバックグラウンドサービスとして動かしている場合は `tslink install` をもう一度実行します。

```bash
brew install --cask anydoor7/tap/tslink
```

Windows では[最新リリース](https://github.com/anydoor7/tslink/releases/latest)から `tslink_<version>_windows_<arch>.zip` をダウンロードし、`checksums.txt` と照合してから `tslink install` を実行すると、サインイン時に TSLink が起動します。zip には Authenticode 署名がないため、署名付きのチェックサムとビルド来歴の証明で[リリースを検証](verify-release.md)してください。Linux 用の `.deb` と `.rpm` パッケージも同じリリースページにあります。ソースからビルドする場合は **Git と Go 1.26.6+** が必要です。以下のコマンドは bash/zsh 用です。[macOS・Linux・Windows の設定](platforms.md)

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
