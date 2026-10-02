<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink-Logo">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Gib deinen lokalen Apps, Modellen und Dateien eine eigene private Adresse.</strong><br>
  Öffne sie auf einem anderen berechtigten Gerät in deinem Tailscale-Netzwerk.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Lizenz: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 oder neuer"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: eingebettete tsnet-Knoten"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 Werkzeuge"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <strong>Deutsch</strong><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Installation

Du brauchst **Go 1.26.6 oder neuer** und Git. Fertige Releases und ein Homebrew-Cask wurden noch nicht veröffentlicht; installiere aus dem Quellcode. Diese Beispiele verwenden **bash oder zsh**; siehe [Plattformunterstützung](platforms.md) für die Anforderungen unter Windows und an Hintergrunddienste.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Verwende ein Tailscale-Konto mit [aktiviertem MagicDNS und HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Das empfangende Gerät muss in deinem Tailscale-Netzwerk (**Tailnet**) angemeldet sein und gemäß dessen Regeln auf den Dienst zugreifen dürfen. TSLink bettet Tailscale auf dem veröffentlichenden Rechner ein.

### Deine erste Seite teilen

Erstelle eine Seite; TSLink liefert sie direkt aus und startet bei Bedarf seinen Hintergrunddienst:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Wenn TSLink eine Registrierungs-URL ausgibt, öffne sie, um den Knoten zu autorisieren. Dein Tailnet kann zusätzlich eine Gerätefreigabe durch einen Administrator verlangen. Rufe anschließend die genaue Adresse ab:

```bash
tslink url demo --wait
```

Öffne diese URL auf einem berechtigten Gerät. Für diese erste Freigabe ist kein API-Token nötig.
[Vollständige Einrichtung und Lebenszyklus →](getting-started.md)

<a id="use-cases"></a>

## Was möchtest du teilen?

Dateien müssen vorhanden sein; Apps, Datenbanken und Modell-Backends müssen bereits auf den angegebenen Ports laufen.

| Anwendung | Befehl |
|---|---|
| Eine lokale App auf einem anderen Gerät öffnen | `tslink share 3000` |
| Ein Dateiverzeichnis durchsuchen | `tslink share ./public --name files` |
| Einen erzeugten HTML-Bericht auf dem Smartphone lesen | `tslink share ./report.html --name report` |
| Über TCP auf eine lokale Datenbank zugreifen | `tslink add database --tcp localhost:5432` |
| Eine lokale HTTP-Modell-API wie Ollama nutzen | `tslink add model --proxy localhost:11434` |

Rufe für Ollama die genaue URL mit `tslink url model --wait` ab. Die `baseURL` eines OpenAI-kompatiblen Clients verwendet diese URL mit angehängtem `/v1`. [Lokale Modelle und Abläufe mit privaten Daten →](local-ai.md)

Für mehrere Apps auf einem Host bündelt TSLink benannte Dienstknoten, HTTP-Freigabelisten nach Identität, Funnel-Ablaufzeiten und MCP-Verwaltung. Für eine App auf deinen eigenen Geräten kann [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) ausreichen.

<a id="architecture"></a>

## Architektur

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Beispielhafte Dienstübersicht: App, Docs, Database und Model sind getrennte benannte Knoten im selben Tailnet. Apps, Dateien und Modell-APIs verwenden HTTPS; die Datenbank verwendet privates TCP." width="960">
</picture>

**Ein Tailnet, getrennte Dienstknoten.** Ein gemeinsamer Daemon betreibt pro Dienst einen eingebetteten tsnet-Knoten, der HTTP weiterleitet, Dateien ausliefert oder TCP als Proxy überträgt. Änderungen am Register werden im laufenden Betrieb wirksam. Jeder Knoten hat seine eigene Netzwerkidentität; die Dienste teilen sich den veröffentlichenden Rechner.
[Architektur im Detail →](architecture.md)

| Baustein | Aufgabe |
|---|---|
| [Go](../go.mod) | Natives Kommandozeilenprogramm |
| [Tailscale tsnet](architecture.md) | Dienstknoten und Tailnet-Transport |
| [Cobra](https://github.com/spf13/cobra) | Befehle und Hilfe |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transporte für Agenten |
| Schlüsselbund des Betriebssystems und Benutzerdienstmanager | Optionale Zugangsdaten und Hintergrundbetrieb |

Dienste bleiben in deinem Tailnet, solange du nicht ausdrücklich [öffentliches Funnel](getting-started.md#more-examples) aktivierst. HTTP- und Dateidienste unterstützen Zugriffslisten anhand von Identitäten (`WhoIs`, `--allow`); TCP verwendet die Tailnet-Regeln und die eigene Authentifizierung des Backends. Siehe [Freigabegrenzen](sharing.md).

TSLink installiert keine Apps, führt keine Modelle aus, isoliert keine Hostprozesse und fasst keine mehreren Hosts zusammen. Netzwerk, Verschlüsselung und HTTPS stammen von Tailscale; TSLink ist ein unabhängiges Projekt.

<a id="agents"></a>

## Für Agenten

Die **19 MCP-Werkzeuge** ermöglichen einem Agenten, Berichte zu teilen, Dienste zu verwalten, URLs abzurufen und die Einrichtung zu prüfen. Verbinde einen lokalen MCP-Client mit dem installierten Programm:

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

MCP verwaltet TSLink; Anwendungen verwenden für die Inferenz die HTTP-Modell-API. Siehe [MCP-Clients](mcp-clients.md), [entferntes MCP](remote-mcp.md) und den [Betriebsleitfaden für Agenten](../AGENTS.md) für Konfiguration und Automatisierung.

Die CLI unterstützt `--json` mit `schema_version` auf `1`; nutze `tslink status --urls --json`. Lokales MCP verwendet JSON-RPC über stdio. Siehe [JSON-Automatisierung](json-automation.md).

<a id="roadmap"></a>

## Demnächst

Einträge mit Wird zusammengeführt, In Prüfung oder Geplant sind in der obigen Quellinstallation nicht enthalten.

| Anwendungsfall | Status |
|---|---|
| F1. Gib Angehörigen 3 Tage Zugriff auf private HTTP-/Datei-Apps und bündle die App-Einladungen in einer Nachricht; Empfänger brauchen weiterhin Tailscale. | Wird zusammengeführt |
| F2. Prüfe den App-Zustand und erhalte Ausfall- oder Ablaufwarnungen über einen optionalen Befehl oder Webhook. | Wird zusammengeführt |
| F3. Finde unterstützte Loopback-Apps und prüfe Rezepte für selbst gehostete Apps vor der Freigabe. | Wird zusammengeführt |
| F8. Lege Upload-Größe und Anfragezeitlimits je HTTP-App für große Uploads und langsame Clients fest. | Wird zusammengeführt |
| F9. Starte einen abgestürzten Windows-Daemon während der Anmeldung über eine geplante Aufgabe und einen eingebauten Supervisor neu. | Wird zusammengeführt |
| F4. Sieh in lokalen Zugriffsprotokollen, wer welche App geöffnet hat, mit den Pfadmodi `prefix`, `full` oder `off`. | In Prüfung |
| F5. Öffne eine Startseite mit erlaubten Apps und Registrierungshinweisen für Eigentümer; Besucher brauchen weiterhin Tailscale. | In Prüfung |
| F6. Gib einem Agenten eine Rolle und einen App-Bereich mit Prüfbelegen für seine Änderungen. | In Prüfung |
| F10. Lass Gäste eine HTTP-App im Browser ohne Tailscale-Installation über einen befristeten Link und eine optionale PIN durch zugangsgeschütztes öffentliches Funnel öffnen. | In Prüfung |
| F11. Wähle Vorgaben oder eigene Laufzeiten ab 1 Stunde, mit standardmäßig höchstens 7 Tagen für Gäste, konfigurierbar. | In Prüfung |
| F12. Hilf Handynutzern beim Beitritt per QR-Code und genehmige App-Zugriff oder mehr Zeit in einem Schritt. | In Prüfung |
| F7. Sieh Apps mehrerer Hosts in einem Verzeichnis. | Geplant |

<a id="documentation"></a>

## Dokumentation und Lizenz

[Erste Schritte](getting-started.md) · [Lokale Modelle](local-ai.md) ·
[CLI-Referenz](cli-reference.md) · [Plattformen](platforms.md) · [Roadmap](roadmap.md)

Für Beiträge siehe [CONTRIBUTING.md](../CONTRIBUTING.md); melde Sicherheitslücken gemäß [SECURITY.md](../SECURITY.md).

TSLink verwendet die unveränderte [Apache-Lizenz 2.0](../LICENSE), einschließlich kommerzieller Nutzung. Bewahre bei der Weiterverteilung die geltenden [NOTICE](../NOTICE)-Angaben und [Hinweise zu Drittanbietern](../THIRD_PARTY_NOTICES.md). Die [kommerzielle Zusammenarbeit](../COMMERCIAL.md) ist freiwillig und fügt keine Lizenzbedingung hinzu. Die Tailscale-Nutzungsbedingungen und -Tarife gelten unabhängig davon.
