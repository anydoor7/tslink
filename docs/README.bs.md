<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logotip">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Dajte svojim lokalnim aplikacijama, modelima i datotekama vlastitu privatnu adresu.</strong><br>
  Otvarajte ih s drugog uređaja kojem je dozvoljen pristup u vašoj Tailscale mreži.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licenca: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 ili noviji"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: ugrađeni tsnet čvorovi"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 alata"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <strong>Bosanski</strong> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Instalacija

Potrebni su vam **Go 1.26.6+** i Git. Gotova izdanja i
Homebrew cask nisu objavljeni; instalirajte iz izvornog koda. Ovi primjeri koriste
**bash ili zsh**; zahtjeve za Windows i servise u pozadini pogledajte u [podršci za platforme](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Koristite Tailscale račun s [uključenim MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
Uređaj koji pristupa servisu mora biti prijavljen u vašu Tailscale mrežu (**tailnet**), a pravila mreže
moraju dozvoljavati pristup servisu. TSLink ugrađuje Tailscale na računar koji objavljuje servis.

### Podijelite svoju prvu stranicu

Napravite stranicu; TSLink je direktno poslužuje i po potrebi pokreće svoj servis u pozadini:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Ako TSLink ispiše URL za registraciju, otvorite ga da autorizujete čvor; vaša tailnet mreža također može
zahtijevati administratorsko odobrenje uređaja. Zatim dohvatite tačnu adresu:

```bash
tslink url demo --wait
```

Otvorite taj URL na uređaju kojem je dozvoljen pristup. Za ovo prvo dijeljenje nije potreban API token.
[Potpuno podešavanje i detalji životnog ciklusa →](getting-started.md)

<a id="use-cases"></a>

## Šta ćete podijeliti?

Datoteke moraju postojati; aplikacije, baze podataka i serverske komponente modela već moraju raditi na navedenim portovima.

| Primjena | Naredba |
|---|---|
| Otvorite lokalnu aplikaciju s drugog uređaja | `tslink share 3000` |
| Pregledajte direktorij datoteka | `tslink share ./public --name files` |
| Čitajte generisani HTML izvještaj na telefonu | `tslink share ./report.html --name report` |
| Povežite se s lokalnom bazom podataka preko TCP-a | `tslink add database --tcp localhost:5432` |
| Koristite HTTP API lokalnog modela, kao što je Ollama | `tslink add model --proxy localhost:11434` |

Za Ollama dohvatite tačan URL pomoću `tslink url model --wait`; `baseURL` klijenta
kompatibilnog s OpenAI-jem koristi taj URL uz dodatak `/v1`. [Lokalni modeli i radni tokovi s privatnim podacima →](local-ai.md)

Za više aplikacija na jednom hostu TSLink objedinjuje imenovane čvorove servisa, HTTP liste dozvoljenih identiteta, istek Funnela i MCP upravljanje. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) može biti dovoljan za jednu aplikaciju na vlastitim uređajima.

<a id="architecture"></a>

## Arhitektura

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Primjer mape servisa: App, Docs, Database i Model su zasebni imenovani čvorovi u jednoj tailnet mreži. Aplikacije, datoteke i API-ji modela koriste HTTPS; baza podataka koristi privatni TCP." width="960">
</picture>

**Jedna tailnet mreža, zasebni čvorovi servisa.** Zajednički daemon pokreće po jedan ugrađeni tsnet čvor za
svaki servis, prosljeđuje HTTP, poslužuje datoteke ili posreduje za TCP. Promjene registra stupaju na snagu
za vrijeme njegovog rada. Svaki čvor ima vlastiti mrežni identitet; servisi dijele računar koji ih objavljuje.
[Detalji arhitekture →](architecture.md)

| Komponenta | Uloga |
|---|---|
| [Go](../go.mod) | Nativni program komandne linije |
| [Tailscale tsnet](architecture.md) | Čvorovi servisa i transport kroz tailnet mrežu |
| [Cobra](https://github.com/spf13/cobra) | Naredbe i pomoć |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transport za agente |
| Sistemsko spremište ključeva i upravitelj korisničkih servisa | Opcionalne vjerodajnice i rad u pozadini |

Servisi ostaju unutar vaše tailnet mreže osim ako izričito omogućite [javni Funnel](getting-started.md#more-examples).
HTTP servisi i servisi za datoteke podržavaju liste dozvoljenih identiteta (`WhoIs`, `--allow`); TCP koristi pravila tailnet mreže i
vlastitu autentifikaciju serverske komponente. Pogledajte [granice dijeljenja](sharing.md).

TSLink ne instalira aplikacije, ne pokreće modele, ne izolira procese hosta i ne objedinjuje više hostova. Mreža, enkripcija i HTTPS dolaze od Tailscalea; TSLink je nezavisan projekat.

<a id="agents"></a>

## Za agente

**19 MCP alata** omogućava agentu dijeljenje izvještaja, upravljanje servisima, dohvat URL-ova i provjeru
postavki. Povežite lokalni MCP klijent s instaliranim programom:

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

MCP upravlja TSLinkom; aplikacije koriste HTTP API modela za inferenciju.
Za konfiguraciju i automatizaciju pogledajte [MCP klijente](mcp-clients.md), [udaljeni MCP](remote-mcp.md) i
[operativni vodič za agente](../AGENTS.md).

CLI automatizacija podržava `--json` s `schema_version` postavljenim na `1`; pogledajte `tslink status --urls --json`. Lokalni MCP koristi JSON-RPC preko stdio. Pogledajte [JSON automatizaciju](json-automation.md).

<a id="roadmap"></a>

## Šta dolazi

Stavke U spajanju, Na pregledu ili Planirano nisu uključene u prethodnu instalaciju iz izvornog koda.

| Namjena | Status |
|---|---|
| <!-- roadmap:people --> Dajte rođaku 3 dana pristupa privatnim HTTP/datotečnim aplikacijama i objedinite pozivnice u jednoj poruci; primalac i dalje treba Tailscale. | U spajanju |
| <!-- roadmap:health --> Provjeravajte zdravlje aplikacija i primajte upozorenja o prekidu ili isteku preko opcionalne komande ili webhooka. | U spajanju |
| <!-- roadmap:recipes --> Pronađite podržane loopback aplikacije i pregledajte recepte za samostalno hostane aplikacije prije dijeljenja. | U spajanju |
| <!-- roadmap:limits --> Postavite veličinu otpremanja i vremenska ograničenja zahtjeva po HTTP aplikaciji za velike datoteke i spore klijente. | U spajanju |
| <!-- roadmap:windows --> Ponovo pokrenite srušeni Windows daemon tokom prijavljene sesije pomoću zakazanog zadatka i ugrađenog nadzornika. | U spajanju |
| <!-- roadmap:access-log --> Pogledajte ko je otvorio koju aplikaciju u lokalnim zapisima pristupa, uz režime putanje `prefix`, `full` ili `off`. | Na pregledu |
| <!-- roadmap:portal --> Otvorite jednu početnu stranicu s dozvoljenim aplikacijama i uputama za registraciju vlasnika; posjetioci i dalje trebaju Tailscale. | Na pregledu |
| <!-- roadmap:mcp-scopes --> Dodijelite agentu ulogu i opseg aplikacija, uz revizijske potvrde njegovih izmjena. | Na pregledu |
| <!-- roadmap:guest-links --> Omogućite gostu da otvori jednu HTTP aplikaciju u pregledniku bez instaliranja Tailscalea, pomoću vremenskog linka i opcionalnog PIN-a kroz javni Funnel s kontrolom pristupa. | Na pregledu |
| <!-- roadmap:durations --> Izaberite gotova ili vlastita trajanja od najmanje 1 sata, s podesivim maksimumom za goste od 7 dana po zadanim postavkama. | Na pregledu |
| <!-- roadmap:requests --> Pomozite korisnicima telefona da se pridruže QR kodom i jednim korakom odobrite pristup ili dodatno vrijeme. | Na pregledu |
| <!-- roadmap:multi-host --> Pogledajte aplikacije s više hostova u jednom popisu. | Planirano |

<a id="documentation"></a>

## Dokumentacija i licenca

[Prvi koraci](getting-started.md) · [Lokalni modeli](local-ai.md) ·
[CLI referenca](cli-reference.md) · [Platforme](platforms.md) · [Plan razvoja](roadmap.md)

Doprinosite kroz [CONTRIBUTING.md](../CONTRIBUTING.md); ranjivosti prijavljujte prema
[SECURITY.md](../SECURITY.md).

TSLink koristi neizmijenjenu [licencu Apache 2.0](../LICENSE), uključujući komercijalnu upotrebu.
Pri redistribuciji sačuvajte primjenjivi [NOTICE](../NOTICE) i [obavijesti trećih strana](../THIRD_PARTY_NOTICES.md).
[Komercijalna saradnja](../COMMERCIAL.md) je dobrovoljna i ne dodaje uslove licence.
Uslovi usluge i paketi Tailscalea primjenjuju se zasebno.
