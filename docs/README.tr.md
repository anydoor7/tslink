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

<a id="use-cases"></a>

## Uygulamalarınız, kendi cihazlarınızda

- **Her uygulamaya bir adres.** Web uygulamaları, klasörler, tek dosyalar ve TCP portları tailnet'inizde kendi adını alır; onları IP adresi ve port yerine adıyla açarsınız.
- **Varsayılan olarak özel.** Bir konuk bağlantısı oluşturana ya da Funnel ile yayımlayana kadar hiçbir şey herkese açık olmaz.
- **Bir ana sayfa**, uygulamalarınızı sağlık durumlarıyla listeler. [Portal](portal.md)
- **Sağlık kontrolleri ve uyarılar** komut ya da webhook ile; erişim günlüğü reddedilen istekleri de içerir. [Sağlık ve uyarılar](health-and-alerts.md) · [Erişim geçmişi](access-log.md)
- **15 kendi barındırılan uygulama için tarifler**; Home Assistant, Jellyfin, Immich ve Ollama dahil. `tslink apps detect` zaten çalışanları bulur. [Uygulama tarifleri](apps.md)

## İstediğinizde paylaşın

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Konuk bağlantılarının ve herkese açık URL'lerin süresi her zaman dolar ve yalnızca web uygulamalarında çalışır; klasörler, dosyalar ve TCP portları özel kalır. [Kişiler](people.md) · [Konuk bağlantıları](guest-links.md) · [Herkese açık erişim](funnel.md)

<a id="agents"></a>

## Yapay zekâ ajanları için

Bir ajanın `localhost` üzerinde başlattığı geliştirme sunucusuna telefonunuzdan ulaşamazsınız. TSLink, ajanın seçtiğiniz bir rol içinde ona özel bir adres vermesini, tam URL'yi bildirmesini ve iş bitince kaldırmasını sağlar.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI veya MCP.** Yönetim komutları `--json` alır ve sürümlü sonuçlar döndürür; `tslink mcp` aynı işlemleri MCP üzerinden sunar.
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
| Siz | MagicDNS ve HTTPS'i açık bir Tailscale hesabı |
| Uygulamalarınızı çalıştıran makine | Yalnızca TSLink (Tailscale içinde gelir) |
| Cihazlarınız ve paylaştığınız kişiler | Tailscale uygulaması |
| Konuklar | Bir tarayıcı |

Uygulama adları herkese açık sertifika günlüklerinde görünür; başkalarının görmesinde sakınca olmayan adlar seçin.

<a id="documentation"></a>

## Daha fazla

[Tüm belgeler](INDEX.md) · [CLI başvurusu](cli-reference.md) · [Serve, ngrok ve Cloudflare ile karşılaştırma](comparison.md) · [Katkıda bulunma](../CONTRIBUTING.md) · [Güvenlik](../SECURITY.md)

Apache 2.0. TSLink bağımsız bir projedir; Tailscale tarafından yapılmamış veya onaylanmamıştır.
