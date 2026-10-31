<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink เป็น CLI และเซิร์ฟเวอร์ MCP แบบโอเพนซอร์สที่ให้ที่อยู่ส่วนตัว <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code> กับแต่ละแอปบนคอมพิวเตอร์ของคุณ เพื่อให้คุณหรือ coding agent ของคุณใช้โทรศัพท์เข้าถึงเซิร์ฟเวอร์สำหรับพัฒนาที่กำลังรันอยู่บนคอมพิวเตอร์ได้ เป็นโปรเจกต์อิสระ ไม่ได้สร้างโดย Tailscale</p>
<p align="center">โทรศัพท์ของคุณต้องมีแอป Tailscale และล็อกอินเข้า tailnet เดียวกัน</p>
<p align="center"><strong>ไทย</strong> · <a href="https://github.com/anydoor7/tslink">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">ภาษาอื่น</a></p>

```text
# Claude Code หลังติดตั้ง CLI แล้ว (ดู "ติดตั้ง" ด้านล่าง)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# ไคลเอนต์ MCP ในเครื่องที่ใช้ JSON แบบ mcpServers (ไคลเอนต์อื่น: ดูวิธีตั้งค่า MCP ของไคลเอนต์นั้น)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

บอกเอเจนต์ของคุณ: "แชร์เซิร์ฟเวอร์สำหรับพัฒนาให้หน่อย ฉันจะเปิดบนโทรศัพท์"
```

ทำสิ่งเดียวกันด้วยมือ โดยเซิร์ฟเวอร์สำหรับพัฒนารอรับการเชื่อมต่อที่พอร์ต 5173 อยู่แล้ว เมื่อใส่ `--json` คำสั่ง `share` จะรายงานชื่อที่ลงทะเบียนไว้ใน `data.name` ให้ใช้ชื่อนี้ในคำสั่งถัดไป ชื่อนี้จะต่างจาก `--name` เมื่อชื่อนั้นมีคนใช้แล้ว เช่น `web-2`

```sh
tslink share 5173 --name web --json   # data.name; data.url หรือ data.auth_url (ลิงก์ล็อกอิน Tailscale) เมื่อ status เป็น needs_login
tslink url <name> --wait              # หลังล็อกอินเสร็จ: URL ที่แน่นอน
tslink remove <name>                  # เมื่อใช้เสร็จ; ข้ามถ้า share ล้มเหลว เซิร์ฟเวอร์สำหรับพัฒนายังรันต่อไป
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

หากต้องการอัปเกรด ให้รัน `brew upgrade --cask tslink` หรือ `scoop update; scoop update tslink` หาก TSLink ทำงานเป็นบริการเบื้องหลัง ให้รัน `tslink install` อีกครั้งหลังอัปเกรด

แพ็กเกจ `.deb` และ `.rpm` สำหรับ Linux และบิลด์สำหรับ Windows อยู่ใน[รุ่นล่าสุด](https://github.com/anydoor7/tslink/releases/latest) ครั้งแรกที่คุณแชร์แอป TSLink จะแสดงลิงก์ลงชื่อเข้าใช้ Tailscale สำหรับแอปนั้น [เริ่มต้นใช้งาน](getting-started.md)

<a id="agents"></a>

## สำหรับเอเจนต์ AI

เซิร์ฟเวอร์สำหรับพัฒนาที่เอเจนต์เปิดบน `localhost` เข้าจากโทรศัพท์ของคุณไม่ได้ TSLink ให้เอเจนต์กำหนดที่อยู่ส่วนตัวให้เซิร์ฟเวอร์นั้น ส่ง URL ที่แน่นอนให้คุณ และเลิกแชร์เมื่อเสร็จ โทรศัพท์ของคุณต้องมีแอป Tailscale และล็อกอินเข้า tailnet เดียวกัน

ทดสอบเมื่อ 2026-10-22 ด้วย Vite 8.3.5 และ iPhone 15 Pro: เอเจนต์แชร์เซิร์ฟเวอร์สำหรับพัฒนาผ่าน MCP โทรศัพท์เปิด URL ส่วนตัวได้ และหน้าเว็บอัปเดตเมื่อบันทึกโดยไม่ต้องรีเฟรชเอง

สำหรับ Claude Code ให้ติดตั้ง CLI ก่อน แล้วรัน:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

ปลั๊กอินมี[สกิลพรีวิว](../skills/tslink-preview/SKILL.md)และการเชื่อมต่อ MCP ด้านล่าง มาร์เก็ตเพลสอยู่ในรีโพนี้ และดึงปลั๊กอินจาก [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin) สกิลต้นฉบับอยู่ที่นี่ ส่วนปลั๊กอินมีสำเนาที่ CI ตรวจสอบ เอเจนต์อื่นใช้สกิลเดียวกันหรือเชื่อมต่อ MCP โดยตรงได้ การเชื่อมต่อนี้ต้องใช้ CLI ในเครื่อง และใช้ในแชต claude.ai ไม่ได้ [การตั้งค่าและการลงทะเบียน](agent-integrations.md)

สำหรับไคลเอนต์ MCP ในเครื่องที่ใช้ JSON แบบ `mcpServers` (ไคลเอนต์อื่น: ดูวิธีตั้งค่า MCP ของไคลเอนต์นั้นและ[คู่มือไคลเอนต์ MCP](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **การติดตั้งใหม่ใช้เกตเวย์เป็นค่าเริ่มต้น** แอป HTTP/ไฟล์ส่วนตัวแต่ละตัวได้พอร์ตคงที่ เช่น `https://home.<tailnet>.ts.net:8443/` การติดตั้งเดิมยังคงโหมดเดิม และแต่ละแอปยังมีโหนด/URL ของตัวเอง ต้องเลือก `--portal-only` อย่างชัดเจน
- **ลงทะเบียนเกตเวย์** เมื่อไม่มีข้อมูลรับรอง บัญชีที่ยืนยันแล้วของเกตเวย์ที่ไม่มีแท็กสามารถเป็น owner ได้ หากใช้แท็ก/OAuth ให้รัน `tslink portal enable --owner <login>` บนเครื่อง การรอลงทะเบียนโหนดแอปไม่ขวางเส้นทางเกตเวย์ที่พร้อมแล้ว
- **ให้สิทธิ์เป็นรายบุคคล** แอปใหม่อนุญาต owner/admin และผู้ที่ได้รับสิทธิ์อย่างชัดเจน `people add`, `add --allow` และ MCP `add/share.allow` ตรวจสอบสมาชิกและเตรียมคำเชิญที่จำเป็น สมาชิกไม่ต้องรับคำเชิญอุปกรณ์ ส่วนผู้ใช้ภายนอกรับคำเชิญเกตเวย์หนึ่งครั้ง ข้อผิดพลาดของไดเรกทอรี/API จะคืนสถานะยังไม่เสร็จสมบูรณ์
- **แก้ไข บันทึก แล้วดูผล** เมื่อบันทึกโค้ด หน้าเว็บบนโทรศัพท์จะอัปเดต กฎ Host และ Origin ของแอปเองยังอาจบล็อกคำขอได้: ในการทดสอบของเรา Vite ไม่ต้องแก้ `allowedHosts` ส่วน Next.js ยังไม่ได้ทดสอบ และยังต้องใช้ `allowedDevOrigins`
- **เก็บกวาด** เอเจนต์เรียก `unshare` หรือคุณรัน `tslink remove <name>` โดยใช้ชื่อที่ผลลัพธ์ของ share ส่งกลับมาเสมอ การลบจะยกเลิกการลงทะเบียนการแชร์ และพยายามลบโหนด tailnet ของมันเมื่อ TSLink พิสูจน์ได้ว่าเป็นเจ้าของ จึงควรตรวจฟิลด์การล้างหรือสถานะที่คงไว้ในผลลัพธ์ คำสั่งนี้ไม่ได้หยุดเซิร์ฟเวอร์สำหรับพัฒนา
- **CLI หรือ MCP** คำสั่งจัดการรับ `--json` และคืนผลลัพธ์ที่มีเวอร์ชันกำกับ ส่วน `tslink mcp` ให้การจัดการแอปและสิทธิ์เข้าถึงผ่าน MCP
- **ไฟล์เดียว ไม่ใช่ทั้งโฟลเดอร์** เอเจนต์แชร์เฉพาะรายงาน HTML ที่สมบูรณ์ในตัวเองด้วย `tslink share ./report.html` ส่วนไฟล์อื่นในโฟลเดอร์นั้นยังเข้าถึงไม่ได้
- **บทบาทที่จำกัด** `viewer`, `app-operator` หรือ `people-manager` เฉพาะแอปที่คุณระบุ การแชร์แอปใหม่ต้องใช้บทบาท `owner` คำสั่ง `tslink mcp-audit` แสดงว่าเอเจนต์เปลี่ยนอะไรไปบ้าง บทบาทจำกัดเครื่องมือของ TSLink เท่านั้น ไม่ได้จำกัดเชลล์ของเอเจนต์เอง

[คู่มือเอเจนต์](agent-quickstart.md) · [สิทธิ์ MCP](mcp-scopes.md) · [MCP ระยะไกล](remote-mcp.md)

<a id="why"></a>

## เมื่อไหร่ที่ต้องใช้ TSLink

ถ้าเป็นแอปเดียวที่คุณเปิดเองและดูบนโทรศัพท์ของตัวเอง Serve ก็พอ TSLink มีไว้สำหรับเซิร์ฟเวอร์สำหรับพัฒนาที่เอเจนต์เปิด ซึ่งมักมีหลายตัว เมื่อแต่ละตัวควรมีชื่อส่วนตัวของตัวเอง และขั้นตอนแชร์ ขอ URL และเก็บกวาดมีให้ใช้ทันทีใน CLI, MCP และสกิลของ Claude Code

| งาน | ใช้ Tailscale อย่างเดียว | TSLink |
|---|---|---|
| เซิร์ฟเวอร์สำหรับพัฒนาหลายตัวที่เอเจนต์เปิด ตัวละหนึ่งชื่อ | Serve ตั้งเส้นทางระดับอุปกรณ์ภายใต้หนึ่งชื่อต่อเครื่อง ส่วน Services ต้องมีคำนิยาม การอนุมัติ และโฮสต์ที่ติดแท็กสำหรับแต่ละชื่อ | หนึ่งโหนดต่อแอป โดยการแชร์ การขอ URL และการเก็บกวาดมีให้ใช้ทันทีใน CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) และสกิล |
| แอปเดียวที่คุณเปิดเอง บนโทรศัพท์ | `tailscale serve 3000` ก็พอ | `tslink share 3000` |
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
