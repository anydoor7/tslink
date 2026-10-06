<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Giv hver app på din computer eller server sin egen private adresse i dit Tailscale-netværk, og bestem, hvem der kan nå den.</strong></p>

Åbn dine webapps, mapper, model-API'er og databaser fra din egen telefon og bærbare, med sundhedstjek og adgangshistorik for hver af dem. Dine AI-agenter kan også udgive og tjekke dem inden for den rolle, du giver dem. Når en anden skal have adgang, kan du give en bestemt person adgang frem til en dato eller åbne en webapp mod internettet i en begrænset periode.

**Kræver Tailscale.** Du skal have en Tailscale-konto (gratis til personlig brug), og hver enhed, der åbner en privat app, skal have Tailscale-appen; gæster og offentlige besøgende behøver kun en browser. TSLink er et uafhængigt projekt, som hverken er lavet eller godkendt af Tailscale. [Krav](#requirements)

<p align="center"><a href="#quickstart">Kom i gang</a> · <a href="#agents">Til agenter</a> · <a href="comparison.md">Sammenlignet med Serve, ngrok og Cloudflare</a> · <a href="#documentation">Dokumentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <strong>Dansk</strong> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Det kan du

### Nå dine egne apps

- **En adresse til hver app.** `tslink share 3000`, `tslink share ./photos` eller `tslink add db --tcp localhost:5432` giver en webapp, mappe, fil eller TCP-tjeneste sin egen private adresse i dit tailnet (dit private Tailscale-netværk), for eksempel `https://photos.<tailnet>.ts.net`. Hver app er en separat Tailscale-enhed, så du åbner apps ved navn i stedet for IP-adresse og port.
- **Privat, medmindre du vælger andet.** Apps bliver inden for dit tailnet, og dets politik afgør, hvilke enheder der kan forbinde. Intet når ud på internettet, før du opretter et gæstelink eller udgiver en app.
- **Ét sted at se dem.** `tslink status --urls` viser alle apps på denne computer, og en valgfri privat startside viser adressen og tilstanden for hver app. [Portal](portal.md)
- **Få besked, når noget går i stykker.** Sundhedstjek i baggrunden kan advare dig via en kommando eller webhook, når en app går ned eller kommer op igen, eller når dens Tailscale-login snart udløber. Adgangshistorikken viser, hvem der åbnede hvilken app hvornår, også afviste forespørgsler. [Sundhed og alarmer](health-and-alerts.md) · [Adgangshistorik](access-log.md)
- **Almindelige apps klar til brug.** Opskrifter dækker 15 selvhostede apps, blandt andet Home Assistant, Jellyfin, Immich og Ollama, og `tslink apps detect` finder apps, der allerede kører. Foto- og videoapps får [uploadgrænser](sharing.md), der passer til store filer. [Appopskrifter](apps.md) · [Lokal AI](local-ai.md)

### Lad dine agenter arbejde med dem

En agent, der starter en udviklingsserver, en forhåndsvisning eller et lokalt model-API, efterlader det på `localhost`, hvor din telefon og dine andre computere ikke kan åbne det. Med TSLink kan agenten udgive det privat, give dig den præcise adresse og tage det ned igen, inden for de grænser, du sætter.

- **Del, tjek, fortryd.** `share` returnerer det navn, den registrerede, og enten den præcise URL eller et login-link, som du skal åbne. `url --wait` og `status` melder, når appen er oppe, og `remove` (`unshare` i MCP) tager den ned. [Agentguide](agent-quickstart.md)
- **Bygget til automatisering.** Kommandoer tager `--json` og returnerer et versioneret resultat med stabile fejlkoder. `tslink mcp` tilbyder de samme handlinger til en lokal MCP-klient, og `tslink serve --mcp` til agenter på dine andre enheder via tailnettet. [JSON-automatisering](json-automation.md) · [Fjern-MCP](remote-mcp.md)
- **Begrænset myndighed.** En agent, du selv kører, handler som ejer. Giv andre agenter en begrænset rolle (`viewer`, `app-operator` eller `people-manager`), der kun dækker de apps, du nævner, og som begrænser, hvor længe en adgang, agenten giver, må vare. Ændringer foretaget via MCP bliver registreret, og `tslink mcp-audit` viser dem. Roller begrænser TSLinks værktøjer, ikke agentens egen shell eller filer. [MCP-rettigheder](mcp-scopes.md)

### Del med de personer, du vælger

- **Bestemte personer, frem til en dato.** `tslink people add alice@example.com --apps photos,notes --for 7d` lader det Tailscale-login åbne de web- og filapps frem til fristen. `people update`, `extend` og `people remove` ændrer eller afslutter adgangen; efter fjernelse afvises personens næste forespørgsel, men det, der allerede er downloadet, kan ikke kaldes tilbage. [Personer](people.md) · [Varigheder](durations.md)
- **Nogen uden for dit tailnet.** Tilføj `--invite --print-links` for at få én besked, klar til at sende, med en enhedsinvitation til hver app (det kræver et API-token, som en bruger ejer). `--qr` udskriver en kode til opsætning på telefonen.
- **Anmodninger.** Personer i dit tailnet kan fra startsiden bede om mere tid eller om adgang til en app, du har markeret som mulig at anmode om. Du godkender med en varighed i én kommando. [Adgangsanmodninger](requests.md)

### Åbn en webapp mod internettet i et stykke tid

- **Gæstelinks.** `tslink guest create photos --for 3d --public --print-link` laver et browserlink til én webapp, eventuelt med pinkode, som du kan tilbagekalde for sig. Gæster behøver ingen Tailscale-konto. Alle, der har linket, kan bruge det, så det beviser ikke, hvem der besøgte appen. [Gæstelinks](guest-links.md)
- **En åben offentlig URL.** `tslink add preview --proxy localhost:3000 --funnel --public` udgiver en webapp til alle, der har dens URL. Den udløber efter 24 timer, medmindre du sætter en anden levetid med `--funnel-ttl`. [Funnel](funnel.md)
- Begge kører gennem Tailscale Funnel, udløber altid (1 time til 7 dage, medmindre du hæver grænsen) og virker kun for webapps. Mapper, filer og TCP-tjenester forbliver private.

Alt ovenfor følger med v0.1.0.

<a id="requirements"></a>

## Krav

TSLink er bygget på Tailscale. Det er et uafhængigt projekt, som hverken er lavet eller godkendt af Tailscale, og Tailscales egne vilkår og [abonnementer](https://tailscale.com/pricing) gælder.

| Hvem | Hvad de skal bruge |
|---|---|
| Dig | En Tailscale-konto med [MagicDNS og HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) slået til. Det gratis Personal-abonnement er til ikke-kommerciel brug. |
| Computeren eller serveren, der kører dine apps | Kun TSLink. Det indeholder Tailscale, så der er ingen separat installation. Hver ny app beder om et login i browseren og om godkendelse af enheden, hvis dit tailnet kræver det. |
| Dine andre enheder | Tailscale-appen, logget ind på dit tailnet. |
| Personer, du vælger | Tailscale-appen og deres eget login. De tilslutter sig enten dit tailnet, hvilket føjer en bruger til dit abonnement, eller accepterer en enhedsinvitation til hver app. Din tailnet-politik skal give dem adgang. |
| Gæster og offentlige besøgende | En browser. Dit tailnet skal tillade Funnel, som Tailscale stadig kalder beta. |

Når du slår HTTPS til, offentliggøres dit tailnet-navn og dine enhedsnavne, herunder hver apps navn, i en offentlig certifikatlog, så vælg appnavne, som du ikke har noget imod, at andre ser.

<a id="installation"></a>
<a id="quickstart"></a>

## Kom i gang

På macOS og Linux installerer du med Homebrew. macOS-binæren er signeret med et Developer ID-certifikat og notariseret af Apple. Opgrader senere med `brew upgrade --cask tslink`, og kør derefter `tslink install` igen, hvis TSLink kører som baggrundstjeneste.

```bash
brew install --cask anydoor7/tap/tslink
```

På Windows downloader du `tslink_<version>_windows_<arch>.zip` fra den [seneste udgivelse](https://github.com/anydoor7/tslink/releases/latest), kontrollerer den mod `checksums.txt` og kører `tslink install`, så TSLink starter, når du logger ind. Zip-filen er ikke Authenticode-signeret; [verificér udgivelsen](verify-release.md) med de signerede checksummer og attesteringer. Linux-pakker i `.deb` og `.rpm` ligger på samme udgivelsesside. Vil du bygge fra kildekoden, skal du bruge **Git og Go 1.26.6+**. Kommandoerne nedenfor bruger bash/zsh; se [Opsætning på macOS, Linux og Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Du skal have en **Tailscale-konto** samt [MagicDNS og HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private modtagerenheder skal bruge Tailscale og tilladelse i netværkspolitikken. TSLink indlejrer Tailscale på appværten.

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
