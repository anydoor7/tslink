<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink to otwartoźródłowe narzędzie CLI i serwer MCP, które nadaje każdej aplikacji na twoim komputerze prywatny adres <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, dzięki czemu ty albo twój coding agent możecie otworzyć na telefonie działający serwer deweloperski. Niezależny projekt, nie jest tworzony przez Tailscale.</p>
<p align="center">Twój telefon potrzebuje aplikacji Tailscale, zalogowanej do tego samego tailnetu.</p>
<p align="center"><strong>Polski</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Więcej języków</a></p>

```text
# Claude Code, po zainstalowaniu CLI (zobacz Instalacja poniżej)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Lokalni klienci MCP używający JSON z mcpServers (inni: zajrzyj do konfiguracji MCP swojego klienta)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Powiedz agentowi: „Udostępnij serwer deweloperski, żebym mógł go otworzyć na telefonie."
```

To samo ręcznie, przy założeniu, że serwer deweloperski już nasłuchuje na porcie 5173. Z `--json` polecenie `share` podaje zarejestrowaną nazwę w `data.name`. Używaj tej nazwy w kolejnych poleceniach: różni się od `--name`, gdy ta nazwa była zajęta, na przykład `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, albo data.auth_url (link do logowania w Tailscale), gdy status to needs_login
tslink url <name> --wait              # po zakończeniu logowania: dokładny URL
tslink remove <name>                  # gdy skończysz; pomiń, jeśli share się nie powiodło. Serwer deweloperski działa dalej
```

<a id="installation"></a>
<a id="quickstart"></a>

## Instalacja

Hosty macOS wymagają macOS 13 Ventura lub nowszego ([obsługiwane platformy](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

W systemie Windows zainstaluj przez Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Aby zaktualizować, uruchom `brew upgrade --cask tslink` lub `scoop update; scoop update tslink`. Jeśli TSLink działa jako usługa w tle, następnie ponownie uruchom `tslink install`.

Pakiety Linux `.deb` i `.rpm` oraz wersje dla Windows są w [najnowszym wydaniu](https://github.com/anydoor7/tslink/releases/latest). Gdy pierwszy raz udostępniasz aplikację, TSLink wyświetla dla niej link do logowania w Tailscale. [Pierwsze kroki](getting-started.md)

<a id="agents"></a>

## Dla agentów AI

Serwer deweloperski, który agent uruchamia na `localhost`, jest poza zasięgiem twojego telefonu. TSLink pozwala agentowi nadać mu prywatny adres, przekazać ci dokładny URL i usunąć udostępnienie po zakończeniu. Twój telefon potrzebuje aplikacji Tailscale, zalogowanej do tego samego tailnetu.

Przetestowano 2026-10-22 z Vite 8.3.5 i iPhonem 15 Pro: agent udostępnił serwer deweloperski przez MCP, telefon otworzył prywatny URL, a strona odświeżyła się po zapisie bez ręcznego przeładowania.

W Claude Code najpierw zainstaluj CLI, a potem uruchom:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Wtyczka zawiera [skill do podglądu](../skills/tslink-preview/SKILL.md) i poniższe połączenie MCP. Marketplace pozostaje w tym repozytorium i pobiera wtyczkę z [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Wzorcowy skill zostaje tutaj; wtyczka zawiera kopię sprawdzaną przez CI. Inni agenci mogą używać tego samego skilla albo łączyć się bezpośrednio przez MCP. Ta integracja wymaga lokalnego CLI i nie działa w czacie claude.ai. [Konfiguracja i rejestracja](agent-integrations.md)

Dla lokalnego klienta MCP używającego JSON z `mcpServers` (inni klienci: zajrzyj do ich instrukcji konfiguracji MCP i [przewodnika po klientach MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Brama domyślna w nowych instalacjach.** Każda prywatna aplikacja HTTP/plikowa dostaje stały port, np. `https://home.<tailnet>.ts.net:8443/`. Istniejące instalacje zachowują tryb, a aplikacje własne węzły/URL; `--portal-only` wymaga jawnego wyboru.
- **Zarejestruj bramę.** Bez poświadczeń zweryfikowane własne konto bramy bez tagów może zostać owner. Przy tagach/OAuth uruchom lokalnie `tslink portal enable --owner <login>`. Oczekująca rejestracja węzłów aplikacji nie blokuje gotowej trasy bramy.
- **Dostęp dla osób.** Nowe aplikacje pozwalają na dostęp owner/admin i jawnie uprawnionym osobom. `people add`, `add --allow` oraz MCP `add/share.allow` sprawdzają członkostwo i przygotowują wymagane zaproszenia. Członkowie nie potrzebują zaproszenia do urządzenia; osoby zewnętrzne akceptują jedno zaproszenie do bramy. Błędy katalogu/API zwracają nieukończoną konfigurację.
- **Edytujesz, zapisujesz, widzisz.** Zapisanie kodu odświeża stronę na telefonie. Reguły Host i Origin twojej aplikacji nadal mogą blokować żądania: w naszym teście Vite nie wymagał zmiany `allowedHosts`; Next.js nie był testowany i nadal wymaga `allowedDevOrigins`.
- **Sprzątanie.** Agent wywołuje `unshare` albo ty uruchamiasz `tslink remove <name>`, zawsze z nazwą zwróconą w wyniku share. Usunięcie wyrejestrowuje udostępnienie i próbuje usunąć jego węzeł w tailnecie, gdy TSLink potrafi udowodnić, że jest jego właścicielem, więc sprawdź w wyniku pola dotyczące czyszczenia lub zachowanego stanu. Nie zatrzymuje serwera deweloperskiego.
- **CLI lub MCP.** Polecenia zarządzania przyjmują `--json` i zwracają wersjonowane wyniki; `tslink mcp` udostępnia operacje na aplikacjach i zarządzanie dostępem przez MCP.
- **Jeden plik, nie folder.** Agent może udostępnić tylko swój samodzielny raport HTML poleceniem `tslink share ./report.html`; pozostałe pliki z tego folderu pozostają niedostępne.
- **Ograniczone role.** `viewer`, `app-operator` lub `people-manager`, ograniczone do aplikacji, które wskażesz; udostępnienie nowej aplikacji wymaga roli `owner`. `tslink mcp-audit` pokazuje, co agent zmienił. Role ograniczają narzędzia TSLink, a nie własną powłokę agenta.

[Przewodnik agenta](agent-quickstart.md) · [Uprawnienia MCP](mcp-scopes.md) · [Zdalny MCP](remote-mcp.md)

<a id="why"></a>

## Kiedy przydaje się TSLink

Do jednej aplikacji, którą uruchamiasz sam i otwierasz na własnym telefonie, wystarczy Serve. TSLink jest dla serwerów deweloperskich uruchamianych przez agenta, często kilku, gdy każdy ma dostać własną prywatną nazwę, a udostępnienie, URL i sprzątanie mają być gotowymi krokami w CLI, MCP i skillu Claude Code.

| Zadanie | Sam Tailscale | TSLink |
|---|---|---|
| Kilka serwerów deweloperskich uruchamianych przez agenta, po jednej nazwie na każdy | Serve ustawia trasy na poziomie urządzenia pod jedną nazwą na maszynę; Services wymaga dla każdej nazwy definicji, zatwierdzenia i hosta z tagami | Jeden węzeł na aplikację, z udostępnianiem, sprawdzaniem URL i sprzątaniem gotowymi w CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) i skillu |
| Jedna aplikacja uruchomiona przez ciebie, na telefonie | Wystarczy `tailscale serve 3000` | `tslink share 3000` |
| Jedna osoba, jedna aplikacja, siedem dni | Reguły zasad, potem narzędzie JIT albo ręczne usunięcie | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/pliki) |
| Link w przeglądarce, trzy dni | Publiczny Funnel; kontrolę dostępu i zaplanowane wyłączenie trzeba dodać samemu | `tslink guest create photos --for 3d --public --print-link` (tylko HTTP) |

Prywatni odbiorcy potrzebują Tailscale. Linki dla gości są publiczne, można je przekazywać dalej i służą jako dane dostępowe.

[Pełne porównanie](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Twoje aplikacje na twoich urządzeniach

- **Adres dla każdej aplikacji.** Aplikacje webowe, foldery, pojedyncze pliki i porty TCP dostają własne nazwy w twoim tailnecie, więc używasz nazw zamiast adresów IP.
- **Domyślnie prywatne.** Nic nie jest publiczne, dopóki nie utworzysz linku dla gościa albo nie opublikujesz przez Funnel.
- **Jedna aplikacja, nie cała maszyna.** Każda publikowana aplikacja dostaje własny węzeł, który przekazuje ruch tylko do niej. Bez aplikacji Tailscale na hoście TSLink nie dodaje do twojego tailnetu żadnych innych portów hosta.
- **Strona startowa** z listą twoich aplikacji i ich stanem. [Portal](portal.md)
- **Kontrole stanu i alerty** przez polecenie lub webhook oraz dziennik dostępu, w którym widać też odrzucone żądania. [Stan i alerty](health-and-alerts.md) · [Historia dostępu](access-log.md)
- **Receptury dla 15 aplikacji self-hosted**, m.in. Home Assistant, Jellyfin, Immich i Ollama. `tslink apps detect` znajduje te, które już działają. [Receptury aplikacji](apps.md)

## Udostępniaj, kiedy chcesz

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Linki dla gości i nowe publiczne adresy URL wygasają i działają tylko dla aplikacji webowych; foldery, pliki i porty TCP pozostają prywatne. [Osoby](people.md) · [Linki dla gości](guest-links.md) · [Dostęp publiczny](funnel.md)

<a id="architecture"></a>

## Jak to działa

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Jeden komputer lub host w chmurze: CLI/MCP zarządza wspólnym demonem i węzłami aplikacji. Urządzenia prywatne korzystają z szyfrowania Tailscale; opcjonalny publiczny HTTPS/Funnel prowadzi do aplikacji HTTP przez kontrolę gości lub jawną publikację." width="960">
</picture>

Jeden proces w tle uruchamia osobny węzeł Tailscale dla każdej aplikacji. Tailscale zapewnia transport w tailnecie i certyfikaty HTTPS. Prywatny dostęp do stron i plików można ograniczyć według tożsamości Tailscale za pomocą `--allow` i uprawnień dla osób; surowy TCP opiera się na zasadach twojego tailnetu i własnym logowaniu aplikacji. [Architektura](architecture.md)

<a id="requirements"></a>

## Wymagania

| Kto | Czego potrzebuje |
|---|---|
| Ty | Konta Tailscale z włączonym MagicDNS i HTTPS |
| Maszyna z twoimi aplikacjami | TSLinka, który ma wbudowany Tailscale (w Linuksie także sesji użytkownika systemd) |
| Twoje urządzenia i osoby, którym udostępniasz | Aplikacji Tailscale |
| Goście | Przeglądarki |

Nazwy aplikacji HTTPS pojawiają się w publicznych logach certyfikatów, więc wybieraj nazwy, które mogą zobaczyć inni.

<a id="documentation"></a>

## Więcej

[Cała dokumentacja](INDEX.md) · [Opis CLI](cli-reference.md) · [Porównanie z Serve, ngrok i Cloudflare](comparison.md) · [Współtworzenie](../CONTRIBUTING.md) · [Bezpieczeństwo](../SECURITY.md)

Apache 2.0. TSLink to niezależny projekt, nie jest tworzony ani wspierany przez Tailscale.
