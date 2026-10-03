<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Access and manage your apps from anywhere.<br>Keep them private or share them on your terms.</strong></p>

Your computer or cloud server, your apps: reach them through an encrypted private network, or deliberately offer browser guest links or public access. Operate them yourself or through an agent.

<p align="center"><a href="#quickstart">Quickstart</a> · <a href="#agents">For agents</a> · <a href="#documentation">Docs</a></p>
<p align="center">
<strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.zh-TW.md">繁體中文</a> · <a href="docs/README.ko.md">한국어</a> · <a href="docs/README.de.md">Deutsch</a> · <a href="docs/README.es.md">Español</a> · <a href="docs/README.fr.md">Français</a> · <a href="docs/README.it.md">Italiano</a> · <a href="docs/README.da.md">Dansk</a> · <a href="docs/README.ja.md">日本語</a> · <a href="docs/README.pl.md">Polski</a> · <a href="docs/README.ru.md">Русский</a> · <a href="docs/README.bs.md">Bosanski</a> · <a href="docs/README.ar.md">العربية</a> · <a href="docs/README.no.md">Norsk</a> · <a href="docs/README.pt-BR.md">Português (Brasil)</a> · <a href="docs/README.th.md">ไทย</a> · <a href="docs/README.tr.md">Türkçe</a> · <a href="docs/README.uk.md">Українська</a> · <a href="docs/README.bn.md">বাংলা</a> · <a href="docs/README.el.md">Ελληνικά</a> · <a href="docs/README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Your apps, within reach

| What you need | What TSLink provides |
|---|---|
| Use your own apps across devices | Private addresses for home dashboards, local-only web pages, files, model APIs and TCP services on a PC or server. |
| Share with named people | Selected HTTP/file apps, verified Tailscale logins, expiry and revocation; recipients use Tailscale. [People](docs/people.md) |
| Let someone visit in a browser | Expiring guest links with an optional PIN for HTTP proxy apps, or explicitly public HTTPS via Funnel. Guest links can be forwarded; they do not verify a person's identity. [Guest links](docs/guest-links.md) |
| Keep an app collection manageable | Per-host inventory, a private home portal, health checks and alerts, access history, and CLI/MCP access management with scoped agent roles and audit receipts. [Portal](docs/portal.md) · [MCP scopes](docs/mcp-scopes.md) |

[App recipes](docs/apps.md), [upload limits](docs/sharing.md), [flexible lifetimes](docs/durations.md), and [QR onboarding and access requests](docs/requests.md) help with everyday maintenance. These capabilities are in this source tree.

<a id="installation"></a>
<a id="quickstart"></a>

## Quickstart

Install from source with **Git and Go 1.26.6+**; prebuilt releases and Homebrew are not yet published. Commands below use bash/zsh. See [macOS, Linux and Windows setup](docs/platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

You need repository access, a **Tailscale account**, and [MagicDNS and HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private receiving devices need Tailscale and policy permission. TSLink embeds Tailscale on the app host.

With your app already running on port 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Use a fresh name; if `share` returns another name, use that name in `url`. Complete any printed browser enrollment and device approval first, then open the exact app URL on a permitted device. `share` starts the background service when needed. This private first-use path needs no admin API token. Files can be shared with `tslink share ./report.html`; apps must already be running and files must exist. [Full setup](docs/getting-started.md)

Once it works for you, consider [starring TSLink](https://github.com/anydoor7/tslink) to help others discover it. It's entirely optional.

<a id="architecture"></a>

## How it fits together

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="docs/assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark.svg">
  <img src="docs/assets/service-map-light.svg" alt="One PC or cloud host: CLI/MCP manages a shared daemon and per-app nodes. Private devices connect through encrypted Tailscale transport; optional public HTTPS/Funnel reaches HTTP apps through a guest gate or explicit open publication." width="960">
</picture>

Think of a private, encrypted route to your apps. **Tailscale supplies the network transport and HTTPS; TSLink manages the apps' access on each host.** One daemon runs a separate embedded node per service. The private portal lists permitted apps; health and access history help you maintain them.

Public access is opt-in: guest links require the link and optional PIN; open Funnel is reachable by anyone with the URL. Both use public HTTPS, not private user identity. Raw TCP stays private and relies on tailnet policy and backend authentication. TSLink does not install apps, isolate host processes, create a cloud VPC or aggregate multiple hosts. Works with Tailscale; independent project. [Architecture and boundaries](docs/architecture.md)

<a id="agents"></a>

## For agents

Manage inventory, health, URLs and access through the CLI or MCP. Start with the [agent quickstart](docs/agent-quickstart.md); read live tool schemas and verify actual app access before reporting success.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI automation uses `--json`; MCP uses JSON-RPC over stdio. [Client setup](docs/mcp-clients.md) · [Remote MCP](docs/remote-mcp.md) · [Roles and app scopes](docs/mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Documentation and license

[All guides](docs/INDEX.md) · [CLI reference](docs/cli-reference.md) · [Local AI](docs/local-ai.md) · [Health](docs/health-and-alerts.md) · [Access history](docs/access-log.md) · [Roadmap](docs/roadmap.md)

Multi-host inventory is planned. [Contributions](CONTRIBUTING.md) and [security reports](SECURITY.md) are welcome. [Apache 2.0](LICENSE) permits commercial use; preserve [NOTICE](NOTICE) and [third-party notices](THIRD_PARTY_NOTICES.md). [Commercial cooperation](COMMERCIAL.md) is voluntary. Tailscale terms and plans apply separately.
