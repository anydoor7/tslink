<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Privatne adrese za vaše aplikacije, na vašoj Tailscale mreži.</strong></p>
<p align="center">Otvarajte ih sa svojih uređaja. Podijelite jednu s osobom ili putem linka, do datuma koji odaberete.</p>
<p align="center"><strong>Bosanski</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Više jezika</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
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

<a id="why"></a>

## Kada vam treba TSLink

Za jednu aplikaciju na vlastitim uređajima dovoljan je Serve. TSLink objedinjuje adrese aplikacija, rokove i promjene pristupa u jednom toku rada.

| Zadatak | Samo Tailscale | TSLink |
|---|---|---|
| Jedna web aplikacija na telefonu | Dovoljno je `tailscale serve 3000` | `tslink share 3000` |
| Više aplikacija, svaka sa svojim imenom | Podešavanje Services ili zasebni čvorovi | Jedan `share`/`add` po aplikaciji; prijavite svaki čvor |
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

<a id="agents"></a>

## Za AI agente

Razvojni server koji agent pokrene na `localhost` nije dostupan s vašeg telefona. TSLink omogućava agentu da mu dodijeli privatnu adresu, javi tačan URL i ukloni ga kad završi.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI ili MCP.** Komande za upravljanje primaju `--json` i vraćaju verzionisane rezultate; `tslink mcp` nudi operacije za aplikacije i pristup preko MCP-a.
- **Jedan fajl, ne cijeli folder.** Agent može podijeliti samo svoj HTML izvještaj komandom `tslink share ./report.html`; ostali fajlovi u tom folderu ostaju nedostupni.
- **Ograničene uloge.** `viewer`, `app-operator` ili `people-manager`, ograničene na aplikacije koje navedete. `tslink mcp-audit` pokazuje šta je agent promijenio. Uloge ograničavaju TSLink alate, a ne agentov vlastiti shell.

[Vodič za agente](agent-quickstart.md) · [MCP ovlasti](mcp-scopes.md) · [Udaljeni MCP](remote-mcp.md)

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
