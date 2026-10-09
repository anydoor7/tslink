<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>আপনার অ্যাপের জন্য ব্যক্তিগত ঠিকানা, আপনার Tailscale নেটওয়ার্কে।</strong></p>
<p align="center">নিজের ডিভাইস থেকে খুলুন। আপনার বেছে নেওয়া তারিখ পর্যন্ত কোনো একজনের সঙ্গে বা লিংকের মাধ্যমে শেয়ার করুন।</p>
<p align="center"><strong>বাংলা</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">আরও ভাষা</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## ইনস্টল

macOS হোস্টের জন্য macOS 13 Ventura বা পরবর্তী সংস্করণ প্রয়োজন ([সমর্থিত প্ল্যাটফর্ম](platforms.md))।

```sh
brew install --cask anydoor7/tap/tslink
```

Windows-এ Scoop দিয়ে ইনস্টল করুন:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

আপগ্রেড করতে `brew upgrade --cask tslink` বা `scoop update; scoop update tslink` চালান। TSLink ব্যাকগ্রাউন্ড সেবা হিসেবে চললে আপগ্রেডের পরে আবার `tslink install` চালান।

Linux-এর `.deb` ও `.rpm` প্যাকেজ এবং Windows বিল্ড [সর্বশেষ রিলিজে](https://github.com/anydoor7/tslink/releases/latest) আছে। প্রথমবার কোনো অ্যাপ শেয়ার করলে TSLink সেটির জন্য একটি Tailscale সাইন-ইন লিংক দেখায়। [শুরু করা](getting-started.md)

<a id="why"></a>

## কখন TSLink দরকার

নিজের ডিভাইসে একটি অ্যাপ খোলার জন্য Serve-ই যথেষ্ট। TSLink অ্যাপের ঠিকানা, মেয়াদ ও অ্যাক্সেস পরিবর্তন এক কাজের ধারায় রাখে।

| কাজ | শুধু Tailscale | TSLink |
|---|---|---|
| ফোনে একটি ওয়েব অ্যাপ | `tailscale serve 3000`-ই যথেষ্ট | `tslink share 3000` |
| একাধিক অ্যাপ, প্রতিটির আলাদা নাম | Services সেটআপ, অথবা আলাদা নোড | প্রতি অ্যাপে একবার `share`/`add`; প্রতিটি নোড নিবন্ধন করুন |
| একজন মানুষ, একটি অ্যাপ, সাত দিন | নীতির নিয়ম, তারপর JIT টুল বা হাতে সরানো | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/ফাইল) |
| ব্রাউজার লিংক, তিন দিন | প্রকাশ্য Funnel; প্রবেশ নিয়ন্ত্রণ ও নির্ধারিত বন্ধ নিজে যোগ করুন | `tslink guest create photos --for 3d --public --print-link` (শুধু HTTP) |

ব্যক্তিগতভাবে যাদের সঙ্গে শেয়ার করবেন, তাদের Tailscale লাগবে। অতিথি লিংক প্রকাশ্য ও অন্যকে পাঠানো যায়; এগুলো অ্যাক্সেসের চাবি হিসেবে কাজ করে।

[পূর্ণ তুলনা](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## আপনার অ্যাপ, আপনার নিজের ডিভাইসে

- **প্রতিটি অ্যাপের জন্য একটি ঠিকানা।** ওয়েব অ্যাপ, ফোল্ডার, একক ফাইল আর TCP পোর্ট আপনার tailnet-এ নিজস্ব নাম পায়, তাই IP ঠিকানার বদলে নাম ব্যবহার করেন।
- **ডিফল্টভাবে ব্যক্তিগত।** অতিথি লিংক তৈরি না করা বা Funnel দিয়ে প্রকাশ না করা পর্যন্ত কিছুই প্রকাশ্য হয় না।
- **পুরো মেশিন নয়, শুধু একটি অ্যাপ।** আপনি যে অ্যাপ প্রকাশ করেন, তার প্রতিটি নিজস্ব নোড পায়, যা শুধু সেই অ্যাপেই ফরোয়ার্ড করে। হোস্টে Tailscale অ্যাপ না থাকলে TSLink আপনার tailnet-এ হোস্টের আর কোনো পোর্ট যোগ করে না।
- **একটি হোম পেজ**, যেখানে আপনার অ্যাপগুলোর তালিকা ও বর্তমান অবস্থা দেখা যায়। [পোর্টাল](portal.md)
- **স্বাস্থ্য পরীক্ষা ও সতর্কতা** কমান্ড বা webhook দিয়ে, আর একটি অ্যাক্সেস লগ যাতে প্রত্যাখ্যাত অনুরোধও থাকে। [স্বাস্থ্য ও সতর্কতা](health-and-alerts.md) · [অ্যাক্সেস ইতিহাস](access-log.md)
- **15টি সেলফ-হোস্টেড অ্যাপের রেসিপি**, যার মধ্যে Home Assistant, Jellyfin, Immich ও Ollama আছে। `tslink apps detect` আগে থেকে চলা অ্যাপগুলো খুঁজে বের করে। [অ্যাপ সেটআপ রেসিপি](apps.md)

## যখন চান তখন শেয়ার করুন

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

অতিথি লিংক ও নতুন প্রকাশ্য URL-এর মেয়াদ শেষ হয় এবং এগুলো শুধু ওয়েব অ্যাপে কাজ করে; ফোল্ডার, ফাইল ও TCP পোর্ট ব্যক্তিগত থাকে। [মানুষের অ্যাক্সেস](people.md) · [অতিথি লিংক](guest-links.md) · [প্রকাশ্য অ্যাক্সেস](funnel.md)

<a id="agents"></a>

## AI এজেন্টদের জন্য

কোনো এজেন্ট `localhost`-এ যে ডেভ সার্ভার চালু করে, সেটি আপনার ফোন থেকে পাওয়া যায় না। TSLink দিয়ে এজেন্ট সেটিকে একটি ব্যক্তিগত ঠিকানা দিতে, সঠিক URL জানাতে এবং কাজ শেষে সরিয়ে ফেলতে পারে।

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI বা MCP।** ম্যানেজমেন্ট কমান্ড `--json` নেয় এবং ভার্সনযুক্ত ফলাফল ফেরত দেয়; `tslink mcp` অ্যাপ ও অ্যাক্সেস সংক্রান্ত কাজ MCP-র মাধ্যমে দেয়।
- **পুরো ফোল্ডার নয়, একটি ফাইল।** এজেন্ট `tslink share ./report.html` দিয়ে শুধু তার HTML রিপোর্ট শেয়ার করতে পারে; ওই ফোল্ডারের অন্য ফাইলগুলোতে পৌঁছানো যায় না।
- **সীমিত ভূমিকা।** `viewer`, `app-operator` বা `people-manager`, আপনার নির্দিষ্ট করা অ্যাপের মধ্যে সীমিত। `tslink mcp-audit` দেখায় এজেন্ট কী বদলেছে। ভূমিকা TSLink-এর টুল সীমিত করে, এজেন্টের নিজের শেল নয়।

[এজেন্ট নির্দেশিকা](agent-quickstart.md) · [MCP অনুমতি](mcp-scopes.md) · [দূরবর্তী MCP](remote-mcp.md)

<a id="architecture"></a>

## যেভাবে কাজ করে

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="একটি পিসি বা ক্লাউড হোস্ট: CLI/MCP একটি যৌথ ডেমন ও অ্যাপভিত্তিক নোড পরিচালনা করে। ব্যক্তিগত ডিভাইস এনক্রিপ্ট করা Tailscale ব্যবহার করে; ঐচ্ছিক পাবলিক HTTPS/Funnel অতিথি যাচাই বা স্পষ্ট উন্মুক্ত প্রকাশের মাধ্যমে HTTP অ্যাপে পৌঁছায়।" width="960">
</picture>

একটি ব্যাকগ্রাউন্ড প্রসেস প্রতিটি অ্যাপের জন্য আলাদা Tailscale নোড চালায়। tailnet-এর ভেতরের ট্রান্সপোর্ট ও HTTPS সার্টিফিকেট দেয় Tailscale। ব্যক্তিগত ওয়েব ও ফাইল অ্যাক্সেস `--allow` ও মানুষের অনুমতি দিয়ে Tailscale পরিচয় অনুযায়ী সীমিত করা যায়; কাঁচা TCP নির্ভর করে আপনার tailnet নীতি ও অ্যাপের নিজস্ব লগইনের ওপর। [স্থাপত্য](architecture.md)

<a id="requirements"></a>

## প্রয়োজনীয়তা

| কে | কী লাগে |
|---|---|
| আপনি | MagicDNS ও HTTPS চালু থাকা একটি Tailscale অ্যাকাউন্ট |
| যে মেশিনে অ্যাপ চলে | TSLink, যার ভেতরে Tailscale আছে (Linux-এ একটি systemd ইউজার সেশনও লাগে) |
| আপনার ডিভাইস ও যাদের সঙ্গে শেয়ার করেন | Tailscale অ্যাপ |
| অতিথি | একটি ব্রাউজার |

HTTPS অ্যাপের নাম প্রকাশ্য সার্টিফিকেট লগে দেখা যায়, তাই এমন নাম বেছে নিন যা অন্যরা দেখলে আপনার আপত্তি নেই।

<a id="documentation"></a>

## আরও

[সব নথি](INDEX.md) · [CLI রেফারেন্স](cli-reference.md) · [Serve, ngrok ও Cloudflare-এর সঙ্গে তুলনা](comparison.md) · [অবদান](../CONTRIBUTING.md) · [নিরাপত্তা](../SECURITY.md)

Apache 2.0। TSLink একটি স্বাধীন প্রকল্প, Tailscale এটি তৈরি বা অনুমোদন করেনি।
