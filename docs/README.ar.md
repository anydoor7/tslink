<div dir="rtl">

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="شعار TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>شارك تطبيقات حاسوبك مع الأشخاص الذين تختارهم، للمدة التي تختارها.</strong><br>
  يحصل كل تطبيق على عنوان خاص به داخل شبكة Tailscale لديك. اعرف من يملك حق الوصول واسحبه متى شئت.
</p>

<p align="center" dir="ltr">
  <a href="#quickstart">البدء السريع</a> · <a href="#agents">للوكلاء</a> · <a href="getting-started.md">الوثائق</a> ·
  <strong>العربية</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">جميع اللغات</a>
</p>

## كيف يستخدمه الناس

- **افتح عملك على هاتفك.** تقرير أنشأه سكربت، أو خادم تطوير، أو دفتر ملاحظات، أو API لنموذج محلي، عبر عنوان HTTPS خاص تصل إليه الأجهزة المسموح لها.
- **امنح شخصًا واحدًا تطبيقًا واحدًا لفترة محددة.** دع شريكك يستخدم مكتبة الصور لأسبوع، أو زميلك يجرب النسخة الأولية لثلاثة أيام. ينتهي الوصول تلقائيًا، ويمكنك إنهاؤه مبكرًا.
- **دع وكيلك يتولى المشاركة.** أنشأ وكيل البرمجة للتو لوحة معلومات. اطلب منه مشاركتها معك ومع زميلك حتى الجمعة. يمكنه أيضًا إخبارك بما هو مشارك حاليًا وسحب المشاركة.

تستمر تطبيقاتك في العمل حيث تعمل أصلًا. يتحكم TSLink في من يصل إلى كل تطبيق، ويحتفظ بقائمة واحدة لما تمت مشاركته، ومع من، وحتى متى.

<a id="quickstart"></a>

## البدء السريع

تحتاج إلى **Go 1.26.6+** وGit وحساب Tailscale مع [تفعيل MagicDNS وHTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). لم تُنشر إصدارات جاهزة بعد، لذا ثبّت من المصدر:

</div>

<div dir="ltr">

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

</div>

<div dir="rtl">

شارك صفحة:

</div>

<div dir="ltr">

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

</div>

<div dir="rtl">

في المرة الأولى، يطبع TSLink رابط تسجيل دخول لإلحاق عقدة الخدمة الجديدة؛ وقد تتطلب شبكة tailnet لديك موافقة مسؤول على الجهاز أيضًا. بعد التسجيل، افتح عنوان الخدمة على جهاز مسموح له بالدخول ومسجّل في شبكة tailnet لديك. لا تحتاج إلى رمز API.

تحقق مما هو مشارك، ثم أزل المثال:

</div>

<div dir="ltr">

```bash
tslink status --urls
tslink remove demo
```

</div>

<div dir="rtl">

يمكنك أيضًا مشاركة ما يلي بعد تشغيل خدمته الخلفية:

| المحتوى | الأمر |
|---|---|
| تطبيق ويب محلي | <span dir="ltr">`tslink share 3000`</span> |
| مجلد ملفات | <span dir="ltr">`tslink share ./public --name files`</span> |
| API لنموذج محلي مثل Ollama | <span dir="ltr">`tslink add model --proxy localhost:11434`</span> |
| قاعدة بيانات عبر TCP خاص | <span dir="ltr">`tslink add database --tcp localhost:5432`</span> |
| تطبيق معروف باستضافة ذاتية (Jellyfin وImmich وHome Assistant و13 تطبيقًا آخر) | <span dir="ltr">`tslink apps detect`</span>، ثم <span dir="ltr">`tslink apps share jellyfin --yes`</span> |

[البدء والمنصات والخدمة الخلفية →](getting-started.md)

## اختر من يمكنه فتح التطبيق

| الجمهور | ما يحتاجه المستلم | الهوية | انتهاء الوصول |
|---|---|---|---|
| **أجهزتك الخاصة** | تسجيل الدخول إلى شبكة tailnet لديك | هوية Tailscale موثّقة | عند إزالة التطبيق |
| **أشخاص محددون** (HTTP وملفات خاصة) | حساب Tailscale؛ يقبل الأشخاص خارج الشبكة دعوة لكل تطبيق | هوية Tailscale موثّقة | في الموعد الذي تحدده (<span dir="ltr">`--for 7d`</span>) أو باستخدام <span dir="ltr">`tslink people remove`</span> |
| **أي شخص يحمل العنوان** (Funnel) | متصفح | أي شخص؛ يبقى تسجيل دخول التطبيق نفسه ساريًا | بعد 24 ساعة افتراضيًا (<span dir="ltr">`--funnel-ttl`</span>) |
| **رابط ضيف للمتصفح** *(قريبًا)* | متصفح وPIN اختياري | من يحمل الرابط | عند انتهاء مدته أو إلغائه |

</div>

<div dir="ltr">

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

</div>

<div dir="rtl">

تُفحص المواعيد النهائية في كل طلب لمشاركات HTTP والملفات الخاصة. سحب الوصول يوقف الطلبات الجديدة؛ ولا يسترجع البيانات التي تم تنزيلها ولا يغلق التدفقات واتصالات WebSocket المقبولة مسبقًا. [المشاركة مع أشخاص →](people.md) · [حدود المشاركة →](sharing.md)

<a id="agents"></a>

## للوكلاء

يتضمن TSLink خادم MCP ليتمكن الوكيل من المشاركة والسرد والشرح وإزالة المشاركات كما تفعل أنت. أضفه إلى عميل MCP محلي:

</div>

<div dir="ltr">

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

</div>

<div dir="rtl">

- **نتائج دقيقة.** تدعم أتمتة CLI الخيار <span dir="ltr">`--json`</span> مع <span dir="ltr">`schema_version: 1`</span> ورموز أخطاء ثابتة؛ ويستخدم <span dir="ltr">`tslink mcp`</span> بروتوكول JSON-RPC بدلًا منه. يصف <span dir="ltr">`tslink manifest`</span> كل أمر وخيار. على الوكلاء جلب العناوين الفعلية باستخدام <span dir="ltr">`tslink url <name> --wait`</span> بدلًا من تركيبها.
- **حالات انتظار صريحة.** تعلن العقدة الجديدة التي لا تزال بحاجة إلى تسجيل دخول بشري عن <span dir="ltr">`needs_login`</span> بدلًا من الادعاء بأنها جاهزة.
- **الصلاحيات.** يعمل MCP المحلي بصلاحيات مستخدمك. أما MCP البعيد فهو نقطة اتصال تُفعّل اختياريًا، متاحة داخل tailnet فقط، ومقصورة على هويات الدخول أو الوسوم التي تحددها. أدوار كل وكيل ونطاقات التطبيقات وإيصالات الإجراءات ستأتي *قريبًا*.

يتحكم MCP الخاص بـ TSLink في TSLink نفسه. إذا نشرت خادم MCP آخر عبر TSLink، فسيظل بحاجة إلى صلاحيات أدوات خاصة به.
[دليل الوكلاء →](agents.md) · [عملاء MCP →](mcp-clients.md) · [MCP البعيد →](remote-mcp.md) · [أتمتة JSON →](json-automation.md)

## متى تستخدم أداة أخرى

| إذا أردت | فكّر في |
|---|---|
| خدمة محلية واحدة على أجهزتك باستخدام عميل Tailscale الذي تشغله بالفعل | [<span dir="ltr">`tailscale serve`</span>](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| خدمات يديرها مسؤول بأسماء ثابتة عبر مضيفين متعددين | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| عنوانًا عامًا لـ webhook أو تجربة API دون حساب Tailscale | [ngrok](https://ngrok.com/docs/start) أو [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| تثبيت وتشغيل تطبيقات باستضافة ذاتية إلى جانب مشاركتها | [Umbrel](https://umbrel.com) أو [Coolify](https://coolify.io) |
| منصة وصول تعتمد على الهوية للمؤسسة بأكملها | [Pangolin](https://github.com/fosrl/pangolin) أو [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

يناسب TSLink شخصًا يشغّل تطبيقات متعددة ويريد وصولًا محدد المدة لكل تطبيق ولكل شخص، يمكنه هو ووكيله فحصه.

## كيف يعمل

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App وDocs وDatabase وModel عُقد منفصلة ذات أسماء في شبكة tailnet واحدة، يشغّلها برنامج خلفي واحد من TSLink على الحاسوب الذي ينشر الخدمات." width="720">
</picture>

يشغّل برنامج خلفي واحد عقدة Tailscale مضمّنة لكل تطبيق، فيحصل كل تطبيق على اسمه وعنوانه. في مشاركات HTTP والملفات الخاصة، تتحكم <span dir="ltr">`WhoIs`</span> ومنح الوصول للأشخاص أو قواعد <span dir="ltr">`--allow`</span> في الوصول، وتُفحص مواعيد الأشخاص في كل طلب. يستخدم TCP الخام سياسة tailnet ومصادقة الخدمة الخلفية. يوفر Tailscale نقل tailnet والتشفير والشهادات؛ TSLink مشروع مستقل. تشترك جميع التطبيقات في حاسوب النشر، لذلك لا يعزلها TSLink عن بعضها. [البنية →](architecture.md)

## الحالة

متاح الآن: عناوين خاصة لكل تطبيق، أشخاص بمواعيد انتهاء وحزم دعوات، Funnel عام مع انتهاء الصلاحية، فحوص صحة التطبيقات والتنبيهات، وصفات لتطبيقات الاستضافة الذاتية، حدود طلبات لكل تطبيق، إعادة تشغيل بعد التعطل في Windows، وCLI وMCP. ومتاح أيضًا: روابط ضيوف للمتصفح، مدد مرنة، سجل وصول، صفحة رئيسية للتطبيقات، أدوار وكلاء محددة النطاق، انضمام عبر QR وطلبات وصول.

عرض عدة حواسيب في قائمة واحدة مخطط له. [خارطة الطريق →](roadmap.md)

## الوثائق والترخيص

[llms.txt](../llms.txt) · [دليل البدء السريع للوكلاء](agent-quickstart.md) · [اختيار أداة للمشاركة](comparison.md)

[البدء](getting-started.md) · [مرجع CLI](cli-reference.md) · [المنصات](platforms.md) · [النماذج المحلية](local-ai.md) · [المساهمة](../CONTRIBUTING.md) · [الأمان](../SECURITY.md)

Apache License 2.0، بما في ذلك الاستخدام التجاري. احتفظ بملف [NOTICE](../NOTICE) و[إشعارات الجهات الخارجية](../THIRD_PARTY_NOTICES.md) عند إعادة التوزيع. تُطبّق شروط Tailscale وخططه بصورة منفصلة.

</div>
