<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink, bilgisayarınızdaki her uygulamaya <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> biçiminde özel bir adres veren açık kaynaklı bir CLI ve MCP sunucusudur; böylece siz ya da coding agent'ınız çalışan bir geliştirme sunucusunu telefonunuzda açabilirsiniz. Bağımsız bir projedir, Tailscale tarafından yapılmamıştır.</p>
<p align="center">Telefonunuzda Tailscale uygulaması olmalı ve aynı tailnet'e giriş yapılmış olmalı.</p>
<p align="center"><strong>Türkçe</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Diğer diller</a></p>

```text
# Claude Code, CLI'ı kurduktan sonra (aşağıdaki Kurulum bölümüne bakın)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# mcpServers JSON'u kullanan yerel MCP istemcileri (diğerleri için istemcinizin MCP kurulumuna bakın)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Ajanınıza söyleyin: "Geliştirme sunucusunu paylaş, telefonumda açabileyim."
```

Aynı işlemin elle yapılışı; geliştirme sunucusunun 5173 numaralı bağlantı noktasını zaten dinlediği varsayılır. `--json` ile `share`, kaydedilen adı `data.name` içinde bildirir. Sonraki komutlarda bu adı kullanın: `--name` ile verdiğiniz ad alınmışsa, örneğin `web-2` gibi farklı olur.

```sh
tslink share 5173 --name web --json   # data.name; data.url, status needs_login ise data.auth_url (bir Tailscale giriş bağlantısı)
tslink url <name> --wait              # girişi bitirdikten sonra: tam URL
tslink remove <name>                  # işiniz bitince; share başarısız olduysa atlayın. Geliştirme sunucusu çalışmaya devam eder
```

<a id="installation"></a>
<a id="quickstart"></a>

## Kurulum

macOS makineleri macOS 13 Ventura veya daha yenisini gerektirir ([platform desteği](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Windows üzerinde Scoop ile yükleyin:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Yükseltmek için `brew upgrade --cask tslink` veya `scoop update; scoop update tslink` çalıştırın. TSLink arka plan hizmeti olarak çalışıyorsa yükseltmeden sonra `tslink install` komutunu yeniden çalıştırın.

Linux `.deb` ve `.rpm` paketleri ile Windows sürümleri [son sürüm](https://github.com/anydoor7/tslink/releases/latest) sayfasında. Bir uygulamayı ilk kez paylaştığınızda TSLink onun için bir Tailscale oturum açma bağlantısı gösterir. [Başlarken](getting-started.md)

<a id="agents"></a>

## Yapay zekâ ajanları için

Bir ajanın `localhost` üzerinde başlattığı geliştirme sunucusuna telefonunuzdan ulaşamazsınız. TSLink, ajanın ona özel bir adres vermesini, tam URL'yi size iletmesini ve iş bitince paylaşımı kaldırmasını sağlar. Telefonunuzda Tailscale uygulaması olmalı ve aynı tailnet'e giriş yapılmış olmalı.

2026-10-22 tarihinde Vite 8.3.5 ve bir iPhone 15 Pro ile denendi: ajan geliştirme sunucusunu MCP ile paylaştı, telefon özel URL'yi açtı ve sayfa kaydedince elle yenilemeye gerek kalmadan güncellendi.

Claude Code için önce CLI'ı kurun, sonra şunu çalıştırın:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Eklenti, bir [önizleme skill'i](../skills/tslink-preview/SKILL.md) ve aşağıdaki MCP bağlantısını içerir. Marketplace bu depoda kalır ve eklentiyi [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin) deposundan alır. Esas skill burada durur; eklentide CI ile denetlenen bir kopya bulunur. Diğer ajanlar aynı skill'i kullanabilir ya da MCP'ye doğrudan bağlanabilir. Bu entegrasyon yerel CLI gerektirir ve claude.ai sohbetinde çalışmaz. [Kurulum ve kayıt](agent-integrations.md)

`mcpServers` JSON'u kullanan yerel bir MCP istemcisi için (diğer istemciler için kendi MCP kurulum yönergelerine ve [MCP istemcileri rehberine](mcp-clients.md) bakın):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Geliştirme sunucusu başına bir ad.** Her paylaşım, kök yolda ve 443 numaralı bağlantı noktasında kendi özel adresini `https://<name>.<tailnet>.ts.net` alır. Alınmış bir ada sayı eki eklenir; bu yüzden ajanın geri aldığı URL'yi kullanın.
- **Her yeni uygulama için bir kişi gerekir.** Varsayılan kurulumda her yeni uygulama düğümü için bir kişinin Tailscale kaydını tamamlaması gerekir. Sonuç `needs_login` ise girişi tamamlayın, ardından tam URL için `tslink url <name> --wait` komutunu çalıştırın. Ayrıca yeni bir adın erişilebilir olması biraz sürebilir: iki test çalıştırmamızda 36 ile 38 saniye.
- **Kimler açabilir.** Varsayılan olarak politikanızın izin verdiği tailnet cihazları. MCP `share`, erişimi yalnızca kendi girişinizle sınırlamak için `allow` kabul eder; CLI'dan `tslink add web --proxy localhost:5173 --allow you@example.com` kullanın. Adlar herkese açık sertifika günlüklerinde görünür, yani gizli değildir.
- **Düzenle, kaydet, gör.** Kodu kaydetmek telefonunuzdaki sayfayı günceller. Uygulamanızın kendi Host ve Origin kuralları istekleri yine de engelleyebilir: testimizde Vite için `allowedHosts` değişikliği gerekmedi; Next.js denenmedi ve hâlâ `allowedDevOrigins` gerektiriyor.
- **Temizlik.** Ajan `unshare` çağırır ya da siz `tslink remove <name>` çalıştırırsınız; her zaman share sonucunun döndürdüğü adla. Kaldırma, paylaşımın kaydını siler ve TSLink düğümün kendisine ait olduğunu kanıtlayabildiğinde tailnet düğümünü silmeyi dener; bu yüzden sonuçtaki temizlik veya korunan durum alanlarına bakın. Geliştirme sunucusunu durdurmaz.
- **CLI veya MCP.** Yönetim komutları `--json` alır ve sürümlü sonuçlar döndürür; `tslink mcp` uygulama ve erişim işlemlerini MCP üzerinden sunar.
- **Klasör değil, tek dosya.** Bir ajan `tslink share ./report.html` ile yalnızca kendi içinde tamamlanmış HTML raporunu paylaşabilir; o klasördeki diğer dosyalara erişilemez.
- **Sınırlı roller.** `viewer`, `app-operator` veya `people-manager`; yalnızca belirttiğiniz uygulamalarla sınırlıdır. Yeni bir uygulamayı paylaşmak için `owner` rolü gerekir. `tslink mcp-audit` bir ajanın neleri değiştirdiğini gösterir. Roller TSLink'in araçlarını sınırlar, ajanın kendi kabuğunu değil.

[Ajan kılavuzu](agent-quickstart.md) · [MCP yetkileri](mcp-scopes.md) · [Uzak MCP](remote-mcp.md)

<a id="why"></a>

## TSLink'e ne zaman ihtiyaç duyarsınız

Kendi başlattığınız ve kendi telefonunuzda açtığınız tek bir uygulama için Serve yeterli. TSLink, bir ajanın başlattığı, çoğu zaman birden fazla olan geliştirme sunucuları içindir: her birine kendi özel adı verilmesi ve paylaşma, URL alma ve temizleme adımlarının CLI'da, MCP'de ve bir Claude Code skill'inde hazır gelmesi istendiğinde.

| İş | Yalnızca Tailscale | TSLink |
|---|---|---|
| Ajanınızın başlattığı birden fazla geliştirme sunucusu, her birine bir ad | Serve, makine başına tek ad altında cihaz düzeyinde rotalar tanımlar; Services her ad için bir tanım, onay ve etiketli bir ana makine ister | Uygulama başına bir düğüm; paylaşma, URL alma ve temizleme CLI'da (`share`, `url`, `remove`), MCP'de (`share`, `url`, `unshare`) ve bir skill'de hazır |
| Kendi başlattığınız tek bir uygulama, telefonunuzda | `tailscale serve 3000` yeterli | `tslink share 3000` |
| Bir kişi, bir uygulama, yedi gün | Politika kuralları, ardından bir JIT aracı veya elle kaldırma | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/dosyalar) |
| Tarayıcı bağlantısı, üç gün | Herkese açık Funnel; erişim kapısını ve zamanlanmış kapatmayı siz ekleyin | `tslink guest create photos --for 3d --public --print-link` (yalnızca HTTP) |

Özel alıcıların Tailscale'e ihtiyacı vardır. Konuk bağlantıları herkese açıktır, iletilebilir ve erişim anahtarı işlevi görür.

[Ayrıntılı karşılaştırma](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Uygulamalarınız, kendi cihazlarınızda

- **Her uygulamaya bir adres.** Web uygulamaları, klasörler, tek dosyalar ve TCP portları tailnet'inizde kendi adını alır; IP adresleri yerine adları kullanırsınız.
- **Varsayılan olarak özel.** Bir konuk bağlantısı oluşturana ya da Funnel ile yayımlayana kadar hiçbir şey herkese açık olmaz.
- **Tüm makine değil, tek bir uygulama.** Yayımladığınız her uygulamanın, yalnızca o uygulamaya yönlendiren kendi düğümü olur. Ana makinede Tailscale uygulaması yoksa TSLink, tailnet'inize ana makinenin başka hiçbir portunu eklemez.
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
