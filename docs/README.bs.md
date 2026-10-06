<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Dajte svakoj aplikaciji na vašem računaru ili serveru vlastitu privatnu adresu u vašoj Tailscale mreži i sami odlučite ko joj može pristupiti.</strong></p>

Otvarajte svoje web aplikacije, foldere, API-je modela i baze podataka sa vlastitog telefona i laptopa, uz provjeru stanja i historiju pristupa za svaku. Vaši AI agenti mogu aplikacijama koje pokrenu na localhost dati privatnu adresu za vaše druge uređaje i provjeravati ih, u okviru uloge koju im date. Kada nekom drugom treba pristup, dajte ga određenoj osobi do nekog datuma ili otvorite web aplikaciju prema javnom internetu na ograničeno vrijeme.

**Potreban je Tailscale.** Treba vam Tailscale račun (besplatan za ličnu upotrebu), a svaki uređaj koji otvara privatnu aplikaciju treba Tailscale aplikaciju; gostima i javnim posjetiocima treba samo preglednik. TSLink je nezavisan projekat koji Tailscale nije napravio niti podržao. [Zahtjevi](#requirements)

<p align="center"><a href="#quickstart">Brzi početak</a> · <a href="#agents">Za agente</a> · <a href="comparison.md">Poređenje sa Serve, ngrok i Cloudflare</a> · <a href="#documentation">Dokumentacija</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <strong>Bosanski</strong> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Šta možete raditi

### Pristup vlastitim aplikacijama

- **Adresa za svaku aplikaciju.** `tslink share 3000`, `tslink share ./photos` ili `tslink add db --tcp localhost:5432` daje web aplikaciji, folderu, datoteci ili TCP servisu vlastitu privatnu adresu u vašem tailnetu (vašoj privatnoj Tailscale mreži), npr. `https://photos.<tailnet>.ts.net`. Svaka aplikacija je zaseban Tailscale uređaj, pa aplikacije otvarate po imenu umjesto po IP adresi i portu.
- **Privatno dok ne odlučite drugačije.** TSLink podrazumijevano drži pristup aplikacijama privatnim, a politika vašeg tailneta određuje koji se uređaji mogu povezati. Javnu pristupnu tačku za aplikaciju TSLink otvara samo kada napravite link za goste ili izričito objavite kroz Funnel.
- **Sve na jednom mjestu.** `tslink status --urls` prikazuje aplikacije registrovane na ovom računaru, a opcionalna privatna početna stranica prikazuje njihove adrese i stanje. [Portal](portal.md)
- **Saznajte kad nešto prestane raditi.** Provjere stanja u pozadini mogu vas upozoriti putem naredbe ili webhooka kada aplikacija padne ili se vrati, ili kada njena Tailscale prijava uskoro ističe. Historija pristupa pokazuje ko je i kada otvorio koju aplikaciju, uključujući odbijene zahtjeve. [Stanje i upozorenja](health-and-alerts.md) · [Historija pristupa](access-log.md)
- **Recepti za aplikacije.** Recepti pokrivaju 15 self-hosted aplikacija, među njima Home Assistant, Jellyfin, Immich i Ollama, a `tslink apps detect` može pronaći podržane aplikacije koje već lokalno slušaju. Za slanje velikih fotografija i videa [povećajte ograničenja slanja za tu aplikaciju](sharing.md). [Recepti za aplikacije](apps.md) · [Lokalni AI](local-ai.md)

### Neka vaši agenti rade s njima

Kada agent pokrene razvojni server, pregled ili lokalni API modela na `localhost`, vaš telefon i drugi računari ne mogu doći do te adrese. TSLink omogućava agentu da mu dodijeli privatnu adresu, kaže vam tačan URL i zatim ponovo ukloni njegovu registraciju, u granicama koje vi postavite.

- **Podijeli, provjeri, poništi.** `share --json` vraća ime koje je registrovao i tačan URL ili link za prijavu koji trebate otvoriti. `url <name> --wait` i `status --urls --name <name>` javljaju da li je pristupna tačka spremna, a `remove <name>` (u MCP-u `unshare`) uklanja dijeljenje. [Vodič za agente](agent-quickstart.md)
- **Napravljeno za automatizaciju.** Upravljačke naredbe osim `tslink mcp` prihvataju `--json` i vraćaju verzionisane rezultate sa stabilnim kodovima grešaka. `tslink mcp` nudi alate za aplikacije i pristup lokalnom MCP klijentu preko JSON-RPC-a. Uz podešena povezivanja pozivalaca, `tslink serve --mcp` nudi te alate MCP klijentima na vašim drugim uređajima kroz tailnet. [JSON automatizacija](json-automation.md) · [Udaljeni MCP](remote-mcp.md)
- **Ograničene ovlasti.** Lokalni agent podrazumijevano ima ovlasti vlasnika. Dajte agentu ograničenu ulogu (`viewer`, `app-operator` ili `people-manager`) koja pokriva samo aplikacije koje navedete i ograničava koliko dugo može trajati svaki pristup koji odobri. Promjene napravljene kroz MCP se bilježe, a `tslink mcp-audit` ih prikazuje. Uloge ograničavaju TSLink alate, a ne vlastitu ljusku ili datoteke agenta. [MCP ovlasti](mcp-scopes.md)

### Dijelite s osobama koje izaberete

- **Određene osobe, do datuma.** `tslink people add alice@example.com --apps photos,notes --for 7d` omogućava toj Tailscale prijavi da otvara te web i datotečne aplikacije do roka. `people update`, `extend` i `people remove` mijenjaju ili završavaju pristup; nakon uklanjanja sljedeći zahtjev te osobe se odbija, ali ono što je već preuzela ne može se vratiti. [Osobe](people.md) · [Trajanje](durations.md)
- **Neko izvan vašeg tailneta.** Dodajte `--invite --print-links` da dobijete jednu poruku spremnu za slanje, s pozivnicom za uređaj za svaku aplikaciju (za ovo treba API token u vlasništvu korisnika). `--qr` ispisuje kod za podešavanje na telefonu.
- **Zahtjevi.** Osobe u vašem tailnetu mogu s početne stranice tražiti više vremena ili pristup aplikaciji koju ste označili kao dostupnu na zahtjev. Odobravate jednom naredbom uz trajanje. [Zahtjevi za pristup](requests.md)

### Otvorite web aplikaciju prema internetu na neko vrijeme

- **Linkovi za goste.** `tslink guest create photos --for 3d --public --print-link` pravi link za preglednik do jedne web aplikacije, opcionalno s PIN-om, koji možete opozvati zasebno. Gostima ne treba Tailscale račun. Link može koristiti svako ko ga ima, pa ne dokazuje ko je posjetio aplikaciju. [Gosti](guest-links.md)
- **Otvoren javni URL.** `tslink add preview --proxy localhost:3000 --funnel --public` objavljuje web aplikaciju za svakoga ko zna njen URL. Nova objava podrazumijevano traje 24 sata; drugo trajanje birate s `--funnel-ttl`. [Funnel](funnel.md)
- Novi linkovi za goste i nove otvorene javne objave idu kroz Tailscale Funnel i imaju ograničeno trajanje (najmanje 1 sat, podrazumijevano najviše 7 dana, vlasnik to može promijeniti). Ovi javni putevi podržavaju aplikacije iza HTTP proxyja; direktno dijeljeni folderi i datoteke te čisti TCP ostaju privatni.

Sve navedeno dolazi s verzijom v0.1.0.

<a id="requirements"></a>

## Zahtjevi

TSLink je izgrađen na Tailscaleu. To je nezavisan projekat koji Tailscale nije napravio niti podržao, a vrijede Tailscaleovi uslovi i [planovi](https://tailscale.com/pricing).

| Ko | Šta treba |
|---|---|
| Vi | Tailscale račun s uključenim [MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Besplatni Personal plan je za nekomercijalnu upotrebu. |
| Računar ili server na kojem rade vaše aplikacije | Samo TSLink. Ima ugrađen Tailscale, pa ne trebate posebno instalirati Tailscale. Uz podrazumijevano podešavanje, svaki novi čvor aplikacije traži prijavu u pregledniku i možda odobrenje uređaja. [Sačuvani akreditivi](credentials-and-tags.md) omogućavaju upis bez prijave u pregledniku za svaku aplikaciju. |
| Vaši drugi uređaji | Tailscale aplikacija, prijavljena u vaš tailnet. |
| Osobe koje izaberete | Tailscale aplikacija i vlastita prijava. Ili se pridruže vašem tailnetu, što dodaje korisnika u vaš plan, ili prihvate pozivnicu za uređaj za svaku aplikaciju. Politika vašeg tailneta mora im dozvoliti pristup. |
| Gosti i javni posjetioci | Preglednik. Vaš tailnet mora dozvoliti Funnel, koji Tailscale i dalje vodi kao beta. |

Kada se za aplikaciju izda HTTPS certifikat, njeno Tailscale ime uređaja i DNS ime vašeg tailneta pojavljuju se u javnom zapisniku certifikata. Birajte imena aplikacija za koja vam ne smeta da ih drugi vide.

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
