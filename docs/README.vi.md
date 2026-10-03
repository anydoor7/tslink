<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Truy cập và quản lý ứng dụng của bạn từ mọi nơi.<br>Giữ riêng tư hoặc chia sẻ theo cách bạn muốn.</strong></p>

Ứng dụng trên máy tính hoặc máy chủ đám mây của bạn: truy cập qua mạng riêng được mã hóa, hoặc chủ động chọn liên kết khách trên trình duyệt hay truy cập công khai. Tự quản lý hoặc giao cho tác tử.

<p align="center"><a href="#quickstart">Bắt đầu nhanh</a> · <a href="#agents">Dành cho tác tử</a> · <a href="#documentation">Tài liệu</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <strong>Tiếng Việt</strong>
</p>

<a id="use-cases"></a>

## Ứng dụng luôn trong tầm tay

| Nhu cầu | TSLink cung cấp |
|---|---|
| Dùng ứng dụng trên nhiều thiết bị | Địa chỉ riêng cho bảng điều khiển tại nhà, trang web chỉ chạy cục bộ, tệp, API mô hình và dịch vụ TCP trên PC hoặc máy chủ. |
| Chia sẻ với người cụ thể | Chọn ứng dụng HTTP/tệp, xác minh danh tính đăng nhập Tailscale, đặt hạn và thu hồi quyền. Người nhận cần Tailscale. [Người dùng](people.md) |
| Cho khách mở bằng trình duyệt | Liên kết có hạn với PIN tùy chọn cho ứng dụng proxy HTTP, hoặc HTTPS công khai qua Funnel khi chủ động bật. Liên kết có thể chuyển tiếp, không xác minh danh tính. [Liên kết khách](guest-links.md) |
| Quản lý một nhóm ứng dụng | Danh mục theo máy chủ, cổng riêng, kiểm tra tình trạng và cảnh báo, lịch sử truy cập, CLI/MCP với vai trò tác tử, phạm vi ứng dụng và bản ghi kiểm toán. [Cổng](portal.md) · [Quyền MCP](mcp-scopes.md) |

[Công thức cấu hình](apps.md), [giới hạn tải lên](sharing.md), [thời hạn linh hoạt](durations.md) và [hướng dẫn QR, yêu cầu truy cập](requests.md) giúp bảo trì hằng ngày. Các tính năng này đã có trong mã nguồn hiện tại.

<a id="installation"></a>
<a id="quickstart"></a>

## Bắt đầu nhanh

Cài từ mã nguồn với **Git và Go 1.26.6+**; chưa phát hành bản biên dịch sẵn hoặc Homebrew. Lệnh dùng bash/zsh. [Thiết lập macOS, Linux và Windows](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Bạn cần quyền truy cập kho mã, **tài khoản Tailscale**, [MagicDNS và HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Thiết bị truy cập riêng cần Tailscale và quyền theo chính sách mạng. TSLink nhúng Tailscale trên máy chạy ứng dụng.

Khi ứng dụng đã chạy ở cổng 3000:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

Chọn tên chưa dùng; nếu `share` trả về tên khác, dùng tên đó trong `url`. Hoàn tất đăng ký qua trình duyệt và phê duyệt thiết bị nếu được yêu cầu, rồi mở URL chính xác trên thiết bị được phép. `share` khởi động dịch vụ nền khi cần. Lần dùng riêng đầu tiên này không cần token API quản trị. Chia sẻ tệp bằng `tslink share ./report.html`; tệp phải tồn tại và ứng dụng phải đang chạy. [Thiết lập đầy đủ](getting-started.md)

Khi dùng thành công và thấy hữu ích, bạn có thể [gắn sao cho TSLink](https://github.com/anydoor7/tslink) để người khác dễ tìm thấy hơn. Hoàn toàn tự nguyện.

<a id="architecture"></a>

## Cách các thành phần kết nối

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Một PC hoặc máy chủ đám mây: CLI/MCP quản lý tiến trình nền chung và nút riêng cho từng ứng dụng. Thiết bị riêng tư dùng Tailscale mã hóa; HTTPS/Funnel công khai tùy chọn đi qua cổng khách hoặc chế độ công khai rõ ràng tới ứng dụng HTTP." width="960">
</picture>

Hãy hình dung một đường riêng được mã hóa tới ứng dụng. **Tailscale cung cấp truyền tải mạng và HTTPS; TSLink quản lý truy cập trên từng máy chủ.** Một tiến trình nền chạy nút nhúng riêng cho mỗi dịch vụ. Cổng riêng liệt kê ứng dụng được phép; tình trạng và lịch sử giúp bảo trì.

Truy cập công khai phải được bật chủ động: khách cần liên kết và PIN nếu có; Funnel mở cho phép bất kỳ ai có URL truy cập. Cả hai dùng HTTPS công khai, không phải danh tính người dùng riêng tư. TCP thuần vẫn riêng tư, dựa vào chính sách tailnet và xác thực backend. TSLink không cài ứng dụng, cô lập tiến trình, tạo VPC đám mây hay tổng hợp nhiều máy chủ. Dự án độc lập hoạt động cùng Tailscale. [Kiến trúc và giới hạn](architecture.md)

<a id="agents"></a>

## Dành cho tác tử

Quản lý danh mục, tình trạng, URL và quyền qua CLI/MCP. Đọc [hướng dẫn tác tử](agent-quickstart.md), xem schema công cụ hiện tại và xác minh truy cập thực tế trước khi báo thành công.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

Tự động hóa CLI dùng `--json`; MCP dùng JSON-RPC qua stdio. [Máy khách](mcp-clients.md) · [MCP từ xa](remote-mcp.md) · [Vai trò và phạm vi](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## Tài liệu và giấy phép

[Mọi hướng dẫn](INDEX.md) · [Tham khảo CLI](cli-reference.md) · [AI cục bộ](local-ai.md) · [Tình trạng](health-and-alerts.md) · [Lịch sử truy cập](access-log.md) · [Lộ trình](roadmap.md)

Danh mục nhiều máy chủ đang được lên kế hoạch. Hoan nghênh [đóng góp](../CONTRIBUTING.md) và [báo cáo bảo mật](../SECURITY.md). [Apache 2.0](../LICENSE) cho phép dùng thương mại; giữ [NOTICE](../NOTICE) và [thông báo bên thứ ba](../THIRD_PARTY_NOTICES.md) khi phân phối lại. [Hợp tác thương mại](../COMMERCIAL.md) là tự nguyện. Điều khoản và gói Tailscale áp dụng riêng.
