<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Uygulamalarınız için Tailscale ağınızda özel adresler.</strong></p>
<p align="center">Kendi cihazlarınızdan açın. Birini bir kişiyle ya da bir bağlantıyla, seçtiğiniz tarihe kadar paylaşın.</p>
<p align="center"><strong>Türkçe</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Diğer diller</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Kurulum

```sh
brew install --cask anydoor7/tap/tslink
```

Linux `.deb` ve `.rpm` paketleri ile Windows sürümleri [son sürüm](https://github.com/anydoor7/tslink/releases/latest) sayfasında. Bir uygulamayı ilk kez paylaştığınızda TSLink onun için bir Tailscale oturum açma bağlantısı gösterir. [Başlarken](getting-started.md)

<a id="why"></a>

## TSLink'e ne zaman ihtiyaç duyarsınız

Kendi cihazlarınızda tek bir uygulama için Serve yeterli. TSLink uygulama adreslerini, süreleri ve erişim değişikliklerini tek bir akışta toplar.

| İş | Yalnızca Tailscale | TSLink |
|---|---|---|
| Telefonda tek bir web uygulaması | `tailscale serve 3000` yeterli | `tslink share 3000` |
| Birden çok uygulama, her birine ayrı ad | Services kurulumu veya ayrı düğümler | Her uygulama için bir `share`/`add`; her düğümü kaydedin |
| Bir kişi, bir uygulama, yedi gün | Politika kuralları, ardından bir JIT aracı veya elle kaldırma | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/dosyalar) |
| Tarayıcı bağlantısı, üç gün | Herkese açık Funnel; erişim kapısını ve zamanlanmış kapatmayı siz ekleyin | `tslink guest create photos --for 3d --public --print-link` (yalnızca HTTP) |

Özel alıcıların Tailscale'e ihtiyacı vardır. Konuk bağlantıları herkese açıktır, iletilebilir ve erişim anahtarı işlevi görür.

[Ayrıntılı karşılaştırma](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Uygulamalarınız, kendi cihazlarınızda

- **Her uygulamaya bir adres.** Web uygulamaları, klasörler, tek dosyalar ve TCP portları tailnet'inizde kendi adını alır; IP adresleri yerine adları kullanırsınız.
- **Varsayılan olarak özel.** Bir konuk bağlantısı oluşturana ya da Funnel ile yayımlayana kadar hiçbir şey herkese açık olmaz.
- **Bir ana sayfa**, uygulamalarınızı sağlık durumlarıyla listeler. [Portal](portal.md)
- **Sağlık kontrolleri ve uyarılar** komut ya da webhook ile; erişim günlüğü reddedilen istekleri de içerir. [Sağlık ve uyarılar](health-and-alerts.md) · [Erişim geçmişi](access-log.md)
- **Kendi sunucunuzda barındırabileceğiniz 15 uygulama için tarifler**; Home Assistant, Jellyfin, Immich ve Ollama dahil. `tslink apps detect` zaten çalışanları bulur. [Uygulama tarifleri](apps.md)

## İstediğinizde paylaşın

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Konuk bağlantılarının ve yeni herkese açık URL'lerin süresi dolar ve yalnızca web uygulamalarında çalışır; klasörler, dosyalar ve TCP portları özel kalır. [Kişiler](people.md) · [Konuk bağlantıları](guest-links.md) · [Herkese açık erişim](funnel.md)

<a id="agents"></a>

## Yapay zekâ ajanları için

Bir ajanın `localhost` üzerinde başlattığı geliştirme sunucusuna telefonunuzdan ulaşamazsınız. TSLink, ajanın ona özel bir adres vermesini, tam URL'yi bildirmesini ve iş bitince kaldırmasını sağlar.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI veya MCP.** Yönetim komutları `--json` alır ve sürümlü sonuçlar döndürür; `tslink mcp` uygulama ve erişim işlemlerini MCP üzerinden sunar.
- **Sınırlı roller.** `viewer`, `app-operator` veya `people-manager`, belirttiğiniz uygulamalarla sınırlı. `tslink mcp-audit` bir ajanın neyi değiştirdiğini gösterir. Roller TSLink'in araçlarını sınırlar, ajanın kendi kabuğunu değil.

[Ajan kılavuzu](agent-quickstart.md) · [MCP yetkileri](mcp-scopes.md) · [Uzak MCP](remote-mcp.md)

<a id="architecture"></a>

## Nasıl çalışır?

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Tek PC veya bulut sunucusunda CLI/MCP ortak arka plan hizmetini ve uygulama düğümlerini yönetir. Özel cihazlar şifreli Tailscale kullanır; isteğe bağlı herkese açık HTTPS/Funnel, HTTP uygulamalarına konuk denetimi veya açık yayın üzerinden ulaşır." width="960">
</picture>

Tek bir arka plan süreci her uygulama için ayrı bir Tailscale düğümü çalıştırır. tailnet içindeki taşımayı ve HTTPS sertifikalarını Tailscale sağlar. Özel web ve dosya erişimi `--allow` ve kişi izinleriyle Tailscale kimliğine göre sınırlandırılabilir; ham TCP ise tailnet politikanıza ve uygulamanın kendi oturum açma adımına dayanır. [Mimari](architecture.md)

<a id="requirements"></a>

## Gereksinimler

| Kim | Ne gerekir |
|---|---|
| Siz | MagicDNS ve HTTPS'in etkin olduğu bir Tailscale hesabı |
| Uygulamalarınızı çalıştıran makine | Tailscale'i içinde barındıran TSLink (Linux'ta ayrıca bir systemd kullanıcı oturumu gerekir) |
| Cihazlarınız ve paylaştığınız kişiler | Tailscale uygulaması |
| Konuklar | Bir tarayıcı |

HTTPS uygulamalarının adları herkese açık sertifika günlüklerinde görünür; başkalarının görmesinde sakınca olmayan adlar seçin.

<a id="documentation"></a>

## Daha fazla

[Tüm belgeler](INDEX.md) · [CLI başvurusu](cli-reference.md) · [Serve, ngrok ve Cloudflare ile karşılaştırma](comparison.md) · [Katkıda bulunma](../CONTRIBUTING.md) · [Güvenlik](../SECURITY.md)

Apache 2.0. TSLink bağımsız bir projedir; Tailscale tarafından yapılmamış veya onaylanmamıştır.
