<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Give your local apps, models, and files their own private address.</strong><br>
  Open them from another permitted device on your Tailscale network.
</p>

<p align="center">
  <a href="LICENSE"><img src="docs/assets/badge-license.svg" alt="License: Apache 2.0"></a>
  <a href="go.mod"><img src="docs/assets/badge-go.svg" alt="Go 1.26.6 or newer"></a>
  <a href="docs/architecture.md"><img src="docs/assets/badge-tsnet.svg" alt="Tailscale: embedded tsnet nodes"></a>
  <a href="#agents"><img src="docs/assets/badge-mcp.svg" alt="MCP: 19 tools"></a>
</p>

<p align="center">
  <strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.zh-TW.md">繁體中文</a> · <a href="docs/README.ko.md">한국어</a> · <a href="docs/README.de.md">Deutsch</a><br>
  <a href="docs/README.es.md">Español</a> · <a href="docs/README.fr.md">Français</a> · <a href="docs/README.it.md">Italiano</a> · <a href="docs/README.da.md">Dansk</a> · <a href="docs/README.ja.md">日本語</a><br>
  <a href="docs/README.pl.md">Polski</a> · <a href="docs/README.ru.md">Русский</a> · <a href="docs/README.bs.md">Bosanski</a> · <a href="docs/README.ar.md">العربية</a> · <a href="docs/README.no.md">Norsk</a><br>
  <a href="docs/README.pt-BR.md">Português (Brasil)</a> · <a href="docs/README.th.md">ไทย</a> · <a href="docs/README.tr.md">Türkçe</a> · <a href="docs/README.uk.md">Українська</a><br>
  <a href="docs/README.bn.md">বাংলা</a> · <a href="docs/README.el.md">Ελληνικά</a> · <a href="docs/README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installation

You need **Go 1.26.6+** and Git. Prebuilt releases and a
Homebrew cask have not been published; install from source. These examples use
**bash or zsh**; see [platform support](docs/platforms.md) for Windows and background-service requirements.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Use a Tailscale account with [MagicDNS and HTTPS enabled](https://tailscale.com/docs/how-to/set-up-https-certificates).
The receiving device must be signed into your Tailscale network (**tailnet**), with policy
permission to reach the service. TSLink embeds Tailscale on the publishing host.

### Share your first page

Create a page; TSLink serves it directly and starts its background service when needed:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

If TSLink prints an enrollment URL, open it to authorize the node; your tailnet may also
require administrator device approval. Then get the exact address:

```bash
tslink url demo --wait
```

Open that URL on a permitted device. No API token is needed for this first share.
[Full setup and lifecycle details →](docs/getting-started.md)

<a id="use-cases"></a>

## What will you share?

Files must exist; app, database, and model backends must already be running on the specified ports.

| Use case | Command |
|---|---|
| Open a local app from another device | `tslink share 3000` |
| Browse a directory of files | `tslink share ./public --name files` |
| Read a generated HTML report on your phone | `tslink share ./report.html --name report` |
| Connect to a local database over TCP | `tslink add database --tcp localhost:5432` |
| Use a local model HTTP API, such as Ollama | `tslink add model --proxy localhost:11434` |

For Ollama, get the exact URL with `tslink url model --wait`; an OpenAI-compatible client's
`baseURL` uses that URL plus `/v1`. [Local models and private-data workflows →](docs/local-ai.md)

For several apps on one host, TSLink combines named service nodes, HTTP identity allow lists, Funnel expiry and MCP management. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) may be sufficient for one app on your own devices.

<a id="architecture"></a>

## Architecture

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="docs/assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark.svg">
  <img src="docs/assets/service-map-light.svg" alt="Example service map: App, Docs, Database, and Model are distinct named nodes in one tailnet. Apps, files, and model APIs use HTTPS; the database uses private TCP." width="960">
</picture>

**One tailnet, distinct service nodes.** A shared daemon runs one embedded tsnet node per
service, forwarding HTTP, serving files, or proxying TCP. Registry changes take effect while
it runs. Each node has its own network identity; services share the publishing host.
[Architecture details →](docs/architecture.md)

| Building block | Role |
|---|---|
| [Go](go.mod) | Native command-line binary |
| [Tailscale tsnet](docs/architecture.md) | Service nodes and tailnet transport |
| [Cobra](https://github.com/spf13/cobra) | Commands and help |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Agent transports |
| OS keychain and user service manager | Optional credentials and background operation |

Services stay inside your tailnet unless you explicitly enable [public Funnel](docs/getting-started.md#more-examples).
HTTP/file services support identity allow lists (`WhoIs`, `--allow`); raw TCP uses tailnet policy and the backend's
own authentication. See [sharing boundaries](docs/sharing.md).

TSLink does not install apps, run models, sandbox host processes or aggregate multiple hosts. Networking, encryption and HTTPS come from Tailscale; TSLink is an independent project.

<a id="agents"></a>

## For agents

The **19 MCP tools** let an agent share reports, manage services, retrieve URLs, and inspect
setup. Connect a local MCP client to the installed binary:

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

MCP manages TSLink; applications use the model HTTP API for inference.
See [MCP clients](docs/mcp-clients.md), [remote MCP](docs/remote-mcp.md), and the
[agent operating guide](AGENTS.md) for configuration and automation.

CLI automation supports `--json` with `schema_version` set to `1`; inspect `tslink status --urls --json`. Local MCP uses JSON-RPC over stdio. See [JSON automation](docs/json-automation.md).

<a id="roadmap"></a>

## What's coming

Items marked Merging, In review or Planned are not included in the source installation above.

| Use case | Status |
|---|---|
| F1. Give a relative private HTTP/file app access for 3 days, with a message bundling app invitations; the recipient still needs Tailscale. | Merging |
| F2. Check app health and receive outage or expiry alerts through an optional command or webhook. | Merging |
| F3. Find supported loopback apps and preview self-hosted app recipes before sharing. | Merging |
| F8. Set each HTTP app's upload size and request timeouts for large uploads and slow clients. | Merging |
| F9. Restart a crashed Windows daemon while signed in, using a scheduled task and built-in supervisor. | Merging |
| F4. See who opened which app in local access logs, with `prefix`, `full` or `off` path recording. | In review |
| F5. Open one home page listing permitted apps, with enrollment handoff for owners; visitors still need Tailscale. | In review |
| F6. Give an agent a role and an app scope, with audit receipts for its mutations. | In review |
| F10. Let a guest open one HTTP app in a browser without installing Tailscale, using an expiring link and optional PIN through gated public Funnel. | In review |
| F11. Choose presets or custom lifetimes of at least 1 hour, with a configurable 7-day guest maximum by default. | In review |
| F12. Help phone users join through a QR code and approve app-access or extra-time requests in one action. | In review |
| F7. View apps from several hosts in one inventory. | Planned |

<a id="documentation"></a>

## Documentation & license

[Getting started](docs/getting-started.md) · [Local models](docs/local-ai.md) ·
[CLI reference](docs/cli-reference.md) · [Platforms](docs/platforms.md) · [Roadmap](docs/roadmap.md)

Contribute through [CONTRIBUTING.md](CONTRIBUTING.md); report vulnerabilities using
[SECURITY.md](SECURITY.md).

TSLink uses the unmodified [Apache License 2.0](LICENSE), including commercial use.
Keep applicable [NOTICE](NOTICE) and [third-party notices](THIRD_PARTY_NOTICES.md) when redistributing.
[Commercial cooperation](COMMERCIAL.md) is voluntary and adds no license condition.
Tailscale service terms and plans apply separately.
