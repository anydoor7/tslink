<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center">TSLink là một CLI và máy chủ MCP mã nguồn mở, cấp cho mỗi ứng dụng trên máy tính của bạn một địa chỉ riêng <code>https://&lt;name&gt;.&lt;tailnet&gt;.ts.net</code>, để bạn hoặc coding agent của bạn mở được máy chủ phát triển đang chạy trên điện thoại. Dự án độc lập, không do Tailscale tạo ra.</p>
<p align="center">Điện thoại của bạn cần có ứng dụng Tailscale, đã đăng nhập vào cùng tailnet.</p>
<p align="center"><strong>Tiếng Việt</strong> · <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ko.md">한국어</a> · <a href="README.es.md">Español</a> · <a href="INDEX.md#translated-homepages">Ngôn ngữ khác</a></p>

```text
# Claude Code, sau khi cài CLI (xem Cài đặt bên dưới)
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink

# Các MCP client cục bộ dùng JSON mcpServers (client khác: xem phần thiết lập MCP của client đó)
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}

Hãy nói với tác tử: "Chia sẻ máy chủ phát triển để tôi mở được trên điện thoại."
```

Làm cùng việc đó thủ công, khi máy chủ phát triển đã lắng nghe ở cổng 5173. Với `--json`, `share` báo tên đã đăng ký trong `data.name`. Hãy dùng tên đó trong các lệnh sau: nó khác `--name` khi tên đó đã bị dùng, ví dụ `web-2`.

```sh
tslink share 5173 --name web --json   # data.name; data.url, hoặc data.auth_url (liên kết đăng nhập Tailscale) khi status là needs_login
tslink url <name> --wait              # sau khi đăng nhập xong: URL chính xác
tslink remove <name>                  # khi xong việc; bỏ qua nếu share thất bại. Máy chủ phát triển vẫn chạy
```

<a id="installation"></a>
<a id="quickstart"></a>

## Cài đặt

Máy macOS yêu cầu macOS 13 Ventura trở lên ([hỗ trợ nền tảng](platforms.md)).

```sh
brew install --cask anydoor7/tap/tslink
```

Trên Windows, cài đặt bằng Scoop:

```powershell
scoop bucket add anydoor7 https://github.com/anydoor7/scoop-bucket
scoop install anydoor7/tslink
```

Để nâng cấp, chạy `brew upgrade --cask tslink` hoặc `scoop update; scoop update tslink`. Nếu TSLink chạy dưới dạng dịch vụ nền, hãy chạy lại `tslink install` sau khi nâng cấp.

Gói `.deb` và `.rpm` cho Linux cùng bản dựng cho Windows có trong [bản phát hành mới nhất](https://github.com/anydoor7/tslink/releases/latest). Lần đầu bạn chia sẻ một ứng dụng, TSLink sẽ hiện một liên kết đăng nhập Tailscale cho ứng dụng đó. [Bắt đầu](getting-started.md)

<a id="agents"></a>

## Dành cho tác tử AI

Máy chủ phát triển mà tác tử khởi động trên `localhost` thì điện thoại của bạn không với tới được. TSLink cho phép tác tử cấp cho nó một địa chỉ riêng, đưa bạn URL chính xác và gỡ bỏ phần chia sẻ khi xong. Điện thoại của bạn cần có ứng dụng Tailscale, đã đăng nhập vào cùng tailnet.

Đã thử ngày 2026-10-22 với Vite 8.3.5 và iPhone 15 Pro: tác tử chia sẻ máy chủ phát triển qua MCP, điện thoại mở URL riêng, và trang tự cập nhật khi lưu mà không cần tải lại thủ công.

Với Claude Code, hãy cài CLI trước, rồi chạy:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Plugin gồm một [skill xem trước](../skills/tslink-preview/SKILL.md) và kết nối MCP bên dưới. Marketplace nằm trong kho này và lấy plugin từ [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin). Skill gốc nằm ở đây; plugin mang theo một bản sao được CI kiểm tra. Các tác tử khác có thể dùng cùng skill này hoặc kết nối MCP trực tiếp. Tích hợp này cần CLI cục bộ và không chạy trong trò chuyện trên claude.ai. [Thiết lập và đăng ký](agent-integrations.md)

Với MCP client cục bộ dùng JSON `mcpServers` (client khác: xem hướng dẫn thiết lập MCP của họ và [hướng dẫn MCP client](mcp-clients.md)):

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

- **Mỗi máy chủ phát triển một tên.** Mỗi lần chia sẻ có địa chỉ riêng `https://<name>.<tailnet>.ts.net` ở đường dẫn gốc, cổng 443. Nếu tên đã bị dùng, hệ thống thêm hậu tố số, nên hãy dùng URL mà tác tử nhận lại.
- **Mỗi ứng dụng mới cần một người.** Ở thiết lập mặc định, mỗi nút ứng dụng mới cần một người hoàn tất việc đăng ký với Tailscale. Nếu kết quả là `needs_login`, hãy đăng nhập xong rồi chạy `tslink url <name> --wait` để lấy URL chính xác. Ngoài ra, một tên mới có thể mất một lúc mới truy cập được: từ 36 đến 38 giây trong hai lần chạy thử của chúng tôi.
- **Ai mở được.** Mặc định là các thiết bị trong tailnet mà chính sách của bạn cho phép. `share` của MCP nhận `allow` để giới hạn ở tài khoản của bạn; từ CLI, dùng `tslink add web --proxy localhost:5173 --allow you@example.com`. Tên xuất hiện trong nhật ký chứng chỉ công khai nên không phải bí mật.
- **Sửa, lưu, thấy ngay.** Lưu mã sẽ làm trang trên điện thoại cập nhật. Quy tắc Host và Origin của chính ứng dụng vẫn có thể chặn yêu cầu: trong thử nghiệm của chúng tôi, Vite không cần đổi `allowedHosts`; Next.js chưa được thử và vẫn cần `allowedDevOrigins`.
- **Dọn dẹp.** Tác tử gọi `unshare`, hoặc bạn chạy `tslink remove <name>`, luôn dùng tên mà kết quả share trả về. Việc gỡ sẽ hủy đăng ký phần chia sẻ và cố xóa nút tailnet của nó khi TSLink chứng minh được mình sở hữu nút đó, nên hãy xem các trường dọn dẹp hoặc trạng thái được giữ lại trong kết quả. Nó không dừng máy chủ phát triển.
- **CLI hoặc MCP.** Các lệnh quản lý nhận `--json` và trả về kết quả có phiên bản; `tslink mcp` cung cấp các thao tác về ứng dụng và quyền truy cập qua MCP.
- **Một tệp, không phải cả thư mục.** Tác tử có thể chỉ chia sẻ báo cáo HTML tự chứa của nó bằng `tslink share ./report.html`; các tệp khác trong thư mục đó vẫn không truy cập được.
- **Vai trò giới hạn.** `viewer`, `app-operator` hoặc `people-manager`, giới hạn trong các ứng dụng bạn chỉ định; chia sẻ một ứng dụng mới cần vai trò `owner`. `tslink mcp-audit` cho biết tác tử đã thay đổi gì. Vai trò chỉ giới hạn công cụ của TSLink, không giới hạn shell riêng của tác tử.

[Hướng dẫn tác tử](agent-quickstart.md) · [Quyền MCP](mcp-scopes.md) · [MCP từ xa](remote-mcp.md)

<a id="why"></a>

## Khi nào bạn cần TSLink

Với một ứng dụng bạn tự khởi động và mở trên điện thoại của mình, Serve là đủ. TSLink dành cho các máy chủ phát triển do tác tử khởi động, thường là nhiều cái, khi mỗi cái cần một tên riêng và các bước chia sẻ, lấy URL và dọn dẹp được làm sẵn trong CLI, MCP và một skill của Claude Code.

| Việc cần làm | Chỉ Tailscale | TSLink |
|---|---|---|
| Nhiều máy chủ phát triển do tác tử của bạn khởi động, mỗi cái một tên | Serve đặt các tuyến ở cấp thiết bị dưới một tên cho mỗi máy; Services yêu cầu với mỗi tên một định nghĩa, một bước phê duyệt và một máy chủ có gắn thẻ | Mỗi ứng dụng một nút, với việc chia sẻ, lấy URL và dọn dẹp được làm sẵn trong CLI (`share`, `url`, `remove`), MCP (`share`, `url`, `unshare`) và một skill |
| Một ứng dụng bạn tự khởi động, trên điện thoại | `tailscale serve 3000` là đủ | `tslink share 3000` |
| Một người, một ứng dụng, bảy ngày | Quy tắc chính sách, rồi dùng công cụ JIT hoặc tự gỡ | `tslink people add alice@example.com --apps photos --for 7d` (HTTP/tệp) |
| Liên kết trình duyệt, ba ngày | Funnel công khai; tự thêm lớp kiểm soát truy cập và lịch tắt | `tslink guest create photos --for 3d --public --print-link` (chỉ HTTP) |

Người nhận riêng tư cần có Tailscale. Liên kết khách là công khai, có thể chuyển tiếp và dùng làm thông tin xác thực để truy cập.

[So sánh đầy đủ](comparison.md#tailscale-alone-or-tslink)

<a id="use-cases"></a>

## Ứng dụng của bạn, trên thiết bị của bạn

- **Mỗi ứng dụng một địa chỉ.** Ứng dụng web, thư mục, tệp đơn lẻ và cổng TCP đều có tên riêng trong tailnet của bạn, nên bạn dùng tên thay vì địa chỉ IP.
- **Mặc định là riêng tư.** Không có gì công khai cho đến khi bạn tạo liên kết khách hoặc xuất bản qua Funnel.
- **Một ứng dụng, không phải cả máy.** Mỗi ứng dụng bạn xuất bản có nút riêng, chỉ chuyển tiếp tới ứng dụng đó. Nếu máy chủ không cài ứng dụng Tailscale, TSLink không thêm cổng nào khác của máy chủ vào tailnet của bạn.
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
