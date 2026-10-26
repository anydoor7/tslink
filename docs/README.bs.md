<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink je CLI i MCP server otvorenog koda koji svakoj aplikaciji na vašem računaru daje privatnu adresu <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, tako da vi ili vaš coding agent možete na telefonu otvoriti pokrenuti razvojni server. Nezavisan projekat, nije proizvod kompanije Tailscale.</p>
<p align="center">Vaš telefon treba aplikaciju Tailscale, prijavljenu na isti tailnet.</p>
<p align="center"><strong>Bosanski</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Više jezika</a></p>

```text
# Claude Code, nakon instalacije CLI-ja (vidi Instalacija ispod)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Lokalni MCP klijenti koji koriste JSON s mcpServers (ostali: pogledajte MCP postavke svog klijenta)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Recite svom agentu: „Podijeli razvojni server da ga mogu otvoriti na telefonu."
```

Isto ručno, s razvojnim serverom koji već sluša na portu 5173. Uz `--json`, `share` prijavljuje registrovano ime u `data.name`. Koristite to ime u narednim komandama: razlikuje se od `--name` kad je to ime bilo zauzeto, na primjer `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, ili data.auth_url (Tailscale link za prijavu) kad je status needs_login
tslink url <name> --wait              # kad završite prijavu: tačan URL
tslink remove <name>                  # kad završite; preskočite ako share nije uspio. Razvojni server nastavlja raditi
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalacija

macOS računari zahtijevaju macOS 13 Ventura ili noviji ([podržane platforme](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Na Windowsu instalirajte pomoću Scoopa:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Za nadogradnju pokrenite `brew upgrade --cask tslink` ili `scoop update; scoop update tslink`. Ako TSLink radi kao pozadinski servis, nakon toga ponovo pokrenite `tslink install`.

Linux paketi `.deb` i `.rpm` te Windows verzije nalaze se u [najnovijem izdanju](https://github.com/anydoor7/tslink/releases/latest). Kada prvi put podijelite aplikaciju, TSLink prikaže Tailscale link za prijavu za nju. [Prvi koraci](getting-started.md)

<a id="agents"></a>

## Za AI agente

Razvojni server koji agent pokrene na `localhost` nije dostupan s vašeg telefona. TSLink omogućava agentu da mu dodijeli privatnu adresu, preda vam tačan URL i ukloni dijeljenje kad završi. Vaš telefon treba aplikaciju Tailscale, prijavljenu na isti tailnet.

Testirano 2026-10-22 s Vite 8.3.5 i iPhoneom 15 Pro: agent je podijelio razvojni server preko MCP-a, telefon je otvorio privatni URL, a stranica se osvježila pri spremanju bez ručnog ponovnog učitavanja.

Za Claude Code prvo instalirajte CLI, zatim pokrenite:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Dodatak uključuje [skill za pregled](../skills/tslink-preview/SKILL.md) i MCP vezu ispod. Marketplace ostaje u ovom repozitoriju i preuzima dodatak iz [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Glavni skill ostaje ovdje; dodatak sadrži kopiju koju provjerava CI. Drugi agenti mogu koristiti isti skill ili se direktno povezati preko MCP-a. Ova integracija zahtijeva lokalni CLI i ne radi u claude.ai chatu. [Postavljanje i registracija](agent-integrations.md)

Za lokalnog MCP klijenta koji koristi JSON s `mcpServers` (ostali klijenti: pogledajte njihove upute za MCP postavke i [vodič za MCP klijente](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Gateway je zadani za nove instalacije.** Svaka privatna HTTP/datotečna aplikacija dobija stabilan port, npr. `https://home.<tailnet>.ts.net:8443/`. Postojeće instalacije zadržavaju način rada, a aplikacije svoje čvorove/URL-ove; `--portal-only` se bira izričito.
- **Registrujte gateway.** Bez vjerodajnica, provjereni vlastiti račun gatewaya bez oznaka može postati owner. Za tagged/OAuth lokalno pokrenite `tslink portal enable --owner <login>`. Registracija čvorova aplikacija na čekanju ne blokira spremnu gateway rutu.
- **Pristup po osobi.** Nove aplikacije dopuštaju owner/admin i izričito ovlaštene osobe. `people add`, `add --allow` i MCP `add/share.allow` provjeravaju članstvo i pripremaju potrebne pozivnice. Članovima ne treba pozivnica za uređaj; vanjske osobe prihvataju jednu gateway pozivnicu. Greške direktorija/API-ja vraćaju nedovršeno stanje.
- **Uredite, spremite, vidite.** Spremanje koda osvježava stranicu na telefonu. Pravila Host i Origin same aplikacije i dalje mogu blokirati zahtjeve: u našem testu Vite nije trebao promjenu `allowedHosts`; Next.js nije testiran i i dalje traži `allowedDevOrigins`.
- **Čišćenje.** Agent poziva `unshare`, ili vi pokrenete `tslink remove <name>`, uvijek s imenom koje je vratio rezultat share. Uklanjanje poništava registraciju dijeljenja i pokušava obrisati njegov čvor na tailnetu kad TSLink može dokazati da je njegov vlasnik, zato u rezultatu provjerite polja o čišćenju ili zadržanom stanju. Ne zaustavlja razvojni server.
- **CLI ili MCP.** Komande za upravljanje primaju `--json` i vraćaju verzionisane rezultate; `tslink mcp` nudi operacije za aplikacije i pristup preko MCP-a.
- **Jedna datoteka, ne folder.** Agent može podijeliti samo svoj samostalni HTML izvještaj komandom `tslink share ./report.html`; ostale datoteke u tom folderu ostaju nedostupne.
- **Ograničene uloge.** `viewer`, `app-operator` ili `people-manager`, ograničene na aplikacije koje navedete; dijeljenje nove aplikacije traži ulogu `owner`. `tslink mcp-audit` pokazuje šta je agent promijenio. Uloge ograničavaju TSLink alate, a ne agentov vlastiti shell.

[Vodič za agente](agent-quickstart.md) · [MCP ovlasti](mcp-scopes.md) · [Udaljeni MCP](remote-mcp.md)

<a id="why"></a>

## Kada vam treba TSLink

Za jednu aplikaciju koju sami pokrenete i otvorite na vlastitom telefonu dovoljan je Serve. TSLink je za razvojne servere koje pokreće agent, često više njih, kad svaki treba dobiti vlastito privatno ime, a dijeljenje, URL i čišćenje trebaju biti gotovi koraci u CLI-ju, MCP-u i skillu za Claude Code.

| Zadatak | Samo Tailscale | TSLink |
|---|---|---|
| Više razvojnih servera koje pokreće vaš agent, po jedno ime za svaki | Serve postavlja rute na nivou uređaja pod jednim imenom po mašini; Services za svako ime traži definiciju, odobrenje i host s oznakama | Jedan čvor po aplikaciji, s dijeljenjem, traženjem URL-a i čišćenjem spremnim u CLI-ju (`share`, `url`, `remove`), MCP-u (`share`, `url`, `unshare`) i skillu |
| Jedna aplikacija koju sami pokrenete, na telefonu | Dovoljno je `tailscale serve 3000` | `tslink share 3000` |
| Jedna osoba, jedna aplikacija, sedam dana | Pravila politike, pa JIT alat ili ručno uklanjanje | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/fajlovi) |
| Link za preglednik, tri dana | Javni Funnel; sami dodajte kontrolu pristupa i zakazano gašenje | `tslink guest create photos --for 3d --public --print-link` (samo HTTP) |

Privatni primaoci trebaju Tailscale. Linkovi za goste su javni, mogu se proslijediti i služe kao pristupne vjerodajnice.

[Potpuno poređenje](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Vaše aplikacije, na vašim uređajima

- **Adresa za svaku aplikaciju.** Web aplikacije, folderi, pojedinačni fajlovi i TCP portovi dobijaju vlastito ime u vašem tailnetu, pa koristite imena umjesto IP adresa.
- **Podrazumijevano privatno.** Ništa nije javno dok ne napravite link za goste ili objavite kroz Funnel.
- **Jedna aplikacija, ne cijela mašina.** Svaka aplikacija koju objavite dobija vlastiti čvor koji prosljeđuje samo do nje. Bez Tailscale aplikacije na hostu TSLink ne dodaje druge portove hosta u vaš tailnet.
- **Početna stranica** sa spiskom vaših aplikacija i njihovim stanjem. [Portal](portal.md)
- **Provjere stanja i upozorenja** putem komande ili webhooka, uz zapis pristupa koji uključuje i odbijene zahtjeve. [Stanje i upozorenja](health-and-alerts.md) · [Historija pristupa](access-log.md)
- **Recepti za 15 self-hosted aplikacija**, među njima Home Assistant, Jellyfin, Immich i Ollama. `tslink apps detect` pronalazi one koje već rade. [Recepti za aplikacije](apps.md)

## Dijelite kad želite

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Linkovi za goste i novi javni URL-ovi ističu i rade samo za web aplikacije; folderi, fajlovi i TCP portovi ostaju privatni. [Osobe](people.md) · [Linkovi za goste](guest-links.md) · [Javni pristup](funnel.md)

<a id="architecture"></a>

## Kako radi

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Jedan računar ili cloud server: CLI/MCP upravlja zajedničkim demonom i čvorovima po aplikaciji. Privatni uređaji koriste šifrirani Tailscale; opcionalni javni HTTPS/Funnel vodi do HTTP aplikacija kroz provjeru gostiju ili izričitu javnu objavu." width="960">
</picture>

Jedan pozadinski proces pokreće zaseban Tailscale čvor za svaku aplikaciju. Tailscale pruža transport kroz tailnet i HTTPS certifikate. Privatni pristup webu i fajlovima može se ograničiti po Tailscale identitetu pomoću `--allow` i dozvola za osobe; sirovi TCP se oslanja na politiku vašeg tailneta i vlastitu prijavu aplikacije. [Arhitektura](architecture.md)

<a id="requirements"></a>

## Zahtjevi

| Ko | Šta treba |
|---|---|
| Vi | Tailscale račun s uključenim MagicDNS i HTTPS |
| Računar na kojem rade vaše aplikacije | TSLink, koji ima ugrađen Tailscale (na Linuxu je potrebna i korisnička sesija systemd-a) |
| Vaši uređaji i osobe s kojima dijelite | Tailscale aplikacija |
| Gosti | Preglednik |

Imena HTTPS aplikacija pojavljuju se u javnim zapisima certifikata, pa birajte imena koja drugi smiju vidjeti.

<a id="documentation"></a>

## Više

[Sva dokumentacija](INDEX.md) · [CLI referenca](cli-reference.md) · [Poređenje sa Serve, ngrok i Cloudflare](comparison.md) · [Doprinosi](../CONTRIBUTING.md) · [Sigurnost](../SECURITY.md)

Apache 2.0. TSLink je nezavisan projekat koji Tailscale nije napravio niti podržao.
