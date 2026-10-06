<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink logo">
  </picture>
</p>
<h1 align="center">TSLink</h1>
<p align="center"><strong>어디서든 내 앱에 접속하고 관리하세요.<br>비공개로 사용하거나 원하는 조건으로 공유하세요.</strong></p>

내 컴퓨터나 클라우드 서버의 앱을 암호화된 사설 네트워크로 이용하세요. 필요하면 브라우저 게스트 링크나 공개 접속을 직접 선택할 수 있습니다. 사람이 직접 또는 에이전트를 통해 관리할 수 있습니다.

<p align="center"><a href="#quickstart">빠른 시작</a> · <a href="#agents">에이전트 안내</a> · <a href="#documentation">문서</a></p>
<p align="center">
<a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <strong>한국어</strong> · <a href="README.de.md">Deutsch</a> · <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a> · <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a> · <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a> · <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="use-cases"></a>

## 내 앱을 더 가까이

| 필요한 일 | TSLink의 기능 |
|---|---|
| 여러 기기에서 내 앱 사용 | PC나 서버의 홈 대시보드, 로컬 전용 웹 페이지, 파일, 모델 API, TCP 서비스에 비공개 주소를 제공합니다. |
| 지정한 사람에게 공유 | HTTP/파일 앱별로 만료와 철회를 설정하고 Tailscale 로그인 신원을 확인합니다. 받는 사람도 Tailscale이 필요합니다. [사용자 공유](people.md) |
| 브라우저로 방문 허용 | HTTP 프록시 앱에 만료되는 게스트 링크와 선택적 PIN을 제공하거나 Funnel 공개 HTTPS를 명시적으로 켭니다. 링크는 전달할 수 있으며 방문자의 신원을 증명하지 않습니다. [게스트 링크](guest-links.md) |
| 여러 앱을 꾸준히 관리 | 호스트별 앱 목록, 비공개 포털, 상태 점검과 알림, 접속 이력, CLI/MCP 권한 관리. 에이전트 역할, 앱 범위 제한, 감사 기록도 지원합니다. [포털](portal.md) · [MCP 권한](mcp-scopes.md) |

[앱 구성 레시피](apps.md), [업로드 제한](sharing.md), [유연한 유효 기간](durations.md), [QR 안내와 접근 요청](requests.md)은 일상적인 관리를 돕습니다. 모두 현재 소스에 포함되어 있습니다.

<a id="installation"></a>
<a id="quickstart"></a>

## 빠른 시작

**Git과 Go 1.26.6+**로 소스에서 설치합니다. 사전 빌드 릴리스와 Homebrew는 아직 배포되지 않았습니다. 아래 명령은 bash/zsh용입니다. [macOS, Linux, Windows 설정](platforms.md)

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

**Tailscale 계정**과 [MagicDNS와 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)가 필요합니다. 비공개 접속 기기는 Tailscale과 네트워크 정책의 허용이 필요합니다. 앱 호스트에는 TSLink가 Tailscale을 내장합니다.

앱이 이미 3000 포트에서 실행 중이라면:

```bash
tslink share 3000 --name myapp
tslink url myapp --wait
```

사용하지 않는 이름을 지정하세요. `share`가 다른 이름을 반환하면 `url`에도 그 이름을 사용하세요. 표시된 브라우저 등록과 기기 승인을 먼저 완료하고, 허용된 기기에서 정확한 앱 URL을 여세요. `share`는 필요할 때 백그라운드 서비스를 시작합니다. 첫 비공개 사용에는 관리자 API 토큰이 필요 없습니다. 파일은 `tslink share ./report.html`로 공유합니다. 앱은 실행 중이어야 하고 파일은 존재해야 합니다. [전체 설정](getting-started.md)

잘 사용하고 있다면 [TSLink에 Star](https://github.com/anydoor7/tslink)를 남겨 다른 사람이 발견하도록 도와주세요. 전적으로 선택 사항입니다.

<a id="architecture"></a>

## 동작 방식

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="PC 또는 클라우드 호스트 하나에서 CLI/MCP가 공용 데몬과 앱별 노드를 관리합니다. 비공개 기기는 Tailscale 암호화 경로로, 선택적 공개 HTTPS/Funnel은 게스트 인증 또는 명시적 공개 설정을 통해 HTTP 앱에 연결됩니다." width="960">
</picture>

앱으로 이어지는 암호화된 사설 경로라고 생각하세요. **Tailscale은 네트워크 전송과 HTTPS를, TSLink는 호스트별 앱 접근 관리를 담당합니다.** 하나의 데몬이 서비스마다 별도의 내장 노드를 실행합니다. 비공개 포털에는 허용된 앱을 표시하며 상태와 접속 이력으로 유지 관리를 돕습니다.

공개 접속은 직접 켜야 합니다. 게스트는 링크와 선택적 PIN이 필요하며, 개방형 Funnel은 URL을 가진 누구나 접속할 수 있습니다. 두 방식 모두 공개 HTTPS를 사용하며 비공개 사용자 신원 인증과 다릅니다. 원시 TCP는 비공개로 유지되고 tailnet 정책과 백엔드 인증에 의존합니다. TSLink는 앱 설치, 호스트 프로세스 격리, 클라우드 VPC 생성, 다중 호스트 통합을 하지 않습니다. Tailscale과 함께 쓰는 독립 프로젝트입니다. [구조와 경계](architecture.md)

<a id="agents"></a>

## 에이전트 안내

CLI/MCP로 목록, 상태, URL, 접근 권한을 관리하세요. [에이전트 빠른 시작](agent-quickstart.md)을 읽고 실제 도구 스키마를 확인한 뒤 앱 접속을 검증해야 성공으로 보고할 수 있습니다.

```json
{"mcpServers":{"tslink":{"command":"tslink","args":["mcp"]}}}
```

CLI 자동화는 `--json`, MCP는 stdio 기반 JSON-RPC를 사용합니다. [클라이언트 설정](mcp-clients.md) · [원격 MCP](remote-mcp.md) · [역할과 앱 범위](mcp-scopes.md)

<a id="roadmap"></a>
<a id="documentation"></a>

## 문서와 라이선스

[전체 가이드](INDEX.md) · [CLI 참조](cli-reference.md) · [로컬 AI](local-ai.md) · [상태 점검](health-and-alerts.md) · [접속 이력](access-log.md) · [로드맵](roadmap.md)

다중 호스트 목록은 계획 중입니다. [기여](../CONTRIBUTING.md)와 [보안 제보](../SECURITY.md)를 환영합니다. [Apache 2.0](../LICENSE)은 상업적 사용을 허용합니다. 재배포 시 [NOTICE](../NOTICE)와 [타사 고지](../THIRD_PARTY_NOTICES.md)를 유지하세요. [상업적 협력](../COMMERCIAL.md)은 자발적입니다. Tailscale 약관과 요금제는 별도로 적용됩니다.
