<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Del de selvhostede appene dine med dem du velger, så lenge du vil.</strong></p>

TSLink gir hver app på datamaskinen eller serveren din en egen privat Tailscale-adresse. Gi utvalgte personer tilgang frem til en frist, send en gjestelenke for nettleser til noen som ikke bruker Tailscale, og trekk tilbake begge deler med én kommando. Gjør det selv eller via en KI-agent som er begrenset til rollen du tildeler. Et uavhengig prosjekt som fungerer med Tailscale.

<p align="center"><a href="#quickstart">Kom i gang</a> · <a href="#agents">For agenter</a> · <a href="comparison.md">Sammenlignet med Serve, ngrok og Cloudflare</a> · <a href="#documentation">Dokumentasjon</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <strong>Norsk</strong> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Appene dine innen rekkevidde

| Det du trenger | Det TSLink tilbyr |
|---|---|
| Bruke egne apper på tvers av enheter | Private adresser til hjemmepaneler, lokale nettsider, filer, modell-API-er og TCP-tjenester på PC eller server. |
| Dele med bestemte personer | Utvalgte HTTP-/filapper, bekreftet Tailscale-identitet, utløp og tilbakekalling. Mottakere bruker Tailscale. [Personer](people.md) |
| La noen besøke i nettleseren | Tidsbegrensede gjestelenker med valgfri PIN for HTTP-proxyapper, eller uttrykkelig offentlig HTTPS via Funnel. Lenker kan videresendes og bekrefter ikke identitet. [Gjestelenker](guest-links.md) |
| Holde orden på flere apper | Oversikt per vert, privat portal, helsesjekker og varsler, tilgangshistorikk og CLI/MCP-styring med agentroller, appavgrensning og revisjonskvitteringer. [Portal](portal.md) · [MCP-rettigheter](mcp-scopes.md) |

[Appoppskrifter](apps.md), [opplastingsgrenser](sharing.md), [fleksible varigheter](durations.md) og [QR-veiledning og tilgangsforespørsler](requests.md) forenkler hverdagen. Funksjonene følger med v0.1.0.

<a id="installation"></a>
<a id="quickstart"></a>

## Kom i gang

På macOS og Linux installerer du med Homebrew. macOS-binærfilen er signert med et Developer ID-sertifikat og notarisert av Apple. Oppgrader senere med `brew upgrade --cask tslink`, og kjør deretter `tslink install` på nytt hvis TSLink kjører som bakgrunnstjeneste.

```bash
brew install --cask anydoor7/tap/tslink
```

På Windows laster du ned `tslink_<version>_windows_<arch>.zip` fra den [nyeste utgivelsen](https://github.com/anydoor7/tslink/releases/latest), kontrollerer filen mot `checksums.txt` og kjører `tslink install`, slik at TSLink starter når du logger på. Zip-filen er ikke Authenticode-signert; [verifiser utgivelsen](verify-release.md) med de signerte sjekksummene og attesteringene. Linux-pakker i `.deb` og `.rpm` ligger på samme utgivelsesside. For å bygge fra kildekode trenger du **Git og Go 1.26.6+**. Kommandoene nedenfor bruker bash/zsh; se [Oppsett for macOS, Linux og Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Du trenger en **Tailscale-konto** og [MagicDNS og HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private mottakerenheter trenger Tailscale og tillatelse i nettverksreglene. TSLink bygger inn Tailscale på appverten.

Når appen allerede kjører på port 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Velg et ledig navn. Returnerer `share` et annet, bruk det i `url`. Fullfør først eventuell nettleserregistrering og enhetsgodkjenning, og åpne så den nøyaktige app-URL-en på en tillatt enhet. `share` starter bakgrunnstjenesten ved behov. Denne første private bruken trenger ikke administratorens API-token. Del filer med `tslink share ./report.html`; filer må finnes og apper må kjøre. [Full veiledning](getting-started.md)

Når det fungerer for deg, kan du [gi TSLink en stjerne](https://github.com/anydoor7/tslink) så flere oppdager det. Det er helt frivillig.

<a id="architecture"></a>

## Slik henger det sammen

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Én PC eller skyvert: CLI/MCP styrer en felles bakgrunnsprosess og noder per app. Private enheter bruker kryptert Tailscale; valgfri offentlig HTTPS/Funnel når HTTP-apper via gjestekontroll eller uttrykkelig åpen publisering." width="960">
</picture>

Tenk på en privat, kryptert vei til appene dine. **Tailscale leverer nettverkstransport og HTTPS; TSLink styrer apptilgang på hver vert.** Én bakgrunnsprosess kjører en innebygd node per tjeneste. Portalen viser tillatte apper; helse og tilgangshistorikk støtter vedlikeholdet.

Offentlig tilgang må velges: gjester trenger lenken og eventuell PIN; åpen Funnel er tilgjengelig for alle med URL-en. Begge bruker offentlig HTTPS, ikke privat brukeridentitet. Rå TCP forblir privat og avhenger av tailnet-regler og backend-autentisering. TSLink installerer ikke apper, isolerer ikke prosesser, lager ikke sky-VPC og samler ikke flere verter. Et uavhengig prosjekt som fungerer med Tailscale. [Arkitektur og grenser](architecture.md)

<a id="agents"></a>

## For agenter

Administrer oversikt, helse, URL-er og tilgang via CLI/MCP. Les [agentveiledningen](agent-quickstart.md) og de aktuelle verktøyskjemaene, og kontroller faktisk apptilgang før du melder suksess.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI-automatisering bruker `--json`; MCP bruker JSON-RPC over stdio. [Klienter](mcp-clients.md) · [Ekstern MCP](remote-mcp.md) · [Roller og appgrenser](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Dokumentasjon og lisens

[Alle veiledninger](INDEX.md) · [CLI-referanse](cli-reference.md) · [Lokal KI](local-ai.md) · [Helse](health-and-alerts.md) · [Tilgangshistorikk](access-log.md) · [Veikart](roadmap.md)

Oversikt på tvers av verter er planlagt. [Bidrag](../CONTRIBUTING.md) og [sikkerhetsrapporter](../SECURITY.md) er velkomne. [Apache 2.0](../LICENSE) tillater kommersiell bruk; behold [NOTICE](../NOTICE) og [tredjepartsmerknader](../THIRD_PARTY_NOTICES.md) ved videredistribusjon. [Kommersielt samarbeid](../COMMERCIAL.md) er frivillig. Tailscales vilkår og abonnementer gjelder separat.
