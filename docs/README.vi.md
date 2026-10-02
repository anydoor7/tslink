<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="Biểu trưng TSLink">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>Cấp địa chỉ riêng cho ứng dụng, mô hình và tệp trên máy của bạn.</strong><br>
  Truy cập từ một thiết bị khác được phép trong mạng Tailscale của bạn.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="Giấy phép: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 trở lên"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: các nút tsnet tích hợp"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 19 công cụ"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ko.md">한국어</a> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <strong>Tiếng Việt</strong>
</p>

<a id="installation"></a>

## Cài đặt

Bạn cần **Go 1.26.6 trở lên** và Git. Hiện chưa có bản phát hành biên dịch sẵn hay Homebrew cask, nên hãy cài từ mã nguồn. Các ví dụ dùng **bash hoặc zsh**; xem [hỗ trợ nền tảng](platforms.md) để biết yêu cầu của Windows và dịch vụ chạy nền. Hướng dẫn chi tiết được viết bằng tiếng Anh.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

Dùng tài khoản Tailscale đã [bật MagicDNS và HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates). Thiết bị truy cập phải đăng nhập vào mạng Tailscale của bạn (**tailnet**), và chính sách mạng phải cho phép kết nối tới dịch vụ. TSLink tích hợp Tailscale trên máy cung cấp dịch vụ.

### Chia sẻ trang đầu tiên

Tạo một trang; TSLink phục vụ trực tiếp và khởi động dịch vụ chạy nền khi cần:

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

Nếu TSLink in URL đăng ký nút, hãy mở URL đó để cấp quyền cho nút. Tailnet có thể còn yêu cầu quản trị viên phê duyệt thiết bị. Sau đó lấy địa chỉ chính xác:

```bash
tslink url demo --wait
```

Mở URL được trả về trên thiết bị được phép. Lần chia sẻ đầu tiên không cần API token. [Thiết lập đầy đủ và vòng đời dịch vụ →](getting-started.md)

<a id="use-cases"></a>

## Bạn muốn chia sẻ gì?

Các tệp phải có sẵn; ứng dụng, cơ sở dữ liệu và dịch vụ mô hình phải đang chạy trên các cổng được chỉ định.

| Nhu cầu | Lệnh |
|---|---|
| Mở ứng dụng trên máy từ thiết bị khác | `tslink share 3000` |
| Duyệt các tệp trong một thư mục | `tslink share ./public --name files` |
| Đọc báo cáo HTML đã tạo trên điện thoại | `tslink share ./report.html --name report` |
| Kết nối cơ sở dữ liệu trên máy qua TCP | `tslink add database --tcp localhost:5432` |
| Dùng HTTP API của mô hình trên máy, chẳng hạn Ollama | `tslink add model --proxy localhost:11434` |

Với Ollama, lấy URL chính xác bằng `tslink url model --wait`. `baseURL` của ứng dụng khách tương thích OpenAI API là URL đó thêm `/v1`. [Mô hình trên máy và quy trình xử lý dữ liệu riêng tư →](local-ai.md)

Với nhiều ứng dụng trên một máy chủ, TSLink kết hợp nút dịch vụ có tên, danh sách cho phép theo danh tính HTTP, hạn Funnel và quản lý MCP. [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve) có thể đủ cho một ứng dụng trên các thiết bị của bạn.

<a id="architecture"></a>

## Kiến trúc

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="Sơ đồ dịch vụ minh họa: App, Docs, Database và Model là các nút có tên riêng trong cùng một tailnet. Ứng dụng, tệp và API mô hình dùng HTTPS; cơ sở dữ liệu dùng TCP riêng tư." width="960">
</picture>

**Một tailnet, các nút dịch vụ riêng biệt.** Một daemon dùng chung chạy nút tsnet tích hợp cho từng dịch vụ để chuyển tiếp HTTP, phục vụ tệp hoặc làm proxy TCP. Thay đổi trong sổ đăng ký dịch vụ có hiệu lực khi daemon đang chạy. Mỗi nút có danh tính mạng riêng; các dịch vụ dùng chung máy chủ cung cấp. [Chi tiết kiến trúc →](architecture.md)

| Thành phần | Vai trò |
|---|---|
| [Go](../go.mod) | Chương trình dòng lệnh dạng nhị phân |
| [Tailscale tsnet](architecture.md) | Các nút dịch vụ và truyền tải qua tailnet |
| [Cobra](https://github.com/spf13/cobra) | Lệnh và trợ giúp |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | Kênh truyền tải cho agent |
| Kho khóa của hệ điều hành và trình quản lý dịch vụ người dùng | Lưu thông tin xác thực tùy chọn và chạy nền |

Dịch vụ chỉ nằm trong tailnet trừ khi bạn chủ động bật [Funnel công khai](getting-started.md#more-examples). Dịch vụ HTTP và tệp hỗ trợ danh sách cho phép theo danh tính (`WhoIs`, `--allow`); TCP dựa vào chính sách tailnet và cơ chế xác thực của chính dịch vụ đích. Xem [phạm vi chia sẻ](sharing.md).

TSLink không cài ứng dụng, không chạy mô hình, không cô lập tiến trình máy chủ và không tổng hợp nhiều máy chủ. Mạng, mã hóa và HTTPS do Tailscale cung cấp; TSLink là dự án độc lập.

<a id="agents"></a>

## Dành cho agent

**19 công cụ MCP** cho phép agent chia sẻ báo cáo, quản lý dịch vụ, lấy URL và kiểm tra thiết lập. Kết nối ứng dụng khách MCP trên máy với chương trình đã cài:

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

MCP quản lý TSLink; ứng dụng dùng HTTP API của mô hình để suy luận. Xem [ứng dụng khách MCP](mcp-clients.md), [MCP từ xa](remote-mcp.md) và [hướng dẫn vận hành cho agent](../AGENTS.md) để thiết lập và tự động hóa.

Tự động hóa CLI hỗ trợ `--json` với `schema_version` bằng `1`; xem `tslink status --urls --json`. MCP cục bộ dùng JSON-RPC qua stdio. Xem [tự động hóa JSON](json-automation.md).

<a id="roadmap"></a>

## Sắp có

Các mục Đang hợp nhất, Đang xét duyệt hoặc Dự kiến chưa có trong bản cài từ mã nguồn ở trên.

| Trường hợp sử dụng | Trạng thái |
|---|---|
| <!-- roadmap:people --> Cho người thân truy cập ứng dụng HTTP/tệp riêng tư trong 3 ngày và gộp lời mời vào một tin nhắn; người nhận vẫn cần Tailscale. | Đang hợp nhất |
| <!-- roadmap:health --> Kiểm tra sức khỏe ứng dụng và nhận cảnh báo ngừng hoạt động hoặc hết hạn qua lệnh hay webhook tùy chọn. | Đang hợp nhất |
| <!-- roadmap:recipes --> Tìm ứng dụng loopback được hỗ trợ và xem trước công thức cho ứng dụng tự lưu trữ trước khi chia sẻ. | Đang hợp nhất |
| <!-- roadmap:limits --> Đặt kích thước tải lên và thời gian chờ yêu cầu cho từng ứng dụng HTTP để hỗ trợ tệp lớn và máy khách chậm. | Đang hợp nhất |
| <!-- roadmap:windows --> Khởi động lại daemon Windows bị lỗi khi người dùng vẫn duy trì phiên đăng nhập, bằng tác vụ theo lịch và bộ giám sát tích hợp. | Đang hợp nhất |
| <!-- roadmap:access-log --> Xem ai mở ứng dụng nào trong nhật ký truy cập cục bộ, với chế độ đường dẫn `prefix`, `full` hoặc `off`. | Đang xét duyệt |
| <!-- roadmap:portal --> Mở một trang chủ liệt kê ứng dụng được phép và chuyển tiếp đăng ký cho chủ sở hữu; khách vẫn cần Tailscale. | Đang xét duyệt |
| <!-- roadmap:mcp-scopes --> Gán vai trò và phạm vi ứng dụng cho agent, kèm biên nhận kiểm toán các thay đổi. | Đang xét duyệt |
| <!-- roadmap:guest-links --> Cho khách mở một ứng dụng HTTP trong trình duyệt mà không cài Tailscale, bằng liên kết có hạn và PIN tùy chọn qua Funnel công khai có kiểm soát truy cập. | Đang xét duyệt |
| <!-- roadmap:durations --> Chọn thời hạn đặt sẵn hoặc tùy chỉnh ít nhất 1 giờ, với tối đa mặc định 7 ngày cho khách và có thể cấu hình. | Đang xét duyệt |
| <!-- roadmap:requests --> Giúp người dùng điện thoại tham gia bằng mã QR; cho phép chủ sở hữu duyệt yêu cầu truy cập ứng dụng hoặc thêm thời gian trong một thao tác. | Đang xét duyệt |
| <!-- roadmap:multi-host --> Xem ứng dụng từ nhiều máy chủ trong một danh sách. | Dự kiến |

<a id="documentation"></a>

## Tài liệu và giấy phép

[Bắt đầu](getting-started.md) · [Mô hình trên máy](local-ai.md) · [Tham chiếu CLI](cli-reference.md) · [Nền tảng](platforms.md) · [Lộ trình](roadmap.md)

Tham khảo [CONTRIBUTING.md](../CONTRIBUTING.md) để đóng góp; báo cáo lỗ hổng qua kênh trong [SECURITY.md](../SECURITY.md).

TSLink dùng [Apache License 2.0](../LICENSE) không sửa đổi, cho phép sử dụng thương mại theo giấy phép này. Khi phân phối lại, hãy giữ [NOTICE](../NOTICE) và [thông báo của bên thứ ba](../THIRD_PARTY_NOTICES.md) áp dụng. [Hợp tác thương mại](../COMMERCIAL.md) là tự nguyện và không thêm điều kiện giấy phép. Điều khoản và gói dịch vụ của Tailscale áp dụng riêng.
