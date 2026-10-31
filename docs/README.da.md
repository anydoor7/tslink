<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink er en open source-CLI og MCP-server, der giver hver app på din computer en privat adresse <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, så du eller din coding agent kan åbne en kørende udviklingsserver på din telefon. Uafhængigt projekt, ikke lavet af Tailscale.</p>
<p align="center">Din telefon skal have Tailscale-appen, logget ind på samme tailnet.</p>
<p align="center"><strong>Dansk</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Flere sprog</a></p>

```text
# Claude Code, efter du har installeret CLI'en (se Installation nedenfor)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Lokale MCP-klienter, der bruger mcpServers-JSON (andre: se din klients MCP-opsætning)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Sig til din agent: "Del udviklingsserveren, så jeg kan åbne den på min telefon."
```

Det samme manuelt, med udviklingsserveren allerede lyttende på port 5173. Med `--json` melder `share` det registrerede navn i `data.name`. Brug det navn i de følgende kommandoer: Det afviger fra `--name`, hvis det navn var optaget, for eksempel `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, eller data.auth_url (et Tailscale-loginlink), når status er needs_login
tslink url <name> --wait              # når du er færdig med login: den præcise URL
tslink remove <name>                  # når du er færdig; spring over, hvis share fejlede. Udviklingsserveren kører videre
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installation

macOS-værter kræver macOS 13 Ventura eller nyere ([platformunderstøttelse](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

På Windows installeres med Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Opdater med `brew upgrade --cask tslink` eller `scoop update; scoop update tslink`. Hvis TSLink kører som en baggrundstjeneste, skal du køre `tslink install` igen bagefter.

Linux-pakker (`.deb` og `.rpm`) og Windows-builds ligger under [seneste udgivelse](https://github.com/anydoor7/tslink/releases/latest). Første gang du deler en app, viser TSLink et Tailscale-loginlink til den. [Kom godt i gang](getting-started.md)

<a id="agents"></a>

## Til AI-agenter

En udviklingsserver, som en agent starter på `localhost`, kan du ikke nå fra din telefon. Med TSLink kan agenten give den en privat adresse, give dig den præcise URL og fjerne delingen igen bagefter. Din telefon skal have Tailscale-appen, logget ind på samme tailnet.

Testet den 2026-10-22 med Vite 8.3.5 og en iPhone 15 Pro: Agenten delte udviklingsserveren over MCP, telefonen åbnede den private URL, og siden blev opdateret ved gem uden manuel genindlæsning.

Til Claude Code installerer du først CLI'en og kører derefter:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Pluginnet indeholder en [forhåndsvisnings-skill](../skills/tslink-preview/SKILL.md) og MCP-forbindelsen nedenfor. Markedspladsen ligger i dette repository og henter pluginnet fra [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Den gældende skill bliver her; pluginnet har en kopi, som CI kontrollerer. Andre agenter kan bruge den samme skill eller forbinde MCP direkte. Integrationen kræver den lokale CLI og kører ikke i claude.ai-chatten. [Opsætning og tilmelding](agent-integrations.md)

Til en lokal MCP-klient, der bruger `mcpServers`-JSON (andre klienter: se deres MCP-opsætning og [guiden til MCP-klienter](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Gateway som standard ved nye installationer.** Private HTTP-/filapps får en stabil port, f.eks. `https://home.<tailnet>.ts.net:8443/`. Eksisterende installationer beholder deres tilstand, og apps beholder egne noder/URL’er; `--portal-only` vælges eksplicit.
- **Tilmeld gatewayen.** Uden legitimationsoplysninger kan den verificerede egen konto på en gateway uden tags blive owner. Ved tags/OAuth køres `tslink portal enable --owner <login>` lokalt. Ventende tilmelding af appnoder blokerer ikke en klar gatewayrute.
- **Adgang pr. person.** Nye apps tillader owner/admin og udtrykkeligt godkendte personer. `people add`, `add --allow` og MCP `add/share.allow` kontrollerer medlemskab og klargør nødvendige invitationer. Medlemmer behøver ingen enhedsinvitation; eksterne personer accepterer én gatewayinvitation. Fejl i katalog/API giver en ufuldstændig opsætning.
- **Redigér, gem, se.** Når du gemmer koden, opdateres siden på din telefon. Din apps egne Host- og Origin-regler kan stadig blokere forespørgsler: I vores test behøvede Vite ingen ændring af `allowedHosts`; Next.js er ikke testet og kræver stadig `allowedDevOrigins`.
- **Oprydning.** Agenten kalder `unshare`, eller du kører `tslink remove <name>`, altid med det navn, share-resultatet returnerede. Fjernelsen afregistrerer delingen og forsøger at slette dens tailnet-node, når TSLink kan bevise, at den ejer den, så tjek resultatet for felter om oprydning eller bevaret tilstand. Den stopper ikke udviklingsserveren.
- **CLI eller MCP.** Styringskommandoerne tager `--json` og returnerer versionerede resultater; `tslink mcp` tilbyder handlinger for apps og adgang over MCP.
- **Én fil, ikke mappen.** En agent kan dele netop sin selvstændige HTML-rapport med `tslink share ./report.html`; de andre filer i mappen forbliver utilgængelige.
- **Begrænsede roller.** `viewer`, `app-operator` eller `people-manager`, afgrænset til de apps du angiver; at dele en ny app kræver rollen `owner`. `tslink mcp-audit` viser, hvad en agent har ændret. Roller begrænser TSLinks værktøjer, ikke agentens egen shell.

[Agentguide](agent-quickstart.md) · [MCP-rettigheder](mcp-scopes.md) · [Fjern-MCP](remote-mcp.md)

<a id="why"></a>

## Hvornår du har brug for TSLink

Serve er nok til én app, du selv starter og åbner på din egen telefon. TSLink er til de udviklingsservere, en agent starter, ofte flere, når hver skal have sit eget private navn, og deling, URL og oprydning skal være klar i CLI'en, MCP og en Claude Code-skill.

| Opgave | Kun Tailscale | TSLink |
|---|---|---|
| Flere udviklingsservere, din agent starter, ét navn hver | Serve sætter ruter op på enhedsniveau under ét navn pr. maskine; Services kræver for hvert navn en definition, en godkendelse og en tagget vært | Én node pr. app, med deling, opslag af URL og oprydning klar i CLI'en (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) og en skill |
| Én app, du selv starter, på din telefon | `tailscale serve 3000` er nok | `tslink share 3000` |
| Én person, én app, syv dage | Politikregler, derefter et JIT-værktøj eller manuel fjernelse | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/filer) |
| Browserlink, tre dage | Offentlig Funnel; tilføj selv adgangskontrol og planlagt lukning | `tslink guest create photos --for 3d --public --print-link` (kun HTTP) |

Private modtagere skal have Tailscale. Gæstelinks er offentlige, kan videresendes og fungerer som adgangsnøgler.

[Fuld sammenligning](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Dine apps, på dine egne enheder

- **En adresse til hver app.** Webapps, mapper, enkeltfiler og TCP-porte får hver deres eget navn i dit tailnet, så du bruger navne i stedet for IP-adresser.
- **Privat som standard.** Intet er offentligt, før du opretter et gæstelink eller udgiver via Funnel.
- **Én app, ikke hele maskinen.** Hver app, du udgiver, får sin egen node, der kun videresender til den app. Uden Tailscale-appen på værten tilføjer TSLink ingen andre porte fra værten til dit tailnet.
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
