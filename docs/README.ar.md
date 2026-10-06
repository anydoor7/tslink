<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>امنح كل تطبيق على حاسوبك أو خادمك عنوانا خاصا به داخل شبكة Tailscale الخاصة بك، وقرر بنفسك من يستطيع الوصول إليه.</strong></p>

افتح تطبيقات الويب والمجلدات وواجهات نماذج الذكاء الاصطناعي وقواعد البيانات من هاتفك وحاسوبك المحمول، مع فحص صحة وسجل وصول لكل تطبيق. ويمكن لوكلاء الذكاء الاصطناعي أيضا نشرها وفحصها في حدود الدور الذي تمنحه لهم. وعندما يحتاج شخص آخر إلى الوصول، امنح شخصا محددا وصولا حتى تاريخ معين، أو افتح تطبيق ويب على الإنترنت العام لمدة محدودة.

**يتطلب Tailscale.** تحتاج إلى حساب Tailscale (مجاني للاستخدام الشخصي)، وكل جهاز يفتح تطبيقا خاصا يحتاج إلى تطبيق Tailscale؛ أما الضيوف والزوار العامون فيحتاجون إلى متصفح فقط. TSLink مشروع مستقل، لم تصنعه Tailscale ولم تعتمده. [المتطلبات](#requirements)

<p align="center"><a href="#quickstart">البدء السريع</a> · <a href="#agents">للوكلاء</a> · <a href="comparison.md">مقارنة مع Serve وngrok وCloudflare</a> · <a href="#documentation">الوثائق</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <strong>العربية</strong> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## ما يمكنك فعله

### الوصول إلى تطبيقاتك

- **عنوان لكل تطبيق.** يمنح `tslink share 3000` أو `tslink share ./photos` أو `tslink add db --tcp localhost:5432` تطبيق ويب أو مجلدا أو ملفا أو خدمة TCP عنوانا خاصا به داخل tailnet الخاصة بك (شبكتك الخاصة على Tailscale)، مثل `https://photos.<tailnet>.ts.net`. كل تطبيق جهاز Tailscale مستقل، لذلك تفتح التطبيقات بأسمائها بدلا من عنوان IP والمنفذ.
- **خاص ما لم تختر غير ذلك.** تبقى التطبيقات داخل tailnet الخاصة بك، وسياستها هي التي تحدد الأجهزة التي يمكنها الاتصال. لا يصل شيء إلى الإنترنت العام حتى تنشئ رابط ضيف أو تنشر تطبيقا.
- **كل شيء في مكان واحد.** يعرض `tslink status --urls` كل التطبيقات على هذا الحاسوب، وتعرض صفحة رئيسية خاصة اختيارية عنوان كل تطبيق وحالته. [البوابة](portal.md)
- **اعرف حين يتعطل شيء.** يمكن لفحوص الصحة في الخلفية تنبيهك عبر أمر أو webhook حين يتوقف تطبيق أو يعود، أو حين يوشك تسجيل دخوله إلى Tailscale على الانتهاء. ويعرض سجل الوصول من فتح أي تطبيق ومتى، بما في ذلك الطلبات المرفوضة. [الصحة والتنبيهات](health-and-alerts.md) · [سجل الوصول](access-log.md)
- **تطبيقات شائعة جاهزة.** تغطي الوصفات 15 تطبيقا مستضافا ذاتيا، منها Home Assistant وJellyfin وImmich وOllama، ويعثر `tslink apps detect` على التطبيقات التي تعمل بالفعل. وتحصل تطبيقات الصور والفيديو على [حدود رفع](sharing.md) تناسب الملفات الكبيرة. [وصفات التطبيقات](apps.md) · [الذكاء الاصطناعي المحلي](local-ai.md)

### دع وكلاءك يعملون عليها

الوكيل الذي يشغل خادم تطوير أو معاينة أو واجهة نموذج محلية يتركها على `localhost`، حيث لا يستطيع هاتفك وحواسيبك الأخرى فتحها. يتيح TSLink للوكيل نشرها بشكل خاص، وإخبارك بالعنوان الدقيق، ثم إزالتها، ضمن الحدود التي تضعها.

- **شارك، افحص، تراجع.** يعيد `share` الاسم الذي سجله، ومعه إما عنوان URL الدقيق أو رابط تسجيل دخول لتفتحه أنت. ويبلغ `url --wait` و`status` متى يصبح التطبيق متاحا، ويزيله `remove` (`unshare` في MCP). [دليل الوكلاء](agent-quickstart.md)
- **مصمم للأتمتة.** تقبل الأوامر `--json` وتعيد نتيجة ذات إصدار مع رموز أخطاء ثابتة. يقدم `tslink mcp` العمليات نفسها لعميل MCP محلي، ويقدمها `tslink serve --mcp` للوكلاء على أجهزتك الأخرى عبر tailnet. [أتمتة JSON](json-automation.md) · [MCP البعيد](remote-mcp.md)
- **صلاحيات محدودة.** الوكيل الذي تشغله بنفسك يعمل بصفة المالك. امنح الوكلاء الآخرين دورا مقيدا (`viewer` أو `app-operator` أو `people-manager`) يشمل فقط التطبيقات التي تحددها، ويضع حدا أقصى لمدة أي وصول يمنحه. تسجل التغييرات التي تتم عبر MCP، ويعرضها `tslink mcp-audit`. الأدوار تقيد أدوات TSLink، ولا تقيد سطر أوامر الوكيل نفسه أو ملفاته. [صلاحيات MCP](mcp-scopes.md)

### شارك مع من تختارهم

- **أشخاص محددون حتى تاريخ معين.** يسمح `tslink people add alice@example.com --apps photos,notes --for 7d` لحساب Tailscale هذا بفتح تطبيقات الويب والملفات المذكورة حتى الموعد النهائي. يغير `people update` و`extend` و`people remove` الوصول أو ينهيه؛ بعد الإزالة يرفض الطلب التالي لهذا الشخص، لكن لا يمكن استرجاع ما نزله بالفعل. [الأشخاص](people.md) · [المدد](durations.md)
- **شخص خارج tailnet الخاصة بك.** أضف `--invite --print-links` لتحصل على رسالة واحدة جاهزة للإرسال فيها دعوة جهاز لكل تطبيق (يتطلب ذلك رمز API مملوكا لمستخدم). ويطبع `--qr` رمزا لإعداد الهاتف.
- **الطلبات.** يمكن للأشخاص في tailnet الخاصة بك أن يطلبوا من الصفحة الرئيسية وقتا إضافيا، أو الوصول إلى تطبيق حددته بأنه قابل للطلب. وتوافق أنت بأمر واحد مع تحديد المدة. [طلبات الوصول](requests.md)

### افتح تطبيق ويب على الإنترنت لفترة

- **روابط الضيوف.** ينشئ `tslink guest create photos --for 3d --public --print-link` رابط متصفح إلى تطبيق ويب واحد، مع PIN اختياري، ويمكنك إلغاؤه وحده. لا يحتاج الضيوف إلى حساب Tailscale. يستطيع أي شخص يملك الرابط استخدامه، لذلك لا يثبت من زار التطبيق. [روابط الضيوف](guest-links.md)
- **عنوان URL عام مفتوح.** ينشر `tslink add preview --proxy localhost:3000 --funnel --public` تطبيق ويب لكل من يعرف عنوانه. وينتهي بعد 24 ساعة ما لم تحدد مدة أخرى عبر `--funnel-ttl`. [Funnel](funnel.md)
- يعمل كلاهما عبر Tailscale Funnel، وينتهي دائما (من ساعة واحدة إلى 7 أيام ما لم ترفع الحد)، ولا يعمل إلا مع تطبيقات الويب. تبقى المجلدات والملفات وخدمات TCP خاصة.

كل ما سبق متاح في v0.1.0.

<a id="requirements"></a>

## المتطلبات

TSLink مبني على Tailscale. وهو مشروع مستقل، لم تصنعه Tailscale ولم تعتمده، وتنطبق شروط Tailscale و[خططها](https://tailscale.com/pricing).

| من | ما يحتاجه |
|---|---|
| أنت | حساب Tailscale مع تفعيل [MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). خطة Personal المجانية مخصصة للاستخدام غير التجاري. |
| الحاسوب أو الخادم الذي يشغل تطبيقاتك | TSLink فقط. فهو يتضمن Tailscale، فلا حاجة إلى تثبيت منفصل. يطلب كل تطبيق جديد تسجيل الدخول من المتصفح، والموافقة على الجهاز إذا كانت tailnet الخاصة بك تشترط ذلك. |
| أجهزتك الأخرى | تطبيق Tailscale مسجلا الدخول إلى tailnet الخاصة بك. |
| الأشخاص الذين تختارهم | تطبيق Tailscale وحساب الدخول الخاص بهم. إما أن ينضموا إلى tailnet الخاصة بك، وهذا يضيف مستخدما إلى خطتك، أو يقبلوا دعوة جهاز لكل تطبيق. ويجب أن تسمح سياسة tailnet الخاصة بك بوصولهم. |
| الضيوف والزوار العامون | متصفح. ويجب أن تسمح tailnet الخاصة بك بـ Funnel، الذي لا تزال Tailscale تصنفه تجريبيا. |

يؤدي تفعيل HTTPS إلى نشر اسم tailnet الخاصة بك وأسماء أجهزتك، بما فيها اسم كل تطبيق، في سجل شهادات عام، لذلك اختر أسماء تطبيقات لا تمانع أن يراها الآخرون.

<a id="installation"></a>
<a id="quickstart"></a>

## البدء السريع

على macOS وLinux، ثبت TSLink باستخدام Homebrew. الملف التنفيذي لنظام macOS موقع بشهادة Developer ID وموثق من Apple. للترقية لاحقا شغل `brew upgrade --cask tslink`، ثم شغل `tslink install` مجددا إذا كان TSLink يعمل كخدمة في الخلفية.

```bash
brew install --cask anydoor7/tap/tslink
```

على Windows، قم بتنزيل `tslink_<version>_windows_<arch>.zip` من [أحدث إصدار](https://github.com/anydoor7/tslink/releases/latest)، وتحقق منه مقابل `checksums.txt`، ثم شغل `tslink install` ليبدأ TSLink عند تسجيل الدخول. ملف zip غير موقع بتقنية Authenticode؛ [تحقق من الإصدار](verify-release.md) عبر المجاميع الاختبارية الموقعة وشهادات الإثبات. حزم `.deb` و`.rpm` الخاصة بـLinux موجودة في صفحة الإصدار نفسها. للبناء من المصدر تحتاج إلى **Git وGo 1.26.6+**. الأوامر تستخدم bash/zsh. [إعداد macOS وLinux وWindows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

تحتاج إلى **حساب Tailscale** و[MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). أجهزة الوصول الخاص تحتاج Tailscale وإذن سياسة الشبكة. يدمج TSLink برنامج Tailscale في مضيف التطبيقات.

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
