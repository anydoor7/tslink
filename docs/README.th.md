<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="โลโก้ TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>ให้แอป โมเดล และไฟล์ในเครื่องมีที่อยู่ส่วนตัวของตัวเอง</strong><br>
  เข้าถึงจากอุปกรณ์อื่นที่ได้รับอนุญาตในเครือข่าย Tailscale ของคุณ
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="สัญญาอนุญาต: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 ขึ้นไป"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: โหนด tsnet ในตัว"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 เครื่องมือ"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <strong>ไทย</strong> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## การติดตั้ง

ต้องมี **Go 1.26.6 ขึ้นไป** และ Git ยังไม่มีรุ่นไบนารีที่สร้างไว้ล่วงหน้าหรือ Homebrew cask จึงต้องติดตั้งจากซอร์ส ตัวอย่างใช้ **bash หรือ zsh** ดูข้อกำหนดของ Windows และบริการเบื้องหลังได้ที่ [การรองรับแพลตฟอร์ม](platforms.md) คู่มือโดยละเอียดเป็นภาษาอังกฤษ

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

ใช้บัญชี Tailscale ที่[เปิด MagicDNS และ HTTPS แล้ว](https://tailscale.com/docs/how-to/set-up-https-certificates) อุปกรณ์ที่จะเข้าถึงบริการต้องลงชื่อเข้าใช้เครือข่าย Tailscale ของคุณ (**tailnet**) และนโยบายเครือข่ายต้องอนุญาตให้เชื่อมต่อกับบริการ TSLink มี Tailscale ในตัวบนเครื่องที่ให้บริการ

### แชร์หน้าแรกของคุณ

สร้างหน้าเว็บ แล้วให้ TSLink ให้บริการไฟล์โดยตรง พร้อมเริ่มบริการเบื้องหลังเมื่อจำเป็น:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

หาก TSLink แสดง URL สำหรับลงทะเบียนโหนด ให้เปิด URL เพื่ออนุญาตโหนด tailnet ของคุณอาจต้องให้ผู้ดูแลระบบอนุมัติอุปกรณ์ด้วย จากนั้นรับที่อยู่จริงของบริการ:

```bash
tslink url demo --wait
```

เปิด URL ที่ได้รับบนอุปกรณ์ที่ได้รับอนุญาต การแชร์ครั้งแรกไม่ต้องใช้ API token [รายละเอียดการตั้งค่าและวงจรการทำงาน →](getting-started.md)

<a id="use-cases"></a>

## คุณอยากแชร์อะไร?

ต้องมีไฟล์อยู่ก่อนแล้ว ส่วนแอป ฐานข้อมูล และระบบโมเดลเบื้องหลังต้องทำงานบนพอร์ตที่ระบุอยู่แล้ว

| การใช้งาน | คำสั่ง |
|---|---|
| เปิดแอปในเครื่องจากอุปกรณ์อื่น | `tslink share 3000` |
| เรียกดูไฟล์ในไดเรกทอรี | `tslink share ./public --name files` |
| อ่านรายงาน HTML ที่สร้างไว้บนโทรศัพท์ | `tslink share ./report.html --name report` |
| เชื่อมต่อฐานข้อมูลในเครื่องผ่าน TCP | `tslink add database --tcp localhost:5432` |
| ใช้ HTTP API ของโมเดลในเครื่อง เช่น Ollama | `tslink add model --proxy localhost:11434` |

สำหรับ Ollama ให้ใช้ `tslink url model --wait` เพื่อรับ URL จริง ส่วน `baseURL` ของไคลเอนต์ที่รองรับ OpenAI API คือ URL นั้นต่อท้ายด้วย `/v1` [โมเดลในเครื่องและการทำงานกับข้อมูลส่วนตัว →](local-ai.md)

สำหรับหลายแอปบนโฮสต์เดียว TSLink รวมโหนดบริการที่มีชื่อ รายการอนุญาตตามตัวตนสำหรับ HTTP วันหมดอายุของ Funnel และการจัดการ MCP ไว้ด้วยกัน [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) อาจเพียงพอสำหรับแอปเดียวบนอุปกรณ์ของคุณเอง

<a id="architecture"></a>

## สถาปัตยกรรม

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="ตัวอย่างแผนผังบริการ: App, Docs, Database และ Model เป็นโหนดที่มีชื่อแยกกันใน tailnet เดียว แอป ไฟล์ และ API ของโมเดลใช้ HTTPS ส่วนฐานข้อมูลใช้ TCP ภายในเครือข่ายส่วนตัว" width="960">
</picture>

**หนึ่ง tailnet หลายโหนดบริการ** เดมอนร่วมกันหนึ่งตัวทำงานด้วยโหนด tsnet ในตัวแยกสำหรับแต่ละบริการ เพื่อส่งต่อ HTTP ให้บริการไฟล์ หรือพร็อกซี TCP การเปลี่ยนแปลงทะเบียนบริการมีผลระหว่างการทำงาน แต่ละโหนดมีตัวตนบนเครือข่ายของตัวเอง และทุกบริการใช้เครื่องที่ให้บริการร่วมกัน [รายละเอียดสถาปัตยกรรม →](architecture.md)

| องค์ประกอบ | หน้าที่ |
|---|---|
| [Go](../go.mod) | โปรแกรมบรรทัดคำสั่งแบบเนทีฟ |
| [Tailscale tsnet](architecture.md) | โหนดบริการและการรับส่งผ่าน tailnet |
| [Cobra](https://github.com/spf13/cobra) | คำสั่งและความช่วยเหลือ |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | ช่องทางรับส่งของเอเจนต์ |
| พวงกุญแจของระบบปฏิบัติการและตัวจัดการบริการผู้ใช้ | การเก็บข้อมูลรับรองแบบเลือกใช้และการทำงานเบื้องหลัง |

บริการอยู่ภายใน tailnet เว้นแต่คุณจะเปิด [Funnel สาธารณะ](getting-started.md#more-examples) อย่างชัดเจน บริการ HTTP และไฟล์รองรับรายการอนุญาตตามตัวตน (`WhoIs`, `--allow`) ส่วน TCP ใช้นโยบาย tailnet และการยืนยันตัวตนของระบบเบื้องหลังเอง ดู [ขอบเขตการแชร์](sharing.md)

TSLink ไม่ติดตั้งแอป ไม่รันโมเดล ไม่แยกโปรเซสของโฮสต์ และไม่รวมหลายโฮสต์ เครือข่าย การเข้ารหัสและ HTTPS มาจาก Tailscale โดย TSLink เป็นโครงการอิสระ

<a id="agents"></a>

## สำหรับเอเจนต์

**เครื่องมือ MCP ทั้ง 19 ตัว** ช่วยให้เอเจนต์แชร์รายงาน จัดการบริการ รับ URL และตรวจสอบการตั้งค่า เชื่อมต่อไคลเอนต์ MCP ในเครื่องกับโปรแกรมที่ติดตั้งแล้ว:

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

MCP ใช้จัดการ TSLink ส่วนแอปใช้ HTTP API ของโมเดลสำหรับการอนุมาน ดูการตั้งค่าและระบบอัตโนมัติที่ [ไคลเอนต์ MCP](mcp-clients.md), [MCP ระยะไกล](remote-mcp.md) และ [คู่มือการทำงานของเอเจนต์](../AGENTS.md)

การทำงานอัตโนมัติผ่าน CLI รองรับ `--json` โดย `schema_version` เป็น `1` ดูได้ด้วย `tslink status --urls --json` ส่วน MCP ในเครื่องใช้ JSON-RPC ผ่าน stdio ดู [การทำงานอัตโนมัติด้วย JSON](json-automation.md)

<a id="roadmap"></a>

## สิ่งที่จะมา

รายการที่ระบุว่ากำลังรวม อยู่ระหว่างตรวจทาน หรือวางแผนไว้ ยังไม่รวมในการติดตั้งจากซอร์สด้านบน

| กรณีใช้งาน | สถานะ |
|---|---|
| <!-- roadmap:people --> ให้ญาติเข้าถึงแอป HTTP/ไฟล์ส่วนตัวเป็นเวลา 3 วัน พร้อมรวมคำเชิญของแต่ละแอปในข้อความเดียว โดยผู้รับยังต้องใช้ Tailscale | กำลังรวม |
| <!-- roadmap:health --> ตรวจสุขภาพแอปและรับแจ้งเตือนเมื่อขัดข้องหรือหมดอายุผ่านคำสั่งหรือ webhook ที่เลือกเปิดใช้ | กำลังรวม |
| <!-- roadmap:recipes --> ค้นหาแอป loopback ที่รองรับและดูสูตรสำหรับแอปที่โฮสต์เองก่อนแชร์ | กำลังรวม |
| <!-- roadmap:limits --> ตั้งขนาดอัปโหลดและเวลารอคำขอของแต่ละแอป HTTP สำหรับไฟล์ใหญ่และไคลเอนต์ที่ช้า | กำลังรวม |
| <!-- roadmap:windows --> เริ่ม daemon ของ Windows ใหม่หลังขัดข้องขณะผู้ใช้ลงชื่อเข้าใช้ โดยใช้งานตามกำหนดเวลาและตัวควบคุมในตัว | กำลังรวม |
| <!-- roadmap:access-log --> ดูว่าใครเปิดแอปใดในบันทึกการเข้าถึงในเครื่อง โดยเลือกโหมดบันทึกเส้นทาง `prefix`, `full` หรือ `off` | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:portal --> เปิดหน้าแรกเดียวที่แสดงแอปที่อนุญาต พร้อมข้อมูลส่งต่อการลงทะเบียนให้เจ้าของ โดยผู้เยี่ยมชมยังต้องใช้ Tailscale | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:mcp-scopes --> กำหนดบทบาทและขอบเขตแอปให้เอเจนต์ พร้อมบันทึกตรวจสอบการเปลี่ยนแปลงของเอเจนต์ | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:guest-links --> ให้ผู้เยี่ยมชมเปิดแอป HTTP หนึ่งแอปในเบราว์เซอร์โดยไม่ต้องติดตั้ง Tailscale ด้วยลิงก์หมดอายุและ PIN ที่เลือกใช้ได้ ผ่าน Funnel สาธารณะที่มีการควบคุมการเข้าถึง | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:durations --> เลือกช่วงเวลาสำเร็จรูปหรือกำหนดเองอย่างน้อย 1 ชั่วโมง โดยเวลาสูงสุดของผู้เยี่ยมชมเป็น 7 วันตามค่าเริ่มต้นและปรับได้ | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:requests --> ช่วยผู้ใช้โทรศัพท์เข้าร่วมด้วย QR code และอนุมัติการเข้าถึงแอปหรือขอเวลาเพิ่มในครั้งเดียว | อยู่ระหว่างตรวจทาน |
| <!-- roadmap:multi-host --> ดูแอปจากหลายโฮสต์ในรายการเดียว | วางแผนไว้ |

<a id="documentation"></a>

## เอกสารและสัญญาอนุญาต

[เริ่มต้นใช้งาน](getting-started.md) · [โมเดลในเครื่อง](local-ai.md) · [คู่มือ CLI](cli-reference.md) · [แพลตฟอร์ม](platforms.md) · [แผนการพัฒนา](roadmap.md)

ดูวิธีร่วมพัฒนาที่ [CONTRIBUTING.md](../CONTRIBUTING.md) และรายงานช่องโหว่ผ่านช่องทางใน [SECURITY.md](../SECURITY.md)

TSLink ใช้ [Apache License 2.0](../LICENSE) โดยไม่มีการแก้ไข และอนุญาตให้ใช้เชิงพาณิชย์ตามสัญญาอนุญาตนี้ เมื่อแจกจ่ายต่อ ให้เก็บ [NOTICE](../NOTICE) และ [ประกาศของบุคคลที่สาม](../THIRD_PARTY_NOTICES.md) ที่เกี่ยวข้องไว้ [ความร่วมมือเชิงพาณิชย์](../COMMERCIAL.md) เป็นเรื่องสมัครใจและไม่เพิ่มเงื่อนไขสัญญาอนุญาต ข้อกำหนดและแพ็กเกจบริการของ Tailscale มีผลแยกต่างหาก
