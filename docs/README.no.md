<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink er en åpen kildekode-CLI og MCP-server som gir hver app på datamaskinen din en privat adresse <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, slik at du eller coding agenten din kan åpne en kjørende utviklingsserver på telefonen. Uavhengig prosjekt, ikke laget av Tailscale.</p>
<p align="center">Telefonen din trenger Tailscale-appen, logget inn på samme tailnet.</p>
<p align="center"><strong>Norsk</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Flere språk</a></p>

```text
# Claude Code, etter at du har installert CLI-en (se Installasjon nedenfor)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Lokale MCP-klienter som bruker mcpServers-JSON (andre: se MCP-oppsettet til klienten din)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Si til agenten din: «Del utviklingsserveren slik at jeg kan åpne den på telefonen.»
```

Det samme manuelt, med utviklingsserveren allerede lyttende på port 5173. Med `--json` melder `share` det registrerte navnet i `data.name`. Bruk det navnet i de neste kommandoene: Det avviker fra `--name` hvis det navnet var opptatt, for eksempel `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, eller data.auth_url (en Tailscale-påloggingslenke) når status er needs_login
tslink url <name> --wait              # når du er ferdig med pålogging: den nøyaktige URL-en
tslink remove <name>                  # når du er ferdig; hopp over hvis share feilet. Utviklingsserveren fortsetter å kjøre
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

Oppgrader med `brew upgrade --cask tslink` eller `scoop update; scoop update tslink`. Hvis TSLink kjører som en bakgrunnstjeneste, kjør `tslink install` på nytt etterpå.

Linux-pakker (`.deb` og `.rpm`) og Windows-bygg ligger under [siste utgivelse](https://github.com/anydoor7/tslink/releases/latest). Første gang du deler en app, viser TSLink en Tailscale-innloggingslenke for den. [Kom i gang](getting-started.md)

<a id="agents"></a>

## For KI-agenter

En utviklingsserver som en agent starter på `localhost`, når du ikke fra telefonen. Med TSLink kan agenten gi den en privat adresse, gi deg den nøyaktige URL-en og fjerne delingen når den er ferdig. Telefonen din trenger Tailscale-appen, logget inn på samme tailnet.

Testet 2026-10-22 med Vite 8.3.5 og en iPhone 15 Pro: Agenten delte utviklingsserveren over MCP, telefonen åpnet den private URL-en, og siden ble oppdatert ved lagring uten manuell omlasting.

For Claude Code installerer du først CLI-en og kjører deretter:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Pluginen inneholder en [forhåndsvisnings-skill](../skills/tslink-preview/SKILL.md) og MCP-tilkoblingen nedenfor. Markedsplassen ligger i dette repoet og henter pluginen fra [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Den gjeldende skillen blir her; pluginen har en kopi som CI kontrollerer. Andre agenter kan bruke samme skill eller koble til MCP direkte. Integrasjonen krever den lokale CLI-en og kjører ikke i claude.ai-chatten. [Oppsett og registrering](agent-integrations.md)

For en lokal MCP-klient som bruker `mcpServers`-JSON (andre klienter: se deres MCP-oppsett og [veiledningen for MCP-klienter](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Gateway som standard ved nye installasjoner.** Private HTTP-/filapper får en stabil port, f.eks. `https://home.<tailnet>.ts.net:8443/`. Eksisterende installasjoner beholder modusen, og apper beholder egne noder/URL-er; `--portal-only` velges uttrykkelig.
- **Registrer gatewayen.** Uten legitimasjon kan gatewayens verifiserte egen konto uten tagger bli owner. Ved tagger/OAuth kjøres `tslink portal enable --owner <login>` lokalt. Ventende registrering av appnoder blokkerer ikke en klar gatewayrute.
- **Tilgang per person.** Nye apper tillater owner/admin og uttrykkelig godkjente personer. `people add`, `add --allow` og MCP `add/share.allow` sjekker medlemskap og klargjør nødvendige invitasjoner. Medlemmer trenger ingen enhetsinvitasjon; eksterne personer godtar én gatewayinvitasjon. Katalog-/API-feil gir ufullstendig oppsett.
- **Rediger, lagre, se.** Når du lagrer koden, oppdateres siden på telefonen. Appens egne Host- og Origin-regler kan fortsatt blokkere forespørsler: I testen vår trengte Vite ingen endring av `allowedHosts`; Next.js er ikke testet og krever fortsatt `allowedDevOrigins`.
- **Opprydding.** Agenten kaller `unshare`, eller du kjører `tslink remove <name>`, alltid med navnet share-resultatet returnerte. Fjerningen avregistrerer delingen og forsøker å slette tailnet-noden når TSLink kan bevise at den eier den, så sjekk resultatet for felt om opprydding eller beholdt tilstand. Den stopper ikke utviklingsserveren.
- **CLI eller MCP.** Styringskommandoene tar `--json` og returnerer versjonerte resultater; `tslink mcp` tilbyr operasjoner for apper og tilgang over MCP.
- **Én fil, ikke mappen.** En agent kan dele bare den selvstendige HTML-rapporten sin med `tslink share ./report.html`; de andre filene i mappen forblir utilgjengelige.
- **Begrensede roller.** `viewer`, `app-operator` eller `people-manager`, avgrenset til appene du angir; å dele en ny app krever rollen `owner`. `tslink mcp-audit` viser hva en agent har endret. Roller begrenser TSLinks verktøy, ikke agentens eget skall.

[Agentveiledning](agent-quickstart.md) · [MCP-rettigheter](mcp-scopes.md) · [Ekstern MCP](remote-mcp.md)

<a id="why"></a>

## Når du trenger TSLink

Serve er nok for én app du starter selv og åpner på din egen telefon. TSLink er for utviklingsserverne en agent starter, ofte flere, når hver skal ha sitt eget private navn og deling, URL og opprydding skal være ferdige steg i CLI-en, MCP og en Claude Code-skill.

| Oppgave | Bare Tailscale | TSLink |
|---|---|---|
| Flere utviklingsservere agenten din starter, ett navn hver | Serve setter opp ruter på enhetsnivå under ett navn per maskin; Services krever for hvert navn en definisjon, en godkjenning og en tagget vert | Én node per app, med deling, oppslag av URL og opprydding ferdig i CLI-en (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) og en skill |
| Én app du starter selv, på telefonen | `tailscale serve 3000` holder | `tslink share 3000` |
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
