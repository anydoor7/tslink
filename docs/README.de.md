<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Gib jeder App auf deinem Computer oder Server eine eigene private Adresse in deinem Tailscale-Netzwerk und entscheide, wer sie erreichen kann.</strong></p>

Öffne deine Web-Apps, Ordner, Modell-APIs und Datenbanken von deinem eigenen Handy und Laptop aus, mit Zustandsprüfung und Zugriffshistorie für jede App. Auch deine KI-Agenten können sie veröffentlichen und prüfen, im Rahmen der Rolle, die du ihnen gibst. Braucht jemand anderes Zugang, gib einer bestimmten Person Zugriff bis zu einem Datum oder stelle eine Web-App für begrenzte Zeit ins öffentliche Internet.

**Tailscale ist Voraussetzung.** Du brauchst ein Tailscale-Konto (für private Nutzung kostenlos), und jedes Gerät, das eine private App öffnet, braucht die Tailscale-App; Gäste und öffentliche Besucher brauchen nur einen Browser. TSLink ist ein unabhängiges Projekt, weder von Tailscale entwickelt noch von Tailscale befürwortet. [Voraussetzungen](#requirements)

<p align="center"><a href="#quickstart">Schnellstart</a> · <a href="#agents">Für Agenten</a> · <a href="comparison.md">Vergleich mit Serve, ngrok und Cloudflare</a> · <a href="#documentation">Dokumentation</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <strong>Deutsch</strong> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Was du damit machen kannst

### Deine eigenen Apps erreichen

- **Eine Adresse pro App.** `tslink share 3000`, `tslink share ./photos` oder `tslink add db --tcp localhost:5432` gibt einer Web-App, einem Ordner, einer Datei oder einem TCP-Dienst eine eigene private Adresse in deinem Tailnet (deinem privaten Tailscale-Netzwerk), etwa `https://photos.<tailnet>.ts.net`. Jede App ist ein eigenes Tailscale-Gerät, daher öffnest du Apps über ihren Namen statt über IP-Adresse und Port.
- **Privat, solange du nichts anderes wählst.** Apps bleiben in deinem Tailnet, und dessen Richtlinie legt fest, welche Geräte sich verbinden dürfen. Nichts gelangt ins öffentliche Internet, bevor du einen Gastlink erstellst oder eine App veröffentlichst.
- **Alles an einem Ort.** `tslink status --urls` listet jede App auf diesem Computer auf, und eine optionale private Startseite zeigt Adresse und Zustand jeder App. [Portal](portal.md)
- **Merken, wenn etwas ausfällt.** Zustandsprüfungen im Hintergrund können dich per Befehl oder Webhook benachrichtigen, wenn eine App ausfällt oder wieder läuft oder ihre Tailscale-Anmeldung bald abläuft. Die Zugriffshistorie zeigt, wer wann welche App geöffnet hat, einschließlich abgelehnter Anfragen. [Zustand und Alarme](health-and-alerts.md) · [Zugriffshistorie](access-log.md)
- **Gängige Apps sofort startklar.** Rezepte decken 15 selbst gehostete Apps ab, darunter Home Assistant, Jellyfin, Immich und Ollama, und `tslink apps detect` findet Apps, die bereits laufen. Foto- und Video-Apps bekommen [Upload-Limits](sharing.md), die zu großen Dateien passen. [App-Rezepte](apps.md) · [Lokale KI](local-ai.md)

### Lass deine Agenten damit arbeiten

Ein Agent, der einen Entwicklungsserver, eine Vorschau oder eine lokale Modell-API startet, lässt sie auf `localhost` liegen, wo dein Handy und deine anderen Computer sie nicht öffnen können. Mit TSLink kann der Agent sie privat veröffentlichen, dir die genaue Adresse nennen und sie wieder abbauen, innerhalb der Grenzen, die du setzt.

- **Teilen, prüfen, rückgängig machen.** `share` gibt den registrierten Namen zurück und entweder die genaue URL oder einen Anmeldelink, den du öffnest. `url --wait` und `status` melden, wann die App erreichbar ist, und `remove` (in MCP `unshare`) nimmt sie wieder vom Netz. [Agenten-Schnellstart](agent-quickstart.md)
- **Für Automatisierung gebaut.** Befehle akzeptieren `--json` und liefern ein versioniertes Ergebnis mit stabilen Fehlercodes. `tslink mcp` bietet dieselben Operationen einem lokalen MCP-Client an, `tslink serve --mcp` den Agenten auf deinen anderen Geräten über das Tailnet. [JSON-Automatisierung](json-automation.md) · [Remote-MCP](remote-mcp.md)
- **Begrenzte Befugnisse.** Ein Agent, den du selbst ausführst, handelt als Eigentümer. Anderen Agenten gibst du eine eingeschränkte Rolle (`viewer`, `app-operator` oder `people-manager`), die nur die von dir genannten Apps umfasst und begrenzt, wie lange von ihm erteilte Freigaben gelten dürfen. Änderungen über MCP werden protokolliert, und `tslink mcp-audit` zeigt sie an. Rollen begrenzen die Werkzeuge von TSLink, nicht die eigene Shell oder die Dateien des Agenten. [MCP-Berechtigungen](mcp-scopes.md)

### Mit Menschen deiner Wahl teilen

- **Bestimmte Personen, bis zu einem Datum.** `tslink people add alice@example.com --apps photos,notes --for 7d` erlaubt dieser Tailscale-Anmeldung, die genannten Web- und Datei-Apps bis zur Frist zu öffnen. `people update`, `extend` und `people remove` ändern oder beenden den Zugriff; nach dem Entfernen wird die nächste Anfrage abgelehnt, bereits Heruntergeladenes lässt sich aber nicht zurückholen. [Personen](people.md) · [Laufzeiten](durations.md)
- **Jemand außerhalb deines Tailnets.** Mit `--invite --print-links` bekommst du eine versandfertige Nachricht mit einer Geräteeinladung pro App (dafür ist ein nutzereigenes API-Token nötig). `--qr` gibt einen Code für die Einrichtung am Handy aus.
- **Anfragen.** Personen in deinem Tailnet können auf der Startseite mehr Zeit anfragen oder Zugriff auf eine App, die du als anfragbar markiert hast. Du genehmigst mit einer Laufzeit in einem Befehl. [Zugriffsanfragen](requests.md)

### Eine Web-App zeitweise ins Internet stellen

- **Gastlinks.** `tslink guest create photos --for 3d --public --print-link` erstellt einen Browserlink zu einer Web-App, optional mit PIN, den du einzeln widerrufen kannst. Gäste brauchen kein Tailscale-Konto. Jeder, der den Link hat, kann ihn nutzen; er belegt also nicht, wer zu Besuch war. [Gastlinks](guest-links.md)
- **Eine offene öffentliche URL.** `tslink add preview --proxy localhost:3000 --funnel --public` veröffentlicht eine Web-App für alle, die ihre URL kennen. Sie läuft nach 24 Stunden ab, sofern du mit `--funnel-ttl` keine andere Laufzeit setzt. [Funnel](funnel.md)
- Beide laufen über Tailscale Funnel, laufen immer ab (1 Stunde bis 7 Tage, sofern du das Limit nicht anhebst) und funktionieren nur für Web-Apps. Ordner, Dateien und TCP-Dienste bleiben privat.

Alles oben Genannte ist in v0.1.0 enthalten.

<a id="requirements"></a>

## Voraussetzungen

TSLink baut auf Tailscale auf. Es ist ein unabhängiges Projekt, weder von Tailscale entwickelt noch von Tailscale befürwortet, und es gelten die Bedingungen und [Tarife](https://tailscale.com/pricing) von Tailscale.

| Wer | Was nötig ist |
|---|---|
| Du | Ein Tailscale-Konto mit eingeschaltetem [MagicDNS und HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Der kostenlose Personal-Tarif ist für nicht kommerzielle Nutzung gedacht. |
| Der Computer oder Server, auf dem deine Apps laufen | Nur TSLink. Tailscale ist enthalten, eine separate Installation entfällt. Jede neue App fragt nach einer Anmeldung im Browser und nach einer Gerätefreigabe, falls dein Tailnet sie verlangt. |
| Deine anderen Geräte | Die Tailscale-App, angemeldet in deinem Tailnet. |
| Personen deiner Wahl | Die Tailscale-App und ihre eigene Anmeldung. Sie treten entweder deinem Tailnet bei, was deinem Tarif einen Nutzer hinzufügt, oder nehmen pro App eine Geräteeinladung an. Deine Tailnet-Richtlinie muss ihnen den Zugriff erlauben. |
| Gäste und öffentliche Besucher | Ein Browser. Dein Tailnet muss Funnel erlauben, das Tailscale noch als Beta führt. |

Wenn du HTTPS einschaltest, erscheinen dein Tailnet-Name und deine Gerätenamen, einschließlich des Namens jeder App, in einem öffentlichen Zertifikatslog. Wähle also App-Namen, die jeder sehen darf.

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
