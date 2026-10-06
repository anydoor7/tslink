<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Bilgisayarınızdaki veya sunucunuzdaki her uygulamaya Tailscale ağınızda kendine ait özel bir adres verin ve ona kimin erişebileceğine siz karar verin.</strong></p>

Web uygulamalarınızı, klasörlerinizi, model API'lerinizi ve veritabanlarınızı kendi telefonunuzdan ve dizüstü bilgisayarınızdan açın; her biri için sağlık kontrolü ve erişim geçmişi de hazır. Yapay zekâ ajanlarınız, onlara verdiğiniz rolün sınırları içinde, localhost üzerinde başlattıkları uygulamalara diğer cihazlarınız için özel bir adres verebilir ve bu uygulamaları kontrol edebilir. Başka birinin erişmesi gerektiğinde, belirli bir kişiye bir tarihe kadar erişim verin ya da bir web uygulamasını sınırlı bir süre için genel internete açın.

**Tailscale gerektirir.** Bir Tailscale hesabına (kişisel kullanım için ücretsiz) ihtiyacınız var ve özel bir uygulamayı açan her cihazda Tailscale uygulaması bulunmalı; konuklar ve genel ziyaretçiler için yalnızca bir tarayıcı yeterli. TSLink bağımsız bir projedir; Tailscale tarafından geliştirilmemiş ve onaylanmamıştır. [Gereksinimler](#requirements)

<p align="center"><a href="#quickstart">Hızlı başlangıç</a> · <a href="#agents">Ajanlar için</a> · <a href="comparison.md">Serve, ngrok ve Cloudflare ile karşılaştırma</a> · <a href="#documentation">Belgeler</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <strong>Türkçe</strong> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## Neler yapabilirsiniz

### Kendi uygulamalarınıza erişin

- **Her uygulamaya bir adres.** `tslink share 3000`, `tslink share ./photos` veya `tslink add db --tcp localhost:5432`, bir web uygulamasına, klasöre, dosyaya ya da TCP hizmetine tailnet'inizde (özel Tailscale ağınızda) kendine ait özel bir adres verir; örneğin `https://photos.<tailnet>.ts.net`. Her uygulama ayrı bir Tailscale cihazıdır, bu yüzden uygulamaları IP adresi ve port yerine adıyla açarsınız.
- **Siz aksini seçmedikçe özel.** TSLink uygulama erişimini varsayılan olarak özel tutar ve hangi cihazların bağlanabileceğine tailnet politikanız karar verir. Genel bir uygulama uç noktasını yalnızca bir konuk bağlantısı oluşturduğunuzda veya Funnel üzerinden açıkça yayımladığınızda açar.
- **Hepsini tek yerden görün.** `tslink status --urls` bu bilgisayarda kayıtlı uygulamaları listeler; isteğe bağlı özel ana sayfa da bunların adreslerini ve sağlık durumunu gösterir. [Portal](portal.md)
- **Bir şey bozulduğunda haberiniz olsun.** Arka plandaki sağlık kontrolleri, bir uygulama çöktüğünde veya geri geldiğinde ya da Tailscale oturumunun süresi dolmak üzereyken sizi bir komut veya webhook ile uyarabilir. Erişim geçmişi, reddedilen istekler dahil, kimin hangi uygulamayı ne zaman açtığını gösterir. [Sağlık ve uyarılar](health-and-alerts.md) · [Erişim geçmişi](access-log.md)
- **Uygulama tarifleri.** Tarifler; Home Assistant, Jellyfin, Immich ve Ollama dahil, kendi barındırdığınız 15 uygulamayı kapsar. `tslink apps detect` ise yerelde zaten dinlemekte olan desteklenen uygulamaları bulabilir. Büyük fotoğraf ve video yüklemeleri için [uygulamaya özel yükleme sınırlarını yükseltin](sharing.md). [Uygulama tarifleri](apps.md) · [Yerel yapay zekâ](local-ai.md)

### Ajanlarınız onlarla çalışsın

Bir ajan `localhost` üzerinde bir geliştirme sunucusu, önizleme veya yerel model API'si başlattığında, telefonunuz ve diğer bilgisayarlarınız o adrese ulaşamaz. TSLink, ajanın sizin belirlediğiniz sınırlar içinde buna özel bir adres vermesini, size tam URL'yi söylemesini ve sonra kaydını yeniden kaldırmasını sağlar.

- **Paylaş, kontrol et, geri al.** `share --json`, kaydettiği adı ve ya tam URL'yi ya da sizin açmanız gereken bir oturum açma bağlantısını döndürür. `url <name> --wait` ve `status --urls --name <name>` uç noktanın hazır olup olmadığını bildirir, `remove <name>` (MCP'de `unshare`) paylaşımı kaldırır. [Ajan kılavuzu](agent-quickstart.md)
- **Otomasyon için tasarlandı.** `tslink mcp` dışındaki yönetim komutları `--json` alır ve kararlı hata kodlarıyla sürümlü sonuçlar döndürür. `tslink mcp`, uygulama ve erişim araçlarını JSON-RPC üzerinden yerel bir MCP istemcisine sunar. Çağıran bağlamaları yapılandırıldığında `tslink serve --mcp`, bu araçları tailnet üzerinden diğer cihazlarınızdaki MCP istemcilerine sunar. [JSON otomasyonu](json-automation.md) · [Uzak MCP](remote-mcp.md)
- **Sınırlı yetki.** Yerel bir ajan varsayılan olarak sahip yetkisiyle çalışır. Bir ajana yalnızca belirttiğiniz uygulamaları kapsayan ve verdiği erişimin en fazla ne kadar sürebileceğini sınırlayan daraltılmış bir rol (`viewer`, `app-operator` veya `people-manager`) verin. MCP üzerinden yapılan değişiklikler kaydedilir ve `tslink mcp-audit` bunları gösterir. Roller TSLink'in araçlarını sınırlar; ajanın kendi kabuğunu veya dosyalarını sınırlamaz. [MCP yetkileri](mcp-scopes.md)

### Seçtiğiniz kişilerle paylaşın

- **Belirli kişiler, bir tarihe kadar.** `tslink people add alice@example.com --apps photos,notes --for 7d`, o Tailscale hesabının bu web ve dosya uygulamalarını son tarihe kadar açmasına izin verir. `people update`, `extend` ve `people remove` erişimi değiştirir veya sonlandırır; kaldırdıktan sonra kişinin bir sonraki isteği reddedilir, ancak zaten indirdikleri geri alınamaz. [Kişiler](people.md) · [Süreler](durations.md)
- **Tailnet'inizin dışındaki biri.** `--invite --print-links` ekleyerek her uygulama için bir cihaz davetiyesi içeren, gönderilmeye hazır tek bir mesaj alın (bunun için kullanıcıya ait bir API belirteci gerekir). `--qr` telefon kurulumu için bir kod yazdırır.
- **Talepler.** Tailnet'inizdeki kişiler ana sayfadan ek süre ya da talep edilebilir olarak işaretlediğiniz bir uygulamaya erişim isteyebilir. Tek komutla, bir süre belirterek onaylarsınız. [Erişim talepleri](requests.md)

### Bir web uygulamasını bir süreliğine internete açın

- **Konuk bağlantıları.** `tslink guest create photos --for 3d --public --print-link`, tek bir web uygulamasına isteğe bağlı PIN'li, ayrı olarak iptal edebileceğiniz bir tarayıcı bağlantısı oluşturur. Konukların Tailscale hesabına ihtiyacı yoktur. Bağlantıya sahip olan herkes onu kullanabilir, bu yüzden kimin ziyaret ettiğini kanıtlamaz. [Konuk bağlantıları](guest-links.md)
- **Açık bir genel URL.** `tslink add preview --proxy localhost:3000 --funnel --public`, bir web uygulamasını URL'sini bilen herkese yayımlar. Yeni bir yayın varsayılan olarak 24 saat sürer; başka bir süre seçmek için `--funnel-ttl` kullanın. [Funnel](funnel.md)
- Yeni konuk bağlantıları ve yeni açık genel yayınlar Tailscale Funnel kullanır ve süreleri sınırlıdır (en az 1 saat, varsayılan en fazla 7 gün, sahip tarafından değiştirilebilir). Bu genel yollar HTTP proxy uygulamalarını destekler; doğrudan sunulan klasör ve dosya hizmetleri ile ham TCP özel kalır.

Yukarıdakilerin hepsi v0.1.0 ile birlikte gelir.

<a id="requirements"></a>

## Gereksinimler

TSLink, Tailscale üzerine kuruludur. Bağımsız bir projedir; Tailscale tarafından geliştirilmemiş ve onaylanmamıştır. Tailscale'in kendi koşulları ve [planları](https://tailscale.com/pricing) geçerlidir.

| Kim | Neye ihtiyaç duyar |
|---|---|
| Siz | [MagicDNS ve HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) açık bir Tailscale hesabı. Ücretsiz Personal planı ticari olmayan kullanım içindir. |
| Uygulamalarınızı çalıştıran bilgisayar veya sunucu | Yalnızca TSLink. Tailscale yerleşik olarak gelir, ayrıca Tailscale kurmanız gerekmez. Varsayılan kurulumda her yeni uygulama düğümü tarayıcıda oturum açmayı gerektirir ve cihaz onayı da gerekebilir. [Kayıtlı kimlik bilgileri](credentials-and-tags.md), her uygulama için tarayıcıda oturum açmadan kayıt yapmayı sağlar. |
| Diğer cihazlarınız | Tailnet'inizde oturum açmış Tailscale uygulaması. |
| Seçtiğiniz kişiler | Tailscale uygulaması ve kendi hesapları. Ya tailnet'inize katılırlar, bu da planınıza bir kullanıcı ekler, ya da her uygulama için bir cihaz davetiyesini kabul ederler. Tailnet politikanız erişimlerine izin vermelidir. |
| Konuklar ve genel ziyaretçiler | Bir tarayıcı. Tailnet'iniz Funnel'a izin vermelidir; Tailscale bu özelliği hâlâ beta olarak sunar. |

Bir uygulama için HTTPS sertifikası verildiğinde, uygulamanın Tailscale cihaz adı ve tailnet DNS adınız herkese açık bir sertifika günlüğünde görünür. Başkalarının görmesinde sakınca görmediğiniz uygulama adları seçin.

<a id="installation"></a>
<a id="quickstart"></a>

## Hızlı başlangıç

macOS ve Linux'ta Homebrew ile kurun. macOS ikili dosyası bir Developer ID sertifikasıyla imzalanmış ve Apple tarafından noter onayından geçirilmiştir. Daha sonra güncellemek için `brew upgrade --cask tslink` çalıştırın; TSLink arka plan hizmeti olarak çalışıyorsa ardından `tslink install` komutunu yeniden çalıştırın.

```bash
brew install --cask anydoor7/tap/tslink
```

Windows'ta [en son sürümden](https://github.com/anydoor7/tslink/releases/latest) `tslink_<version>_windows_<arch>.zip` dosyasını indirin, `checksums.txt` ile doğrulayın ve TSLink'in oturum açtığınızda başlaması için `tslink install` çalıştırın. Zip dosyası Authenticode ile imzalı değildir; [sürümü doğrulamak](verify-release.md) için imzalı sağlama toplamlarını ve derleme kanıtlarını kullanın. Linux için `.deb` ve `.rpm` paketleri aynı sürüm sayfasındadır. Kaynaktan derlemek için **Git ve Go 1.26.6+** gerekir. Aşağıdaki komutlar bash/zsh içindir; ayrıntılar için [macOS, Linux ve Windows kurulumu](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

**Tailscale hesabı** ve [MagicDNS ve HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) gerekir. Özel erişim sağlayan cihazlarda Tailscale ve ağ politikası izni bulunmalıdır. TSLink, uygulama sunucusunda Tailscale'i içerir.

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
