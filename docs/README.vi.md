<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Biểu trưng TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Chia sẻ ứng dụng trên máy tính với người bạn chọn, trong khoảng thời gian bạn muốn.</strong><br>
  Mỗi ứng dụng có địa chỉ riêng trong mạng Tailscale của bạn. Xem ai có quyền truy cập và thu hồi quyền đó.
</p>

<p align="center">
  <a href="#quickstart">Bắt đầu nhanh</a> · <a href="#agents">Dành cho tác tử</a> · <a href="getting-started.md">Tài liệu</a> ·
  <strong>Tiếng Việt</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">Tất cả ngôn ngữ</a>
</p>

## Mọi người dùng để làm gì

- **Mở thành quả công việc trên điện thoại.** Báo cáo do tập lệnh tạo, máy chủ phát triển, sổ tay hoặc API mô hình cục bộ đều có thể truy cập từ thiết bị được phép qua địa chỉ HTTPS riêng.
- **Cho một người dùng một ứng dụng trong một thời gian.** Cho người bạn đời dùng thư viện ảnh một tuần, hoặc đồng nghiệp thử bản xem trước ba ngày. Quyền truy cập tự hết hạn; bạn cũng có thể kết thúc sớm.
- **Để tác tử lo việc chia sẻ.** Tác tử lập trình vừa tạo một bảng điều khiển. Hãy yêu cầu chia sẻ cho bạn và đồng đội đến thứ Sáu. Nó cũng có thể cho biết những gì đang được chia sẻ và thu hồi lượt chia sẻ.

Ứng dụng tiếp tục chạy tại nơi vốn đang chạy. TSLink quản lý ai được truy cập từng ứng dụng và giữ một danh sách về nội dung được chia sẻ, người nhận và thời hạn.

<a id="quickstart"></a>

## Bắt đầu nhanh

Bạn cần **Go 1.26.6+**, Git và tài khoản Tailscale đã [bật MagicDNS và HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Chưa có bản phát hành biên dịch sẵn, nên hãy cài từ mã nguồn:

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Chia sẻ một trang:

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

Lần đầu, TSLink in liên kết đăng nhập để đăng ký nút dịch vụ mới; tailnet của bạn có thể còn yêu cầu quản trị viên duyệt thiết bị. Sau khi đăng ký, mở URL dịch vụ trên thiết bị được phép đã đăng nhập tailnet của bạn. Không cần mã thông báo API.

Kiểm tra nội dung đang chia sẻ rồi xóa bản thử:

```bash
tslink status --urls
tslink remove demo
```

Bạn còn có thể chia sẻ các nội dung sau khi phần phụ trợ đã chạy:

| Nội dung | Lệnh |
|---|---|
| Ứng dụng web cục bộ | `tslink share 3000` |
| Thư mục tệp | `tslink share ./public --name files` |
| API mô hình cục bộ như Ollama | `tslink add model --proxy localhost:11434` |
| Cơ sở dữ liệu qua TCP riêng | `tslink add database --tcp localhost:5432` |
| Ứng dụng tự lưu trữ được hỗ trợ (Jellyfin, Immich, Home Assistant và 13 ứng dụng khác) | `tslink apps detect`, rồi `tslink apps share jellyfin --yes` |

[Bắt đầu, nền tảng và dịch vụ nền →](getting-started.md)

## Chọn ai được mở

| Đối tượng | Người nhận cần gì | Danh tính | Kết thúc |
|---|---|---|---|
| **Thiết bị của bạn** | Đăng nhập tailnet của bạn | Danh tính Tailscale đã xác minh | Khi bạn xóa ứng dụng |
| **Người được chỉ định** (HTTP/tệp riêng) | Tài khoản Tailscale; người ngoài chấp nhận một lời mời cho mỗi ứng dụng | Danh tính Tailscale đã xác minh | Đến hạn bạn đặt (`--for 7d`) hoặc dùng `tslink people remove` |
| **Bất kỳ ai có URL** (Funnel) | Trình duyệt | Bất kỳ ai; yêu cầu đăng nhập của ứng dụng vẫn áp dụng | Mặc định sau 24 giờ (`--funnel-ttl`) |
| **Liên kết khách qua trình duyệt** *(sắp có)* | Trình duyệt và PIN tùy chọn | Người giữ liên kết | Đến hạn riêng hoặc khi bị thu hồi |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

Với HTTP và tệp riêng, thời hạn được kiểm tra ở mỗi yêu cầu. Thu hồi quyền chặn yêu cầu mới; không lấy lại dữ liệu đã tải hay đóng luồng và kết nối WebSocket đã được chấp nhận. [Chia sẻ với từng người →](people.md) · [Giới hạn chia sẻ →](sharing.md)

<a id="agents"></a>

## Dành cho tác tử

TSLink có máy chủ MCP để tác tử chia sẻ, liệt kê, giải thích và xóa lượt chia sẻ như bạn. Thêm vào một máy khách MCP cục bộ:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **Kết quả chính xác.** Tự động hóa CLI hỗ trợ `--json` với `schema_version: 1` và mã lỗi ổn định; `tslink mcp` dùng JSON-RPC. `tslink manifest` mô tả mọi lệnh và cờ. Tác tử nên lấy URL thật bằng `tslink url <name> --wait` thay vì tự ghép.
- **Báo rõ trạng thái chờ.** Nút mới còn cần con người đăng nhập sẽ báo `needs_login`, không giả vờ đã sẵn sàng.
- **Thẩm quyền.** MCP cục bộ chạy với quyền người dùng của bạn. MCP từ xa cần bật chủ động, chỉ truy cập được trong tailnet và giới hạn theo tài khoản hoặc thẻ bạn liệt kê. Vai trò cho từng tác tử, phạm vi ứng dụng và biên nhận thao tác *sắp có*.

MCP của TSLink điều khiển chính TSLink. Nếu bạn xuất bản máy chủ MCP khác qua TSLink, máy chủ đó vẫn cần quyền công cụ riêng.
[Hướng dẫn tác tử →](agents.md) · [Máy khách MCP →](mcp-clients.md) · [MCP từ xa →](remote-mcp.md) · [Tự động hóa JSON →](json-automation.md)

## Khi nào nên dùng công cụ khác

| Nếu bạn muốn | Cân nhắc |
|---|---|
| Một dịch vụ cục bộ trên thiết bị của mình, dùng máy khách Tailscale đang chạy | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| Dịch vụ do quản trị viên quản lý với tên ổn định trên nhiều máy chủ | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| URL công khai cho webhook hoặc bản thử API mà không cần tài khoản Tailscale | [ngrok](https://ngrok.com/docs/start) hoặc [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| Cài và chạy ứng dụng tự lưu trữ, ngoài việc chia sẻ | [Umbrel](https://umbrel.com) hoặc [Coolify](https://coolify.io) |
| Nền tảng truy cập dựa trên danh tính cho toàn tổ chức | [Pangolin](https://github.com/fosrl/pangolin) hoặc [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

TSLink phù hợp khi một người chạy nhiều ứng dụng và muốn cấp quyền có thời hạn theo ứng dụng, theo người, để cả chủ sở hữu và tác tử đều kiểm tra được.

## Cách hoạt động

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database và Model là các nút có tên riêng trong cùng một tailnet, do một daemon TSLink chạy trên máy tính xuất bản dịch vụ." width="720">
</picture>

Một daemon nền chạy nút Tailscale nhúng cho mỗi ứng dụng, giúp từng ứng dụng có tên và địa chỉ riêng. Với HTTP và tệp riêng, `WhoIs` cùng quyền theo người hoặc quy tắc `--allow` kiểm soát truy cập; thời hạn được kiểm tra ở từng yêu cầu. TCP thuần dùng chính sách tailnet và xác thực của phần phụ trợ. Tailscale cung cấp truyền tải tailnet, mã hóa và chứng chỉ; TSLink là dự án độc lập. Mọi ứng dụng dùng chung máy tính xuất bản nên TSLink không cách ly chúng với nhau. [Kiến trúc →](architecture.md)

## Trạng thái

Đã có: địa chỉ riêng theo ứng dụng, người được chỉ định với thời hạn và gói lời mời, Funnel công khai có hết hạn, kiểm tra sức khỏe và cảnh báo, công thức ứng dụng tự lưu trữ, giới hạn yêu cầu theo ứng dụng, khởi động lại sau sự cố trên Windows, CLI và MCP.

Sắp có: liên kết khách qua trình duyệt, thời lượng linh hoạt, nhật ký truy cập, trang chủ ứng dụng, vai trò tác tử giới hạn phạm vi, hướng dẫn tham gia bằng QR và yêu cầu truy cập. Danh sách chung cho nhiều máy tính đang được lên kế hoạch. [Lộ trình →](roadmap.md)

## Tài liệu và giấy phép

[llms.txt](../llms.txt) · [Bắt đầu nhanh cho tác tử](agent-quickstart.md) · [Chọn công cụ chia sẻ](comparison.md)

[Bắt đầu](getting-started.md) · [Tham chiếu CLI](cli-reference.md) · [Nền tảng](platforms.md) · [Mô hình cục bộ](local-ai.md) · [Đóng góp](../CONTRIBUTING.md) · [Bảo mật](../SECURITY.md)

Apache License 2.0 cho phép cả sử dụng thương mại. Giữ [NOTICE](../NOTICE) và [thông báo của bên thứ ba](../THIRD_PARTY_NOTICES.md) khi phân phối lại. Điều khoản và gói dịch vụ của Tailscale áp dụng riêng.
