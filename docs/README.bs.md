<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Dijelite svoje self-hosted aplikacije s osobama koje izaberete, onoliko dugo koliko želite.</strong></p>

TSLink svakoj aplikaciji na vašem računaru ili serveru daje vlastitu privatnu Tailscale adresu. Dajte odabranim osobama pristup do određenog roka, pošaljite link za goste u pregledniku nekome ko ne koristi Tailscale i opozovite jedno ili drugo jednom naredbom. Uradite to sami ili putem AI agenta ograničenog na ulogu koju mu dodijelite. Nezavisan projekat koji radi s Tailscaleom.

<p align="center"><a href="#quickstart">Brzi početak</a> · <a href="#agents">Za agente</a> · <a href="comparison.md">Poređenje sa Serve, ngrok i Cloudflare</a> · <a href="#documentation">Dokumentacija</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <strong>Bosanski</strong> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Vaše aplikacije nadohvat ruke

| Šta vam treba | Šta TSLink nudi |
|---|---|
| Korištenje vlastitih aplikacija na više uređaja | Privatne adrese za kućne kontrolne ploče, lokalne web stranice, datoteke, API-je modela i TCP servise na računaru ili serveru. |
| Dijeljenje s određenim osobama | Odabrane HTTP/datotečne aplikacije, provjeren Tailscale identitet, rok i opoziv pristupa. Primaoci koriste Tailscale. [Osobe](people.md) |
| Posjeta kroz preglednik | Vremenski ograničeni linkovi s opcionalnim PIN-om za HTTP proxy aplikacije ili izričito javni HTTPS putem Funnela. Linkovi se mogu proslijediti i ne potvrđuju identitet. [Gosti](guest-links.md) |
| Upravljanje skupom aplikacija | Popis po hostu, privatni portal, provjere stanja i upozorenja, historija pristupa te CLI/MCP upravljanje s ulogama agenata, ograničenjem na aplikacije i revizijskim zapisima. [Portal](portal.md) · [MCP ovlasti](mcp-scopes.md) |

[Recepti za aplikacije](apps.md), [ograničenja slanja](sharing.md), [fleksibilno trajanje](durations.md) i [QR upute i zahtjevi za pristup](requests.md) olakšavaju održavanje. Ove mogućnosti dolaze s verzijom v0.1.0.

<a id="installation"></a>
<a id="quickstart"></a>

## Brzi početak

Na macOS-u i Linuxu instalirajte putem Homebrewa. Binarna datoteka za macOS potpisana je Developer ID certifikatom i notarizirana kod Applea. Za kasniju nadogradnju pokrenite `brew upgrade --cask tslink`, a zatim ponovo `tslink install` ako TSLink radi kao pozadinski servis.

```bash
brew install --cask anydoor7/tap/tslink
```

Na Windowsu preuzmite `tslink_<version>_windows_<arch>.zip` iz [najnovijeg izdanja](https://github.com/anydoor7/tslink/releases/latest), provjerite ga prema `checksums.txt` i pokrenite `tslink install` kako bi se TSLink pokretao pri prijavi. Zip nije potpisan Authenticodeom; [provjerite izdanje](verify-release.md) pomoću potpisanih kontrolnih suma i atestacija. Linux paketi `.deb` i `.rpm` nalaze se na istoj stranici izdanja. Za izgradnju iz izvornog koda potrebni su **Git i Go 1.26.6+**. Naredbe ispod koriste bash/zsh; pogledajte [Postavljanje na macOS, Linux i Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Trebaju vam **Tailscale račun** te [MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Uređaji za privatni pristup trebaju Tailscale i dozvolu mrežne politike. TSLink ugrađuje Tailscale na host aplikacija.

Ako vaša aplikacija već radi na portu 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Odaberite slobodno ime; ako `share` vrati drugo, koristite ga u `url`. Prvo završite prikazanu registraciju u pregledniku i odobrenje uređaja, pa otvorite tačan URL na dozvoljenom uređaju. `share` po potrebi pokreće pozadinski servis. Za prvi privatni pristup ne treba administratorski API token. Datoteke dijelite s `tslink share ./report.html`; moraju postojati, a aplikacije već raditi. [Potpuno postavljanje](getting-started.md)

Kada vam proradi i bude korisno, možete [dati zvjezdicu TSLinku](https://github.com/anydoor7/tslink) da ga drugi lakše pronađu. To je potpuno dobrovoljno.

<a id="architecture"></a>

## Kako sve radi

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Jedan računar ili cloud server: CLI/MCP upravlja zajedničkim demonom i čvorovima po aplikaciji. Privatni uređaji koriste šifrirani Tailscale; opcionalni javni HTTPS/Funnel vodi do HTTP aplikacija kroz provjeru gostiju ili izričitu javnu objavu." width="960">
</picture>

Zamislite privatni, šifrirani put do svojih aplikacija. **Tailscale pruža mrežni prijenos i HTTPS; TSLink upravlja pristupom aplikacijama na svakom hostu.** Jedan demon pokreće ugrađeni čvor za svaki servis. Privatni portal prikazuje dozvoljene aplikacije; stanje i historija pristupa pomažu u održavanju.

Javni pristup se izričito uključuje: gostima treba link i eventualni PIN; otvoreni Funnel dostupan je svakome s URL-om. Oba koriste javni HTTPS, ne privatni korisnički identitet. Sirovi TCP ostaje privatan i zavisi od tailnet politike i autentifikacije odredišta. TSLink ne instalira aplikacije, ne izolira procese, ne stvara cloud VPC niti objedinjuje hostove. Nezavisan projekat koji radi s Tailscaleom. [Arhitektura i granice](architecture.md)

<a id="agents"></a>

## Za agente

Upravljajte popisom, stanjem, URL-ovima i pristupom putem CLI/MCP-a. Počnite od [vodiča za agente](agent-quickstart.md), pročitajte aktuelne sheme alata i provjerite stvarni pristup prije prijave uspjeha.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI automatizacija koristi `--json`; MCP koristi JSON-RPC preko stdio. [Klijenti](mcp-clients.md) · [Udaljeni MCP](remote-mcp.md) · [Uloge i opseg](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Dokumentacija i licenca

[Svi vodiči](INDEX.md) · [CLI referenca](cli-reference.md) · [Lokalni AI](local-ai.md) · [Stanje](health-and-alerts.md) · [Historija pristupa](access-log.md) · [Plan razvoja](roadmap.md)

Popis aplikacija s više hostova je planiran. Dobrodošli su [doprinosi](../CONTRIBUTING.md) i [sigurnosne prijave](../SECURITY.md). [Apache 2.0](../LICENSE) dozvoljava komercijalnu upotrebu; pri redistribuciji sačuvajte [NOTICE](../NOTICE) i [obavijesti trećih strana](../THIRD_PARTY_NOTICES.md). [Komercijalna saradnja](../COMMERCIAL.md) je dobrovoljna. Tailscale uslovi i paketi primjenjuju se zasebno.
