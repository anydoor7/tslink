<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-Logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Teile die Apps auf deinem Computer mit den Menschen deiner Wahl, so lange du möchtest.</strong><br>
  Jede App erhält eine eigene private Adresse in deinem Tailscale-Netzwerk. Sieh nach, wer Zugriff hat, und entziehe ihn wieder.
</p>

<p align="center">
  <a href="#quickstart">Schnellstart</a> · <a href="#agents">Für Agenten</a> · <a href="getting-started.md">Dokumentation</a> ·
  <strong>Deutsch</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Alle Sprachen</a>
</p>

## Wofür Menschen TSLink nutzen

- **Öffne deine Arbeit auf dem Handy.** Ein Bericht aus deinem Skript, ein Entwicklungsserver, ein Notebook oder eine lokale Modell-API: über eine private HTTPS-Adresse auf berechtigten Geräten erreichbar.
- **Gib einer Person vorübergehend Zugriff auf eine App.** Lass deinen Partner eine Woche lang die Fotobibliothek nutzen oder einen Kollegen drei Tage lang deine Vorschau testen. Der Zugriff endet automatisch; du kannst ihn auch früher beenden.
- **Lass deinen Agenten das Teilen übernehmen.** Dein Coding-Agent hat gerade ein Dashboard gebaut. Bitte ihn, es bis Freitag mit dir und deinem Teamkollegen zu teilen. Er kann dir auch sagen, was gerade geteilt wird, und eine Freigabe zurücknehmen.

Deine Apps laufen weiter an ihrem bisherigen Ort. TSLink steuert, wer welche App erreichen kann, und führt eine Liste: Was ist geteilt, mit wem und bis wann?

<a id="quickstart"></a>

## Schnellstart

Du brauchst **Go 1.26.6+**, Git und ein Tailscale-Konto mit [aktiviertem MagicDNS und HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Fertige Binärversionen sind noch nicht veröffentlicht; installiere deshalb aus dem Quellcode:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Teile eine Seite:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Beim ersten Mal gibt TSLink einen Anmeldelink zur Registrierung des neuen Dienstknotens aus. Dein tailnet kann zusätzlich eine Gerätefreigabe durch einen Administrator verlangen. Öffne anschließend die Dienst-URL auf einem berechtigten Gerät, das in deinem tailnet angemeldet ist. Ein API-Token ist nicht nötig.

Prüfe die Freigaben und entferne dann die Demo:

```bash
tslink status --urls
tslink remove demo
```

Weitere Dinge, die du teilen kannst, sobald ihr Backend läuft:

| Inhalt | Befehl |
|---|---|
| Eine lokale Web-App | `tslink share 3000` |
| Ein Dateiordner | `tslink share ./public --name files` |
| Eine lokale Modell-API, etwa Ollama | `tslink add model --proxy localhost:11434` |
| Eine Datenbank über privates TCP | `tslink add database --tcp localhost:5432` |
| Eine bekannte selbst gehostete App (Jellyfin, Immich, Home Assistant und 13 weitere) | `tslink apps detect`, dann `tslink apps share jellyfin --yes` |

[Einstieg, Plattformen und Hintergrunddienst →](getting-started.md)

## Bestimme, wer Zugriff bekommt

| Zielgruppe | Was Empfänger brauchen | Identität | Ende |
|---|---|---|---|
| **Deine eigenen Geräte** | Anmeldung in deinem tailnet | Verifizierte Tailscale-Anmeldung | Wenn du die App entfernst |
| **Benannte Personen** (privates HTTP/Dateien) | Eine Tailscale-Anmeldung; Außenstehende nehmen eine Einladung pro App an | Verifizierte Tailscale-Anmeldung | Zum gesetzten Termin (`--for 7d`) oder durch `tslink people remove` |
| **Alle mit der URL** (Funnel) | Einen Browser | Beliebige Personen; die Anmeldung der App gilt weiterhin | Standardmäßig nach 24 Stunden (`--funnel-ttl`) |
| **Gastlink für den Browser** *(demnächst)* | Einen Browser und optional eine PIN | Wer den Link besitzt | Bei Ablauf oder Widerruf |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Bei privaten HTTP- und Dateifreigaben wird die Frist bei jeder Anfrage geprüft. Ein Widerruf verhindert neue Anfragen; bereits heruntergeladene Daten lassen sich nicht zurückholen, und angenommene Streams oder WebSocket-Verbindungen werden nicht geschlossen. [Mit Personen teilen →](people.md) · [Grenzen der Freigabe →](sharing.md)

<a id="agents"></a>

## Für Agenten

TSLink enthält einen MCP-Server. Ein Agent kann Freigaben genauso wie du erstellen, auflisten, erklären und entfernen. Füge ihn einem lokalen MCP-Client hinzu:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Genaue Ergebnisse.** CLI-Automatisierung unterstützt `--json` mit `schema_version: 1` und stabilen Fehlercodes; `tslink mcp` verwendet stattdessen JSON-RPC. `tslink manifest` beschreibt jeden Befehl und jede Option. Agenten sollten echte URLs mit `tslink url <name> --wait` abrufen, statt sie zusammenzusetzen.
- **Ehrliche Wartezustände.** Ein neuer Knoten, der noch eine menschliche Anmeldung benötigt, meldet `needs_login`, statt Einsatzbereitschaft vorzutäuschen.
- **Berechtigungen.** Lokales MCP läuft mit deinen Benutzerrechten. Remote-MCP ist ein ausdrücklich aktivierter Endpunkt nur im tailnet, beschränkt auf die aufgeführten Anmeldungen oder Tags. Rollen pro Agent, App-Bereiche und Aktionsbelege kommen *demnächst*.

Das MCP von TSLink steuert TSLink selbst. Wenn du einen anderen MCP-Server über TSLink veröffentlichst, braucht dieser weiterhin eigene Werkzeugberechtigungen.
[Agentenleitfaden →](agents.md) · [MCP-Clients →](mcp-clients.md) · [Remote-MCP →](remote-mcp.md) · [JSON-Automatisierung →](json-automation.md)

## Wann andere Werkzeuge passen

| Dein Ziel | Mögliche Alternative |
|---|---|
| Ein lokaler Dienst auf deinen eigenen Geräten mit dem bereits laufenden Tailscale-Client | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Administrativ verwaltete Dienste mit stabilen Namen über mehrere Hosts hinweg | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Eine öffentliche URL für einen Webhook oder eine API-Demo ohne Tailscale-Konto | [ngrok](https://ngrok.com/docs/start) oder [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Selbst gehostete Apps installieren und betreiben, nicht nur teilen | [Umbrel](https://umbrel.com) oder [Coolify](https://coolify.io) |
| Eine organisationsweite Zugriffsplattform mit Identitätsprüfung | [Pangolin](https://github.com/fosrl/pangolin) oder [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink passt, wenn eine Person mehrere Apps betreibt und zeitlich begrenzten Zugriff pro App und Person möchte, den sie und ihr Agent prüfen können.

## So funktioniert es

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database und Model sind getrennte, benannte Knoten in einem tailnet, betrieben von einem TSLink-Daemon auf dem veröffentlichenden Computer." width="720">
</picture>

Ein Hintergrund-Daemon betreibt für jede App einen eingebetteten Tailscale-Knoten mit eigenem Namen und eigener Adresse. Bei privaten HTTP- und Dateifreigaben regeln `WhoIs` und Personenfreigaben oder `--allow`-Regeln den Zugriff; Personenfristen werden bei jeder Anfrage geprüft. Rohes TCP verwendet die tailnet-Richtlinie und die Authentifizierung des Backends. Tailscale liefert tailnet-Transport, Verschlüsselung und Zertifikate; TSLink ist ein unabhängiges Projekt. Alle Apps teilen sich den veröffentlichenden Computer und werden durch TSLink nicht voneinander isoliert. [Architektur →](architecture.md)

## Stand

Jetzt verfügbar: private Adressen pro App, Personenfreigaben mit Fristen und gebündelten Einladungen, öffentliches Funnel mit Ablaufdatum, App-Gesundheitsprüfungen und Warnungen, Rezepte für selbst gehostete Apps, Anfragegrenzen pro App, automatischer Neustart des Daemons nach Abstürzen unter Windows, CLI und MCP. Ebenfalls verfügbar: Browser-Gastlinks, flexible Laufzeiten, ein Zugriffsprotokoll, eine Startseite deiner Apps, begrenzte Agentenrollen, QR-Einrichtung und Zugriffsanfragen.

Eine gemeinsame Liste für mehrere Computer ist geplant. [Roadmap →](roadmap.md)

## Dokumentation und Lizenz

[Einstieg](getting-started.md) · [CLI-Referenz](cli-reference.md) · [Plattformen](platforms.md) · [Lokale Modelle](local-ai.md) · [Mitwirken](../CONTRIBUTING.md) · [Sicherheit](../SECURITY.md)

Apache License 2.0, einschließlich kommerzieller Nutzung. Bewahre bei Weitergabe [NOTICE](../NOTICE) und die [Drittherstellerhinweise](../THIRD_PARTY_NOTICES.md) auf. Die Bedingungen und Tarife von Tailscale gelten separat.
