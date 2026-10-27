<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink è una CLI e un server MCP open source che dà a ogni app del tuo computer un indirizzo privato <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, così tu o il tuo coding agent potete aprire sul telefono un server di sviluppo in esecuzione. Progetto indipendente, non realizzato da Tailscale.</p>
<p align="center">Il tuo telefono ha bisogno dell'app Tailscale, con l'accesso effettuato alla stessa tailnet.</p>
<p align="center"><strong>Italiano</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Altre lingue</a></p>

```text
# Claude Code, dopo aver installato la CLI (vedi Installazione più sotto)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Client MCP locali che usano JSON con mcpServers (altri: consulta la configurazione MCP del tuo client)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Chiedi al tuo agente: "Condividi il server di sviluppo così posso aprirlo sul telefono."
```

Lo stesso, a mano, con il server di sviluppo già in ascolto sulla porta 5173. Con `--json`, `share` riporta il nome registrato in `data.name`. Usa quel nome nei comandi successivi: è diverso da `--name` quando quel nome era già occupato, per esempio `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, oppure data.auth_url (un link di accesso a Tailscale) quando status è needs_login
tslink url <name> --wait              # dopo aver completato l'accesso: l'URL esatto
tslink remove <name>                  # quando hai finito; salta se share è fallito. Il server di sviluppo resta in esecuzione
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

<a id="agents"></a>

## Per gli agenti IA

Un server di sviluppo che un agente avvia su `localhost` non è raggiungibile dal tuo telefono. TSLink permette all'agente di dargli un indirizzo privato, consegnarti l'URL esatto e rimuovere la condivisione alla fine. Il tuo telefono ha bisogno dell'app Tailscale, con l'accesso effettuato alla stessa tailnet.

Testato il 2026-10-22 con Vite 8.3.5 e un iPhone 15 Pro: l'agente ha condiviso il server di sviluppo via MCP, il telefono ha aperto l'URL privato e la pagina si è aggiornata al salvataggio, senza ricaricare a mano.

Per Claude Code, installa prima la CLI, poi esegui:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Il plugin include una [skill di anteprima](../skills/tslink-preview/SKILL.md) e la connessione MCP qui sotto. Il marketplace resta in questo repository e recupera il plugin da [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). La skill di riferimento resta qui; il plugin ne contiene una copia verificata dalla CI. Altri agenti possono usare la stessa skill o collegarsi direttamente via MCP. Questa integrazione richiede la CLI locale e non funziona nella chat di claude.ai. [Configurazione e registrazione](agent-integrations.md)

Per un client MCP locale che usa JSON con `mcpServers` (altri client: vedi le loro istruzioni di configurazione MCP e la [guida ai client MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Gateway predefinito nelle nuove installazioni.** Ogni app HTTP/file privata riceve una porta stabile, come `https://home.<tailnet>.ts.net:8443/`. Le installazioni esistenti mantengono la modalità e ogni app conserva nodo/URL; `--portal-only` resta una scelta esplicita.
- **Registra il gateway.** Senza credenziali, l’account verificato del gateway senza tag può diventare owner. Con tag/OAuth, esegui localmente `tslink portal enable --owner <login>`. La registrazione in sospeso dei nodi app non blocca una route gateway pronta.
- **Accesso per persona.** Le nuove app consentono owner/admin e persone autorizzate esplicitamente. `people add`, `add --allow` e MCP `add/share.allow` verificano l’appartenenza e preparano gli inviti necessari. I membri non richiedono inviti dispositivo; gli esterni accettano un invito gateway. Errori di directory/API restituiscono uno stato incompleto.
- **Modifichi, salvi, vedi.** Salvando il codice la pagina si aggiorna sul telefono. Le regole Host e Origin della tua app possono comunque bloccare le richieste: nel nostro test Vite non ha richiesto modifiche a `allowedHosts`; Next.js non è stato provato e richiede ancora `allowedDevOrigins`.
- **Pulizia.** L'agente chiama `unshare`, oppure esegui `tslink remove <name>`, sempre con il nome restituito dal risultato di share. La rimozione annulla la registrazione della condivisione e tenta di eliminare il suo nodo nella tailnet quando TSLink può dimostrare di esserne il proprietario, quindi controlla nel risultato i campi di pulizia o di stato conservato. Non ferma il server di sviluppo.
- **CLI o MCP.** I comandi di gestione accettano `--json` e restituiscono risultati con versione; `tslink mcp` offre operazioni su app e accessi tramite MCP.
- **Un file, non la cartella.** Un agente può condividere solo il suo report HTML autonomo con `tslink share ./report.html`; gli altri file di quella cartella restano irraggiungibili.
- **Ruoli limitati.** `viewer`, `app-operator` o `people-manager`, circoscritti alle app che indichi; condividere una nuova app richiede il ruolo `owner`. `tslink mcp-audit` mostra cosa ha cambiato un agente. I ruoli limitano gli strumenti di TSLink, non la shell dell'agente.

[Guida per agenti](agent-quickstart.md) · [Permessi MCP](mcp-scopes.md) · [MCP remoto](remote-mcp.md)

<a id="why"></a>

## Quando ti serve TSLink

Per un'app che avvii tu e apri sul tuo telefono basta Serve. TSLink serve per i server di sviluppo che avvia un agente, spesso più di uno, quando ognuno deve avere un proprio nome privato e condivisione, URL e pulizia devono essere già pronti nella CLI, in MCP e in una skill di Claude Code.

| Obiettivo | Solo Tailscale | TSLink |
|---|---|---|
| Più server di sviluppo avviati dal tuo agente, un nome ciascuno | Serve imposta route a livello di dispositivo sotto un nome per macchina; Services richiede per ogni nome una definizione, un'approvazione e un host con tag | Un nodo per app, con condivisione, ricerca dell'URL e pulizia integrate nella CLI (`share`, `url`, `remove`), in MCP (`share`, `url`, `unshare`) e in una skill |
| Un'app che avvii tu, sul tuo telefono | Basta `tailscale serve 3000` | `tslink share 3000` |
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
