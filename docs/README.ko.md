<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 로고">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>로컬 앱, 모델, 파일에 각각 전용 비공개 주소를 부여하세요.</strong><br>
  Tailscale 네트워크의 다른 허용된 기기에서 접근할 수 있습니다.
</p>

<p align="center">
  <a href="../LICENSE"><img src="assets/badge-license.svg" alt="라이선스: Apache 2.0"></a>
  <a href="../go.mod"><img src="assets/badge-go.svg" alt="Go 1.26.6 이상"></a>
  <a href="architecture.md"><img src="assets/badge-tsnet.svg" alt="Tailscale: 내장 tsnet 노드"></a>
  <a href="#agents"><img src="assets/badge-mcp.svg" alt="MCP: 도구 19개"></a>
</p>

<p align="center">
  <a href="../README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <strong>한국어</strong> · <a href="README.de.md">Deutsch</a><br>
  <a href="README.es.md">Español</a> · <a href="README.fr.md">Français</a> · <a href="README.it.md">Italiano</a> · <a href="README.da.md">Dansk</a> · <a href="README.ja.md">日本語</a><br>
  <a href="README.pl.md">Polski</a> · <a href="README.ru.md">Русский</a> · <a href="README.bs.md">Bosanski</a> · <a href="README.ar.md">العربية</a> · <a href="README.no.md">Norsk</a><br>
  <a href="README.pt-BR.md">Português (Brasil)</a> · <a href="README.th.md">ไทย</a> · <a href="README.tr.md">Türkçe</a> · <a href="README.uk.md">Українська</a><br>
  <a href="README.bn.md">বাংলা</a> · <a href="README.el.md">Ελληνικά</a> · <a href="README.vi.md">Tiếng Việt</a>
</p>

<a id="installation"></a>

## 설치

**Go 1.26.6 이상**과 Git이 필요합니다. 미리 빌드된 릴리스와 Homebrew cask는 아직 공개되지 않았으므로 소스에서 설치합니다. 예제는 **bash 또는 zsh**를 사용합니다. Windows와 백그라운드 서비스 요구 사항은 [플랫폼 지원](platforms.md)을 확인하세요. 상세 안내 문서는 영어로 제공됩니다.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

[MagicDNS와 HTTPS가 활성화된](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale 계정을 사용하세요. 접속할 기기는 자신의 Tailscale 네트워크(**tailnet**)에 로그인되어 있어야 하며, 네트워크 정책에서 서비스 접근을 허용해야 합니다. 서비스를 제공하는 호스트에는 TSLink가 Tailscale을 내장합니다.

### 첫 페이지 공유하기

페이지를 만들면 TSLink가 직접 제공하고, 필요할 때 백그라운드 서비스를 시작합니다.

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

노드 등록 URL이 표시되면 열어서 승인하세요. tailnet에 따라 관리자의 기기 승인도 필요할 수 있습니다. 이어서 정확한 주소를 가져옵니다.

```bash
tslink url demo --wait
```

허용된 기기에서 반환된 URL을 여세요. 첫 공유에는 API 토큰이 필요하지 않습니다. [전체 설정과 수명 주기 안내 →](getting-started.md)

<a id="use-cases"></a>

## 무엇을 공유할까요?

파일은 미리 준비해야 합니다. 앱, 데이터베이스, 모델 백엔드는 지정한 포트에서 이미 실행 중이어야 합니다.

| 사용 사례 | 명령 |
|---|---|
| 다른 기기에서 로컬 앱 열기 | `tslink share 3000` |
| 디렉터리의 파일 탐색하기 | `tslink share ./public --name files` |
| 생성된 HTML 보고서를 휴대전화에서 읽기 | `tslink share ./report.html --name report` |
| TCP로 로컬 데이터베이스에 접속하기 | `tslink add database --tcp localhost:5432` |
| Ollama 등의 로컬 모델 HTTP API 사용하기 | `tslink add model --proxy localhost:11434` |

Ollama는 `tslink url model --wait`로 정확한 URL을 가져옵니다. OpenAI API 호환 클라이언트의 `baseURL`에는 해당 URL 뒤에 `/v1`을 붙입니다. [로컬 모델과 비공개 데이터 작업 흐름 →](local-ai.md)

한 호스트의 여러 앱을 관리할 때 TSLink는 이름이 있는 서비스 노드, HTTP 신원 허용 목록, Funnel 만료와 MCP 관리를 함께 제공합니다. 자신의 기기에서 앱 하나만 사용한다면 [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)로 충분할 수 있습니다.

<a id="architecture"></a>

## 아키텍처

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="서비스 구성 예: App, Docs, Database, Model은 하나의 tailnet 안에 있는 서로 다른 이름의 노드입니다. 앱, 파일, 모델 API는 HTTPS를, 데이터베이스는 비공개 TCP를 사용합니다." width="960">
</picture>

**하나의 tailnet, 서비스별 노드.** 공통 데몬이 서비스마다 내장 tsnet 노드를 실행하며 HTTP 전달, 파일 제공, TCP 프록시를 담당합니다. 레지스트리 변경 사항은 실행 중에 반영됩니다. 각 노드는 독립적인 네트워크 식별 정보를 가지며, 서비스들은 같은 호스트에서 실행됩니다. [아키텍처 자세히 보기 →](architecture.md)

| 구성 요소 | 역할 |
|---|---|
| [Go](../go.mod) | 네이티브 명령줄 프로그램 |
| [Tailscale tsnet](architecture.md) | 서비스 노드와 tailnet 통신 |
| [Cobra](https://github.com/spf13/cobra) | 명령과 도움말 |
| [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) | 에이전트 전송 계층 |
| OS 키체인과 사용자 서비스 관리자 | 선택적 자격 증명 저장과 백그라운드 실행 |

[공개 Funnel](getting-started.md#more-examples)을 명시적으로 활성화하지 않는 한 서비스는 tailnet 내부에만 제공됩니다. HTTP 및 파일 서비스는 신원 기반 허용 목록을 지원합니다 (`WhoIs`, `--allow`). TCP는 tailnet 정책과 백엔드 자체 인증에 의존합니다. [공유 범위](sharing.md)를 확인하세요.

TSLink는 앱 설치, 모델 실행, 호스트 프로세스 격리나 여러 호스트 집계를 수행하지 않습니다. 네트워크, 암호화와 HTTPS는 Tailscale이 제공합니다. TSLink는 독립 프로젝트입니다.

<a id="agents"></a>

## 에이전트에서 사용하기

**MCP 도구 19개**로 에이전트가 보고서를 공유하고, 서비스를 관리하며, URL을 가져오고 설정을 확인할 수 있습니다. 로컬 MCP 클라이언트를 설치된 프로그램에 연결하세요.

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

MCP는 TSLink를 관리하고, 앱의 추론에는 모델 HTTP API를 사용합니다. 설정과 자동화는 [MCP 클라이언트](mcp-clients.md), [원격 MCP](remote-mcp.md), [에이전트 운영 안내](../AGENTS.md)를 확인하세요.

CLI 자동화는 `--json`을 지원하며 `schema_version`은 `1`입니다. `tslink status --urls --json`으로 확인하세요. 로컬 MCP는 stdio의 JSON-RPC를 사용합니다. [JSON 자동화](json-automation.md)를 참고하세요.

<a id="roadmap"></a>

## 예정된 기능

병합 중, 검토 중 또는 계획됨으로 표시된 항목은 위 소스 설치에 포함되지 않습니다.

| 사용 사례 | 상태 |
|---|---|
| F1. 친척에게 비공개 HTTP/파일 앱 접근을 3일 동안 허용하고 앱 초대를 한 메시지로 묶습니다. 받는 사람에게는 Tailscale이 필요합니다. | 병합 중 |
| F2. 앱 상태를 확인하고 선택한 명령이나 webhook으로 장애 또는 만료 알림을 받습니다. | 병합 중 |
| F3. 지원되는 루프백 앱을 찾고 공유 전에 자체 호스팅 앱 레시피를 미리 봅니다. | 병합 중 |
| F8. 큰 업로드와 느린 클라이언트에 맞춰 HTTP 앱별 업로드 크기와 요청 시간 제한을 설정합니다. | 병합 중 |
| F9. Windows에 로그인한 동안 예약 작업과 내장 감독 프로세스로 충돌한 데몬을 다시 시작합니다. | 병합 중 |
| F4. 로컬 접근 로그에서 누가 어떤 앱을 열었는지 확인하고 경로 기록을 `prefix`, `full` 또는 `off`로 선택합니다. | 검토 중 |
| F5. 허용된 앱을 하나의 홈 페이지에 표시하고 소유자에게 노드 등록 인계 정보를 제공합니다. 방문자에게는 Tailscale이 필요합니다. | 검토 중 |
| F6. 에이전트에 역할과 앱 범위를 지정하고 변경 작업의 감사 기록을 남깁니다. | 검토 중 |
| F10. 접근을 검사하는 공개 Funnel을 통해 게스트가 만료 링크와 선택적 PIN으로 Tailscale 설치 없이 브라우저에서 HTTP 앱 하나를 열게 합니다. | 검토 중 |
| F11. 프리셋이나 사용자 지정 기간을 선택합니다. 최소 1시간이며 게스트 최대 기간은 기본 7일이고 변경할 수 있습니다. | 검토 중 |
| F12. 휴대전화 사용자가 QR 코드로 참여하고 앱 접근이나 시간 연장 요청을 한 번에 승인하게 합니다. | 검토 중 |
| F7. 여러 호스트의 앱을 하나의 목록에서 봅니다. | 계획됨 |

<a id="documentation"></a>

## 문서와 라이선스

[시작하기](getting-started.md) · [로컬 모델](local-ai.md) · [CLI 참조](cli-reference.md) · [플랫폼](platforms.md) · [로드맵](roadmap.md)

기여 방법은 [CONTRIBUTING.md](../CONTRIBUTING.md)를 확인하세요. 취약점은 [SECURITY.md](../SECURITY.md)의 안내에 따라 신고하세요.

TSLink는 수정하지 않은 [Apache License 2.0](../LICENSE)을 적용하며, 해당 라이선스에 따라 상업적으로 사용할 수 있습니다. 재배포할 때는 적용되는 [NOTICE](../NOTICE)와 [제삼자 고지](../THIRD_PARTY_NOTICES.md)를 유지하세요. [상업적 협업](../COMMERCIAL.md)은 선택 사항이며 라이선스 조건을 추가하지 않습니다. Tailscale의 서비스 약관과 요금제는 별도로 적용됩니다.
