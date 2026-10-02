<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Giv dine lokale apps, modeller og filer hver sin private adresse.</strong><br>
  Åbn dem fra en anden godkendt enhed på dit Tailscale-netværk.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licens: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 eller nyere"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: indlejrede tsnet-noder"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 værktøjer"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <strong>Dansk</strong> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installation

Du skal bruge **Go 1.26.6 eller nyere** og Git. Der er endnu ikke udgivet færdigbyggede versioner eller en Homebrew-cask; installer fra kildekoden. Eksemplerne bruger **bash eller zsh**; se [platformsunderstøttelse](platforms.md) for krav til Windows og baggrundstjenester.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Brug en Tailscale-konto med [MagicDNS og HTTPS aktiveret](https://tailscale.com/docs/how-to/set-up-https-certificates). Modtagerenheden skal være logget ind på dit Tailscale-netværk (**tailnet**), og netværkets regler skal tillade adgang til tjenesten. TSLink indlejrer Tailscale på den computer, der udstiller tjenesten.

### Del din første side

Opret en side; TSLink serverer den direkte og starter sin baggrundstjeneste efter behov:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Hvis TSLink viser en tilmeldings-URL, skal du åbne den for at godkende noden. Dit tailnet kan også kræve, at en administrator godkender enheden. Hent derefter den præcise adresse:

```bash
tslink url demo --wait
```

Åbn URL’en på en godkendt enhed. Du behøver ikke et API-token til denne første deling.
[Fuld opsætning og detaljer om livscyklus →](getting-started.md)

<a id="use-cases"></a>

## Hvad vil du dele?

Filerne skal eksistere; apps, databaser og model-backends skal allerede køre på de angivne porte.

| Anvendelse | Kommando |
|---|---|
| Åbn en lokal app fra en anden enhed | `tslink share 3000` |
| Gennemse en mappe med filer | `tslink share ./public --name files` |
| Læs en genereret HTML-rapport på din telefon | `tslink share ./report.html --name report` |
| Forbind til en lokal database via TCP | `tslink add database --tcp localhost:5432` |
| Brug en lokal HTTP-model-API, f.eks. Ollama | `tslink add model --proxy localhost:11434` |

For Ollama skal du hente den præcise URL med `tslink url model --wait`; `baseURL` i en OpenAI-kompatibel klient bruger denne URL med `/v1` tilføjet. [Lokale modeller og arbejdsgange med private data →](local-ai.md)

Til flere apps på én vært samler TSLink navngivne tjenesteknuder, HTTP-tilladelseslister efter identitet, Funnel-udløb og MCP-styring. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) kan være nok til én app på dine egne enheder.

<a id="architecture"></a>

## Arkitektur

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Eksempel på et tjenestekort: App, Docs, Database og Model er særskilte navngivne noder i samme tailnet. Apps, filer og model-API’er bruger HTTPS; databasen bruger privat TCP." width="960">
</picture>

**Ét tailnet, særskilte tjenestenoder.** En fælles daemon kører én indlejret tsnet-node pr. tjeneste, som videresender HTTP, serverer filer eller fungerer som TCP-proxy. Ændringer i registret træder i kraft, mens den kører. Hver node har sin egen netværksidentitet; tjenesterne deler computeren, der udstiller dem.
[Arkitektur i detaljer →](architecture.md)

| Byggesten | Rolle |
|---|---|
| [Go](../go.mod) | Nativt kommandolinjeprogram |
| [Tailscale tsnet](architecture.md) | Tjenestenoder og tailnet-transport |
| [Cobra](https://github.com/spf13/cobra) | Kommandoer og hjælp |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transporter til agenter |
| Operativsystemets nøglering og tjenestehåndtering for brugeren | Valgfrie legitimationsoplysninger og baggrundsdrift |

Tjenesterne bliver i dit tailnet, medmindre du udtrykkeligt aktiverer [offentlig Funnel](getting-started.md#more-examples). HTTP- og filtjenester understøtter lister over godkendte identiteter (`WhoIs`, `--allow`); TCP bruger tailnet-reglerne og backendens egen godkendelse. Se [grænser for deling](sharing.md).

TSLink installerer ikke apps, kører ikke modeller, isolerer ikke værtsprocesser og samler ikke flere værter. Netværk, kryptering og HTTPS kommer fra Tailscale; TSLink er et selvstændigt projekt.

<a id="agents"></a>

## Til agenter

De **19 MCP-værktøjer** lader en agent dele rapporter, administrere tjenester, hente URL’er og undersøge opsætningen. Forbind en lokal MCP-klient til det installerede program:

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

MCP administrerer TSLink; applikationer bruger modellens HTTP-API til inferens. Se [MCP-klienter](mcp-clients.md), [fjern-MCP](remote-mcp.md) og [driftsvejledningen for agenter](../AGENTS.md) for konfiguration og automatisering.

CLI-automatisering understøtter `--json` med `schema_version` sat til `1`; se `tslink status --urls --json`. Lokal MCP bruger JSON-RPC over stdio. Se [JSON-automatisering](json-automation.md).

<a id="roadmap"></a>

## På vej

Punkter med Under sammenfletning, Under gennemgang eller Planlagt indgår ikke i kildeinstallationen ovenfor.

| Anvendelse | Status |
|---|---|
| F1. Giv et familiemedlem 3 dages adgang til private HTTP-/filapps, og saml invitationerne i én besked; modtageren skal stadig bruge Tailscale. | Under sammenfletning |
| F2. Kontrollér appens tilstand, og modtag nedbruds- eller udløbsadvarsler via en valgfri kommando eller webhook. | Under sammenfletning |
| F3. Find understøttede loopback-apps, og se opskrifter til selvhostede apps før deling. | Under sammenfletning |
| F8. Indstil uploadstørrelse og forespørgselsfrister pr. HTTP-app til store uploads og langsomme klienter. | Under sammenfletning |
| F9. Genstart en nedbrudt Windows-daemon, mens brugeren er logget ind, med en planlagt opgave og indbygget supervisor. | Under sammenfletning |
| F4. Se hvem der åbnede hvilken app i lokale adgangslogfiler med stitilstandene `prefix`, `full` eller `off`. | Under gennemgang |
| F5. Åbn én startside med tilladte apps og tilmeldingshenvisning for ejere; besøgende skal stadig bruge Tailscale. | Under gennemgang |
| F6. Giv en agent en rolle og et appområde med revisionskvitteringer for ændringer. | Under gennemgang |
| F10. Lad en gæst åbne én HTTP-app i browseren uden at installere Tailscale, med et tidsbegrænset link og valgfri PIN via adgangskontrolleret offentlig Funnel. | Under gennemgang |
| F11. Vælg forvalg eller egne varigheder på mindst 1 time med en konfigurerbar gæstegrænse på som standard 7 dage. | Under gennemgang |
| F12. Hjælp telefonbrugere med at tilslutte sig via QR-kode, og godkend appadgang eller ekstra tid i én handling. | Under gennemgang |
| F7. Se apps fra flere værter i én oversigt. | Planlagt |

<a id="documentation"></a>

## Dokumentation og licens

[Kom i gang](getting-started.md) · [Lokale modeller](local-ai.md) ·
[CLI-reference](cli-reference.md) · [Platforme](platforms.md) · [Roadmap](roadmap.md)

Bidrag efter vejledningen i [CONTRIBUTING.md](../CONTRIBUTING.md); rapportér sårbarheder som beskrevet i [SECURITY.md](../SECURITY.md).

TSLink bruger den uændrede [Apache License 2.0](../LICENSE), også til kommerciel brug. Bevar relevante [NOTICE](../NOTICE)-oplysninger og [tredjepartsmeddelelser](../THIRD_PARTY_NOTICES.md) ved videre distribution. [Kommercielt samarbejde](../COMMERCIAL.md) er frivilligt og tilføjer ingen licensbetingelser. Tailscales tjenestevilkår og abonnementer gælder separat.
