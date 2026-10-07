<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Địa chỉ riêng cho ứng dụng của bạn, trên mạng Tailscale của bạn.</strong></p>
<p align="center">Mở chúng từ thiết bị của chính bạn. Chia sẻ cho một người hoặc qua một liên kết, đến ngày bạn chọn.</p>
<p align="center"><strong>Tiếng Việt</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Ngôn ngữ khác</a></p>

```sh
tslink share 3000 --name notes       # a web app → https://notes.<your-tailnet>.ts.net
tslink share ./photos                # a folder or a single file
tslink add db --tcp localhost:5432   # any TCP port
```

<a id="installation"></a>
<a id="quickstart"></a>

## Cài đặt

```sh
brew install --cask anydoor7/tap/tslink
```

Gói `.deb` và `.rpm` cho Linux cùng bản dựng cho Windows có trong [bản phát hành mới nhất](https://github.com/anydoor7/tslink/releases/latest). Lần đầu bạn chia sẻ một ứng dụng, TSLink sẽ hiện một liên kết đăng nhập Tailscale cho ứng dụng đó. [Bắt đầu](getting-started.md)

<a id="why"></a>

## Khi nào bạn cần TSLink

Với một ứng dụng trên thiết bị của bạn, Serve là đủ. TSLink gom địa chỉ ứng dụng, thời hạn và thay đổi quyền truy cập vào một quy trình.

| Việc cần làm | Chỉ Tailscale | TSLink |
|---|---|---|
| Một ứng dụng web trên điện thoại | `tailscale serve 3000` là đủ | `tslink share 3000` |
| Nhiều ứng dụng, mỗi cái một tên | Thiết lập Services, hoặc các nút riêng | Mỗi ứng dụng một lệnh `share`/`add`; đăng ký từng nút |
| Một người, một ứng dụng, bảy ngày | Quy tắc chính sách, rồi dùng công cụ JIT hoặc tự gỡ | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/tệp) |
| Liên kết trình duyệt, ba ngày | Funnel công khai; tự thêm lớp kiểm soát truy cập và lịch tắt | `tslink guest create photos --for 3d --public --print-link` (chỉ HTTP) |

Người nhận riêng tư cần có Tailscale. Liên kết khách là công khai: ai có liên kết cũng mở và chuyển tiếp được.

[So sánh đầy đủ](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Ứng dụng của bạn, trên thiết bị của bạn

- **Mỗi ứng dụng một địa chỉ.** Ứng dụng web, thư mục, tệp đơn lẻ và cổng TCP đều có tên riêng trong tailnet của bạn, nên bạn dùng tên thay vì địa chỉ IP.
- **Mặc định là riêng tư.** Không có gì công khai cho đến khi bạn tạo liên kết khách hoặc xuất bản qua Funnel.
- **Một trang chủ** liệt kê ứng dụng của bạn cùng tình trạng của chúng. [Cổng](portal.md)
- **Kiểm tra tình trạng và cảnh báo** qua lệnh hoặc webhook, cùng nhật ký truy cập có cả các yêu cầu bị từ chối. [Tình trạng và cảnh báo](health-and-alerts.md) · [Lịch sử truy cập](access-log.md)
- **Công thức cho 15 ứng dụng tự lưu trữ**, gồm Home Assistant, Jellyfin, Immich và Ollama. `tslink apps detect` tìm những ứng dụng đang chạy sẵn. [Công thức cấu hình](apps.md)

## Chia sẻ khi bạn muốn

```sh
tslink people add alice@example.com --apps notes --for 7d   # a tailnet member, for 7 days
tslink guest create notes --for 3d --public --print-link    # a browser link, no Tailscale needed
tslink add launch --proxy localhost:4000 --funnel --public --funnel-ttl 1h   # anyone, for one hour
```

Liên kết khách và URL công khai mới sẽ hết hạn và chỉ dùng được cho ứng dụng web; thư mục, tệp và cổng TCP vẫn riêng tư. [Người dùng](people.md) · [Liên kết khách](guest-links.md) · [Truy cập công khai](funnel.md)

<a id="agents"></a>

## Dành cho tác tử AI

Máy chủ phát triển mà tác tử khởi động trên `localhost` thì điện thoại của bạn không với tới được. TSLink cho phép tác tử cấp cho nó một địa chỉ riêng, báo URL chính xác và gỡ bỏ khi xong.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **CLI hoặc MCP.** Các lệnh quản lý nhận `--json` và trả về kết quả có phiên bản; `tslink mcp` cung cấp các thao tác về ứng dụng và quyền truy cập qua MCP.
- **Vai trò giới hạn.** `viewer`, `app-operator` hoặc `people-manager`, chỉ trong các ứng dụng bạn chỉ định. `tslink mcp-audit` cho thấy tác tử đã thay đổi gì. Vai trò giới hạn công cụ của TSLink, không giới hạn shell riêng của tác tử.

[Hướng dẫn tác tử](agent-quickstart.md) · [Quyền MCP](mcp-scopes.md) · [MCP từ xa](remote-mcp.md)

<a id="architecture"></a>

## Cách hoạt động

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Một PC hoặc máy chủ đám mây: CLI/MCP quản lý tiến trình nền chung và nút riêng cho từng ứng dụng. Thiết bị riêng tư dùng Tailscale mã hóa; HTTPS/Funnel công khai tùy chọn đi qua cổng khách hoặc chế độ công khai rõ ràng tới ứng dụng HTTP." width="960">
</picture>

Một tiến trình nền chạy một nút Tailscale riêng cho mỗi ứng dụng. Tailscale cung cấp kênh truyền trong tailnet và chứng chỉ HTTPS. Truy cập riêng tư vào web và tệp có thể giới hạn theo danh tính Tailscale bằng `--allow` và quyền cấp cho người dùng; TCP thô dựa vào chính sách tailnet của bạn và cơ chế đăng nhập riêng của ứng dụng. [Kiến trúc](architecture.md)

<a id="requirements"></a>

## Yêu cầu

| Ai | Cần gì |
|---|---|
| Bạn | Tài khoản Tailscale đã bật MagicDNS và HTTPS |
| Máy chạy ứng dụng của bạn | TSLink, vốn đã tích hợp Tailscale (trên Linux, cần một phiên người dùng systemd) |
| Thiết bị của bạn và những người bạn chia sẻ | Ứng dụng Tailscale |
| Khách | Trình duyệt |

Tên các ứng dụng HTTPS xuất hiện trong nhật ký chứng chỉ công khai, nên hãy chọn tên mà bạn không ngại người khác nhìn thấy.

<a id="documentation"></a>

## Thêm

[Toàn bộ tài liệu](INDEX.md) · [Tham khảo CLI](cli-reference.md) · [So sánh với Serve, ngrok và Cloudflare](comparison.md) · [Đóng góp](../CONTRIBUTING.md) · [Bảo mật](../SECURITY.md)

Apache 2.0. TSLink là dự án độc lập, không do Tailscale tạo ra hay bảo trợ.
