<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>নিজের হোস্ট করা অ্যাপ শেয়ার করুন আপনার বেছে নেওয়া মানুষের সঙ্গে, যতদিন আপনি চান।</strong></p>

TSLink আপনার কম্পিউটার বা সার্ভারের প্রতিটি অ্যাপকে নিজস্ব ব্যক্তিগত Tailscale ঠিকানা দেয়। নির্দিষ্ট মানুষকে একটি সময়সীমা পর্যন্ত অ্যাক্সেস দিন, Tailscale ব্যবহার করেন না এমন কাউকে ব্রাউজারের অতিথি লিংক পাঠান, আর যেকোনোটি একটি কমান্ডেই বাতিল করুন। নিজে করুন, অথবা আপনার দেওয়া ভূমিকার মধ্যে সীমিত কোনো AI এজেন্টকে দিয়ে করান। এটি Tailscale-এর সঙ্গে কাজ করা স্বাধীন প্রকল্প।

<p align="center"><a href="#quickstart">দ্রুত শুরু</a> · <a href="#agents">এজেন্টদের জন্য</a> · <a href="comparison.md">Serve, ngrok ও Cloudflare-এর সঙ্গে তুলনা</a> · <a href="#documentation">নথি</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <strong>বাংলা</strong> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## আপনার অ্যাপ হাতের নাগালে

| আপনার প্রয়োজন | TSLink যা দেয় |
|---|---|
| বিভিন্ন ডিভাইসে নিজের অ্যাপ ব্যবহার | পিসি বা সার্ভারের হোম ড্যাশবোর্ড, শুধু লোকালে চলা ওয়েব পৃষ্ঠা, ফাইল, মডেল API ও TCP সেবার ব্যক্তিগত ঠিকানা। |
| নির্দিষ্ট মানুষের সঙ্গে শেয়ার | নির্বাচিত HTTP/ফাইল অ্যাপ, যাচাই করা Tailscale পরিচয়, মেয়াদ ও অনুমতি প্রত্যাহার। প্রাপকের Tailscale দরকার। [মানুষের অ্যাক্সেস](people.md) |
| ব্রাউজারে অতিথিকে ঢুকতে দেওয়া | HTTP proxy অ্যাপের জন্য মেয়াদযুক্ত লিংক ও ঐচ্ছিক PIN, অথবা স্পষ্টভাবে চালু করা Funnel পাবলিক HTTPS। লিংক অন্যকে পাঠানো যায়; এটি পরিচয় যাচাই করে না। [অতিথি লিংক](guest-links.md) |
| একসঙ্গে অনেক অ্যাপ দেখাশোনা | প্রতি হোস্টে তালিকা, ব্যক্তিগত পোর্টাল, স্বাস্থ্য পরীক্ষা ও সতর্কতা, অ্যাক্সেস ইতিহাস এবং এজেন্টের ভূমিকা, অ্যাপের সীমা ও নিরীক্ষার রেকর্ডসহ CLI/MCP ব্যবস্থাপনা। [পোর্টাল](portal.md) · [MCP অনুমতি](mcp-scopes.md) |

[অ্যাপ সেটআপ রেসিপি](apps.md), [আপলোডের সীমা](sharing.md), [নমনীয় মেয়াদ](durations.md) ও [QR নির্দেশনা এবং অ্যাক্সেসের অনুরোধ](requests.md) দৈনন্দিন রক্ষণাবেক্ষণ সহজ করে। এগুলো v0.1.0-এ রয়েছে।

<a id="installation"></a>
<a id="quickstart"></a>

## দ্রুত শুরু

macOS ও Linux-এ Homebrew দিয়ে ইনস্টল করুন। macOS-এর বাইনারি Developer ID সার্টিফিকেট দিয়ে স্বাক্ষরিত এবং Apple দ্বারা নোটারাইজ করা। পরে আপগ্রেড করতে `brew upgrade --cask tslink` চালান, তারপর TSLink ব্যাকগ্রাউন্ড সার্ভিস হিসেবে চললে আবার `tslink install` চালান।

```bash
brew install --cask anydoor7/tap/tslink
```

Windows-এ [সর্বশেষ রিলিজ](https://github.com/anydoor7/tslink/releases/latest) থেকে `tslink_<version>_windows_<arch>.zip` ডাউনলোড করুন, `checksums.txt` দিয়ে মিলিয়ে নিন, তারপর `tslink install` চালান যাতে সাইন ইন করলে TSLink চালু হয়। zip ফাইলটি Authenticode-স্বাক্ষরিত নয়; স্বাক্ষরিত চেকসাম ও অ্যাটেস্টেশন দিয়ে [রিলিজ যাচাই করুন](verify-release.md)। Linux-এর `.deb` ও `.rpm` প্যাকেজ একই রিলিজ পাতায় আছে। সোর্স থেকে বিল্ড করতে **Git ও Go 1.26.6+** লাগবে। নিচের কমান্ডগুলো bash/zsh-এর জন্য। [macOS, Linux ও Windows সেটআপ](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

একটি **Tailscale অ্যাকাউন্ট** এবং [MagicDNS ও HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) দরকার। ব্যক্তিগতভাবে যুক্ত ডিভাইসে Tailscale ও নেটওয়ার্ক নীতির অনুমতি থাকতে হবে। অ্যাপের হোস্টে TSLink-এর মধ্যেই Tailscale রয়েছে।

অ্যাপটি আগে থেকেই 3000 পোর্টে চললে:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

অব্যবহৃত নাম নিন; `share` অন্য নাম ফেরত দিলে `url`-এ সেটিই ব্যবহার করুন। আগে নির্দেশিত ব্রাউজার নিবন্ধন ও ডিভাইস অনুমোদন শেষ করুন, তারপর অনুমোদিত ডিভাইসে অ্যাপের সঠিক URL খুলুন। প্রয়োজনে `share` পটভূমির সেবা চালু করে। এই প্রথম ব্যক্তিগত ব্যবহারে প্রশাসকের API টোকেন লাগে না। `tslink share ./report.html` দিয়ে ফাইল শেয়ার করুন; ফাইল থাকতে হবে এবং অ্যাপ চলতে হবে। [সম্পূর্ণ সেটআপ](getting-started.md)

সফলভাবে ব্যবহার করে উপকার পেলে [TSLink-কে একটি স্টার দিতে পারেন](https://github.com/anydoor7/tslink), যাতে অন্যরা খুঁজে পায়। এটি সম্পূর্ণ ঐচ্ছিক।

<a id="architecture"></a>

## যেভাবে কাজ করে

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="একটি পিসি বা ক্লাউড হোস্ট: CLI/MCP একটি যৌথ ডেমন ও অ্যাপভিত্তিক নোড পরিচালনা করে। ব্যক্তিগত ডিভাইস এনক্রিপ্ট করা Tailscale ব্যবহার করে; ঐচ্ছিক পাবলিক HTTPS/Funnel অতিথি যাচাই বা স্পষ্ট উন্মুক্ত প্রকাশের মাধ্যমে HTTP অ্যাপে পৌঁছায়।" width="960">
</picture>

অ্যাপের দিকে একটি ব্যক্তিগত, এনক্রিপ্ট করা পথ ভাবুন। **Tailscale নেটওয়ার্ক পরিবহন ও HTTPS দেয়; TSLink প্রতি হোস্টে অ্যাপের অ্যাক্সেস পরিচালনা করে।** একটি ডেমন প্রতি সেবায় আলাদা এমবেড করা নোড চালায়। ব্যক্তিগত পোর্টালে অনুমোদিত অ্যাপ দেখা যায়; স্বাস্থ্য ও অ্যাক্সেস ইতিহাস রক্ষণাবেক্ষণে সাহায্য করে।

পাবলিক অ্যাক্সেস নিজে চালু করতে হয়: অতিথির লিংক ও সেট করা থাকলে PIN দরকার; খোলা Funnel-এ URL থাকা যে কেউ পৌঁছাতে পারে। দুটিই পাবলিক HTTPS ব্যবহার করে, ব্যক্তিগত ব্যবহারকারীর পরিচয় নয়। সরাসরি TCP ব্যক্তিগত থাকে এবং tailnet নীতি ও ব্যাকএন্ডের পরিচয় যাচাইয়ের ওপর নির্ভর করে। TSLink অ্যাপ ইনস্টল, প্রক্রিয়া বিচ্ছিন্ন, ক্লাউড VPC তৈরি বা একাধিক হোস্ট একত্র করে না। এটি Tailscale-এর সঙ্গে কাজ করা স্বাধীন প্রকল্প। [স্থাপত্য ও সীমা](architecture.md)

<a id="agents"></a>

## এজেন্টদের জন্য

CLI/MCP দিয়ে তালিকা, স্বাস্থ্য, URL ও অনুমতি পরিচালনা করুন। [এজেন্ট নির্দেশিকা](agent-quickstart.md) পড়ুন, বর্তমান টুলের স্কিমা দেখুন এবং সাফল্য জানানোর আগে বাস্তবে অ্যাপ খোলে কি না যাচাই করুন।

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI স্বয়ংক্রিয়তায় `--json`; MCP-তে stdio দিয়ে JSON-RPC ব্যবহৃত হয়। [ক্লায়েন্ট](mcp-clients.md) · [দূরবর্তী MCP](remote-mcp.md) · [ভূমিকা ও সীমা](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## নথি ও লাইসেন্স

[সব নির্দেশিকা](INDEX.md) · [CLI রেফারেন্স](cli-reference.md) · [লোকাল AI](local-ai.md) · [স্বাস্থ্য](health-and-alerts.md) · [অ্যাক্সেস ইতিহাস](access-log.md) · [পরিকল্পনা](roadmap.md)

একাধিক হোস্টের যৌথ তালিকা পরিকল্পনায় আছে। [অবদান](../CONTRIBUTING.md) ও [নিরাপত্তা প্রতিবেদন](../SECURITY.md) স্বাগত। [Apache 2.0](../LICENSE) বাণিজ্যিক ব্যবহার অনুমোদন করে; পুনর্বিতরণে [NOTICE](../NOTICE) ও [তৃতীয় পক্ষের বিজ্ঞপ্তি](../THIRD_PARTY_NOTICES.md) রাখুন। [বাণিজ্যিক সহযোগিতা](../COMMERCIAL.md) স্বেচ্ছামূলক। Tailscale-এর শর্ত ও পরিকল্পনা আলাদাভাবে প্রযোজ্য।
