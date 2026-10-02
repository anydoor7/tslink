<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="شعار TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>امنح تطبيقاتك ونماذجك وملفاتك المحلية عنوانًا خاصًا بها.</strong><br>
  افتحها من جهاز آخر مسموح له بالوصول ضمن شبكة Tailscale الخاصة بك.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="الترخيص: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 أو أحدث"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: عُقد tsnet مضمّنة"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 أداة"></a>
</p>

<p align="center" dir="ltr">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <strong>العربية</strong> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## التثبيت

تحتاج إلى **Go 1.26.6+** وGit. لم تُنشر إصدارات جاهزة أو
حزمة Homebrew cask؛ ثبّت البرنامج من المصدر. تستخدم هذه الأمثلة
**bash أو zsh**؛ راجع [دعم المنصات](platforms.md) لمعرفة متطلبات Windows والخدمات التي تعمل في الخلفية.

</div>

<div dir="ltr">

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

</div>

<div dir="rtl">

استخدم حساب Tailscale مع [تفعيل MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates).
يجب أن يكون الجهاز المستقبِل مسجّلًا في شبكة Tailscale الخاصة بك (**tailnet**)، وأن تسمح سياسة الشبكة
له بالوصول إلى الخدمة. يضمّن TSLink برنامج Tailscale على المضيف الذي ينشر الخدمة.

### شارك صفحتك الأولى

أنشئ صفحة؛ يقدّمها TSLink مباشرةً ويشغّل خدمته في الخلفية عند الحاجة:

</div>

<div dir="ltr">

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

</div>

<div dir="rtl">

إذا طبع TSLink رابط تسجيل، فافتحه لتفويض العقدة؛ وقد تتطلب شبكة tailnet الخاصة بك أيضًا
موافقة المسؤول على الجهاز. بعد ذلك احصل على العنوان الدقيق:

</div>

<div dir="ltr">

```bash
tslink url demo --wait
```

</div>

<div dir="rtl">

افتح هذا الرابط على جهاز مسموح له بالوصول. لا تحتاج إلى رمز API لهذه المشاركة الأولى.
[تفاصيل الإعداد الكامل ودورة الحياة ←](getting-started.md)

<a id="use-cases"></a>

## ماذا ستشارك؟

يجب أن تكون الملفات موجودة؛ ويجب أن تكون الخدمات الخلفية للتطبيقات وقواعد البيانات والنماذج قيد التشغيل بالفعل على المنافذ المحددة.

| حالة الاستخدام | الأمر |
|---|---|
| فتح تطبيق محلي من جهاز آخر | <span dir="ltr"><span dir="ltr">`tslink share 3000`</span></span> |
| تصفّح مجلد ملفات | <span dir="ltr"><span dir="ltr">`tslink share ./public --name files`</span></span> |
| قراءة تقرير HTML مولّد على هاتفك | <span dir="ltr"><span dir="ltr">`tslink share ./report.html --name report`</span></span> |
| الاتصال بقاعدة بيانات محلية عبر TCP | <span dir="ltr"><span dir="ltr">`tslink add database --tcp localhost:5432`</span></span> |
| استخدام واجهة HTTP API لنموذج محلي، مثل Ollama | <span dir="ltr"><span dir="ltr">`tslink add model --proxy localhost:11434`</span></span> |

بالنسبة إلى Ollama، احصل على الرابط الدقيق باستخدام <span dir="ltr"><span dir="ltr">`tslink url model --wait`</span></span>؛ تستخدم قيمة <span dir="ltr"><span dir="ltr">`baseURL`</span></span>
لعميل متوافق مع OpenAI ذلك الرابط مع إضافة <span dir="ltr"><span dir="ltr">`/v1`</span></span>. [النماذج المحلية وسير العمل مع البيانات الخاصة ←](local-ai.md)

لإدارة تطبيقات متعددة على مضيف واحد، يجمع TSLink عُقد خدمات مسماة وقوائم هويات مسموحة لـ HTTP وانتهاء صلاحية Funnel وإدارة MCP. قد يكفي [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) لتطبيق واحد على أجهزتك الخاصة.

<a id="architecture"></a>

## البنية

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="مثال لخريطة الخدمات: App وDocs وDatabase وModel عُقد منفصلة بأسماء محددة ضمن شبكة tailnet واحدة. تستخدم التطبيقات والملفات وواجهات API للنماذج بروتوكول HTTPS؛ وتستخدم قاعدة البيانات اتصال TCP خاصًا." width="960">
</picture>

**شبكة tailnet واحدة، وعُقد خدمات منفصلة.** تشغّل عملية خلفية مشتركة عقدة tsnet مضمّنة لكل
خدمة، فتوجّه HTTP أو تقدّم الملفات أو تعمل وسيطًا لاتصالات TCP. تسري تغييرات السجل أثناء
تشغيلها. لكل عقدة هوية شبكية خاصة بها؛ وتشترك الخدمات في المضيف الذي ينشرها.
[تفاصيل البنية ←](architecture.md)

| المكوّن | الدور |
|---|---|
| [Go](../go.mod) | برنامج أصلي لسطر الأوامر |
| [Tailscale tsnet](architecture.md) | عُقد الخدمات والنقل عبر شبكة tailnet |
| [Cobra](https://github.com/spf13/cobra) | الأوامر والمساعدة |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | وسائل نقل الاتصال للوكلاء |
| سلسلة مفاتيح نظام التشغيل ومدير خدمات المستخدم | بيانات اعتماد اختيارية وتشغيل في الخلفية |

تبقى الخدمات داخل شبكة tailnet الخاصة بك ما لم تفعّل صراحةً [Funnel العام](getting-started.md#more-examples).
تدعم خدمات HTTP والملفات قوائم السماح حسب الهوية (<span dir="ltr">`WhoIs`</span>, <span dir="ltr">`--allow`</span>)؛ ويستخدم TCP سياسة tailnet
وآلية المصادقة الخاصة بالخدمة الخلفية. راجع [حدود المشاركة](sharing.md).

لا يثبّت TSLink التطبيقات ولا يشغّل النماذج ولا يعزل عمليات المضيف ولا يجمع مضيفين متعددين. الشبكة والتشفير وHTTPS تأتي من Tailscale؛ TSLink مشروع مستقل.

<a id="agents"></a>

## للوكلاء

تتيح **أدوات MCP البالغ عددها 19** للوكيل مشاركة التقارير وإدارة الخدمات والحصول على الروابط وفحص
الإعداد. صِل عميل MCP محليًا بالبرنامج المثبّت:

</div>

<div dir="ltr">

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

</div>

<div dir="rtl">

يدير MCP برنامج TSLink؛ وتستخدم التطبيقات واجهة HTTP API للنموذج لتنفيذ الاستدلال.
راجع [عملاء MCP](mcp-clients.md) و[MCP البعيد](remote-mcp.md)
و[دليل تشغيل الوكلاء](../AGENTS.md) لمعرفة كيفية الإعداد والأتمتة.

تدعم أتمتة CLI الخيار <span dir="ltr">`--json`</span> مع <span dir="ltr">`schema_version`</span> بقيمة <span dir="ltr">`1`</span>؛ استخدم <span dir="ltr">`tslink status --urls --json`</span>. يستخدم MCP المحلي JSON-RPC عبر stdio. راجع [أتمتة JSON](json-automation.md).

<a id="roadmap"></a>

## ما القادم

العناصر الموسومة قيد الدمج أو قيد المراجعة أو مخطط غير مشمولة في التثبيت من المصدر أعلاه.

| حالة الاستخدام | الحالة |
|---|---|
| <!-- roadmap:people --> امنح قريبًا وصولًا إلى تطبيقات HTTP/الملفات الخاصة لمدة 3 أيام مع دعوات التطبيقات في رسالة واحدة؛ لا يزال المستلم بحاجة إلى Tailscale. | قيد الدمج |
| <!-- roadmap:health --> افحص صحة التطبيقات واستقبل تنبيهات التعطل أو انتهاء الصلاحية عبر أمر أو webhook اختياري. | قيد الدمج |
| <!-- roadmap:recipes --> اكتشف تطبيقات loopback المدعومة وعاين وصفات التطبيقات ذات الاستضافة الذاتية قبل مشاركتها. | قيد الدمج |
| <!-- roadmap:limits --> اضبط حجم الرفع ومهل الطلبات لكل تطبيق HTTP للملفات الكبيرة والعملاء البطيئين. | قيد الدمج |
| <!-- roadmap:windows --> أعد تشغيل daemon في Windows بعد تعطله أثناء تسجيل الدخول، باستخدام مهمة مجدولة ومشرف مدمج. | قيد الدمج |
| <!-- roadmap:access-log --> اعرف من فتح أي تطبيق في سجلات الوصول المحلية، مع أوضاع تسجيل المسار <span dir="ltr">`prefix`</span> أو <span dir="ltr">`full`</span> أو <span dir="ltr">`off`</span>. | قيد المراجعة |
| <!-- roadmap:portal --> افتح صفحة رئيسية واحدة تعرض التطبيقات المسموحة وتسليم التسجيل للمالكين؛ لا يزال الزوار بحاجة إلى Tailscale. | قيد المراجعة |
| <!-- roadmap:mcp-scopes --> امنح الوكيل دورًا ونطاق تطبيقات مع إيصالات تدقيق لتعديلاته. | قيد المراجعة |
| <!-- roadmap:guest-links --> دع ضيفًا يفتح تطبيق HTTP واحدًا في المتصفح دون تثبيت Tailscale، برابط مؤقت وPIN اختياري عبر Funnel عام مع بوابة تحقق. | قيد المراجعة |
| <!-- roadmap:durations --> اختر مددًا جاهزة أو مخصصة لا تقل عن ساعة واحدة، بحد أقصى افتراضي للضيف قدره 7 أيام قابل للتعديل. | قيد المراجعة |
| <!-- roadmap:requests --> ساعد مستخدمي الهواتف على الانضمام برمز QR ووافق على طلبات الوصول أو الوقت الإضافي بخطوة واحدة. | قيد المراجعة |
| <!-- roadmap:multi-host --> اعرض تطبيقات مضيفين متعددين في قائمة واحدة. | مخطط |

<a id="documentation"></a>

## الوثائق والترخيص

[بدء الاستخدام](getting-started.md) · [النماذج المحلية](local-ai.md) ·
[مرجع CLI](cli-reference.md) · [المنصات](platforms.md) · [خارطة الطريق](roadmap.md)

ساهم وفق [CONTRIBUTING.md](../CONTRIBUTING.md)؛ وأبلغ عن الثغرات باستخدام
[SECURITY.md](../SECURITY.md).

يستخدم TSLink [ترخيص Apache 2.0](../LICENSE) دون تعديل، بما في ذلك الاستخدام التجاري.
احتفظ بملف [NOTICE](../NOTICE) و[إشعارات الجهات الخارجية](../THIRD_PARTY_NOTICES.md) المنطبقة عند إعادة التوزيع.
[التعاون التجاري](../COMMERCIAL.md) طوعي ولا يضيف أي شرط إلى الترخيص.
تُطبّق شروط خدمة Tailscale وخططها بصورة منفصلة.

</div>
