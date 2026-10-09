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

Gli host macOS richiedono macOS 13 Ventura o successivo ([piattaforme supportate](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Su Windows, installa con Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Per aggiornare, esegui `brew upgrade --cask tslink` o `scoop update; scoop update tslink`. Se TSLink viene eseguito come servizio in background, esegui nuovamente `tslink install` dopo l’aggiornamento.

I pacchetti Linux `.deb` e `.rpm` e le build per Windows sono nell'[ultima release](https://github.com/anydoor7/tslink/releases/latest). La prima volta che condividi un'app, TSLink mostra un link di accesso a Tailscale per quell'app. [Primi passi](getting-started.md)

<a id="why"></a>

## Quando ti serve TSLink

Per un'app sui tuoi dispositivi basta Serve. TSLink riunisce indirizzi delle app, scadenze e modifiche agli accessi in un unico flusso.

| Obiettivo | Solo Tailscale | TSLink |
|---|---|---|
| Una web app sul telefono | Basta `tailscale serve 3000` | `tslink share 3000` |
| Più app, ognuna con il suo nome | Configurare Services, o nodi separati | Un `share`/`add` per app; registrare ogni nodo |
| Una persona, un'app, sette giorni | Regole di policy, poi uno strumento JIT o la rimozione manuale | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/file) |
| Link per browser, tre giorni | Funnel pubblico; aggiungere un controllo d'accesso e lo spegnimento programmato | `tslink guest create photos --for 3d --public --print-link` (solo HTTP) |

I destinatari privati hanno bisogno di Tailscale. I link ospite sono pubblici, inoltrabili e fungono da credenziali di accesso.

[Confronto completo](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Le tue app, sui tuoi dispositivi

- **Un indirizzo per ogni app.** App web, cartelle, singoli file e porte TCP ricevono ciascuno un proprio nome nella tua tailnet, così usi nomi invece di indirizzi IP.
- **Privato per impostazione predefinita.** Niente è pubblico finché non crei un link ospite o pubblichi tramite Funnel.
- **Un'app, non tutta la macchina.** Ogni app che pubblichi ha un proprio nodo, che inoltra solo a quell'app. Senza l'app Tailscale sull'host, TSLink non aggiunge altre porte dell'host alla tua tailnet.
- **Una home page** che elenca le tue app con il loro stato. [Portale](portal.md)
- **Controlli dello stato e avvisi** tramite comando o webhook, e un registro degli accessi che include le richieste negate. [Salute e avvisi](health-and-alerts.md) · [Cronologia accessi](access-log.md)
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
- **Un file, non la cartella.** Un agente può condividere solo il suo report HTML con `tslink share ./report.html`; gli altri file di quella cartella restano irraggiungibili.
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
