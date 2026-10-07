<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Indirizzi privati per le tue app, sulla tua rete Tailscale.</strong></p>
<p align="center">Aprile dai tuoi dispositivi. Condividine una con una persona o con un link, fino alla data che scegli.</p>
<p align="center"><strong>Italiano</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Altre lingue</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installazione

```sh
brew install --cask anydoor7/tap/tslink
```

I pacchetti Linux `.deb` e `.rpm` e le build per Windows sono nell'[ultima release](https://github.com/anydoor7/tslink/releases/latest). La prima volta che condividi un'app, TSLink mostra un link di accesso a Tailscale per quell'app. [Primi passi](getting-started.md)

<a id="use-cases"></a>

## Le tue app, sui tuoi dispositivi

- **Un indirizzo per ogni app.** App web, cartelle, singoli file e porte TCP ricevono ciascuno un proprio nome nella tua tailnet, così usi nomi invece di indirizzi IP.
- **Privato per impostazione predefinita.** Niente è pubblico finché non crei un link ospite o pubblichi tramite Funnel.
- **Una home page** che elenca le tue app con il loro stato. [Portale](portal.md)
- **Controlli di salute e avvisi** tramite comando o webhook, e un registro degli accessi che include le richieste negate. [Salute e avvisi](health-and-alerts.md) · [Cronologia accessi](access-log.md)
- **Ricette per 15 app self-hosted**, tra cui Home Assistant, Jellyfin, Immich e Ollama. `tslink apps detect` trova quelle già in esecuzione. [Ricette per app](apps.md)

## Condividi quando vuoi

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

I link ospite e i nuovi URL pubblici scadono e funzionano solo per le app web; cartelle, file e porte TCP restano privati. [Persone](people.md) · [Link ospite](guest-links.md) · [Accesso pubblico](funnel.md)

<a id="agents"></a>

## Per gli agenti IA

Un server di sviluppo che un agente avvia su `localhost` non è raggiungibile dal tuo telefono. TSLink permette all'agente di dargli un indirizzo privato, riportare l'URL esatto e rimuoverlo alla fine.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI o MCP.** I comandi di gestione accettano `--json` e restituiscono risultati con versione; `tslink mcp` offre operazioni su app e accessi tramite MCP.
- **Ruoli limitati.** `viewer`, `app-operator` o `people-manager`, circoscritti alle app che indichi. `tslink mcp-audit` mostra cosa ha cambiato un agente. I ruoli limitano gli strumenti di TSLink, non la shell dell'agente.

[Guida per agenti](agent-quickstart.md) · [Permessi MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

<a id="architecture"></a>

## Come funziona

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC o host cloud: CLI/MCP gestisce un demone comune e nodi per app. Il percorso privato usa Tailscale cifrato; HTTPS/Funnel pubblico opzionale raggiunge le app HTTP tramite controllo ospiti o pubblicazione aperta esplicita." width="960">
</picture>

Un processo in background esegue un nodo Tailscale separato per ogni app. Tailscale fornisce il trasporto nella tailnet e i certificati HTTPS. L'accesso privato a web e file si può limitare per identità Tailscale con `--allow` e con le autorizzazioni per persona; il TCP grezzo si affida alla policy della tua tailnet e al login dell'app stessa. [Architettura](architecture.md)

<a id="requirements"></a>

## Requisiti

| Chi | Cosa serve |
|---|---|
| Tu | Un account Tailscale con MagicDNS e HTTPS attivi |
| La macchina che esegue le tue app | TSLink, che include Tailscale (su Linux, una sessione utente systemd) |
| I tuoi dispositivi e le persone con cui condividi | L'app Tailscale |
| Ospiti | Un browser |

I nomi delle app HTTPS compaiono nei log pubblici dei certificati, quindi scegli nomi che non ti dispiace far vedere ad altri.

<a id="documentation"></a>

## Altro

[Tutta la documentazione](INDEX.md) · [Riferimento CLI](cli-reference.md) · [Confronto con Serve, ngrok e Cloudflare](comparison.md) · [Contribuire](../CONTRIBUTING.md) · [Sicurezza](../SECURITY.md)

Apache 2.0. TSLink è un progetto indipendente, non realizzato né approvato da Tailscale.
