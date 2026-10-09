<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Private adresser til appene dine, på Tailscale-nettverket ditt.</strong></p>
<p align="center">Åpne dem fra dine egne enheter. Del én med en person eller via en lenke, fram til en dato du velger.</p>
<p align="center"><strong>Norsk</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Flere språk</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installasjon

macOS-maskiner krever macOS 13 Ventura eller nyere ([plattformstøtte](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

På Windows installerer du med Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Oppgrader med `brew upgrade --cask tslink` eller `scoop update tslink`. Hvis TSLink kjører som en bakgrunnstjeneste, kjør `tslink install` på nytt etterpå.

Linux-pakker (`.deb` og `.rpm`) og Windows-bygg ligger under [siste utgivelse](https://github.com/anydoor7/tslink/releases/latest). Første gang du deler en app, viser TSLink en Tailscale-innloggingslenke for den. [Kom i gang](getting-started.md)

<a id="why"></a>

## Når du trenger TSLink

For én app på dine egne enheter holder Serve. TSLink samler appadresser, frister og tilgangsendringer i én arbeidsflyt.

| Oppgave | Bare Tailscale | TSLink |
|---|---|---|
| Én webapp på telefonen | `tailscale serve 3000` holder | `tslink share 3000` |
| Flere apper, hver sitt navn | Oppsett av Services, eller egne noder | Én `share`/`add` per app; registrer hver node |
| Én person, én app, sju dager | Policyregler, deretter et JIT-verktøy eller manuell fjerning | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/filer) |
| Nettleserlenke, tre dager | Offentlig Funnel; legg selv til tilgangskontroll og planlagt stans | `tslink guest create photos --for 3d --public --print-link` (bare HTTP) |

Private mottakere trenger Tailscale. Gjestelenker er offentlige, kan videresendes og fungerer som tilgangsnøkler.

[Full sammenligning](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Appene dine, på dine egne enheter

- **En adresse for hver app.** Webapper, mapper, enkeltfiler og TCP-porter får hver sitt navn i tailnetet ditt, så du bruker navn i stedet for IP-adresser.
- **Privat som standard.** Ingenting er offentlig før du lager en gjestelenke eller publiserer via Funnel.
- **Én app, ikke hele maskinen.** Hver app du publiserer, får sin egen node som bare videresender til den appen. Uten Tailscale-appen på verten legger TSLink ikke til andre porter fra verten i tailnetet ditt.
- **En startside** som viser appene dine og statusen deres. [Portal](portal.md)
- **Helsesjekker og varsler** via kommando eller webhook, og en tilgangslogg som også viser avviste forespørsler. [Helse og varsler](health-and-alerts.md) · [Tilgangshistorikk](access-log.md)
- **Oppskrifter for 15 selvhostede apper**, blant annet Home Assistant, Jellyfin, Immich og Ollama. `tslink apps detect` finner dem som allerede kjører. [Appoppskrifter](apps.md)

## Del når du vil

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Gjestelenker og nye offentlige URL-er utløper og fungerer bare for webapper; mapper, filer og TCP-porter forblir private. [Personer](people.md) · [Gjestelenker](guest-links.md) · [Offentlig tilgang](funnel.md)

<a id="agents"></a>

## For KI-agenter

En utviklingsserver som en agent starter på `localhost`, når du ikke fra telefonen. Med TSLink kan agenten gi den en privat adresse, oppgi den nøyaktige URL-en og fjerne den når den er ferdig.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI eller MCP.** Styringskommandoene tar `--json` og returnerer versjonerte resultater; `tslink mcp` tilbyr operasjoner for apper og tilgang over MCP.
- **Én fil, ikke mappen.** En agent kan dele bare HTML-rapporten sin med `tslink share ./report.html`; de andre filene i mappen forblir utilgjengelige.
- **Begrensede roller.** `viewer`, `app-operator` eller `people-manager`, avgrenset til appene du angir. `tslink mcp-audit` viser hva en agent har endret. Roller begrenser TSLinks verktøy, ikke agentens eget skall.

[Agentveiledning](agent-quickstart.md) · [MCP-rettigheter](mcp-scopes.md) · [Ekstern MCP](remote-mcp.md)

<a id="architecture"></a>

## Slik fungerer det

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Én PC eller skyvert: CLI/MCP styrer en felles bakgrunnsprosess og noder per app. Private enheter bruker kryptert Tailscale; valgfri offentlig HTTPS/Funnel når HTTP-apper via gjestekontroll eller uttrykkelig åpen publisering." width="960">
</picture>

Én bakgrunnsprosess kjører en egen Tailscale-node for hver app. Tailscale står for transporten i tailnetet og HTTPS-sertifikatene. Privat tilgang til web og filer kan begrenses etter Tailscale-identitet med `--allow` og persontilganger; rå TCP er avhengig av tailnet-policyen din og appens egen innlogging. [Arkitektur](architecture.md)

<a id="requirements"></a>

## Krav

| Hvem | Trenger |
|---|---|
| Deg | En Tailscale-konto med MagicDNS og HTTPS slått på |
| Maskinen som kjører appene dine | TSLink, som har Tailscale innebygd (på Linux en systemd-brukerøkt) |
| Enhetene dine og de du deler med | Tailscale-appen |
| Gjester | En nettleser |

Navn på HTTPS-apper vises i offentlige sertifikatlogger, så velg navn du ikke har noe imot at andre ser.

<a id="documentation"></a>

## Mer

[Alle dokumenter](INDEX.md) · [CLI-referanse](cli-reference.md) · [Sammenlignet med Serve, ngrok og Cloudflare](comparison.md) · [Bidra](../CONTRIBUTING.md) · [Sikkerhet](../SECURITY.md)

Apache 2.0. TSLink er et uavhengig prosjekt, verken laget eller godkjent av Tailscale.
