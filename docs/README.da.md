<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Private adresser til dine apps, på dit Tailscale-netværk.</strong></p>
<p align="center">Åbn dem fra dine egne enheder. Del en med en person eller via et link, indtil en dato du vælger.</p>
<p align="center"><strong>Dansk</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Flere sprog</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installation

```sh
brew install --cask anydoor7/tap/tslink
```

Linux-pakker (`.deb` og `.rpm`) og Windows-builds ligger under [seneste udgivelse](https://github.com/anydoor7/tslink/releases/latest). Første gang du deler en app, viser TSLink et Tailscale-loginlink til den. [Kom godt i gang](getting-started.md)

<a id="use-cases"></a>

## Dine apps, på dine egne enheder

- **En adresse til hver app.** Webapps, mapper, enkeltfiler og TCP-porte får hver deres eget navn i dit tailnet, så du bruger navne i stedet for IP-adresser.
- **Privat som standard.** Intet er offentligt, før du opretter et gæstelink eller udgiver via Funnel.
- **En startside**, der viser dine apps og deres tilstand. [Portal](portal.md)
- **Sundhedstjek og alarmer** via kommando eller webhook, og en adgangslog, der også viser afviste forespørgsler. [Sundhed og alarmer](health-and-alerts.md) · [Adgangshistorik](access-log.md)
- **Opskrifter til 15 selvhostede apps**, blandt andet Home Assistant, Jellyfin, Immich og Ollama. `tslink apps detect` finder dem, der allerede kører. [Appopskrifter](apps.md)

## Del, når du vil

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Gæstelinks og nye offentlige URL'er udløber og virker kun til webapps; mapper, filer og TCP-porte forbliver private. [Personer](people.md) · [Gæstelinks](guest-links.md) · [Offentlig adgang](funnel.md)

<a id="agents"></a>

## Til AI-agenter

En udviklingsserver, som en agent starter på `localhost`, kan du ikke nå fra din telefon. Med TSLink kan agenten give den en privat adresse, oplyse den præcise URL og fjerne den igen bagefter.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI eller MCP.** Styringskommandoerne tager `--json` og returnerer versionerede resultater; `tslink mcp` tilbyder handlinger for apps og adgang over MCP.
- **Begrænsede roller.** `viewer`, `app-operator` eller `people-manager`, afgrænset til de apps du angiver. `tslink mcp-audit` viser, hvad en agent har ændret. Roller begrænser TSLinks værktøjer, ikke agentens egen shell.

[Agentguide](agent-quickstart.md) · [MCP-rettigheder](mcp-scopes.md) · [Fjern-MCP](remote-mcp.md)

<a id="architecture"></a>

## Sådan virker det

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Én pc eller cloudvært: CLI/MCP styrer en fælles dæmon og noder pr. app. Private enheder bruger krypteret Tailscale; valgfri offentlig HTTPS/Funnel når HTTP-apps via gæsteadgang eller udtrykkelig åben deling." width="960">
</picture>

En baggrundsproces kører en separat Tailscale-node for hver app. Tailscale står for transporten i dit tailnet og HTTPS-certifikaterne. Privat adgang til web og filer kan begrænses efter Tailscale-identitet med `--allow` og personadgange; rå TCP afhænger af din tailnet-politik og appens eget login. [Arkitektur](architecture.md)

<a id="requirements"></a>

## Krav

| Hvem | Skal bruge |
|---|---|
| Dig | En Tailscale-konto med MagicDNS og HTTPS slået til |
| Maskinen, der kører dine apps | TSLink, som har Tailscale indbygget (på Linux en systemd-brugersession) |
| Dine enheder og dem, du deler med | Tailscale-appen |
| Gæster | En browser |

Navne på HTTPS-apps vises i offentlige certifikatlogs, så vælg navne, du ikke har noget imod, at andre ser.

<a id="documentation"></a>

## Mere

[Alle dokumenter](INDEX.md) · [CLI-reference](cli-reference.md) · [Sammenlignet med Serve, ngrok og Cloudflare](comparison.md) · [Bidrag](../CONTRIBUTING.md) · [Sikkerhed](../SECURITY.md)

Apache 2.0. TSLink er et uafhængigt projekt, som hverken er lavet eller godkendt af Tailscale.
