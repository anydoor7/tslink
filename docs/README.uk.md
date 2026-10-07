<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Приватні адреси для ваших застосунків у вашій мережі Tailscale.</strong></p>
<p align="center">Відкривайте їх зі своїх пристроїв. Діліться з людиною чи за посиланням до дати, яку оберете.</p>
<p align="center"><strong>Українська</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Інші мови</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Встановлення

```sh
brew install --cask anydoor7/tap/tslink
```

Пакети `.deb` і `.rpm` для Linux та збірки для Windows є в [останньому релізі](https://github.com/anydoor7/tslink/releases/latest). Коли ви вперше ділитеся застосунком, TSLink показує для нього посилання для входу в Tailscale. [Початок роботи](getting-started.md)

<a id="use-cases"></a>

## Ваші застосунки на ваших пристроях

- **Окрема адреса для кожного застосунку.** Вебзастосунки, теки, окремі файли й TCP-порти отримують власні імена у вашому tailnet, тож ви відкриваєте їх за іменем, а не за IP-адресою та портом.
- **Приватно за замовчуванням.** Нічого не стає публічним, доки ви не створите гостьове посилання або не опублікуєте через Funnel.
- **Домашня сторінка** зі списком ваших застосунків та їхнім станом. [Портал](portal.md)
- **Перевірки стану й сповіщення** через команду або webhook, а також журнал доступу, де видно й відхилені запити. [Стан і сповіщення](health-and-alerts.md) · [Історія доступу](access-log.md)
- **Рецепти для 15 self-hosted застосунків**, серед них Home Assistant, Jellyfin, Immich і Ollama. `tslink apps detect` знаходить ті, що вже запущені. [Рецепти застосунків](apps.md)

## Діліться, коли захочете

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Гостьові посилання й публічні URL завжди спливають і працюють лише для вебзастосунків; теки, файли й TCP-порти лишаються приватними. [Люди](people.md) · [Гостьові посилання](guest-links.md) · [Публічний доступ](funnel.md)

<a id="agents"></a>

## Для ШІ-агентів

Dev-сервер, який агент запустив на `localhost`, недоступний з вашого телефона. TSLink дає агентові змогу надати йому приватну адресу, повідомити точну URL-адресу й прибрати її після роботи, у межах ролі, яку ви оберете.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI або MCP.** Команди керування приймають `--json` і повертають версіоновані результати; `tslink mcp` надає ті самі операції через MCP.
- **Обмежені ролі.** `viewer`, `app-operator` або `people-manager`, лише для застосунків, які ви вкажете. `tslink mcp-audit` показує, що змінив агент. Ролі обмежують інструменти TSLink, а не власну оболонку агента.

[Посібник агента](agent-quickstart.md) · [Права MCP](mcp-scopes.md) · [Віддалений MCP](remote-mcp.md)

<a id="architecture"></a>

## Як це працює

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Один ПК або хмарний сервер: CLI/MCP керує спільним демоном і вузлами застосунків. Приватні пристрої використовують шифрований Tailscale; необов'язковий публічний HTTPS/Funnel веде до HTTP-застосунків через гостьову перевірку або явну відкриту публікацію." width="960">
</picture>

Один фоновий процес запускає окремий вузол Tailscale для кожного застосунку. Tailscale забезпечує транспорт у tailnet і HTTPS-сертифікати. Приватний доступ до вебзастосунків і файлів можна обмежити за посвідченням Tailscale через `--allow` і доступ для людей; чистий TCP покладається на політику вашого tailnet і власний вхід застосунку. [Архітектура](architecture.md)

<a id="requirements"></a>

## Вимоги

| Хто | Що потрібно |
|---|---|
| Ви | Обліковий запис Tailscale з увімкненими MagicDNS і HTTPS |
| Машина, де працюють ваші застосунки | Лише TSLink, у який вбудовано Tailscale |
| Ваші пристрої та ті, з ким ви ділитеся | Застосунок Tailscale |
| Гості | Браузер |

Імена застосунків потрапляють у публічні журнали сертифікатів, тож обирайте імена, які можна показати іншим.

<a id="documentation"></a>

## Більше

[Уся документація](INDEX.md) · [Довідник CLI](cli-reference.md) · [Порівняння із Serve, ngrok і Cloudflare](comparison.md) · [Участь у проєкті](../CONTRIBUTING.md) · [Безпека](../SECURITY.md)

Apache 2.0. TSLink — незалежний проєкт, його не створювала й не схвалювала Tailscale.
