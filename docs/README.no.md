<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Gi hver app på datamaskinen eller serveren din en egen privat adresse i Tailscale-nettverket ditt, og bestem hvem som kan nå den.</strong></p>

Åpne webappene, mappene, modell-API-ene og databasene dine fra din egen telefon og bærbare PC, med helsesjekk og tilgangshistorikk for hver av dem. KI-agentene dine kan også publisere og sjekke dem, innenfor rollen du gir dem. Når noen andre trenger tilgang, kan du gi en bestemt person tilgang frem til en dato, eller åpne en webapp mot det offentlige internettet for en begrenset tid.

**Krever Tailscale.** Du trenger en Tailscale-konto (gratis for personlig bruk), og hver enhet som åpner en privat app, trenger Tailscale-appen; gjester og offentlige besøkende trenger bare en nettleser. TSLink er et uavhengig prosjekt, verken laget eller godkjent av Tailscale. [Krav](#requirements)

<p align="center"><a href="#quickstart">Kom i gang</a> · <a href="#agents">For agenter</a> · <a href="comparison.md">Sammenlignet med Serve, ngrok og Cloudflare</a> · <a href="#documentation">Dokumentasjon</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <strong>Norsk</strong> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Dette kan du gjøre

### Nå dine egne apper

- **En adresse for hver app.** `tslink share 3000`, `tslink share ./photos` eller `tslink add db --tcp localhost:5432` gir en webapp, mappe, fil eller TCP-tjeneste en egen privat adresse i tailnettet ditt (ditt private Tailscale-nettverk), for eksempel `https://photos.<tailnet>.ts.net`. Hver app er en egen Tailscale-enhet, så du åpner apper med navn i stedet for IP-adresse og port.
- **Privat med mindre du velger noe annet.** Appene blir værende i tailnettet ditt, og reglene der bestemmer hvilke enheter som kan koble til. Ingenting når det offentlige internettet før du lager en gjestelenke eller publiserer en app.
- **Ett sted å se dem.** `tslink status --urls` lister alle apper på denne datamaskinen, og en valgfri privat startside viser adressen og helsen til hver app. [Portal](portal.md)
- **Få vite når noe går galt.** Helsesjekker i bakgrunnen kan varsle deg via en kommando eller webhook når en app går ned eller kommer tilbake, eller når Tailscale-innloggingen dens snart utløper. Tilgangshistorikken viser hvem som åpnet hvilken app og når, også avviste forespørsler. [Helse og varsler](health-and-alerts.md) · [Tilgangshistorikk](access-log.md)
- **Vanlige apper klare til bruk.** Oppskrifter dekker 15 selvhostede apper, blant dem Home Assistant, Jellyfin, Immich og Ollama, og `tslink apps detect` finner apper som allerede kjører. Foto- og videoapper får [opplastingsgrenser](sharing.md) som passer for store filer. [Appoppskrifter](apps.md) · [Lokal KI](local-ai.md)

### La agentene dine jobbe med dem

En agent som starter en utviklingsserver, en forhåndsvisning eller et lokalt modell-API, lar det ligge på `localhost`, der telefonen og de andre datamaskinene dine ikke kan åpne det. Med TSLink kan agenten publisere det privat, gi deg den nøyaktige adressen og ta det ned igjen, innenfor grensene du setter.

- **Del, sjekk, angre.** `share` returnerer navnet den registrerte, og enten den nøyaktige URL-en eller en innloggingslenke du skal åpne. `url --wait` og `status` melder når appen er oppe, og `remove` (`unshare` i MCP) tar den ned. [Agentveiledning](agent-quickstart.md)
- **Laget for automatisering.** Kommandoer tar `--json` og returnerer et versjonert resultat med stabile feilkoder. `tslink mcp` tilbyr de samme operasjonene til en lokal MCP-klient, og `tslink serve --mcp` til agenter på de andre enhetene dine via tailnettet. [JSON-automatisering](json-automation.md) · [Ekstern MCP](remote-mcp.md)
- **Begrenset myndighet.** En agent du kjører selv, opptrer som eier. Gi andre agenter en redusert rolle (`viewer`, `app-operator` eller `people-manager`) som bare dekker appene du navngir, og som begrenser hvor lenge en tilgang agenten gir, kan vare. Endringer gjort via MCP blir registrert, og `tslink mcp-audit` viser dem. Roller begrenser TSLinks verktøy, ikke agentens eget skall eller egne filer. [MCP-rettigheter](mcp-scopes.md)

### Del med folk du velger

- **Bestemte personer, frem til en dato.** `tslink people add alice@example.com --apps photos,notes --for 7d` lar den Tailscale-innloggingen åpne de nett- og filappene frem til fristen. `people update`, `extend` og `people remove` endrer eller avslutter tilgangen; etter fjerning avvises personens neste forespørsel, men det som allerede er lastet ned, kan ikke hentes tilbake. [Personer](people.md) · [Varigheter](durations.md)
- **Noen utenfor tailnettet ditt.** Legg til `--invite --print-links` for å få én melding, klar til å sende, med en enhetsinvitasjon for hver app (dette krever et API-token som en bruker eier). `--qr` skriver ut en kode for oppsett på telefonen.
- **Forespørsler.** Personer i tailnettet ditt kan be om mer tid, eller om tilgang til en app du har merket som mulig å be om, fra startsiden. Du godkjenner med en varighet i én kommando. [Tilgangsforespørsler](requests.md)

### Åpne en webapp mot internett for en stund

- **Gjestelenker.** `tslink guest create photos --for 3d --public --print-link` lager en nettleserlenke til én webapp, eventuelt med PIN, som du kan trekke tilbake for seg. Gjester trenger ingen Tailscale-konto. Alle som har lenken, kan bruke den, så den beviser ikke hvem som var innom. [Gjestelenker](guest-links.md)
- **En åpen offentlig URL.** `tslink add preview --proxy localhost:3000 --funnel --public` publiserer en webapp for alle som har URL-en. Den utløper etter 24 timer med mindre du setter en annen levetid med `--funnel-ttl`. [Funnel](funnel.md)
- Begge går gjennom Tailscale Funnel, utløper alltid (1 time til 7 dager med mindre du hever grensen) og fungerer bare for webapper. Mapper, filer og TCP-tjenester forblir private.

Alt over følger med v0.1.0.

<a id="requirements"></a>

## Krav

TSLink er bygget på Tailscale. Det er et uavhengig prosjekt, verken laget eller godkjent av Tailscale, og Tailscales egne vilkår og [abonnementer](https://tailscale.com/pricing) gjelder.

| Hvem | Hva de trenger |
|---|---|
| Deg | En Tailscale-konto med [MagicDNS og HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) slått på. Det gratis Personal-abonnementet er for ikke-kommersiell bruk. |
| Datamaskinen eller serveren som kjører appene dine | Bare TSLink. Det inneholder Tailscale, så det trengs ingen egen installasjon. Hver nye app ber om innlogging i nettleseren, og om godkjenning av enheten hvis tailnettet ditt krever det. |
| De andre enhetene dine | Tailscale-appen, logget inn i tailnettet ditt. |
| Personer du velger | Tailscale-appen og sin egen innlogging. De blir enten med i tailnettet ditt, noe som legger til en bruker i abonnementet ditt, eller godtar en enhetsinvitasjon for hver app. Tailnet-reglene dine må gi dem tilgang. |
| Gjester og offentlige besøkende | En nettleser. Tailnettet ditt må tillate Funnel, som Tailscale fortsatt kaller beta. |

Når du slår på HTTPS, publiseres tailnet-navnet ditt og enhetsnavnene dine, inkludert navnet på hver app, i en offentlig sertifikatlogg, så velg appnavn du ikke har noe imot at andre ser.

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
