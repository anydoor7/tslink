<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Korzystaj ze swoich aplikacji i zarządzaj nimi z dowolnego miejsca.<br>Zachowaj je prywatne lub udostępniaj na własnych zasadach.</strong></p>

Twoje aplikacje na komputerze lub serwerze w chmurze: korzystaj z szyfrowanej sieci prywatnej albo świadomie wybierz linki dla gości w przeglądarce lub dostęp publiczny. Zarządzaj samodzielnie lub przez agenta.

<p align="center"><a href="#quickstart">Szybki start</a> · <a href="#agents">Dla agentów</a> · <a href="#documentation">Dokumentacja</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <strong>Polski</strong> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Twoje aplikacje pod ręką

| Potrzeba | Możliwości TSLink |
|---|---|
| Własne aplikacje na różnych urządzeniach | Prywatne adresy domowych paneli, lokalnych stron WWW, plików, API modeli i usług TCP na komputerze lub serwerze. |
| Udostępnianie konkretnym osobom | Wybrane aplikacje HTTP/plikowe, zweryfikowana tożsamość Tailscale, termin ważności i cofanie dostępu. Odbiorcy używają Tailscale. [Osoby](people.md) |
| Wizyta przez przeglądarkę | Wygasające linki z opcjonalnym PIN-em do aplikacji proxy HTTP lub jawnie publiczny HTTPS przez Funnel. Link można przekazać dalej; nie potwierdza tożsamości. [Goście](guest-links.md) |
| Zarządzanie zbiorem aplikacji | Lista na każdym hoście, prywatny portal, kontrola stanu i alerty, historia dostępu oraz CLI/MCP z rolami agentów, zakresem aplikacji i zapisami audytowymi. [Portal](portal.md) · [Uprawnienia MCP](mcp-scopes.md) |

[Receptury aplikacji](apps.md), [limity wysyłania](sharing.md), [elastyczne okresy dostępu](durations.md) oraz [wdrażanie przez QR i prośby o dostęp](requests.md) ułatwiają utrzymanie. Funkcje są dostępne w tym kodzie źródłowym.

<a id="installation"></a>
<a id="quickstart"></a>

## Szybki start

Zainstaluj ze źródeł przy użyciu **Git i Go 1.26.6+**. Gotowe wydania i Homebrew nie zostały jeszcze opublikowane. Polecenia używają bash/zsh. [Konfiguracja macOS, Linux i Windows](platforms.md)

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
