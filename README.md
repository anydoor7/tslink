<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Private addresses for your apps, on your Tailscale network.</strong></p>
<p align="center">Open them from your own devices. Share one with a person or a link, until a date you pick.</p>
<p align="center"><strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.ja.md">日本語</a> · <a href="docs/README.ko.md">한국어</a> · <a href="docs/README.es.md">Español</a> · <a href="docs/INDEX.md#translated-homepages">More languages</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Install

```sh
brew install --cask anydoor7/tap/tslink
```

Linux `.deb` and `.rpm` packages and Windows builds are on the [latest release](https://github.com/anydoor7/tslink/releases/latest). The first time you share an app, TSLink prints a Tailscale sign-in link for it. [Getting started](docs/getting-started.md)

<a id="why"></a>

## When you need TSLink

Serve is enough for one app on your own devices. TSLink puts app addresses, deadlines and access changes in one workflow.

| Job | Tailscale alone | TSLink |
|---|---|---|
| One web app on your phone | `tailscale serve 3000` is enough | `tslink share 3000` |
| Several apps, separate names | Services setup, or separate nodes | One `share`/`add` per app; enroll each node |
| One person, one app, seven days | Policy rules, then a JIT tool or manual removal | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/files) |
| Browser link, three days | Public Funnel; add a gate and scheduled shutdown | `tslink guest create photos --for 3d --public --print-link` (HTTP only) |

Private recipients need Tailscale. Guest links are public, forwardable bearer links.

[Full comparison](docs/comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Your apps, on your own devices

- **An address for each app.** Web apps, folders, single files and TCP ports each get their own name in your tailnet, so you use names instead of IP addresses.
- **Private by default.** Nothing is public until you create a guest link or publish through Funnel.
- **One app, not the whole machine.** Each app you publish gets its own node that forwards to that app only. Without the Tailscale app on the host, TSLink adds no other host ports to your tailnet.
- **A home page** that lists your apps with their health. [Portal](docs/portal.md)
- **Health checks and alerts** by command or webhook, and an access log that includes denied requests. [Health and alerts](docs/health-and-alerts.md) · [Access history](docs/access-log.md)
- **Recipes for 15 self-hosted apps**, including Home Assistant, Jellyfin, Immich and Ollama. `tslink apps detect` finds the ones already running. [App recipes](docs/apps.md)

## Share when you want to

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Guest links and new public URLs expire and work for web apps only; folders, files and TCP ports stay private. [People](docs/people.md) · [Guest links](docs/guest-links.md) · [Public access](docs/funnel.md)

<a id="agents"></a>

## For AI agents

A dev server an agent starts on `localhost` is out of reach from your phone. TSLink lets the agent give it a private address, report the exact URL and remove it when done.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI or MCP.** Management commands take `--json` and return versioned results; `tslink mcp` offers app and access operations over MCP.
- **One file, not the folder.** An agent can share just its HTML report with `tslink share ./report.html`; other files in that folder stay unreachable.
- **Limited roles.** `viewer`, `app-operator` or `people-manager`, scoped to the apps you name. `tslink mcp-audit` shows what an agent changed. Roles limit TSLink's tools, not the agent's own shell.

[Agent quickstart](docs/agent-quickstart.md) · [MCP scopes](docs/mcp-scopes.md) · [Remote MCP](docs/remote-mcp.md)

<a id="architecture"></a>

## How it works

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="docs/assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark.svg">
  <img src="docs/assets/service-map-light.svg" alt="One PC or cloud host: CLI/MCP manages a shared daemon and per-app nodes. Private devices connect through encrypted Tailscale transport; optional public HTTPS/Funnel reaches HTTP apps through a guest gate or explicit open publication." width="960">
</picture>

One background process runs a separate Tailscale node for each app. Tailscale provides the tailnet transport and HTTPS certificates. Private web and file access can be limited by Tailscale identity with `--allow` and people grants; raw TCP relies on your tailnet policy and the app's own login. [Architecture](docs/architecture.md)

<a id="requirements"></a>

## Requirements

| Who | Needs |
|---|---|
| You | A Tailscale account with MagicDNS and HTTPS turned on |
| The machine running your apps | TSLink, which embeds Tailscale (on Linux, a systemd user session) |
| Your devices, and people you share with | The Tailscale app |
| Guests | A browser |

Names of HTTPS apps appear in public certificate logs, so pick names you are happy for others to see.

<a id="documentation"></a>

## More

[All docs](docs/INDEX.md) · [CLI reference](docs/cli-reference.md) · [Compared with Serve, ngrok and Cloudflare](docs/comparison.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)

Apache 2.0. TSLink is an independent project, not made or endorsed by Tailscale.
