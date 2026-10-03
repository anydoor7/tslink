<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logotip">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Podijelite aplikacije sa svog računara s ljudima koje odaberete, onoliko dugo koliko želite.</strong><br>
  Svaka aplikacija dobija vlastitu privatnu adresu u vašoj Tailscale mreži. Provjerite ko ima pristup i povucite ga.
</p>

<p align="center">
  <a href="#quickstart">Brzi početak</a> · <a href="#agents">Za agente</a> · <a href="getting-started.md">Dokumentacija</a> ·
  <strong>Bosanski</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Svi jezici</a>
</p>

## Za šta se koristi

- **Otvorite svoj rad na telefonu.** Izvještaj koji je napravila skripta, razvojni server, bilježnica ili API lokalnog modela, na privatnoj HTTPS adresi dostupnoj dozvoljenim uređajima.
- **Dajte jednoj osobi jednu aplikaciju na određeno vrijeme.** Partneru omogućite korištenje biblioteke fotografija sedmicu dana, a kolegi probnu verziju tri dana. Pristup ističe automatski; možete ga i ranije prekinuti.
- **Prepustite dijeljenje agentu.** Vaš agent za programiranje upravo je napravio kontrolnu ploču. Zamolite ga da je podijeli s vama i kolegom do petka. Može vam reći i šta se trenutno dijeli te povući dijeljenje.

Aplikacije nastavljaju raditi tamo gdje su već pokrenute. TSLink upravlja pristupom svakoj i vodi jedan spisak: šta je podijeljeno, s kim i do kada.

<a id="quickstart"></a>

## Brzi početak

Potrebni su **Go 1.26.6+**, Git i Tailscale račun s [uključenim MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Gotova binarna izdanja još nisu objavljena, pa instalirajte iz izvornog koda:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Podijelite stranicu:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Prvi put TSLink ispisuje poveznicu za prijavu radi registracije novog servisnog čvora; vaš tailnet može tražiti i odobrenje uređaja od administratora. Nakon registracije otvorite URL servisa na dozvoljenom uređaju prijavljenom na vaš tailnet. API token nije potreban.

Provjerite šta se dijeli pa uklonite demonstraciju:

```bash
tslink status --urls
tslink remove demo
```

Možete podijeliti i sljedeće kada njihov pozadinski servis radi:

| Sadržaj | Naredba |
|---|---|
| Lokalna web aplikacija | `tslink share 3000` |
| Mapa s datotekama | `tslink share ./public --name files` |
| API lokalnog modela, poput Ollama | `tslink add model --proxy localhost:11434` |
| Baza podataka preko privatnog TCP-a | `tslink add database --tcp localhost:5432` |
| Poznata samostalno hostovana aplikacija (Jellyfin, Immich, Home Assistant i još 13) | `tslink apps detect`, zatim `tslink apps share jellyfin --yes` |

[Prvi koraci, platforme i pozadinski servis →](getting-started.md)

## Odaberite ko može pristupiti

| Publika | Šta primalac treba | Identitet | Kraj pristupa |
|---|---|---|---|
| **Vaši uređaji** | Prijavu na vaš tailnet | Provjeren Tailscale identitet | Kada uklonite aplikaciju |
| **Imenovane osobe** (privatni HTTP/datoteke) | Tailscale prijavu; vanjski korisnici prihvataju poziv za svaku aplikaciju | Provjeren Tailscale identitet | U zadanom roku (`--for 7d`) ili uz `tslink people remove` |
| **Svako ko ima URL** (Funnel) | Preglednik | Bilo ko; prijava same aplikacije i dalje važi | Podrazumijevano nakon 24 sata (`--funnel-ttl`) |
| **Gostujuća poveznica za preglednik** *(uskoro)* | Preglednik i opcionalni PIN | Vlasnik poveznice | Po isteku njenog roka ili opozivu |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Rokovi za privatni HTTP i datoteke provjeravaju se pri svakom zahtjevu. Opoziv zaustavlja nove zahtjeve; ne može povratiti preuzete podatke niti zatvoriti već prihvaćene tokove i WebSocket veze. [Dijeljenje s osobama →](people.md) · [Granice dijeljenja →](sharing.md)

<a id="agents"></a>

## Za agente

TSLink uključuje MCP server, pa agent može dijeliti, listati, objašnjavati i uklanjati dijeljenja kao i vi. Dodajte ga lokalnom MCP klijentu:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Tačni rezultati.** CLI automatizacija podržava `--json` uz `schema_version: 1` i stabilne kodove grešaka; `tslink mcp` koristi JSON-RPC. `tslink manifest` opisuje svaku naredbu i opciju. Agenti trebaju preuzeti stvarne URL-ove uz `tslink url <name> --wait`, umjesto da ih sastavljaju.
- **Jasno čekanje.** Novi čvor kojem još treba ljudska prijava prijavljuje `needs_login`, umjesto da se prikazuje spremnim.
- **Ovlasti.** Lokalni MCP radi s ovlastima vašeg korisnika. Udaljeni MCP se izričito uključuje, dostupan je samo u tailnetu i ograničen na navedene prijave ili oznake. Uloge po agentu, opseg aplikacija i potvrde radnji stižu *uskoro*.

MCP koji pruža TSLink upravlja projektom TSLink. Ako preko TSLinka objavite drugi MCP server, njemu su i dalje potrebne vlastite dozvole za alate.
[Vodič za agente →](agents.md) · [MCP klijenti →](mcp-clients.md) · [Udaljeni MCP →](remote-mcp.md) · [JSON automatizacija →](json-automation.md)

## Kada odabrati drugi alat

| Ako želite | Razmotrite |
|---|---|
| Jedan lokalni servis na svojim uređajima, uz Tailscale klijent koji već koristite | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Administratorski upravljane servise sa stabilnim imenima na više računara | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Javni URL za webhook ili API demonstraciju bez Tailscale računa | [ngrok](https://ngrok.com/docs/start) ili [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Instalirati i pokretati samostalno hostovane aplikacije, uz dijeljenje | [Umbrel](https://umbrel.com) ili [Coolify](https://coolify.io) |
| Platformu za pristup na osnovu identiteta za cijelu organizaciju | [Pangolin](https://github.com/fosrl/pangolin) ili [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink odgovara osobi koja pokreće više aplikacija i želi vremenski ograničen pristup po aplikaciji i osobi, koji mogu provjeriti i ona i njen agent.

## Kako radi

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database i Model su zasebno imenovani čvorovi u jednom tailnetu, koje pokreće jedan TSLink daemon na računaru koji objavljuje servise." width="720">
</picture>

Jedan pozadinski daemon pokreće ugrađeni Tailscale čvor za svaku aplikaciju, pa svaka ima svoje ime i adresu. Za privatni HTTP i datoteke, `WhoIs` i dozvole osobama ili pravila `--allow` kontrolišu pristup; rokovi se provjeravaju pri svakom zahtjevu. Sirovi TCP koristi tailnet pravila i autentifikaciju pozadinskog servisa. Tailscale pruža tailnet prijenos, enkripciju i certifikate; TSLink je nezavisan projekt. Sve aplikacije dijele računar koji ih objavljuje, pa ih TSLink ne izoluje jednu od druge. [Arhitektura →](architecture.md)

## Stanje

Dostupno: privatne adrese po aplikaciji, imenovane osobe s rokovima i paketima poziva, javni Funnel s istekom, provjere zdravlja i upozorenja, recepti za samostalno hostovane aplikacije, ograničenja zahtjeva po aplikaciji, ponovno pokretanje nakon pada na Windows, CLI i MCP. Dostupni su i: gostujuće poveznice za preglednik, fleksibilna trajanja, dnevnik pristupa, početna stranica aplikacija, ograničene uloge agenata, QR uvođenje i zahtjevi za pristup.

Zajednički spisak za više računara je planiran. [Plan razvoja →](roadmap.md)

## Dokumentacija i licenca

[Prvi koraci](getting-started.md) · [CLI referenca](cli-reference.md) · [Platforme](platforms.md) · [Lokalni modeli](local-ai.md) · [Doprinosi](../CONTRIBUTING.md) · [Sigurnost](../SECURITY.md)

Apache License 2.0, uključujući komercijalnu upotrebu. Pri redistribuciji zadržite [NOTICE](../NOTICE) i [obavijesti trećih strana](../THIRD_PARTY_NOTICES.md). Uslovi i paketi usluge Tailscale primjenjuju se zasebno.
