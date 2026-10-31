<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink — це CLI та MCP-сервер з відкритим кодом: він дає кожному застосунку на вашому комп'ютері приватну адресу <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, щоб ви або ваш coding agent могли відкрити запущений dev-сервер на телефоні. Незалежний проєкт, не створений Tailscale.</p>
<p align="center">На телефоні потрібен застосунок Tailscale з входом у той самий tailnet.</p>
<p align="center"><strong>Українська</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Інші мови</a></p>

```text
# Claude Code, після встановлення CLI (див. розділ «Встановлення» нижче)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Локальні MCP-клієнти, що використовують JSON з mcpServers (інші: див. налаштування MCP у вашому клієнті)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Скажіть агентові: «Поділися dev-сервером, щоб я міг відкрити його на телефоні».
```

Те саме вручну, коли dev-сервер уже слухає порт 5173. З прапорцем `--json` команда `share` повідомляє зареєстроване ім'я в `data.name`. Використовуйте це ім'я в наступних командах: воно відрізняється від `--name`, якщо таке ім'я було зайняте, наприклад `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, або data.auth_url (посилання для входу в Tailscale), якщо status дорівнює needs_login
tslink url <name> --wait              # після входу: точна URL-адреса
tslink remove <name>                  # коли завершите; пропустіть, якщо share не вдався. Dev-сервер продовжує працювати
```

<a id="installation"></a>
<a id="quickstart"></a>

## Встановлення

Для комп’ютерів macOS потрібна macOS 13 Ventura або новіша ([підтримувані платформи](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

У Windows установіть через Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Для оновлення виконайте `brew upgrade --cask tslink` або `scoop update; scoop update tslink`. Якщо TSLink працює як фонова служба, після оновлення знову виконайте `tslink install`.

Пакети `.deb` і `.rpm` для Linux та збірки для Windows є в [останньому релізі](https://github.com/anydoor7/tslink/releases/latest). Коли ви вперше ділитеся застосунком, TSLink показує для нього посилання для входу в Tailscale. [Початок роботи](getting-started.md)

<a id="agents"></a>

## Для ШІ-агентів

Dev-сервер, який агент запустив на `localhost`, недоступний з вашого телефона. TSLink дає агентові змогу надати йому приватну адресу, передати вам точну URL-адресу й прибрати спільний доступ після роботи. На телефоні потрібен застосунок Tailscale з входом у той самий tailnet.

Перевірено 2026-10-22 з Vite 8.3.5 та iPhone 15 Pro: агент відкрив спільний доступ до dev-сервера через MCP, телефон відкрив приватну URL-адресу, а сторінка оновилася після збереження без ручного перезавантаження.

Для Claude Code спершу встановіть CLI, потім виконайте:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

До плагіна входять [skill для попереднього перегляду](../skills/tslink-preview/SKILL.md) і MCP-підключення нижче. Маркетплейс лишається в цьому репозиторії й бере плагін з [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Основний skill зберігається тут; у плагіні лежить копія, яку перевіряє CI. Інші агенти можуть використовувати той самий skill або підключати MCP напряму. Для цієї інтеграції потрібен локальний CLI, у чаті claude.ai вона не працює. [Налаштування та реєстрація](agent-integrations.md)

Для локального MCP-клієнта, який використовує JSON з `mcpServers` (інші клієнти: див. їхні інструкції з налаштування MCP і [посібник із MCP-клієнтів](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Шлюз за замовчуванням для нових установок.** Кожен приватний HTTP/файловий застосунок отримує сталий порт, наприклад `https://home.<tailnet>.ts.net:8443/`. Наявні установки зберігають режим, а застосунки — власні вузли/URL; `--portal-only` обирається явно.
- **Зареєструйте шлюз.** Без облікових даних перевірений власний акаунт шлюзу без тегів може стати owner. Для tagged/OAuth виконайте локально `tslink portal enable --owner <login>`. Очікування реєстрації вузлів застосунків не блокує готовий маршрут шлюзу.
- **Доступ для людей.** Нові застосунки дозволяють owner/admin і явно авторизованих людей. `people add`, `add --allow` і MCP `add/share.allow` перевіряють членство та готують потрібні запрошення. Учасникам мережі запрошення пристрою не потрібне; зовнішні користувачі приймають одне запрошення шлюзу. Помилки каталогу/API повертають незавершений стан.
- **Редагуєте, зберігаєте, бачите.** Збереження коду оновлює сторінку на телефоні. Власні правила Host та Origin вашого застосунку все одно можуть блокувати запити: у нашому тесті Vite не вимагав змінювати `allowedHosts`; Next.js не тестувався й досі потребує `allowedDevOrigins`.
- **Очищення.** Агент викликає `unshare`, або ви запускаєте `tslink remove <name>`, завжди з іменем, яке повернув результат share. Команда видаляє запис про наданий доступ і намагається видалити відповідний вузол tailnet, коли TSLink може довести, що вузол належить йому, тож перевірте в результаті поля про очищення або збережений стан. Dev-сервер вона не зупиняє.
- **CLI або MCP.** Команди керування приймають `--json` і повертають версіоновані результати; `tslink mcp` надає операції із застосунками й доступом через MCP.
- **Один файл, а не тека.** Агент може поділитися лише своїм самодостатнім HTML-звітом командою `tslink share ./report.html`; решта файлів у цій теці лишаються недоступними.
- **Обмежені ролі.** `viewer`, `app-operator` або `people-manager`, у межах указаних вами застосунків; щоб поділитися новим застосунком, потрібна роль `owner`. `tslink mcp-audit` показує, що змінив агент. Ролі обмежують інструменти TSLink, а не власну оболонку агента.

[Посібник агента](agent-quickstart.md) · [Права MCP](mcp-scopes.md) · [Віддалений MCP](remote-mcp.md)

<a id="why"></a>

## Коли потрібен TSLink

Для одного застосунку, який ви запускаєте самі й відкриваєте на власному телефоні, вистачить Serve. TSLink потрібен для dev-серверів, які запускає агент, часто кількох, коли кожен має отримати власне приватне ім'я, а спільний доступ, URL-адреса й очищення мають бути готовими кроками в CLI, MCP та skill для Claude Code.

| Завдання | Лише Tailscale | TSLink |
|---|---|---|
| Кілька dev-серверів, запущених агентом, по імені на кожен | Serve задає маршрути на рівні пристрою під одним іменем на машину; Services вимагає для кожного імені визначення, схвалення й хост із тегами | Один вузол на застосунок; спільний доступ, отримання URL-адреси й очищення вже вбудовані в CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) і skill |
| Один застосунок, який ви запускаєте самі, на телефоні | Вистачить `tailscale serve 3000` | `tslink share 3000` |
| Одна людина, один застосунок, сім днів | Правила політики, потім JIT-інструмент або видалення вручну | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/файли) |
| Посилання для браузера, три дні | Публічний Funnel; захист доступу й вимкнення за розкладом додати самостійно | `tslink guest create photos --for 3d --public --print-link` (лише HTTP) |

Приватним отримувачам потрібен Tailscale. Гостьові посилання публічні, їх можна пересилати; вони слугують ключами доступу.

[Повне порівняння](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Ваші застосунки на ваших пристроях

- **Окрема адреса для кожного застосунку.** Вебзастосунки, теки, окремі файли й TCP-порти отримують власні імена у вашому tailnet, тож ви користуєтеся іменами замість IP-адрес.
- **Приватно за замовчуванням.** Нічого не стає публічним, доки ви не створите гостьове посилання або не опублікуєте через Funnel.
- **Один застосунок, а не вся машина.** Кожен застосунок, який ви публікуєте, отримує власний вузол, що пересилає трафік лише до цього застосунку. Без застосунку Tailscale на хості TSLink не додає до вашого tailnet інших портів хоста.
- **Домашня сторінка** зі списком ваших застосунків та їхнім станом. [Портал](portal.md)
- **Перевірки стану й сповіщення** через команду або webhook, а також журнал доступу, де видно й відхилені запити. [Стан і сповіщення](health-and-alerts.md) · [Історія доступу](access-log.md)
- **Рецепти для 15 self-hosted застосунків**, серед них Home Assistant, Jellyfin, Immich і Ollama. `tslink apps detect` знаходить ті, що вже запущені. [Рецепти застосунків](apps.md)

## Діліться, коли захочете

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Гостьові посилання й нові публічні URL діють до закінчення строку й працюють лише для вебзастосунків; теки, файли й TCP-порти лишаються приватними. [Люди](people.md) · [Гостьові посилання](guest-links.md) · [Публічний доступ](funnel.md)

<a id="architecture"></a>

## Як це працює

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Один ПК або хмарний сервер: CLI/MCP керує спільним демоном і вузлами застосунків. Приватні пристрої використовують шифрований Tailscale; необов'язковий публічний HTTPS/Funnel веде до HTTP-застосунків через гостьову перевірку або явну відкриту публікацію." width="960">
</picture>

Один фоновий процес запускає окремий вузол Tailscale для кожного застосунку. Tailscale забезпечує транспорт у tailnet і HTTPS-сертифікати. Приватний доступ до вебзастосунків і файлів можна обмежити за обліковим записом Tailscale через `--allow` і доступ для людей; чистий TCP покладається на політику вашого tailnet і власний вхід застосунку. [Архітектура](architecture.md)

<a id="requirements"></a>

## Вимоги

| Хто | Що потрібно |
|---|---|
| Ви | Обліковий запис Tailscale з увімкненими MagicDNS і HTTPS |
| Машина, де працюють ваші застосунки | TSLink, у який вбудовано Tailscale (у Linux також користувацький сеанс systemd) |
| Ваші пристрої та ті, з ким ви ділитеся | Застосунок Tailscale |
| Гості | Браузер |

Імена HTTPS-застосунків потрапляють у публічні журнали сертифікатів, тож обирайте імена, які можна показати іншим.

<a id="documentation"></a>

## Більше

[Уся документація](INDEX.md) · [Довідник CLI](cli-reference.md) · [Порівняння із Serve, ngrok і Cloudflare](comparison.md) · [Участь у проєкті](../CONTRIBUTING.md) · [Безпека](../SECURITY.md)

Apache 2.0. TSLink — незалежний проєкт, його не створювала й не схвалювала Tailscale.
