<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Dai a ogni app sul tuo computer o server un proprio indirizzo privato nella tua rete Tailscale, e decidi chi può raggiungerla.</strong></p>

Apri le tue app web, cartelle, API di modelli e database dal tuo telefono e dal tuo portatile, con controlli di salute e cronologia accessi per ciascuna. I tuoi agenti IA possono dare alle app che avviano su localhost un indirizzo privato per gli altri tuoi dispositivi e controllarle, entro il ruolo che gli assegni. Quando qualcun altro deve entrare, concedi l'accesso a una persona specifica fino a una data, oppure apri un'app web a Internet per un periodo limitato.

**Richiede Tailscale.** Ti serve un account Tailscale (gratuito per uso personale), e ogni dispositivo che apre un'app privata deve avere l'app Tailscale; ospiti e visitatori pubblici hanno bisogno solo di un browser. TSLink è un progetto indipendente, non realizzato né approvato da Tailscale. [Requisiti](#requirements)

<p align="center"><a href="#quickstart">Avvio rapido</a> · <a href="#agents">Per gli agenti</a> · <a href="comparison.md">Confronto con Serve, ngrok e Cloudflare</a> · <a href="#documentation">Documentazione</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <strong>Italiano</strong> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Cosa puoi fare

### Raggiungere le tue app

- **Un indirizzo per ogni app.** `tslink share 3000`, `tslink share ./photos` o `tslink add db --tcp localhost:5432` dà a un'app web, una cartella, un file o un servizio TCP un proprio indirizzo privato nella tua tailnet (la tua rete Tailscale privata), ad esempio `https://photos.<tailnet>.ts.net`. Ogni app è un dispositivo Tailscale separato, quindi apri le app per nome invece che per indirizzo IP e porta.
- **Private, a meno che tu non scelga diversamente.** TSLink mantiene privato per impostazione predefinita l'accesso alle app, e le regole della tua tailnet decidono quali dispositivi possono connettersi. Apre un endpoint pubblico solo quando crei un link ospite o pubblichi esplicitamente tramite Funnel.
- **Tutto in un posto.** `tslink status --urls` elenca le app registrate su questo computer, e una home page privata facoltativa ne mostra indirizzi e stato. [Portale](portal.md)
- **Saperlo quando qualcosa si rompe.** I controlli di salute in background possono avvisarti tramite un comando o un webhook quando un'app si ferma o torna attiva, o quando il suo accesso a Tailscale sta per scadere. La cronologia accessi mostra chi ha aperto quale app e quando, comprese le richieste rifiutate. [Salute e avvisi](health-and-alerts.md) · [Cronologia accessi](access-log.md)
- **Ricette per app.** Le ricette coprono 15 app self-hosted, tra cui Home Assistant, Jellyfin, Immich e Ollama, e `tslink apps detect` può trovare le app supportate già in ascolto in locale. Per caricare foto e video di grandi dimensioni, [alza i limiti di caricamento della singola app](sharing.md). [Ricette per app](apps.md) · [IA locale](local-ai.md)

### Lascia lavorare i tuoi agenti

Quando un agente avvia un server di sviluppo, un'anteprima o un'API di modello locale su `localhost`, il tuo telefono e gli altri computer non possono raggiungere quell'indirizzo. TSLink permette all'agente di dare un indirizzo privato a quel servizio, dirti l'URL esatto e poi rimuoverne la registrazione, entro i limiti che stabilisci.

- **Condividi, controlla, annulla.** `share --json` restituisce il nome registrato e l'URL esatto oppure un link di accesso da aprire. `url <name> --wait` e `status --urls --name <name>` segnalano se l'endpoint è pronto, e `remove <name>` (`unshare` in MCP) rimuove la condivisione. [Guida per agenti](agent-quickstart.md)
- **Fatto per l'automazione.** I comandi di gestione diversi da `tslink mcp` accettano `--json` e restituiscono risultati con versione e codici di errore stabili. `tslink mcp` espone gli strumenti per app e accessi a un client MCP locale tramite JSON-RPC. Con i binding dei chiamanti configurati, `tslink serve --mcp` espone questi strumenti ai client MCP sugli altri tuoi dispositivi attraverso la tailnet. [Automazione JSON](json-automation.md) · [MCP remoto](remote-mcp.md)
- **Autorità limitata.** Un agente locale ha per impostazione predefinita l'autorità di proprietario. Dai a un agente un ruolo ridotto (`viewer`, `app-operator` o `people-manager`) che copre solo le app che indichi e limita la durata massima di ogni accesso che concede. Le modifiche fatte tramite MCP vengono registrate e `tslink mcp-audit` le mostra. I ruoli limitano gli strumenti di TSLink, non la shell o i file dell'agente stesso. [Permessi MCP](mcp-scopes.md)

### Condividi con le persone che scegli

- **Persone specifiche, fino a una data.** `tslink people add alice@example.com --apps photos,notes --for 7d` consente a quell'account Tailscale di aprire quelle app web e di file fino alla scadenza. `people update`, `extend` e `people remove` modificano o chiudono l'accesso; dopo la rimozione la sua richiesta successiva viene rifiutata, ma ciò che ha già scaricato non si può recuperare. [Persone](people.md) · [Durate](durations.md)
- **Qualcuno fuori dalla tua tailnet.** Aggiungi `--invite --print-links` per ottenere un messaggio pronto da inviare con un invito dispositivo per ogni app (serve un token API di proprietà di un utente). `--qr` stampa un codice per configurare il telefono.
- **Richieste.** Le persone nella tua tailnet possono chiedere più tempo, o l'accesso a un'app che hai segnato come richiedibile, dalla home page. Approvi con una durata in un solo comando. [Richieste di accesso](requests.md)

### Apri un'app web a Internet, per un po'

- **Link ospite.** `tslink guest create photos --for 3d --public --print-link` crea un link per browser a un'app web, con PIN facoltativo, che puoi revocare singolarmente. Gli ospiti non hanno bisogno di un account Tailscale. Chiunque abbia il link può usarlo, quindi non dimostra chi ha visitato. [Ospiti](guest-links.md)
- **Un URL pubblico aperto.** `tslink add preview --proxy localhost:3000 --funnel --public` pubblica un'app web per chiunque abbia il suo URL. Una nuova pubblicazione dura 24 ore per impostazione predefinita; usa `--funnel-ttl` per scegliere un'altra durata. [Funnel](funnel.md)
- I nuovi link ospite e le nuove pubblicazioni pubbliche aperte passano da Tailscale Funnel e hanno una durata limitata (minimo 1 ora, massimo predefinito 7 giorni, modificabile dal proprietario). Questi percorsi pubblici supportano le app con proxy HTTP; i servizi diretti di cartelle o file e il TCP grezzo restano privati.

Tutto quanto sopra è incluso nella v0.1.0.

<a id="requirements"></a>

## Requisiti

TSLink è costruito su Tailscale. È un progetto indipendente, non realizzato né approvato da Tailscale, e si applicano i termini e i [piani](https://tailscale.com/pricing) di Tailscale.

| Chi | Cosa serve |
|---|---|
| Tu | Un account Tailscale con [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) attivati. Il piano Personal gratuito è per uso non commerciale. |
| Il computer o server che esegue le tue app | Solo TSLink. Integra Tailscale, quindi non serve installare Tailscale a parte. Con la configurazione predefinita, ogni nuovo nodo app richiede un accesso dal browser e può richiedere l'approvazione del dispositivo. Le [credenziali salvate](credentials-and-tags.md) permettono la registrazione senza un accesso dal browser per ogni app. |
| Gli altri tuoi dispositivi | L'app Tailscale, collegata alla tua tailnet. |
| Le persone che scegli | L'app Tailscale e il proprio account. Possono entrare nella tua tailnet, il che aggiunge un utente al tuo piano, oppure accettare un invito dispositivo per ogni app. Le regole della tua tailnet devono consentire loro l'accesso. |
| Ospiti e visitatori pubblici | Un browser. La tua tailnet deve consentire Funnel, che Tailscale considera ancora in beta. |

Quando viene emesso un certificato HTTPS per un'app, il suo nome di dispositivo Tailscale e il nome DNS della tua tailnet compaiono in un registro pubblico dei certificati. Scegli nomi di app che non ti dispiace far vedere.

<a id="installation"></a>
<a id="quickstart"></a>

## Avvio rapido

Su macOS o Linux installa con Homebrew. Il binario per macOS è firmato con un certificato Developer ID e notarizzato da Apple. Per aggiornare in seguito, esegui `brew upgrade --cask tslink` e poi di nuovo `tslink install` se TSLink è in esecuzione come servizio in background.

```bash
brew install --cask anydoor7/tap/tslink
```

Su Windows scarica `tslink_<version>_windows_<arch>.zip` dall'[ultima release](https://github.com/anydoor7/tslink/releases/latest), verificalo con `checksums.txt` ed esegui `tslink install` per avviare TSLink all'accesso. Lo zip non ha firma Authenticode; [verifica la release](verify-release.md) tramite i checksum firmati e le attestazioni. I pacchetti Linux `.deb` e `.rpm` sono nella stessa pagina della release. Per compilare dai sorgenti servono **Git e Go 1.26.6+**. I comandi seguenti usano bash/zsh; vedi [Configurazione macOS, Linux e Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Servono un **account Tailscale** e [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). I dispositivi privati destinatari richiedono Tailscale e autorizzazione nelle regole di rete. TSLink incorpora Tailscale sull'host delle app.

Con l'app già in esecuzione sulla porta 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Scegli un nome libero; se `share` ne restituisce un altro, usalo in `url`. Completa prima la registrazione nel browser e l'approvazione del dispositivo richieste, poi apri l'URL esatto da un dispositivo autorizzato. `share` avvia il servizio in background se necessario. Il primo accesso privato non richiede token API amministrativi. Per i file usa `tslink share ./report.html`; i file devono esistere e le app essere in esecuzione. [Guida completa](getting-started.md)

Quando ti è utile e funziona, puoi [dare una stella a TSLink](https://github.com/anydoor7/tslink) per farlo conoscere. È del tutto facoltativo.

<a id="architecture"></a>

## Come funziona

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Un PC o host cloud: CLI/MCP gestisce un demone comune e nodi per app. Il percorso privato usa Tailscale cifrato; HTTPS/Funnel pubblico opzionale raggiunge le app HTTP tramite controllo ospiti o pubblicazione aperta esplicita." width="960">
</picture>

Immagina un percorso privato e cifrato verso le tue app. **Tailscale fornisce trasporto di rete e HTTPS; TSLink gestisce gli accessi su ogni host.** Un demone esegue un nodo integrato per servizio. Il portale privato mostra le app consentite; stato e cronologia aiutano nella manutenzione.

L'accesso pubblico va attivato: agli ospiti servono link ed eventuale PIN; Funnel aperto è raggiungibile da chiunque abbia l'URL. Entrambi usano HTTPS pubblico, non l'identità privata dell'utente. TCP puro resta privato e dipende dalle regole tailnet e dall'autenticazione del backend. TSLink non installa app, isola processi, crea VPC cloud o aggrega host. Progetto indipendente che funziona con Tailscale. [Architettura e limiti](architecture.md)

<a id="agents"></a>

## Per gli agenti

Gestisci inventario, salute, URL e accessi via CLI/MCP. Parti dalla [guida per agenti](agent-quickstart.md), leggi gli schemi correnti degli strumenti e verifica l'accesso reale prima di dichiarare il successo.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

La CLI usa `--json` per l'automazione; MCP usa JSON-RPC su stdio. [Client](mcp-clients.md) · [MCP remoto](remote-mcp.md) · [Ruoli e ambiti](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Documentazione e licenza

[Tutte le guide](INDEX.md) · [Riferimento CLI](cli-reference.md) · [IA locale](local-ai.md) · [Salute](health-and-alerts.md) · [Cronologia accessi](access-log.md) · [Roadmap](roadmap.md)

L'inventario multi-host è pianificato. Sono benvenuti [contributi](../CONTRIBUTING.md) e [segnalazioni di sicurezza](../SECURITY.md). [Apache 2.0](../LICENSE) consente l'uso commerciale; conserva [NOTICE](../NOTICE) e gli [avvisi di terze parti](../THIRD_PARTY_NOTICES.md) nella ridistribuzione. La [collaborazione commerciale](../COMMERCIAL.md) è volontaria. Termini e piani Tailscale si applicano separatamente.
