<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo di TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Dai alle tue app, ai modelli e ai file locali un indirizzo privato dedicato.</strong><br>
  Aprili da un altro dispositivo autorizzato nella tua rete Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licenza: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 o successivo"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: nodi tsnet integrati"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 strumenti"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <strong>Italiano</strong> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installazione

Servono **Go 1.26.6 o successivo** e Git. Non sono ancora state pubblicate release precompilate né un cask Homebrew; installa dal codice sorgente. Questi esempi usano **bash o zsh**; consulta il [supporto delle piattaforme](platforms.md) per i requisiti di Windows e dei servizi in background.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Usa un account Tailscale con [MagicDNS e HTTPS abilitati](https://tailscale.com/docs/how-to/set-up-https-certificates). Il dispositivo destinatario deve essere connesso alla tua rete Tailscale (**tailnet**), con una policy che consenta l'accesso al servizio. TSLink integra Tailscale sul computer che pubblica.

### Condividi la tua prima pagina

Crea una pagina; TSLink la serve direttamente e avvia il servizio in background quando necessario:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Se TSLink mostra un URL di registrazione, aprilo per autorizzare il nodo; la tua tailnet potrebbe richiedere anche l'approvazione del dispositivo da parte di un amministratore. Poi ottieni l'indirizzo esatto:

```bash
tslink url demo --wait
```

Apri questo URL su un dispositivo autorizzato. Non serve un token API per questa prima condivisione.
[Configurazione completa e dettagli sul ciclo di vita →](getting-started.md)

<a id="use-cases"></a>

## Cosa vuoi condividere?

I file devono esistere; app, database e backend dei modelli devono essere già in esecuzione sulle porte indicate.

| Caso d’uso | Comando |
|---|---|
| Aprire un’app locale da un altro dispositivo | `tslink share 3000` |
| Esplorare una cartella di file | `tslink share ./public --name files` |
| Leggere un report HTML generato sul telefono | `tslink share ./report.html --name report` |
| Connettersi a un database locale tramite TCP | `tslink add database --tcp localhost:5432` |
| Usare un’API HTTP di un modello locale, come Ollama | `tslink add model --proxy localhost:11434` |

Per Ollama, ottieni l’URL esatto con `tslink url model --wait`; il `baseURL` di un client compatibile con OpenAI usa tale URL seguito da `/v1`. [Modelli locali e flussi con dati privati →](local-ai.md)

Per più app su un host, TSLink riunisce nodi con nome, elenchi di identità consentite per HTTP, scadenza di Funnel e gestione MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) può bastare per una sola app sui tuoi dispositivi.

<a id="architecture"></a>

## Architettura

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Esempio di mappa dei servizi: App, Docs, Database e Model sono nodi con nomi distinti nella stessa tailnet. App, file e API dei modelli usano HTTPS; il database usa TCP privato." width="960">
</picture>

**Una tailnet, nodi di servizio distinti.** Un daemon condiviso esegue un nodo tsnet integrato per ogni servizio, inoltrando HTTP, servendo file o facendo da proxy TCP. Le modifiche al registro hanno effetto durante l’esecuzione. Ogni nodo ha la propria identità di rete; i servizi condividono il computer che li pubblica.
[Dettagli dell’architettura →](architecture.md)

| Componente | Ruolo |
|---|---|
| [Go](../go.mod) | Eseguibile nativo a riga di comando |
| [Tailscale tsnet](architecture.md) | Nodi di servizio e trasporto della tailnet |
| [Cobra](https://github.com/spf13/cobra) | Comandi e guida |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Trasporti per gli agenti |
| Portachiavi del sistema e gestore dei servizi utente | Credenziali facoltative ed esecuzione in background |

I servizi rimangono nella tua tailnet a meno che tu non abiliti esplicitamente [Funnel pubblico](getting-started.md#more-examples). I servizi HTTP e di file supportano elenchi di identità autorizzate (`WhoIs`, `--allow`); TCP usa la policy della tailnet e l’autenticazione del backend. Consulta i [limiti della condivisione](sharing.md).

TSLink non installa app, non esegue modelli, non isola i processi dell’host e non aggrega più host. Rete, crittografia e HTTPS sono forniti da Tailscale; TSLink è un progetto indipendente.

<a id="agents"></a>

## Per gli agenti

I **19 strumenti MCP** consentono a un agente di condividere report, gestire servizi, ottenere URL e verificare la configurazione. Collega un client MCP locale all’eseguibile installato:

```json
{
  "mcpServers": {
    "tslink": {
      "command": "tslink",
      "args": ["mcp"]
    }
  }
}
```

MCP gestisce TSLink; le applicazioni usano l’API HTTP del modello per l’inferenza. Consulta i [client MCP](mcp-clients.md), [MCP remoto](remote-mcp.md) e la [guida operativa per gli agenti](../AGENTS.md) per configurazione e automazione.

L’automazione CLI supporta `--json` con `schema_version` pari a `1`; consulta `tslink status --urls --json`. MCP locale usa JSON-RPC su stdio. Vedi [automazione JSON](json-automation.md).

<a id="roadmap"></a>

## In arrivo

Gli elementi In integrazione, In revisione o Pianificato non sono inclusi nell’installazione da sorgente sopra.

| Caso d’uso | Stato |
|---|---|
| <!-- roadmap:people --> Concedi a un parente 3 giorni di accesso ad app HTTP/file private e raccogli gli inviti in un messaggio; il destinatario necessita ancora di Tailscale. | In integrazione |
| <!-- roadmap:health --> Controlla lo stato delle app e ricevi avvisi di interruzione o scadenza tramite un comando o webhook facoltativo. | In integrazione |
| <!-- roadmap:recipes --> Trova app compatibili su loopback e visualizza le ricette per app self-hosted prima di condividerle. | In integrazione |
| <!-- roadmap:limits --> Imposta dimensione degli upload e timeout delle richieste per ciascuna app HTTP per file grandi e client lenti. | In integrazione |
| <!-- roadmap:windows --> Riavvia un daemon Windows dopo un arresto anomalo durante una sessione attiva, con un’attività pianificata e un supervisore integrato. | In integrazione |
| <!-- roadmap:access-log --> Scopri chi ha aperto quale app nei log locali, con modalità di percorso `prefix`, `full` o `off`. | In revisione |
| <!-- roadmap:portal --> Apri una pagina iniziale con le app consentite e il passaggio di registrazione per i proprietari; i visitatori necessitano ancora di Tailscale. | In revisione |
| <!-- roadmap:mcp-scopes --> Assegna a un agente un ruolo e un ambito di app, con ricevute di audit per le modifiche. | In revisione |
| <!-- roadmap:guest-links --> Permetti a un ospite di aprire un’app HTTP nel browser senza installare Tailscale, con un link a scadenza e PIN facoltativo, tramite Funnel pubblico con controllo degli accessi. | In revisione |
| <!-- roadmap:durations --> Scegli durate predefinite o personalizzate di almeno 1 ora, con un massimo ospite predefinito di 7 giorni, configurabile. | In revisione |
| <!-- roadmap:requests --> Aiuta gli utenti di telefoni a partecipare tramite QR code e approva accesso o tempo aggiuntivo in un’azione. | In revisione |
| <!-- roadmap:multi-host --> Visualizza le app di più host in un inventario. | Pianificato |

<a id="documentation"></a>

## Documentazione e licenza

[Primi passi](getting-started.md) · [Modelli locali](local-ai.md) ·
[Riferimento CLI](cli-reference.md) · [Piattaforme](platforms.md) · [Roadmap](roadmap.md)

Per contribuire, segui [CONTRIBUTING.md](../CONTRIBUTING.md); segnala le vulnerabilità secondo [SECURITY.md](../SECURITY.md).

TSLink usa la [licenza Apache 2.0](../LICENSE) senza modifiche, anche per uso commerciale. Conserva il [NOTICE](../NOTICE) applicabile e gli [avvisi di terze parti](../THIRD_PARTY_NOTICES.md) quando lo ridistribuisci. La [collaborazione commerciale](../COMMERCIAL.md) è volontaria e non aggiunge condizioni alla licenza. I termini di servizio e i piani Tailscale si applicano separatamente.
