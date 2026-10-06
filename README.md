<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/tslink-mark-dark.svg">
    <img src="docs/assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Give each app on your computer or server its own private address on your Tailscale network, and decide who can reach it.</strong></p>

Open your web apps, folders, model APIs and databases from your own phone and laptop, with health checks and access history for each. Your AI agents can give apps they start on localhost a private address for your other devices and check them, within the role you give them. When someone else needs in, grant a named person access until a date, or open a web app to the public internet for a limited time.

**Requires Tailscale.** You need a Tailscale account (free for personal use), and each device that opens a private app needs the Tailscale app; guests and public visitors need only a browser. TSLink is an independent project, not made or endorsed by Tailscale. [Requirements](#requirements)

<p align="center"><a href="#quickstart">Quickstart</a> · <a href="#agents">For agents</a> · <a href="docs/comparison.md">Compared with Serve, ngrok and Cloudflare</a> · <a href="#documentation">Docs</a></p>
<p align="center">
<strong>English</strong> · <a href="docs/README.zh-CN.md">简体中文</a> · <a href="docs/README.zh-TW.md">繁體中文</a> · <a href="docs/README.ko.md">한국어</a> · <a href="docs/README.de.md">Deutsch</a> · <a href="docs/README.es.md">Español</a> · <a href="docs/README.fr.md">Français</a> · <a href="docs/README.it.md">Italiano</a> · <a href="docs/README.da.md">Dansk</a> · <a href="docs/README.ja.md">日本語</a> · <a href="docs/README.pl.md">Polski</a> · <a href="docs/README.ru.md">Русский</a> · <a href="docs/README.bs.md">Bosanski</a> · <a href="docs/README.ar.md">العربية</a> · <a href="docs/README.no.md">Norsk</a> · <a href="docs/README.pt-BR.md">Português (Brasil)</a> · <a href="docs/README.th.md">ไทย</a> · <a href="docs/README.tr.md">Türkçe</a> · <a href="docs/README.uk.md">Українська</a> · <a href="docs/README.bn.md">বাংলা</a> · <a href="docs/README.el.md">Ελληνικά</a> · <a href="docs/README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## What you can do

### Reach your own apps

- **An address for each app.** `tslink share 3000`, `tslink share ./photos` or `tslink add db --tcp localhost:5432` gives a web app, folder, file or TCP service its own private address in your tailnet (your private Tailscale network), such as `https://photos.<tailnet>.ts.net`. Each app is a separate Tailscale device, so you open apps by name instead of by IP address and port.
- **Private unless you choose otherwise.** TSLink keeps app access private by default, and your tailnet policy decides which devices can connect. It opens a public app endpoint only when you create a guest link or explicitly publish through Funnel.
- **One place to see them.** `tslink status --urls` lists the apps registered on this computer, and an optional private home page shows their addresses and health. [Portal](docs/portal.md)
- **Know when something breaks.** Background health checks can alert you through a command or webhook when an app goes down or comes back, or when its Tailscale sign-in is about to expire. Access history shows who opened which app and when, including denied requests. [Health and alerts](docs/health-and-alerts.md) · [Access history](docs/access-log.md)
- **App recipes.** Recipes cover 15 self-hosted apps, including Home Assistant, Jellyfin, Immich and Ollama, and `tslink apps detect` can find supported apps already listening locally. For large photo and video uploads, [raise the per-app upload limits](docs/sharing.md). [App recipes](docs/apps.md) · [Local AI](docs/local-ai.md)

### Let your agents work with them

When an agent starts a dev server, preview or local model API on `localhost`, your phone and other computers cannot reach that address. TSLink lets the agent give it a private address, tell you the exact URL and remove its registration again, within limits you set.

- **Share, check, undo.** `share --json` returns the name it registered and either the exact URL or a sign-in link for you to open. `url <name> --wait` and `status --urls --name <name>` report endpoint readiness, and `remove <name>` (MCP `unshare`) removes the share. [Agent quickstart](docs/agent-quickstart.md)
- **Made for automation.** Management commands other than `tslink mcp` accept `--json` and return versioned results with stable error codes. `tslink mcp` exposes app and access tools to a local MCP client over JSON-RPC. With caller bindings configured, `tslink serve --mcp` exposes those tools to MCP clients on your other devices over the tailnet. [JSON automation](docs/json-automation.md) · [Remote MCP](docs/remote-mcp.md)
- **Limited authority.** A local agent has owner authority by default. Give an agent a reduced role (`viewer`, `app-operator` or `people-manager`) that covers only the apps you name and caps how long any grant it makes can last. Changes made through MCP are recorded, and `tslink mcp-audit` shows them. Roles limit TSLink's tools, not the agent's own shell or files. [MCP scopes](docs/mcp-scopes.md)

### Share with people you choose

- **Named people, until a date.** `tslink people add alice@example.com --apps photos,notes --for 7d` lets that Tailscale login open those web and file apps until the deadline. `people update`, `extend` and `people remove` change or end it; removal denies their next request but cannot recall what they already downloaded. [People](docs/people.md) · [Durations](docs/durations.md)
- **Someone outside your tailnet.** Add `--invite --print-links` to get one message with a device invitation for each app, ready to send (this needs a user-owned API token). `--qr` prints a code for phone setup.
- **Requests.** People in your tailnet can ask for more time, or for an app you mark as requestable, from the home page. You approve with a duration in one command. [Access requests](docs/requests.md)

### Open a web app to the internet, for a while

- **Guest links.** `tslink guest create photos --for 3d --public --print-link` makes a browser link to one web app, optionally with a PIN, that you can revoke on its own. Guests need no Tailscale account. Anyone holding the link can use it, so it does not prove who visited. [Guest links](docs/guest-links.md)
- **An open public URL.** `tslink add preview --proxy localhost:3000 --funnel --public` publishes a web app to anyone with its URL. A new publication defaults to 24 hours; use `--funnel-ttl` to choose another lifetime. [Funnel](docs/funnel.md)
- New guest links and open public publications use Tailscale Funnel and have finite lifetimes (minimum 1 hour, default maximum 7 days, configurable by the owner). These public paths support HTTP proxy apps; direct folder/file services and raw TCP stay private.

Everything above ships in v0.1.0.

<a id="requirements"></a>

## Requirements

TSLink is built on Tailscale. It is an independent project, not made or endorsed by Tailscale, and Tailscale's own terms and [plans](https://tailscale.com/pricing) apply.

| Who | What they need |
|---|---|
| You | A Tailscale account with [MagicDNS and HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) turned on. The free Personal plan is for non-commercial use. |
| The computer or server running your apps | TSLink only. It embeds Tailscale, so no separate Tailscale install is needed. With the default setup, each fresh app node needs browser sign-in and may need device approval. [Stored credentials](docs/credentials-and-tags.md) support enrollment without per-app browser sign-in. |
| Your other devices | The Tailscale app, signed in to your tailnet. |
| People you choose | The Tailscale app and their own login. They either join your tailnet, which adds a user to your plan, or accept a device invitation for each app. Your tailnet policy must let them reach it. |
| Guests and public visitors | A browser. Your tailnet must allow Funnel, which Tailscale still calls beta. |

When an HTTPS certificate is issued for an app, its Tailscale device name and your tailnet DNS name appear in a public certificate log. Choose app names you are happy to have seen.

<a id="installation"></a>
<a id="quickstart"></a>

## Quickstart

Install on macOS or Linux with Homebrew. The macOS binary is signed with a Developer ID certificate and notarized by Apple. To upgrade later, run `brew upgrade --cask tslink`, then `tslink install` again if TSLink runs as a background service.

```bash
brew install --cask anydoor7/tap/tslink
```

On Windows, download `tslink_<version>_windows_<arch>.zip` from the [latest release](https://github.com/anydoor7/tslink/releases/latest), check it against `checksums.txt`, and run `tslink install` so TSLink starts when you sign in. The zip is not Authenticode-signed; [verify the release](docs/verify-release.md) through its signed checksums and attestations. Linux `.deb` and `.rpm` packages are on the same release page. To build from source instead, you need **Git and Go 1.26.6+**. Commands below use bash/zsh; see [macOS, Linux and Windows setup](docs/platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

You need a **Tailscale account** and [MagicDNS and HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private receiving devices need Tailscale and policy permission. TSLink embeds Tailscale on the app host.

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
