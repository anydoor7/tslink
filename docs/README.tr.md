<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logosu">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Yerel uygulamalarınıza, modellerinize ve dosyalarınıza kendilerine ait özel bir adres verin.</strong><br>
  Tailscale ağınızdaki erişim izni olan başka bir cihazdan açın.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Lisans: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 veya üzeri"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: gömülü tsnet düğümleri"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 araç"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <strong>Türkçe</strong> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## Kurulum

**Go 1.26.6+** ve Git gerekir. Hazır derlemeler ve
Homebrew cask yayımlanmamıştır; kaynak koddan kurun. Bu örneklerde
**bash veya zsh** kullanılır; Windows ve arka plan hizmeti gereksinimleri için [platform desteğine](platforms.md) bakın.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

[MagicDNS ve HTTPS etkin](https://tailscale.com/docs/how-to/set-up-https-certificates) bir Tailscale hesabı kullanın.
Erişecek cihaz Tailscale ağınızda (**tailnet**) oturum açmış olmalı ve ağ politikası
hizmete erişmesine izin vermelidir. TSLink, hizmeti yayımlayan makinede Tailscale'i gömülü olarak çalıştırır.

### İlk sayfanızı paylaşın

Bir sayfa oluşturun; TSLink bunu doğrudan sunar ve gerektiğinde arka plan hizmetini başlatır:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

TSLink bir kayıt URL'si yazdırırsa düğümü yetkilendirmek için açın; tailnet'iniz ayrıca
cihaz için yönetici onayı gerektirebilir. Ardından tam adresi alın:

```bash
tslink url demo --wait
```

Bu URL'yi erişim izni olan bir cihazda açın. İlk paylaşım için API belirteci gerekmez.
[Tüm kurulum ve yaşam döngüsü ayrıntıları →](getting-started.md)

<a id="use-cases"></a>

## Neyi paylaşacaksınız?

Dosyalar mevcut olmalıdır; uygulama, veritabanı ve model arka uçları belirtilen bağlantı noktalarında zaten çalışıyor olmalıdır.

| Kullanım senaryosu | Komut |
|---|---|
| Yerel bir uygulamayı başka bir cihazdan açın | `tslink share 3000` |
| Bir dosya dizinine göz atın | `tslink share ./public --name files` |
| Oluşturulan HTML raporunu telefonunuzda okuyun | `tslink share ./report.html --name report` |
| Yerel bir veritabanına TCP üzerinden bağlanın | `tslink add database --tcp localhost:5432` |
| Ollama gibi yerel bir modelin HTTP API'sini kullanın | `tslink add model --proxy localhost:11434` |

Ollama için tam URL'yi `tslink url model --wait` ile alın; OpenAI uyumlu bir istemcinin
`baseURL` değeri, bu URL'ye `/v1` eklenerek oluşturulur. [Yerel modeller ve özel verilerle çalışma akışları →](local-ai.md)

Tek ana makinede birden çok uygulama için TSLink adlandırılmış düğümleri, HTTP kimlik izin listelerini, Funnel süre sonunu ve MCP yönetimini bir araya getirir. Kendi cihazlarınızdaki tek uygulama için [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) yeterli olabilir.

<a id="architecture"></a>

## Mimari

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Örnek hizmet haritası: App, Docs, Database ve Model, tek bir tailnet içinde ayrı adlandırılmış düğümlerdir. Uygulamalar, dosyalar ve model API’leri HTTPS kullanır; veritabanı özel TCP kullanır." width="960">
</picture>

**Tek tailnet, ayrı hizmet düğümleri.** Ortak bir arka plan süreci her hizmet için gömülü bir tsnet düğümü
çalıştırır; HTTP'yi yönlendirir, dosyaları sunar veya TCP'ye aracılık eder. Kayıt defteri değişiklikleri süreç
çalışırken uygulanır. Her düğümün kendi ağ kimliği vardır; hizmetler aynı yayımlayıcı makineyi paylaşır.
[Mimari ayrıntıları →](architecture.md)

| Bileşen | Rolü |
|---|---|
| [Go](../go.mod) | Yerel komut satırı programı |
| [Tailscale tsnet](architecture.md) | Hizmet düğümleri ve tailnet aktarımı |
| [Cobra](https://github.com/spf13/cobra) | Komutlar ve yardım |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Ajan iletişim taşıma katmanları |
| İşletim sistemi anahtar zinciri ve kullanıcı hizmet yöneticisi | İsteğe bağlı kimlik bilgileri ve arka planda çalışma |

Siz açıkça [genel erişime açık Funnel](getting-started.md#more-examples) özelliğini etkinleştirmedikçe hizmetler tailnet'inizde kalır.
HTTP ve dosya hizmetleri kimliğe göre izin listelerini destekler (`WhoIs`, `--allow`); TCP, tailnet politikasını ve arka ucun
kendi kimlik doğrulamasını kullanır. [Paylaşım sınırlarına](sharing.md) bakın.

TSLink uygulama kurmaz, model çalıştırmaz, ana makine süreçlerini yalıtmaz ve birden çok ana makineyi birleştirmez. Ağ, şifreleme ve HTTPS Tailscale tarafından sağlanır; TSLink bağımsız bir projedir.

<a id="agents"></a>

## Ajanlar için

**19 MCP aracı**, bir ajanın rapor paylaşmasını, hizmetleri yönetmesini, URL'leri almasını ve kurulumu
incelemesini sağlar. Yerel bir MCP istemcisini kurulu programa bağlayın:

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

MCP, TSLink'i yönetir; uygulamalar çıkarım için modelin HTTP API'sini kullanır.
Yapılandırma ve otomasyon için [MCP istemcilerine](mcp-clients.md), [uzak MCP'ye](remote-mcp.md) ve
[ajan kullanım kılavuzuna](../AGENTS.md) bakın.

CLI otomasyonu `--json` çıktısını destekler; `schema_version` değeri `1` olur; `tslink status --urls --json` kullanın. Yerel MCP, stdio üzerinden JSON-RPC kullanır. [JSON otomasyonuna](json-automation.md) bakın.

<a id="roadmap"></a>

## Yakında

Birleştiriliyor, İncelemede veya Planlandı olarak işaretli öğeler yukarıdaki kaynak kurulumuna dahil değildir.

| Kullanım | Durum |
|---|---|
| <!-- roadmap:people --> Bir yakınınıza özel HTTP/dosya uygulamalarına 3 günlük erişim verip davetleri tek mesajda toplayın; alıcı hâlâ Tailscale kullanmalıdır. | Birleştiriliyor |
| <!-- roadmap:health --> Uygulama sağlığını kontrol edip isteğe bağlı komut veya webhook ile kesinti ya da süre sonu uyarıları alın. | Birleştiriliyor |
| <!-- roadmap:recipes --> Desteklenen loopback uygulamalarını bulup paylaşmadan önce kendi barındırdığınız uygulamaların tariflerini önizleyin. | Birleştiriliyor |
| <!-- roadmap:limits --> Büyük yüklemeler ve yavaş istemciler için her HTTP uygulamasının yükleme boyutunu ve istek zaman aşımını ayarlayın. | Birleştiriliyor |
| <!-- roadmap:windows --> Windows oturumu açıkken çöken daemon’ı zamanlanmış görev ve yerleşik gözeticiyle yeniden başlatın. | Birleştiriliyor |
| <!-- roadmap:access-log --> Yerel erişim günlüklerinde kimin hangi uygulamayı açtığını, `prefix`, `full` veya `off` yol kaydıyla görün. | İncelemede |
| <!-- roadmap:portal --> İzin verilen uygulamaları tek ana sayfada, sahipler için kayıt yönlendirmesiyle açın; ziyaretçiler hâlâ Tailscale kullanmalıdır. | İncelemede |
| <!-- roadmap:mcp-scopes --> Bir ajana rol ve uygulama kapsamı verip değişiklikleri için denetim kayıtları tutun. | İncelemede |
| <!-- roadmap:guest-links --> Misafirin süreli bağlantı ve isteğe bağlı PIN ile Tailscale kurmadan tarayıcıda tek HTTP uygulamasını erişim kontrollü genel Funnel üzerinden açmasını sağlayın. | İncelemede |
| <!-- roadmap:durations --> En az 1 saatlik hazır veya özel süreler seçin; misafir üst sınırı varsayılan olarak 7 gündür ve değiştirilebilir. | İncelemede |
| <!-- roadmap:requests --> Telefon kullanıcılarını QR koduyla katılmaya yönlendirin ve erişim ya da ek süre isteklerini tek adımda onaylayın. | İncelemede |
| <!-- roadmap:multi-host --> Birden çok ana makinenin uygulamalarını tek listede görün. | Planlandı |

<a id="documentation"></a>

## Belgeler ve lisans

[Başlangıç](getting-started.md) · [Yerel modeller](local-ai.md) ·
[CLI başvuru kaynağı](cli-reference.md) · [Platformlar](platforms.md) · [Yol haritası](roadmap.md)

[CONTRIBUTING.md](../CONTRIBUTING.md) üzerinden katkıda bulunun; güvenlik açıklarını
[SECURITY.md](../SECURITY.md) yönergeleriyle bildirin.

TSLink, ticari kullanım da dahil olmak üzere değiştirilmemiş [Apache Lisansı 2.0](../LICENSE) kullanır.
Yeniden dağıtırken geçerli [NOTICE](../NOTICE) ve [üçüncü taraf bildirimlerini](../THIRD_PARTY_NOTICES.md) koruyun.
[Ticari iş birliği](../COMMERCIAL.md) isteğe bağlıdır ve ek bir lisans koşulu getirmez.
Tailscale hizmet koşulları ve planları ayrı olarak uygulanır.
