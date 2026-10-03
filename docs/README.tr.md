<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logosu">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Bilgisayarınızdaki uygulamaları seçtiğiniz kişilerle, seçtiğiniz süre boyunca paylaşın.</strong><br>
  Her uygulama Tailscale ağınızda kendi özel adresini alır. Kimin erişimi olduğunu görün ve erişimi geri alın.
</p>

<p align="center">
  <a href="#quickstart">Hızlı başlangıç</a> · <a href="#agents">Ajanlar için</a> · <a href="getting-started.md">Belgeler</a> ·
  <strong>Türkçe</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Tüm diller</a>
</p>

## Ne için kullanılıyor

- **Çalışmanızı telefonunuzdan açın.** Betiğinizin oluşturduğu rapor, geliştirme sunucusu, not defteri veya yerel model API'si, izinli cihazların ulaşabildiği özel bir HTTPS adresinde.
- **Bir kişiye bir uygulamayı bir süreliğine açın.** Partneriniz fotoğraf arşivini bir hafta kullansın veya iş arkadaşınız önizlemeyi üç gün denesin. Erişim kendiliğinden sona erer; daha erken de bitirebilirsiniz.
- **Paylaşımı ajanınıza bırakın.** Kodlama ajanınız az önce bir pano oluşturdu. Bunu sizinle ve ekip arkadaşınızla cumaya kadar paylaşmasını isteyin. Şu anda nelerin paylaşıldığını da söyleyebilir ve paylaşımı geri alabilir.

Uygulamalarınız çalıştıkları yerde çalışmaya devam eder. TSLink her birine kimin ulaşabileceğini yönetir; neyin, kiminle ve ne zamana kadar paylaşıldığını tek listede tutar.

<a id="quickstart"></a>

## Hızlı başlangıç

**Go 1.26.6+**, Git ve [MagicDNS ile HTTPS etkin](https://tailscale.com/docs/how-to/set-up-https-certificates) bir Tailscale hesabı gerekir. Hazır derlenmiş sürümler henüz yayımlanmadığından kaynak koddan kurun:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Bir sayfa paylaşın:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

İlk kullanımda TSLink yeni hizmet düğümünü kaydetmek için bir giriş bağlantısı gösterir; tailnet ayrıca yönetici cihaz onayı isteyebilir. Kayıttan sonra hizmet URL'sini tailnet ağınıza giriş yapmış izinli bir cihazda açın. API belirteci gerekmez.

Paylaşılanları kontrol edip demoyu kaldırın:

```bash
tslink status --urls
tslink remove demo
```

Arka uçları çalışıyorsa şunları da paylaşabilirsiniz:

| İçerik | Komut |
|---|---|
| Yerel bir web uygulaması | `tslink share 3000` |
| Dosya klasörü | `tslink share ./public --name files` |
| Ollama gibi yerel model API'si | `tslink add model --proxy localhost:11434` |
| Özel TCP üzerinden veritabanı | `tslink add database --tcp localhost:5432` |
| Bilinen bir kendi barındırdığınız uygulama (Jellyfin, Immich, Home Assistant ve 13 diğer uygulama) | `tslink apps detect`, ardından `tslink apps share jellyfin --yes` |

[Başlangıç, platformlar ve arka plan hizmeti →](getting-started.md)

## Kimin açabileceğini seçin

| Kitle | Alıcıya gereken | Kimlik | Bitiş |
|---|---|---|---|
| **Kendi cihazlarınız** | tailnet ağınıza giriş | Doğrulanmış Tailscale hesabı | Uygulamayı kaldırdığınızda |
| **Belirlenen kişiler** (özel HTTP/dosyalar) | Tailscale hesabı; dışarıdakiler uygulama başına bir davet kabul eder | Doğrulanmış Tailscale hesabı | Belirlediğiniz son tarihte (`--for 7d`) veya `tslink people remove` ile |
| **URL'ye sahip herkes** (Funnel) | Tarayıcı | Herkes; uygulamanın kendi giriş koşulu geçerlidir | Varsayılan olarak 24 saat sonra (`--funnel-ttl`) |
| **Tarayıcı misafir bağlantısı** | Tarayıcı ve isteğe bağlı PIN | Bağlantıyı elinde tutan kişi | Kendi süresi dolduğunda veya iptal edilince |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Özel HTTP ve dosya paylaşımlarında son tarihler her istekte kontrol edilir. Erişimi geri almak yeni istekleri durdurur; indirilmiş verileri geri getiremez ve kabul edilmiş akışları veya WebSocket bağlantılarını kapatmaz. [Kişilerle paylaşım →](people.md) · [Paylaşım sınırları →](sharing.md)

<a id="agents"></a>

## Ajanlar için

TSLink bir MCP sunucusu içerir; ajan da sizin gibi paylaşabilir, listeleyebilir, açıklayabilir ve paylaşımları kaldırabilir. Yerel bir MCP istemcisine ekleyin:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Kesin sonuçlar.** CLI otomasyonu `--json`, `schema_version: 1` ve kararlı hata kodlarını destekler; `tslink mcp` ise JSON-RPC kullanır. `tslink manifest` her komutu ve bayrağı açıklar. Ajanlar URL'leri oluşturmak yerine `tslink url <name> --wait` ile gerçek URL'leri almalıdır.
- **Açık bekleme durumları.** İnsan girişi gerektiren yeni bir düğüm hazırmış gibi davranmak yerine `needs_login` bildirir.
- **Yetki.** Yerel MCP kullanıcınızın yetkileriyle çalışır. Uzak MCP isteğe bağlı açılır, yalnızca tailnet içinden erişilir ve belirttiğiniz hesaplarla ya da etiketlerle sınırlıdır. Ajan rolleri, uygulama kapsamları ve işlem makbuzları artık kullanılabilir.

TSLink MCP, TSLink uygulamasının kendisini yönetir. TSLink üzerinden başka bir MCP sunucusu yayımlarsanız onun araç izinlerini ayrıca yönetmeniz gerekir.
[Ajan rehberi →](agents.md) · [MCP istemcileri →](mcp-clients.md) · [Uzak MCP →](remote-mcp.md) · [JSON otomasyonu →](json-automation.md)

## Ne zaman başka araç kullanılmalı

| İstediğiniz | Düşünebileceğiniz araç |
|---|---|
| Zaten çalışan Tailscale istemcisiyle kendi cihazlarınızda tek yerel hizmet | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Birçok sunucuda kararlı adlara sahip, yönetici tarafından yönetilen hizmetler | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Tailscale hesabı olmadan webhook veya API demosu için herkese açık URL | [ngrok](https://ngrok.com/docs/start) veya [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Kendi barındırdığınız uygulamaları paylaşmanın yanında kurup çalıştırmak | [Umbrel](https://umbrel.com) veya [Coolify](https://coolify.io) |
| Kuruluş genelinde kimliğe dayalı erişim platformu | [Pangolin](https://github.com/fosrl/pangolin) veya [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

Bir kişi birden fazla uygulama çalıştırıyor ve kendisinin de ajanının da inceleyebileceği, uygulama ve kişi başına süreli erişim istiyorsa TSLink uygundur.

## Nasıl çalışır

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database ve Model tek bir tailnet içindeki ayrı adlandırılmış düğümlerdir; hizmetleri yayımlayan bilgisayarda tek TSLink arka plan süreci tarafından çalıştırılır." width="720">
</picture>

Tek bir arka plan süreci her uygulama için gömülü Tailscale düğümü çalıştırır, böylece her uygulama kendi adına ve adresine sahip olur. Özel HTTP ve dosya paylaşımlarında `WhoIs` ile kişi izinleri veya `--allow` kuralları erişimi denetler; kişi süreleri her istekte kontrol edilir. Ham TCP, tailnet ilkelerini ve arka uç kimlik doğrulamasını kullanır. Tailscale, tailnet aktarımını, şifrelemeyi ve sertifikaları sağlar; TSLink bağımsız bir projedir. Tüm uygulamalar yayımlayan bilgisayarı paylaşır; TSLink onları birbirinden yalıtmaz. [Mimari →](architecture.md)

## Durum

Şimdi kullanılabilir: uygulama başına özel adresler, süreli kişi izinleri ve davet paketleri, süreli herkese açık Funnel, uygulama sağlık kontrolleri ve uyarılar, kendi barındırdığınız uygulamalar için tarifler, uygulama başına istek sınırları, Windows çökme sonrası yeniden başlatma, CLI ve MCP. Ayrıca kullanılabilir: tarayıcı misafir bağlantıları, esnek süreler, erişim günlüğü, uygulama ana sayfası, kapsamı sınırlı ajan rolleri, QR ile katılım ve erişim istekleri.

Birden fazla bilgisayarı tek listede görüntüleme planlanıyor. [Yol haritası →](roadmap.md)

## Belgeler ve lisans

[llms.txt](../llms.txt) · [Ajanlar için hızlı başlangıç](agent-quickstart.md) · [Paylaşım aracı seçimi](comparison.md)

[Başlangıç](getting-started.md) · [CLI başvurusu](cli-reference.md) · [Platformlar](platforms.md) · [Yerel modeller](local-ai.md) · [Katkıda bulunma](../CONTRIBUTING.md) · [Güvenlik](../SECURITY.md)

Ticari kullanım dahil Apache License 2.0 geçerlidir. Yeniden dağıtırken [NOTICE](../NOTICE) ve [üçüncü taraf bildirimlerini](../THIRD_PARTY_NOTICES.md) koruyun. Tailscale şartları ve planları ayrıca geçerlidir.
