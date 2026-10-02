<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Gi lokale apper, modeller og filer hver sin private adresse.</strong><br>
  Åpne dem fra en annen tillatt enhet på Tailscale-nettverket ditt.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Lisens: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 eller nyere"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: innebygde tsnet-noder"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 verktøy"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <strong>Norsk</strong><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installasjon

Du trenger **Go 1.26.6 eller nyere** og Git. Ferdigbygde utgivelser og en Homebrew-cask er ikke publisert ennå; installer fra kildekoden. Eksemplene bruker **bash eller zsh**; se [plattformstøtte](platforms.md) for krav til Windows og bakgrunnstjenester.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Bruk en Tailscale-konto med [MagicDNS og HTTPS aktivert](https://tailscale.com/docs/how-to/set-up-https-certificates). Mottakerenheten må være logget inn på Tailscale-nettverket ditt (**tailnet**), og nettverkets regler må tillate tilgang til tjenesten. TSLink bygger inn Tailscale på maskinen som publiserer.

### Del din første side

Opprett en side; TSLink leverer den direkte og starter bakgrunnstjenesten ved behov:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Hvis TSLink viser en registrerings-URL, åpner du den for å autorisere noden. Tailnettet kan også kreve at en administrator godkjenner enheten. Hent deretter den nøyaktige adressen:

```bash
tslink url demo --wait
```

Åpne URL-en på en tillatt enhet. Du trenger ikke et API-token for denne første delingen.
[Fullstendig oppsett og livssyklusdetaljer →](getting-started.md)

<a id="use-cases"></a>

## Hva vil du dele?

Filene må finnes; apper, databaser og modell-backender må allerede kjøre på de angitte portene.

| Bruksområde | Kommando |
|---|---|
| Åpne en lokal app fra en annen enhet | `tslink share 3000` |
| Bla gjennom en filmappe | `tslink share ./public --name files` |
| Les en generert HTML-rapport på telefonen | `tslink share ./report.html --name report` |
| Koble til en lokal database via TCP | `tslink add database --tcp localhost:5432` |
| Bruk en lokal HTTP-modell-API, for eksempel Ollama | `tslink add model --proxy localhost:11434` |

For Ollama henter du den nøyaktige URL-en med `tslink url model --wait`; `baseURL` i en OpenAI-kompatibel klient bruker denne URL-en med `/v1` lagt til. [Lokale modeller og arbeidsflyter med private data →](local-ai.md)

For flere apper på én vert samler TSLink navngitte tjenestenoder, HTTP-lister over tillatte identiteter, Funnel-utløp og MCP-administrasjon. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) kan være nok for én app på dine egne enheter.

<a id="architecture"></a>

## Arkitektur

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Eksempel på tjenestekart: App, Docs, Database og Model er separate navngitte noder i samme tailnet. Apper, filer og modell-API-er bruker HTTPS; databasen bruker privat TCP." width="960">
</picture>

**Ett tailnet, separate tjenestenoder.** En felles daemon kjører én innebygd tsnet-node per tjeneste for å videresende HTTP, levere filer eller fungere som TCP-proxy. Registerendringer trer i kraft mens den kjører. Hver node har sin egen nettverksidentitet; tjenestene deler maskinen som publiserer dem.
[Arkitekturdetaljer →](architecture.md)

| Byggestein | Rolle |
|---|---|
| [Go](../go.mod) | Nativt kommandolinjeprogram |
| [Tailscale tsnet](architecture.md) | Tjenestenoder og tailnet-transport |
| [Cobra](https://github.com/spf13/cobra) | Kommandoer og hjelp |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transporter for agenter |
| Operativsystemets nøkkelring og tjenestebehandler for brukeren | Valgfrie påloggingsopplysninger og bakgrunnsdrift |

Tjenestene forblir i tailnettet med mindre du uttrykkelig aktiverer [offentlig Funnel](getting-started.md#more-examples). HTTP- og filtjenester støtter lister over tillatte identiteter (`WhoIs`, `--allow`); TCP bruker tailnet-reglene og backendens egen autentisering. Se [grenser for deling](sharing.md).

TSLink installerer ikke apper, kjører ikke modeller, isolerer ikke vertsprosesser og samler ikke flere verter. Nettverk, kryptering og HTTPS kommer fra Tailscale; TSLink er et selvstendig prosjekt.

<a id="agents"></a>

## For agenter

De **19 MCP-verktøyene** lar en agent dele rapporter, administrere tjenester, hente URL-er og undersøke oppsettet. Koble en lokal MCP-klient til det installerte programmet:

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

MCP administrerer TSLink; applikasjoner bruker modellens HTTP-API til inferens. Se [MCP-klienter](mcp-clients.md), [ekstern MCP](remote-mcp.md) og [driftsveiledningen for agenter](../AGENTS.md) for konfigurasjon og automatisering.

CLI-automatisering støtter `--json` med `schema_version` satt til `1`; se `tslink status --urls --json`. Lokal MCP bruker JSON-RPC over stdio. Se [JSON-automatisering](json-automation.md).

<a id="roadmap"></a>

## På vei

Punkter merket Under sammenslåing, Til gjennomgang eller Planlagt er ikke med i kildeinstallasjonen ovenfor.

| Bruksområde | Status |
|---|---|
| <!-- roadmap:people --> Gi en slektning 3 dagers tilgang til private HTTP-/filapper og samle appinvitasjonene i én melding; mottakeren trenger fortsatt Tailscale. | Under sammenslåing |
| <!-- roadmap:health --> Sjekk appenes helse og motta varsler om nedetid eller utløp via en valgfri kommando eller webhook. | Under sammenslåing |
| <!-- roadmap:recipes --> Finn støttede loopback-apper og forhåndsvis oppskrifter for selvhostede apper før deling. | Under sammenslåing |
| <!-- roadmap:limits --> Angi opplastingsstørrelse og tidsgrenser per HTTP-app for store opplastinger og trege klienter. | Under sammenslåing |
| <!-- roadmap:windows --> Start en krasjet Windows-daemon på nytt mens brukeren er innlogget, med en planlagt oppgave og innebygd supervisor. | Under sammenslåing |
| <!-- roadmap:access-log --> Se hvem som åpnet hvilken app i lokale tilgangslogger med stimodusene `prefix`, `full` eller `off`. | Til gjennomgang |
| <!-- roadmap:portal --> Åpne én startside med tillatte apper og registreringsoverføring for eiere; besøkende trenger fortsatt Tailscale. | Til gjennomgang |
| <!-- roadmap:mcp-scopes --> Gi en agent en rolle og et appomfang med revisjonskvitteringer for endringer. | Til gjennomgang |
| <!-- roadmap:guest-links --> La en gjest åpne én HTTP-app i nettleseren uten å installere Tailscale, med en tidsbegrenset lenke og valgfri PIN gjennom offentlig Funnel med tilgangskontroll. | Til gjennomgang |
| <!-- roadmap:durations --> Velg forhåndsinnstillinger eller egne varigheter på minst 1 time, med en konfigurerbar gjestegrense på som standard 7 dager. | Til gjennomgang |
| <!-- roadmap:requests --> Hjelp telefonbrukere å bli med via QR-kode; la eieren godkjenne forespørsler om apptilgang eller ekstra tid i én handling. | Til gjennomgang |
| <!-- roadmap:multi-host --> Se apper fra flere verter i én oversikt. | Planlagt |

<a id="documentation"></a>

## Dokumentasjon og lisens

[Kom i gang](getting-started.md) · [Lokale modeller](local-ai.md) ·
[CLI-referanse](cli-reference.md) · [Plattformer](platforms.md) · [Veikart](roadmap.md)

Følg [CONTRIBUTING.md](../CONTRIBUTING.md) for å bidra; rapporter sårbarheter som beskrevet i [SECURITY.md](../SECURITY.md).

TSLink bruker den uendrede [Apache License 2.0](../LICENSE), også for kommersiell bruk. Behold relevante [NOTICE](../NOTICE)-opplysninger og [tredjepartsmerknader](../THIRD_PARTY_NOTICES.md) ved videredistribusjon. [Kommersielt samarbeid](../COMMERCIAL.md) er frivillig og legger ikke til lisensvilkår. Tailscales tjenestevilkår og abonnementer gjelder separat.
