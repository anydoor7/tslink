<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Tilgå og administrer dine apps, uanset hvor du er.<br>Behold dem private, eller del dem på dine vilkår.</strong></p>

Dine apps på din computer eller cloudserver: tilgå dem via et krypteret privat netværk, eller vælg gæstelinks til browseren eller offentlig adgang. Betjen dem selv eller gennem en agent.

<p align="center"><a href="#quickstart">Kom i gang</a> · <a href="#agents">Til agenter</a> · <a href="#documentation">Dokumentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <strong>Dansk</strong> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Dine apps inden for rækkevidde

| Dit behov | Det tilbyder TSLink |
|---|---|
| Brug egne apps på tværs af enheder | Private adresser til hjemmets dashboards, lokale websider, filer, model-API'er og TCP-tjenester på pc eller server. |
| Del med bestemte personer | Udvalgte HTTP-/filapps, bekræftet Tailscale-login, udløb og tilbagekaldelse. Modtagere bruger Tailscale. [Personer](people.md) |
| Lad nogen besøge via browseren | Tidsbegrænsede gæstelinks med valgfri pinkode til HTTP-proxyapps eller udtrykkeligt offentlig HTTPS via Funnel. Links kan videresendes og beviser ikke identitet. [Gæstelinks](guest-links.md) |
| Hold styr på flere apps | Oversigt pr. vært, privat portal, sundhedstjek og alarmer, adgangshistorik og CLI/MCP-adgangsstyring med agentroller, appafgrænsning og revisionskvitteringer. [Portal](portal.md) · [MCP-rettigheder](mcp-scopes.md) |

[Appopskrifter](apps.md), [uploadgrænser](sharing.md), [fleksible varigheder](durations.md) og [QR-introduktion og adgangsanmodninger](requests.md) letter hverdagen. Funktionerne findes i denne kildekode.

<a id="installation"></a>
<a id="quickstart"></a>

## Kom i gang

Installer fra kildekoden med **Git og Go 1.26.6+**. Færdigbyggede udgivelser og Homebrew er endnu ikke udgivet. Kommandoerne bruger bash/zsh. [Opsætning på macOS, Linux og Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Du skal have adgang til repositoriet, en **Tailscale-konto** samt [MagicDNS og HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private modtagerenheder skal bruge Tailscale og tilladelse i netværkspolitikken. TSLink indlejrer Tailscale på appværten.

Når din app allerede kører på port 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Vælg et ledigt navn; returnerer `share` et andet, bruges det i `url`. Afslut først den viste browserregistrering og enhedsgodkendelse, og åbn derefter den præcise app-URL på en tilladt enhed. `share` starter baggrundstjenesten efter behov. Denne private førstegangsbrug kræver intet administrator-API-token. Del filer med `tslink share ./report.html`; filer skal findes, og apps skal køre. [Fuld opsætning](getting-started.md)

Når det virker for dig, må du gerne [give TSLink en stjerne](https://github.com/anydoor7/tslink), så andre kan opdage det. Det er helt frivilligt.

<a id="architecture"></a>

## Sådan hænger det sammen

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Én pc eller cloudvært: CLI/MCP styrer en fælles dæmon og noder pr. app. Private enheder bruger krypteret Tailscale; valgfri offentlig HTTPS/Funnel når HTTP-apps via gæsteadgang eller udtrykkelig åben deling." width="960">
</picture>

Tænk på en privat, krypteret vej til dine apps. **Tailscale leverer netværkstransport og HTTPS; TSLink administrerer appadgang på hver vært.** Én dæmon kører en indlejret node pr. tjeneste. Den private portal viser tilladte apps; sundhed og adgangshistorik hjælper med vedligeholdelsen.

Offentlig adgang er et aktivt tilvalg: gæster behøver link og eventuel pinkode; åben Funnel kan nås af alle med URL'en. Begge bruger offentlig HTTPS, ikke privat brugeridentitet. Rå TCP forbliver privat med tailnet-politik og backend-godkendelse. TSLink installerer ikke apps, isolerer ikke værtsprocesser, opretter ikke en cloud-VPC og samler ikke flere værter. Et uafhængigt projekt, der fungerer med Tailscale. [Arkitektur og grænser](architecture.md)

<a id="agents"></a>

## Til agenter

Administrer oversigt, sundhed, URL'er og adgang via CLI/MCP. Start med [agentguiden](agent-quickstart.md), læs de aktuelle værktøjsskemaer, og kontroller reel appadgang, før du melder succes.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI-automatisering bruger `--json`; MCP bruger JSON-RPC via stdio. [Klienter](mcp-clients.md) · [Fjern-MCP](remote-mcp.md) · [Roller og appafgrænsning](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Dokumentation og licens

[Alle vejledninger](INDEX.md) · [CLI-reference](cli-reference.md) · [Lokal AI](local-ai.md) · [Sundhed](health-and-alerts.md) · [Adgangshistorik](access-log.md) · [Planer](roadmap.md)

En oversigt på tværs af værter er planlagt. [Bidrag](../CONTRIBUTING.md) og [sikkerhedsrapporter](../SECURITY.md) er velkomne. [Apache 2.0](../LICENSE) tillader erhvervsbrug; bevar [NOTICE](../NOTICE) og [tredjepartsmeddelelser](../THIRD_PARTY_NOTICES.md) ved videredistribution. [Kommercielt samarbejde](../COMMERCIAL.md) er frivilligt. Tailscales vilkår og abonnementer gælder særskilt.
