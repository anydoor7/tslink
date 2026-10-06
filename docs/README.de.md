<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Teile deine selbst gehosteten Apps mit den Menschen deiner Wahl, so lange du willst.</strong></p>

TSLink gibt jeder App auf deinem Computer oder Server eine eigene private Tailscale-Adresse. Gib ausgewählten Personen Zugriff bis zu einer Frist, schicke jemandem ohne Tailscale einen Browser-Gastlink und widerrufe jeden davon mit einem einzigen Befehl. Erledige das selbst oder über einen KI-Agenten, der auf die von dir zugewiesene Rolle beschränkt ist. Unabhängiges Projekt, das mit Tailscale arbeitet.

<p align="center"><a href="#quickstart">Schnellstart</a> · <a href="#agents">Für Agenten</a> · <a href="comparison.md">Vergleich mit Serve, ngrok und Cloudflare</a> · <a href="#documentation">Dokumentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <strong>Deutsch</strong> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Deine Apps in Reichweite

| Dein Bedarf | Das bietet TSLink |
|---|---|
| Eigene Apps geräteübergreifend nutzen | Private Adressen für Heim-Dashboards, nur lokal erreichbare Webseiten, Dateien, Modell-APIs und TCP-Dienste auf PC oder Server. |
| Mit bestimmten Personen teilen | Ausgewählte HTTP-/Datei-Apps, geprüfte Tailscale-Anmeldungen, Ablauf und Widerruf. Empfänger brauchen Tailscale. [Personen](people.md) |
| Besuch per Browser ermöglichen | Befristete Gastlinks mit optionaler PIN für HTTP-Proxy-Apps oder ausdrücklich öffentliches HTTPS über Funnel. Links sind weiterleitbar und kein Identitätsnachweis. [Gastlinks](guest-links.md) |
| Eine App-Sammlung verwalten | Inventar je Host, privates Portal, Zustandsprüfungen und Alarme, Zugriffshistorie sowie CLI/MCP-Zugriffsverwaltung mit Agentenrollen, App-Grenzen und Auditbelegen. [Portal](portal.md) · [MCP-Berechtigungen](mcp-scopes.md) |

[App-Rezepte](apps.md), [Upload-Limits](sharing.md), [flexible Laufzeiten](durations.md) und [QR-Einstieg und Zugriffsanfragen](requests.md) erleichtern den Alltag. Diese Funktionen sind Teil von v0.1.0.

<a id="installation"></a>
<a id="quickstart"></a>

## Schnellstart

Unter macOS und Linux installierst du TSLink mit Homebrew. Die macOS-Binärdatei ist mit einem Developer-ID-Zertifikat signiert und von Apple notarisiert. Für spätere Updates führst du `brew upgrade --cask tslink` aus und danach erneut `tslink install`, falls TSLink als Hintergrunddienst läuft.

```bash
brew install --cask anydoor7/tap/tslink
```

Unter Windows lädst du `tslink_<version>_windows_<arch>.zip` aus dem [neuesten Release](https://github.com/anydoor7/tslink/releases/latest) herunter, prüfst die Datei mit `checksums.txt` und führst `tslink install` aus, damit TSLink bei der Anmeldung startet. Das Zip ist nicht Authenticode-signiert; [prüfe das Release](verify-release.md) anhand der signierten Prüfsummen und Attestierungen. Linux-Pakete im Format `.deb` und `.rpm` liegen auf derselben Release-Seite. Für einen Build aus dem Quellcode brauchst du **Git und Go 1.26.6+**. Die Befehle unten verwenden bash/zsh; siehe [Einrichtung für macOS, Linux und Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Du brauchst ein **Tailscale-Konto** sowie [MagicDNS und HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Private Empfangsgeräte brauchen Tailscale und eine passende Netzwerkfreigabe. Auf dem App-Host ist Tailscale in TSLink eingebettet.

Wenn deine App bereits auf Port 3000 läuft:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Wähle einen freien Namen. Gibt `share` einen anderen zurück, verwende diesen bei `url`. Schließe zuerst die angezeigte Browser-Anmeldung und Gerätefreigabe ab und öffne dann die exakte App-URL auf einem erlaubten Gerät. `share` startet bei Bedarf den Hintergrunddienst. Dieser private Einstieg benötigt kein Admin-API-Token. Dateien teilst du mit `tslink share ./report.html`; Apps müssen laufen, Dateien existieren. [Vollständige Einrichtung](getting-started.md)

Wenn es dir hilft, kannst du [TSLink einen Stern geben](https://github.com/anydoor7/tslink), damit andere es entdecken. Das ist völlig freiwillig.

<a id="architecture"></a>

## So greift alles ineinander

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Ein PC oder Cloud-Host: CLI/MCP verwaltet einen gemeinsamen Daemon und Knoten je App. Private Geräte nutzen verschlüsselten Tailscale-Transport; optionales öffentliches HTTPS/Funnel erreicht HTTP-Apps über eine Gastprüfung oder ausdrücklich offene Freigabe." width="960">
</picture>

Stell dir einen privaten, verschlüsselten Weg zu deinen Apps vor. **Tailscale liefert Netzwerktransport und HTTPS; TSLink verwaltet den App-Zugriff je Host.** Ein Daemon betreibt pro Dienst einen eigenen eingebetteten Knoten. Das private Portal zeigt erlaubte Apps; Zustand und Zugriffshistorie helfen bei der Pflege.

Öffentlicher Zugriff ist optional: Gäste brauchen Link und gegebenenfalls PIN; offenes Funnel ist für jeden mit der URL erreichbar. Beide nutzen öffentliches HTTPS statt privater Benutzeridentität. Rohes TCP bleibt privat und benötigt Tailnet-Regeln sowie Backend-Authentifizierung. TSLink installiert keine Apps, isoliert keine Host-Prozesse, erstellt keine Cloud-VPC und fasst keine Hosts zusammen. Unabhängiges Projekt, das mit Tailscale arbeitet. [Architektur und Grenzen](architecture.md)

<a id="agents"></a>

## Für Agenten

Verwalte Inventar, Zustand, URLs und Zugriff per CLI/MCP. Beginne beim [Agenten-Schnellstart](agent-quickstart.md), lies die aktuellen Tool-Schemas und prüfe den tatsächlichen App-Zugriff, bevor du Erfolg meldest.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI-Automatisierung nutzt `--json`, MCP nutzt JSON-RPC über stdio. [Clients](mcp-clients.md) · [Remote-MCP](remote-mcp.md) · [Rollen und App-Grenzen](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Dokumentation und Lizenz

[Alle Anleitungen](INDEX.md) · [CLI-Referenz](cli-reference.md) · [Lokale KI](local-ai.md) · [Zustand](health-and-alerts.md) · [Zugriffshistorie](access-log.md) · [Roadmap](roadmap.md)

Ein hostübergreifendes Inventar ist geplant. [Beiträge](../CONTRIBUTING.md) und [Sicherheitsmeldungen](../SECURITY.md) sind willkommen. [Apache 2.0](../LICENSE) erlaubt kommerzielle Nutzung; [NOTICE](../NOTICE) und [Drittanbieterhinweise](../THIRD_PARTY_NOTICES.md) müssen bei Weitergabe erhalten bleiben. [Kommerzielle Zusammenarbeit](../COMMERCIAL.md) ist freiwillig. Tailscale-Bedingungen und -Tarife gelten separat.
