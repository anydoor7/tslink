<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink is an open-source CLI and MCP server that gives each app on your computer a private <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> address, so you or your coding agent can open a running dev server on your phone. Independent project, not made by Tailscale.</p>
<p align="center">Your phone needs the Tailscale app, signed in to the same tailnet.</p>
<p align="center">Docs and guides: <a href="https://tslink.md">tslink.md</a></p>

[![MCP Badge](https://lobehub.com/badge/mcp/anydoor7-tslink)](https://lobehub.com/mcp/anydoor7-tslink)

<p align="center"><strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.ja.md">日本語</a> · <a href="docs/README.ko.md">한국어</a> · <a href="docs/README.es.md">Español</a> · <a href="docs/INDEX.md#translated-homepages">More languages</a></p>

```text
# Claude Code, after installing the CLI (see Install below)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Local MCP clients that use mcpServers JSON (others: see your client's MCP setup)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Ask your agent: "Share the dev server so I can open it on my phone."
```

The same thing by hand, with the dev server already listening on port 5173. With `--json`, `share` reports the registered name as `data.name`. Use that name in the later commands: it differs from `--name` when that name was taken, for example `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, or data.auth_url (a Tailscale sign-in link) when status is needs_login
tslink url <name> --wait              # after you finish sign-in: the exact URL
tslink remove <name>                  # when you are done; skip it if share failed. The dev server keeps running
```

<a id="installation"></a>
<a id="quickstart"></a>

## Install

macOS hosts require macOS 13 Ventura or later ([platform support](docs/platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

On Windows, install with Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

To upgrade, run `brew upgrade --cask tslink` or `scoop update; scoop update tslink`. If TSLink runs as a background service, run `tslink install` again afterwards.

Linux `.deb` and `.rpm` packages and Windows builds are on the [latest release](https://github.com/anydoor7/tslink/releases/latest). The first time you share an app, TSLink prints a Tailscale sign-in link for it. [Getting started](docs/getting-started.md)

<a id="agents"></a>

## For AI agents

A dev server an agent starts on `localhost` is out of reach from your phone. TSLink lets the agent give it a private address, hand you the exact URL and remove the share when you are done. Your phone needs the Tailscale app, signed in to the same tailnet.

Tested on 2026-10-22 with Vite 8.3.5 and an iPhone 15 Pro: the agent shared the dev server over MCP, the phone opened the private URL, and the page updated on save without a manual refresh.

For Claude Code, install the CLI first, then run:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

The plugin includes a [preview skill](skills/tslink-preview/SKILL.md) and the
MCP connection below. The marketplace stays in this repository and fetches
the plugin from [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin).
The canonical skill stays here; the plugin carries a CI-checked copy. Other
agents can use the same skill or connect MCP directly. This integration
requires the local CLI and does not run in claude.ai chat.
[Setup and enrollment](docs/agent-integrations.md)

For a local MCP client that uses `mcpServers` JSON (other clients: see their MCP setup instructions and the [MCP clients guide](docs/mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **One name per dev server.** Each share gets its own private `https://<name>.<tailnet>.ts.net` address at the root path on port 443. A taken name gets a number suffix, so use the URL the agent gets back.
- **Needs a person for each new app.** In the default setup, each fresh app node needs a person to complete Tailscale enrollment. If the result is `needs_login`, finish sign-in, then run `tslink url <name> --wait` for the exact URL. Separately, a new name can take a while to become reachable: 36 to 38 seconds in our two test runs.
- **Who can open it.** By default, tailnet devices your policy allows. MCP `share` accepts `allow` to limit it to your login; from the CLI, use `tslink add web --proxy localhost:5173 --allow you@example.com`. Names appear in public certificate logs, so they are not secret.
- **Edit, save, see it.** Saving code updates the page on your phone. Your app's own Host and Origin rules can still block requests; Vite needed no `allowedHosts` change in our test, and Next.js is untested and still needs `allowedDevOrigins`.
- **Clean up.** The agent calls `unshare`, or you run `tslink remove <name>`, always with the name the share result returned. Removal unregisters the share and attempts to delete its tailnet node when TSLink can prove it owns it, so check the result for cleanup or retained-state fields. It does not stop the dev server.
- **CLI or MCP.** Management commands take `--json` and return versioned results; `tslink mcp` offers app and access operations over MCP.
- **One file, not the folder.** An agent can share just its self-contained HTML report with `tslink share ./report.html`; other files in that folder stay unreachable.
- **Limited roles.** `viewer`, `app-operator` or `people-manager`, scoped to the apps you name; sharing a new app needs the `owner` role. `tslink mcp-audit` shows what an agent changed. Roles limit TSLink's tools, not the agent's own shell.

[Agent quickstart](docs/agent-quickstart.md) · [MCP scopes](docs/mcp-scopes.md) · [Remote MCP](docs/remote-mcp.md)

<a id="why"></a>

## When you need TSLink

Serve is enough for one app you start yourself and open on your own phone. TSLink is for the dev servers an agent starts, often several, when each should get its own private name and the share, URL and cleanup steps should come ready-made in the CLI, MCP and a Claude Code skill.

| Job | Tailscale alone | TSLink |
|---|---|---|
| Several dev servers your agent starts, one name each | Serve sets device-level routes under one name per machine; Services gives each name a definition, approval and a tagged host | One node per app, with sharing, URL lookup and cleanup packaged in the CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) and a skill |
| One app you start yourself, on your phone | `tailscale serve 3000` is enough | `tslink share 3000` |
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

## Privacy Policy

This section covers the local TSLink CLI, MCP server and agent plugin.
Its [HTTPS policy URL](https://github.com/anydoor7/tslink/blob/main/README.md#privacy-policy)
is separate from the [website privacy policy](https://tslink.md/privacy),
which covers the website.

- **Data and use.** TSLink processes the app traffic you choose to proxy or
  serve, plus service configuration, Tailscale identities and access rules,
  to route requests and enforce access. Optional people, invitation, guest
  and access-request features store their associated records locally.
  Enabled access history records identities, timestamps, app names, methods,
  statuses, byte counts, durations and access decisions, plus coarse IP
  prefixes for unidentified tailnet callers, node names and tags, and MCP/guest
  audit receipts (including guest link IDs, not bearer tokens). The default path
  mode retains the first path segment with token-shaped segments redacted;
  full-path and no-path modes are configurable. Access history excludes
  query strings, request headers and bodies. Diagnostic logs are a separate
  surface and can contain sensitive details, including enrollment links.
- **Local storage.** Configuration, registry, runtime state, node keys and
  logs live in TSLink's configuration directory, normally
  `~/.config/tslink` on macOS/Linux or `%AppData%\tslink` on Windows
  ([platform details](docs/platforms.md)). `TSLINK_CONFIG_DIR` can override it.
  Optional administrative credentials use the system credential store;
  macOS/Linux have a conditional file fallback, while Windows has none.
  See [credential storage](docs/credentials-and-tags.md).
- **Third parties.** Embedded Tailscale contacts the coordination service,
  uses encrypted peer or relay transport and obtains HTTPS certificates.
  Its SDK uploads diagnostic logs to Tailscale by default. HTTPS names appear
  in public certificate transparency logs. Administrative operations can send
  device, policy and invitation data to the Tailscale API. Custom coordination
  servers and configured alert webhooks or commands receive the data needed
  for those features. TSLink has no separate maintainer analytics collector.
  Your MCP client receives tool arguments and results, which may include
  identities, URLs and requested logs; the client's model provider and
  retention policy also apply. Publishing through Funnel or guest links
  exposes the selected app through public infrastructure.
- **Retention and deletion.** Local access history defaults to 30 UTC calendar
  days including today and a 64 MiB data cap, configurable or disableable;
  oldest segments are evicted while the daemon runs. The mutation journal is
  separately bounded to 1,024 entries or 1 MiB. The daemon's stderr log
  (`tslink.err.log`) rotates at 8 MiB and keeps one previous file when TSLink
  can confirm the descriptor is append-only. `tslink.out.log` and
  supervisor-managed copies have no TSLink retention limit; supervisor
  policies may differ.
  Configuration and node state persist until removed. `tslink remove <name>`
  removes a service and performs ownership-safe cleanup; it does not erase
  historical logs. `tslink logout` clears credentials and node authentication
  state, not all configuration or logs. See [access history](docs/access-log.md)
  and [access-request retention](docs/requests.md). Third-party retention is
  governed by those providers, including [Tailscale](https://tailscale.com/privacy-policy).
- **Contact.** Use the [issue tracker](https://github.com/anydoor7/tslink/issues)
  for non-sensitive privacy questions. Never post credentials, private links,
  logs or personal data in public issues. For sensitive security reports,
  follow [SECURITY.md](SECURITY.md).

<a id="documentation"></a>

## More

[All docs](docs/INDEX.md) · [CLI reference](docs/cli-reference.md) · [Compared with Serve, ngrok and Cloudflare](docs/comparison.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)

Apache 2.0. TSLink is an independent project, not made or endorsed by Tailscale.
