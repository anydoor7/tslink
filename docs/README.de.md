<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Private Adressen für deine Apps, in deinem Tailscale-Netzwerk.</strong></p>
<p align="center">Öffne sie auf deinen eigenen Geräten. Teile eine mit einer Person oder per Link, bis zu einem Datum deiner Wahl.</p>
<p align="center"><strong>Deutsch</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Weitere Sprachen</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installation

```sh
brew install --cask anydoor7/tap/tslink
```

Linux-Pakete (`.deb` und `.rpm`) und Windows-Builds findest du im [neuesten Release](https://github.com/anydoor7/tslink/releases/latest). Wenn du zum ersten Mal eine App teilst, gibt TSLink einen Tailscale-Anmeldelink dafür aus. [Erste Schritte](getting-started.md)

<a id="why"></a>

## Wann du TSLink brauchst

Für eine App auf deinen eigenen Geräten reicht Serve. TSLink bündelt App-Adressen, Fristen und Zugriffsänderungen in einem Ablauf.

| Aufgabe | Nur Tailscale | TSLink |
|---|---|---|
| Eine Web-App auf dem Handy | `tailscale serve 3000` reicht | `tslink share 3000` |
| Mehrere Apps, eigene Namen | Services einrichten oder getrennte Knoten | Ein `share`/`add` pro App; jeden Knoten anmelden |
| Eine Person, eine App, sieben Tage | Richtlinienregeln, dann ein JIT-Tool oder manuelles Entfernen | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/Dateien) |
| Browserlink, drei Tage | Öffentlicher Funnel; Zugangsschutz und geplantes Abschalten selbst ergänzen | `tslink guest create photos --for 3d --public --print-link` (nur HTTP) |

Private Empfänger brauchen Tailscale. Gastlinks sind öffentliche, weitergebbare Zugangsdaten.

[Ausführlicher Vergleich](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Deine Apps, auf deinen eigenen Geräten

- **Eine Adresse pro App.** Web-Apps, Ordner, einzelne Dateien und TCP-Ports bekommen jeweils einen eigenen Namen in deinem tailnet. Du nutzt Namen statt IP-Adressen.
- **Standardmäßig privat.** Nichts ist öffentlich, bis du einen Gastlink erstellst oder über Funnel veröffentlichst.
- **Eine Startseite**, die deine Apps mit ihrem Zustand auflistet. [Portal](portal.md)
- **Zustandsprüfungen und Alarme** per Befehl oder Webhook, dazu ein Zugriffsprotokoll, das auch abgelehnte Anfragen zeigt. [Zustand und Alarme](health-and-alerts.md) · [Zugriffshistorie](access-log.md)
- **Rezepte für 15 selbst gehostete Apps**, darunter Home Assistant, Jellyfin, Immich und Ollama. `tslink apps detect` findet die, die schon laufen. [App-Rezepte](apps.md)

## Teilen, wenn du willst

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Gastlinks und neue öffentliche URLs laufen ab und funktionieren nur für Web-Apps; Ordner, Dateien und TCP-Ports bleiben privat. [Personen](people.md) · [Gastlinks](guest-links.md) · [Öffentlicher Zugriff](funnel.md)

<a id="agents"></a>

## Für KI-Agenten

Einen Dev-Server, den ein Agent auf `localhost` startet, erreichst du vom Handy aus nicht. Mit TSLink gibt der Agent ihm eine private Adresse, meldet die genaue URL und entfernt sie danach wieder.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI oder MCP.** Verwaltungsbefehle akzeptieren `--json` und liefern versionierte Ergebnisse; `tslink mcp` bietet App- und Zugriffsoperationen über MCP.
- **Begrenzte Rollen.** `viewer`, `app-operator` oder `people-manager`, beschränkt auf die Apps, die du nennst. `tslink mcp-audit` zeigt, was ein Agent geändert hat. Rollen begrenzen die Werkzeuge von TSLink, nicht die eigene Shell des Agenten.

[Agenten-Schnellstart](agent-quickstart.md) · [MCP-Berechtigungen](mcp-scopes.md) · [Remote-MCP](remote-mcp.md)

<a id="architecture"></a>

## So funktioniert es

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Ein PC oder Cloud-Host: CLI/MCP verwaltet einen gemeinsamen Daemon und Knoten je App. Private Geräte nutzen verschlüsselten Tailscale-Transport; optionales öffentliches HTTPS/Funnel erreicht HTTP-Apps über eine Gastprüfung oder ausdrücklich offene Freigabe." width="960">
</picture>

Ein Hintergrundprozess betreibt für jede App einen eigenen Tailscale-Knoten. Tailscale liefert den Transport im tailnet und die HTTPS-Zertifikate. Privater Web- und Dateizugriff lässt sich mit `--allow` und Personenfreigaben nach Tailscale-Identität einschränken; rohes TCP verlässt sich auf deine tailnet-Richtlinie und den eigenen Login der App. [Architektur](architecture.md)

<a id="requirements"></a>

## Voraussetzungen

| Wer | Braucht |
|---|---|
| Du | Ein Tailscale-Konto mit aktiviertem MagicDNS und HTTPS |
| Der Rechner, auf dem deine Apps laufen | TSLink, das Tailscale mitbringt (unter Linux eine systemd-Benutzersitzung) |
| Deine Geräte und Personen, mit denen du teilst | Die Tailscale-App |
| Gäste | Einen Browser |

Namen von HTTPS-Apps erscheinen in öffentlichen Zertifikatslogs, wähle also Namen, die andere sehen dürfen.

<a id="documentation"></a>

## Mehr

[Alle Dokumente](INDEX.md) · [CLI-Referenz](cli-reference.md) · [Vergleich mit Serve, ngrok und Cloudflare](comparison.md) · [Mitwirken](../CONTRIBUTING.md) · [Sicherheit](../SECURITY.md)

Apache 2.0. TSLink ist ein unabhängiges Projekt und wird von Tailscale weder entwickelt noch unterstützt.
