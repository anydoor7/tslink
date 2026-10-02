<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="logo TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Nadaj swoim lokalnym aplikacjom, modelom i plikom własny prywatny adres.</strong><br>
  Otwieraj je z innego uprawnionego urządzenia w swojej sieci Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Licencja: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 lub nowszy"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: wbudowane węzły tsnet"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 narzędzi"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <strong>Polski</strong> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Instalacja

Potrzebujesz **Go 1.26.6+** i Git. Gotowe wydania i pakiet
Homebrew cask nie zostały opublikowane; zainstaluj program ze źródeł. Przykłady używają
**bash lub zsh**; wymagania dla Windows i usług działających w tle znajdziesz w [obsłudze platform](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Użyj konta Tailscale z [włączonymi MagicDNS i HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
Urządzenie odbierające musi być zalogowane do Twojej sieci Tailscale (**tailnet**), a jej polityka
musi zezwalać na dostęp do usługi. TSLink zawiera wbudowany Tailscale na hoście udostępniającym.

### Udostępnij swoją pierwszą stronę

Utwórz stronę; TSLink udostępni ją bezpośrednio i w razie potrzeby uruchomi swoją usługę w tle:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Jeśli TSLink wyświetli URL rejestracji, otwórz go, aby autoryzować węzeł; Twoja sieć tailnet może też
wymagać zatwierdzenia urządzenia przez administratora. Następnie pobierz dokładny adres:

```bash
tslink url demo --wait
```

Otwórz ten URL na uprawnionym urządzeniu. Pierwsze udostępnienie nie wymaga tokena API.
[Pełna konfiguracja i szczegóły cyklu życia →](getting-started.md)

<a id="use-cases"></a>

## Co udostępnisz?

Pliki muszą istnieć; aplikacje, bazy danych i zaplecza modeli muszą już działać na wskazanych portach.

| Zastosowanie | Polecenie |
|---|---|
| Otwórz lokalną aplikację z innego urządzenia | `tslink share 3000` |
| Przeglądaj katalog plików | `tslink share ./public --name files` |
| Czytaj wygenerowany raport HTML na telefonie | `tslink share ./report.html --name report` |
| Połącz się z lokalną bazą danych przez TCP | `tslink add database --tcp localhost:5432` |
| Użyj API HTTP lokalnego modelu, np. Ollama | `tslink add model --proxy localhost:11434` |

Dla Ollama pobierz dokładny URL poleceniem `tslink url model --wait`; wartość `baseURL`
klienta zgodnego z OpenAI to ten URL z dopisanym `/v1`. [Lokalne modele i przepływy pracy z prywatnymi danymi →](local-ai.md)

Dla wielu aplikacji na jednym hoście TSLink łączy nazwane węzły usług, listy dozwolonych tożsamości HTTP, wygasanie Funnel i zarządzanie MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) może wystarczyć do jednej aplikacji na własnych urządzeniach.

<a id="architecture"></a>

## Architektura

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Przykładowa mapa usług: App, Docs, Database i Model to odrębne nazwane węzły w jednej sieci tailnet. Aplikacje, pliki i API modeli używają HTTPS; baza danych używa prywatnego TCP." width="960">
</picture>

**Jedna sieć tailnet, odrębne węzły usług.** Wspólny demon uruchamia jeden wbudowany węzeł tsnet na
usługę, przekazując HTTP, udostępniając pliki lub pośrednicząc w TCP. Zmiany rejestru obowiązują podczas
jego działania. Każdy węzeł ma własną tożsamość sieciową; usługi współdzielą host udostępniający.
[Szczegóły architektury →](architecture.md)

| Element | Rola |
|---|---|
| [Go](../go.mod) | Natywny program wiersza poleceń |
| [Tailscale tsnet](architecture.md) | Węzły usług i transport w sieci tailnet |
| [Cobra](https://github.com/spf13/cobra) | Polecenia i pomoc |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Transport dla agentów |
| Systemowy magazyn kluczy i menedżer usług użytkownika | Opcjonalne poświadczenia i działanie w tle |

Usługi pozostają w Twojej sieci tailnet, chyba że jawnie włączysz [publiczny Funnel](getting-started.md#more-examples).
Usługi HTTP i plików obsługują listy dozwolonych tożsamości (`WhoIs`, `--allow`); TCP korzysta z polityki tailnet i
uwierzytelniania samego zaplecza. Zobacz [granice udostępniania](sharing.md).

TSLink nie instaluje aplikacji, nie uruchamia modeli, nie izoluje procesów hosta ani nie agreguje wielu hostów. Sieć, szyfrowanie i HTTPS pochodzą od Tailscale; TSLink jest niezależnym projektem.

<a id="agents"></a>

## Dla agentów

**19 narzędzi MCP** pozwala agentowi udostępniać raporty, zarządzać usługami, pobierać URL-e i sprawdzać
konfigurację. Podłącz lokalnego klienta MCP do zainstalowanego programu:

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

MCP zarządza TSLink; aplikacje korzystają z API HTTP modelu do wnioskowania.
Konfigurację i automatyzację opisują [klienci MCP](mcp-clients.md), [zdalne MCP](remote-mcp.md) oraz
[przewodnik operacyjny dla agentów](../AGENTS.md).

Automatyzacja CLI obsługuje `--json` z `schema_version` równym `1`; sprawdź `tslink status --urls --json`. Lokalny MCP używa JSON-RPC przez stdio. Zobacz [automatyzację JSON](json-automation.md).

<a id="roadmap"></a>

## Nadchodzące funkcje

Elementy W trakcie scalania, W przeglądzie lub Planowane nie wchodzą w skład powyższej instalacji ze źródeł.

| Zastosowanie | Status |
|---|---|
| <!-- roadmap:people --> Przyznaj krewnemu dostęp do prywatnych aplikacji HTTP/plikowych na 3 dni i zbierz zaproszenia w jednej wiadomości; odbiorca nadal potrzebuje Tailscale. | W trakcie scalania |
| <!-- roadmap:health --> Sprawdzaj stan aplikacji i odbieraj alerty awarii lub wygaśnięcia przez opcjonalne polecenie albo webhook. | W trakcie scalania |
| <!-- roadmap:recipes --> Znajdź obsługiwane aplikacje na loopback i obejrzyj receptury aplikacji self-hosted przed udostępnieniem. | W trakcie scalania |
| <!-- roadmap:limits --> Ustaw wielkość wysyłania i limity czasu żądań każdej aplikacji HTTP dla dużych plików i wolnych klientów. | W trakcie scalania |
| <!-- roadmap:windows --> Uruchom ponownie demona Windows po awarii w trakcie zalogowanej sesji, korzystając z zaplanowanego zadania i wbudowanego nadzorcy. | W trakcie scalania |
| <!-- roadmap:access-log --> Sprawdź, kto otworzył którą aplikację w lokalnych logach dostępu, z trybami ścieżki `prefix`, `full` lub `off`. | W przeglądzie |
| <!-- roadmap:portal --> Otwórz jedną stronę główną z dozwolonymi aplikacjami i przekazaniem rejestracji właścicielom; odwiedzający nadal potrzebują Tailscale. | W przeglądzie |
| <!-- roadmap:mcp-scopes --> Nadaj agentowi rolę i zakres aplikacji, z zapisami audytu jego zmian. | W przeglądzie |
| <!-- roadmap:guest-links --> Pozwól gościowi otworzyć jedną aplikację HTTP w przeglądarce bez instalowania Tailscale, przez wygasający link i opcjonalny PIN, przez publiczny Funnel z kontrolą dostępu. | W przeglądzie |
| <!-- roadmap:durations --> Wybierz gotowe lub własne okresy od 1 godziny, z konfigurowalnym maksimum dla gości wynoszącym domyślnie 7 dni. | W przeglądzie |
| <!-- roadmap:requests --> Pomóż użytkownikom telefonów dołączyć przez kod QR i zatwierdź dostęp lub dodatkowy czas jedną czynnością. | W przeglądzie |
| <!-- roadmap:multi-host --> Przeglądaj aplikacje z wielu hostów w jednym spisie. | Planowane |

<a id="documentation"></a>

## Dokumentacja i licencja

[Pierwsze kroki](getting-started.md) · [Lokalne modele](local-ai.md) ·
[Dokumentacja CLI](cli-reference.md) · [Platformy](platforms.md) · [Plan rozwoju](roadmap.md)

Współtwórz projekt zgodnie z [CONTRIBUTING.md](../CONTRIBUTING.md); luki w zabezpieczeniach zgłaszaj według
[SECURITY.md](../SECURITY.md).

TSLink korzysta z niezmienionej [licencji Apache 2.0](../LICENSE), obejmującej użytek komercyjny.
Przy redystrybucji zachowaj mające zastosowanie [NOTICE](../NOTICE) i [informacje o podmiotach trzecich](../THIRD_PARTY_NOTICES.md).
[Współpraca komercyjna](../COMMERCIAL.md) jest dobrowolna i nie dodaje warunków licencji.
Warunki świadczenia usług i plany Tailscale obowiązują odrębnie.
