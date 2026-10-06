<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Діліться своїми self-hosted застосунками з тими, кого оберете, і на стільки, на скільки вирішите.</strong></p>

TSLink дає кожному застосунку на вашому комп'ютері чи сервері власну приватну адресу Tailscale. Надавайте доступ конкретним людям до визначеного строку, надсилайте гостьове посилання для браузера тим, хто не користується Tailscale, і відкликайте будь-який із цих доступів однією командою. Робіть це самі або через ШІ-агента, обмеженого призначеною вами роллю. Незалежний проєкт, що працює з Tailscale.

<p align="center"><a href="#quickstart">Швидкий старт</a> · <a href="#agents">Для агентів</a> · <a href="comparison.md">Порівняння із Serve, ngrok і Cloudflare</a> · <a href="#documentation">Документація</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <strong>Українська</strong> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Ваші застосунки завжди поруч

| Ваша потреба | Можливості TSLink |
|---|---|
| Власні застосунки на різних пристроях | Приватні адреси домашніх панелей, локальних вебсторінок, файлів, API моделей і TCP-сервісів на ПК чи сервері. |
| Доступ для конкретних людей | Вибрані HTTP/файлові застосунки, перевірений обліковий запис Tailscale, строк дії та відкликання. Отримувачам потрібен Tailscale. [Люди](people.md) |
| Відвідування через браузер | Тимчасові посилання з необов'язковим PIN для HTTP-проксі-застосунків або явно публічний HTTPS через Funnel. Посилання можна переслати; воно не підтверджує особу. [Гостьові посилання](guest-links.md) |
| Обслуговування набору застосунків | Перелік на кожному хості, приватний портал, перевірки стану й сповіщення, історія доступу та CLI/MCP-керування з ролями агентів, межами за застосунками й записами аудиту. [Портал](portal.md) · [Права MCP](mcp-scopes.md) |

[Рецепти застосунків](apps.md), [ліміти завантаження](sharing.md), [гнучкі строки](durations.md) та [QR-інструкції й запити доступу](requests.md) спрощують обслуговування. Ці функції входять до v0.1.0.

<a id="installation"></a>
<a id="quickstart"></a>

## Швидкий старт

На macOS і Linux встановіть через Homebrew. Двійковий файл для macOS підписано сертифікатом Developer ID і нотаризовано Apple. Щоб згодом оновитися, виконайте `brew upgrade --cask tslink`, а потім знову `tslink install`, якщо TSLink працює як фонова служба.

```bash
brew install --cask anydoor7/tap/tslink
```

У Windows завантажте `tslink_<version>_windows_<arch>.zip` з [останнього релізу](https://github.com/anydoor7/tslink/releases/latest), звірте його з `checksums.txt` і виконайте `tslink install`, щоб TSLink запускався під час входу в систему. Архів zip не має підпису Authenticode; [перевірте реліз](verify-release.md) за підписаними контрольними сумами та атестаціями. Пакети `.deb` і `.rpm` для Linux є на тій самій сторінці релізу. Для збирання з вихідного коду потрібні **Git і Go 1.26.6+**. Команди нижче використовують bash/zsh; див. [Налаштування macOS, Linux і Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Потрібні **обліковий запис Tailscale** і [MagicDNS і HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Для приватного доступу пристроям потрібні Tailscale і дозвіл мережевої політики. На хості застосунків Tailscale вбудовано в TSLink.

Якщо застосунок уже працює на порту 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Оберіть вільне ім'я; якщо `share` поверне інше, використовуйте його в `url`. Спершу завершіть вказану реєстрацію у браузері й схвалення пристрою, потім відкрийте точний URL на дозволеному пристрої. `share` за потреби запускає фонову службу. Перший приватний доступ не потребує адміністративного API-токена. Файли можна ділити через `tslink share ./report.html`; вони мають існувати, а застосунки вже працювати. [Повне налаштування](getting-started.md)

Коли все запрацює й стане корисним, можете [поставити TSLink зірочку](https://github.com/anydoor7/tslink), щоб інші його знайшли. Це цілком добровільно.

<a id="architecture"></a>

## Як усе працює

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Один ПК або хмарний сервер: CLI/MCP керує спільним демоном і вузлами застосунків. Приватні пристрої використовують шифрований Tailscale; необов'язковий публічний HTTPS/Funnel веде до HTTP-застосунків через гостьову перевірку або явну відкриту публікацію." width="960">
</picture>

Уявіть приватний зашифрований шлях до застосунків. **Tailscale забезпечує мережевий транспорт і HTTPS; TSLink керує доступом на кожному хості.** Один демон запускає окремий вбудований вузол для кожного сервісу. Приватний портал показує дозволені застосунки; стан та історія допомагають у догляді.

Публічний доступ вмикається явно: гостю потрібні посилання й необов'язковий PIN; відкритий Funnel доступний усім із URL. Обидва використовують публічний HTTPS, а не приватну ідентифікацію користувача. Звичайний TCP залишається приватним і залежить від політики tailnet та автентифікації бекенду. TSLink не встановлює застосунки, не ізолює процеси, не створює хмарну VPC і не об'єднує хости. Незалежний проєкт, що працює з Tailscale. [Архітектура й межі](architecture.md)

<a id="agents"></a>

## Для агентів

Керуйте переліком, станом, URL і доступом через CLI/MCP. Почніть із [посібника агента](agent-quickstart.md), прочитайте актуальні схеми інструментів і перевірте реальний доступ перед повідомленням про успіх.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI-автоматизація використовує `--json`; MCP використовує JSON-RPC через stdio. [Клієнти](mcp-clients.md) · [Віддалений MCP](remote-mcp.md) · [Ролі й межі](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Документація та ліцензія

[Усі посібники](INDEX.md) · [Довідник CLI](cli-reference.md) · [Локальний ШІ](local-ai.md) · [Стан](health-and-alerts.md) · [Історія доступу](access-log.md) · [Плани](roadmap.md)

Спільний перелік кількох хостів заплановано. Вітаються [внески](../CONTRIBUTING.md) й [повідомлення про вразливості](../SECURITY.md). [Apache 2.0](../LICENSE) дозволяє комерційне використання; зберігайте [NOTICE](../NOTICE) і [сторонні повідомлення](../THIRD_PARTY_NOTICES.md) при розповсюдженні. [Комерційна співпраця](../COMMERCIAL.md) добровільна. Умови й плани Tailscale застосовуються окремо.
