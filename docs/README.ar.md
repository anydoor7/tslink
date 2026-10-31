<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink هو CLI وخادم MCP مفتوحا المصدر يمنح كل تطبيق على حاسوبك عنوانا خاصا بصيغة <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>، فتتمكن أنت أو coding agent الخاص بك من فتح خادم تطوير قيد التشغيل على هاتفك. مشروع مستقل، ولم تصنعه Tailscale.</p>
<p align="center">يحتاج هاتفك إلى تطبيق Tailscale، مع تسجيل الدخول إلى شبكة tailnet نفسها.</p>
<p align="center"><strong>العربية</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">لغات أخرى</a></p>

```text
# Claude Code، بعد تثبيت CLI (انظر التثبيت أدناه)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# عملاء MCP المحليون الذين يستخدمون JSON بصيغة mcpServers (غيرهم: راجع إعداد MCP في عميلك)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

قل لوكيلك: "شارك خادم التطوير حتى أتمكن من فتحه على هاتفي."
```

الأمر نفسه يدويا، مع افتراض أن خادم التطوير يستمع بالفعل على المنفذ 5173. مع `--json`، يبلّغ `share` عن الاسم المسجّل في `data.name`. استخدم هذا الاسم في الأوامر اللاحقة: فهو يختلف عن `--name` إذا كان ذلك الاسم مأخوذا، مثل `web-2`.

```sh
tslink share 5173 --name web --json   # data.name؛ وdata.url، أو data.auth_url (رابط تسجيل دخول Tailscale) عندما تكون status هي needs_login
tslink url <name> --wait              # بعد إكمال تسجيل الدخول: عنوان URL الدقيق
tslink remove <name>                  # عند الانتهاء؛ تجاوزه إذا فشل share. يستمر خادم التطوير في العمل
```

<a id="installation"></a>
<a id="quickstart"></a>

## التثبيت

تتطلب أجهزة macOS إصدار macOS 13 Ventura أو أحدث ([دعم المنصات](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

على Windows، ثبّت باستخدام Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

للترقية، شغّل `brew upgrade --cask tslink` أو `scoop update; scoop update tslink`. إذا كان TSLink يعمل كخدمة في الخلفية، فأعد تشغيل `tslink install` بعد الترقية.

حزم Linux بصيغتي `.deb` و`.rpm` وإصدارات Windows متاحة في [أحدث إصدار](https://github.com/anydoor7/tslink/releases/latest). في أول مرة تشارك فيها تطبيقا، يعرض TSLink رابط تسجيل دخول إلى Tailscale خاصا به. [البدء](getting-started.md)

<a id="agents"></a>

## لوكلاء الذكاء الاصطناعي

خادم التطوير الذي يشغّله وكيل على `localhost` لا يمكن الوصول إليه من هاتفك. يتيح TSLink للوكيل أن يمنحه عنوانا خاصا، ويسلّمك عنوان URL الدقيق، ويزيل المشاركة عند الانتهاء. يحتاج هاتفك إلى تطبيق Tailscale، مع تسجيل الدخول إلى شبكة tailnet نفسها.

جرى الاختبار في 2026-10-22 باستخدام Vite 8.3.5 وهاتف iPhone 15 Pro: شارك الوكيل خادم التطوير عبر MCP، وفتح الهاتف عنوان URL الخاص، وتحدّثت الصفحة عند الحفظ دون إعادة تحميل يدوية.

مع Claude Code، ثبّت CLI أولا، ثم شغّل:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

تتضمن الإضافة [مهارة المعاينة](../skills/tslink-preview/SKILL.md) واتصال MCP الموضح أدناه. يبقى السوق في هذا المستودع ويجلب الإضافة من [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). تبقى المهارة المرجعية هنا؛ وتحمل الإضافة نسخة تفحصها CI. يمكن لوكلاء آخرين استخدام المهارة نفسها أو الاتصال بـ MCP مباشرة. يتطلب هذا التكامل CLI محليا ولا يعمل داخل دردشة claude.ai. [الإعداد والتسجيل](agent-integrations.md)

لعميل MCP محلي يستخدم JSON بصيغة `mcpServers` (لعملاء آخرين: راجع تعليمات إعداد MCP لديهم و[دليل عملاء MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **البوابة افتراضية في التثبيتات الجديدة.** يحصل كل تطبيق HTTP/ملفات خاص على منفذ ثابت، مثل `https://home.<tailnet>.ts.net:8443/`. تحتفظ التثبيتات الحالية بوضعها وتحتفظ التطبيقات بعقدها وعناوينها الخاصة؛ ويظل `--portal-only` اختيارا صريحا.
- **سجّل البوابة.** بدون بيانات اعتماد، يمكن لحساب البوابة نفسها بعد التحقق منه أن يصبح owner إذا كانت بلا وسوم. مع الوسوم/OAuth، شغّل محليا `tslink portal enable --owner <login>`. انتظار تسجيل عقد التطبيقات لا يعطل مسار بوابة جاهزا.
- **وصول لكل شخص.** تسمح التطبيقات الجديدة للـ owner/admin وللأشخاص المصرح لهم صراحة. تتحقق `people add` و`add --allow` وMCP `add/share.allow` من العضوية وتجهز الدعوات اللازمة. الأعضاء لا يحتاجون دعوة جهاز؛ ويقبل الزائر الخارجي دعوة بوابة واحدة. أخطاء الدليل/API تعيد حالة إعداد غير مكتمل.
- **عدّل، احفظ، شاهد.** يؤدي حفظ الشيفرة إلى تحديث الصفحة على هاتفك. قد تحجب قواعد Host وOrigin الخاصة بتطبيقك بعض الطلبات: في اختبارنا لم يحتج Vite إلى تغيير `allowedHosts`؛ أما Next.js فلم يُختبر ولا يزال يتطلب `allowedDevOrigins`.
- **التنظيف.** يستدعي الوكيل `unshare`، أو تشغّل أنت `tslink remove <name>`، دائما بالاسم الذي أعاده ناتج share. تلغي الإزالة تسجيل المشاركة وتحاول حذف عقدتها في tailnet عندما يستطيع TSLink إثبات ملكيتها، لذا راجع في النتيجة حقول التنظيف أو الحالة المحتفظ بها. ولا توقف خادم التطوير.
- **CLI أو MCP.** تقبل أوامر الإدارة `--json` وتعيد نتائج ذات إصدار؛ ويوفر `tslink mcp` عمليات التطبيقات والوصول عبر MCP.
- **ملف واحد، لا المجلد كله.** يمكن للوكيل أن يشارك تقرير HTML المكتفي بذاته وحده باستخدام `tslink share ./report.html`؛ وتبقى الملفات الأخرى في ذلك المجلد غير قابلة للوصول.
- **أدوار محدودة.** `viewer` أو `app-operator` أو `people-manager`، محصورة في التطبيقات التي تحددها؛ ومشاركة تطبيق جديد تتطلب دور `owner`. يعرض `tslink mcp-audit` ما غيّره الوكيل. تحد الأدوار من أدوات TSLink، لا من shell الوكيل نفسه.

[دليل الوكلاء](agent-quickstart.md) · [صلاحيات MCP](mcp-scopes.md) · [MCP البعيد](remote-mcp.md)

<a id="why"></a>

## متى تحتاج TSLink

يكفي Serve لتطبيق واحد تشغّله بنفسك وتفتحه على هاتفك. أما TSLink فهو لخوادم التطوير التي يشغّلها وكيل، وغالبا تكون عدة خوادم، حين يجب أن يحصل كل منها على اسمه الخاص وتكون خطوات المشاركة والحصول على URL والتنظيف جاهزة في CLI وMCP ومهارة Claude Code.

| المهمة | Tailscale وحده | TSLink |
|---|---|---|
| عدة خوادم تطوير يشغّلها وكيلك، لكل منها اسم | يضبط Serve مسارات على مستوى الجهاز تحت اسم واحد لكل جهاز؛ أما Services فيتطلب لكل اسم تعريفا وموافقة ومضيفا موسوما | عقدة واحدة لكل تطبيق، مع مشاركة والحصول على URL وتنظيف جاهزة في CLI (`share` و`url` و`remove`) وMCP (`share` و`url` و`unshare`) ومهارة |
| تطبيق واحد تشغّله بنفسك، على هاتفك | يكفي `tailscale serve 3000` | `tslink share 3000` |
| شخص واحد، تطبيق واحد، سبعة أيام | قواعد سياسة، ثم أداة JIT أو إزالة يدوية | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/ملفات) |
| رابط للمتصفح، ثلاثة أيام | Funnel عام؛ أضف بنفسك بوابة وصول وإيقافًا مجدولًا | `tslink guest create photos --for 3d --public --print-link` (HTTP فقط) |

يحتاج المستلمون الخاصون إلى Tailscale. روابط الضيوف عامة وقابلة لإعادة الإرسال، وتُستخدم كبيانات اعتماد للوصول.

[المقارنة الكاملة](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## تطبيقاتك، على أجهزتك

- **عنوان لكل تطبيق.** تحصل تطبيقات الويب والمجلدات والملفات المفردة ومنافذ TCP كل منها على اسم خاص بها في tailnet الخاصة بك، فتستخدم الأسماء بدلا من عناوين IP.
- **خاص افتراضيا.** لا شيء يصبح عاما حتى تنشئ رابط ضيف أو تنشر عبر Funnel.
- **تطبيق واحد، لا الجهاز كله.** كل تطبيق تنشره يحصل على عقدة خاصة به لا تعيد التوجيه إلا إلى ذلك التطبيق. إذا لم يكن تطبيق Tailscale مثبتا على المضيف، فلا يضيف TSLink أي منافذ أخرى من المضيف إلى tailnet الخاصة بك.
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
