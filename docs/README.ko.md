<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/tslink-mark-dark.svg">
    <img src="assets/tslink-mark-light.svg" width="88" height="88" alt="TSLink 로고">
  </picture>
</p>

<h1 align="center">TSLink</h1>

<p align="center">
  <strong>컴퓨터의 앱을 원하는 사람에게, 원하는 기간만큼 공유하세요.</strong><br>
  각 앱은 Tailscale 네트워크에서 독립된 비공개 주소를 갖습니다. 누가 접근할 수 있는지 확인하고 권한을 회수하세요.
</p>

<p align="center">
  <a href="#quickstart">빠른 시작</a> · <a href="#agents">에이전트용</a> · <a href="getting-started.md">문서</a> ·
  <strong>한국어</strong> · <a href="../README.md">English</a> · <a href="INDEX.md#translated-homepages">모든 언어</a>
</p>

## 이렇게 사용합니다

- **휴대폰에서 작업 결과를 여세요.** 스크립트가 만든 보고서, 개발 서버, 노트북, 로컬 모델 API를 허용된 기기에서 비공개 HTTPS 주소로 이용할 수 있습니다.
- **한 사람에게 한 앱을 잠시 공유하세요.** 파트너에게 사진 라이브러리를 일주일 동안, 동료에게 미리 보기 앱을 사흘 동안 열어 주세요. 접근 권한은 자동으로 만료되며 더 일찍 종료할 수도 있습니다.
- **에이전트에게 공유를 맡기세요.** 코딩 에이전트가 방금 대시보드를 만들었다면, 금요일까지 나와 팀원에게 공유해 달라고 요청하세요. 현재 공유 중인 항목을 확인하거나 공유를 취소할 수도 있습니다.

앱은 원래 실행되던 곳에서 계속 실행됩니다. TSLink는 각 앱의 접근 대상을 관리하고, 무엇을 누구에게 언제까지 공유했는지 하나의 목록에 기록합니다.

<a id="quickstart"></a>

## 빠른 시작

**Go 1.26.6+**, Git, [MagicDNS와 HTTPS를 활성화한](https://tailscale.com/docs/how-to/set-up-https-certificates) Tailscale 계정이 필요합니다. 아직 빌드된 릴리스를 배포하지 않으므로 소스에서 설치하세요.

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink && go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

페이지를 공유하세요.

```bash
mkdir -p tslink-demo && printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink url demo --wait
```

처음에는 TSLink가 새 서비스 노드를 등록할 로그인 링크를 출력합니다. tailnet 설정에 따라 관리자의 기기 승인도 필요할 수 있습니다. 등록을 마친 뒤 해당 tailnet에 로그인한 허용 기기에서 서비스 URL을 여세요. API 토큰은 필요 없습니다.

공유 항목을 확인한 다음 데모를 제거하세요.

```bash
tslink status --urls
tslink remove demo
```

백엔드가 실행 중이라면 다음 항목도 공유할 수 있습니다.

| 공유 대상 | 명령 |
|---|---|
| 로컬 웹 앱 | `tslink share 3000` |
| 파일 폴더 | `tslink share ./public --name files` |
| Ollama 같은 로컬 모델 API | `tslink add model --proxy localhost:11434` |
| 비공개 TCP로 연결하는 데이터베이스 | `tslink add database --tcp localhost:5432` |
| 지원되는 자체 호스팅 앱(Jellyfin, Immich, Home Assistant 외 13개) | `tslink apps detect`, 이어서 `tslink apps share jellyfin --yes` |

[시작 안내, 플랫폼 및 백그라운드 서비스 →](getting-started.md)

## 접근할 사람을 선택하세요

| 대상 | 받는 사람에게 필요한 것 | 확인하는 신원 | 종료 시점 |
|---|---|---|---|
| **내 기기** | 내 tailnet에 로그인 | 검증된 Tailscale 로그인 신원 | 앱을 제거할 때 |
| **지정한 사람**(비공개 HTTP/파일) | Tailscale 계정, 외부인은 앱마다 초대 수락 | 검증된 Tailscale 로그인 신원 | 설정한 기한(`--for 7d`) 또는 `tslink people remove` 실행 시 |
| **URL을 가진 누구나**(Funnel) | 브라우저 | 누구나, 앱 자체의 로그인은 그대로 적용 | 기본 24시간 후(`--funnel-ttl`) |
| **브라우저 게스트 링크** *(예정)* | 브라우저와 선택적 PIN | 링크를 가진 사람 | 링크 만료 또는 취소 시 |

```bash
tslink people add alice@example.com --apps photos --for 7d
tslink people list
tslink people remove alice@example.com
```

비공개 HTTP 및 파일 공유는 요청마다 기한을 확인합니다. 권한을 회수하면 새 요청이 차단되지만, 이미 다운로드한 데이터를 되돌리거나 수락된 스트림과 WebSocket 연결을 닫지는 못합니다. [사람별 공유 →](people.md) · [공유의 경계 →](sharing.md)

<a id="agents"></a>

## 에이전트용

TSLink에는 MCP 서버가 포함되어 있어 에이전트도 사용자처럼 공유하고, 목록을 보고, 공유 상태를 설명하고, 제거할 수 있습니다. 로컬 MCP 클라이언트에 다음을 추가하세요.

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

- **정확한 결과.** CLI 자동화는 `--json`, `schema_version: 1`, 안정적인 오류 코드를 지원하며, `tslink mcp`는 JSON-RPC를 사용합니다. `tslink manifest`는 모든 명령과 플래그를 설명합니다. 에이전트는 URL을 조합하지 말고 `tslink url <name> --wait`로 실제 URL을 가져와야 합니다.
- **대기 상태를 명확하게.** 새 노드에 사람의 로그인이 필요하면 준비된 것처럼 표시하지 않고 `needs_login`을 보고합니다.
- **권한.** 로컬 MCP는 사용자의 권한으로 실행됩니다. 원격 MCP는 명시적으로 켜야 하는 tailnet 전용 엔드포인트이며, 지정한 로그인 신원이나 태그만 허용합니다. 에이전트별 역할, 앱 범위 및 작업 영수증은 *예정*입니다.

TSLink의 MCP는 TSLink 자체를 조작합니다. TSLink를 통해 다른 MCP 서버를 공개하더라도 해당 서버에는 별도의 도구 권한 관리가 필요합니다.
[에이전트 안내 →](agents.md) · [MCP 클라이언트 →](mcp-clients.md) · [원격 MCP →](remote-mcp.md) · [JSON 자동화 →](json-automation.md)

## 다른 도구를 선택할 때

| 원하는 작업 | 고려할 도구 |
|---|---|
| 이미 사용하는 Tailscale 클라이언트로 내 기기에서 로컬 서비스 하나에 접근 | [`tailscale serve`](https://tailscale.com/docs/reference/tailscale-cli/serve) |
| 여러 호스트에서 고정된 이름을 쓰는 관리자 운영 서비스 | [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) |
| Tailscale 계정 없이 사용할 webhook 또는 API 데모의 공개 URL | [ngrok](https://ngrok.com/docs/start) 또는 [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) |
| 자체 호스팅 앱을 공유하는 데 더해 설치하고 실행 | [Umbrel](https://umbrel.com) 또는 [Coolify](https://coolify.io) |
| 조직 전체의 신원 기반 접근 플랫폼 | [Pangolin](https://github.com/fosrl/pangolin) 또는 [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/) |

한 사람이 여러 앱을 운영하면서 앱별, 사람별로 기한이 있는 접근 권한을 설정하고, 자신과 에이전트가 이를 확인하려는 경우 TSLink가 잘 맞습니다.

## 작동 방식

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/service-map-dark.svg">
  <img src="assets/service-map-light.svg" alt="App, Docs, Database, Model은 하나의 tailnet에 있는 별도로 이름 붙인 노드이며, 서비스를 공개하는 컴퓨터의 TSLink 데몬 하나가 실행합니다." width="720">
</picture>

백그라운드 데몬 하나가 앱마다 내장 Tailscale 노드를 실행하므로 각 앱에 고유한 이름과 주소가 생깁니다. 비공개 HTTP 및 파일 공유의 접근은 `WhoIs`와 사람별 권한 또는 `--allow` 규칙으로 제어하며, 사람별 기한은 요청마다 확인합니다. 원시 TCP는 tailnet 정책과 백엔드 인증을 사용합니다. Tailscale은 tailnet 전송, 암호화 및 인증서를 제공하며 TSLink는 독립 프로젝트입니다. 모든 앱은 공개용 컴퓨터를 함께 사용하므로 TSLink가 앱을 서로 격리하지는 않습니다. [아키텍처 →](architecture.md)

## 현재 상태

현재 제공: 앱별 비공개 주소, 기한과 초대 묶음을 갖춘 사람별 공유, 만료되는 공개 Funnel, 앱 상태 확인과 알림, 자체 호스팅 앱 레시피, 앱별 요청 제한, Windows 충돌 후 재시작, CLI 및 MCP.

예정: 브라우저 게스트 링크, 유연한 기간, 접근 로그, 앱 홈 화면, 범위가 제한된 에이전트 역할, QR 온보딩 및 접근 요청. 여러 컴퓨터를 한 목록에서 보는 기능은 계획 중입니다. [로드맵 →](roadmap.md)

## 문서와 라이선스

[llms.txt](../llms.txt) · [에이전트 빠른 시작](agent-quickstart.md) · [공유 도구 선택](comparison.md)

[시작 안내](getting-started.md) · [CLI 참조](cli-reference.md) · [플랫폼](platforms.md) · [로컬 모델](local-ai.md) · [기여](../CONTRIBUTING.md) · [보안](../SECURITY.md)

상업적 사용을 포함해 Apache License 2.0을 적용합니다. 재배포 시 [NOTICE](../NOTICE) 및 [타사 고지](../THIRD_PARTY_NOTICES.md)를 보존하세요. Tailscale의 약관과 요금제는 별도로 적용됩니다.
