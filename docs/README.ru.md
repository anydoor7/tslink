<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="логотип TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Дайте своим локальным приложениям, моделям и файлам собственный приватный адрес.</strong><br>
  Открывайте их с другого разрешённого устройства в своей сети Tailscale.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Лицензия: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 или новее"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: встроенные узлы tsnet"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 инструментов"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <strong>Русский</strong> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Установка

Вам нужны **Go 1.26.6+** и Git. Готовые сборки и
Homebrew cask пока не опубликованы; установите программу из исходного кода. Примеры используют
**bash или zsh**; требования для Windows и фоновых служб описаны в [поддержке платформ](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Используйте учётную запись Tailscale с [включёнными MagicDNS и HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
Принимающее устройство должно быть подключено к вашей сети Tailscale (**tailnet**), а политика сети
должна разрешать доступ к службе. TSLink встраивает Tailscale на публикующем хосте.

### Поделитесь своей первой страницей

Создайте страницу; TSLink предоставит к ней прямой доступ и при необходимости запустит свою фоновую службу:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Если TSLink выводит URL регистрации, откройте его, чтобы авторизовать узел; в вашей сети tailnet также может
потребоваться одобрение устройства администратором. Затем получите точный адрес:

```bash
tslink url demo --wait
```

Откройте этот URL на разрешённом устройстве. Для первого предоставления доступа токен API не нужен.
[Полная настройка и подробности жизненного цикла →](getting-started.md)

<a id="use-cases"></a>

## Чем вы поделитесь?

Файлы должны существовать; приложения, базы данных и серверы моделей должны уже работать на указанных портах.

| Сценарий | Команда |
|---|---|
| Открыть локальное приложение с другого устройства | `tslink share 3000` |
| Просматривать каталог файлов | `tslink share ./public --name files` |
| Читать созданный HTML-отчёт на телефоне | `tslink share ./report.html --name report` |
| Подключиться к локальной базе данных по TCP | `tslink add database --tcp localhost:5432` |
| Использовать HTTP API локальной модели, например Ollama | `tslink add model --proxy localhost:11434` |

Для Ollama получите точный URL командой `tslink url model --wait`; значение `baseURL`
клиента, совместимого с OpenAI, состоит из этого URL и `/v1`. [Локальные модели и работа с приватными данными →](local-ai.md)

Для нескольких приложений на одном хосте TSLink объединяет именованные узлы, списки разрешённых HTTP-идентичностей, срок действия Funnel и управление MCP. Для одного приложения на собственных устройствах может хватить [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve).

<a id="architecture"></a>

## Архитектура

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Пример схемы служб: App, Docs, Database и Model, отдельные именованные узлы в одной сети tailnet. Приложения, файлы и API моделей используют HTTPS; база данных использует приватный TCP." width="960">
</picture>

**Одна сеть tailnet, отдельные узлы служб.** Общий демон запускает по одному встроенному узлу tsnet для
каждой службы, перенаправляя HTTP, предоставляя файлы или проксируя TCP. Изменения реестра вступают в силу
во время его работы. У каждого узла своя сетевая идентичность; службы используют общий публикующий хост.
[Подробности архитектуры →](architecture.md)

| Компонент | Роль |
|---|---|
| [Go](../go.mod) | Нативная программа командной строки |
| [Tailscale tsnet](architecture.md) | Узлы служб и транспорт в сети tailnet |
| [Cobra](https://github.com/spf13/cobra) | Команды и справка |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Транспорт для агентов |
| Системное хранилище ключей и менеджер служб пользователя | Необязательные учётные данные и фоновая работа |

Службы остаются внутри вашей сети tailnet, пока вы явно не включите [публичный Funnel](getting-started.md#more-examples).
Службы HTTP и файлов поддерживают списки разрешённых идентичностей (`WhoIs`, `--allow`); TCP использует политику tailnet и
собственную аутентификацию серверной части. См. [границы предоставления доступа](sharing.md).

TSLink не устанавливает приложения, не запускает модели, не изолирует процессы хоста и не объединяет несколько хостов. Сеть, шифрование и HTTPS предоставляет Tailscale; TSLink является независимым проектом.

<a id="agents"></a>

## Для агентов

**19 инструментов MCP** позволяют агенту делиться отчётами, управлять службами, получать URL и проверять
настройку. Подключите локальный клиент MCP к установленной программе:

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

MCP управляет TSLink; приложения используют HTTP API модели для инференса.
О настройке и автоматизации см. [клиенты MCP](mcp-clients.md), [удалённый MCP](remote-mcp.md) и
[руководство по работе для агентов](../AGENTS.md).

Автоматизация CLI поддерживает `--json` с `schema_version`, равным `1`; используйте `tslink status --urls --json`. Локальный MCP использует JSON-RPC через stdio. См. [автоматизацию JSON](json-automation.md).

<a id="roadmap"></a>

## Что дальше

Пункты со статусами Слияние, На проверке и Запланировано не входят в указанную выше установку из исходников.

| Сценарий | Статус |
|---|---|
| F1. Дайте родственнику доступ к частным HTTP/файловым приложениям на 3 дня, объединив приглашения в одно сообщение; получателю всё ещё нужен Tailscale. | Слияние |
| F2. Проверяйте состояние приложений и получайте предупреждения о сбоях или истечении срока через необязательную команду или webhook. | Слияние |
| F3. Находите поддерживаемые приложения на loopback и просматривайте рецепты самостоятельно размещённых приложений перед публикацией. | Слияние |
| F8. Настройте размер загрузки и тайм-ауты запросов каждого HTTP-приложения для больших файлов и медленных клиентов. | Слияние |
| F9. Перезапускайте упавший демон Windows во время активного входа через запланированную задачу и встроенный супервизор. | Слияние |
| F4. Смотрите, кто открыл какое приложение, в локальном журнале доступа с режимами пути `prefix`, `full` или `off`. | На проверке |
| F5. Открывайте одну домашнюю страницу с разрешёнными приложениями и передачей регистрации владельцам; посетителям всё ещё нужен Tailscale. | На проверке |
| F6. Задайте агенту роль и область приложений с квитанциями аудита его изменений. | На проверке |
| F10. Позвольте гостю открыть одно HTTP-приложение в браузере без установки Tailscale, по временному адресу с необязательным PIN через публичный Funnel с контролем доступа. | На проверке |
| F11. Выбирайте готовые или собственные сроки от 1 часа с настраиваемым максимумом для гостей по умолчанию 7 дней. | На проверке |
| F12. Помогите пользователям телефонов подключиться по QR-коду и одобрите доступ к приложению или дополнительное время одним действием. | На проверке |
| F7. Смотрите приложения нескольких хостов в одном списке. | Запланировано |

<a id="documentation"></a>

## Документация и лицензия

[Начало работы](getting-started.md) · [Локальные модели](local-ai.md) ·
[Справочник CLI](cli-reference.md) · [Платформы](platforms.md) · [План развития](roadmap.md)

Участвуйте в разработке согласно [CONTRIBUTING.md](../CONTRIBUTING.md); сообщайте об уязвимостях через
[SECURITY.md](../SECURITY.md).

TSLink использует неизменённую [лицензию Apache 2.0](../LICENSE), включая коммерческое использование.
При распространении сохраняйте применимые [NOTICE](../NOTICE) и [уведомления третьих сторон](../THIRD_PARTY_NOTICES.md).
[Коммерческое сотрудничество](../COMMERCIAL.md) добровольно и не добавляет условий лицензии.
Условия обслуживания и тарифные планы Tailscale применяются отдельно.
