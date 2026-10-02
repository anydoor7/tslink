<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink লোগো">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>আপনার কম্পিউটারের অ্যাপ নিজের পছন্দের মানুষের সঙ্গে, নিজের পছন্দের সময়ের জন্য শেয়ার করুন।</strong><br>
  প্রতিটি অ্যাপ আপনার Tailscale নেটওয়ার্কে আলাদা ব্যক্তিগত ঠিকানা পায়। কার প্রবেশাধিকার আছে দেখুন এবং তা ফিরিয়ে নিন।
</p>

<p align="center">
  <a href="#quickstart">দ্রুত শুরু</a> · <a href="#agents">এজেন্টদের জন্য</a> · <a href="getting-started.md">নথি</a> ·
  <strong>বাংলা</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">সব ভাষা</a>
</p>

## মানুষ এটি কী কাজে ব্যবহার করে

- **ফোনে নিজের কাজ খুলুন।** স্ক্রিপ্টের তৈরি প্রতিবেদন, ডেভেলপমেন্ট সার্ভার, নোটবুক বা স্থানীয় মডেলের API, অনুমোদিত ডিভাইস থেকে ব্যক্তিগত HTTPS ঠিকানায় ব্যবহার করুন।
- **একজনকে একটি অ্যাপ কিছু সময়ের জন্য দিন।** আপনার সঙ্গীকে এক সপ্তাহ ছবির লাইব্রেরি ব্যবহার করতে দিন, বা সহকর্মীকে তিন দিন প্রিভিউ পরীক্ষা করতে দিন। প্রবেশাধিকার নিজে থেকেই শেষ হবে; আগেও শেষ করতে পারেন।
- **শেয়ার করার দায়িত্ব এজেন্টকে দিন।** আপনার কোডিং এজেন্ট এইমাত্র একটি ড্যাশবোর্ড বানিয়েছে। সেটি শুক্রবার পর্যন্ত আপনার ও সতীর্থের সঙ্গে শেয়ার করতে বলুন। এখন কী শেয়ার করা আছে তা জানাতেও এবং শেয়ার বন্ধ করতেও পারে।

অ্যাপ যেখানে চলছে সেখানেই চলতে থাকে। TSLink প্রতিটি অ্যাপে কে পৌঁছাতে পারবে তা নিয়ন্ত্রণ করে এবং কী, কার সঙ্গে, কত দিন শেয়ার করা আছে তার একটি তালিকা রাখে।

<a id="quickstart"></a>

## দ্রুত শুরু

আপনার দরকার **Go 1.26.6+**, Git এবং [MagicDNS ও HTTPS চালু করা](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale অ্যাকাউন্ট। আগে থেকে কম্পাইল করা সংস্করণ এখনও প্রকাশিত হয়নি, তাই সোর্স থেকে ইনস্টল করুন:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

একটি পৃষ্ঠা শেয়ার করুন:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

প্রথমবার TSLink নতুন সার্ভিস নোড নিবন্ধনের জন্য সাইন-ইন লিংক দেখায়; আপনার tailnet-এ প্রশাসকের ডিভাইস অনুমোদনও লাগতে পারে। নিবন্ধনের পর আপনার tailnet-এ সাইন-ইন করা অনুমোদিত ডিভাইসে সার্ভিসের URL খুলুন। API টোকেন লাগে না।

কী শেয়ার করা আছে দেখুন, তারপর ডেমো সরিয়ে দিন:

```bash
tslink status --urls
tslink remove demo
```

ব্যাকএন্ড চালু থাকলে এগুলোও শেয়ার করতে পারেন:

| বিষয় | কমান্ড |
|---|---|
| স্থানীয় ওয়েব অ্যাপ | `tslink share 3000` |
| ফাইলের ফোল্ডার | `tslink share ./public --name files` |
| Ollama-র মতো স্থানীয় মডেলের API | `tslink add model --proxy localhost:11434` |
| ব্যক্তিগত TCP দিয়ে ডেটাবেস | `tslink add database --tcp localhost:5432` |
| পরিচিত স্ব-হোস্ট করা অ্যাপ (Jellyfin, Immich, Home Assistant এবং আরও ১৩টি) | `tslink apps detect`, তারপর `tslink apps share jellyfin --yes` |

[শুরু করা, প্ল্যাটফর্ম ও ব্যাকগ্রাউন্ড সার্ভিস →](getting-started.md)

## কে খুলতে পারবে বেছে নিন

| ব্যবহারকারী | প্রাপকের কী দরকার | পরিচয় | শেষ হবে |
|---|---|---|---|
| **নিজের ডিভাইস** | আপনার tailnet-এ সাইন-ইন | যাচাইকৃত Tailscale পরিচয় | অ্যাপ সরালে |
| **নির্দিষ্ট মানুষ** (ব্যক্তিগত HTTP/ফাইল) | Tailscale অ্যাকাউন্ট; বাইরের মানুষ প্রতিটি অ্যাপের জন্য একটি আমন্ত্রণ গ্রহণ করেন | যাচাইকৃত Tailscale পরিচয় | আপনার নির্ধারিত সময়ে (`--for 7d`) বা `tslink people remove` দিয়ে |
| **URL থাকা যে কেউ** (Funnel) | ব্রাউজার | যে কেউ; অ্যাপের নিজস্ব লগইন শর্ত বহাল থাকে | ডিফল্ট হিসেবে ২৪ ঘণ্টা পর (`--funnel-ttl`) |
| **ব্রাউজারের অতিথি লিংক** *(আসছে)* | ব্রাউজার এবং ঐচ্ছিক PIN | যার কাছে লিংক আছে | লিংকের মেয়াদ শেষে বা বাতিল করলে |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

ব্যক্তিগত HTTP ও ফাইল শেয়ারের সময়সীমা প্রতিটি অনুরোধে পরীক্ষা করা হয়। প্রবেশাধিকার বাতিল করলে নতুন অনুরোধ বন্ধ হয়; ডাউনলোড করা তথ্য ফিরিয়ে নেওয়া বা আগে গ্রহণ করা স্ট্রিম ও WebSocket সংযোগ বন্ধ করা যায় না। [মানুষের সঙ্গে শেয়ার →](people.md) · [শেয়ারের সীমা →](sharing.md)

<a id="agents"></a>

## এজেন্টদের জন্য

TSLink-এ MCP সার্ভার আছে, তাই এজেন্ট আপনার মতোই শেয়ার করতে, তালিকা দেখতে, ব্যাখ্যা করতে এবং শেয়ার সরাতে পারে। স্থানীয় MCP ক্লায়েন্টে যোগ করুন:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **নির্ভুল ফলাফল।** CLI অটোমেশন `--json`, `schema_version: 1` এবং স্থায়ী ত্রুটি কোড সমর্থন করে; `tslink mcp` এর বদলে JSON-RPC ব্যবহার করে। `tslink manifest` প্রতিটি কমান্ড ও ফ্ল্যাগ ব্যাখ্যা করে। এজেন্টের উচিত URL বানানোর বদলে `tslink url <name> --wait` দিয়ে আসল URL নেওয়া।
- **অপেক্ষার অবস্থা স্পষ্ট।** নতুন নোডে এখনও মানুষের সাইন-ইন দরকার হলে প্রস্তুত দেখানোর বদলে `needs_login` জানায়।
- **ক্ষমতা।** স্থানীয় MCP আপনার ব্যবহারকারীর অধিকার নিয়ে চলে। দূরবর্তী MCP আলাদাভাবে চালু করতে হয়, কেবল tailnet-এ পৌঁছানো যায় এবং আপনার তালিকার লগইন পরিচয় বা ট্যাগেই সীমিত থাকে। এজেন্টভিত্তিক ভূমিকা, অ্যাপের পরিসর ও কাজের রসিদ *আসছে*।

TSLink-এর MCP দিয়ে TSLink নিজেকেই পরিচালনা করা হয়। TSLink দিয়ে অন্য MCP সার্ভার প্রকাশ করলে সেই সার্ভারের নিজস্ব টুল অনুমতি এখনও দরকার।
[এজেন্ট নির্দেশিকা →](agents.md) · [MCP ক্লায়েন্ট →](mcp-clients.md) · [দূরবর্তী MCP →](remote-mcp.md) · [JSON অটোমেশন →](json-automation.md)

## কখন অন্য টুল ব্যবহার করবেন

| আপনার প্রয়োজন | বিবেচনা করুন |
|---|---|
| আগে থেকেই চালু Tailscale ক্লায়েন্ট দিয়ে নিজের ডিভাইসে একটি স্থানীয় সার্ভিস | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| একাধিক হোস্টে স্থায়ী নামসহ প্রশাসক-পরিচালিত সার্ভিস | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Tailscale অ্যাকাউন্ট ছাড়া webhook বা API ডেমোর প্রকাশ্য URL | [ngrok](https://ngrok.com/docs/start) বা [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| স্ব-হোস্ট করা অ্যাপ শেয়ারের পাশাপাশি ইনস্টল ও চালু করা | [Umbrel](https://umbrel.com) বা [Coolify](https://coolify.io) |
| পুরো প্রতিষ্ঠানের জন্য পরিচয়ভিত্তিক প্রবেশাধিকার প্ল্যাটফর্ম | [Pangolin](https://github.com/fosrl/pangolin) বা [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

একজন মানুষ একাধিক অ্যাপ চালিয়ে অ্যাপ ও ব্যক্তিভেদে সময়সীমাসহ প্রবেশাধিকার দিতে চাইলে, যা তিনি ও তাঁর এজেন্ট দুজনেই পরীক্ষা করতে পারেন, TSLink উপযুক্ত।

## যেভাবে কাজ করে

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database ও Model একই tailnet-এ আলাদা নামের নোড; প্রকাশকারী কম্পিউটারে একটি TSLink ডেমন এগুলো চালায়।" width="720">
</picture>

একটি ব্যাকগ্রাউন্ড ডেমন প্রতিটি অ্যাপের জন্য এমবেড করা Tailscale নোড চালায়, তাই প্রত্যেকের নিজস্ব নাম ও ঠিকানা থাকে। ব্যক্তিগত HTTP ও ফাইল শেয়ারে `WhoIs` এবং ব্যক্তিভিত্তিক অনুমতি বা `--allow` নিয়ম প্রবেশাধিকার নিয়ন্ত্রণ করে; ব্যক্তির সময়সীমা প্রতিটি অনুরোধে পরীক্ষা হয়। সরাসরি TCP-তে tailnet নীতি ও ব্যাকএন্ডের প্রমাণীকরণ ব্যবহৃত হয়। Tailscale দেয় tailnet পরিবহন, এনক্রিপশন ও সার্টিফিকেট; TSLink একটি স্বাধীন প্রকল্প। সব অ্যাপ একই প্রকাশকারী কম্পিউটার ব্যবহার করে, তাই TSLink তাদের পরস্পর থেকে আলাদা করে না। [স্থাপত্য →](architecture.md)

## অবস্থা

এখন পাওয়া যায়: অ্যাপভিত্তিক ব্যক্তিগত ঠিকানা, সময়সীমা ও আমন্ত্রণের গুচ্ছসহ নির্দিষ্ট মানুষকে প্রবেশাধিকার, মেয়াদসহ প্রকাশ্য Funnel, অ্যাপের স্বাস্থ্য পরীক্ষা ও সতর্কতা, স্ব-হোস্ট করা অ্যাপের রেসিপি, অ্যাপভিত্তিক অনুরোধের সীমা, Windows-এ ক্র্যাশের পর পুনরায় চালু হওয়া, CLI ও MCP।

আসছে: ব্রাউজারের অতিথি লিংক, নমনীয় সময়কাল, প্রবেশের লগ, অ্যাপগুলোর হোম পৃষ্ঠা, সীমিত পরিসরের এজেন্ট ভূমিকা, QR দিয়ে যোগদান ও প্রবেশাধিকার অনুরোধ। এক তালিকায় একাধিক কম্পিউটার দেখার পরিকল্পনা আছে। [পরিকল্পনা →](roadmap.md)

## নথি ও লাইসেন্স

[শুরু করা](getting-started.md) · [CLI রেফারেন্স](cli-reference.md) · [প্ল্যাটফর্ম](platforms.md) · [স্থানীয় মডেল](local-ai.md) · [অবদান](../CONTRIBUTING.md) · [নিরাপত্তা](../SECURITY.md)

বাণিজ্যিক ব্যবহারসহ Apache License 2.0 প্রযোজ্য। পুনর্বিতরণে [NOTICE](../NOTICE) ও [তৃতীয় পক্ষের বিজ্ঞপ্তি](../THIRD_PARTY_NOTICES.md) রাখুন। Tailscale-এর শর্ত ও পরিকল্পনা আলাদাভাবে প্রযোজ্য।
