<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>Cấp cho mỗi ứng dụng trên máy tính hoặc máy chủ của bạn một địa chỉ riêng trong mạng Tailscale của bạn, và tự quyết định ai được truy cập.</strong></p>

Mở ứng dụng web, thư mục, API mô hình và cơ sở dữ liệu từ chính điện thoại và laptop của bạn, kèm kiểm tra tình trạng và lịch sử truy cập cho từng ứng dụng. Tác tử AI của bạn có thể gán cho các ứng dụng mà chúng khởi động trên localhost một địa chỉ riêng tư để các thiết bị khác của bạn mở được, và kiểm tra các ứng dụng đó, trong phạm vi vai trò bạn giao. Khi người khác cần vào, hãy cấp quyền cho một người cụ thể đến một ngày nhất định, hoặc mở một ứng dụng web ra internet công khai trong thời gian giới hạn.

**Cần Tailscale.** Bạn cần một tài khoản Tailscale (miễn phí cho cá nhân), và mỗi thiết bị mở ứng dụng riêng tư cần có ứng dụng Tailscale; khách và người truy cập công khai chỉ cần trình duyệt. TSLink là dự án độc lập, không do Tailscale làm ra hay xác nhận. [Yêu cầu](#requirements)

<p align="center"><a href="#quickstart">Bắt đầu nhanh</a> · <a href="#agents">Dành cho tác tử</a> · <a href="comparison.md">So sánh với Serve, ngrok và Cloudflare</a> · <a href="#documentation">Tài liệu</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <strong>Tiếng Việt</strong>
</p>

<a id="use-cases"></a>

## Bạn có thể làm gì

### Truy cập ứng dụng của chính bạn

- **Mỗi ứng dụng một địa chỉ.** `tslink share 3000`, `tslink share ./photos` hoặc `tslink add db --tcp localhost:5432` cấp cho một ứng dụng web, thư mục, tệp hoặc dịch vụ TCP một địa chỉ riêng tư trong tailnet của bạn (mạng Tailscale riêng của bạn), ví dụ `https://photos.<tailnet>.ts.net`. Mỗi ứng dụng là một thiết bị Tailscale riêng, nên bạn mở ứng dụng bằng tên thay vì địa chỉ IP và số cổng.
- **Riêng tư trừ khi bạn chọn khác.** TSLink mặc định giữ quyền truy cập ứng dụng ở chế độ riêng tư, và chính sách tailnet của bạn quyết định thiết bị nào được kết nối. TSLink chỉ mở một điểm truy cập công khai cho ứng dụng khi bạn tạo liên kết khách hoặc chủ động công bố qua Funnel.
- **Xem tất cả ở một nơi.** `tslink status --urls` liệt kê các ứng dụng đã đăng ký trên máy này, và một trang chủ riêng tư tùy chọn hiển thị địa chỉ và tình trạng của chúng. [Cổng](portal.md)
- **Biết ngay khi có sự cố.** Kiểm tra tình trạng chạy nền có thể báo cho bạn qua một lệnh hoặc webhook khi ứng dụng ngừng hoạt động hay chạy lại, hoặc khi phiên đăng nhập Tailscale của nó sắp hết hạn. Lịch sử truy cập cho thấy ai đã mở ứng dụng nào và khi nào, kể cả các yêu cầu bị từ chối. [Tình trạng và cảnh báo](health-and-alerts.md) · [Lịch sử truy cập](access-log.md)
- **Công thức ứng dụng.** Có công thức cho 15 ứng dụng tự lưu trữ, gồm Home Assistant, Jellyfin, Immich và Ollama, và `tslink apps detect` có thể tìm các ứng dụng được hỗ trợ đang lắng nghe sẵn trên máy. Để tải lên ảnh và video lớn, hãy [nâng giới hạn tải lên của từng ứng dụng](sharing.md). [Công thức cấu hình](apps.md) · [AI cục bộ](local-ai.md)

### Để tác tử làm việc với chúng

Khi tác tử khởi động máy chủ phát triển, bản xem trước hay API mô hình cục bộ trên `localhost`, điện thoại và các máy tính khác của bạn không truy cập được địa chỉ đó. TSLink cho phép tác tử gán cho nó một địa chỉ riêng tư, báo cho bạn URL chính xác và gỡ đăng ký sau đó, trong giới hạn bạn đặt ra.

- **Chia sẻ, kiểm tra, hoàn tác.** `share --json` trả về tên đã đăng ký, kèm URL chính xác hoặc một liên kết đăng nhập để bạn mở. `url <name> --wait` và `status --urls --name <name>` báo điểm truy cập đã sẵn sàng hay chưa, còn `remove <name>` (trong MCP là `unshare`) gỡ bỏ lượt chia sẻ. [Hướng dẫn tác tử](agent-quickstart.md)
- **Làm ra cho tự động hóa.** Các lệnh quản lý, trừ `tslink mcp`, nhận `--json` và trả về kết quả có phiên bản cùng mã lỗi ổn định. `tslink mcp` cung cấp các công cụ về ứng dụng và quyền truy cập cho một ứng dụng khách MCP cục bộ qua JSON-RPC. Khi đã cấu hình liên kết bên gọi, `tslink serve --mcp` cung cấp các công cụ đó cho ứng dụng khách MCP trên các thiết bị khác của bạn qua tailnet. [Tự động hóa JSON](json-automation.md) · [MCP từ xa](remote-mcp.md)
- **Quyền hạn có giới hạn.** Tác tử cục bộ mặc định có quyền của chủ sở hữu. Hãy giao cho tác tử một vai trò thu hẹp (`viewer`, `app-operator` hoặc `people-manager`), chỉ áp dụng cho những ứng dụng bạn chỉ định và giới hạn thời hạn tối đa của mọi quyền mà tác tử cấp. Các thay đổi thực hiện qua MCP đều được ghi lại, và `tslink mcp-audit` hiển thị chúng. Vai trò chỉ giới hạn công cụ của TSLink, không giới hạn shell hay tệp của chính tác tử. [Quyền MCP](mcp-scopes.md)

### Chia sẻ với người bạn chọn

- **Người cụ thể, đến một ngày nhất định.** `tslink people add alice@example.com --apps photos,notes --for 7d` cho phép tài khoản Tailscale đó mở các ứng dụng web và tệp này đến hạn. `people update`, `extend` và `people remove` dùng để thay đổi hoặc chấm dứt quyền; sau khi gỡ, yêu cầu tiếp theo của người đó bị từ chối, nhưng những gì họ đã tải về thì không thu hồi được. [Người dùng](people.md) · [Thời hạn](durations.md)
- **Người ở ngoài tailnet của bạn.** Thêm `--invite --print-links` để nhận một tin nhắn soạn sẵn, gồm lời mời thiết bị cho từng ứng dụng (cần API token thuộc sở hữu của người dùng). `--qr` in ra mã để thiết lập trên điện thoại.
- **Yêu cầu.** Người trong tailnet của bạn có thể xin thêm thời gian, hoặc xin quyền vào một ứng dụng bạn đánh dấu là cho phép yêu cầu, ngay từ trang chủ. Bạn duyệt bằng một lệnh kèm thời hạn. [Yêu cầu truy cập](requests.md)

### Mở ứng dụng web ra internet trong một thời gian

- **Liên kết khách.** `tslink guest create photos --for 3d --public --print-link` tạo một liên kết trình duyệt tới một ứng dụng web, có thể kèm PIN, và bạn có thể thu hồi riêng từng liên kết. Khách không cần tài khoản Tailscale. Ai có liên kết cũng dùng được, nên nó không chứng minh được ai đã truy cập. [Liên kết khách](guest-links.md)
- **URL công khai mở.** `tslink add preview --proxy localhost:3000 --funnel --public` công bố một ứng dụng web cho bất kỳ ai có URL. Một lần công bố mới mặc định kéo dài 24 giờ; dùng `--funnel-ttl` để chọn thời hạn khác. [Funnel](funnel.md)
- Liên kết khách mới và các lượt công bố công khai mới đều dùng Tailscale Funnel và có thời hạn hữu hạn (tối thiểu 1 giờ, tối đa mặc định 7 ngày, chủ sở hữu có thể thay đổi). Các đường công khai này hỗ trợ ứng dụng proxy HTTP; dịch vụ thư mục hay tệp phục vụ trực tiếp và TCP thô vẫn giữ riêng tư.

Mọi thứ ở trên đều có trong v0.1.0.

<a id="requirements"></a>

## Yêu cầu

TSLink được xây dựng trên Tailscale. Đây là dự án độc lập, không do Tailscale làm ra hay xác nhận, và các điều khoản cùng [gói dịch vụ](https://tailscale.com/pricing) của Tailscale vẫn áp dụng.

| Ai | Cần gì |
|---|---|
| Bạn | Một tài khoản Tailscale đã bật [MagicDNS và HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Gói Personal miễn phí dành cho mục đích phi thương mại. |
| Máy tính hoặc máy chủ chạy ứng dụng của bạn | Chỉ cần TSLink. TSLink đã tích hợp sẵn Tailscale nên không phải cài Tailscale riêng. Với thiết lập mặc định, mỗi nút ứng dụng mới cần đăng nhập qua trình duyệt và có thể cần phê duyệt thiết bị. [Thông tin xác thực đã lưu](credentials-and-tags.md) cho phép đăng ký mà không phải đăng nhập qua trình duyệt cho từng ứng dụng. |
| Các thiết bị khác của bạn | Ứng dụng Tailscale, đã đăng nhập vào tailnet của bạn. |
| Người bạn chọn | Ứng dụng Tailscale và tài khoản đăng nhập của chính họ. Họ hoặc tham gia tailnet của bạn, tức là thêm một người dùng vào gói của bạn, hoặc chấp nhận lời mời thiết bị cho từng ứng dụng. Chính sách tailnet của bạn phải cho phép họ truy cập. |
| Khách và người truy cập công khai | Một trình duyệt. Tailnet của bạn phải cho phép Funnel, tính năng mà Tailscale vẫn xếp vào bản beta. |

Khi chứng chỉ HTTPS được cấp cho một ứng dụng, tên thiết bị Tailscale của ứng dụng đó và tên DNS tailnet của bạn sẽ xuất hiện trong một nhật ký chứng chỉ công khai. Hãy chọn tên ứng dụng mà bạn không ngại người khác nhìn thấy.

<a id="installation"></a>
<a id="quickstart"></a>

## Bắt đầu nhanh

Trên macOS hoặc Linux, hãy cài bằng Homebrew. Tệp nhị phân macOS được ký bằng chứng chỉ Developer ID và đã được Apple công chứng (notarize). Khi cần nâng cấp, chạy `brew upgrade --cask tslink`, rồi chạy lại `tslink install` nếu TSLink đang chạy như một dịch vụ nền.

```bash
brew install --cask anydoor7/tap/tslink
```

Trên Windows, tải `tslink_<version>_windows_<arch>.zip` từ [bản phát hành mới nhất](https://github.com/anydoor7/tslink/releases/latest), đối chiếu với `checksums.txt`, rồi chạy `tslink install` để TSLink khởi động khi bạn đăng nhập. Tệp zip không có chữ ký Authenticode; hãy [xác minh bản phát hành](verify-release.md) qua checksum đã ký và các chứng thực bản dựng. Gói `.deb` và `.rpm` cho Linux có trên cùng trang phát hành. Để dựng từ mã nguồn, bạn cần **Git và Go 1.26.6+**. Các lệnh dưới đây dùng bash/zsh; xem [Thiết lập macOS, Linux và Windows](platforms.md).

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Bạn cần **tài khoản Tailscale** và [MagicDNS và HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Thiết bị truy cập riêng cần Tailscale và quyền theo chính sách mạng. TSLink nhúng Tailscale trên máy chạy ứng dụng.

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
