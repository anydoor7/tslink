<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Приватные адреса для ваших приложений в вашей сети Tailscale.</strong></p>
<p align="center">Открывайте их со своих устройств. Делитесь с человеком или по ссылке до выбранной вами даты.</p>
<p align="center"><strong>Русский</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Другие языки</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Установка

```sh
brew install --cask anydoor7/tap/tslink
```

Пакеты `.deb` и `.rpm` для Linux и сборки для Windows есть в [последнем релизе](https://github.com/anydoor7/tslink/releases/latest). Когда вы впервые делитесь приложением, TSLink выводит для него ссылку для входа в Tailscale. [Начало работы](getting-started.md)

<a id="why"></a>

## Когда нужен TSLink

Для одного приложения на своих устройствах хватит Serve. TSLink собирает адреса приложений, сроки и изменения доступа в одном процессе.

| Задача | Только Tailscale | TSLink |
|---|---|---|
| Одно веб-приложение на телефоне | Хватит `tailscale serve 3000` | `tslink share 3000` |
| Несколько приложений, у каждого своё имя | Настройка Services или отдельные узлы | Один `share`/`add` на приложение; подключить каждый узел |
| Один человек, одно приложение, семь дней | Правила политики, затем JIT-инструмент или удаление вручную | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/файлы) |
| Ссылка для браузера, три дня | Публичный Funnel; защиту доступа и отключение по расписанию добавить самому | `tslink guest create photos --for 3d --public --print-link` (только HTTP) |

Приватным получателям нужен Tailscale. Гостевые ссылки публичны: открыть и переслать ссылку может любой, у кого она есть.

[Полное сравнение](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Ваши приложения на ваших устройствах

- **Свой адрес у каждого приложения.** Веб-приложения, папки, отдельные файлы и TCP-порты получают собственные имена в вашем tailnet, поэтому вы пользуетесь именами вместо IP-адресов.
- **Приватно по умолчанию.** Ничего не становится публичным, пока вы не создадите гостевую ссылку или не опубликуете через Funnel.
- **Домашняя страница** со списком ваших приложений и их состоянием. [Портал](portal.md)
- **Проверки состояния и оповещения** через команду или webhook, а также журнал доступа, где видны и отклонённые запросы. [Состояние и оповещения](health-and-alerts.md) · [История доступа](access-log.md)
- **Рецепты для 15 self-hosted приложений**, включая Home Assistant, Jellyfin, Immich и Ollama. `tslink apps detect` находит уже запущенные. [Рецепты приложений](apps.md)

## Делитесь, когда захотите

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Гостевые ссылки и новые публичные URL истекают и работают только для веб-приложений; папки, файлы и TCP-порты остаются приватными. [Доступ для людей](people.md) · [Гостевые ссылки](guest-links.md) · [Публичный доступ](funnel.md)

<a id="agents"></a>

## Для ИИ-агентов

Dev-сервер, который агент запустил на `localhost`, недоступен с вашего телефона. TSLink позволяет агенту дать ему приватный адрес, сообщить точный URL и убрать его после работы.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI или MCP.** Команды управления принимают `--json` и возвращают версионированные результаты; `tslink mcp` даёт операции с приложениями и доступом через MCP.
- **Ограниченные роли.** `viewer`, `app-operator` или `people-manager`, только для указанных вами приложений. `tslink mcp-audit` показывает, что изменил агент. Роли ограничивают инструменты TSLink, а не собственную оболочку агента.

[Руководство агента](agent-quickstart.md) · [Права MCP](mcp-scopes.md) · [Удалённый MCP](remote-mcp.md)

<a id="architecture"></a>

## Как это работает

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Один ПК или облачный сервер: CLI/MCP управляет общим демоном и узлами приложений. Приватные устройства используют шифрованный Tailscale; публичный HTTPS/Funnel по выбору ведёт к HTTP-приложениям через гостевую проверку или явную открытую публикацию." width="960">
</picture>

Один фоновый процесс запускает отдельный узел Tailscale для каждого приложения. Tailscale обеспечивает транспорт в tailnet и HTTPS-сертификаты. Приватный доступ к веб-приложениям и файлам можно ограничить по учётной записи Tailscale с помощью `--allow` и доступа для людей; чистый TCP полагается на политику вашего tailnet и собственный вход приложения. [Архитектура](architecture.md)

<a id="requirements"></a>

## Требования

| Кто | Что нужно |
|---|---|
| Вы | Аккаунт Tailscale с включёнными MagicDNS и HTTPS |
| Машина, где работают ваши приложения | TSLink, в который встроен Tailscale (в Linux также пользовательская сессия systemd) |
| Ваши устройства и те, с кем вы делитесь | Приложение Tailscale |
| Гости | Браузер |

Имена HTTPS-приложений попадают в публичные журналы сертификатов, поэтому выбирайте имена, которые можно показать другим.

<a id="documentation"></a>

## Ещё

[Вся документация](INDEX.md) · [Справочник CLI](cli-reference.md) · [Сравнение с Serve, ngrok и Cloudflare](comparison.md) · [Участие в проекте](../CONTRIBUTING.md) · [Безопасность](../SECURITY.md)

Apache 2.0. TSLink — независимый проект, его не создавала и не одобряла Tailscale.
