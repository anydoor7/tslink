<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Daj każdej aplikacji na Twoim komputerze lub serwerze własny prywatny adres w Twojej sieci Tailscale i decyduj, kto może się z nią połączyć.</strong></p>

Otwieraj swoje aplikacje webowe, foldery, API modeli i bazy danych z własnego telefonu i laptopa, z kontrolą stanu i historią dostępu dla każdej z nich. Twoi agenci AI też mogą je publikować i sprawdzać w ramach roli, którą im nadasz. Gdy ktoś inny potrzebuje dostępu, przyznaj go wybranej osobie do określonej daty albo otwórz aplikację webową na publiczny internet na ograniczony czas.

**Wymaga Tailscale.** Potrzebujesz konta Tailscale (bezpłatnego do użytku osobistego), a każde urządzenie, które otwiera prywatną aplikację, potrzebuje aplikacji Tailscale; goście i publiczni odwiedzający potrzebują tylko przeglądarki. TSLink to niezależny projekt, który nie jest tworzony ani popierany przez Tailscale. [Wymagania](#requirements)

<p align="center"><a href="#quickstart">Szybki start</a> · <a href="#agents">Dla agentów</a> · <a href="comparison.md">Porównanie z Serve, ngrok i Cloudflare</a> · <a href="#documentation">Dokumentacja</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <strong>Polski</strong> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Co możesz zrobić

### Dostęp do własnych aplikacji

- **Adres dla każdej aplikacji.** `tslink share 3000`, `tslink share ./photos` lub `tslink add db --tcp localhost:5432` nadaje aplikacji webowej, folderowi, plikowi lub usłudze TCP własny prywatny adres w Twoim tailnecie (Twojej prywatnej sieci Tailscale), np. `https://photos.<tailnet>.ts.net`. Każda aplikacja jest osobnym urządzeniem Tailscale, więc otwierasz aplikacje po nazwie, bez pamiętania adresu IP i portu.
- **Prywatne, dopóki nie zdecydujesz inaczej.** Aplikacje pozostają w Twoim tailnecie, a jego polityka decyduje, które urządzenia mogą się łączyć. Nic nie trafia do publicznego internetu, dopóki nie utworzysz linku gościa lub nie opublikujesz aplikacji.
- **Wszystko w jednym miejscu.** `tslink status --urls` wyświetla wszystkie aplikacje na tym komputerze, a opcjonalna prywatna strona startowa pokazuje adres i stan każdej z nich. [Portal](portal.md)
- **Wiesz, kiedy coś przestaje działać.** Kontrole stanu w tle mogą powiadomić Cię poleceniem lub webhookiem, gdy aplikacja przestaje działać lub wraca, albo gdy jej logowanie do Tailscale wkrótce wygaśnie. Historia dostępu pokazuje, kto i kiedy otworzył którą aplikację, łącznie z odrzuconymi żądaniami. [Stan i alerty](health-and-alerts.md) · [Historia dostępu](access-log.md)
- **Popularne aplikacje gotowe do użycia.** Receptury obejmują 15 aplikacji self-hosted, w tym Home Assistant, Jellyfin, Immich i Ollama, a `tslink apps detect` znajduje aplikacje, które już działają. Aplikacje do zdjęć i wideo dostają [limity wysyłania](sharing.md) dopasowane do dużych plików. [Receptury aplikacji](apps.md) · [Lokalna AI](local-ai.md)

### Pozwól agentom z nimi pracować

Agent, który uruchamia serwer deweloperski, podgląd lub lokalne API modelu, zostawia je na `localhost`, gdzie Twój telefon i inne komputery nie mogą ich otworzyć. TSLink pozwala agentowi opublikować je prywatnie, podać Ci dokładny adres i potem je zdjąć, w granicach, które ustalisz.

- **Udostępnij, sprawdź, cofnij.** `share` zwraca zarejestrowaną nazwę oraz dokładny URL albo link logowania, który musisz otworzyć. `url --wait` i `status` informują, kiedy aplikacja działa, a `remove` (w MCP `unshare`) ją zdejmuje. [Przewodnik agenta](agent-quickstart.md)
- **Zbudowane do automatyzacji.** Polecenia przyjmują `--json` i zwracają wersjonowany wynik ze stabilnymi kodami błędów. `tslink mcp` udostępnia te same operacje lokalnemu klientowi MCP, a `tslink serve --mcp` agentom na Twoich innych urządzeniach przez tailnet. [Automatyzacja JSON](json-automation.md) · [Zdalny MCP](remote-mcp.md)
- **Ograniczone uprawnienia.** Agent, którego uruchamiasz sam, działa jako właściciel. Innym agentom nadaj ograniczoną rolę (`viewer`, `app-operator` lub `people-manager`), która obejmuje tylko wskazane przez Ciebie aplikacje i ogranicza, jak długo może trwać każdy przyznany przez nie dostęp. Zmiany wprowadzone przez MCP są zapisywane, a `tslink mcp-audit` je pokazuje. Role ograniczają narzędzia TSLink, a nie powłokę ani pliki samego agenta. [Uprawnienia MCP](mcp-scopes.md)

### Udostępniaj wybranym osobom

- **Konkretne osoby, do określonej daty.** `tslink people add alice@example.com --apps photos,notes --for 7d` pozwala temu kontu Tailscale otwierać te aplikacje webowe i plikowe do terminu. `people update`, `extend` i `people remove` zmieniają lub kończą dostęp; po usunięciu następne żądanie tej osoby zostanie odrzucone, ale tego, co już pobrała, nie da się cofnąć. [Osoby](people.md) · [Okresy dostępu](durations.md)
- **Ktoś spoza Twojego tailnetu.** Dodaj `--invite --print-links`, aby dostać jedną wiadomość gotową do wysłania, z zaproszeniem urządzenia dla każdej aplikacji (wymaga to tokenu API należącego do użytkownika). `--qr` wyświetla kod do konfiguracji telefonu.
- **Prośby.** Osoby w Twoim tailnecie mogą ze strony startowej poprosić o więcej czasu albo o dostęp do aplikacji, którą oznaczysz jako dostępną na prośbę. Zatwierdzasz jednym poleceniem z podanym okresem. [Prośby o dostęp](requests.md)

### Otwórz aplikację webową na internet, na jakiś czas

- **Linki gościa.** `tslink guest create photos --for 3d --public --print-link` tworzy link przeglądarkowy do jednej aplikacji webowej, opcjonalnie z PIN-em, który możesz cofnąć osobno. Goście nie potrzebują konta Tailscale. Każdy, kto ma link, może z niego skorzystać, więc nie potwierdza on, kto odwiedził aplikację. [Goście](guest-links.md)
- **Otwarty publiczny URL.** `tslink add preview --proxy localhost:3000 --funnel --public` publikuje aplikację webową dla każdego, kto zna jej URL. Wygasa po 24 godzinach, chyba że ustawisz inny czas przez `--funnel-ttl`. [Funnel](funnel.md)
- Oba działają przez Tailscale Funnel, zawsze wygasają (od 1 godziny do 7 dni, chyba że podniesiesz limit) i działają tylko dla aplikacji webowych. Foldery, pliki i usługi TCP pozostają prywatne.

Wszystko powyższe jest dostępne w wersji v0.1.0.

<a id="requirements"></a>

## Wymagania

TSLink działa na Tailscale. To niezależny projekt, który nie jest tworzony ani popierany przez Tailscale; obowiązują własne warunki i [plany](https://tailscale.com/pricing) Tailscale.

| Kto | Czego potrzebuje |
|---|---|
| Ty | Konta Tailscale z włączonymi [MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Bezpłatny plan Personal jest przeznaczony do użytku niekomercyjnego. |
| Komputer lub serwer, na którym działają Twoje aplikacje | Tylko TSLink. Zawiera Tailscale, więc nie trzeba go instalować osobno. Każda nowa aplikacja prosi o logowanie w przeglądarce oraz o zatwierdzenie urządzenia, jeśli wymaga tego Twój tailnet. |
| Twoje inne urządzenia | Aplikacji Tailscale zalogowanej do Twojego tailnetu. |
| Wybrane przez Ciebie osoby | Aplikacji Tailscale i własnego loginu. Albo dołączają do Twojego tailnetu, co dodaje użytkownika do Twojego planu, albo przyjmują zaproszenie urządzenia dla każdej aplikacji. Polityka Twojego tailnetu musi zezwalać im na dostęp. |
| Goście i publiczni odwiedzający | Przeglądarki. Twój tailnet musi zezwalać na Funnel, który Tailscale nadal oznacza jako beta. |

Włączenie HTTPS publikuje nazwę Twojego tailnetu i nazwy urządzeń, w tym nazwę każdej aplikacji, w publicznym logu certyfikatów, więc wybieraj takie nazwy aplikacji, które mogą zobaczyć inni.

<a id="installation"></a>
<a id="quickstart"></a>

## Szybki start

Na macOS i Linuksie zainstaluj przez Homebrew. Plik binarny dla macOS jest podpisany certyfikatem Developer ID i przeszedł notaryzację Apple. Aby później zaktualizować, uruchom `brew upgrade --cask tslink`, a następnie ponownie `tslink install`, jeśli TSLink działa jako usługa w tle.

```bash
brew install --cask anydoor7/tap/tslink
```

W systemie Windows pobierz `tslink_<version>_windows_<arch>.zip` z [najnowszego wydania](https://github.com/anydoor7/tslink/releases/latest), sprawdź go względem `checksums.txt` i uruchom `tslink install`, aby TSLink startował po zalogowaniu. Archiwum zip nie ma podpisu Authenticode; [zweryfikuj wydanie](verify-release.md) za pomocą podpisanych sum kontrolnych i atestacji. Pakiety `.deb` i `.rpm` dla Linuksa są na tej samej stronie wydania. Do kompilacji ze źródeł potrzebujesz **Git i Go 1.26.6+**. Polecenia poniżej używają bash/zsh; szczegóły: [Konfiguracja macOS, Linux i Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Potrzebujesz **konta Tailscale** oraz [MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Urządzenia odbierające prywatnie wymagają Tailscale i zezwolenia polityki sieci. TSLink zawiera Tailscale na hoście aplikacji.

Gdy aplikacja działa już na porcie 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Wybierz wolną nazwę; jeśli `share` zwróci inną, użyj jej w `url`. Najpierw dokończ wskazaną rejestrację w przeglądarce i zatwierdzenie urządzenia, a potem otwórz dokładny URL na uprawnionym urządzeniu. `share` uruchamia usługę w tle w razie potrzeby. Pierwszy prywatny dostęp nie wymaga tokenu API administratora. Pliki udostępnisz przez `tslink share ./report.html`; muszą istnieć, a aplikacje muszą działać. [Pełna konfiguracja](getting-started.md)

Gdy wszystko działa i jest przydatne, możesz [dać TSLink gwiazdkę](https://github.com/anydoor7/tslink), by inni łatwiej go znaleźli. To całkowicie dobrowolne.

<a id="architecture"></a>

## Jak to działa

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Jeden komputer lub host w chmurze: CLI/MCP zarządza wspólnym demonem i węzłami aplikacji. Urządzenia prywatne korzystają z szyfrowania Tailscale; opcjonalny publiczny HTTPS/Funnel prowadzi do aplikacji HTTP przez kontrolę gości lub jawną publikację." width="960">
</picture>

Wyobraź sobie prywatną, szyfrowaną drogę do aplikacji. **Tailscale zapewnia transport sieciowy i HTTPS; TSLink zarządza dostępem na każdym hoście.** Jeden demon uruchamia osobny wbudowany węzeł dla każdej usługi. Portal pokazuje dozwolone aplikacje; stan i historia dostępu pomagają je utrzymać.

Dostęp publiczny wymaga włączenia: gość potrzebuje linku i ewentualnego PIN-u; otwarty Funnel jest osiągalny dla każdego z URL-em. Oba używają publicznego HTTPS, nie prywatnej tożsamości użytkownika. Surowy TCP pozostaje prywatny i zależy od reguł tailnetu oraz uwierzytelniania backendu. TSLink nie instaluje aplikacji, nie izoluje procesów, nie tworzy VPC w chmurze ani nie agreguje hostów. Niezależny projekt współpracujący z Tailscale. [Architektura i granice](architecture.md)

<a id="agents"></a>

## Dla agentów

Zarządzaj listą, stanem, URL-ami i dostępem przez CLI/MCP. Zacznij od [przewodnika agenta](agent-quickstart.md), przeczytaj bieżące schematy narzędzi i sprawdź rzeczywisty dostęp przed ogłoszeniem sukcesu.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

Automatyzacja CLI używa `--json`; MCP używa JSON-RPC przez stdio. [Klienci](mcp-clients.md) · [Zdalny MCP](remote-mcp.md) · [Role i zakresy](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Dokumentacja i licencja

[Wszystkie poradniki](INDEX.md) · [Opis CLI](cli-reference.md) · [Lokalna AI](local-ai.md) · [Stan](health-and-alerts.md) · [Historia dostępu](access-log.md) · [Plan rozwoju](roadmap.md)

Lista obejmująca wiele hostów jest planowana. Zapraszamy do [współtworzenia](../CONTRIBUTING.md) i [zgłaszania luk](../SECURITY.md). [Apache 2.0](../LICENSE) pozwala na użytek komercyjny; przy redystrybucji zachowaj [NOTICE](../NOTICE) i [informacje o podmiotach trzecich](../THIRD_PARTY_NOTICES.md). [Współpraca komercyjna](../COMMERCIAL.md) jest dobrowolna. Warunki i plany Tailscale obowiązują osobno.
