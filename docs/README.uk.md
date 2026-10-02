<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="логотип TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Надайте своїм локальним застосункам, моделям і файлам власну приватну адресу.</strong><br>
  Відкривайте їх з іншого пристрою з дозволеним доступом у своїй мережі Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Ліцензія: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 або новіший"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: вбудовані вузли tsnet"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 інструментів"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <strong>Українська</strong><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Встановлення

Потрібні **Go 1.26.6+** і Git. Готові збірки та
Homebrew cask ще не опубліковані; встановіть програму з вихідного коду. Ці приклади використовують
**bash або zsh**; вимоги для Windows і фонових служб наведено в [підтримці платформ](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Використовуйте обліковий запис Tailscale з [увімкненими MagicDNS і HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
Пристрій, який отримує доступ, має бути підключений до вашої мережі Tailscale (**tailnet**), а політика мережі
має дозволяти доступ до служби. TSLink вбудовує Tailscale на хості, що публікує службу.

### Поділіться своєю першою сторінкою

Створіть сторінку; TSLink надасть до неї прямий доступ і за потреби запустить свою фонову службу:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Якщо TSLink виведе URL реєстрації, відкрийте його, щоб авторизувати вузол; ваша мережа tailnet також може
вимагати схвалення пристрою адміністратором. Потім отримайте точну адресу:

```bash
tslink url demo --wait
```

Відкрийте цей URL на пристрої з дозволеним доступом. Для першого надання доступу токен API не потрібен.
[Повне налаштування й подробиці життєвого циклу →](getting-started.md)

<a id="use-cases"></a>

## Чим ви поділитеся?

Файли мають існувати; застосунки, бази даних і сервери моделей мають уже працювати на зазначених портах.

| Сценарій | Команда |
|---|---|
| Відкрити локальний застосунок з іншого пристрою | `tslink share 3000` |
| Переглядати каталог файлів | `tslink share ./public --name files` |
| Читати згенерований HTML-звіт на телефоні | `tslink share ./report.html --name report` |
| Підключитися до локальної бази даних через TCP | `tslink add database --tcp localhost:5432` |
| Використовувати HTTP API локальної моделі, наприклад Ollama | `tslink add model --proxy localhost:11434` |

Для Ollama отримайте точний URL командою `tslink url model --wait`; значення `baseURL`
клієнта, сумісного з OpenAI, складається з цього URL і `/v1`. [Локальні моделі й робота з приватними даними →](local-ai.md)

Для кількох застосунків на одному хості TSLink поєднує іменовані вузли, списки дозволених HTTP-ідентичностей, термін дії Funnel та керування MCP. Для одного застосунку на власних пристроях може вистачити [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve).

<a id="architecture"></a>

## Архітектура

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Приклад схеми служб: App, Docs, Database і Model, окремі іменовані вузли в одній мережі tailnet. Застосунки, файли й API моделей використовують HTTPS; база даних використовує приватний TCP." width="960">
</picture>

**Одна мережа tailnet, окремі вузли служб.** Спільний демон запускає по одному вбудованому вузлу tsnet для
кожної служби, перенаправляючи HTTP, надаючи файли або проксіюючи TCP. Зміни реєстру набувають чинності
під час його роботи. Кожен вузол має власну мережеву ідентичність; служби використовують спільний хост публікації.
[Подробиці архітектури →](architecture.md)

| Компонент | Роль |
|---|---|
| [Go](../go.mod) | Нативна програма командного рядка |
| [Tailscale tsnet](architecture.md) | Вузли служб і транспорт у мережі tailnet |
| [Cobra](https://github.com/spf13/cobra) | Команди й довідка |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Транспорт для агентів |
| Системне сховище ключів і менеджер служб користувача | Необов’язкові облікові дані та фонова робота |

Служби залишаються всередині вашої мережі tailnet, доки ви явно не ввімкнете [публічний Funnel](getting-started.md#more-examples).
Служби HTTP і файлів підтримують списки дозволених ідентичностей (`WhoIs`, `--allow`); TCP використовує політику tailnet та
власну автентифікацію серверної частини. Див. [межі надання доступу](sharing.md).

TSLink не встановлює застосунки, не запускає моделі, не ізолює процеси хоста й не об’єднує кілька хостів. Мережу, шифрування та HTTPS надає Tailscale; TSLink є незалежним проєктом.

<a id="agents"></a>

## Для агентів

**19 інструментів MCP** дозволяють агенту ділитися звітами, керувати службами, отримувати URL і перевіряти
налаштування. Підключіть локальний клієнт MCP до встановленої програми:

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

MCP керує TSLink; застосунки використовують HTTP API моделі для інференсу.
Про налаштування й автоматизацію див. [клієнти MCP](mcp-clients.md), [віддалений MCP](remote-mcp.md) і
[операційний посібник для агентів](../AGENTS.md).

Автоматизація CLI підтримує `--json` зі значенням `schema_version` `1`; використовуйте `tslink status --urls --json`. Локальний MCP використовує JSON-RPC через stdio. Див. [автоматизацію JSON](json-automation.md).

<a id="roadmap"></a>

## Що далі

Пункти зі станами Об’єднується, На перевірці або Заплановано не входять до наведеної вище установки з джерел.

| Сценарій | Стан |
|---|---|
| <!-- roadmap:people --> Надайте родичу доступ до приватних HTTP/файлових застосунків на 3 дні та зберіть запрошення в одному повідомленні; одержувачу все ще потрібен Tailscale. | Об’єднується |
| <!-- roadmap:health --> Перевіряйте стан застосунків і отримуйте попередження про збої або завершення терміну через необов’язкову команду чи webhook. | Об’єднується |
| <!-- roadmap:recipes --> Знаходьте підтримувані застосунки на loopback та переглядайте рецепти самостійно розміщених застосунків перед поширенням. | Об’єднується |
| <!-- roadmap:limits --> Налаштуйте розмір завантаження та тайм-аути запитів кожного HTTP-застосунку для великих файлів і повільних клієнтів. | Об’єднується |
| <!-- roadmap:windows --> Перезапускайте аварійно завершений демон Windows під час активного входу через заплановане завдання та вбудований наглядач. | Об’єднується |
| <!-- roadmap:access-log --> Дивіться, хто відкрив який застосунок, у локальних журналах із режимами шляху `prefix`, `full` або `off`. | На перевірці |
| <!-- roadmap:portal --> Відкривайте одну домашню сторінку з дозволеними застосунками та передаванням реєстрації власникам; відвідувачам усе ще потрібен Tailscale. | На перевірці |
| <!-- roadmap:mcp-scopes --> Надайте агенту роль і область застосунків із квитанціями аудиту його змін. | На перевірці |
| <!-- roadmap:guest-links --> Дозвольте гостю відкрити один HTTP-застосунок у браузері без встановлення Tailscale, за тимчасовим посиланням із необов’язковим PIN через публічний Funnel із контролем доступу. | На перевірці |
| <!-- roadmap:durations --> Вибирайте готові або власні терміни від 1 години з налаштовуваним максимумом для гостей за замовчуванням 7 днів. | На перевірці |
| <!-- roadmap:requests --> Допоможіть користувачам телефонів приєднатися через QR-код і схваліть доступ чи додатковий час однією дією. | На перевірці |
| <!-- roadmap:multi-host --> Дивіться застосунки кількох хостів в одному переліку. | Заплановано |

<a id="documentation"></a>

## Документація й ліцензія

[Початок роботи](getting-started.md) · [Локальні моделі](local-ai.md) ·
[Довідник CLI](cli-reference.md) · [Платформи](platforms.md) · [План розвитку](roadmap.md)

Долучайтеся до розробки згідно з [CONTRIBUTING.md](../CONTRIBUTING.md); повідомляйте про вразливості через
[SECURITY.md](../SECURITY.md).

TSLink використовує незмінену [ліцензію Apache 2.0](../LICENSE), включно з комерційним використанням.
Під час розповсюдження зберігайте застосовні [NOTICE](../NOTICE) та [повідомлення третіх сторін](../THIRD_PARTY_NOTICES.md).
[Комерційна співпраця](../COMMERCIAL.md) добровільна й не додає умов ліцензії.
Умови обслуговування та тарифні плани Tailscale застосовуються окремо.
