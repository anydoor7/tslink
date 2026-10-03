<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Logo TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Udostępniaj aplikacje ze swojego komputera wybranym osobom na wybrany czas.</strong><br>
  Każda aplikacja ma własny prywatny adres w Twojej sieci Tailscale. Sprawdzaj, kto ma dostęp, i odbieraj go w razie potrzeby.
</p>

<p align="center">
  <a href="#quickstart">Szybki start</a> · <a href="#agents">Dla agentów</a> · <a href="getting-started.md">Dokumentacja</a> ·
  <strong>Polski</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Wszystkie języki</a>
</p>

## Do czego ludzie go używają

- **Otwieraj swoją pracę na telefonie.** Raport wygenerowany przez skrypt, serwer deweloperski, notatnik lub API lokalnego modelu, pod prywatnym adresem HTTPS dostępnym dla uprawnionych urządzeń.
- **Daj jednej osobie jedną aplikację na jakiś czas.** Udostępnij partnerowi bibliotekę zdjęć na tydzień lub koledze wersję podglądową na trzy dni. Dostęp wygasa sam; możesz też zakończyć go wcześniej.
- **Powierz udostępnianie agentowi.** Twój agent programistyczny właśnie stworzył panel. Poproś, by udostępnił go Tobie i współpracownikowi do piątku. Może też wskazać bieżące udostępnienia i cofnąć dostęp.

Aplikacje nadal działają tam, gdzie dotychczas. TSLink zarządza tym, kto może dotrzeć do każdej z nich, i prowadzi jedną listę: co udostępniono, komu i do kiedy.

<a id="quickstart"></a>

## Szybki start

Potrzebujesz **Go 1.26.6+**, Git oraz konta Tailscale z [włączonymi MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Gotowe wydania binarne nie są jeszcze publikowane, więc zainstaluj program ze źródeł:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Udostępnij stronę:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Za pierwszym razem TSLink wyświetla link logowania do rejestracji nowego węzła usługi; Twoja sieć tailnet może też wymagać zatwierdzenia urządzenia przez administratora. Po rejestracji otwórz URL usługi na uprawnionym urządzeniu zalogowanym do Twojej sieci tailnet. Token API nie jest potrzebny.

Sprawdź udostępnienia, a następnie usuń demonstrację:

```bash
tslink status --urls
tslink remove demo
```

Możesz też udostępnić poniższe elementy, gdy ich zaplecze już działa:

| Co | Polecenie |
|---|---|
| Lokalna aplikacja internetowa | `tslink share 3000` |
| Folder z plikami | `tslink share ./public --name files` |
| API lokalnego modelu, np. Ollama | `tslink add model --proxy localhost:11434` |
| Baza danych przez prywatne TCP | `tslink add database --tcp localhost:5432` |
| Znana aplikacja hostowana samodzielnie (Jellyfin, Immich, Home Assistant i 13 innych) | `tslink apps detect`, następnie `tslink apps share jellyfin --yes` |

[Pierwsze kroki, platformy i usługa w tle →](getting-started.md)

## Wybierz, kto może korzystać

| Odbiorcy | Czego potrzebują | Tożsamość | Koniec dostępu |
|---|---|---|---|
| **Twoje urządzenia** | Logowanie do Twojej sieci tailnet | Zweryfikowana tożsamość Tailscale | Gdy usuniesz aplikację |
| **Wskazane osoby** (prywatne HTTP/pliki) | Logowanie Tailscale; osoby z zewnątrz przyjmują zaproszenie do każdej aplikacji | Zweryfikowana tożsamość Tailscale | W ustawionym terminie (`--for 7d`) lub po `tslink people remove` |
| **Każdy, kto ma URL** (Funnel) | Przeglądarka | Dowolna osoba; nadal obowiązuje logowanie w aplikacji | Domyślnie po 24 godzinach (`--funnel-ttl`) |
| **Link gościnny do przeglądarki** *(wkrótce)* | Przeglądarka i opcjonalny PIN | Posiadacz linku | Po upływie jego terminu lub cofnięciu |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Terminy prywatnych udostępnień HTTP i plików są sprawdzane przy każdym żądaniu. Cofnięcie dostępu blokuje nowe żądania; nie odzyska pobranych danych ani nie zamknie już przyjętych strumieni i połączeń WebSocket. [Udostępnianie osobom →](people.md) · [Granice udostępniania →](sharing.md)

<a id="agents"></a>

## Dla agentów

TSLink zawiera serwer MCP, dzięki któremu agent może tak jak Ty udostępniać, wyświetlać, objaśniać i usuwać udostępnienia. Dodaj go do lokalnego klienta MCP:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Dokładne wyniki.** Automatyzacja CLI obsługuje `--json` z `schema_version: 1` i stabilnymi kodami błędów; `tslink mcp` używa zamiast tego JSON-RPC. `tslink manifest` opisuje każde polecenie i flagę. Agenci powinni pobierać prawdziwe URL-e przez `tslink url <name> --wait`, zamiast je składać.
- **Rzetelne stany oczekiwania.** Nowy węzeł wymagający logowania przez człowieka zgłasza `needs_login`, zamiast udawać gotowość.
- **Uprawnienia.** Lokalny MCP działa z uprawnieniami Twojego użytkownika. Zdalny MCP wymaga włączenia, jest dostępny tylko w tailnet i dopuszcza wyłącznie wymienione konta lub tagi. Role agentów, zakresy aplikacji i potwierdzenia działań pojawią się *wkrótce*.

MCP w TSLink steruje samym TSLink. Jeśli udostępnisz przez TSLink inny serwer MCP, nadal potrzebuje on własnych uprawnień do narzędzi.
[Poradnik dla agentów →](agents.md) · [Klienci MCP →](mcp-clients.md) · [Zdalny MCP →](remote-mcp.md) · [Automatyzacja JSON →](json-automation.md)

## Kiedy wybrać inne narzędzie

| Jeśli potrzebujesz | Rozważ |
|---|---|
| Jednej lokalnej usługi na swoich urządzeniach, z już działającym klientem Tailscale | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Usług zarządzanych przez administratora ze stałymi nazwami na wielu hostach | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Publicznego URL-a dla webhooka lub demonstracji API bez konta Tailscale | [ngrok](https://ngrok.com/docs/start) lub [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Instalowania i uruchamiania samodzielnie hostowanych aplikacji, poza ich udostępnianiem | [Umbrel](https://umbrel.com) lub [Coolify](https://coolify.io) |
| Platformy dostępu opartej na tożsamości dla całej organizacji | [Pangolin](https://github.com/fosrl/pangolin) lub [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink pasuje, gdy jedna osoba uruchamia kilka aplikacji i chce przyznawać czasowy dostęp osobno do każdej aplikacji i osobie, z możliwością kontroli przez siebie i agenta.

## Jak to działa

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database i Model to osobne nazwane węzły w jednej sieci tailnet, uruchamiane przez jeden demon TSLink na komputerze udostępniającym usługi." width="720">
</picture>

Jeden demon w tle uruchamia wbudowany węzeł Tailscale dla każdej aplikacji, nadając jej własną nazwę i adres. Dla prywatnego HTTP i plików dostęp kontrolują `WhoIs` oraz uprawnienia osób lub reguły `--allow`; terminy są sprawdzane przy każdym żądaniu. Surowe TCP korzysta z polityki tailnet i uwierzytelniania zaplecza. Tailscale zapewnia transport tailnet, szyfrowanie i certyfikaty; TSLink jest niezależnym projektem. Wszystkie aplikacje współdzielą komputer udostępniający, więc TSLink nie izoluje ich od siebie. [Architektura →](architecture.md)

## Stan

Już dostępne: prywatne adresy aplikacji, wskazane osoby z terminami i pakietami zaproszeń, publiczny Funnel z wygaśnięciem, sprawdzanie kondycji aplikacji i alerty, przepisy dla aplikacji hostowanych samodzielnie, limity żądań dla aplikacji, restart po awarii w Windows, CLI i MCP. Dostępne są też: linki gościnne do przeglądarki, elastyczne okresy, dziennik dostępu, strona główna aplikacji, ograniczone role agentów, wdrażanie przez QR i wnioski o dostęp.

Wspólna lista dla wielu komputerów jest planowana. [Plan rozwoju →](roadmap.md)

## Dokumentacja i licencja

[Pierwsze kroki](getting-started.md) · [Opis CLI](cli-reference.md) · [Platformy](platforms.md) · [Modele lokalne](local-ai.md) · [Współtworzenie](../CONTRIBUTING.md) · [Bezpieczeństwo](../SECURITY.md)

Apache License 2.0 obejmuje także użytek komercyjny. Przy redystrybucji zachowaj [NOTICE](../NOTICE) i [informacje o podmiotach trzecich](../THIRD_PARTY_NOTICES.md). Warunki i plany Tailscale obowiązują osobno.
