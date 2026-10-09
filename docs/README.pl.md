<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Prywatne adresy dla twoich aplikacji, w twojej sieci Tailscale.</strong></p>
<p align="center">Otwieraj je na własnych urządzeniach. Udostępnij jedną osobie lub przez link, do wybranej przez siebie daty.</p>
<p align="center"><strong>Polski</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Więcej języków</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
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

<a id="why"></a>

## Kiedy przydaje się TSLink

Do jednej aplikacji na własnych urządzeniach wystarczy Serve. TSLink łączy adresy aplikacji, terminy i zmiany dostępu w jednym procesie.

| Zadanie | Sam Tailscale | TSLink |
|---|---|---|
| Jedna aplikacja webowa na telefonie | Wystarczy `tailscale serve 3000` | `tslink share 3000` |
| Kilka aplikacji, każda z własną nazwą | Konfiguracja Services albo osobne węzły | Jedno `share`/`add` na aplikację; zarejestruj każdy węzeł |
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

<a id="agents"></a>

## Dla agentów AI

Serwer deweloperski, który agent uruchamia na `localhost`, jest poza zasięgiem twojego telefonu. TSLink pozwala agentowi nadać mu prywatny adres, podać dokładny URL i usunąć go po zakończeniu.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI lub MCP.** Polecenia zarządzania przyjmują `--json` i zwracają wersjonowane wyniki; `tslink mcp` udostępnia operacje na aplikacjach i zarządzanie dostępem przez MCP.
- **Jeden plik, nie cały folder.** Agent może udostępnić tylko swój raport HTML przez `tslink share ./report.html`; pozostałe pliki w tym folderze pozostają niedostępne.
- **Ograniczone role.** `viewer`, `app-operator` lub `people-manager`, zawężone do wskazanych aplikacji. `tslink mcp-audit` pokazuje, co agent zmienił. Role ograniczają narzędzia TSLink, a nie własną powłokę agenta.

[Przewodnik agenta](agent-quickstart.md) · [Uprawnienia MCP](mcp-scopes.md) · [Zdalny MCP](remote-mcp.md)

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
