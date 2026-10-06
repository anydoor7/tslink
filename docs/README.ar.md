<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>امنح كل تطبيق على حاسوبك أو خادمك عنوانا خاصا به داخل شبكة Tailscale الخاصة بك، وقرر بنفسك من يستطيع الوصول إليه.</strong></p>

افتح تطبيقات الويب والمجلدات وواجهات نماذج الذكاء الاصطناعي وقواعد البيانات من هاتفك وحاسوبك المحمول، مع فحص صحة وسجل وصول لكل تطبيق. ويمكن لوكلاء الذكاء الاصطناعي، في حدود الدور الذي تمنحه لهم، منح التطبيقات التي يشغلونها على localhost عنوانا خاصا تفتحه من أجهزتك الأخرى، وفحص هذه التطبيقات. وعندما يحتاج شخص آخر إلى الوصول، امنح شخصا محددا وصولا حتى تاريخ معين، أو افتح تطبيق ويب على الإنترنت العام لمدة محدودة.

**يتطلب Tailscale.** تحتاج إلى حساب Tailscale (مجاني للاستخدام الشخصي)، وكل جهاز يفتح تطبيقا خاصا يحتاج إلى تطبيق Tailscale؛ أما الضيوف والزوار العامون فيحتاجون إلى متصفح فقط. TSLink مشروع مستقل، لم تصنعه Tailscale ولم تعتمده. [المتطلبات](#requirements)

<p align="center"><a href="#quickstart">البدء السريع</a> · <a href="#agents">للوكلاء</a> · <a href="comparison.md">مقارنة مع Serve وngrok وCloudflare</a> · <a href="#documentation">الوثائق</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <strong>العربية</strong> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## ما يمكنك فعله

### الوصول إلى تطبيقاتك

- **عنوان لكل تطبيق.** يمنح `tslink share 3000` أو `tslink share ./photos` أو `tslink add db --tcp localhost:5432` تطبيق ويب أو مجلدا أو ملفا أو خدمة TCP عنوانا خاصا به داخل tailnet الخاصة بك (شبكتك الخاصة على Tailscale)، مثل `https://photos.<tailnet>.ts.net`. كل تطبيق جهاز Tailscale مستقل، لذلك تفتح التطبيقات بأسمائها بدلا من عنوان IP والمنفذ.
- **خاص ما لم تختر غير ذلك.** يبقي TSLink الوصول إلى التطبيقات خاصا افتراضيا، وسياسة tailnet الخاصة بك هي التي تحدد الأجهزة التي يمكنها الاتصال. ولا يفتح نقطة وصول عامة لتطبيق إلا عندما تنشئ رابط ضيف أو تنشر صراحة عبر Funnel.
- **كل شيء في مكان واحد.** يعرض `tslink status --urls` التطبيقات المسجلة على هذا الحاسوب، وتعرض صفحة رئيسية خاصة اختيارية عناوينها وحالتها. [البوابة](portal.md)
- **اعرف حين يتعطل شيء.** يمكن لفحوص الصحة في الخلفية تنبيهك عبر أمر أو webhook حين يتوقف تطبيق أو يعود، أو حين يوشك تسجيل دخوله إلى Tailscale على الانتهاء. ويعرض سجل الوصول من فتح أي تطبيق ومتى، بما في ذلك الطلبات المرفوضة. [الصحة والتنبيهات](health-and-alerts.md) · [سجل الوصول](access-log.md)
- **وصفات التطبيقات.** تغطي الوصفات 15 تطبيقا مستضافا ذاتيا، منها Home Assistant وJellyfin وImmich وOllama، ويستطيع `tslink apps detect` العثور على التطبيقات المدعومة التي تستمع محليا بالفعل. ولرفع الصور ومقاطع الفيديو الكبيرة، [زد حدود الرفع لكل تطبيق](sharing.md). [وصفات التطبيقات](apps.md) · [الذكاء الاصطناعي المحلي](local-ai.md)

### دع وكلاءك يعملون عليها

عندما يشغل وكيل خادم تطوير أو معاينة أو واجهة نموذج محلية على `localhost`، لا يستطيع هاتفك وحواسيبك الأخرى الوصول إلى ذلك العنوان. يتيح TSLink للوكيل منحها عنوانا خاصا، وإخبارك بعنوان URL الدقيق، ثم إزالة تسجيلها، ضمن الحدود التي تضعها.

- **شارك، افحص، تراجع.** يعيد `share --json` الاسم الذي سجله، ومعه إما عنوان URL الدقيق أو رابط تسجيل دخول لتفتحه أنت. ويبلغ `url <name> --wait` و`status --urls --name <name>` ما إذا كانت نقطة الوصول جاهزة، ويزيل `remove <name>` (`unshare` في MCP) المشاركة. [دليل الوكلاء](agent-quickstart.md)
- **مصمم للأتمتة.** تقبل أوامر الإدارة، باستثناء `tslink mcp`، الخيار `--json` وتعيد نتائج ذات إصدار مع رموز أخطاء ثابتة. يقدم `tslink mcp` أدوات التطبيقات والوصول لعميل MCP محلي عبر JSON-RPC. وعند إعداد ربط المستدعين، يقدم `tslink serve --mcp` هذه الأدوات لعملاء MCP على أجهزتك الأخرى عبر tailnet. [أتمتة JSON](json-automation.md) · [MCP البعيد](remote-mcp.md)
- **صلاحيات محدودة.** يملك الوكيل المحلي صلاحيات المالك افتراضيا. امنح الوكيل دورا مقيدا (`viewer` أو `app-operator` أو `people-manager`) يشمل فقط التطبيقات التي تحددها، ويضع حدا أقصى لمدة أي وصول يمنحه. تسجل التغييرات التي تتم عبر MCP، ويعرضها `tslink mcp-audit`. الأدوار تقيد أدوات TSLink، ولا تقيد سطر أوامر الوكيل نفسه أو ملفاته. [صلاحيات MCP](mcp-scopes.md)

### شارك مع من تختارهم

- **أشخاص محددون حتى تاريخ معين.** يسمح `tslink people add alice@example.com --apps photos,notes --for 7d` لحساب Tailscale هذا بفتح تطبيقات الويب والملفات المذكورة حتى الموعد النهائي. يغير `people update` و`extend` و`people remove` الوصول أو ينهيه؛ بعد الإزالة يرفض الطلب التالي لهذا الشخص، لكن لا يمكن استرجاع ما نزله بالفعل. [الأشخاص](people.md) · [المدد](durations.md)
- **شخص خارج tailnet الخاصة بك.** أضف `--invite --print-links` لتحصل على رسالة واحدة جاهزة للإرسال فيها دعوة جهاز لكل تطبيق (يتطلب ذلك رمز API مملوكا لمستخدم). ويطبع `--qr` رمزا لإعداد الهاتف.
- **الطلبات.** يمكن للأشخاص في tailnet الخاصة بك أن يطلبوا من الصفحة الرئيسية وقتا إضافيا، أو الوصول إلى تطبيق حددته بأنه قابل للطلب. وتوافق أنت بأمر واحد مع تحديد المدة. [طلبات الوصول](requests.md)

### افتح تطبيق ويب على الإنترنت لفترة

- **روابط الضيوف.** ينشئ `tslink guest create photos --for 3d --public --print-link` رابط متصفح إلى تطبيق ويب واحد، مع PIN اختياري، ويمكنك إلغاؤه وحده. لا يحتاج الضيوف إلى حساب Tailscale. يستطيع أي شخص يملك الرابط استخدامه، لذلك لا يثبت من زار التطبيق. [روابط الضيوف](guest-links.md)
- **عنوان URL عام مفتوح.** ينشر `tslink add preview --proxy localhost:3000 --funnel --public` تطبيق ويب لكل من يعرف عنوانه. ومدة النشر الجديد 24 ساعة افتراضيا، ويمكنك اختيار مدة أخرى عبر `--funnel-ttl`. [Funnel](funnel.md)
- تعمل روابط الضيوف الجديدة وعمليات النشر العامة المفتوحة الجديدة عبر Tailscale Funnel، ولها مدة محدودة (ساعة واحدة على الأقل، و7 أيام كحد أقصى افتراضي، ويمكن للمالك تغيير هذا الحد). وتدعم هذه المسارات العامة تطبيقات HTTP التي تمر عبر خادم وسيط، أما خدمات المجلدات والملفات المباشرة وTCP الخام فتبقى خاصة.

كل ما سبق متاح في v0.1.0.

<a id="requirements"></a>

## المتطلبات

TSLink مبني على Tailscale. وهو مشروع مستقل، لم تصنعه Tailscale ولم تعتمده، وتنطبق شروط Tailscale و[خططها](https://tailscale.com/pricing).

| من | ما يحتاجه |
|---|---|
| أنت | حساب Tailscale مع تفعيل [MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). خطة Personal المجانية مخصصة للاستخدام غير التجاري. |
| الحاسوب أو الخادم الذي يشغل تطبيقاتك | TSLink فقط. فهو يضم Tailscale مدمجا، فلا حاجة إلى تثبيت Tailscale بشكل منفصل. في الإعداد الافتراضي، تحتاج كل عقدة تطبيق جديدة إلى تسجيل الدخول من المتصفح، وقد تحتاج إلى الموافقة على الجهاز. وتتيح [بيانات الاعتماد المخزنة](credentials-and-tags.md) انضمام التطبيقات دون تسجيل الدخول من المتصفح لكل تطبيق. |
| أجهزتك الأخرى | تطبيق Tailscale مسجلا الدخول إلى tailnet الخاصة بك. |
| الأشخاص الذين تختارهم | تطبيق Tailscale وحساب الدخول الخاص بهم. إما أن ينضموا إلى tailnet الخاصة بك، وهذا يضيف مستخدما إلى خطتك، أو يقبلوا دعوة جهاز لكل تطبيق. ويجب أن تسمح سياسة tailnet الخاصة بك بوصولهم. |
| الضيوف والزوار العامون | متصفح. ويجب أن تسمح tailnet الخاصة بك بـ Funnel، الذي لا تزال Tailscale تصنفه تجريبيا. |

عند إصدار شهادة HTTPS لتطبيق، يظهر اسم جهازه في Tailscale واسم DNS لشبكة tailnet الخاصة بك في سجل شهادات عام. اختر أسماء تطبيقات لا تمانع أن يراها الآخرون.

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
