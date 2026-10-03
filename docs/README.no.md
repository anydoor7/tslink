<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Del appene på datamaskinen din med dem du velger, så lenge du ønsker.</strong><br>
  Hver app får sin egen private adresse i Tailscale-nettverket ditt. Se hvem som har tilgang, og trekk den tilbake.
</p>

<p align="center">
  <a href="#quickstart">Hurtigstart</a> · <a href="#agents">For agenter</a> · <a href="getting-started.md">Dokumentasjon</a> ·
  <strong>Norsk</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Alle språk</a>
</p>

## Hva folk bruker det til

- **Åpne arbeidet ditt på telefonen.** En rapport fra skriptet ditt, en utviklingsserver, en notebook eller et lokalt modell-API, på en privat HTTPS-adresse som tillatte enheter kan nå.
- **Gi én person én app for en stund.** La partneren bruke bildebiblioteket i en uke eller en kollega prøve forhåndsvisningen i tre dager. Tilgangen utløper automatisk; du kan også avslutte den tidligere.
- **La agenten stå for delingen.** Kodeagenten din har nettopp laget et dashbord. Be den dele det med deg og en kollega fram til fredag. Den kan også fortelle hva som deles nå, og trekke en deling tilbake.

Appene kjører videre der de allerede kjører. TSLink styrer hvem som kan nå hver app, og fører én liste over hva som deles, med hvem og til når.

<a id="quickstart"></a>

## Hurtigstart

Du trenger **Go 1.26.6+**, Git og en Tailscale-konto med [MagicDNS og HTTPS aktivert](https://tailscale.com/docs/how-to/set-up-https-certificates). Ferdigbygde utgivelser er ennå ikke publisert, så installer fra kildekoden:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Del en side:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Første gang skriver TSLink ut en innloggingslenke for å registrere den nye tjenestenoden; tailnet kan også kreve at en administrator godkjenner enheten. Etter registrering åpner du tjenestens URL på en tillatt enhet som er logget inn i ditt tailnet. Du trenger ikke et API-token.

Kontroller hva som deles, og fjern deretter demoen:

```bash
tslink status --urls
tslink remove demo
```

Andre ting du kan dele når bakenden kjører:

| Innhold | Kommando |
|---|---|
| En lokal webapp | `tslink share 3000` |
| En mappe med filer | `tslink share ./public --name files` |
| Et lokalt modell-API, som Ollama | `tslink add model --proxy localhost:11434` |
| En database over privat TCP | `tslink add database --tcp localhost:5432` |
| En kjent selvhostet app (Jellyfin, Immich, Home Assistant og 13 andre) | `tslink apps detect`, deretter `tslink apps share jellyfin --yes` |

[Komme i gang, plattformer og bakgrunnstjeneste →](getting-started.md)

## Velg hvem som kan åpne den

| Målgruppe | Hva mottakeren trenger | Identitet | Tilgangen slutter |
|---|---|---|---|
| **Dine egne enheter** | Innlogging i ditt tailnet | Verifisert Tailscale-innlogging | Når du fjerner appen |
| **Navngitte personer** (privat HTTP/filer) | En Tailscale-innlogging; utenforstående godtar én invitasjon per app | Verifisert Tailscale-innlogging | Ved fristen du setter (`--for 7d`), eller med `tslink people remove` |
| **Alle med URL-en** (Funnel) | En nettleser | Hvem som helst; appens egen innlogging gjelder fortsatt | Etter 24 timer som standard (`--funnel-ttl`) |
| **Gjestelenke i nettleseren** *(kommer)* | En nettleser og en valgfri PIN | Den som har lenken | Ved egen frist eller tilbakekalling |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

For private HTTP- og fildelinger kontrolleres frister ved hver forespørsel. Tilbakekalling stopper nye forespørsler; den kan ikke hente tilbake nedlastede data eller lukke strømmer og WebSocket-forbindelser som allerede er akseptert. [Del med personer →](people.md) · [Grenser for deling →](sharing.md)

<a id="agents"></a>

## For agenter

TSLink har en MCP-server, slik at en agent kan dele, liste opp, forklare og fjerne delinger på samme måte som deg. Legg den til i en lokal MCP-klient:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Nøyaktige resultater.** CLI-automatisering støtter `--json` med `schema_version: 1` og stabile feilkoder; `tslink mcp` bruker i stedet JSON-RPC. `tslink manifest` beskriver alle kommandoer og flagg. Agenter bør hente reelle URL-er med `tslink url <name> --wait` i stedet for å sette dem sammen.
- **Ærlige ventetilstander.** En ny node som fortsatt trenger menneskelig innlogging, rapporterer `needs_login` i stedet for å late som den er klar.
- **Rettigheter.** Lokal MCP kjører med brukerens rettigheter. Ekstern MCP må aktiveres uttrykkelig, er bare tilgjengelig i tailnet og begrenses til innloggingene eller taggene du oppgir. Roller per agent, appavgrensninger og kvitteringer for handlinger *kommer*.

MCP i TSLink styrer TSLink selv. Publiserer du en annen MCP-server gjennom TSLink, trenger den fortsatt egne verktøyrettigheter.
[Agentveiledning →](agents.md) · [MCP-klienter →](mcp-clients.md) · [Ekstern MCP →](remote-mcp.md) · [JSON-automatisering →](json-automation.md)

## Når andre verktøy passer bedre

| Hvis du vil ha | Vurder |
|---|---|
| Én lokal tjeneste på egne enheter med Tailscale-klienten du allerede kjører | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Administratorstyrte tjenester med stabile navn på tvers av flere verter | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| En offentlig URL for en webhook eller API-demo uten Tailscale-konto | [ngrok](https://ngrok.com/docs/start) eller [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Installere og kjøre selvhostede apper, i tillegg til å dele dem | [Umbrel](https://umbrel.com) eller [Coolify](https://coolify.io) |
| En identitetsbasert tilgangsplattform for hele organisasjonen | [Pangolin](https://github.com/fosrl/pangolin) eller [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink passer når én person kjører flere apper og ønsker tidsbegrenset tilgang per app og person som både personen og agenten kan kontrollere.

## Slik fungerer det

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database og Model er separate navngitte noder i ett tailnet, kjørt av én TSLink-daemon på datamaskinen som publiserer tjenestene." width="720">
</picture>

Én bakgrunnsdaemon kjører en innebygd Tailscale-node for hver app, slik at hver app har eget navn og adresse. For privat HTTP og filer styrer `WhoIs` og personrettigheter eller `--allow`-regler tilgangen; personfrister sjekkes ved hver forespørsel. Rå TCP bruker tailnet-policyen og autentiseringen i bakenden. Tailscale leverer tailnet-transport, kryptering og sertifikater; TSLink er et uavhengig prosjekt. Alle appene deler datamaskinen som publiserer dem, så TSLink isolerer dem ikke fra hverandre. [Arkitektur →](architecture.md)

## Status

Tilgjengelig nå: private adresser per app, personer med frister og samlede invitasjoner, offentlig Funnel med utløp, helsesjekker og varsler, oppskrifter for selvhostede apper, forespørselsgrenser per app, omstart etter krasj i Windows, CLI og MCP. Også tilgjengelig: gjestelenker i nettleseren, fleksible varigheter, tilgangslogg, en startside for appene, avgrensede agentroller, QR-oppstart og tilgangsforespørsler.

En felles liste for flere datamaskiner er planlagt. [Utviklingsplan →](roadmap.md)

## Dokumentasjon og lisens

[Komme i gang](getting-started.md) · [CLI-referanse](cli-reference.md) · [Plattformer](platforms.md) · [Lokale modeller](local-ai.md) · [Bidra](../CONTRIBUTING.md) · [Sikkerhet](../SECURITY.md)

Apache License 2.0, også for kommersiell bruk. Behold [NOTICE](../NOTICE) og [tredjepartsmerknader](../THIRD_PARTY_NOTICES.md) ved videredistribusjon. Vilkårene for Tailscale og abonnementer gjelder separat.
