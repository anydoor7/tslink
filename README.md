<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Share the apps on your computer with the people you choose, for as long as you choose.</strong><br>
  Each app gets its own private address on your Tailscale network. See who has access, and take it back.
</p>

<p align="center">
  <a href="#quickstart">Quickstart</a> · <a href="#agents">For agents</a> · <a href="docs/getting-started.md">Docs</a> ·
  <strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.ja.md">日本語</a> · <a href="docs/INDEX.md#translated-homepages">+19 languages</a>
</p>

## What people use it for

- **Open your own work on your phone.** A report your script generated, a dev server, a notebook, or a local model API, at a private HTTPS address that permitted devices can reach.
- **Give one person one app, for a while.** Let your partner use the photo library for a week, or a colleague try your preview for three days. Access ends by itself; you can also end it early.
- **Let your agent do the sharing.** Your coding agent just built a dashboard. Ask it to share the dashboard with you and your teammate until Friday. It can also tell you what is shared right now, and take a share back.

Your apps keep running where they already run. TSLink decides who can reach each one, and keeps one list of what is shared, with whom, and until when.

<a id="quickstart"></a>

## Quickstart

You need **Go 1.26.6+**, Git, and a Tailscale account with [MagicDNS and HTTPS enabled](https://tailscale.com/docs/how-to/set-up-https-certificates). Prebuilt releases are not published yet, so install from source:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Share a page:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

The first time, TSLink prints a sign-in link to enroll the new service node; your tailnet may also ask an admin to approve the device. After enrollment, open the service URL on a permitted device signed into your tailnet. No API token is needed.

Check what is shared, then remove the demo:

```bash
tslink status --urls
tslink remove demo
```

Other things you can share once their backend is running:

| What | Command |
|---|---|
| A local web app | `tslink share 3000` |
| A folder of files | `tslink share ./public --name files` |
| A local model API, such as Ollama | `tslink add model --proxy localhost:11434` |
| A database over private TCP | `tslink add database --tcp localhost:5432` |
| A known self-hosted app (Jellyfin, Immich, Home Assistant, and 13 more) | `tslink apps detect`, then `tslink apps share jellyfin --yes` |

[Getting started, platforms and background service →](docs/getting-started.md)

## Choose who can open it

| Audience | What the recipient needs | Who they are | Ends |
|---|---|---|---|
| **Your own devices** | Signed into your tailnet | Verified Tailscale login | When you remove the app |
| **Named people** (private HTTP/files) | A Tailscale login; outsiders accept one invitation per app | Verified Tailscale login | At the deadline you set (`--for 7d`), or `tslink people remove` |
| **Anyone with the URL** (Funnel) | A browser | Anyone; the app's own login applies | After 24 hours by default (`--funnel-ttl`) |
| **Browser guest link** *(coming)* | A browser, plus an optional PIN | Whoever holds the link | At its own deadline, or on revoke |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

For private HTTP and file shares, deadlines are checked on every request. Revoking access stops new requests; it cannot recall downloaded data or close streams and WebSocket connections already accepted. [Sharing with people →](docs/people.md) · [Sharing boundaries →](docs/sharing.md)

<a id="agents"></a>

## For agents

TSLink includes an MCP server, so an agent can share, list, explain and remove shares in the same way you do. Add it to a local MCP client:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Exact results.** CLI automation supports `--json` with `schema_version: 1` and stable error codes; `tslink mcp` uses JSON-RPC instead. `tslink manifest` describes every command and flag. Agents should fetch real URLs with `tslink url <name> --wait` rather than build them.
- **Honest pending states.** A new node that still needs a human sign-in reports `needs_login` instead of pretending to be ready.
- **Authority.** Local MCP runs with your user's authority. Remote MCP is an opt-in, tailnet-only endpoint limited to the logins or tags you list. Per-agent roles, app scopes and action receipts are *coming*.

TSLink's MCP operates TSLink itself. If you publish another MCP server through TSLink, that server still needs its own tool permissions.
[Agent guide →](docs/agents.md) · [MCP clients →](docs/mcp-clients.md) · [Remote MCP →](docs/remote-mcp.md) · [JSON automation →](docs/json-automation.md)

## When to use something else

| If you want | Consider |
|---|---|
| One local service on your own devices, using the Tailscale client you already run | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Admin-managed services with stable names across many hosts | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| A public URL for a webhook or API demo, with no Tailscale account | [ngrok](https://ngrok.com/docs/start) or [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| To install and run self-hosted apps, not only share them | [Umbrel](https://umbrel.com) or [Coolify](https://coolify.io) |
| An organization-wide identity-aware access platform | [Pangolin](https://github.com/fosrl/pangolin) or [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink fits when one person runs several apps and wants per-app, per-person, time-limited access that they and their agent can inspect.

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark.svg">
  <img src="docs/assets/service-map-light.svg" alt="App, Docs, Database and Model are separate named nodes in one tailnet, run by one TSLink daemon on the publishing computer." width="720">
</picture>

One background daemon runs an embedded Tailscale node for each app, so each app has its own name and address. For private HTTP and file shares, `WhoIs` and people grants or `--allow` rules control access; people deadlines are checked on each request. Raw TCP uses tailnet policy and the backend's authentication. Tailscale provides tailnet transport, encryption and certificates; TSLink is an independent project. All apps share the publishing computer, so TSLink does not isolate them from each other. [Architecture →](docs/architecture.md)

## Status

Available now: private per-app addresses, named people with deadlines and invitation bundles, public Funnel with expiry, app health checks and alerts, self-hosted app recipes, per-app request limits, Windows crash restart, the CLI and MCP. Also available: browser guest links, flexible durations, an access log, a home page of your apps, scoped agent roles, QR onboarding and access requests.

Viewing several computers in one list is planned. [Roadmap →](docs/roadmap.md)

## Documentation and license

[Getting started](docs/getting-started.md) · [CLI reference](docs/cli-reference.md) · [Platforms](docs/platforms.md) · [Local models](docs/local-ai.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)

Apache License 2.0, including commercial use. Keep [NOTICE](NOTICE) and [third-party notices](THIRD_PARTY_NOTICES.md) when redistributing. Tailscale's terms and plans apply separately.
