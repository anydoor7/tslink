<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Del apps på din computer med dem, du vælger, så længe du ønsker.</strong><br>
  Hver app får sin egen private adresse på dit Tailscale-netværk. Se, hvem der har adgang, og træk den tilbage.
</p>

<p align="center">
  <a href="#quickstart">Kom hurtigt i gang</a> · <a href="#agents">Til agenter</a> · <a href="getting-started.md">Dokumentation</a> ·
  <strong>Dansk</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Alle sprog</a>
</p>

## Hvad folk bruger det til

- **Åbn dit arbejde på telefonen.** En rapport fra dit script, en udviklingsserver, en notebook eller et lokalt model-API, på en privat HTTPS-adresse, som tilladte enheder kan nå.
- **Giv én person én app i en periode.** Lad din partner bruge fotobiblioteket i en uge eller en kollega prøve din forhåndsvisning i tre dage. Adgangen udløber automatisk; du kan også afslutte den tidligere.
- **Lad din agent stå for delingen.** Din kodeagent har lige bygget et dashboard. Bed den dele det med dig og din kollega indtil fredag. Den kan også fortælle, hvad der deles nu, og trække en deling tilbage.

Dine apps kører videre, hvor de allerede kører. TSLink styrer, hvem der kan nå hver app, og fører én liste over, hvad der deles, med hvem og indtil hvornår.

<a id="quickstart"></a>

## Kom hurtigt i gang

Du skal bruge **Go 1.26.6+**, Git og en Tailscale-konto med [MagicDNS og HTTPS aktiveret](https://tailscale.com/docs/how-to/set-up-https-certificates). Der er endnu ingen udgivne færdigbyggede versioner, så installer fra kildekoden:

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

Første gang viser TSLink et loginlink til registrering af den nye tjenesteknude; dit tailnet kan også kræve, at en administrator godkender enheden. Åbn derefter tjenestens URL på en tilladt enhed, der er logget ind på dit tailnet. Et API-token er ikke nødvendigt.

Se, hvad der deles, og fjern så demoen:

```bash
tslink status --urls
tslink remove demo
```

Andre ting, du kan dele, når deres backend kører:

| Indhold | Kommando |
|---|---|
| En lokal webapp | `tslink share 3000` |
| En mappe med filer | `tslink share ./public --name files` |
| Et lokalt model-API, som Ollama | `tslink add model --proxy localhost:11434` |
| En database via privat TCP | `tslink add database --tcp localhost:5432` |
| En kendt selvhostet app (Jellyfin, Immich, Home Assistant og 13 andre) | `tslink apps detect`, derefter `tslink apps share jellyfin --yes` |

[Introduktion, platforme og baggrundstjeneste →](getting-started.md)

## Vælg, hvem der kan åbne den

| Målgruppe | Hvad modtageren behøver | Identitet | Adgangen slutter |
|---|---|---|---|
| **Dine egne enheder** | Login på dit tailnet | Verificeret Tailscale-login | Når du fjerner appen |
| **Navngivne personer** (privat HTTP/filer) | Et Tailscale-login; personer udenfor accepterer én invitation pr. app | Verificeret Tailscale-login | Ved den valgte frist (`--for 7d`) eller med `tslink people remove` |
| **Alle med URL'en** (Funnel) | En browser | Alle; appens eget login gælder stadig | Efter 24 timer som standard (`--funnel-ttl`) |
| **Gæstelink til browser** *(kommer snart)* | En browser og eventuelt en PIN | Den, der har linket | Ved egen udløbsfrist eller tilbagekaldelse |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Frister for private HTTP- og fildelinger kontrolleres ved hver forespørgsel. Tilbagekaldelse stopper nye forespørgsler; den kan ikke hente downloadede data tilbage eller lukke allerede accepterede streams og WebSocket-forbindelser. [Deling med personer →](people.md) · [Grænser for deling →](sharing.md)

<a id="agents"></a>

## Til agenter

TSLink indeholder en MCP-server, så en agent kan dele, liste, forklare og fjerne delinger ligesom dig. Tilføj den til en lokal MCP-klient:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Præcise resultater.** CLI-automatisering understøtter `--json` med `schema_version: 1` og stabile fejlkoder; `tslink mcp` bruger i stedet JSON-RPC. `tslink manifest` beskriver hver kommando og hvert flag. Agenter bør hente rigtige URL'er med `tslink url <name> --wait` i stedet for at konstruere dem.
- **Tydelige ventetilstande.** En ny knude, der stadig kræver en persons login, rapporterer `needs_login` i stedet for at foregive at være klar.
- **Rettigheder.** Lokal MCP kører med din brugers rettigheder. Fjern-MCP er et tilvalg, kun tilgængeligt i dit tailnet og begrænset til de angivne loginidentiteter eller tags. Roller pr. agent, appafgrænsninger og handlingskvitteringer *kommer snart*.

MCP i TSLink styrer TSLink selv. Udgiver du en anden MCP-server gennem TSLink, skal den stadig have sine egne værktøjsrettigheder.
[Agentvejledning →](agents.md) · [MCP-klienter →](mcp-clients.md) · [Fjern-MCP →](remote-mcp.md) · [JSON-automatisering →](json-automation.md)

## Hvornår andre værktøjer passer bedre

| Hvis du vil have | Overvej |
|---|---|
| Én lokal tjeneste på dine egne enheder med den Tailscale-klient, du allerede kører | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Administratorstyrede tjenester med stabile navne på tværs af flere værter | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| En offentlig URL til en webhook eller API-demo uden en Tailscale-konto | [ngrok](https://ngrok.com/docs/start) eller [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Installere og køre selvhostede apps, ud over at dele dem | [Umbrel](https://umbrel.com) eller [Coolify](https://coolify.io) |
| En identitetsbaseret adgangsplatform til hele organisationen | [Pangolin](https://github.com/fosrl/pangolin) eller [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink passer, når én person kører flere apps og ønsker tidsbegrænset adgang pr. app og person, som både personen og agenten kan undersøge.

## Sådan virker det

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database og Model er særskilte navngivne knuder i ét tailnet, drevet af én TSLink-daemon på computeren, der udgiver tjenesterne." width="720">
</picture>

Én baggrundsdaemon kører en indlejret Tailscale-knude for hver app, så hver app har sit eget navn og sin egen adresse. For privat HTTP og filer styrer `WhoIs` og personrettigheder eller `--allow`-regler adgangen; personfrister kontrolleres ved hver forespørgsel. Rå TCP bruger tailnet-politikken og backendens godkendelse. Tailscale leverer tailnet-transport, kryptering og certifikater; TSLink er et uafhængigt projekt. Alle apps deler computeren, der udgiver dem, så TSLink isolerer dem ikke fra hinanden. [Arkitektur →](architecture.md)

## Status

Tilgængeligt nu: private adresser pr. app, personer med frister og samlede invitationer, offentlig Funnel med udløb, apphelbredstjek og alarmer, opskrifter til selvhostede apps, forespørgselsgrænser pr. app, genstart efter nedbrud i Windows, CLI og MCP.

På vej: gæstelinks til browser, fleksible varigheder, adgangslog, en startside med dine apps, afgrænsede agentroller, QR-introduktion og adgangsanmodninger. En fælles liste for flere computere er planlagt. [Udviklingsplan →](roadmap.md)

## Dokumentation og licens

[Introduktion](getting-started.md) · [CLI-reference](cli-reference.md) · [Platforme](platforms.md) · [Lokale modeller](local-ai.md) · [Bidrag](../CONTRIBUTING.md) · [Sikkerhed](../SECURITY.md)

Apache License 2.0, også til kommerciel brug. Bevar [NOTICE](../NOTICE) og [tredjepartsmeddelelser](../THIRD_PARTY_NOTICES.md) ved videredistribution. Vilkår for Tailscale og abonnementer gælder separat.
