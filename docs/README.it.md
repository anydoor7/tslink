<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo di TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Condividi le app del tuo computer con le persone che scegli, per il tempo che decidi tu.</strong><br>
  Ogni app ha un indirizzo privato nella tua rete Tailscale. Guarda chi può accedere e revoca l'accesso quando vuoi.
</p>

<p align="center">
  <a href="#quickstart">Avvio rapido</a> · <a href="#agents">Per gli agenti</a> · <a href="getting-started.md">Documentazione</a> ·
  <strong>Italiano</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Tutte le lingue</a>
</p>

## A cosa serve

- **Apri il tuo lavoro sul telefono.** Un rapporto generato da uno script, un server di sviluppo, un notebook o un'API di un modello locale, a un indirizzo HTTPS privato raggiungibile dai dispositivi autorizzati.
- **Concedi a una persona una sola app, per un po'.** Lascia usare la raccolta di foto al tuo partner per una settimana o provare un'anteprima a un collega per tre giorni. L'accesso scade da solo; puoi anche terminarlo prima.
- **Lascia che sia il tuo agente a condividere.** Il tuo agente di programmazione ha appena creato una dashboard. Chiedigli di condividerla con te e un collega fino a venerdì. Può anche dirti cosa è condiviso e revocare una condivisione.

Le app continuano a funzionare dove sono già in esecuzione. TSLink controlla chi può raggiungerle e tiene un elenco di cosa è condiviso, con chi e fino a quando.

<a id="quickstart"></a>

## Avvio rapido

Servono **Go 1.26.6+**, Git e un account Tailscale con [MagicDNS e HTTPS abilitati](https://tailscale.com/docs/how-to/set-up-https-certificates). Non sono ancora pubblicate versioni precompilate, quindi installa dai sorgenti:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Condividi una pagina:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

La prima volta TSLink stampa un link di accesso per registrare il nuovo nodo di servizio; la tua tailnet potrebbe anche richiedere l'approvazione del dispositivo da parte di un amministratore. Dopo la registrazione, apri l'URL del servizio su un dispositivo autorizzato connesso alla tua tailnet. Non serve un token API.

Controlla le condivisioni e rimuovi la demo:

```bash
tslink status --urls
tslink remove demo
```

Altri contenuti che puoi condividere quando il loro backend è in esecuzione:

| Contenuto | Comando |
|---|---|
| Un'app web locale | `tslink share 3000` |
| Una cartella di file | `tslink share ./public --name files` |
| Un'API di modello locale, come Ollama | `tslink add model --proxy localhost:11434` |
| Un database tramite TCP privato | `tslink add database --tcp localhost:5432` |
| Un'app self-hosted nota (Jellyfin, Immich, Home Assistant e altre 13) | `tslink apps detect`, poi `tslink apps share jellyfin --yes` |

[Primi passi, piattaforme e servizio in background →](getting-started.md)

## Scegli chi può accedere

| Destinatari | Cosa serve | Identità | Fine dell'accesso |
|---|---|---|---|
| **I tuoi dispositivi** | Accesso alla tua tailnet | Identità Tailscale verificata | Quando rimuovi l'app |
| **Persone specifiche** (HTTP/file privati) | Un account Tailscale; chi è esterno accetta un invito per app | Identità Tailscale verificata | Alla scadenza impostata (`--for 7d`) o con `tslink people remove` |
| **Chiunque abbia l'URL** (Funnel) | Un browser | Chiunque; resta valido l'accesso richiesto dall'app | Dopo 24 ore per impostazione predefinita (`--funnel-ttl`) |
| **Link ospite nel browser** *(in arrivo)* | Un browser e un PIN facoltativo | Chi possiede il link | Alla sua scadenza o alla revoca |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Per HTTP e file privati, le scadenze vengono controllate a ogni richiesta. La revoca blocca le nuove richieste; non recupera dati già scaricati e non chiude flussi o connessioni WebSocket già accettati. [Condividere con persone →](people.md) · [Limiti della condivisione →](sharing.md)

<a id="agents"></a>

## Per gli agenti

TSLink include un server MCP: un agente può condividere, elencare, spiegare e rimuovere condivisioni come fai tu. Aggiungilo a un client MCP locale:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Risultati precisi.** L'automazione CLI supporta `--json` con `schema_version: 1` e codici di errore stabili; `tslink mcp` usa invece JSON-RPC. `tslink manifest` descrive ogni comando e opzione. Gli agenti devono ottenere gli URL reali con `tslink url <name> --wait`, senza costruirli.
- **Attese dichiarate.** Un nuovo nodo che richiede ancora l'accesso di una persona segnala `needs_login`, senza fingere di essere pronto.
- **Permessi.** MCP locale usa i permessi del tuo utente. MCP remoto è un endpoint da abilitare esplicitamente, accessibile solo dalla tailnet e limitato agli account o tag elencati. Ruoli per agente, ambiti delle app e ricevute delle azioni sono *in arrivo*.

L'MCP di TSLink gestisce TSLink stesso. Se pubblichi un altro server MCP tramite TSLink, quel server deve comunque avere i propri permessi per gli strumenti.
[Guida per agenti →](agents.md) · [Client MCP →](mcp-clients.md) · [MCP remoto →](remote-mcp.md) · [Automazione JSON →](json-automation.md)

## Quando usare altri strumenti

| Se vuoi | Considera |
|---|---|
| Un servizio locale sui tuoi dispositivi, con il client Tailscale già in uso | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Servizi amministrati con nomi stabili su più host | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Un URL pubblico per un webhook o una demo API, senza account Tailscale | [ngrok](https://ngrok.com/docs/start) o [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Installare ed eseguire app self-hosted, oltre a condividerle | [Umbrel](https://umbrel.com) o [Coolify](https://coolify.io) |
| Una piattaforma di accesso basata sull'identità per tutta l'organizzazione | [Pangolin](https://github.com/fosrl/pangolin) o [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink è adatto quando una persona esegue più app e vuole accessi temporanei per app e persona, consultabili sia da lei sia dal suo agente.

## Come funziona

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database e Model sono nodi distinti con un nome proprio nella stessa tailnet, eseguiti da un unico daemon TSLink sul computer che pubblica i servizi." width="720">
</picture>

Un daemon in background esegue un nodo Tailscale integrato per ogni app, con nome e indirizzo propri. Per HTTP e file privati, `WhoIs` e i permessi per persona o le regole `--allow` controllano l'accesso; le scadenze vengono verificate a ogni richiesta. TCP grezzo usa le regole della tailnet e l'autenticazione del backend. Tailscale fornisce trasporto della tailnet, crittografia e certificati; TSLink è un progetto indipendente. Le app condividono il computer che le pubblica: TSLink non le isola tra loro. [Architettura →](architecture.md)

## Stato

Disponibili: indirizzi privati per app, persone con scadenze e inviti raggruppati, Funnel pubblico con scadenza, controlli di salute e avvisi, ricette per app self-hosted, limiti delle richieste per app, riavvio dopo crash su Windows, CLI e MCP.

In arrivo: link ospite per browser, durate flessibili, registro degli accessi, pagina iniziale delle app, ruoli limitati per agenti, onboarding tramite QR e richieste di accesso. È prevista una lista unica per più computer. [Tabella di marcia →](roadmap.md)

## Documentazione e licenza

[Primi passi](getting-started.md) · [Riferimento CLI](cli-reference.md) · [Piattaforme](platforms.md) · [Modelli locali](local-ai.md) · [Contribuire](../CONTRIBUTING.md) · [Sicurezza](../SECURITY.md)

Apache License 2.0, anche per uso commerciale. Conserva [NOTICE](../NOTICE) e gli [avvisi di terze parti](../THIRD_PARTY_NOTICES.md) durante la ridistribuzione. Termini e piani Tailscale si applicano separatamente.
