<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink লোগো">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>আপনার কম্পিউটারের অ্যাপ, মডেল ও ফাইলকে নিজস্ব ব্যক্তিগত নেটওয়ার্ক ঠিকানা দিন।</strong><br>
  আপনার Tailscale নেটওয়ার্কের অন্য অনুমোদিত ডিভাইস থেকে এগুলো ব্যবহার করুন।
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="লাইসেন্স: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 বা পরবর্তী সংস্করণ"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: অন্তর্নির্মিত tsnet নোড"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: ১৯টি টুল"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <strong>বাংলা</strong> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## ইনস্টলেশন

প্রয়োজন **Go 1.26.6 বা পরবর্তী সংস্করণ** এবং Git। আগে থেকে কম্পাইল করা রিলিজ বা Homebrew cask এখনো প্রকাশিত হয়নি, তাই সোর্স থেকে ইনস্টল করুন। উদাহরণগুলো **bash বা zsh** ব্যবহার করে। Windows ও ব্যাকগ্রাউন্ড সার্ভিসের শর্তের জন্য [প্ল্যাটফর্ম সমর্থন](platforms.md) দেখুন। বিস্তারিত নির্দেশিকা ইংরেজিতে দেওয়া আছে।

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

[MagicDNS ও HTTPS চালু করা](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale অ্যাকাউন্ট ব্যবহার করুন। যে ডিভাইস থেকে সার্ভিসে সংযোগ করবেন, সেটি আপনার Tailscale নেটওয়ার্কে (**tailnet**) লগইন করা থাকতে হবে এবং নেটওয়ার্ক নীতি অনুযায়ী সার্ভিসে প্রবেশের অনুমতি থাকতে হবে। সার্ভিস প্রকাশকারী কম্পিউটারে TSLink-এর মধ্যেই Tailscale অন্তর্ভুক্ত থাকে।

### প্রথম পেজ শেয়ার করুন

একটি পেজ তৈরি করুন; TSLink সরাসরি সেটি পরিবেশন করে এবং প্রয়োজন হলে ব্যাকগ্রাউন্ড সার্ভিস চালু করে:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

TSLink নোড নিবন্ধনের URL দেখালে সেটি খুলে নোডকে অনুমোদন দিন। আপনার tailnet-এ প্রশাসকের ডিভাইস অনুমোদনও প্রয়োজন হতে পারে। এরপর সঠিক ঠিকানা নিন:

```bash
tslink url demo --wait
```

অনুমোদিত ডিভাইসে ফেরত পাওয়া URL খুলুন। প্রথমবার শেয়ার করতে API token লাগে না। [সম্পূর্ণ সেটআপ ও সার্ভিসের জীবনচক্র →](getting-started.md)

<a id="use-cases"></a>

## কী শেয়ার করতে চান?

ফাইল আগে থেকেই থাকতে হবে; অ্যাপ, ডেটাবেস ও মডেলের ব্যাকএন্ড নির্দিষ্ট পোর্টে চালু থাকতে হবে।

| ব্যবহার | কমান্ড |
|---|---|
| অন্য ডিভাইস থেকে কম্পিউটারের অ্যাপ খুলুন | `tslink share 3000` |
| একটি ডিরেক্টরির ফাইল দেখুন | `tslink share ./public --name files` |
| তৈরি করা HTML রিপোর্ট ফোনে পড়ুন | `tslink share ./report.html --name report` |
| TCP দিয়ে কম্পিউটারের ডেটাবেসে সংযোগ করুন | `tslink add database --tcp localhost:5432` |
| Ollama-র মতো কম্পিউটারে চলা মডেলের HTTP API ব্যবহার করুন | `tslink add model --proxy localhost:11434` |

Ollama-র জন্য `tslink url model --wait` দিয়ে সঠিক URL নিন। OpenAI API-সামঞ্জস্যপূর্ণ ক্লায়েন্টের `baseURL` হবে সেই URL-এর শেষে `/v1` যোগ করা ঠিকানা। [লোকাল মডেল ও ব্যক্তিগত তথ্যের কর্মপ্রবাহ →](local-ai.md)

এক হোস্টে একাধিক অ্যাপের জন্য TSLink নামযুক্ত সার্ভিস নোড, HTTP পরিচয় অনুমোদন তালিকা, Funnel মেয়াদ ও MCP ব্যবস্থাপনা একসঙ্গে দেয়। নিজের ডিভাইসে একটি অ্যাপের জন্য [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) যথেষ্ট হতে পারে।

<a id="architecture"></a>

## স্থাপত্য

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="সার্ভিসের উদাহরণচিত্র: App, Docs, Database ও Model একই tailnet-এর আলাদা নামযুক্ত নোড। অ্যাপ, ফাইল ও মডেলের API HTTPS ব্যবহার করে; ডেটাবেস ব্যক্তিগত TCP সংযোগ ব্যবহার করে।" width="960">
</picture>

**একটি tailnet, আলাদা সার্ভিস নোড।** একটি শেয়ার করা ডেমন প্রতিটি সার্ভিসের জন্য অন্তর্নির্মিত tsnet নোড চালায়, HTTP অনুরোধ পাঠায়, ফাইল পরিবেশন করে বা TCP প্রক্সি হিসেবে কাজ করে। চলার সময়ই সার্ভিস রেজিস্ট্রির পরিবর্তন কার্যকর হয়। প্রতিটি নোডের নিজস্ব নেটওয়ার্ক পরিচয় থাকে; সার্ভিসগুলো একই প্রকাশকারী কম্পিউটারে চলে। [স্থাপত্যের বিস্তারিত →](architecture.md)

| উপাদান | ভূমিকা |
|---|---|
| [Go](../go.mod) | নেটিভ কমান্ড-লাইন প্রোগ্রাম |
| [Tailscale tsnet](architecture.md) | সার্ভিস নোড ও tailnet-এ তথ্য পরিবহন |
| [Cobra](https://github.com/spf13/cobra) | কমান্ড ও সহায়তা |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | এজেন্টের যোগাযোগ ব্যবস্থা |
| অপারেটিং সিস্টেমের কিচেইন ও ব্যবহারকারীর সার্ভিস ম্যানেজার | ঐচ্ছিক প্রমাণীকরণ তথ্য সংরক্ষণ ও ব্যাকগ্রাউন্ডে চালানো |

স্পষ্টভাবে [পাবলিক Funnel](getting-started.md#more-examples) চালু না করলে সার্ভিসগুলো tailnet-এর মধ্যেই থাকে। HTTP ও ফাইল সার্ভিস পরিচয়ভিত্তিক অনুমোদিত তালিকা সমর্থন করে (`WhoIs`, `--allow`); TCP নির্ভর করে tailnet নীতি ও ব্যাকএন্ডের নিজস্ব পরিচয় যাচাইয়ের ওপর। [শেয়ার করার সীমা](sharing.md) দেখুন।

TSLink অ্যাপ ইনস্টল, মডেল চালানো, হোস্টের প্রক্রিয়া বিচ্ছিন্ন করা বা একাধিক হোস্ট একত্র করা করে না। নেটওয়ার্ক, এনক্রিপশন ও HTTPS দেয় Tailscale; TSLink একটি স্বতন্ত্র প্রকল্প।

<a id="agents"></a>

## এজেন্টের জন্য

**১৯টি MCP টুল** দিয়ে এজেন্ট রিপোর্ট শেয়ার করতে, সার্ভিস পরিচালনা করতে, URL নিতে ও সেটআপ পরীক্ষা করতে পারে। কম্পিউটারের MCP ক্লায়েন্টকে ইনস্টল করা প্রোগ্রামের সঙ্গে যুক্ত করুন:

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

MCP দিয়ে TSLink পরিচালনা করা হয়; মডেলের অনুমান প্রক্রিয়ার জন্য অ্যাপ HTTP API ব্যবহার করে। কনফিগারেশন ও স্বয়ংক্রিয় ব্যবহারের নির্দেশনা পেতে [MCP ক্লায়েন্ট](mcp-clients.md), [দূরবর্তী MCP](remote-mcp.md) ও [এজেন্ট পরিচালনার নির্দেশিকা](../AGENTS.md) দেখুন।

CLI অটোমেশন `--json` সমর্থন করে, যেখানে `schema_version` হলো `1`; দেখুন `tslink status --urls --json`। স্থানীয় MCP stdio-তে JSON-RPC ব্যবহার করে। দেখুন [JSON অটোমেশন](json-automation.md)।

<a id="roadmap"></a>

## যা আসছে

মার্জ চলছে, পর্যালোচনাধীন বা পরিকল্পিত লেখা বিষয়গুলো উপরের সোর্স ইনস্টলে অন্তর্ভুক্ত নয়।

| ব্যবহার | অবস্থা |
|---|---|
| <!-- roadmap:people --> আত্মীয়কে ব্যক্তিগত HTTP/ফাইল অ্যাপে ৩ দিনের প্রবেশাধিকার দিন এবং অ্যাপের আমন্ত্রণ একটি বার্তায় একত্র করুন; প্রাপকের এখনও Tailscale লাগবে। | মার্জ চলছে |
| <!-- roadmap:health --> অ্যাপের স্বাস্থ্য পরীক্ষা করুন এবং ঐচ্ছিক কমান্ড বা webhook-এ বিভ্রাট বা মেয়াদ শেষের সতর্কতা পান। | মার্জ চলছে |
| <!-- roadmap:recipes --> সমর্থিত loopback অ্যাপ খুঁজুন এবং শেয়ারের আগে নিজে হোস্ট করা অ্যাপের রেসিপি দেখুন। | মার্জ চলছে |
| <!-- roadmap:limits --> বড় আপলোড ও ধীর ক্লায়েন্টের জন্য প্রতিটি HTTP অ্যাপের আপলোডের আকার ও অনুরোধের সময়সীমা ঠিক করুন। | মার্জ চলছে |
| <!-- roadmap:windows --> Windows-এ লগইন থাকা অবস্থায় নির্ধারিত টাস্ক ও অন্তর্নির্মিত সুপারভাইজার দিয়ে ক্র্যাশ করা daemon আবার চালু করুন। | মার্জ চলছে |
| <!-- roadmap:access-log --> স্থানীয় প্রবেশ লগে কে কোন অ্যাপ খুলেছে দেখুন, `prefix`, `full` বা `off` পাথ রেকর্ডিং দিয়ে। | পর্যালোচনাধীন |
| <!-- roadmap:portal --> অনুমোদিত অ্যাপের একটি হোম পেজ খুলুন, মালিকের জন্য নোড নিবন্ধনের হস্তান্তরসহ; দর্শকের এখনও Tailscale লাগবে। | পর্যালোচনাধীন |
| <!-- roadmap:mcp-scopes --> এজেন্টকে ভূমিকা ও অ্যাপের পরিধি দিন, তার পরিবর্তনের অডিট রসিদসহ। | পর্যালোচনাধীন |
| <!-- roadmap:guest-links --> নিয়ন্ত্রিত পাবলিক Funnel দিয়ে অতিথিকে মেয়াদযুক্ত লিংক ও ঐচ্ছিক PIN ব্যবহার করে Tailscale ইনস্টল না করে ব্রাউজারে একটি HTTP অ্যাপ খুলতে দিন। | পর্যালোচনাধীন |
| <!-- roadmap:durations --> প্রিসেট বা নিজস্ব মেয়াদ বাছুন, সর্বনিম্ন ১ ঘণ্টা এবং অতিথির জন্য ডিফল্ট সর্বোচ্চ ৭ দিন, যা বদলানো যায়। | পর্যালোচনাধীন |
| <!-- roadmap:requests --> ফোন ব্যবহারকারীকে QR কোড দিয়ে যোগ দিতে সাহায্য করুন; মালিককে এক ধাপে অ্যাপে প্রবেশাধিকার বা বাড়তি সময়ের অনুরোধ অনুমোদন করতে দিন। | পর্যালোচনাধীন |
| <!-- roadmap:multi-host --> এক তালিকায় একাধিক হোস্টের অ্যাপ দেখুন। | পরিকল্পিত |

<a id="documentation"></a>

## ডকুমেন্টেশন ও লাইসেন্স

[শুরু করুন](getting-started.md) · [লোকাল মডেল](local-ai.md) · [CLI রেফারেন্স](cli-reference.md) · [প্ল্যাটফর্ম](platforms.md) · [রোডম্যাপ](roadmap.md)

অবদান রাখার জন্য [CONTRIBUTING.md](../CONTRIBUTING.md) দেখুন; নিরাপত্তা দুর্বলতা জানাতে [SECURITY.md](../SECURITY.md)-এর নির্দেশিত মাধ্যম ব্যবহার করুন।

TSLink অপরিবর্তিত [Apache License 2.0](../LICENSE) ব্যবহার করে, যার শর্ত মেনে বাণিজ্যিক ব্যবহারও করা যায়। পুনর্বিতরণের সময় প্রযোজ্য [NOTICE](../NOTICE) ও [তৃতীয় পক্ষের বিজ্ঞপ্তি](../THIRD_PARTY_NOTICES.md) রাখুন। [বাণিজ্যিক সহযোগিতা](../COMMERCIAL.md) স্বেচ্ছামূলক এবং লাইসেন্সে কোনো নতুন শর্ত যোগ করে না। Tailscale-এর পরিষেবার শর্ত ও প্যাকেজ আলাদাভাবে প্রযোজ্য।
