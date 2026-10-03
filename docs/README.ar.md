<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>صل إلى تطبيقاتك وأدرها من أي مكان.<br>أبقها خاصة أو شاركها بشروطك.</strong></p>

تطبيقاتك على حاسوبك أو خادمك السحابي: صل إليها عبر شبكة خاصة مشفرة، أو اختر روابط ضيوف للمتصفح أو وصولا عاما. أدرها بنفسك أو بواسطة وكيل.

<p align="center"><a href="#quickstart">البدء السريع</a> · <a href="#agents">للوكلاء</a> · <a href="#documentation">الوثائق</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <strong>العربية</strong> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## تطبيقاتك في متناولك

| ما تحتاجه | ما يقدمه TSLink |
|---|---|
| استخدام تطبيقاتك عبر الأجهزة | عناوين خاصة للوحات المنزل وصفحات الويب المحلية والملفات وواجهات نماذج الذكاء الاصطناعي وخدمات TCP على الحاسوب أو الخادم. |
| المشاركة مع أشخاص محددين | تطبيقات HTTP/ملفات مختارة، وهوية Tailscale موثقة، وانتهاء صلاحية وإلغاء وصول. يحتاج المستلمون إلى Tailscale. [الأشخاص](people.md) |
| استقبال زائر من المتصفح | روابط مؤقتة مع PIN اختياري لتطبيقات وكيل HTTP، أو HTTPS عام عبر Funnel بتفعيل صريح. يمكن إعادة إرسال الروابط؛ وهي لا تثبت هوية الشخص. [روابط الضيوف](guest-links.md) |
| إدارة مجموعة تطبيقات | قائمة لكل مضيف، وبوابة خاصة، وفحوص صحة وتنبيهات، وسجل وصول، وإدارة CLI/MCP بأدوار للوكلاء ونطاقات تطبيقات وسجلات تدقيق. [البوابة](portal.md) · [صلاحيات MCP](mcp-scopes.md) |

تسهل [وصفات التطبيقات](apps.md) و[حدود الرفع](sharing.md) و[المدد المرنة](durations.md) و[إرشادات QR وطلبات الوصول](requests.md) الصيانة اليومية. هذه الميزات موجودة في الشفرة الحالية.

<a id="installation"></a>
<a id="quickstart"></a>

## البدء السريع

ثبت من المصدر باستخدام **Git وGo 1.26.6+**؛ لم تنشر بعد إصدارات مبنية مسبقا أو Homebrew. الأوامر تستخدم bash/zsh. [إعداد macOS وLinux وWindows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

تحتاج إلى إذن للوصول إلى المستودع، و**حساب Tailscale**، و[MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). أجهزة الوصول الخاص تحتاج Tailscale وإذن سياسة الشبكة. يدمج TSLink برنامج Tailscale في مضيف التطبيقات.

إذا كان تطبيقك يعمل بالفعل على المنفذ 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

اختر اسما غير مستخدم؛ إذا أعاد `share` اسما آخر فاستخدمه في `url`. أكمل أولا تسجيل المتصفح والموافقة على الجهاز إن طلبا، ثم افتح عنوان التطبيق الدقيق من جهاز مسموح. يبدأ `share` الخدمة الخلفية عند الحاجة. لا يتطلب هذا الاستخدام الخاص الأول رمز API إداريا. شارك الملفات باستخدام `tslink share ./report.html`؛ يجب أن تكون موجودة وأن تكون التطبيقات قيد التشغيل. [الإعداد الكامل](getting-started.md)

بعد نجاح الاستخدام، يمكنك [منح TSLink نجمة](https://github.com/anydoor7/tslink) ليسهل على الآخرين اكتشافه. الأمر اختياري تماما.

<a id="architecture"></a>

## كيف تعمل الأجزاء معا

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="حاسوب أو خادم سحابي واحد: تدير CLI/MCP عملية خلفية مشتركة وعقدة لكل تطبيق. تتصل الأجهزة الخاصة عبر Tailscale المشفر، ويصل HTTPS/Funnel العام الاختياري إلى تطبيقات HTTP عبر بوابة ضيوف أو نشر مفتوح صريح." width="960">
</picture>

تصور مسارا خاصا مشفرا إلى تطبيقاتك. **يوفر Tailscale نقل الشبكة وHTTPS؛ ويدير TSLink الوصول إلى التطبيقات على كل مضيف.** تشغل عملية خلفية واحدة عقدة مدمجة مستقلة لكل خدمة. تعرض البوابة الخاصة التطبيقات المسموح بها؛ وتساعد الصحة وسجلات الوصول في صيانتها.

الوصول العام اختياري: يحتاج الضيف الرابط وPIN إن كان مفعلا؛ أما Funnel المفتوح فيمكن لأي حامل للعنوان الوصول إليه. كلاهما يستخدم HTTPS عاما، لا هوية مستخدم خاصة. يبقى TCP الخام خاصا ويعتمد على سياسة tailnet ومصادقة الخدمة الخلفية. لا يثبت TSLink التطبيقات ولا يعزل العمليات ولا ينشئ VPC سحابية ولا يجمع عدة مضيفين. مشروع مستقل يعمل مع Tailscale. [البنية والحدود](architecture.md)

<a id="agents"></a>

## للوكلاء

أدر القوائم والصحة والعناوين والوصول عبر CLI/MCP. ابدأ بـ[دليل الوكلاء](agent-quickstart.md)، واقرأ مخططات الأدوات الحالية، وتحقق من الوصول الفعلي قبل إعلان النجاح.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

تستخدم أتمتة CLI الخيار `--json`؛ ويستخدم MCP بروتوكول JSON-RPC عبر stdio. [العملاء](mcp-clients.md) · [MCP البعيد](remote-mcp.md) · [الأدوار والنطاقات](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## الوثائق والترخيص

[كل الأدلة](INDEX.md) · [مرجع CLI](cli-reference.md) · [الذكاء الاصطناعي المحلي](local-ai.md) · [الصحة](health-and-alerts.md) · [سجل الوصول](access-log.md) · [خطة التطوير](roadmap.md)

قائمة التطبيقات المشتركة بين عدة مضيفين مخطط لها. نرحب بـ[المساهمات](../CONTRIBUTING.md) و[تقارير الأمان](../SECURITY.md). يسمح [Apache 2.0](../LICENSE) بالاستخدام التجاري؛ احتفظ بـ[NOTICE](../NOTICE) و[إشعارات الأطراف الثالثة](../THIRD_PARTY_NOTICES.md) عند إعادة التوزيع. [التعاون التجاري](../COMMERCIAL.md) طوعي. تنطبق شروط Tailscale وخططه بشكل مستقل.

</div>
