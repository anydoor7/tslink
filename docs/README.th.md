<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>เข้าถึงและจัดการแอปของคุณได้จากทุกที่<br>เก็บไว้เป็นส่วนตัว หรือแชร์ตามเงื่อนไขของคุณ</strong></p>

แอปของคุณบนคอมพิวเตอร์หรือเซิร์ฟเวอร์คลาวด์ของคุณ: เข้าถึงผ่านเครือข่ายส่วนตัวที่เข้ารหัส หรือเลือกใช้ลิงก์ผู้เยี่ยมชมบนเบราว์เซอร์หรือการเข้าถึงสาธารณะ จัดการเองหรือผ่านเอเจนต์ก็ได้

<p align="center"><a href="#quickstart">เริ่มต้นอย่างรวดเร็ว</a> · <a href="#agents">สำหรับเอเจนต์</a> · <a href="#documentation">เอกสาร</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <strong>ไทย</strong> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## แอปของคุณอยู่ใกล้มือเสมอ

| สิ่งที่ต้องการ | สิ่งที่ TSLink มีให้ |
|---|---|
| ใช้แอปของตัวเองข้ามอุปกรณ์ | ที่อยู่ส่วนตัวสำหรับแดชบอร์ดในบ้าน หน้าเว็บที่เปิดได้เฉพาะในเครื่อง ไฟล์ API โมเดล และบริการ TCP บนพีซีหรือเซิร์ฟเวอร์ |
| แชร์ให้คนที่ระบุ | เลือกแอป HTTP/ไฟล์ ตรวจสอบบัญชี Tailscale กำหนดวันหมดอายุและเพิกถอนสิทธิ์ ผู้รับต้องใช้ Tailscale [บุคคล](people.md) |
| ให้ผู้เยี่ยมชมเปิดในเบราว์เซอร์ | ลิงก์ชั่วคราวพร้อม PIN ที่เลือกตั้งได้สำหรับแอป HTTP proxy หรือ HTTPS สาธารณะผ่าน Funnel ที่เปิดอย่างชัดเจน ลิงก์ส่งต่อได้และไม่ยืนยันตัวบุคคล [ลิงก์ผู้เยี่ยมชม](guest-links.md) |
| ดูแลแอปหลายตัว | รายการแอปต่อโฮสต์ พอร์ทัลส่วนตัว ตรวจสุขภาพและแจ้งเตือน ประวัติการเข้าถึง และ CLI/MCP พร้อมบทบาทเอเจนต์ ขอบเขตแอป และบันทึกการตรวจสอบ [พอร์ทัล](portal.md) · [สิทธิ์ MCP](mcp-scopes.md) |

[สูตรตั้งค่าแอป](apps.md), [ข้อจำกัดการอัปโหลด](sharing.md), [ระยะเวลาที่ยืดหยุ่น](durations.md) และ [คำแนะนำผ่าน QR กับคำขอเข้าถึง](requests.md) ช่วยให้ดูแลประจำวันง่ายขึ้น ความสามารถเหล่านี้มีอยู่ในซอร์สโค้ดนี้แล้ว

<a id="installation"></a>
<a id="quickstart"></a>

## เริ่มต้นอย่างรวดเร็ว

ติดตั้งจากซอร์สด้วย **Git และ Go 1.26.6+** ยังไม่มีรุ่นไบนารีสำเร็จรูปหรือ Homebrew เผยแพร่ คำสั่งนี้ใช้ bash/zsh ดู[การตั้งค่า macOS, Linux และ Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

คุณต้องมี **บัญชี Tailscale** และ [MagicDNS กับ HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) อุปกรณ์ที่เข้าถึงแบบส่วนตัวต้องใช้ Tailscale และได้รับอนุญาตตามนโยบายเครือข่าย TSLink ฝัง Tailscale ไว้บนโฮสต์ของแอป

เมื่อแอปทำงานอยู่ที่พอร์ต 3000 แล้ว:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

เลือกชื่อที่ยังไม่ถูกใช้ หาก `share` คืนชื่ออื่น ให้ใช้ชื่อนั้นกับ `url` ทำการลงทะเบียนผ่านเบราว์เซอร์และอนุมัติอุปกรณ์ตามที่แจ้งก่อน แล้วเปิด URL แอปที่ได้รับจริงบนอุปกรณ์ที่อนุญาต `share` เริ่มบริการเบื้องหลังเมื่อจำเป็น การใช้แบบส่วนตัวครั้งแรกนี้ไม่ต้องมีโทเค็น API ผู้ดูแล แชร์ไฟล์ด้วย `tslink share ./report.html` โดยไฟล์ต้องมีอยู่และแอปต้องทำงานแล้ว [การตั้งค่าฉบับเต็ม](getting-started.md)

เมื่อใช้งานได้และมีประโยชน์ คุณอาจ[ให้ดาว TSLink](https://github.com/anydoor7/tslink) เพื่อช่วยให้คนอื่นค้นพบ ทั้งหมดเป็นความสมัครใจ

<a id="architecture"></a>

## ส่วนต่างๆ ทำงานร่วมกันอย่างไร

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="พีซีหรือโฮสต์คลาวด์หนึ่งเครื่อง: CLI/MCP จัดการเดมอนร่วมและโหนดแยกต่อแอป อุปกรณ์ส่วนตัวเชื่อมต่อผ่าน Tailscale ที่เข้ารหัส ส่วน HTTPS/Funnel สาธารณะที่เลือกเปิดจะเข้าถึงแอป HTTP ผ่านการตรวจสอบผู้เยี่ยมชมหรือการเผยแพร่แบบเปิดอย่างชัดเจน" width="960">
</picture>

ให้นึกถึงเส้นทางส่วนตัวที่เข้ารหัสไปยังแอปของคุณ **Tailscale ให้การรับส่งเครือข่ายและ HTTPS ส่วน TSLink จัดการสิทธิ์เข้าแอปในแต่ละโฮสต์** เดมอนหนึ่งตัวรันโหนดฝังตัวแยกสำหรับแต่ละบริการ พอร์ทัลส่วนตัวแสดงแอปที่อนุญาต พร้อมสถานะสุขภาพและประวัติที่ช่วยดูแลระบบ

การเข้าถึงสาธารณะต้องเลือกเปิด: ผู้เยี่ยมชมต้องมีลิงก์และ PIN หากตั้งไว้ ส่วน Funnel แบบเปิดเข้าถึงได้โดยทุกคนที่มี URL ทั้งสองใช้ HTTPS สาธารณะ ไม่ใช่ตัวตนผู้ใช้แบบส่วนตัว TCP ดิบยังคงเป็นส่วนตัวและขึ้นกับนโยบาย tailnet กับการยืนยันตัวตนของแบ็กเอนด์ TSLink ไม่ติดตั้งแอป ไม่แยกกระบวนการ ไม่สร้าง VPC คลาวด์ และไม่รวมหลายโฮสต์ เป็นโครงการอิสระที่ทำงานร่วมกับ Tailscale [สถาปัตยกรรมและขอบเขต](architecture.md)

<a id="agents"></a>

## สำหรับเอเจนต์

จัดการรายการ สุขภาพ URL และสิทธิ์ผ่าน CLI/MCP เริ่มจาก[คู่มือเอเจนต์](agent-quickstart.md) อ่านสคีมาของเครื่องมือปัจจุบัน และตรวจสอบการเข้าแอปจริงก่อนรายงานความสำเร็จ

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

ระบบอัตโนมัติ CLI ใช้ `--json` ส่วน MCP ใช้ JSON-RPC ผ่าน stdio [ไคลเอนต์](mcp-clients.md) · [MCP ระยะไกล](remote-mcp.md) · [บทบาทและขอบเขต](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## เอกสารและสัญญาอนุญาต

[คู่มือทั้งหมด](INDEX.md) · [อ้างอิง CLI](cli-reference.md) · [AI ในเครื่อง](local-ai.md) · [สุขภาพ](health-and-alerts.md) · [ประวัติการเข้าถึง](access-log.md) · [แผนพัฒนา](roadmap.md)

รายการแอปรวมหลายโฮสต์ยังอยู่ในแผน ยินดีรับ[การมีส่วนร่วม](../CONTRIBUTING.md)และ[รายงานความปลอดภัย](../SECURITY.md) [Apache 2.0](../LICENSE) อนุญาตการใช้เชิงพาณิชย์ โปรดคง [NOTICE](../NOTICE) และ[ประกาศบุคคลที่สาม](../THIRD_PARTY_NOTICES.md) เมื่อแจกจ่ายต่อ [ความร่วมมือเชิงพาณิชย์](../COMMERCIAL.md) เป็นความสมัครใจ เงื่อนไขและแพ็กเกจ Tailscale ใช้แยกต่างหาก
