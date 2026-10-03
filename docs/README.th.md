<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="โลโก้ TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>แชร์แอปบนคอมพิวเตอร์ให้คนที่คุณเลือก นานเท่าที่คุณต้องการ</strong><br>
  แต่ละแอปมีที่อยู่ส่วนตัวของตัวเองบนเครือข่าย Tailscale ของคุณ ดูได้ว่าใครมีสิทธิ์เข้าถึง และถอนสิทธิ์ได้
</p>

<p align="center">
  <a href="#quickstart">เริ่มต้นอย่างรวดเร็ว</a> · <a href="#agents">สำหรับเอเจนต์</a> · <a href="getting-started.md">เอกสาร</a> ·
  <strong>ไทย</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">ทุกภาษา</a>
</p>

## ผู้คนใช้ทำอะไร

- **เปิดผลงานของคุณบนมือถือ** ไม่ว่าจะเป็นรายงานจากสคริปต์ เซิร์ฟเวอร์พัฒนา โน้ตบุ๊ก หรือ API ของโมเดลในเครื่อง ก็เข้าถึงได้ผ่านที่อยู่ HTTPS ส่วนตัวจากอุปกรณ์ที่ได้รับอนุญาต
- **ให้คนหนึ่งใช้แอปหนึ่งเป็นการชั่วคราว** ให้คู่ของคุณใช้คลังภาพหนึ่งสัปดาห์ หรือให้เพื่อนร่วมงานลองเวอร์ชันตัวอย่างสามวัน สิทธิ์จะหมดอายุเอง และคุณยุติก่อนกำหนดได้
- **ให้เอเจนต์จัดการการแชร์** เอเจนต์เขียนโค้ดเพิ่งสร้างแดชบอร์ดเสร็จ คุณขอให้แชร์กับคุณและเพื่อนร่วมทีมจนถึงวันศุกร์ได้ เอเจนต์ยังบอกได้ว่ากำลังแชร์อะไรอยู่และถอนการแชร์ได้ด้วย

แอปยังทำงานอยู่ที่เดิม TSLink จัดการว่าใครเข้าถึงแต่ละแอปได้ และเก็บรายการเดียวว่าแชร์อะไร กับใคร และถึงเมื่อไร

<a id="quickstart"></a>

## เริ่มต้นอย่างรวดเร็ว

คุณต้องมี **Go 1.26.6+**, Git และบัญชี Tailscale ที่[เปิด MagicDNS และ HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates) ยังไม่มีรุ่นที่คอมไพล์แล้วเผยแพร่ จึงต้องติดตั้งจากซอร์สโค้ด:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

แชร์หน้าเว็บ:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

ครั้งแรก TSLink จะแสดงลิงก์เข้าสู่ระบบเพื่อลงทะเบียนโหนดบริการใหม่ และ tailnet อาจต้องให้ผู้ดูแลอนุมัติอุปกรณ์ด้วย หลังลงทะเบียน ให้เปิด URL ของบริการบนอุปกรณ์ที่ได้รับอนุญาตและเข้าสู่ระบบ tailnet ของคุณแล้ว ไม่ต้องใช้โทเค็น API

ตรวจสอบสิ่งที่แชร์ แล้วลบตัวอย่าง:

```bash
tslink status --urls
tslink remove demo
```

เมื่อระบบเบื้องหลังทำงานแล้ว คุณยังแชร์สิ่งเหล่านี้ได้:

| สิ่งที่แชร์ | คำสั่ง |
|---|---|
| เว็บแอปในเครื่อง | `tslink share 3000` |
| โฟลเดอร์ไฟล์ | `tslink share ./public --name files` |
| API โมเดลในเครื่อง เช่น Ollama | `tslink add model --proxy localhost:11434` |
| ฐานข้อมูลผ่าน TCP ส่วนตัว | `tslink add database --tcp localhost:5432` |
| แอปที่โฮสต์เองซึ่งรองรับ (Jellyfin, Immich, Home Assistant และอีก 13 แอป) | `tslink apps detect` แล้วตามด้วย `tslink apps share jellyfin --yes` |

[เริ่มต้น แพลตฟอร์ม และบริการเบื้องหลัง →](getting-started.md)

## เลือกว่าใครเปิดได้

| กลุ่มผู้ใช้ | สิ่งที่ผู้รับต้องมี | ตัวตน | สิ้นสุดเมื่อ |
|---|---|---|---|
| **อุปกรณ์ของคุณเอง** | เข้าสู่ระบบ tailnet ของคุณ | บัญชี Tailscale ที่ตรวจสอบแล้ว | เมื่อคุณลบแอป |
| **คนที่ระบุชื่อ** (HTTP/ไฟล์ส่วนตัว) | บัญชี Tailscale; คนนอกยอมรับคำเชิญแต่ละแอป | บัญชี Tailscale ที่ตรวจสอบแล้ว | ครบกำหนดที่ตั้งไว้ (`--for 7d`) หรือใช้ `tslink people remove` |
| **ใครก็ตามที่มี URL** (Funnel) | เบราว์เซอร์ | ใครก็ได้ แต่ยังต้องทำตามเงื่อนไขเข้าสู่ระบบของแอป | ค่าเริ่มต้นคือหลัง 24 ชั่วโมง (`--funnel-ttl`) |
| **ลิงก์ผู้เยี่ยมชมผ่านเบราว์เซอร์** | เบราว์เซอร์และ PIN ถ้ากำหนดไว้ | ผู้ที่มีลิงก์ | ครบกำหนดของลิงก์หรือถูกเพิกถอน |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

การแชร์ HTTP และไฟล์ส่วนตัวจะตรวจสอบกำหนดเวลาทุกคำขอ การถอนสิทธิ์หยุดคำขอใหม่ แต่เรียกคืนข้อมูลที่ดาวน์โหลดแล้วหรือปิดสตรีมและการเชื่อมต่อ WebSocket ที่รับไว้แล้วไม่ได้ [แชร์ให้แต่ละคน →](people.md) · [ขอบเขตการแชร์ →](sharing.md)

<a id="agents"></a>

## สำหรับเอเจนต์

TSLink มีเซิร์ฟเวอร์ MCP ให้เอเจนต์แชร์ แสดงรายการ อธิบาย และลบการแชร์ได้เหมือนคุณ เพิ่มลงในไคลเอนต์ MCP ในเครื่อง:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **ผลลัพธ์ที่แน่นอน** การทำงานอัตโนมัติผ่าน CLI รองรับ `--json` พร้อม `schema_version: 1` และรหัสข้อผิดพลาดที่คงที่ ส่วน `tslink mcp` ใช้ JSON-RPC โดย `tslink manifest` อธิบายทุกคำสั่งและแฟล็ก เอเจนต์ควรดึง URL จริงด้วย `tslink url <name> --wait` แทนการประกอบเอง
- **แจ้งสถานะรอตามจริง** โหนดใหม่ที่ยังต้องให้คนเข้าสู่ระบบจะรายงาน `needs_login` แทนการแสดงว่าพร้อมแล้ว
- **สิทธิ์** MCP ในเครื่องทำงานด้วยสิทธิ์ผู้ใช้ของคุณ MCP ระยะไกลต้องเปิดใช้งานเอง เข้าถึงได้เฉพาะใน tailnet และจำกัดเฉพาะบัญชีหรือแท็กที่ระบุ บทบาทรายเอเจนต์ ขอบเขตแอป และใบรับรองการดำเนินการพร้อมใช้งานแล้ว

MCP ของ TSLink ใช้ควบคุม TSLink เอง หากเผยแพร่เซิร์ฟเวอร์ MCP อื่นผ่าน TSLink เซิร์ฟเวอร์นั้นยังต้องมีสิทธิ์เครื่องมือของตัวเอง
[คู่มือเอเจนต์ →](agents.md) · [ไคลเอนต์ MCP →](mcp-clients.md) · [MCP ระยะไกล →](remote-mcp.md) · [ระบบอัตโนมัติ JSON →](json-automation.md)

## เมื่อไรควรใช้เครื่องมืออื่น

| ถ้าคุณต้องการ | ลองพิจารณา |
|---|---|
| บริการในเครื่องหนึ่งรายการบนอุปกรณ์ของคุณ โดยใช้ไคลเอนต์ Tailscale ที่เปิดอยู่แล้ว | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| บริการที่ผู้ดูแลจัดการและมีชื่อคงที่บนหลายโฮสต์ | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| URL สาธารณะสำหรับ webhook หรือสาธิต API โดยไม่ต้องมีบัญชี Tailscale | [ngrok](https://ngrok.com/docs/start) หรือ [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| ติดตั้งและรันแอปที่โฮสต์เอง นอกเหนือจากแชร์แอป | [Umbrel](https://umbrel.com) หรือ [Coolify](https://coolify.io) |
| แพลตฟอร์มเข้าถึงตามตัวตนสำหรับทั้งองค์กร | [Pangolin](https://github.com/fosrl/pangolin) หรือ [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink เหมาะเมื่อคนหนึ่งรันหลายแอปและต้องการสิทธิ์ที่มีระยะเวลากำหนด แยกตามแอปและคน ซึ่งทั้งเจ้าของและเอเจนต์ตรวจสอบได้

## ทำงานอย่างไร

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database และ Model เป็นโหนดที่มีชื่อแยกกันใน tailnet เดียว รันโดยเดมอน TSLink หนึ่งตัวบนคอมพิวเตอร์ที่เผยแพร่บริการ" width="720">
</picture>

เดมอนเบื้องหลังหนึ่งตัวรันโหนด Tailscale แบบฝังตัวให้แต่ละแอป จึงมีชื่อและที่อยู่ของตัวเอง สำหรับ HTTP และไฟล์ส่วนตัว `WhoIs` และสิทธิ์รายบุคคลหรือกฎ `--allow` จะควบคุมการเข้าถึง โดยตรวจสอบกำหนดเวลารายบุคคลทุกคำขอ TCP แบบดิบใช้ข้อกำหนด tailnet และการยืนยันตัวตนของระบบเบื้องหลัง Tailscale ให้การรับส่งผ่าน tailnet การเข้ารหัส และใบรับรอง ส่วน TSLink เป็นโครงการอิสระ ทุกแอปใช้คอมพิวเตอร์ที่เผยแพร่ร่วมกัน ดังนั้น TSLink ไม่ได้แยกแอปออกจากกัน [สถาปัตยกรรม →](architecture.md)

## สถานะ

ใช้ได้แล้ว: ที่อยู่ส่วนตัวรายแอป สิทธิ์รายบุคคลพร้อมกำหนดเวลาและชุดคำเชิญ Funnel สาธารณะที่หมดอายุได้ การตรวจสอบสุขภาพแอปและแจ้งเตือน สูตรสำหรับแอปที่โฮสต์เอง ขีดจำกัดคำขอรายแอป การเริ่มใหม่หลังขัดข้องบน Windows, CLI และ MCP ฟีเจอร์ที่ใช้ได้แล้วเช่นกัน: ลิงก์ผู้เยี่ยมชมผ่านเบราว์เซอร์ ระยะเวลาที่ยืดหยุ่น บันทึกการเข้าถึง หน้าแรกของแอป บทบาทเอเจนต์ที่จำกัดขอบเขต การเริ่มใช้งานผ่าน QR และคำขอเข้าถึง

การดูหลายคอมพิวเตอร์ในรายการเดียวอยู่ในแผน [แผนพัฒนา →](roadmap.md)

## เอกสารและสัญญาอนุญาต

[llms.txt](../llms.txt) · [เริ่มต้นใช้งานสำหรับเอเจนต์](agent-quickstart.md) · [เลือกเครื่องมือสำหรับการแชร์](comparison.md)

[เริ่มต้น](getting-started.md) · [อ้างอิง CLI](cli-reference.md) · [แพลตฟอร์ม](platforms.md) · [โมเดลในเครื่อง](local-ai.md) · [ร่วมพัฒนา](../CONTRIBUTING.md) · [ความปลอดภัย](../SECURITY.md)

ใช้ Apache License 2.0 รวมถึงการใช้เชิงพาณิชย์ เมื่อแจกจ่ายต่อให้เก็บ [NOTICE](../NOTICE) และ[ประกาศของบุคคลที่สาม](../THIRD_PARTY_NOTICES.md) ไว้ ข้อกำหนดและแผนบริการของ Tailscale มีผลแยกต่างหาก
