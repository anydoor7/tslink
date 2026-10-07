<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>عناوين خاصة لتطبيقاتك، على شبكة Tailscale الخاصة بك.</strong></p>
<p align="center">افتحها من أجهزتك. شارك أحدها مع شخص أو عبر رابط، حتى تاريخ تختاره.</p>
<p align="center"><strong>العربية</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">لغات أخرى</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## التثبيت

```sh
brew install --cask anydoor7/tap/tslink
```

حزم Linux بصيغتي `.deb` و`.rpm` وإصدارات Windows متاحة في [أحدث إصدار](https://github.com/anydoor7/tslink/releases/latest). في أول مرة تشارك فيها تطبيقا، يعرض TSLink رابط تسجيل دخول إلى Tailscale خاصا به. [البدء](getting-started.md)

<a id="use-cases"></a>

## تطبيقاتك، على أجهزتك

- **عنوان لكل تطبيق.** تحصل تطبيقات الويب والمجلدات والملفات المفردة ومنافذ TCP كل منها على اسم خاص بها في tailnet الخاصة بك، فتستخدم الأسماء بدلا من عناوين IP.
- **خاص افتراضيا.** لا شيء يصبح عاما حتى تنشئ رابط ضيف أو تنشر عبر Funnel.
- **صفحة رئيسية** تعرض تطبيقاتك مع حالتها. [البوابة](portal.md)
- **فحوص الصحة والتنبيهات** عبر أمر أو webhook، وسجل وصول يتضمن الطلبات المرفوضة. [الصحة والتنبيهات](health-and-alerts.md) · [سجل الوصول](access-log.md)
- **وصفات لـ 15 تطبيقا مستضافا ذاتيا**، منها Home Assistant وJellyfin وImmich وOllama. يعثر `tslink apps detect` على ما يعمل منها بالفعل. [وصفات التطبيقات](apps.md)

## شارك متى أردت

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

تنتهي صلاحية روابط الضيوف وعناوين URL العامة الجديدة، وتعمل لتطبيقات الويب فقط؛ أما المجلدات والملفات ومنافذ TCP فتبقى خاصة. [الأشخاص](people.md) · [روابط الضيوف](guest-links.md) · [الوصول العام](funnel.md)

<a id="agents"></a>

## لوكلاء الذكاء الاصطناعي

خادم التطوير الذي يشغّله وكيل على `localhost` لا يمكن الوصول إليه من هاتفك. يتيح TSLink للوكيل أن يمنحه عنوانا خاصا، ويبلغ عن عنوان URL الدقيق، ويزيله عند الانتهاء.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI أو MCP.** تقبل أوامر الإدارة `--json` وتعيد نتائج ذات إصدار؛ ويوفر `tslink mcp` عمليات التطبيقات والوصول عبر MCP.
- **أدوار محدودة.** `viewer` أو `app-operator` أو `people-manager`، محصورة في التطبيقات التي تحددها. يعرض `tslink mcp-audit` ما غيّره الوكيل. تحد الأدوار من أدوات TSLink، لا من shell الوكيل نفسه.

[دليل الوكلاء](agent-quickstart.md) · [صلاحيات MCP](mcp-scopes.md) · [MCP البعيد](remote-mcp.md)

<a id="architecture"></a>

## كيف يعمل

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="حاسوب أو خادم سحابي واحد: تدير CLI/MCP عملية خلفية مشتركة وعقدة لكل تطبيق. تتصل الأجهزة الخاصة عبر Tailscale المشفر، ويصل HTTPS/Funnel العام الاختياري إلى تطبيقات HTTP عبر بوابة ضيوف أو نشر مفتوح صريح." width="960">
</picture>

تشغّل عملية خلفية واحدة عقدة Tailscale منفصلة لكل تطبيق. يوفر Tailscale النقل عبر tailnet وشهادات HTTPS. يمكن حصر الوصول الخاص إلى الويب والملفات حسب هوية Tailscale باستخدام `--allow` ومنح الأشخاص؛ أما TCP الخام فيعتمد على سياسة tailnet الخاصة بك وتسجيل الدخول الخاص بالتطبيق. [البنية](architecture.md)

<a id="requirements"></a>

## المتطلبات

| من | ما يحتاجه |
|---|---|
| أنت | حساب Tailscale مع تفعيل MagicDNS وHTTPS |
| الجهاز الذي يشغّل تطبيقاتك | TSLink، وفيه Tailscale مدمج (وعلى Linux جلسة مستخدم systemd) |
| أجهزتك ومن تشارك معهم | تطبيق Tailscale |
| الضيوف | متصفح |

تظهر أسماء تطبيقات HTTPS في سجلات الشهادات العامة، لذا اختر أسماء لا تمانع أن يراها الآخرون.

<a id="documentation"></a>

## المزيد

[كل الوثائق](INDEX.md) · [مرجع CLI](cli-reference.md) · [مقارنة مع Serve وngrok وCloudflare](comparison.md) · [المساهمة](../CONTRIBUTING.md) · [الأمان](../SECURITY.md)

Apache 2.0. TSLink مشروع مستقل، لم تصنعه Tailscale ولم تعتمده.

</div>
