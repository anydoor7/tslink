<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>ที่อยู่ส่วนตัวสำหรับแอปของคุณ บนเครือข่าย Tailscale ของคุณ</strong></p>
<p align="center">เปิดจากอุปกรณ์ของคุณเอง แชร์ให้คนหนึ่งคนหรือผ่านลิงก์ได้ จนถึงวันที่คุณเลือก</p>
<p align="center"><strong>ไทย</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">ภาษาอื่น</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## ติดตั้ง

เครื่อง macOS ต้องใช้ macOS 13 Ventura หรือใหม่กว่า ([แพลตฟอร์มที่รองรับ](platforms.md))

```sh
brew install --cask anydoor7/tap/tslink
```

บน Windows ให้ติดตั้งด้วย Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

หากต้องการอัปเกรด ให้รัน `brew upgrade --cask tslink` หรือ `scoop update tslink` หาก TSLink ทำงานเป็นบริการเบื้องหลัง ให้รัน `tslink install` อีกครั้งหลังอัปเกรด

แพ็กเกจ `.deb` และ `.rpm` สำหรับ Linux และบิลด์สำหรับ Windows อยู่ใน[รุ่นล่าสุด](https://github.com/anydoor7/tslink/releases/latest) ครั้งแรกที่คุณแชร์แอป TSLink จะแสดงลิงก์ลงชื่อเข้าใช้ Tailscale สำหรับแอปนั้น [เริ่มต้นใช้งาน](getting-started.md)

<a id="why"></a>

## เมื่อไหร่ที่ต้องใช้ TSLink

ถ้าเปิดแอปเดียวบนอุปกรณ์ของคุณเอง Serve ก็พอ TSLink รวมที่อยู่ของแอป กำหนดเวลา และการเปลี่ยนสิทธิ์เข้าถึงไว้ในกระบวนการเดียว

| งาน | ใช้ Tailscale อย่างเดียว | TSLink |
|---|---|---|
| เว็บแอปหนึ่งตัวบนมือถือ | `tailscale serve 3000` ก็พอ | `tslink share 3000` |
| หลายแอป แต่ละแอปมีชื่อของตัวเอง | ตั้งค่า Services หรือแยกโหนด | `share`/`add` หนึ่งครั้งต่อแอป และลงทะเบียนทีละโหนด |
| หนึ่งคน หนึ่งแอป เจ็ดวัน | เขียนกฎนโยบาย แล้วใช้เครื่องมือ JIT หรือลบเอง | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/ไฟล์) |
| ลิงก์สำหรับเบราว์เซอร์ สามวัน | Funnel แบบสาธารณะ ต้องเพิ่มด่านตรวจและตั้งเวลาปิดเอง | `tslink guest create photos --for 3d --public --print-link` (HTTP เท่านั้น) |

ผู้รับแบบส่วนตัวต้องมี Tailscale ลิงก์ผู้เยี่ยมชมเป็นลิงก์สาธารณะ ส่งต่อได้ และใช้เป็นกุญแจสำหรับเข้าถึง

[การเปรียบเทียบฉบับเต็ม](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## แอปของคุณ บนอุปกรณ์ของคุณเอง

- **ที่อยู่สำหรับแต่ละแอป** เว็บแอป โฟลเดอร์ ไฟล์เดี่ยว และพอร์ต TCP ต่างได้ชื่อของตัวเองใน tailnet ของคุณ คุณจึงใช้ชื่อแทนที่อยู่ IP
- **เป็นส่วนตัวโดยค่าเริ่มต้น** ไม่มีอะไรเป็นสาธารณะจนกว่าคุณจะสร้างลิงก์ผู้เยี่ยมชมหรือเผยแพร่ผ่าน Funnel
- **เฉพาะแอป ไม่ใช่ทั้งเครื่อง** แอปแต่ละตัวที่คุณเผยแพร่มีโหนดของตัวเอง ซึ่งส่งต่อไปยังแอปนั้นเท่านั้น ถ้าบนโฮสต์ไม่มีแอป Tailscale TSLink จะไม่เพิ่มพอร์ตอื่นของโฮสต์เข้าไปใน tailnet ของคุณ
- **หน้าแรก**ที่แสดงรายการแอปพร้อมสถานะการทำงาน [พอร์ทัล](portal.md)
- **การตรวจสอบสถานะและการแจ้งเตือน**ผ่านคำสั่งหรือ webhook และบันทึกการเข้าถึงที่รวมคำขอที่ถูกปฏิเสธด้วย [สุขภาพและการแจ้งเตือน](health-and-alerts.md) · [ประวัติการเข้าถึง](access-log.md)
- **สูตรสำหรับแอปที่โฮสต์เอง 15 ตัว** รวมถึง Home Assistant, Jellyfin, Immich และ Ollama คำสั่ง `tslink apps detect` จะหาแอปที่กำลังทำงานอยู่แล้ว [สูตรตั้งค่าแอป](apps.md)

## แชร์เมื่อคุณต้องการ

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

ลิงก์ผู้เยี่ยมชมและ URL สาธารณะที่สร้างใหม่จะหมดอายุ และใช้ได้กับเว็บแอปเท่านั้น ส่วนโฟลเดอร์ ไฟล์ และพอร์ต TCP ยังคงเป็นส่วนตัว [บุคคล](people.md) · [ลิงก์ผู้เยี่ยมชม](guest-links.md) · [การเข้าถึงสาธารณะ](funnel.md)

<a id="agents"></a>

## สำหรับเอเจนต์ AI

เซิร์ฟเวอร์สำหรับพัฒนาที่เอเจนต์เปิดบน `localhost` เข้าจากโทรศัพท์ของคุณไม่ได้ TSLink ให้เอเจนต์กำหนดที่อยู่ส่วนตัวให้เซิร์ฟเวอร์นั้น รายงาน URL ที่แน่นอน และลบออกเมื่อเสร็จ

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI หรือ MCP** คำสั่งจัดการรับ `--json` และคืนผลลัพธ์ที่มีเวอร์ชันกำกับ ส่วน `tslink mcp` ให้การจัดการแอปและสิทธิ์เข้าถึงผ่าน MCP
- **ไฟล์เดียว ไม่ใช่ทั้งโฟลเดอร์** เอเจนต์แชร์เฉพาะรายงาน HTML ของตัวเองได้ด้วย `tslink share ./report.html` ส่วนไฟล์อื่นในโฟลเดอร์นั้นยังเข้าถึงไม่ได้
- **บทบาทที่จำกัด** `viewer`, `app-operator` หรือ `people-manager` เฉพาะแอปที่คุณระบุ คำสั่ง `tslink mcp-audit` แสดงว่าเอเจนต์เปลี่ยนอะไรไปบ้าง บทบาทจำกัดเครื่องมือของ TSLink เท่านั้น ไม่ได้จำกัดเชลล์ของเอเจนต์เอง

[คู่มือเอเจนต์](agent-quickstart.md) · [สิทธิ์ MCP](mcp-scopes.md) · [MCP ระยะไกล](remote-mcp.md)

<a id="architecture"></a>

## ทำงานอย่างไร

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="พีซีหรือโฮสต์คลาวด์หนึ่งเครื่อง: CLI/MCP จัดการเดมอนร่วมและโหนดแยกต่อแอป อุปกรณ์ส่วนตัวเชื่อมต่อผ่าน Tailscale ที่เข้ารหัส ส่วน HTTPS/Funnel สาธารณะที่เลือกเปิดจะเข้าถึงแอป HTTP ผ่านการตรวจสอบผู้เยี่ยมชมหรือการเผยแพร่แบบเปิดอย่างชัดเจน" width="960">
</picture>

โปรเซสเบื้องหลังหนึ่งตัวรันโหนด Tailscale แยกกันสำหรับแต่ละแอป Tailscale ให้การรับส่งข้อมูลใน tailnet และใบรับรอง HTTPS การเข้าถึงเว็บและไฟล์แบบส่วนตัวจำกัดตามตัวตน Tailscale ได้ด้วย `--allow` และการให้สิทธิ์บุคคล ส่วน TCP ดิบอาศัยนโยบาย tailnet ของคุณและการล็อกอินของแอปเอง [สถาปัตยกรรม](architecture.md)

<a id="requirements"></a>

## ข้อกำหนด

| ใคร | ต้องมี |
|---|---|
| คุณ | บัญชี Tailscale ที่เปิด MagicDNS และ HTTPS |
| เครื่องที่รันแอปของคุณ | TSLink ซึ่งมี Tailscale ในตัว (บน Linux ต้องมีเซสชันผู้ใช้ของ systemd ด้วย) |
| อุปกรณ์ของคุณ และคนที่คุณแชร์ให้ | แอป Tailscale |
| ผู้เยี่ยมชม | เบราว์เซอร์ |

ชื่อของแอป HTTPS จะปรากฏในบันทึกใบรับรองสาธารณะ จึงควรเลือกชื่อที่คุณไม่ว่าอะไรถ้าคนอื่นเห็น

<a id="documentation"></a>

## เพิ่มเติม

[เอกสารทั้งหมด](INDEX.md) · [อ้างอิง CLI](cli-reference.md) · [เปรียบเทียบกับ Serve, ngrok และ Cloudflare](comparison.md) · [การมีส่วนร่วม](../CONTRIBUTING.md) · [ความปลอดภัย](../SECURITY.md)

Apache 2.0 TSLink เป็นโปรเจกต์อิสระ ไม่ได้สร้างหรือรับรองโดย Tailscale
