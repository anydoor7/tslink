<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink ist eine Open-Source-CLI und ein MCP-Server, der jeder App auf deinem Computer eine private Adresse <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> gibt, damit du oder dein Coding-Agent einen laufenden Dev-Server auf dem Handy öffnen könnt. Unabhängiges Projekt, nicht von Tailscale.</p>
<p align="center">Dein Handy braucht die Tailscale-App, angemeldet im selben Tailnet.</p>
<p align="center"><strong>Deutsch</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Weitere Sprachen</a></p>

```text
# Claude Code, nach der Installation der CLI (siehe Installation unten)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Lokale MCP-Clients, die mcpServers-JSON verwenden (andere: siehe die MCP-Einrichtung deines Clients)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Sag deinem Agenten: „Teile den Dev-Server, damit ich ihn auf dem Handy öffnen kann."
```

Dasselbe von Hand, wobei der Dev-Server bereits auf Port 5173 lauscht. Mit `--json` meldet `share` den registrierten Namen in `data.name`. Verwende diesen Namen in den folgenden Befehlen: Er weicht von `--name` ab, wenn dieser Name schon vergeben war, zum Beispiel `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, oder data.auth_url (ein Tailscale-Anmeldelink), wenn status needs_login ist
tslink url <name> --wait              # nach der Anmeldung: die genaue URL
tslink remove <name>                  # wenn du fertig bist; entfällt, wenn share fehlgeschlagen ist. Der Dev-Server läuft weiter
```

<a id="installation"></a>
<a id="quickstart"></a>

## Installation

macOS-Hosts benötigen macOS 13 Ventura oder neuer ([Plattformunterstützung](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Unter Windows mit Scoop installieren:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Zum Aktualisieren `brew upgrade --cask tslink` oder `scoop update; scoop update tslink` ausführen. Wenn TSLink als Hintergrunddienst läuft, danach erneut `tslink install` ausführen.

Linux-Pakete (`.deb` und `.rpm`) und Windows-Builds findest du im [neuesten Release](https://github.com/anydoor7/tslink/releases/latest). Wenn du zum ersten Mal eine App teilst, gibt TSLink einen Tailscale-Anmeldelink dafür aus. [Erste Schritte](getting-started.md)

<a id="agents"></a>

## Für KI-Agenten

Einen Dev-Server, den ein Agent auf `localhost` startet, erreichst du vom Handy aus nicht. Mit TSLink gibt der Agent ihm eine private Adresse, übergibt dir die genaue URL und entfernt die Freigabe danach wieder. Dein Handy braucht die Tailscale-App, angemeldet im selben Tailnet.

Getestet am 2026-10-22 mit Vite 8.3.5 und einem iPhone 15 Pro: Der Agent hat den Dev-Server über MCP freigegeben, das Handy hat die private URL geöffnet, und die Seite wurde beim Speichern ohne manuelles Neuladen aktualisiert.

Für Claude Code installierst du zuerst die CLI und führst dann aus:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Das Plugin enthält einen [Vorschau-Skill](../skills/tslink-preview/SKILL.md) und die MCP-Verbindung weiter unten. Der Marketplace liegt in diesem Repository und holt das Plugin von [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Der maßgebliche Skill bleibt hier; das Plugin enthält eine per CI geprüfte Kopie. Andere Agenten können denselben Skill nutzen oder MCP direkt verbinden. Diese Integration braucht die lokale CLI und läuft nicht im claude.ai-Chat. [Einrichtung und Anmeldung](agent-integrations.md)

Für einen lokalen MCP-Client, der `mcpServers`-JSON verwendet (andere Clients: siehe deren MCP-Einrichtungsanleitung und die [Anleitung für MCP-Clients](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Gateway als Standard bei Neuinstallationen.** Private HTTP-/Datei-Apps erhalten stabile Ports, etwa `https://home.<tailnet>.ts.net:8443/`. Bestehende Installationen behalten ihren Modus; App-Knoten und eigene URLs bleiben erhalten. `--portal-only` wird ausdrücklich gewählt.
- **Gateway anmelden.** Ohne Zugangsdaten kann das verifizierte eigene Konto eines ungetaggten Gateways owner werden. Bei Tags/OAuth lokal `tslink portal enable --owner <login>` ausführen. Eine ausstehende App-Knoten-Anmeldung blockiert keine bereits bereite Gateway-Route.
- **Zugriff pro Person.** Neue Gateway-Apps erlauben owner/admin und ausdrücklich berechtigte Personen. `people add`, `add --allow` und MCP `add/share.allow` prüfen die Mitgliedschaft und bereiten nötige Einladungen vor. Mitglieder brauchen keine Geräte-Einladung; externe Personen nehmen eine Gateway-Einladung an. Verzeichnis-/API-Fehler melden eine unvollständige Einrichtung.
- **Ändern, speichern, sehen.** Beim Speichern des Codes aktualisiert sich die Seite auf deinem Handy. Die Host- und Origin-Regeln deiner App können Anfragen weiterhin blockieren: In unserem Test brauchte Vite keine Änderung an `allowedHosts`; Next.js ist ungetestet und braucht weiterhin `allowedDevOrigins`.
- **Aufräumen.** Der Agent ruft `unshare` auf, oder du führst `tslink remove <name>` aus, jeweils mit dem Namen, den das share-Ergebnis zurückgegeben hat. Das Entfernen hebt die Registrierung der Freigabe auf und versucht, ihren Tailnet-Knoten zu löschen, wenn TSLink nachweisen kann, dass er ihm gehört; prüfe daher im Ergebnis die Felder zu Bereinigung oder beibehaltenem Zustand. Der Dev-Server wird dabei nicht gestoppt.
- **CLI oder MCP.** Verwaltungsbefehle akzeptieren `--json` und liefern versionierte Ergebnisse; `tslink mcp` bietet App- und Zugriffsoperationen über MCP.
- **Eine Datei, nicht der Ordner.** Ein Agent kann mit `tslink share ./report.html` nur seinen in sich geschlossenen HTML-Bericht teilen; die übrigen Dateien in diesem Ordner bleiben unerreichbar.
- **Begrenzte Rollen.** `viewer`, `app-operator` oder `people-manager`, beschränkt auf die Apps, die du nennst; eine neue App zu teilen erfordert die Rolle `owner`. `tslink mcp-audit` zeigt, was ein Agent geändert hat. Rollen begrenzen die Werkzeuge von TSLink, nicht die eigene Shell des Agenten.

[Agenten-Schnellstart](agent-quickstart.md) · [MCP-Berechtigungen](mcp-scopes.md) · [Remote-MCP](remote-mcp.md)

<a id="why"></a>

## Wann du TSLink brauchst

Serve reicht für eine App, die du selbst startest und auf deinem eigenen Handy öffnest. TSLink ist für die Dev-Server gedacht, die ein Agent startet, oft mehrere, wenn jeder einen eigenen privaten Namen bekommen soll und Teilen, URL und Aufräumen als fertige Schritte in der CLI, in MCP und in einem Claude-Code-Skill bereitstehen sollen.

| Aufgabe | Nur Tailscale | TSLink |
|---|---|---|
| Mehrere Dev-Server, die dein Agent startet, je ein Name | Serve legt geräteweite Routen unter einem Namen pro Rechner an; Services verlangt für jeden Namen eine Definition, eine Freigabe und einen getaggten Host | Ein Knoten pro App, mit Teilen, URL-Abfrage und Aufräumen als fertige Schritte in der CLI (`share`, `url`, `remove`), in MCP (`share`, `url`, `unshare`) und in einem Skill |
| Eine App, die du selbst startest, auf dem Handy | `tailscale serve 3000` reicht | `tslink share 3000` |
| Eine Person, eine App, sieben Tage | Richtlinienregeln, dann ein JIT-Tool oder manuelles Entfernen | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/Dateien) |
| Browserlink, drei Tage | Öffentlicher Funnel; Zugangsschutz und geplantes Abschalten selbst ergänzen | `tslink guest create photos --for 3d --public --print-link` (nur HTTP) |

Private Empfänger brauchen Tailscale. Gastlinks sind öffentliche, weitergebbare Zugangsdaten.

[Ausführlicher Vergleich](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Deine Apps, auf deinen eigenen Geräten

- **Eine Adresse pro App.** Web-Apps, Ordner, einzelne Dateien und TCP-Ports bekommen jeweils einen eigenen Namen in deinem tailnet. Du nutzt Namen statt IP-Adressen.
- **Standardmäßig privat.** Nichts ist öffentlich, bis du einen Gastlink erstellst oder über Funnel veröffentlichst.
- **Eine App, nicht der ganze Rechner.** Jede App, die du veröffentlichst, bekommt einen eigenen Knoten, der nur an diese App weiterleitet. Ohne die Tailscale-App auf dem Host fügt TSLink deinem tailnet keine weiteren Host-Ports hinzu.
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
