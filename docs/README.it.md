<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Accedi alle tue app e gestiscile ovunque.<br>Tienile private o condividile alle tue condizioni.</strong></p>

Le tue app, sul tuo computer o server cloud: raggiungile tramite una rete privata cifrata oppure scegli link ospite per browser o accesso pubblico. Gestiscile direttamente o tramite un agente.

<p align="center"><a href="#quickstart">Avvio rapido</a> · <a href="#agents">Per gli agenti</a> · <a href="#documentation">Documentazione</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <strong>Italiano</strong> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Le tue app a portata di mano

| Cosa ti serve | Cosa offre TSLink |
|---|---|
| Usare le tue app da più dispositivi | Indirizzi privati per dashboard domestiche, pagine web solo locali, file, API di modelli e servizi TCP su PC o server. |
| Condividere con persone specifiche | App HTTP/file selezionate, identità Tailscale verificata, scadenza e revoca. I destinatari usano Tailscale. [Persone](people.md) |
| Accogliere visite dal browser | Link ospite a scadenza con PIN facoltativo per app proxy HTTP, oppure HTTPS esplicitamente pubblico tramite Funnel. I link sono inoltrabili e non verificano l'identità. [Ospiti](guest-links.md) |
| Gestire un insieme di app | Inventario per host, portale privato, controlli di salute e avvisi, cronologia accessi e gestione CLI/MCP con ruoli, ambiti per app e registrazioni di audit. [Portale](portal.md) · [Permessi MCP](mcp-scopes.md) |

[Ricette per app](apps.md), [limiti di caricamento](sharing.md), [durate flessibili](durations.md) e [QR e richieste di accesso](requests.md) semplificano la manutenzione. Sono già inclusi in questo codice sorgente.

<a id="installation"></a>
<a id="quickstart"></a>

## Avvio rapido

Installa dai sorgenti con **Git e Go 1.26.6+**; release precompilate e Homebrew non sono ancora pubblicati. I comandi usano bash/zsh. [Configurazione macOS, Linux e Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Servono accesso al repository, un **account Tailscale** e [MagicDNS e HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). I dispositivi privati destinatari richiedono Tailscale e autorizzazione nelle regole di rete. TSLink incorpora Tailscale sull'host delle app.

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
