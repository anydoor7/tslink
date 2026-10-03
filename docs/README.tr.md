<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Uygulamalarınıza her yerden erişin ve onları yönetin.<br>Gizli tutun veya kendi koşullarınızla paylaşın.</strong></p>

Bilgisayarınızdaki veya bulut sunucunuzdaki uygulamalara şifreli özel ağ üzerinden erişin; dilerseniz tarayıcı konuk bağlantılarını veya herkese açık erişimi seçin. Kendiniz ya da bir ajan aracılığıyla yönetin.

<p align="center"><a href="#quickstart">Hızlı başlangıç</a> · <a href="#agents">Ajanlar için</a> · <a href="#documentation">Belgeler</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <strong>Türkçe</strong> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Uygulamalarınız elinizin altında

| İhtiyacınız | TSLink'in sunduğu |
|---|---|
| Kendi uygulamalarınızı farklı cihazlarda kullanmak | PC veya sunucudaki ev panoları, yalnızca yerelde açılan web sayfaları, dosyalar, model API'leri ve TCP hizmetleri için özel adresler. |
| Belirli kişilerle paylaşmak | Seçili HTTP/dosya uygulamaları, doğrulanmış Tailscale kimliği, süre sonu ve erişim iptali. Alıcıların Tailscale kullanması gerekir. [Kişiler](people.md) |
| Tarayıcıdan konuk kabul etmek | HTTP proxy uygulamaları için isteğe bağlı PIN'li süreli bağlantılar veya Funnel üzerinden açıkça etkinleştirilen genel HTTPS. Bağlantılar iletilebilir, kişinin kimliğini doğrulamaz. [Konuk bağlantıları](guest-links.md) |
| Bir uygulama grubunu yönetmek | Sunucu başına envanter, özel portal, sağlık kontrolleri ve uyarılar, erişim geçmişi; ajan rolleri, uygulama kapsamları ve denetim kayıtlarıyla CLI/MCP yönetimi. [Portal](portal.md) · [MCP yetkileri](mcp-scopes.md) |

[Uygulama tarifleri](apps.md), [yükleme sınırları](sharing.md), [esnek süreler](durations.md) ve [QR ile katılım ve erişim talepleri](requests.md) bakımı kolaylaştırır. Bu özellikler mevcut kaynak kodda bulunur.

<a id="installation"></a>
<a id="quickstart"></a>

## Hızlı başlangıç

**Git ve Go 1.26.6+** ile kaynaktan kurun; hazır derlemeler ve Homebrew henüz yayımlanmadı. Komutlar bash/zsh içindir. [macOS, Linux ve Windows kurulumu](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Depoya erişim, **Tailscale hesabı**, [MagicDNS ve HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) gerekir. Özel erişim sağlayan cihazlarda Tailscale ve ağ politikası izni bulunmalıdır. TSLink, uygulama sunucusunda Tailscale'i içerir.

Uygulamanız 3000 portunda zaten çalışıyorsa:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Boş bir ad seçin; `share` başka ad döndürürse `url` için onu kullanın. Önce belirtilen tarayıcı kaydını ve cihaz onayını tamamlayın, ardından izinli cihazda tam uygulama URL'sini açın. `share` gerektiğinde arka plan hizmetini başlatır. İlk özel kullanım yönetici API belirteci gerektirmez. Dosyalar için `tslink share ./report.html` kullanın; dosyalar mevcut, uygulamalar çalışır durumda olmalıdır. [Tam kurulum](getting-started.md)

Çalışıp işinize yaradığında, başkalarının da keşfetmesi için [TSLink'e yıldız verebilirsiniz](https://github.com/anydoor7/tslink). Tamamen isteğe bağlıdır.

<a id="architecture"></a>

## Nasıl çalışır?

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Tek PC veya bulut sunucusunda CLI/MCP ortak arka plan hizmetini ve uygulama düğümlerini yönetir. Özel cihazlar şifreli Tailscale kullanır; isteğe bağlı herkese açık HTTPS/Funnel, HTTP uygulamalarına konuk denetimi veya açık yayın üzerinden ulaşır." width="960">
</picture>

Uygulamalarınıza uzanan özel, şifreli bir yol düşünün. **Ağ iletimini ve HTTPS'yi Tailscale, her sunucudaki uygulama erişimini TSLink sağlar.** Tek arka plan süreci her hizmet için ayrı gömülü düğüm çalıştırır. Özel portal izinli uygulamaları gösterir; durum ve erişim geçmişi bakıma yardımcı olur.

Genel erişim açıkça etkinleştirilir: konuk için bağlantı ve varsa PIN gerekir; açık Funnel'a URL'si olan herkes ulaşabilir. İkisi de özel kullanıcı kimliği yerine genel HTTPS kullanır. Ham TCP özel kalır; tailnet politikası ve arka uç kimlik doğrulamasına dayanır. TSLink uygulama kurmaz, süreçleri yalıtmaz, bulut VPC'si oluşturmaz veya birden çok sunucuyu birleştirmez. Tailscale ile çalışan bağımsız bir projedir. [Mimari ve sınırlar](architecture.md)

<a id="agents"></a>

## Ajanlar için

Envanteri, sağlığı, URL'leri ve erişimi CLI/MCP ile yönetin. [Ajan kılavuzuyla](agent-quickstart.md) başlayın; güncel araç şemalarını okuyun ve başarı bildirmeden gerçek erişimi doğrulayın.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI otomasyonu `--json`, MCP ise stdio üzerinden JSON-RPC kullanır. [İstemciler](mcp-clients.md) · [Uzak MCP](remote-mcp.md) · [Roller ve kapsamlar](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Belgeler ve lisans

[Tüm kılavuzlar](INDEX.md) · [CLI başvurusu](cli-reference.md) · [Yerel yapay zekâ](local-ai.md) · [Sağlık](health-and-alerts.md) · [Erişim geçmişi](access-log.md) · [Yol haritası](roadmap.md)

Çok sunuculu envanter planlanmaktadır. [Katkılar](../CONTRIBUTING.md) ve [güvenlik bildirimleri](../SECURITY.md) memnuniyetle karşılanır. [Apache 2.0](../LICENSE) ticari kullanıma izin verir; dağıtırken [NOTICE](../NOTICE) ve [üçüncü taraf bildirimlerini](../THIRD_PARTY_NOTICES.md) koruyun. [Ticari işbirliği](../COMMERCIAL.md) gönüllüdür. Tailscale koşulları ve planları ayrıca geçerlidir.
